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
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// nodeAt builds a SWIFT node with explicit creation and Ready-transition times,
// which is the whole evidence base for the never-ready detector.
func nodeAt(created, transitioned time.Time, ready bool, reason string) *corev1.Node {
	status := corev1.ConditionFalse
	if ready {
		status = corev1.ConditionTrue
	}
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:              nodeName,
			Labels:            map[string]string{SwiftV2LabelKey: SwiftV2LabelValue},
			CreationTimestamp: metav1.NewTime(created),
		},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{{
				Type:               corev1.NodeReady,
				Status:             status,
				Reason:             reason,
				LastTransitionTime: metav1.NewTime(transitioned),
			}},
		},
	}
}

// bornBroken is a node that registered and never reached Ready, stuck for d.
func bornBroken(d time.Duration, reason string) *corev1.Node {
	created := testNow.Add(-d)
	return nodeAt(created, created, false, reason)
}

// TestNeverReadyFiresPastDwell is the INT case: a node that registered, never
// reached Ready, and sat there. Nothing in node lifecycle rescues it, so the
// detector has to.
func TestNeverReadyFiresPastDwell(t *testing.T) {
	node := bornBroken(22*time.Hour, "KubeletNotReady")

	got, snap := Decide(node, nil, nil, testNow)
	if got != DecisionWedged {
		t.Fatalf("Decide() = %v, want Wedged", got)
	}
	if snap.DetectorName != "never-ready" {
		t.Errorf("DetectorName = %q, want never-ready", snap.DetectorName)
	}
	if snap.MatchedSignature != "KubeletNotReady" {
		t.Errorf("MatchedSignature = %q, want the Ready condition reason", snap.MatchedSignature)
	}
	if snap.Reason == "" {
		t.Error("Reason is empty; Decide must stamp the detector's reason when it fires")
	}
	if reason := snap.ReasonString(); !strings.Contains(reason, "never reached Ready") ||
		!strings.Contains(reason, "KubeletNotReady") {
		t.Errorf("ReasonString() = %q, want the never-Ready detail with the cause", reason)
	}
}

// TestNeverReadyDwell pins the boundary. Real nodes reach Ready in well under
// four minutes (dev fleet max was 3.4), so nothing legitimate is anywhere near
// the dwell, but the detector must still not fire early.
func TestNeverReadyDwell(t *testing.T) {
	tests := []struct {
		name  string
		stuck time.Duration
		want  Decision
	}{
		{name: "far inside the dwell, a node still bootstrapping", stuck: 2 * time.Minute, want: DecisionUnknown},
		{name: "just inside the dwell", stuck: neverReadyDwell - time.Second, want: DecisionUnknown},
		{name: "exactly at the dwell", stuck: neverReadyDwell, want: DecisionWedged},
		{name: "past the dwell", stuck: neverReadyDwell + time.Minute, want: DecisionWedged},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := Decide(bornBroken(tc.stuck, "KubeletNotReady"), nil, nil, testNow); got != tc.want {
				t.Errorf("Decide() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestNeverReadyLeavesLifecycleAlone is the discriminator, and the reason the
// original Ready precondition existed. A node that came up and later dropped out
// (reboot, upgrade, drain) is node lifecycle's problem, and must stay that way
// however long it has been NotReady.
func TestNeverReadyLeavesLifecycleAlone(t *testing.T) {
	created := testNow.Add(-30 * 24 * time.Hour)

	tests := []struct {
		name string
		node *corev1.Node
	}{
		{
			name: "was Ready for weeks, dropped out an hour ago",
			node: nodeAt(created, testNow.Add(-time.Hour), false, "KubeletNotReady"),
		},
		{
			name: "reached Ready just outside the tolerance, then dropped out",
			node: nodeAt(created, created.Add(neverReadyTolerance+time.Second), false, "KubeletNotReady"),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := Decide(tc.node, nil, nil, testNow); got != DecisionUnknown {
				t.Errorf("Decide() = %v, want Unknown; a node that was once Ready belongs to node lifecycle", got)
			}
		})
	}
}

// TestNeverReadyTolerance pins the registration allowance. The Ready condition is
// written a moment after the object is created, so exact equality would be too
// strict, but the allowance must stay tight enough that it cannot swallow a node
// that genuinely came up.
func TestNeverReadyTolerance(t *testing.T) {
	created := testNow.Add(-2 * time.Hour)

	inside := nodeAt(created, created.Add(neverReadyTolerance), false, "KubeletNotReady")
	if got, _ := Decide(inside, nil, nil, testNow); got != DecisionWedged {
		t.Errorf("a transition at the tolerance edge is still born broken: Decide() = %v, want Wedged", got)
	}

	outside := nodeAt(created, created.Add(neverReadyTolerance+time.Second), false, "KubeletNotReady")
	if got, _ := Decide(outside, nil, nil, testNow); got != DecisionUnknown {
		t.Errorf("a transition past the tolerance means the node reached Ready: Decide() = %v, want Unknown", got)
	}
}

// TestNeverReadyIgnoresUnusableEvidence keeps the detector quiet when the Node
// does not carry enough to evaluate the discriminator. Labeling on a guess would
// hit nodes that merely came up before this controller started watching.
func TestNeverReadyIgnoresUnusableEvidence(t *testing.T) {
	created := testNow.Add(-2 * time.Hour)

	noReadyCondition := nodeAt(created, created, false, "KubeletNotReady")
	noReadyCondition.Status.Conditions = []corev1.NodeCondition{{
		Type:   corev1.NodeMemoryPressure,
		Status: corev1.ConditionFalse,
	}}

	tests := []struct {
		name string
		node *corev1.Node
	}{
		{name: "no Ready condition at all", node: noReadyCondition},
		{name: "no creation timestamp", node: nodeAt(time.Time{}, created, false, "KubeletNotReady")},
		{name: "no Ready transition timestamp", node: nodeAt(created, time.Time{}, false, "KubeletNotReady")},
		{
			name: "clock skew: Ready transition predates creation",
			node: nodeAt(created, created.Add(-time.Minute), false, "KubeletNotReady"),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := Decide(tc.node, nil, nil, testNow)
			if got != DecisionUnknown {
				t.Errorf("Decide() = %v, want Unknown", got)
			}
		})
	}
}

// TestNeverReadyEmptyReasonStillLabels covers the kubelet leaving the Ready
// reason blank. The label is what mitigation keys on, so a missing triage detail
// must not cost us the detection.
func TestNeverReadyEmptyReasonStillLabels(t *testing.T) {
	got, snap := Decide(bornBroken(time.Hour, ""), nil, nil, testNow)
	if got != DecisionWedged {
		t.Fatalf("Decide() = %v, want Wedged", got)
	}
	if snap.MatchedSignature != "" {
		t.Errorf("MatchedSignature = %q, want empty so no signature annotation is written", snap.MatchedSignature)
	}
	if reason := snap.ReasonString(); !strings.Contains(reason, "no reason reported") {
		t.Errorf("ReasonString() = %q, want a readable stand-in for the missing reason", reason)
	}
}

// TestNeverReadyDoesNotDisturbTheReadyPath is the regression guard for adding a
// second detector to the registry. A Ready node must still be evaluated by the
// pod and event detectors exactly as before, and never-ready must contribute
// nothing to that path.
func TestNeverReadyDoesNotDisturbTheReadyPath(t *testing.T) {
	events, pods := stuckFailing(3, ago(15*time.Minute))

	got, snap := Decide(testNode(true, true), events, pods, testNow)
	if got != DecisionWedged {
		t.Fatalf("Decide() = %v, want Wedged", got)
	}
	if snap.DetectorName != "swift-vf-teardown" {
		t.Errorf("DetectorName = %q, want swift-vf-teardown to still own the Ready path", snap.DetectorName)
	}

	// The same node with no evidence stays Unknown rather than being caught by
	// never-ready, whose Evaluate has no Node to read.
	if got, _ := Decide(testNode(true, true), nil, nil, testNow); got != DecisionUnknown {
		t.Errorf("Decide(quiet Ready node) = %v, want Unknown", got)
	}
}

// TestNeverReadyOwnership pins the scope. AnyApplies runs ahead of the Ready
// gate and decides which nodes the controller holds labels on at all, so a
// non-SWIFT node must still reach NotApplicable and have any stale label retired.
func TestNeverReadyOwnership(t *testing.T) {
	node := bornBroken(22*time.Hour, "KubeletNotReady")
	node.Labels = map[string]string{}

	if got, _ := Decide(node, nil, nil, testNow); got != DecisionNotApplicable {
		t.Errorf("Decide(non-SWIFT born-broken node) = %v, want NotApplicable", got)
	}
}

// TestNeverReadyEvaluateNeverFires pins the Ready-path contract directly: the
// snapshot Evaluate returns carries no dwell evidence, so the detector cannot
// fire from a path that never saw the Node.
func TestNeverReadyEvaluateNeverFires(t *testing.T) {
	snap := neverReady.Evaluate(nil, nil, testNow)
	if !snap.StuckSince.IsZero() {
		t.Errorf("StuckSince = %v, want zero from Evaluate", snap.StuckSince)
	}
	if neverReady.MeetsThreshold(snap, testNow) {
		t.Error("MeetsThreshold(Evaluate snapshot) = true, want false")
	}
	if neverReady.MeetsThreshold(snap, testNow.Add(365*24*time.Hour)) {
		t.Error("MeetsThreshold(Evaluate snapshot, far future) = true; the dwell must not be measured from a zero time")
	}
}

// TestSnapshotDetailOverridesReasonString covers the escape hatch itself. The
// pod-centric summary is what every existing detector renders into the reason
// annotation, so it has to be byte-identical when Detail is unset.
func TestSnapshotDetailOverridesReasonString(t *testing.T) {
	base := Snapshot{SustainedCount: 2, FailureCount: 5, Window: 10 * time.Minute}

	const want = "2 pods stuck past dwell (5 stuck total), no recent success in 10m0s window"
	if got := base.ReasonString(); got != want {
		t.Errorf("ReasonString() with no Detail = %q, want %q", got, want)
	}

	withDetail := base
	withDetail.Detail = "node never reached Ready in 22h0m since creation (KubeletNotReady)"
	if got := withDetail.ReasonString(); got != withDetail.Detail {
		t.Errorf("ReasonString() with Detail = %q, want the detail verbatim", got)
	}
}

// TestProductionBornBrokenNodeFires replays the captured INT node
// aks-userswft3-65765115-vmss00000g (int-westus3, AROSLSRE-1976), the incident
// this detector was built for. Its VMSS instance failed provisioning with a
// terminal OSProvisioningClientError, it registered, never reached Ready, and
// sat for 22 hours until a human cordoned it.
//
// The capture is the reason the discriminator is what it is: the Ready
// condition's lastTransitionTime is byte-for-byte the node's creationTimestamp,
// because the condition was written once at registration and never moved again.
//
// It also pins the two things triage needs. The node carried the SWIFT v2 label,
// which is what makes the detector's ownership scope correct rather than lucky,
// and the Ready reason was KubeletNotReady, which is what lands in the signature
// annotation.
func TestProductionBornBrokenNodeFires(t *testing.T) {
	// Verbatim from the captured node.yaml.
	const (
		created      = "2026-08-31T21:32:14Z"
		transitioned = "2026-08-31T21:32:14Z"
		observedAt   = "2026-09-01T21:52:21Z" // the last heartbeat in the capture
	)
	mustParse := func(s string) time.Time {
		t.Helper()
		ts, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatalf("parse %q: %v", s, err)
		}
		return ts
	}

	node := nodeAt(mustParse(created), mustParse(transitioned), false, "KubeletNotReady")
	node.Status.Conditions[0].Message = "container runtime network not ready: NetworkReady=false " +
		"reason:NetworkPluginNotReady message:Network plugin returns error: cni plugin not initialized"
	now := mustParse(observedAt)

	if !neverReady.Applies(node) {
		t.Fatal("the captured node carried the SWIFT v2 label; the detector must own it")
	}

	got, snap := Decide(node, nil, nil, now)
	if got != DecisionWedged {
		t.Fatalf("Decide() = %v, want Wedged; the captured node sat NotReady for 22 hours", got)
	}
	// This detector reads the node, not pods, so the pod-count fields must stay
	// zero or logs and telemetry imply pod evidence that was never gathered.
	if snap.FailureCount != 0 || snap.SustainedCount != 0 {
		t.Errorf("FailureCount/SustainedCount = %d/%d, want 0/0 for a node-only detector",
			snap.FailureCount, snap.SustainedCount)
	}
	if snap.DetectorName != "never-ready" {
		t.Errorf("DetectorName = %q, want never-ready", snap.DetectorName)
	}
	if snap.MatchedSignature != "KubeletNotReady" {
		t.Errorf("MatchedSignature = %q, want KubeletNotReady", snap.MatchedSignature)
	}

	// It must fire far earlier than the 22 hours we actually took, and not before
	// the dwell.
	dwellHit := mustParse(created).Add(neverReadyDwell)
	if got, _ := Decide(node, nil, nil, dwellHit); got != DecisionWedged {
		t.Errorf("Decide(at the dwell) = %v, want Wedged", got)
	}
	if got, _ := Decide(node, nil, nil, dwellHit.Add(-time.Second)); got != DecisionUnknown {
		t.Errorf("Decide(one second before the dwell) = %v, want Unknown", got)
	}
}
