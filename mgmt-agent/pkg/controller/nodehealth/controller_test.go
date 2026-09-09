// Copyright 2025 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package nodehealth

import (
	"context"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/record"
	"k8s.io/component-base/metrics/legacyregistry"

	"github.com/Azure/ARO-HCP/mgmt-agent/pkg/controller"
	"github.com/Azure/ARO-HCP/mgmt-agent/pkg/controller/nodehealth/detectors"
)

const (
	testHost = "aks-userswft1-0"
	sig      = "route ip+net: no such network interface"
	reason   = "FailedCreatePodSandBox"
)

// newTestController builds a controller wired to fake informers, with a fixed
// clock, and returns it plus the fake clientset and the three informers so a test
// can push objects straight into their stores and call syncHandler without
// running the workqueue. Seeded nodes are added so nodeLister can resolve them.
func newTestController(t *testing.T, enabled bool, nodes ...*corev1.Node) (*Controller, *fake.Clientset, informers.SharedInformerFactory) {
	t.Helper()
	rt := make([]runtime.Object, 0, len(nodes))
	for _, n := range nodes {
		rt = append(rt, n)
	}
	client := fake.NewSimpleClientset(rt...)
	factory := informers.NewSharedInformerFactory(client, 0)
	c, err := NewController(
		client,
		factory.Core().V1().Nodes(),
		factory.Core().V1().Pods(),
		factory.Core().V1().Events(),
		record.NewFakeRecorder(64),
		func() time.Time { return testNow },
		Config{Enabled: enabled},
	)
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	for _, n := range nodes {
		if err := factory.Core().V1().Nodes().Informer().GetStore().Add(n); err != nil {
			t.Fatalf("seed node: %v", err)
		}
	}
	return c, client, factory
}

func swiftReadyNode(name string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: map[string]string{detectors.SwiftV2LabelKey: detectors.SwiftV2LabelValue},
		},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
		},
	}
}

func stuckPodOn(host, name string, since time.Time) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: name, UID: types.UID("uid-" + name)},
		Spec:       corev1.PodSpec{NodeName: host},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			Conditions: []corev1.PodCondition{{
				Type:               corev1.PodReadyToStartContainers,
				Status:             corev1.ConditionFalse,
				LastTransitionTime: metav1.NewTime(since),
			}},
		},
	}
}

func startedPodOn(host, name string, at time.Time, hostNet bool) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: name, UID: types.UID("uid-" + name)},
		Spec:       corev1.PodSpec{NodeName: host, HostNetwork: hostNet},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{{
				Type:               corev1.PodReadyToStartContainers,
				Status:             corev1.ConditionTrue,
				LastTransitionTime: metav1.NewTime(at),
			}},
		},
	}
}

func failEventOn(host, pod string, last time.Time) *corev1.Event {
	return &corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{Namespace: "ns", Name: pod + ".evt"},
		InvolvedObject: corev1.ObjectReference{Kind: "Pod", Namespace: "ns", Name: pod, UID: types.UID("uid-" + pod)},
		Source:         corev1.EventSource{Host: host, Component: "kubelet"},
		Reason:         reason,
		Message:        "failed to setup network for sandbox: " + sig,
		LastTimestamp:  metav1.NewTime(last),
		FirstTimestamp: metav1.NewTime(last),
	}
}

// seedStuckStorm pushes n stuck pods and their matching failure Events into the
// informer stores for host, all stuck since the given time.
func seedStuckStorm(t *testing.T, c *Controller, host string, n int, since time.Time) {
	t.Helper()
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("p%d", i)
		if err := c.podIndexer.Add(stuckPodOn(host, name, since)); err != nil {
			t.Fatalf("add pod: %v", err)
		}
		if err := c.eventIndexer.Add(failEventOn(host, name, testNow.Add(-30*time.Second))); err != nil {
			t.Fatalf("add event: %v", err)
		}
	}
}

func isWedged(t *testing.T, client *fake.Clientset, name string) bool {
	t.Helper()
	return getNode(t, client, name).Labels[labelKey] == labelValue
}

// seedSuccess puts a pod that got a sandbox into the pod cache, which is the
// only place the success signal is read from. The pod asks for a delegated NIC,
// because the SWIFT detector only counts a start as success if the pod actually
// traversed the path it watches.
func seedSuccess(t *testing.T, c *Controller, host, name string, ts time.Time) {
	t.Helper()
	pod := startedPodOn(host, name, ts, false)
	pod.Spec.Containers = []corev1.Container{{
		Name: "nic",
		Resources: corev1.ResourceRequirements{
			Limits: corev1.ResourceList{controller.SwiftNICResourceName: resource.MustParse("1")},
		},
	}}
	if err := c.podIndexer.Add(pod); err != nil {
		t.Fatalf("add pod: %v", err)
	}
}

func TestSyncHandlerWedgesOnSustainedStorm(t *testing.T) {
	node := swiftReadyNode(testHost)
	c, client, _ := newTestController(t, true, node)
	seedStuckStorm(t, c, testHost, 3, testNow.Add(-15*time.Minute))

	if err := c.syncHandler(context.Background(), testHost); err != nil {
		t.Fatalf("syncHandler: %v", err)
	}
	if !isWedged(t, client, testHost) {
		t.Fatal("node should be labeled wedged after a sustained storm")
	}
}

func TestSyncHandlerDisabledIsNoop(t *testing.T) {
	node := swiftReadyNode(testHost)
	c, client, _ := newTestController(t, false, node)
	seedStuckStorm(t, c, testHost, 3, testNow.Add(-15*time.Minute))

	if err := c.syncHandler(context.Background(), testHost); err != nil {
		t.Fatalf("syncHandler: %v", err)
	}
	if isWedged(t, client, testHost) {
		t.Fatal("a disabled controller must never label a node")
	}
}

// TestFreshControllerWedgesWithoutWarmup is the regression test for the cache
// contract: the controller keeps nothing between reconciles, so a process that
// has just started decides from the LIST alone. There is no warm-up window in
// which a genuinely wedged node goes unreported.
func TestFreshControllerWedgesWithoutWarmup(t *testing.T) {
	node := swiftReadyNode(testHost)
	c, client, _ := newTestController(t, true, node)
	seedStuckStorm(t, c, testHost, 3, testNow.Add(-15*time.Minute))

	if err := c.syncHandler(context.Background(), testHost); err != nil {
		t.Fatalf("syncHandler: %v", err)
	}
	if !isWedged(t, client, testHost) {
		t.Fatal("a freshly started controller must wedge straight off the LIST, with no warm-up")
	}
}

func TestSyncHandlerRecoveryUnlabels(t *testing.T) {
	// Node already carries the wedged label; a pod that got a sandbox and no
	// stuck pods must clear it.
	node := swiftReadyNode(testHost)
	node.Labels[labelKey] = labelValue
	c, client, _ := newTestController(t, true, node)
	seedSuccess(t, c, testHost, "ok", testNow.Add(-1*time.Minute))

	if err := c.syncHandler(context.Background(), testHost); err != nil {
		t.Fatalf("syncHandler: %v", err)
	}
	if isWedged(t, client, testHost) {
		t.Fatal("a success with no stuck pods should unlabel the node")
	}
}

// TestSuccessInListPreventsFalseWedge is the AROSLSRE-1643 shape: a node under a
// real sandbox-failure storm that is nevertheless still starting pods. The
// successes sit in the pod cache alongside the stuck pods, so the storm alone is
// not enough to call it wedged.
func TestSuccessInListPreventsFalseWedge(t *testing.T) {
	node := swiftReadyNode(testHost)
	c, client, _ := newTestController(t, true, node)

	seedStuckStorm(t, c, testHost, 3, testNow.Add(-15*time.Minute))
	seedSuccess(t, c, testHost, "started", testNow.Add(-2*time.Minute))

	if err := c.syncHandler(context.Background(), testHost); err != nil {
		t.Fatalf("syncHandler: %v", err)
	}
	if isWedged(t, client, testHost) {
		t.Fatal("a node still starting pods must not be labeled wedged")
	}
}

// TestRestartReachesTheSameVerdict pins the property the in-memory success map
// could not give: two controllers looking at the same LIST agree, whatever
// either of them watched happen. A replacement process is handed the caches a
// long-running one had and must land on the same answer with no history and no
// warm-up.
func TestRestartReachesTheSameVerdict(t *testing.T) {
	for _, tc := range []struct {
		name       string
		success    bool
		wantWedged bool
	}{
		{name: "storm with no success is wedged", success: false, wantWedged: true},
		{name: "storm with a success is not", success: true, wantWedged: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := swiftReadyNode(testHost)
			before, beforeClient, _ := newTestController(t, true, node)
			seedStuckStorm(t, before, testHost, 3, testNow.Add(-15*time.Minute))
			if tc.success {
				seedSuccess(t, before, testHost, "started", testNow.Add(-2*time.Minute))
			}
			if err := before.syncHandler(context.Background(), testHost); err != nil {
				t.Fatalf("syncHandler before restart: %v", err)
			}
			if got := isWedged(t, beforeClient, testHost); got != tc.wantWedged {
				t.Fatalf("wedged before restart = %v, want %v", got, tc.wantWedged)
			}

			// The replacement process: brand new controller, nothing remembered,
			// same evidence.
			after, afterClient, _ := newTestController(t, true, node)
			seedStuckStorm(t, after, testHost, 3, testNow.Add(-15*time.Minute))
			if tc.success {
				seedSuccess(t, after, testHost, "started", testNow.Add(-2*time.Minute))
			}
			if err := after.syncHandler(context.Background(), testHost); err != nil {
				t.Fatalf("syncHandler after restart: %v", err)
			}
			if got := isWedged(t, afterClient, testHost); got != tc.wantWedged {
				t.Errorf("wedged after restart = %v, want %v (must match the pre-restart verdict)", got, tc.wantWedged)
			}
		})
	}
}

// TestHotEnableDecidesImmediately covers the disabled->enabled flip. It used to
// restart a warm-up; now there is nothing to warm up, so the first reconcile
// after the flip acts on the evidence already in cache.
func TestHotEnableDecidesImmediately(t *testing.T) {
	node := swiftReadyNode(testHost)
	c, client, _ := newTestController(t, false, node)
	seedStuckStorm(t, c, testHost, 3, testNow.Add(-15*time.Minute))

	if err := c.syncHandler(context.Background(), testHost); err != nil {
		t.Fatalf("syncHandler while disabled: %v", err)
	}
	if isWedged(t, client, testHost) {
		t.Fatal("precondition: a disabled controller must not label")
	}

	c.OnConfigMap(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "node-health-config"},
		Data:       map[string]string{"config.yaml": "enabled: true"},
	}, "config.yaml")

	if err := c.syncHandler(context.Background(), testHost); err != nil {
		t.Fatalf("syncHandler after enable: %v", err)
	}
	if !isWedged(t, client, testHost) {
		t.Error("the first reconcile after a hot enable must act on the evidence already in cache")
	}
}

// TestNonSuccessPodsDoNotSuppressAWedge pins the two pod shapes that look like a
// success but are not. Host-network pods reach the condition without a delegated
// NIC, which is exactly what a SWIFT wedge cannot provide, and a stuck pod is the
// symptom rather than the cure. Counting either would mask a real wedge, and the
// host-network case matters most: SWIFT mitigation restarts host-network pods.
func TestNonSuccessPodsDoNotSuppressAWedge(t *testing.T) {
	node := swiftReadyNode(testHost)
	c, client, _ := newTestController(t, true, node)
	seedStuckStorm(t, c, testHost, 3, testNow.Add(-15*time.Minute))

	if err := c.podIndexer.Add(startedPodOn(testHost, "hostnet", testNow.Add(-1*time.Minute), true)); err != nil {
		t.Fatalf("add pod: %v", err)
	}
	if err := c.podIndexer.Add(stuckPodOn(testHost, "another-stuck", testNow.Add(-1*time.Minute))); err != nil {
		t.Fatalf("add pod: %v", err)
	}

	if err := c.syncHandler(context.Background(), testHost); err != nil {
		t.Fatalf("syncHandler: %v", err)
	}
	if !isWedged(t, client, testHost) {
		t.Error("neither a host-network pod nor a stuck pod is a success; the wedge must still fire")
	}
}

// TestOutOfWindowSuccessDoesNotSuppressAWedge covers both ends of the window.
// The captured production node was still running 46 started pods whose freshest
// transition was over 8 hours old, so "has ever started a pod" is always true on
// a long-lived node. Future-dated timestamps come from kubelet clock skew and are
// not evidence the node can attach a NIC now either.
func TestOutOfWindowSuccessDoesNotSuppressAWedge(t *testing.T) {
	for _, tc := range []struct {
		name string
		at   time.Time
	}{
		{name: "long stale", at: testNow.Add(-8 * time.Hour)},
		{name: "future dated by kubelet skew", at: testNow.Add(1 * time.Hour)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := swiftReadyNode(testHost)
			c, client, _ := newTestController(t, true, node)
			seedStuckStorm(t, c, testHost, 3, testNow.Add(-15*time.Minute))
			seedSuccess(t, c, testHost, "out-of-window", tc.at)

			if err := c.syncHandler(context.Background(), testHost); err != nil {
				t.Fatalf("syncHandler: %v", err)
			}
			if !isWedged(t, client, testHost) {
				t.Error("an out-of-window success must not suppress detection")
			}
		})
	}
}

func TestPodByNodeIndexFunc(t *testing.T) {
	keys, err := podByNodeIndexFunc(stuckPodOn(testHost, "p0", testNow))
	if err != nil {
		t.Fatalf("index func: %v", err)
	}
	if len(keys) != 1 || keys[0] != testHost {
		t.Errorf("podByNodeIndexFunc = %v, want [%q]", keys, testHost)
	}
	// A pod not yet scheduled has no node key.
	unscheduled := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "x"}}
	if keys, _ := podByNodeIndexFunc(unscheduled); len(keys) != 0 {
		t.Errorf("unscheduled pod should index to no node, got %v", keys)
	}
}

func TestEventBySourceHostIndexFunc(t *testing.T) {
	keys, err := eventBySourceHostIndexFunc(failEventOn(testHost, "p0", testNow))
	if err != nil {
		t.Fatalf("index func: %v", err)
	}
	if len(keys) != 1 || keys[0] != testHost {
		t.Errorf("eventBySourceHostIndexFunc = %v, want [%q]", keys, testHost)
	}
	// An Event with no Source.Host indexes to nothing (keeps the lookup deletion-safe).
	noHost := &corev1.Event{ObjectMeta: metav1.ObjectMeta{Name: "e"}}
	if keys, _ := eventBySourceHostIndexFunc(noHost); len(keys) != 0 {
		t.Errorf("hostless event should index to no node, got %v", keys)
	}
}

func TestEnqueueGatedByEnabled(t *testing.T) {
	node := swiftReadyNode(testHost)
	c, _, _ := newTestController(t, false, node)
	c.enqueue(testHost)
	if c.workqueue.Len() != 0 {
		t.Fatal("a disabled controller must not enqueue work")
	}
	c.SetConfig(Config{Enabled: true})
	c.enqueue(testHost)
	if c.workqueue.Len() != 1 {
		t.Fatalf("an enabled controller should enqueue, got queue len %d", c.workqueue.Len())
	}
}

func TestGatherReadsPodsAndEventsForNode(t *testing.T) {
	node := swiftReadyNode(testHost)
	c, _, _ := newTestController(t, true, node)
	seedStuckStorm(t, c, testHost, 2, testNow.Add(-15*time.Minute))
	// Noise on another node must not leak into this node's gather.
	if err := c.podIndexer.Add(stuckPodOn("other-node", "q0", testNow)); err != nil {
		t.Fatalf("add pod: %v", err)
	}

	events, pods, err := c.gather(testHost)
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	if len(pods) != 2 {
		t.Errorf("gather pods = %d, want 2 (other node excluded)", len(pods))
	}
	if len(events) != 2 {
		t.Errorf("gather events = %d, want 2", len(events))
	}
}

func TestCountWedgedNodesIncludesNonSwiftNodes(t *testing.T) {
	// A node that carries the wedged label but has lost (or never had) the SWIFT
	// label must still be counted. Scoping the count to the SWIFT selector used
	// to hide exactly the stale labels an operator most needs to see.
	wedgedSwift := swiftReadyNode("swift-wedged")
	wedgedSwift.Labels[labelKey] = labelValue

	healthySwift := swiftReadyNode("swift-healthy")

	wedgedNonSwift := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name:   "plain-wedged",
		Labels: map[string]string{labelKey: labelValue},
	}}

	foreignValue := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name:   "plain-foreign",
		Labels: map[string]string{labelKey: "somethingelse"},
	}}

	c, _, _ := newTestController(t, true, wedgedSwift, healthySwift, wedgedNonSwift, foreignValue)

	got, err := c.countWedgedNodes()
	if err != nil {
		t.Fatalf("countWedgedNodes: %v", err)
	}
	if want := 2; got != want {
		t.Errorf("countWedgedNodes() = %d, want %d (both wedged nodes, SWIFT-labeled or not)", got, want)
	}
}

func TestSyncHandlerRetiresLabelWhenNoDetectorApplies(t *testing.T) {
	// A node that carries our label but is no longer a detection candidate (it
	// lost the SWIFT label) must have the label retired, not held forever.
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "n1",
			Labels: map[string]string{labelKey: labelValue},
			Annotations: map[string]string{
				annotationDetector: "swift-vf-teardown",
				annotationReason:   "x",
			},
		},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
		},
	}
	c, client, _ := newTestController(t, true, node)

	if err := c.syncHandler(context.Background(), "n1"); err != nil {
		t.Fatalf("syncHandler: %v", err)
	}

	got := getNode(t, client, "n1")
	if _, ok := got.Labels[labelKey]; ok {
		t.Error("stale wedged label should be retired when no detector applies")
	}
	if _, ok := got.Annotations[annotationDetector]; ok {
		t.Error("detection record should be cleared with the label")
	}
}

func TestSyncHandlerRetiresLabelOnNotReadyNonCandidate(t *testing.T) {
	// Ownership is decided before readiness: a NotReady node that no detector
	// applies to is still not ours to hold a label on.
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "n1",
			Labels: map[string]string{labelKey: labelValue},
		},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionFalse}},
		},
	}
	c, client, _ := newTestController(t, true, node)

	if err := c.syncHandler(context.Background(), "n1"); err != nil {
		t.Fatalf("syncHandler: %v", err)
	}
	if _, ok := getNode(t, client, "n1").Labels[labelKey]; ok {
		t.Error("stale label on a non-candidate node should be retired regardless of readiness")
	}
}

func TestSyncHandlerNotApplicablePreservesForeignLabelValue(t *testing.T) {
	// Our key with someone else's value is not ours to remove.
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "n1",
			Labels: map[string]string{labelKey: "somethingelse"},
		},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
		},
	}
	c, client, _ := newTestController(t, true, node)

	if err := c.syncHandler(context.Background(), "n1"); err != nil {
		t.Fatalf("syncHandler: %v", err)
	}
	if got := getNode(t, client, "n1").Labels[labelKey]; got != "somethingelse" {
		t.Errorf("foreign label value = %q, want it untouched", got)
	}
}

func TestSyncHandlerNotReadySwiftNodeRetainsLabel(t *testing.T) {
	// A NotReady node that IS a detection candidate is deferred to node
	// lifecycle: the label must be retained, not retired.
	node := swiftReadyNode("n1")
	node.Labels[labelKey] = labelValue
	node.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionFalse}}
	c, client, _ := newTestController(t, true, node)

	if err := c.syncHandler(context.Background(), "n1"); err != nil {
		t.Fatalf("syncHandler: %v", err)
	}
	if getNode(t, client, "n1").Labels[labelKey] != labelValue {
		t.Error("a NotReady detection candidate should retain its label")
	}
}

func TestRestartRetainsExistingWedgedLabel(t *testing.T) {
	// The restart view is asymmetric on purpose: a node that still carries the
	// label is only unlabeled on positive evidence of recovery, never because
	// the Event view is briefly empty just after startup.
	node := swiftReadyNode("n1")
	node.Labels[labelKey] = labelValue
	node.Annotations = map[string]string{annotationDetector: "swift-vf-teardown"}

	c, client, _ := newTestController(t, true, node)
	// A freshly started controller with no Events or Pods in cache.

	if err := c.syncHandler(context.Background(), "n1"); err != nil {
		t.Fatalf("syncHandler: %v", err)
	}
	if getNode(t, client, "n1").Labels[labelKey] != labelValue {
		t.Error("a cold restart must retain an existing wedged label, not clear it")
	}
}

func TestOnConfigMapDeletedRevertsToDisabled(t *testing.T) {
	c, _, _ := newTestController(t, true)
	if !c.Config().Enabled {
		t.Fatal("precondition: controller should start enabled")
	}

	c.OnConfigMapDeleted()

	if c.Config().Enabled {
		t.Error("deleting the ConfigMap must revert to the disabled built-in default")
	}
}

func TestOnConfigMapInvalidYAMLKeepsPreviousConfig(t *testing.T) {
	c, _, _ := newTestController(t, true)

	c.OnConfigMap(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "cm"},
		Data:       map[string]string{"config.yaml": "enabled: [not a bool"},
	}, "config.yaml")

	if !c.Config().Enabled {
		t.Error("an unparseable config must be rejected and the previous config kept")
	}
}

func TestOnConfigMapMissingKeyKeepsPreviousConfig(t *testing.T) {
	c, _, _ := newTestController(t, true)

	c.OnConfigMap(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "cm"},
		Data:       map[string]string{"other.yaml": "enabled: false"},
	}, "config.yaml")

	if !c.Config().Enabled {
		t.Error("a ConfigMap without the expected key must keep the previous config")
	}
}

func TestOnConfigMapHotDisableTakesEffect(t *testing.T) {
	c, _, _ := newTestController(t, true)

	c.OnConfigMap(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "cm"},
		Data:       map[string]string{"config.yaml": "enabled: false"},
	}, "config.yaml")

	if c.Config().Enabled {
		t.Fatal("hot disable should take effect")
	}
	// A disabled controller must take no action even if a node is enqueued.
	if err := c.syncHandler(context.Background(), "n1"); err != nil {
		t.Fatalf("syncHandler while disabled: %v", err)
	}
}

// nodeWedgedSeries returns the per-node wedged gauge as a map keyed by the
// "node|detector|signature" label tuple, so a test can assert on the exact
// series set the vector currently exposes.
func nodeWedgedSeries(t *testing.T) map[string]float64 {
	t.Helper()
	families, err := legacyregistry.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	out := map[string]float64{}
	for _, f := range families {
		if f.GetName() != "nodehealth_node_wedged" {
			continue
		}
		for _, m := range f.GetMetric() {
			labels := map[string]string{}
			for _, l := range m.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}
			key := labels["node"] + "|" + labels["detector"] + "|" + labels["signature"]
			out[key] = m.GetGauge().GetValue()
		}
	}
	return out
}

// The per-node gauge is rebuilt from the live list of labeled nodes on every
// resync rather than mutated per transition, so a node that recovers, loses the
// label out of band, or is deleted drops out of the series set on the next sweep
// instead of leaking a series for the process lifetime. That property is the
// whole reason the node identity lives on this gauge and not on
// nodehealth_detections_total, where the series could never retire.
func TestResyncAllPublishesAndRetiresPerNodeWedgedSeries(t *testing.T) {
	RegisterMetrics()
	nodeWedged.Reset()
	t.Cleanup(nodeWedged.Reset)

	wedged := swiftReadyNode("swift-wedged")
	wedged.Labels[labelKey] = labelValue
	wedged.Annotations = map[string]string{
		annotationDetector:  "swift-vf",
		annotationSignature: sig,
	}
	healthy := swiftReadyNode("swift-healthy")

	c, _, factory := newTestController(t, true, wedged, healthy)

	c.resyncAll(context.Background())
	got := nodeWedgedSeries(t)
	want := map[string]float64{"swift-wedged|swift-vf|" + sig: 1}
	if len(got) != len(want) {
		t.Fatalf("series set = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("series %q = %v, want %v", k, got[k], v)
		}
	}

	// The node recovers: the label goes away, and so must its series.
	recovered := wedged.DeepCopy()
	delete(recovered.Labels, labelKey)
	if err := factory.Core().V1().Nodes().Informer().GetStore().Update(recovered); err != nil {
		t.Fatalf("update node: %v", err)
	}
	c.resyncAll(context.Background())
	if got := nodeWedgedSeries(t); len(got) != 0 {
		t.Errorf("series set after recovery = %v, want empty", got)
	}
}

// A disabled controller stops reconciling, so leaving the per-node series
// published would pin an alert on a node nothing is evaluating any more.
func TestResyncAllDisabledClearsPerNodeWedgedSeries(t *testing.T) {
	RegisterMetrics()
	nodeWedged.Reset()
	t.Cleanup(nodeWedged.Reset)

	wedged := swiftReadyNode("swift-wedged")
	wedged.Labels[labelKey] = labelValue
	wedged.Annotations = map[string]string{
		annotationDetector:  "swift-vf",
		annotationSignature: sig,
	}

	c, _, _ := newTestController(t, true, wedged)
	c.resyncAll(context.Background())
	if got := nodeWedgedSeries(t); len(got) != 1 {
		t.Fatalf("series set while enabled = %v, want one entry", got)
	}

	c.SetConfig(Config{Enabled: false})
	c.resyncAll(context.Background())
	if got := nodeWedgedSeries(t); len(got) != 0 {
		t.Errorf("series set while disabled = %v, want empty", got)
	}
}
