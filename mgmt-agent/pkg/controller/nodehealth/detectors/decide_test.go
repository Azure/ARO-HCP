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

package detectors

import (
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var testNow = time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)

func ago(d time.Duration) time.Time { return testNow.Add(-d) }

// podUID is a deterministic UID for a pod name, so a failure Event and its stuck
// Pod correlate by UID the way they do in a live cluster.
func podUID(name string) types.UID { return types.UID("uid-" + name) }

const sig = "route ip+net: no such network interface"

const nodeName = "aks-userswft2-0"

func testNode(swift, ready bool) *corev1.Node {
	labels := map[string]string{}
	if swift {
		labels[SwiftV2LabelKey] = SwiftV2LabelValue
	}
	status := corev1.ConditionFalse
	if ready {
		status = corev1.ConditionTrue
	}
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: nodeName, Labels: labels},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: status}},
		},
	}
}

// failEventFor is a FailedCreatePodSandBox Event whose kubelet Source.Host is the
// node and whose involved object is the named pod (correlated by UID), matching
// the SWIFT signature.
func failEventFor(pod string, last time.Time, msg string) *corev1.Event {
	return &corev1.Event{
		InvolvedObject: corev1.ObjectReference{Kind: "Pod", Namespace: "ns", Name: pod, UID: podUID(pod)},
		Source:         corev1.EventSource{Host: nodeName, Component: "kubelet"},
		Reason:         reasonFailedCreatePodSandBox,
		Message:        msg,
		LastTimestamp:  metav1.NewTime(last),
		FirstTimestamp: metav1.NewTime(last),
	}
}

// stuckPod is a pending pod whose PodReadyToStartContainers condition is False,
// the durable per-pod symptom of a sandbox that never came up. since is the
// condition's lastTransitionTime, which supplies the dwell.
func stuckPod(pod string, since time.Time) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: pod, UID: podUID(pod)},
		Spec:       corev1.PodSpec{NodeName: nodeName},
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

// withSwiftNIC marks a pod as one that asked for a SWIFT v2 delegated NIC, which
// is what makes its start evidence about the delegated-NIC path. Success
// fixtures that stand for "the node can still attach a NIC" must carry it;
// fixtures that stand for ordinary overlay traffic must not.
func withSwiftNIC(p *corev1.Pod) *corev1.Pod {
	p.Spec.Containers = append(p.Spec.Containers, corev1.Container{
		Name: "nic",
		Resources: corev1.ResourceRequirements{
			Limits: corev1.ResourceList{swiftNICResourceName: resource.MustParse("1")},
		},
	})
	return p
}

// startedPod is a pod whose PodReadyToStartContainers condition transitioned to
// True at ts: a fresh sandbox, the success signal SuccessAt records. hostNet
// marks it host-network, which must be excluded since it reaches the condition
// without a delegated NIC.
func startedPod(pod string, ts time.Time, hostNet bool) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: pod},
		Spec:       corev1.PodSpec{NodeName: nodeName, HostNetwork: hostNet},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{{
				Type:               corev1.PodReadyToStartContainers,
				Status:             corev1.ConditionTrue,
				LastTransitionTime: metav1.NewTime(ts),
			}},
		},
	}
}

// stuckFailing builds n distinct stuck pods, each with a matching in-window
// failure Event, all entering the stuck state at since.
func stuckFailing(n int, since time.Time) ([]*corev1.Event, []*corev1.Pod) {
	var events []*corev1.Event
	var pods []*corev1.Pod
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("p%d", i)
		pods = append(pods, stuckPod(name, since))
		events = append(events, failEventFor(name, ago(20*time.Second), sig))
	}
	return events, pods
}

func TestDecide(t *testing.T) {
	tests := []struct {
		name   string
		node   *corev1.Node
		events []*corev1.Event
		pods   []*corev1.Pod
		want   Decision
	}{
		{
			name: "wedged: floor of stuck failing pods, zero success, dwell met",
			node: testNode(true, true),
			events: func() []*corev1.Event {
				e, _ := stuckFailing(3, ago(15*time.Minute))
				return e
			}(),
			pods: func() []*corev1.Pod {
				_, p := stuckFailing(3, ago(15*time.Minute))
				return p
			}(),
			want: DecisionWedged,
		},
		{
			name: "flap: a started pod in the window is not a wedge",
			node: testNode(true, true),
			events: func() []*corev1.Event {
				e, _ := stuckFailing(3, ago(15*time.Minute))
				return e
			}(),
			pods: func() []*corev1.Pod {
				_, p := stuckFailing(3, ago(15*time.Minute))
				return append(p, withSwiftNIC(startedPod("ok", ago(2*time.Minute), false)))
			}(),
			want: DecisionHealthy,
		},
		{
			name: "below the stuck-pod floor is not wedged",
			node: testNode(true, true),
			events: func() []*corev1.Event {
				e, _ := stuckFailing(1, ago(15*time.Minute))
				return e
			}(),
			pods: func() []*corev1.Pod {
				_, p := stuckFailing(1, ago(15*time.Minute))
				return p
			}(),
			want: DecisionUnknown,
		},
		{
			name: "non-SWIFT node is never a candidate, so any label on it is stale",
			node: testNode(false, true),
			events: func() []*corev1.Event {
				e, _ := stuckFailing(5, ago(30*time.Minute))
				return e
			}(),
			pods: func() []*corev1.Pod {
				_, p := stuckFailing(5, ago(30*time.Minute))
				return p
			}(),
			want: DecisionNotApplicable,
		},
		{
			name: "non-SWIFT NotReady node is still not a candidate",
			node: testNode(false, false),
			want: DecisionNotApplicable,
		},
		{
			name: "NotReady node defers to node lifecycle",
			node: testNode(true, false),
			events: func() []*corev1.Event {
				e, _ := stuckFailing(5, ago(30*time.Minute))
				return e
			}(),
			pods: func() []*corev1.Pod {
				_, p := stuckFailing(5, ago(30*time.Minute))
				return p
			}(),
			want: DecisionUnknown,
		},
		{
			name: "dwell not yet met: pods stuck too recently",
			node: testNode(true, true),
			events: func() []*corev1.Event {
				e, _ := stuckFailing(3, ago(2*time.Minute))
				return e
			}(),
			pods: func() []*corev1.Pod {
				_, p := stuckFailing(3, ago(2*time.Minute))
				return p
			}(),
			want: DecisionUnknown,
		},
		{
			name: "floor is per-pod dwell: one old plus two brand-new stuck pods does not fire",
			node: testNode(true, true),
			events: []*corev1.Event{
				failEventFor("old", ago(20*time.Second), sig),
				failEventFor("new1", ago(20*time.Second), sig),
				failEventFor("new2", ago(20*time.Second), sig),
			},
			pods: []*corev1.Pod{
				stuckPod("old", ago(15*time.Minute)),
				stuckPod("new1", ago(20*time.Second)),
				stuckPod("new2", ago(20*time.Second)),
			},
			want: DecisionUnknown,
		},
		{
			name: "recovery: a started pod, no stuck pods",
			node: testNode(true, true),
			pods: []*corev1.Pod{withSwiftNIC(startedPod("ok", ago(1*time.Minute), false))},
			want: DecisionHealthy,
		},
		{
			name: "cold view: no signals leaves the label unchanged",
			node: testNode(true, true),
			want: DecisionUnknown,
		},
		{
			name: "non-matching signature is not a failing pod",
			node: testNode(true, true),
			events: []*corev1.Event{
				failEventFor("p0", ago(20*time.Second), "image pull backoff"),
				failEventFor("p1", ago(20*time.Second), "image pull backoff"),
				failEventFor("p2", ago(20*time.Second), "image pull backoff"),
			},
			pods: []*corev1.Pod{
				stuckPod("p0", ago(15*time.Minute)),
				stuckPod("p1", ago(15*time.Minute)),
				stuckPod("p2", ago(15*time.Minute)),
			},
			want: DecisionUnknown,
		},
		{
			name: "stuck pod without a matching Event is not counted",
			node: testNode(true, true),
			pods: []*corev1.Pod{
				stuckPod("p0", ago(15*time.Minute)),
				stuckPod("p1", ago(15*time.Minute)),
				stuckPod("p2", ago(15*time.Minute)),
			},
			want: DecisionUnknown,
		},
		{
			name: "stale storm: failure Events aged out of the window",
			node: testNode(true, true),
			events: []*corev1.Event{
				failEventFor("p0", ago(20*time.Minute), sig),
				failEventFor("p1", ago(20*time.Minute), sig),
				failEventFor("p2", ago(20*time.Minute), sig),
			},
			pods: []*corev1.Pod{
				stuckPod("p0", ago(20*time.Minute)),
				stuckPod("p1", ago(20*time.Minute)),
				stuckPod("p2", ago(20*time.Minute)),
			},
			want: DecisionUnknown,
		},
		{
			name: "a started pod outside the window does not suppress the wedge",
			node: testNode(true, true),
			events: func() []*corev1.Event {
				e, _ := stuckFailing(3, ago(15*time.Minute))
				return e
			}(),
			pods: func() []*corev1.Pod {
				_, p := stuckFailing(3, ago(15*time.Minute))
				return append(p, withSwiftNIC(startedPod("stale", ago(30*time.Minute), false)))
			}(),
			want: DecisionWedged,
		},
		{
			name: "success not set: a host-network pod in the slice is ignored, node stays wedged",
			node: testNode(true, true),
			events: func() []*corev1.Event {
				e, _ := stuckFailing(3, ago(15*time.Minute))
				return e
			}(),
			pods: func() []*corev1.Pod {
				_, p := stuckFailing(3, ago(15*time.Minute))
				return append(p, startedPod("hostnet", ago(2*time.Minute), true))
			}(),
			want: DecisionWedged,
		},
		{
			name: "gate off: PodReadyToStartContainers absent yields Unknown",
			node: testNode(true, true),
			events: []*corev1.Event{
				failEventFor("p0", ago(20*time.Second), sig),
				failEventFor("p1", ago(20*time.Second), sig),
				failEventFor("p2", ago(20*time.Second), sig),
			},
			pods: []*corev1.Pod{
				{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "p0"}, Spec: corev1.PodSpec{NodeName: nodeName}, Status: corev1.PodStatus{Phase: corev1.PodPending}},
				{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "p1"}, Spec: corev1.PodSpec{NodeName: nodeName}, Status: corev1.PodStatus{Phase: corev1.PodPending}},
				{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "p2"}, Spec: corev1.PodSpec{NodeName: nodeName}, Status: corev1.PodStatus{Phase: corev1.PodPending}},
			},
			want: DecisionUnknown,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := Decide(tc.node, tc.events, tc.pods, testNow)
			if got != tc.want {
				t.Errorf("Decide() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAnyApplies(t *testing.T) {
	// AnyApplies is the gate the controller uses to skip gathering a node's Pods
	// and Events, so it must agree with Decide's own ownership gate.
	tests := []struct {
		name string
		node *corev1.Node
		want bool
	}{
		{name: "nil node", node: nil, want: false},
		{name: "swift node", node: testNode(true, true), want: true},
		{name: "swift node that is not ready", node: testNode(true, false), want: true},
		{name: "non-swift node", node: testNode(false, true), want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := AnyApplies(tc.node); got != tc.want {
				t.Errorf("AnyApplies() = %v, want %v", got, tc.want)
			}
			if !tc.want && tc.node != nil {
				if got, _ := Decide(tc.node, nil, nil, testNow); got != DecisionNotApplicable {
					t.Errorf("Decide() = %v, want NotApplicable to match AnyApplies", got)
				}
			}
		})
	}
}

func TestDecideNilNode(t *testing.T) {
	if got, _ := Decide(nil, nil, nil, testNow); got != DecisionUnknown {
		t.Errorf("Decide(nil) = %v, want Unknown", got)
	}
}

func TestDecideSnapshotOnWedge(t *testing.T) {
	node := testNode(true, true)
	events, pods := stuckFailing(3, ago(15*time.Minute))

	got, snap := Decide(node, events, pods, testNow)
	if got != DecisionWedged {
		t.Fatalf("Decide() = %v, want Wedged", got)
	}
	if snap.DetectorName != swiftVFTeardown.name {
		t.Errorf("snapshot detector = %q, want %q", snap.DetectorName, swiftVFTeardown.name)
	}
	if snap.Pods.FailureCount != 3 {
		t.Errorf("snapshot FailureCount = %d, want 3", snap.Pods.FailureCount)
	}
	if snap.Pods.SustainedCount != 3 {
		t.Errorf("snapshot SustainedCount = %d, want 3", snap.Pods.SustainedCount)
	}
	if snap.Pods.RecentSuccess {
		t.Errorf("snapshot RecentSuccess = %v, want false", snap.Pods.RecentSuccess)
	}
	if !snap.StuckSince.Equal(ago(15 * time.Minute)) {
		t.Errorf("snapshot StuckSince = %v, want %v", snap.StuckSince, ago(15*time.Minute))
	}
	if snap.Reason == "" {
		t.Error("snapshot reason should be set on a wedge")
	}
}

func TestDwellReadFromOldestStuckPod(t *testing.T) {
	// Failure Events are fresh, but the oldest stuck pod condition is well past the
	// dwell, so the wedge fires on the durable Pod timestamp, not the Event age.
	node := testNode(true, true)
	events, pods := stuckFailing(3, ago(20*time.Minute))
	got, _ := Decide(node, events, pods, testNow)
	if got != DecisionWedged {
		t.Errorf("Decide() = %v, want Wedged", got)
	}
}

func TestEventsCorrelateByUIDNotName(t *testing.T) {
	// A wedge's failure Events belong to Pods that were later deleted; new Pods
	// reuse the same namespace/name but are distinct objects with fresh UIDs. The
	// old Events must not be attributed to the new Pods, so name reuse cannot
	// manufacture a wedge.
	node := testNode(true, true)
	events, pods := stuckFailing(3, ago(20*time.Minute))
	for _, p := range pods {
		p.UID = types.UID(string(p.UID) + "-reused")
	}
	got, _ := Decide(node, events, pods, testNow)
	if got != DecisionUnknown {
		t.Errorf("Decide() = %v, want Unknown (name reuse must not inherit old Events)", got)
	}
}

func TestSignatureVariantsMatch(t *testing.T) {
	// Each message must match, and must map to the signature it belongs to, so
	// the index recorded for a pod is the one reported as MatchedSignature.
	for wantIdx, m := range []string{
		"failed to setup network for sandbox: route ip+net: no such network interface",
		"plugin failed: network is unreachable",
		"mtpnc is not ready for pod",
		"dhcp discover for eth0 timed out after 15s",
	} {
		gotIdx, ok := swiftVFTeardown.matchSignature(m)
		if !ok {
			t.Errorf("expected signature match for %q", m)
			continue
		}
		if gotIdx != wantIdx {
			t.Errorf("matchSignature(%q) index = %d, want %d", m, gotIdx, wantIdx)
		}
	}
	if _, ok := swiftVFTeardown.matchSignature("image pull backoff"); ok {
		t.Error("unrelated message should not match")
	}
}

func TestMatchedSignatureReportsDominantFailureMode(t *testing.T) {
	const (
		sigNoSuchIface = `no such network interface`
		sigMtpnc       = `mtpnc is not ready`
	)
	msgNoSuchIface := "failed to setup network for sandbox: route ip+net: no such network interface"
	msgMtpnc := "mtpnc is not ready for pod"

	t.Run("majority wins, end to end through Decide", func(t *testing.T) {
		// Three pods so the detector actually fires: this is the path that reaches
		// the annotation and the metric, so assert the signature survives it.
		stuckAt := ago(20 * time.Minute)
		pods := []*corev1.Pod{stuckPod("p0", stuckAt), stuckPod("p1", stuckAt), stuckPod("p2", stuckAt)}
		events := []*corev1.Event{
			failEventFor("p0", ago(20*time.Second), msgNoSuchIface),
			failEventFor("p1", ago(20*time.Second), msgMtpnc),
			failEventFor("p2", ago(20*time.Second), msgMtpnc),
		}
		got, snap := Decide(testNode(true, true), events, pods, testNow)
		if got != DecisionWedged {
			t.Fatalf("Decide() = %v, want Wedged", got)
		}
		if snap.MatchedSignature != sigMtpnc {
			t.Errorf("MatchedSignature = %q, want %q", snap.MatchedSignature, sigMtpnc)
		}
	})

	// The remaining cases exercise the classification itself, which is Evaluate's
	// job. They go through Evaluate directly rather than inflating the fixtures to
	// clear the floor, since Decide only returns a populated Snapshot on a wedge.
	t.Run("tie broken by declaration order", func(t *testing.T) {
		// One pod each. Both signatures are equally represented, so the earlier
		// declared signature wins and the annotation does not flap between
		// evaluations.
		stuckAt := ago(20 * time.Minute)
		pods := []*corev1.Pod{stuckPod("p0", stuckAt), stuckPod("p1", stuckAt)}
		events := []*corev1.Event{
			failEventFor("p1", ago(20*time.Second), msgMtpnc),
			failEventFor("p0", ago(20*time.Second), msgNoSuchIface),
		}
		snap := swiftVFTeardown.Evaluate(events, pods, testNow)
		if snap.MatchedSignature != sigNoSuchIface {
			t.Errorf("MatchedSignature = %q, want %q (earliest declared signature)", snap.MatchedSignature, sigNoSuchIface)
		}
	})

	t.Run("one pod matching several signatures is classified once", func(t *testing.T) {
		// A single pod with Events for two signatures must not be double counted,
		// and must classify to the earlier declared signature whatever order the
		// informer returns its Events in.
		stuckAt := ago(20 * time.Minute)
		pods := []*corev1.Pod{stuckPod("p0", stuckAt)}
		events := []*corev1.Event{
			failEventFor("p0", ago(20*time.Second), msgMtpnc),
			failEventFor("p0", ago(10*time.Second), msgNoSuchIface),
		}
		snap := swiftVFTeardown.Evaluate(events, pods, testNow)
		if snap.Pods.FailureCount != 1 {
			t.Errorf("FailureCount = %d, want 1 (one pod, two Events)", snap.Pods.FailureCount)
		}
		if snap.MatchedSignature != sigNoSuchIface {
			t.Errorf("MatchedSignature = %q, want %q", snap.MatchedSignature, sigNoSuchIface)
		}
	})

	t.Run("only counted pods are tallied", func(t *testing.T) {
		// A pod with a matching Event that is not stuck contributes nothing, so a
		// stale failure mode cannot outvote the one the node is actually showing.
		stuckAt := ago(20 * time.Minute)
		// startedPod carries no UID, so set it explicitly: without correlation the
		// subtest would pass for the wrong reason.
		p1, p2 := startedPod("p1", ago(1*time.Minute), false), startedPod("p2", ago(1*time.Minute), false)
		p1.UID, p2.UID = podUID("p1"), podUID("p2")
		pods := []*corev1.Pod{stuckPod("p0", stuckAt), p1, p2}
		events := []*corev1.Event{
			failEventFor("p0", ago(20*time.Second), msgMtpnc),
			failEventFor("p1", ago(20*time.Second), msgNoSuchIface),
			failEventFor("p2", ago(20*time.Second), msgNoSuchIface),
		}
		snap := swiftVFTeardown.Evaluate(events, pods, testNow)
		if snap.Pods.FailureCount != 1 {
			t.Errorf("FailureCount = %d, want 1", snap.Pods.FailureCount)
		}
		if snap.MatchedSignature != sigMtpnc {
			t.Errorf("MatchedSignature = %q, want %q", snap.MatchedSignature, sigMtpnc)
		}
	})

	t.Run("no failing pods reports no signature", func(t *testing.T) {
		snap := swiftVFTeardown.Evaluate(nil, nil, testNow)
		if snap.MatchedSignature != "" {
			t.Errorf("MatchedSignature = %q, want empty", snap.MatchedSignature)
		}
	})
}

func TestSuccessAt(t *testing.T) {
	ts := ago(2 * time.Minute)

	// A non-host-network pod with PodReadyToStartContainers=True yields its
	// transition time: the durable per-node success timestamp the controller
	// records.
	if at, ok := SuccessAt(startedPod("ok", ts, false)); !ok || !at.Equal(ts) {
		t.Errorf("SuccessAt(started) = (%v, %v), want (%v, true)", at, ok, ts)
	}

	// A host-network pod reaches the condition without a delegated NIC, so it is
	// never a SWIFT success.
	if _, ok := SuccessAt(startedPod("hostnet", ts, true)); ok {
		t.Error("SuccessAt(host-network) = true, want false")
	}

	// A stuck pod (condition False) is not a success.
	if _, ok := SuccessAt(stuckPod("p0", ts)); ok {
		t.Error("SuccessAt(stuck) = true, want false")
	}

	// A pod without the condition (feature gate off) is not a success.
	bare := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "bare"}, Spec: corev1.PodSpec{NodeName: nodeName}}
	if _, ok := SuccessAt(bare); ok {
		t.Error("SuccessAt(no condition) = true, want false")
	}

	// A nil pod is safe.
	if _, ok := SuccessAt(nil); ok {
		t.Error("SuccessAt(nil) = true, want false")
	}
}

// completedPod is a pod that ran and finished. Its containers terminated, so a
// sandbox existed, but PodReadyToStartContainers has dropped back to False the
// way it does for every finished pod.
func completedPod(name string, startedAt time.Time, hostNet bool) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: name},
		Spec:       corev1.PodSpec{NodeName: nodeName, HostNetwork: hostNet},
		Status: corev1.PodStatus{
			Phase: corev1.PodSucceeded,
			Conditions: []corev1.PodCondition{{
				Type:               corev1.PodReadyToStartContainers,
				Status:             corev1.ConditionFalse,
				LastTransitionTime: metav1.NewTime(startedAt.Add(1 * time.Minute)),
			}},
			ContainerStatuses: []corev1.ContainerStatus{{
				Name: "main",
				State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
					StartedAt:  metav1.NewTime(startedAt),
					FinishedAt: metav1.NewTime(startedAt.Add(1 * time.Minute)),
				}},
			}},
		},
	}
}

// TestSuccessAtCountsFinishedPods pins the Job and CronJob case. A finished pod
// drops PodReadyToStartContainers back to False, so reading only the condition
// would leave a node whose recent traffic was short-lived pods looking like it
// had never started anything. A container can only terminate if it started, and
// it can only start inside a sandbox, so the container's own start time is
// usable proof the node could still attach a NIC.
func TestSuccessAtCountsFinishedPods(t *testing.T) {
	started := ago(3 * time.Minute)

	if at, ok := SuccessAt(completedPod("job", started, false)); !ok || !at.Equal(started) {
		t.Errorf("SuccessAt(completed) = (%v, %v), want (%v, true)", at, ok, started)
	}

	// Host-network is excluded here too: it never needed a delegated NIC.
	if _, ok := SuccessAt(completedPod("hostnet-job", started, true)); ok {
		t.Error("SuccessAt(completed host-network) = true, want false")
	}

	// A pod that failed before any container ran proves nothing: no container
	// reached a terminated state, which is exactly the sandbox-failure shape.
	neverRan := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "never-ran"},
		Spec:       corev1.PodSpec{NodeName: nodeName},
		Status: corev1.PodStatus{
			Phase: corev1.PodFailed,
			ContainerStatuses: []corev1.ContainerStatus{{
				Name:  "main",
				State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ContainerCreating"}},
			}},
		},
	}
	if _, ok := SuccessAt(neverRan); ok {
		t.Error("SuccessAt(terminal pod whose containers never ran) = true, want false")
	}
}

// TestSuccessAtIgnoresRestartedContainers is the false-negative guard on the
// finished-pod path. A container's terminated state describes its latest run, so
// on a restarted container StartedAt is the start of a run inside the sandbox the
// pod already had. An established sandbox survives the VF teardown, so that run
// needed no working network: counting it would let a wedged node fabricate a
// fresh success out of a container looping in an old sandbox and suppress its own
// detection. Only a container that ran exactly once is evidence.
func TestSuccessAtIgnoresRestartedContainers(t *testing.T) {
	started := ago(3 * time.Minute)

	restarted := completedPod("restarted", started, false)
	restarted.Status.ContainerStatuses[0].RestartCount = 2
	if at, ok := SuccessAt(restarted); ok {
		t.Errorf("SuccessAt(restarted container) = (%v, true), want false", at)
	}

	// A sidecar that never restarted still carries proof, even when another
	// container in the same pod did.
	mixed := completedPod("mixed", started, false)
	mixed.Status.ContainerStatuses[0].RestartCount = 5
	mixed.Status.ContainerStatuses = append(mixed.Status.ContainerStatuses, corev1.ContainerStatus{
		Name: "sidecar",
		State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
			StartedAt:  metav1.NewTime(started),
			FinishedAt: metav1.NewTime(started.Add(1 * time.Minute)),
		}},
	})
	if at, ok := SuccessAt(mixed); !ok || !at.Equal(started) {
		t.Errorf("SuccessAt(mixed) = (%v, %v), want (%v, true)", at, ok, started)
	}
}

// TestRestartedContainerDoesNotSuppressAWedge is the same guard at the decision
// level: a pod looping in a sandbox built before the node wedged must not read as
// recovery, however recent its container's latest start is.
func TestRestartedContainerDoesNotSuppressAWedge(t *testing.T) {
	events, pods := stuckFailing(3, ago(15*time.Minute))

	looping := completedPod("looping", ago(1*time.Minute), false)
	looping.Status.ContainerStatuses[0].RestartCount = 7

	got, snap := Decide(testNode(true, true), events, pods, testNow)
	if got != DecisionWedged {
		t.Fatalf("precondition: Decide() = %v, want Wedged", got)
	}

	got, snap = Decide(testNode(true, true), events, append(pods, looping), testNow)
	if got != DecisionWedged {
		t.Errorf("Decide(with a restarting container) = %v, want Wedged", got)
	}
	if snap.Pods.RecentSuccess {
		t.Error("RecentSuccess = true for a container restarting in an existing sandbox, want false")
	}
}

// TestFinishedPodInWindowPreventsAFalseWedge is the reason the finished-pod path
// exists: on a low-churn node the only thing that started recently may be a pod
// that has already completed. It is still real proof the node can build a
// sandbox, so it must suppress the wedge exactly as a running pod does.
//
// The pod has to be one that asked for a delegated NIC. A completed pod that
// only ever used the overlay proves nothing about the path this detector
// watches; see TestOverlaySuccessDoesNotSuppressASwiftWedge.
func TestFinishedPodInWindowPreventsAFalseWedge(t *testing.T) {
	events, pods := stuckFailing(3, ago(15*time.Minute))

	if got, _ := Decide(testNode(true, true), events, pods, testNow); got != DecisionWedged {
		t.Fatalf("precondition: Decide() = %v, want Wedged", got)
	}

	withJob := append(pods, withSwiftNIC(completedPod("router-job", ago(3*time.Minute), false)))
	if got, _ := Decide(testNode(true, true), events, withJob, testNow); got != DecisionHealthy {
		t.Errorf("a finished pod inside the window must rule out a wedge: Decide() = %v, want Healthy", got)
	}

	// The same pod outside the window is not current evidence.
	withOldJob := append(pods, completedPod("old-cronjob", ago(45*time.Minute), false))
	if got, _ := Decide(testNode(true, true), events, withOldJob, testNow); got != DecisionWedged {
		t.Errorf("a finished pod outside the window must not rule out a wedge: Decide() = %v, want Wedged", got)
	}
}

// TestPodRequestsSwiftNIC pins how a pod is recognised as needing a delegated
// NIC. Extended resources are schedulable only as a limit and the kubelet mirrors
// the limit into requests, so both fields have to be read, on init containers as
// well as regular ones.
func TestPodRequestsSwiftNIC(t *testing.T) {
	nic := corev1.ResourceList{swiftNICResourceName: resource.MustParse("1")}
	cpu := corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")}

	tests := []struct {
		name string
		pod  *corev1.Pod
		want bool
	}{
		{name: "nil pod", pod: nil, want: false},
		{name: "no containers", pod: &corev1.Pod{}, want: false},
		{
			name: "limit on a regular container",
			pod: &corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{
				{Name: "c", Resources: corev1.ResourceRequirements{Limits: nic}},
			}}},
			want: true,
		},
		{
			name: "request on a regular container",
			pod: &corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{
				{Name: "c", Resources: corev1.ResourceRequirements{Requests: nic}},
			}}},
			want: true,
		},
		{
			name: "limit on an init container",
			pod: &corev1.Pod{Spec: corev1.PodSpec{InitContainers: []corev1.Container{
				{Name: "i", Resources: corev1.ResourceRequirements{Limits: nic}},
			}}},
			want: true,
		},
		{
			name: "only unrelated resources",
			pod: &corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{
				{Name: "c", Resources: corev1.ResourceRequirements{Limits: cpu, Requests: cpu}},
			}}},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := podRequestsSwiftNIC(tc.pod); got != tc.want {
				t.Errorf("podRequestsSwiftNIC() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestOverlaySuccessDoesNotSuppressASwiftWedge is the partial-wedge case, and the
// reason successScope exists.
//
// A mgmt node runs almost all of its pods on the ordinary overlay, which keeps
// working while the delegated-NIC path is dead. Counting those starts as success
// lets a node that cannot attach a single NIC look healthy for as long as it
// keeps scheduling ordinary pods, which on a busy mgmt node is forever.
//
// Pinned to CI node aks-userswft2-17575576-vmss000003 on 2026-09-01, where 8
// router pods across 7 hosted control planes hung for 16 minutes on
// dhcp-discover timeouts while the node created 110 other pods and brought 107
// of them to Running.
func TestOverlaySuccessDoesNotSuppressASwiftWedge(t *testing.T) {
	events, pods := stuckFailing(3, ago(15*time.Minute))

	if got, _ := Decide(testNode(true, true), events, pods, testNow); got != DecisionWedged {
		t.Fatalf("precondition: Decide() = %v, want Wedged", got)
	}

	// The overlay is healthy throughout: a steady stream of ordinary pods reaches
	// a fresh sandbox well inside the window. None of them asked for a NIC.
	overlay := pods
	for i := 0; i < 20; i++ {
		overlay = append(overlay, startedPod(fmt.Sprintf("overlay%d", i), ago(1*time.Minute), false))
	}
	// A completed overlay pod is the same story on a low-churn node.
	overlay = append(overlay, completedPod("cronjob", ago(3*time.Minute), false))

	got, snap := Decide(testNode(true, true), events, overlay, testNow)
	if got != DecisionWedged {
		t.Errorf("overlay-only successes suppressed a real delegated-NIC wedge: Decide() = %v, want Wedged", got)
	}
	if snap.RecentSuccess {
		t.Error("RecentSuccess = true from pods that never asked for a NIC, want false")
	}

	// One pod that did ask for a NIC and got one is real proof the path works, and
	// must still rule the wedge out.
	recovered := append(overlay, withSwiftNIC(startedPod("router", ago(1*time.Minute), false)))
	if got, _ := Decide(testNode(true, true), events, recovered, testNow); got != DecisionHealthy {
		t.Errorf("a delegated-NIC success must rule out the wedge: Decide() = %v, want Healthy", got)
	}
}

// TestSuccessScopeDefaultsToEveryPod pins the zero value: a detector that does
// not set a scope keeps counting every pod, so adding the field cannot silently
// change a detector that did not opt in.
func TestSuccessScopeDefaultsToEveryPod(t *testing.T) {
	unscoped := swiftVFTeardown
	unscoped.successScope = nil

	_, pods := stuckFailing(3, ago(15*time.Minute))
	pods = append(pods, startedPod("plain", ago(1*time.Minute), false))
	events, _ := stuckFailing(3, ago(15*time.Minute))

	if snap := unscoped.Evaluate(events, pods, testNow); !snap.RecentSuccess {
		t.Error("RecentSuccess = false with a nil successScope, want true")
	}
}
