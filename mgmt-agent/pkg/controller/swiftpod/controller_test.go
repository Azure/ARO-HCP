// Copyright 2026 Microsoft Corporation
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

package swiftpod

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-logr/logr/funcr"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	nodev1 "k8s.io/api/node/v1"
	policyv1 "k8s.io/api/policy/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	ktesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/internal/kuberesources"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/mgmt-agent/pkg/controller/nodehealth/detectors"
)

type fixture struct {
	c    *Controller
	kube *fake.Clientset
	cfg  Config
	pod  *corev1.Pod
	node *corev1.Node
	now  time.Time
}

func TestDelegatedNICSnapshotRoute(t *testing.T) {
	f := newFixture(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/apis/multitenancy.acn.azure.com/v1alpha1/multitenantpodnetworkconfigs" {
			http.NotFound(w, r)
			return
		}
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(`{"apiVersion":"multitenancy.acn.azure.com/v1alpha1","kind":"MultitenantPodNetworkConfigList","items":[
{"apiVersion":"multitenancy.acn.azure.com/v1alpha1","kind":"MultitenantPodNetworkConfig","metadata":{"name":"orphan"},
"spec":{"podUID":"deleted-pod"},"status":{"nodeName":"a","interfaceInfos":[{},{}]}}]}`))
		if err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	dyn, err := dynamic.NewForConfig(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	f.c.dynamic = dyn
	snapshot, err := f.c.snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 || snapshot.NICs["a"]["deleted-pod"] != 2 {
		t.Fatalf("expected exact resource route and orphan NIC accounting, got %d requests, %v", requests.Load(), snapshot.NICs)
	}
}

func TestSnapshotNodeEvidence(t *testing.T) {
	f := newFixture(t)
	event, err := f.kube.CoreV1().Events(f.pod.Namespace).Get(t.Context(), "sandbox", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i, node := range []string{"b", "missing", ""} {
		pod := f.pod.DeepCopy()
		pod.Name = fmt.Sprintf("stalled-%d", i)
		pod.UID += types.UID(pod.Name)
		pod.Spec.NodeName = node
		if _, err := f.kube.CoreV1().Pods(pod.Namespace).Create(t.Context(), pod, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
		evidence := event.DeepCopy()
		evidence.Name, evidence.Source.Host = pod.Name, node
		evidence.InvolvedObject.Name, evidence.InvolvedObject.UID = pod.Name, pod.UID
		if _, err := f.kube.CoreV1().Events(pod.Namespace).Create(t.Context(), evidence, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	wrongHost := event.DeepCopy()
	wrongHost.Name, wrongHost.Source.Host = "wrong-host", "c"
	if _, err := f.kube.CoreV1().Events(f.pod.Namespace).Create(t.Context(), wrongHost, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	f.kube.ClearActions()
	snapshot, err := f.c.snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Pods) != 6 || len(snapshot.Events) != 5 || len(snapshot.Nodes) != 3 {
		t.Fatal("snapshot lost unscheduled, unknown-node or mismatched-host inputs")
	}
	if len(snapshot.Stalled) != 2 || len(snapshot.Stalled["a"]) != 1 || snapshot.Stalled["a"][0] != f.pod.UID ||
		len(snapshot.Stalled["b"]) != 1 || snapshot.Stalled["b"][0] != f.pod.UID+"stalled-0" {
		t.Fatalf("incorrect per-node stalled Pods: %v", snapshot.Stalled)
	}
	for _, node := range snapshot.Nodes {
		var pods []*corev1.Pod
		var events []*corev1.Event
		for _, pod := range snapshot.Pods {
			if pod.Spec.NodeName == node.Name {
				pods = append(pods, pod)
			}
		}
		for i := range snapshot.Events {
			if snapshot.Events[i].Source.Host == node.Name {
				events = append(events, &snapshot.Events[i])
			}
		}
		decision, _ := detectors.Decide(node, events, pods, f.now)
		if snapshot.Faulted[node.Name] != (decision == detectors.DecisionWedged) {
			t.Fatalf("node %s fault decision differs from its live evidence", node.Name)
		}
	}
	if len(mutations(f.kube.Actions())) != 0 {
		t.Fatal("snapshot mutated Kubernetes")
	}
}

func TestWorkerLogFields(t *testing.T) {
	f := newFixture(t)
	f.cfg.Mode = Audit
	if err := f.c.SetConfig(f.cfg); err != nil {
		t.Fatal(err)
	}
	f.c.synced = []cache.InformerSynced{func() bool { return true }}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var entries []string
	logger := funcr.NewJSON(func(entry string) {
		entries = append(entries, entry)
		cancel()
	}, funcr.Options{}).WithValues("component", "mgmt-agent")
	if err := f.c.Run(utils.ContextWithLogger(ctx, logger)); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range entries {
		var fields map[string]any
		if err := json.Unmarshal([]byte(entry), &fields); err != nil {
			t.Fatal(err)
		}
		if fields["msg"] != "SWIFT router candidate eligible" {
			continue
		}
		found = true
		for key, want := range map[string]string{
			"controller_name": ControllerName, "component": "mgmt-agent",
			"namespace": f.pod.Namespace, "pod": f.pod.Name,
		} {
			if fields[key] != want {
				t.Errorf("log field %s = %v, want %s", key, fields[key], want)
			}
		}
		if _, exists := fields["controller"]; exists {
			t.Error("log contains nonstandard controller field")
		}
	}
	if !found {
		t.Fatalf("worker did not log audit eligibility: %v", entries)
	}
}

func TestWorkerRetryBoundAndShutdown(t *testing.T) {
	f := newFixture(t)
	f.cfg.Mode = Audit
	f.cfg.RetryInterval.Duration = 30 * time.Millisecond
	if err := f.c.SetConfig(f.cfg); err != nil {
		t.Fatal(err)
	}
	// Freeze observation time while the real delayed queue releases notifications.
	// Repeated informer events must not bypass the configured per-Pod interval.
	f.c.synced = []cache.InformerSynced{func() bool { return true }}
	var snapshots atomic.Int32
	f.kube.PrependReactor("list", "nodes", func(ktesting.Action) (bool, runtime.Object, error) {
		snapshots.Add(1)
		return false, nil, nil
	})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- f.c.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("worker did not shut down")
		}
	})
	deadline := time.After(5 * time.Second)
	for snapshots.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("worker did not reconcile")
		case <-time.After(time.Millisecond):
		}
	}
	for range 100 {
		f.c.enqueue(f.pod)
	}
	time.Sleep(100 * time.Millisecond)
	if snapshots.Load() != 1 {
		t.Fatalf("notifications bypassed retry interval: %d snapshots", snapshots.Load())
	}
	f.c.OnConfigMapDeleted()
	if len(mutations(f.kube.Actions())) != 0 {
		t.Fatal("audit worker mutated Kubernetes")
	}
}

func TestWorkloadMinReadySeconds(t *testing.T) {
	for _, tc := range []struct {
		name       string
		minReady   int32
		readySince time.Duration
		missing    bool
		unhealthy  bool
		allowed    bool
	}{
		{name: "zero permits missing timestamp", missing: true, allowed: true},
		{name: "missing timestamp", minReady: 60, missing: true},
		{name: "future timestamp", minReady: 60, readySince: time.Second},
		{name: "newly ready", minReady: 60, readySince: -10 * time.Second},
		{name: "at boundary", minReady: 60, readySince: -time.Minute},
		{name: "past boundary", minReady: 60, readySince: -time.Minute - time.Nanosecond, allowed: true},
		{name: "mature", minReady: 60, readySince: -2 * time.Minute, allowed: true},
		{name: "disjoint healthy and available", minReady: 60, readySince: -10 * time.Second, unhealthy: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			deployment, err := f.kube.AppsV1().Deployments(f.pod.Namespace).Get(t.Context(), "router", metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			deployment.Spec.MinReadySeconds = tc.minReady
			if tc.unhealthy {
				f.cfg.Workload.MinAvailableReplicas = ptr.To(int32(1))
				deployment.Status.AvailableReplicas = 1
			}
			if _, err := f.kube.AppsV1().Deployments(f.pod.Namespace).Update(t.Context(), deployment, metav1.UpdateOptions{}); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"router-b", "router-c"} {
				pod, err := f.kube.CoreV1().Pods(f.pod.Namespace).Get(t.Context(), name, metav1.GetOptions{})
				if err != nil {
					t.Fatal(err)
				}
				pod.Status.Conditions[0].LastTransitionTime = metav1.NewTime(f.now.Add(-2 * time.Minute))
				if name == "router-b" {
					pod.Status.Conditions[0].LastTransitionTime = metav1.NewTime(f.now.Add(tc.readySince))
					if tc.missing {
						pod.Status.Conditions[0].LastTransitionTime = metav1.Time{}
					}
				}
				if _, err := f.kube.CoreV1().Pods(pod.Namespace).UpdateStatus(t.Context(), pod, metav1.UpdateOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			snapshot, err := f.c.snapshot(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if tc.unhealthy {
				for _, node := range snapshot.Nodes {
					if node.Name == "c" {
						node.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionFalse}}
					}
				}
			}
			f.kube.ClearActions()
			_, err = f.c.availableWorkload(t.Context(), f.pod, f.cfg, snapshot, nil)
			if tc.allowed && err != nil {
				t.Fatal(err)
			}
			if !tc.allowed && (err == nil || !strings.Contains(err.Error(), "below floor")) {
				t.Fatalf("expected per-Pod availability hold, got %v", err)
			}
			if len(mutations(f.kube.Actions())) != 0 {
				t.Fatal("availability check mutated Kubernetes")
			}
		})
	}
}

func TestReadinessResetHoldsEviction(t *testing.T) {
	for _, stage := range []string{"audit", "before claim", "after claim"} {
		t.Run(stage, func(t *testing.T) {
			f := newFixture(t)
			if stage == "audit" {
				f.cfg.Mode = Audit
				if err := f.c.SetConfig(f.cfg); err != nil {
					t.Fatal(err)
				}
			}
			deployment, err := f.kube.AppsV1().Deployments(f.pod.Namespace).Get(t.Context(), "router", metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			deployment.Spec.MinReadySeconds = 60
			if _, err := f.kube.AppsV1().Deployments(f.pod.Namespace).Update(t.Context(), deployment, metav1.UpdateOptions{}); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"router-b", "router-c"} {
				pod, err := f.kube.CoreV1().Pods(f.pod.Namespace).Get(t.Context(), name, metav1.GetOptions{})
				if err != nil {
					t.Fatal(err)
				}
				pod.Status.Conditions[0].LastTransitionTime = metav1.NewTime(f.now.Add(-2 * time.Minute))
				if _, err := f.kube.CoreV1().Pods(pod.Namespace).UpdateStatus(t.Context(), pod, metav1.UpdateOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			resetReadiness := func() {
				gvr := corev1.SchemeGroupVersion.WithResource("pods")
				obj, err := f.kube.Tracker().Get(gvr, f.pod.Namespace, "router-b")
				if err != nil {
					t.Fatal(err)
				}
				pod := obj.(*corev1.Pod).DeepCopy()
				pod.Status.Conditions[0].LastTransitionTime = metav1.NewTime(f.now.Add(-time.Second))
				if err := f.kube.Tracker().Update(gvr, pod, pod.Namespace); err != nil {
					t.Fatal(err)
				}
			}
			if stage == "after claim" {
				f.kube.PrependReactor("patch", "pods", func(ktesting.Action) (bool, runtime.Object, error) {
					resetReadiness()
					return false, nil, nil
				})
			} else {
				resetReadiness()
			}
			f.kube.ClearActions()
			if err := f.run(); err == nil || !strings.Contains(err.Error(), "below floor") {
				t.Fatalf("expected readiness reset to hold eviction, got %v", err)
			}
			actions := mutations(f.kube.Actions())
			if stage == "after claim" {
				if len(actions) != 1 || actions[0].GetVerb() != "patch" || actions[0].GetResource().Resource != "pods" {
					t.Fatalf("expected only the ownership claim, got %v", actions)
				}
			} else if len(actions) != 0 {
				t.Fatalf("availability hold caused mutations: %v", actions)
			}
		})
	}
}

func TestRestartPreservesAccounting(t *testing.T) {
	f := newFixture(t)
	if err := f.run(); err != nil {
		t.Fatal(err)
	}
	restarted := newFixture(t)
	restarted.c.kube = f.kube
	f.kube.ClearActions()
	if err := restarted.run(); err == nil {
		t.Fatal("restart erased eviction cooldown")
	}
	if len(mutations(f.kube.Actions())) != 0 {
		t.Fatal("restart wrote despite consumed allowance")
	}
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	selector := metav1.LabelSelector{MatchLabels: map[string]string{"app": "private-router"}}
	managed := metav1.LabelSelector{MatchLabels: map[string]string{"hypershift.openshift.io/managed-by": "control-plane-operator"}}
	cfg := Config{Mode: Enforce, RetryInterval: metav1.Duration{Duration: 30 * time.Second},
		ObservationMaxAge: metav1.Duration{Duration: time.Minute}, EvictionWindow: metav1.Duration{Duration: time.Hour},
		EvictionCooldown: metav1.Duration{Duration: time.Minute}, MaxEvictionsPerWorkload: 2, MaxEvictionsPerNode: 3,
		Workload: WorkloadPolicy{NamespaceSelector: selector, PodSelector: selector, DeploymentSelector: managed,
			MinAvailableReplicas: ptr.To(int32(2)), AllowEmptyDir: true}}
	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "router", Namespace: "test", UID: "deployment", Generation: 1, Labels: managed.MatchLabels},
		Spec:   appsv1.DeploymentSpec{Replicas: ptr.To(int32(3)), Selector: &selector},
		Status: appsv1.DeploymentStatus{ObservedGeneration: 1, Replicas: 3, UpdatedReplicas: 3, AvailableReplicas: 2}}
	rs := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Name: "router-rs", Namespace: "test", UID: "replicaset",
		OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(deployment, appsv1.SchemeGroupVersion.WithKind("Deployment"))}},
		Spec: appsv1.ReplicaSetSpec{Replicas: ptr.To(int32(3)), Selector: &selector}}
	pod := placementPod("router-stalled", "a")
	pod.Labels = selector.MatchLabels
	pod.CreationTimestamp, pod.ResourceVersion = metav1.NewTime(now.Add(-5*time.Minute)), "1"
	pod.OwnerReferences = []metav1.OwnerReference{*metav1.NewControllerRef(rs, appsv1.SchemeGroupVersion.WithKind("ReplicaSet"))}
	pod.Status = corev1.PodStatus{Phase: corev1.PodPending, Conditions: []corev1.PodCondition{
		{Type: corev1.PodScheduled, Status: corev1.ConditionTrue, LastTransitionTime: metav1.NewTime(now.Add(-2 * time.Minute))},
		{Type: corev1.PodReadyToStartContainers, Status: corev1.ConditionFalse, LastTransitionTime: metav1.NewTime(now.Add(-2 * time.Minute))},
	}}
	// HyperShift router AROSwift at 2186ab7346f05120a1cbabd1d63c4c38751bd420:
	// disposable tmp-dir plus required hostname and zone anti-affinity.
	pod.Spec.Volumes = []corev1.Volume{{Name: "config", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{}}},
		{Name: "tmp-dir", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}}
	pod.Spec.Affinity = &corev1.Affinity{PodAntiAffinity: &corev1.PodAntiAffinity{}}
	for _, key := range []string{corev1.LabelTopologyZone, corev1.LabelHostname} {
		pod.Spec.Affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution = append(
			pod.Spec.Affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution,
			corev1.PodAffinityTerm{LabelSelector: &selector, TopologyKey: key})
	}
	rs.Spec.Template = corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{Labels: pod.Labels},
		Spec:       *pod.Spec.DeepCopy(),
	}
	rs.Spec.Template.Spec.NodeName = ""
	deployment.Spec.Template = *rs.Spec.Template.DeepCopy()
	event := &corev1.Event{ObjectMeta: metav1.ObjectMeta{Name: "sandbox", Namespace: "test"},
		InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: pod.Name, Namespace: pod.Namespace, UID: pod.UID},
		Source:         corev1.EventSource{Host: "a"}, Reason: "FailedCreatePodSandBox", Message: "route ip+net: no such network interface",
		FirstTimestamp: metav1.NewTime(now.Add(-time.Minute)), LastTimestamp: metav1.NewTime(now)}
	data, err := json.Marshal(ledger{Version: 1, EvictionWindow: cfg.EvictionWindow, Evictions: map[string]evictionRecord{}})
	if err != nil {
		t.Fatal(err)
	}
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: BudgetName, Namespace: "agent", UID: "budget", ResourceVersion: "1"},
		Data: map[string]string{budgetKey: string(data)}}
	objects := []runtime.Object{deployment, rs, pod, event, cm, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "test", Labels: selector.MatchLabels}}}
	var source *corev1.Node
	for _, name := range []string{"a", "b", "c"} {
		node := placementNode(name, name)
		node.Labels[detectors.SwiftV2LabelKey] = detectors.SwiftV2LabelValue
		objects = append(objects, node)
		if name == "a" {
			source = node
			continue
		}
		healthy := pod.DeepCopy()
		healthy.Name, healthy.UID, healthy.Spec.NodeName = "router-"+name, node.UID, name
		healthy.Status = corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}}
		objects = append(objects, healthy)
	}
	kube := fake.NewClientset(objects...)
	factory := informers.NewSharedInformerFactory(kube, 0)
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{{Group: "multitenancy.acn.azure.com", Version: "v1alpha1", Resource: "multitenantpodnetworkconfigs"}: "MultitenantPodNetworkConfigList"})
	c, err := NewController(kube, dyn, "agent", factory.Core().V1().Nodes(), factory.Core().V1().Pods(), factory.Core().V1().Events(), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.queue.ShutDown)
	for _, obj := range objects {
		var add func(any) error
		switch obj.(type) {
		case *corev1.Node:
			add = factory.Core().V1().Nodes().Informer().GetIndexer().Add
		case *corev1.Pod:
			add = factory.Core().V1().Pods().Informer().GetIndexer().Add
		case *corev1.Event:
			add = factory.Core().V1().Events().Informer().GetIndexer().Add
		default:
			continue
		}
		if err := add(obj); err != nil {
			t.Fatal(err)
		}
	}
	c.AllowConfiguration(true)
	if err := c.SetConfig(cfg); err != nil {
		t.Fatal(err)
	}
	return &fixture{c: c, kube: kube, cfg: cfg, pod: pod, node: source, now: now}
}

func (f *fixture) run() error {
	cfg, revision := f.c.configuration()
	_, err := f.c.reconcile(context.Background(), podKey{f.pod.Namespace, f.pod.Name}, cfg, revision)
	return err
}

func mutations(actions []ktesting.Action) []ktesting.Action {
	var result []ktesting.Action
	for _, action := range actions {
		if action.GetVerb() == "get" || action.GetVerb() == "list" || action.GetVerb() == "watch" {
			continue
		}
		result = append(result, action)
	}
	return result
}

func TestReplacementTemplateResources(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*corev1.PodSpec)
		hold   bool
	}{
		{name: "matching template"},
		{name: "template requests less", change: func(spec *corev1.PodSpec) {
			spec.Containers[0].Resources.Requests[corev1.ResourceCPU] = resource.MustParse("500m")
		}},
		{name: "admission injected NIC", change: func(spec *corev1.PodSpec) {
			delete(spec.Containers[0].Resources.Requests, kuberesources.SwiftNICResourceName)
		}},
		{name: "CPU resized below template", hold: true, change: func(spec *corev1.PodSpec) {
			spec.Containers[0].Resources.Requests[corev1.ResourceCPU] = resource.MustParse("9")
		}},
		{name: "memory resized below template", hold: true, change: func(spec *corev1.PodSpec) {
			spec.Containers[0].Resources.Requests[corev1.ResourceMemory] = resource.MustParse("9Gi")
		}},
		{name: "init container demand", hold: true, change: func(spec *corev1.PodSpec) {
			spec.InitContainers = []corev1.Container{{Name: "init", Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("9")},
			}}}
		}},
		{name: "Pod level demand", hold: true, change: func(spec *corev1.PodSpec) {
			spec.Resources = &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("9")},
			}
		}},
	} {
		for _, mode := range []Mode{Audit, Enforce} {
			t.Run(tc.name+"/"+string(mode), func(t *testing.T) {
				f := newFixture(t)
				f.cfg.Mode = mode
				if err := f.c.SetConfig(f.cfg); err != nil {
					t.Fatal(err)
				}
				rs, err := f.kube.AppsV1().ReplicaSets(f.pod.Namespace).Get(t.Context(), "router-rs", metav1.GetOptions{})
				if err != nil {
					t.Fatal(err)
				}
				if tc.change != nil {
					tc.change(&rs.Spec.Template.Spec)
				}
				if _, err := f.kube.AppsV1().ReplicaSets(rs.Namespace).Update(t.Context(), rs, metav1.UpdateOptions{}); err != nil {
					t.Fatal(err)
				}
				f.kube.ClearActions()
				err = f.run()
				if tc.hold {
					if err == nil || !strings.Contains(err.Error(), "ReplicaSet template requests") {
						t.Fatalf("expected template resource hold, got %v", err)
					}
					if got := mutations(f.kube.Actions()); len(got) != 0 {
						t.Fatalf("resource mismatch caused mutations: %v", got)
					}
				} else if err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestReplacementTemplateResourcesRecheckedAfterClaim(t *testing.T) {
	f := newFixture(t)
	f.kube.PrependReactor("patch", "pods", func(ktesting.Action) (bool, runtime.Object, error) {
		gvr := appsv1.SchemeGroupVersion.WithResource("replicasets")
		obj, err := f.kube.Tracker().Get(gvr, f.pod.Namespace, "router-rs")
		if err != nil {
			t.Fatal(err)
		}
		rs := obj.(*appsv1.ReplicaSet).DeepCopy()
		rs.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU] = resource.MustParse("9")
		if err := f.kube.Tracker().Update(gvr, rs, rs.Namespace); err != nil {
			t.Fatal(err)
		}
		return false, nil, nil
	})
	if err := f.run(); err == nil || !strings.Contains(err.Error(), "ReplicaSet template requests") {
		t.Fatalf("expected refreshed template resource hold, got %v", err)
	}
	actions := mutations(f.kube.Actions())
	if len(actions) != 1 || actions[0].GetVerb() != "patch" || actions[0].GetResource().Resource != "pods" {
		t.Fatalf("expected only the ownership claim, got %v", actions)
	}
}

func TestReplacementTemplateSafety(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*corev1.PodTemplateSpec)
	}{
		{name: "host network", change: func(p *corev1.PodTemplateSpec) { p.Spec.HostNetwork = true }},
		{name: "host PID", change: func(p *corev1.PodTemplateSpec) { p.Spec.HostPID = true }},
		{name: "host IPC", change: func(p *corev1.PodTemplateSpec) { p.Spec.HostIPC = true }},
		{name: "custom scheduler", change: func(p *corev1.PodTemplateSpec) { p.Spec.SchedulerName = "custom" }},
		{name: "runtime class", change: func(p *corev1.PodTemplateSpec) { p.Spec.RuntimeClassName = ptr.To("other") }},
		{name: "host port", change: func(p *corev1.PodTemplateSpec) {
			p.Spec.Containers[0].Ports = []corev1.ContainerPort{{ContainerPort: 443, HostPort: 443}}
		}},
		{name: "init host port", change: func(p *corev1.PodTemplateSpec) {
			p.Spec.InitContainers = []corev1.Container{{Name: "init", Ports: []corev1.ContainerPort{{ContainerPort: 443, HostPort: 443}}}}
		}},
		{name: "host path", change: func(p *corev1.PodTemplateSpec) {
			p.Spec.Volumes = []corev1.Volume{{Name: "host", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/data"}}}}
		}},
		{name: "persistent volume", change: func(p *corev1.PodTemplateSpec) {
			p.Spec.Volumes = []corev1.Volume{{Name: "data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "data"}}}}
		}},
		{name: "finalizer", change: func(p *corev1.PodTemplateSpec) { p.Finalizers = []string{"example.com/protected"} }},
		{name: "mirror annotation", change: func(p *corev1.PodTemplateSpec) {
			p.Annotations = map[string]string{corev1.MirrorPodAnnotationKey: "mirror"}
		}},
		{name: "zero termination grace", change: func(p *corev1.PodTemplateSpec) { p.Spec.TerminationGracePeriodSeconds = ptr.To(int64(0)) }},
		{name: "node selector", change: func(p *corev1.PodTemplateSpec) {
			p.Spec.NodeSelector = map[string]string{"example.com/pool": "unavailable"}
		}},
		{name: "node affinity", change: func(p *corev1.PodTemplateSpec) {
			p.Spec.Affinity.NodeAffinity = &corev1.NodeAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{NodeSelectorTerms: []corev1.NodeSelectorTerm{{
					MatchExpressions: []corev1.NodeSelectorRequirement{{Key: "example.com/pool", Operator: corev1.NodeSelectorOpIn, Values: []string{"unavailable"}}},
				}}},
			}
		}},
		{name: "pod affinity", change: func(p *corev1.PodTemplateSpec) {
			p.Spec.Affinity.PodAffinity = &corev1.PodAffinity{RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{{
				TopologyKey: corev1.LabelHostname, LabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "missing"}},
			}}}
		}},
		{name: "pod anti affinity", change: func(p *corev1.PodTemplateSpec) {
			p.Spec.Affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution[0].TopologyKey = "example.com/rack"
		}},
		{name: "topology spread", change: func(p *corev1.PodTemplateSpec) {
			p.Spec.TopologySpreadConstraints = []corev1.TopologySpreadConstraint{{
				MaxSkew: 1, TopologyKey: "example.com/rack", WhenUnsatisfiable: corev1.DoNotSchedule,
				LabelSelector: &metav1.LabelSelector{MatchLabels: p.Labels},
			}}
		}},
		{name: "tolerations", change: func(p *corev1.PodTemplateSpec) {
			p.Spec.Tolerations = []corev1.Toleration{{Key: "example.com/dedicated", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule}}
		}},
		{name: "labels", change: func(p *corev1.PodTemplateSpec) { p.Labels["example.com/group"] = "other" }},
		{name: "inherited mitigation ownership", change: func(p *corev1.PodTemplateSpec) { p.Labels[ownershipLabel] = ControllerName }},
	} {
		for _, stage := range []string{"audit", "before claim", "after claim"} {
			t.Run(tc.name+"/"+stage, func(t *testing.T) {
				f := newFixture(t)
				if stage == "audit" {
					f.cfg.Mode = Audit
					if err := f.c.SetConfig(f.cfg); err != nil {
						t.Fatal(err)
					}
				}
				changeTemplate := func() {
					gvr := appsv1.SchemeGroupVersion.WithResource("replicasets")
					obj, err := f.kube.Tracker().Get(gvr, f.pod.Namespace, "router-rs")
					if err != nil {
						t.Fatal(err)
					}
					rs := obj.(*appsv1.ReplicaSet).DeepCopy()
					tc.change(&rs.Spec.Template)
					if err := f.kube.Tracker().Update(gvr, rs, rs.Namespace); err != nil {
						t.Fatal(err)
					}
				}
				if stage == "after claim" {
					f.kube.PrependReactor("patch", "pods", func(ktesting.Action) (bool, runtime.Object, error) {
						changeTemplate()
						return false, nil, nil
					})
				} else {
					changeTemplate()
				}
				if err := f.run(); err == nil || !strings.Contains(err.Error(), "ReplicaSet template") {
					t.Fatalf("expected template safety hold, got %v", err)
				}
				actions := mutations(f.kube.Actions())
				if stage == "after claim" {
					if len(actions) != 1 || actions[0].GetVerb() != "patch" || actions[0].GetResource().Resource != "pods" {
						t.Fatalf("expected only the ownership claim, got %v", actions)
					}
				} else if len(actions) != 0 {
					t.Fatalf("template mismatch caused mutations: %v", actions)
				}
			})
		}
	}
}

func TestReplacementTemplateAdmissionDefaults(t *testing.T) {
	f := newFixture(t)
	pod := f.pod.DeepCopy()
	pod.Spec.SchedulerName = corev1.DefaultSchedulerName
	pod.Spec.Tolerations = []corev1.Toleration{
		{Key: corev1.TaintNodeNotReady, Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoExecute, TolerationSeconds: ptr.To(int64(300))},
		{Key: corev1.TaintNodeUnreachable, Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoExecute, TolerationSeconds: ptr.To(int64(300))},
	}
	if _, err := f.kube.CoreV1().Pods(pod.Namespace).Update(t.Context(), pod, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := f.run(); err != nil {
		t.Fatal(err)
	}
}

func TestReplacementTemplateRuntimeClass(t *testing.T) {
	for _, tc := range []struct {
		name          string
		pod, template *string
		hold          bool
	}{
		{name: "unset"},
		{name: "matching", pod: ptr.To("router"), template: ptr.To("router")},
		{name: "added", template: ptr.To("router"), hold: true},
		{name: "removed", pod: ptr.To("router"), hold: true},
		{name: "changed", pod: ptr.To("router"), template: ptr.To("other"), hold: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			pod := f.pod.DeepCopy()
			template := &corev1.PodTemplateSpec{ObjectMeta: pod.ObjectMeta, Spec: *pod.Spec.DeepCopy()}
			template.Spec.NodeName = ""
			pod.Spec.RuntimeClassName, template.Spec.RuntimeClassName = tc.pod, tc.template
			pod.Spec.Overhead = corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")}
			err := validateTemplate(pod, template)
			if (err != nil) != tc.hold {
				t.Fatalf("expected hold=%t, got %v", tc.hold, err)
			}
		})
	}
}

func TestRuntimeClassAdmission(t *testing.T) {
	for _, tc := range []struct {
		name         string
		overheadOnly bool
		change       func(*nodev1.RuntimeClass)
		readErr      error
	}{
		{name: "matching"},
		{name: "matching overhead only", overheadOnly: true},
		{name: "overhead increased", overheadOnly: true, change: func(rc *nodev1.RuntimeClass) {
			rc.Overhead.PodFixed[corev1.ResourceCPU] = resource.MustParse("500m")
		}},
		{name: "overhead decreased", overheadOnly: true, change: func(rc *nodev1.RuntimeClass) {
			rc.Overhead.PodFixed[corev1.ResourceCPU] = resource.MustParse("50m")
		}},
		{name: "overhead removed", overheadOnly: true, change: func(rc *nodev1.RuntimeClass) { rc.Overhead = nil }},
		{name: "overhead resource added", overheadOnly: true, change: func(rc *nodev1.RuntimeClass) {
			rc.Overhead.PodFixed[corev1.ResourceMemory] = resource.MustParse("500Mi")
		}},
		{name: "selector changed", change: func(rc *nodev1.RuntimeClass) {
			rc.Scheduling.NodeSelector[corev1.LabelHostname] = "b"
		}},
		{name: "selector added", change: func(rc *nodev1.RuntimeClass) {
			rc.Scheduling.NodeSelector["example.com/pool"] = "runtime"
		}},
		{name: "scheduling added", overheadOnly: true, change: func(rc *nodev1.RuntimeClass) {
			rc.Scheduling = &nodev1.Scheduling{NodeSelector: map[string]string{"example.com/pool": "runtime"}}
		}},
		{name: "scheduling removed", change: func(rc *nodev1.RuntimeClass) { rc.Scheduling = nil }},
		{name: "toleration removed", change: func(rc *nodev1.RuntimeClass) { rc.Scheduling.Tolerations = nil }},
		{name: "toleration changed", change: func(rc *nodev1.RuntimeClass) {
			rc.Scheduling.Tolerations[0].Key = "other"
		}},
		{name: "missing", readErr: apierrors.NewNotFound(nodev1.Resource("runtimeclasses"), "router")},
		{name: "forbidden", readErr: apierrors.NewForbidden(nodev1.Resource("runtimeclasses"), "router", errors.New("denied"))},
		{name: "timeout", readErr: apierrors.NewTimeoutError("runtime class read", 1)},
	} {
		for _, stage := range []string{"audit", "before claim", "after claim"} {
			t.Run(tc.name+"/"+stage, func(t *testing.T) {
				f := newFixture(t)
				if stage == "audit" {
					f.cfg.Mode = Audit
					if err := f.c.SetConfig(f.cfg); err != nil {
						t.Fatal(err)
					}
				}
				rc := &nodev1.RuntimeClass{
					ObjectMeta: metav1.ObjectMeta{Name: "router"}, Handler: "router",
					Overhead: &nodev1.Overhead{PodFixed: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")}},
					Scheduling: &nodev1.Scheduling{
						NodeSelector: map[string]string{corev1.LabelHostname: "a"},
						Tolerations:  []corev1.Toleration{{Key: "runtime", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule}},
					},
				}
				if tc.overheadOnly {
					rc.Scheduling = nil
				}
				pod := f.pod.DeepCopy()
				pod.Spec.RuntimeClassName = ptr.To(rc.Name)
				pod.Spec.Overhead = rc.Overhead.PodFixed.DeepCopy()
				if rc.Scheduling != nil {
					pod.Spec.NodeSelector = rc.Scheduling.DeepCopy().NodeSelector
					pod.Spec.Tolerations = append(pod.Spec.Tolerations, rc.Scheduling.Tolerations...)
				}
				if err := f.kube.Tracker().Update(corev1.SchemeGroupVersion.WithResource("pods"), pod, pod.Namespace); err != nil {
					t.Fatal(err)
				}
				gvr := appsv1.SchemeGroupVersion.WithResource("replicasets")
				obj, err := f.kube.Tracker().Get(gvr, pod.Namespace, "router-rs")
				if err != nil {
					t.Fatal(err)
				}
				rs := obj.(*appsv1.ReplicaSet)
				rs.Spec.Template.Spec.RuntimeClassName = ptr.To(rc.Name)
				if err := f.kube.Tracker().Update(gvr, rs, rs.Namespace); err != nil {
					t.Fatal(err)
				}
				claimed, reads := false, 0
				f.kube.PrependReactor("patch", "pods", func(ktesting.Action) (bool, runtime.Object, error) {
					claimed = true
					return false, nil, nil
				})
				f.kube.PrependReactor("get", "runtimeclasses", func(action ktesting.Action) (bool, runtime.Object, error) {
					reads++
					if action.(ktesting.GetAction).GetName() != rc.Name || action.GetNamespace() != "" {
						t.Fatal("RuntimeClass read targeted the wrong object")
					}
					current := rc.DeepCopy()
					if stage != "after claim" || claimed {
						if tc.readErr != nil {
							return true, nil, tc.readErr
						}
						if tc.change != nil {
							tc.change(current)
						}
					}
					return true, current, nil
				})
				err = f.run()
				hold := tc.change != nil || tc.readErr != nil
				if (err != nil) != hold {
					t.Fatalf("expected hold=%t, got %v", hold, err)
				}
				if tc.readErr != nil && !errors.Is(err, tc.readErr) {
					t.Fatalf("RuntimeClass read error lost: %v", err)
				}
				wantReads := 1
				if stage == "after claim" || (!hold && stage != "audit") {
					wantReads = 2
				}
				if reads != wantReads {
					t.Fatalf("RuntimeClass reads=%d, want %d", reads, wantReads)
				}
				actions := mutations(f.kube.Actions())
				if hold || stage == "audit" {
					want := 0
					if stage == "after claim" {
						want = 1
					}
					if len(actions) != want || (want == 1 && (actions[0].GetVerb() != "patch" || actions[0].GetResource().Resource != "pods")) {
						t.Fatalf("expected only %d ownership claims, got %v", want, actions)
					}
				} else if len(actions) != 4 || actions[2].GetSubresource() != "eviction" {
					t.Fatalf("expected claim, accounting, eviction and event, got %v", actions)
				}
			})
		}
	}
}

func TestRuntimeTemplate(t *testing.T) {
	tolerance := corev1.Toleration{Key: "runtime", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule}
	for _, tc := range []struct {
		name   string
		change func(*corev1.Pod, *corev1.PodTemplateSpec, *nodev1.RuntimeClass)
		hold   bool
	}{
		{name: "no runtime class", change: func(p *corev1.Pod, template *corev1.PodTemplateSpec, _ *nodev1.RuntimeClass) {
			p.Spec.RuntimeClassName, template.Spec.RuntimeClassName = nil, nil
		}},
		{name: "no overhead or scheduling"},
		{name: "overhead added", hold: true, change: func(_ *corev1.Pod, _ *corev1.PodTemplateSpec, rc *nodev1.RuntimeClass) {
			rc.Overhead = &nodev1.Overhead{PodFixed: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")}}
		}},
		{name: "template overhead conflicts", hold: true, change: func(_ *corev1.Pod, template *corev1.PodTemplateSpec, _ *nodev1.RuntimeClass) {
			template.Spec.Overhead = corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")}
		}},
		{name: "terminating class", hold: true, change: func(_ *corev1.Pod, _ *corev1.PodTemplateSpec, rc *nodev1.RuntimeClass) {
			rc.DeletionTimestamp = &metav1.Time{}
		}},
		{name: "matching explicit selector", change: func(p *corev1.Pod, template *corev1.PodTemplateSpec, rc *nodev1.RuntimeClass) {
			p.Spec.NodeSelector = map[string]string{"pool": "router"}
			template.Spec.NodeSelector = map[string]string{"pool": "router"}
			rc.Scheduling = &nodev1.Scheduling{NodeSelector: map[string]string{"pool": "router"}}
		}},
		{name: "selector conflict", hold: true, change: func(p *corev1.Pod, template *corev1.PodTemplateSpec, rc *nodev1.RuntimeClass) {
			p.Spec.NodeSelector = map[string]string{"pool": "router"}
			template.Spec.NodeSelector = map[string]string{"pool": "router"}
			rc.Scheduling = &nodev1.Scheduling{NodeSelector: map[string]string{"pool": "other"}}
		}},
		{name: "duplicate toleration", change: func(p *corev1.Pod, template *corev1.PodTemplateSpec, rc *nodev1.RuntimeClass) {
			p.Spec.Tolerations = []corev1.Toleration{tolerance}
			template.Spec.Tolerations = []corev1.Toleration{tolerance}
			rc.Scheduling = &nodev1.Scheduling{Tolerations: []corev1.Toleration{tolerance}}
		}},
		{name: "overlapping tolerations hold conservatively", hold: true, change: func(p *corev1.Pod, template *corev1.PodTemplateSpec, rc *nodev1.RuntimeClass) {
			p.Spec.Tolerations = []corev1.Toleration{tolerance}
			narrow := tolerance
			narrow.Operator, narrow.Value = corev1.TolerationOpEqual, "router"
			template.Spec.Tolerations = []corev1.Toleration{narrow}
			rc.Scheduling = &nodev1.Scheduling{Tolerations: []corev1.Toleration{tolerance}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pod := placementPod("router", "a")
			pod.Spec.RuntimeClassName = ptr.To("router")
			template := &corev1.PodTemplateSpec{ObjectMeta: *pod.ObjectMeta.DeepCopy(), Spec: *pod.Spec.DeepCopy()}
			template.Spec.NodeName = ""
			rc := &nodev1.RuntimeClass{ObjectMeta: metav1.ObjectMeta{Name: "router"}, Handler: "router"}
			if tc.change != nil {
				tc.change(pod, template, rc)
			}
			beforePod, beforeTemplate := pod.DeepCopy(), template.DeepCopy()
			client := fake.NewClientset(rc)
			result, err := runtimeTemplate(t.Context(), client, pod, template)
			if err == nil {
				err = validateTemplate(pod, result)
			}
			if (err != nil) != tc.hold {
				t.Fatalf("expected hold=%t, got %v", tc.hold, err)
			}
			if !apiequality.Semantic.DeepEqual(beforePod, pod) || !apiequality.Semantic.DeepEqual(beforeTemplate, template) {
				t.Fatal("RuntimeClass admission mutated its inputs")
			}
			if pod.Spec.RuntimeClassName == nil && len(client.Actions()) != 0 {
				t.Fatal("Pod without a RuntimeClass made API calls")
			}
			if len(mutations(client.Actions())) != 0 {
				t.Fatal("RuntimeClass admission mutated Kubernetes")
			}
		})
	}
}

func TestReplacementTemplateInjectedFields(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*corev1.Pod, *corev1.PodTemplateSpec)
		hold   bool
	}{
		{name: "bound topology", change: func(p *corev1.Pod, _ *corev1.PodTemplateSpec) {
			p.Labels[corev1.LabelTopologyRegion] = "region"
			p.Labels[corev1.LabelTopologyZone] = "zone"
		}},
		{name: "memory pressure", change: func(p *corev1.Pod, _ *corev1.PodTemplateSpec) {
			p.Spec.Tolerations = append(p.Spec.Tolerations, corev1.Toleration{Key: corev1.TaintNodeMemoryPressure, Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule})
		}},
		{name: "swift NIC", change: func(p *corev1.Pod, _ *corev1.PodTemplateSpec) {
			p.Spec.Tolerations = append(p.Spec.Tolerations, corev1.Toleration{Key: string(kuberesources.SwiftNICResourceName), Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule})
		}},
		{name: "unknown label", hold: true, change: func(p *corev1.Pod, _ *corev1.PodTemplateSpec) {
			p.Labels["example.com/injected"] = "value"
		}},
		{name: "explicit topology mismatch", hold: true, change: func(p *corev1.Pod, template *corev1.PodTemplateSpec) {
			p.Labels[corev1.LabelTopologyZone] = "zone-b"
			template.Labels[corev1.LabelTopologyZone] = "zone-a"
		}},
		{name: "unknown toleration", hold: true, change: func(p *corev1.Pod, _ *corev1.PodTemplateSpec) {
			p.Spec.Tolerations = append(p.Spec.Tolerations, corev1.Toleration{Key: "example.com/taint", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule})
		}},
		{name: "unbounded NoExecute", hold: true, change: func(p *corev1.Pod, _ *corev1.PodTemplateSpec) {
			p.Spec.Tolerations = append(p.Spec.Tolerations, corev1.Toleration{Key: string(kuberesources.SwiftNICResourceName), Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoExecute})
		}},
		{name: "NIC toleration without request", hold: true, change: func(p *corev1.Pod, template *corev1.PodTemplateSpec) {
			for _, spec := range []*corev1.PodSpec{&p.Spec, &template.Spec} {
				for i := range spec.Containers {
					delete(spec.Containers[i].Resources.Requests, kuberesources.SwiftNICResourceName)
					delete(spec.Containers[i].Resources.Limits, kuberesources.SwiftNICResourceName)
				}
			}
			p.Spec.Tolerations = append(p.Spec.Tolerations, corev1.Toleration{Key: string(kuberesources.SwiftNICResourceName), Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			pod := f.pod.DeepCopy()
			template := &corev1.PodTemplateSpec{ObjectMeta: *pod.ObjectMeta.DeepCopy(), Spec: *pod.Spec.DeepCopy()}
			template.Spec.NodeName = ""
			tc.change(pod, template)
			before := pod.DeepCopy()
			err := validateTemplate(pod, template)
			if (err != nil) != tc.hold {
				t.Fatalf("expected hold=%t, got %v", tc.hold, err)
			}
			if !apiequality.Semantic.DeepEqual(before, pod) {
				t.Fatal("template validation mutated the admitted Pod")
			}
		})
	}
}

func TestAuditEligibilityConfigurationFence(t *testing.T) {
	for _, mode := range []Mode{Disabled, Audit, Enforce} {
		t.Run(string(mode), func(t *testing.T) {
			f := newFixture(t)
			f.cfg.Mode = Audit
			if err := f.c.SetConfig(f.cfg); err != nil {
				t.Fatal(err)
			}
			f.kube.PrependReactor("get", "configmaps", func(ktesting.Action) (bool, runtime.Object, error) {
				updated := f.cfg
				updated.Mode = mode
				if err := f.c.SetConfig(updated); err != nil {
					t.Fatal(err)
				}
				return false, nil, nil
			})
			if err := f.run(); !errors.Is(err, ErrPaused) {
				t.Fatalf("expected stale audit evaluation to stop, got %v", err)
			}
			if actions := mutations(f.kube.Actions()); len(actions) != 0 {
				t.Fatalf("audit configuration race caused mutations: %v", actions)
			}
		})
	}
}

func TestReplacementInjectedFieldsAdmission(t *testing.T) {
	for _, mode := range []Mode{Audit, Enforce} {
		t.Run(string(mode), func(t *testing.T) {
			f := newFixture(t)
			f.cfg.Mode = mode
			if err := f.c.SetConfig(f.cfg); err != nil {
				t.Fatal(err)
			}
			pod := f.pod.DeepCopy()
			pod.Labels[corev1.LabelTopologyZone] = "a"
			for _, key := range []string{corev1.TaintNodeMemoryPressure, string(kuberesources.SwiftNICResourceName)} {
				pod.Spec.Tolerations = append(pod.Spec.Tolerations, corev1.Toleration{
					Key: key, Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule,
				})
			}
			if err := f.kube.Tracker().Update(corev1.SchemeGroupVersion.WithResource("pods"), pod, pod.Namespace); err != nil {
				t.Fatal(err)
			}
			if err := f.run(); err != nil {
				t.Fatal(err)
			}
			actions := mutations(f.kube.Actions())
			if mode == Audit && len(actions) != 0 {
				t.Fatalf("audit wrote: %v", actions)
			}
			if mode == Enforce {
				if len(actions) != 4 || actions[0].GetVerb() != "patch" ||
					actions[1].GetResource().Resource != "configmaps" ||
					actions[2].GetSubresource() != "eviction" ||
					actions[3].GetResource().Resource != "events" {
					t.Fatalf("expected claim, accounting, eviction and event, got %v", actions)
				}
			}
		})
	}
}

func TestReplacementTopologyLabels(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*ClusterSnapshot, *corev1.Pod)
		hold bool
	}{
		{name: "source topology matches"},
		{name: "source topology differs", hold: true, edit: func(_ *ClusterSnapshot, p *corev1.Pod) {
			p.Labels[corev1.LabelTopologyZone] = "other"
		}},
		{name: "source topology missing", hold: true, edit: func(s *ClusterSnapshot, _ *corev1.Pod) {
			for _, node := range s.Nodes {
				delete(node.Labels, corev1.LabelTopologyZone)
			}
		}},
		{name: "empty topology with missing source label", hold: true, edit: func(s *ClusterSnapshot, p *corev1.Pod) {
			p.Labels[corev1.LabelTopologyZone] = ""
			for _, node := range s.Nodes {
				delete(node.Labels, corev1.LabelTopologyZone)
			}
		}},
		{name: "resident anti affinity", hold: true, edit: func(s *ClusterSnapshot, _ *corev1.Pod) {
			resident := placementPod("topology-sensitive", "b")
			resident.Spec.Affinity = &corev1.Affinity{PodAntiAffinity: &corev1.PodAntiAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{{
					TopologyKey:   corev1.LabelHostname,
					LabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{corev1.LabelTopologyZone: "other"}},
				}},
			}}
			s.Pods = append(s.Pods, resident)
		}},
		{name: "candidate matchLabelKeys", hold: true, edit: func(_ *ClusterSnapshot, p *corev1.Pod) {
			p.Spec.Affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution[0].MatchLabelKeys = []string{corev1.LabelTopologyZone}
		}},
		{name: "candidate mismatchLabelKeys", hold: true, edit: func(_ *ClusterSnapshot, p *corev1.Pod) {
			p.Spec.Affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution[0].MismatchLabelKeys = []string{corev1.LabelTopologyRegion}
		}},
		{name: "spread expression", hold: true, edit: func(_ *ClusterSnapshot, p *corev1.Pod) {
			p.Spec.TopologySpreadConstraints = []corev1.TopologySpreadConstraint{{
				TopologyKey: corev1.LabelTopologyZone, MaxSkew: 1, WhenUnsatisfiable: corev1.DoNotSchedule,
				LabelSelector: &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{
					Key: corev1.LabelTopologyRegion, Operator: metav1.LabelSelectorOpExists,
				}}},
			}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			snapshot, err := f.c.snapshot(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			pod := f.pod.DeepCopy()
			pod.Labels[corev1.LabelTopologyRegion], pod.Labels[corev1.LabelTopologyZone] = "region", "a"
			for _, node := range snapshot.Nodes {
				node.Labels[corev1.LabelTopologyRegion] = "region"
			}
			if tc.edit != nil {
				tc.edit(&snapshot, pod)
			}
			err = f.c.checkPlacement(f.cfg, snapshot, pod)
			if (err != nil) != tc.hold {
				t.Fatalf("expected hold=%t, got %v", tc.hold, err)
			}
		})
	}
}

func TestReplacementTemplateEmptyDirPolicy(t *testing.T) {
	for _, mode := range []Mode{Audit, Enforce} {
		t.Run(string(mode), func(t *testing.T) {
			f := newFixture(t)
			f.cfg.Mode, f.cfg.Workload.AllowEmptyDir = mode, false
			if err := f.c.SetConfig(f.cfg); err != nil {
				t.Fatal(err)
			}
			pod := f.pod.DeepCopy()
			pod.Spec.Volumes = pod.Spec.Volumes[:1]
			if err := f.kube.Tracker().Update(corev1.SchemeGroupVersion.WithResource("pods"), pod, pod.Namespace); err != nil {
				t.Fatal(err)
			}
			if err := f.run(); err == nil || !strings.Contains(err.Error(), "ReplicaSet template emptyDir") {
				t.Fatalf("expected template emptyDir hold, got %v", err)
			}
			if len(mutations(f.kube.Actions())) != 0 {
				t.Fatal("template emptyDir hold caused mutations")
			}
		})
	}
}

func TestReplacementPlacementOmitsOwnership(t *testing.T) {
	f := newFixture(t)
	snapshot, err := f.c.snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	pod := f.pod.DeepCopy()
	pod.Labels[ownershipLabel] = ControllerName
	resident := placementPod("other", "a")
	resident.Spec.Affinity = &corev1.Affinity{PodAntiAffinity: &corev1.PodAntiAffinity{
		RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{{
			TopologyKey: corev1.LabelHostname,
			LabelSelector: &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{
				Key: ownershipLabel, Operator: metav1.LabelSelectorOpDoesNotExist,
			}}},
		}},
	}}
	snapshot.Pods = append(snapshot.Pods, resident)
	if err := f.c.checkPlacement(f.cfg, snapshot, pod); err == nil {
		t.Fatal("replacement without ownership bypassed resident anti-affinity")
	}
	if pod.Labels[ownershipLabel] != ControllerName {
		t.Fatal("placement mutated admitted Pod labels")
	}
}

func TestEvictionOrderingAndFailures(t *testing.T) {
	for _, outcome := range []string{"accepted", "denied", "timeout", "conflict"} {
		t.Run(outcome, func(t *testing.T) {
			f := newFixture(t)
			saved, evicted := false, 0
			f.kube.PrependReactor("update", "configmaps", func(action ktesting.Action) (bool, runtime.Object, error) {
				cm := action.(ktesting.UpdateAction).GetObject().(*corev1.ConfigMap)
				if cm.ResourceVersion != "1" || cm.UID != "budget" {
					t.Fatal("lost optimistic accounting identity")
				}
				if outcome == "conflict" {
					return true, nil, apierrors.NewConflict(corev1.Resource("configmaps"), BudgetName, errors.New("concurrent writer"))
				}
				saved = true
				return false, nil, nil
			})
			f.kube.PrependReactor("create", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
				if action.GetSubresource() != "eviction" {
					return false, nil, nil
				}
				evicted++
				eviction := action.(ktesting.CreateAction).GetObject().(*policyv1.Eviction)
				if !saved || eviction.DeleteOptions.Preconditions == nil ||
					*eviction.DeleteOptions.Preconditions.UID != f.pod.UID ||
					*eviction.DeleteOptions.Preconditions.ResourceVersion != "1" || eviction.DeleteOptions.GracePeriodSeconds != nil {
					t.Fatal("eviction bypassed durable record or graceful identity preconditions")
				}
				if outcome == "denied" {
					return true, nil, apierrors.NewTooManyRequests("PDB", 1)
				}
				if outcome == "timeout" {
					return true, nil, context.DeadlineExceeded
				}
				return true, nil, nil
			})
			err := f.run()
			if (err != nil) != (outcome != "accepted") {
				t.Fatalf("outcome=%s error=%v", outcome, err)
			}
			want := 1
			if outcome == "conflict" {
				want = 0
			}
			if evicted != want {
				t.Fatalf("evictions=%d want=%d", evicted, want)
			}
			b, err := f.c.readBudget(context.Background(), f.cfg)
			if err != nil || len(b.Evictions) != want {
				t.Fatalf("accounting=%+v error=%v", b, err)
			}
			if want == 1 && evictionAllowance(b, f.cfg, "deployment", f.node.UID, f.now) == nil {
				t.Fatal("attempt did not survive a fresh read")
			}
			for _, action := range mutations(f.kube.Actions()) {
				if action.GetVerb() == "delete" || action.GetResource().Resource == "nodes" {
					t.Fatalf("unsafe fallback: %v", action)
				}
			}
		})
	}
}

func TestModesAndConfigurationFence(t *testing.T) {
	for _, mode := range []Mode{Disabled, Audit} {
		t.Run(string(mode), func(t *testing.T) {
			f := newFixture(t)
			f.cfg.Mode = mode
			if err := f.c.SetConfig(f.cfg); err != nil {
				t.Fatal(err)
			}
			if err := f.run(); err != nil {
				t.Fatal(err)
			}
			if len(mutations(f.kube.Actions())) != 0 {
				t.Fatal("non-enforce mode wrote")
			}
			if mode == Disabled && len(f.kube.Actions()) != 0 {
				t.Fatal("disabled mode made API calls")
			}
		})
	}
	f := newFixture(t)
	_, revision := f.c.configuration()
	entered, release, changed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_ = f.c.write(revision, func() error { close(entered); <-release; return nil })
	}()
	<-entered
	go func() { defer wg.Done(); f.c.OnConfigMapDeleted(); close(changed) }()
	select {
	case <-changed:
		t.Fatal("config changed inside write")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	wg.Wait()
	if err := f.c.write(revision, func() error { t.Fatal("stale revision wrote"); return nil }); !errors.Is(err, ErrPaused) {
		t.Fatal(err)
	}
}

func TestAdmissionHolds(t *testing.T) {
	for _, test := range []struct {
		name     string
		resource string
		change   func(runtime.Object)
	}{
		{"pod UID", "pods", func(o runtime.Object) { o.(*corev1.Pod).UID = "recreated" }},
		{"pod finalizer", "pods", func(o runtime.Object) { o.(*corev1.Pod).Finalizers = []string{"example.com/protect"} }},
		{"pod owner", "pods", func(o runtime.Object) { o.(*corev1.Pod).OwnerReferences[0].UID = "recreated" }},
		{"pod recovered", "pods", func(o runtime.Object) { o.(*corev1.Pod).Status.Conditions[1].Status = corev1.ConditionTrue }},
		{"ownership", "pods", func(o runtime.Object) { o.(*corev1.Pod).Labels[ownershipLabel] = "other-controller" }},
		{"node UID", "nodes", func(o runtime.Object) { o.(*corev1.Node).UID = "recreated" }},
		{"node deleting", "nodes", func(o runtime.Object) { o.(*corev1.Node).DeletionTimestamp = &metav1.Time{Time: time.Unix(1, 0)} }},
		{"node scope", "nodes", func(o runtime.Object) { delete(o.(*corev1.Node).Labels, detectors.SwiftV2LabelKey) }},
		{"node readiness", "nodes", func(o runtime.Object) { o.(*corev1.Node).Status.Conditions[0].Status = corev1.ConditionFalse }},
		{"availability", "deployments", func(o runtime.Object) { o.(*appsv1.Deployment).Status.AvailableReplicas = 1 }},
		{"rollout", "deployments", func(o runtime.Object) { o.(*appsv1.Deployment).Status.UpdatedReplicas = 2 }},
		{"namespace deleting", "namespaces", func(o runtime.Object) { o.(*corev1.Namespace).DeletionTimestamp = &metav1.Time{Time: time.Unix(1, 0)} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture(t)
			f.kube.PrependReactor("get", test.resource, func(action ktesting.Action) (bool, runtime.Object, error) {
				a := action.(ktesting.GetAction)
				o, err := f.kube.Tracker().Get(a.GetResource(), a.GetNamespace(), a.GetName())
				if err != nil {
					return true, nil, err
				}
				test.change(o)
				return true, o, nil
			})
			if err := f.run(); err == nil {
				t.Fatal("unsafe candidate admitted")
			}
			if len(mutations(f.kube.Actions())) != 0 {
				t.Fatalf("hold wrote: %v", mutations(f.kube.Actions()))
			}
		})
	}
}

func TestPostClaimRevalidation(t *testing.T) {
	for _, failure := range []string{"ownership", "snapshot", "budget", "mode", "revision", "capacity"} {
		t.Run(failure, func(t *testing.T) {
			f := newFixture(t)
			claimed := false
			f.kube.PrependReactor("patch", "pods", func(ktesting.Action) (bool, runtime.Object, error) {
				claimed = true
				return false, nil, nil
			})
			f.kube.PrependReactor("get", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
				if !claimed || failure != "ownership" {
					return false, nil, nil
				}
				a := action.(ktesting.GetAction)
				o, err := f.kube.Tracker().Get(a.GetResource(), a.GetNamespace(), a.GetName())
				if err != nil {
					return true, nil, err
				}
				delete(o.(*corev1.Pod).Labels, ownershipLabel)
				return true, o, nil
			})
			f.kube.PrependReactor("list", "nodes", func(action ktesting.Action) (bool, runtime.Object, error) {
				if !claimed {
					return false, nil, nil
				}
				switch failure {
				case "snapshot":
					return true, nil, errors.New("read failed")
				case "capacity":
					return true, &corev1.NodeList{}, nil
				}
				return false, nil, nil
			})
			f.kube.PrependReactor("update", "configmaps", func(ktesting.Action) (bool, runtime.Object, error) {
				if failure == "budget" {
					return true, nil, errors.New("accounting failed")
				}
				return false, nil, nil
			})
			if failure == "mode" || failure == "revision" {
				// Change before the fenced patch, not from inside its API call.
				f.kube.PrependReactor("get", "configmaps", func(ktesting.Action) (bool, runtime.Object, error) {
					if failure == "mode" {
						f.c.OnConfigMapDeleted()
					} else if err := f.c.SetConfig(f.cfg); err != nil {
						t.Fatal(err)
					}
					return false, nil, nil
				})
			}
			if err := f.run(); err == nil {
				t.Fatal("post-claim failure did not hold")
			}
			for _, action := range mutations(f.kube.Actions()) {
				if action.GetSubresource() == "eviction" {
					t.Fatal("evicted after failed revalidation")
				}
			}
		})
	}
}

func TestConfigurationValidation(t *testing.T) {
	f := newFixture(t)
	for _, input := range []string{"mode: bogus", "mode: audit", "mode: enforce\nunknown: true"} {
		if _, err := Parse([]byte(input)); err == nil {
			t.Fatalf("accepted %q", input)
		}
	}
	f.c.OnConfigMap(&corev1.ConfigMap{Data: map[string]string{"config": "mode: bogus"}}, "config")
	if cfg, _ := f.c.configuration(); cfg.Mode != Enforce {
		t.Fatal("invalid update replaced config")
	}
	f.c.OnConfigMap(&corev1.ConfigMap{}, "config")
	if cfg, _ := f.c.configuration(); cfg.Mode != Disabled {
		t.Fatal("missing key did not disable")
	}
	f.c.AllowConfiguration(false)
	if err := f.c.SetConfig(f.cfg); err == nil {
		t.Fatal("deployment gate bypassed")
	}
	f.c.AllowConfiguration(true)
	if err := f.c.SetConfig(f.cfg); err != nil {
		t.Fatal(err)
	}
	f.cfg.Workload.PodSelector.MatchLabels["app"] = "changed"
	if cfg, _ := f.c.configuration(); cfg.Workload.PodSelector.MatchLabels["app"] != "private-router" {
		t.Fatal("caller mutated accepted config")
	}
}

func TestPlacementRetainsOtherFaults(t *testing.T) {
	for _, fault := range []string{"own", "other-pod", "node-wide"} {
		t.Run(fault, func(t *testing.T) {
			f := newFixture(t)
			snapshot, err := f.c.snapshot(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if fault == "other-pod" {
				snapshot.Stalled["a"] = append(snapshot.Stalled["a"], "other")
			}
			if fault == "node-wide" {
				snapshot.Faulted["a"] = true
			}
			err = f.c.checkPlacement(f.cfg, snapshot, f.pod)
			if (err != nil) != (fault != "own") {
				t.Fatalf("fault=%s error=%v", fault, err)
			}
		})
	}
}

func Example_budget() {
	data, _ := json.Marshal(ledger{Version: 1, EvictionWindow: metav1.Duration{Duration: time.Hour}, Evictions: map[string]evictionRecord{}})
	fmt.Println(string(data))
	// Output: {"version":1,"evictionWindow":"1h0m0s","evictions":{}}
}
