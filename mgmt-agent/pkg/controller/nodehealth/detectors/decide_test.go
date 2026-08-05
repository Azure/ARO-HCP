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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var testNow = time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)

func ago(d time.Duration) time.Time { return testNow.Add(-d) }

// podUID is a deterministic UID for a pod name, so a failure Event and its stuck
// Pod correlate by UID the way they do in a live cluster.
func podUID(name string) types.UID { return types.UID("uid-" + name) }

// warm is an observedSince far enough in the past that a full detector window has
// elapsed, so the success column is trusted. cold is recent, so the controller
// has not yet watched a full window and a would-be wedge stays indeterminate.
var (
	warm = ago(30 * time.Minute)
	cold = ago(1 * time.Minute)
)

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
		name          string
		node          *corev1.Node
		events        []*corev1.Event
		pods          []*corev1.Pod
		observedSince time.Time
		lastSuccessAt time.Time
		want          Decision
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
			name: "flap: a recorded success in the window is not a wedge",
			node: testNode(true, true),
			events: func() []*corev1.Event {
				e, _ := stuckFailing(3, ago(15*time.Minute))
				return e
			}(),
			pods: func() []*corev1.Pod {
				_, p := stuckFailing(3, ago(15*time.Minute))
				return p
			}(),
			lastSuccessAt: ago(2 * time.Minute),
			want:          DecisionHealthy,
		},
		{
			name: "below the stuck-pod floor is not wedged",
			node: testNode(true, true),
			events: func() []*corev1.Event {
				e, _ := stuckFailing(2, ago(15*time.Minute))
				return e
			}(),
			pods: func() []*corev1.Pod {
				_, p := stuckFailing(2, ago(15*time.Minute))
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
			name:          "recovery: a recorded success, no stuck pods",
			node:          testNode(true, true),
			lastSuccessAt: ago(1 * time.Minute),
			want:          DecisionHealthy,
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
			name: "cold controller: full window not yet observed does not wedge",
			node: testNode(true, true),
			events: func() []*corev1.Event {
				e, _ := stuckFailing(3, ago(15*time.Minute))
				return e
			}(),
			pods: func() []*corev1.Pod {
				_, p := stuckFailing(3, ago(15*time.Minute))
				return p
			}(),
			observedSince: cold,
			want:          DecisionUnknown,
		},
		{
			name: "recorded success not set: a host-network pod in the slice is ignored, node stays wedged",
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
			obs := tc.observedSince
			if obs.IsZero() {
				obs = warm
			}
			got, _ := Decide(tc.node, tc.events, tc.pods, testNow, obs, tc.lastSuccessAt)
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
				if got, _ := Decide(tc.node, nil, nil, testNow, warm, time.Time{}); got != DecisionNotApplicable {
					t.Errorf("Decide() = %v, want NotApplicable to match AnyApplies", got)
				}
			}
		})
	}
}

func TestDecideNilNode(t *testing.T) {
	if got, _ := Decide(nil, nil, nil, testNow, warm, time.Time{}); got != DecisionUnknown {
		t.Errorf("Decide(nil) = %v, want Unknown", got)
	}
}

func TestDecideSnapshotOnWedge(t *testing.T) {
	node := testNode(true, true)
	events, pods := stuckFailing(3, ago(15*time.Minute))

	got, snap := Decide(node, events, pods, testNow, warm, time.Time{})
	if got != DecisionWedged {
		t.Fatalf("Decide() = %v, want Wedged", got)
	}
	if snap.DetectorName != swiftVFTeardown.name {
		t.Errorf("snapshot detector = %q, want %q", snap.DetectorName, swiftVFTeardown.name)
	}
	if snap.FailureCount != 3 {
		t.Errorf("snapshot FailureCount = %d, want 3", snap.FailureCount)
	}
	if snap.SustainedCount != 3 {
		t.Errorf("snapshot SustainedCount = %d, want 3", snap.SustainedCount)
	}
	if snap.RecentSuccess {
		t.Errorf("snapshot RecentSuccess = %v, want false", snap.RecentSuccess)
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
	got, _ := Decide(node, events, pods, testNow, warm, time.Time{})
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
	got, _ := Decide(node, events, pods, testNow, warm, time.Time{})
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
		got, snap := Decide(testNode(true, true), events, pods, testNow, warm, time.Time{})
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
		if snap.FailureCount != 1 {
			t.Errorf("FailureCount = %d, want 1 (one pod, two Events)", snap.FailureCount)
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
		if snap.FailureCount != 1 {
			t.Errorf("FailureCount = %d, want 1", snap.FailureCount)
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
