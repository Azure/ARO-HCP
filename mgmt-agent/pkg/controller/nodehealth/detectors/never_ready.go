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
	"time"

	corev1 "k8s.io/api/core/v1"
)

const (
	// neverReadyDwell is how long a node may sit having never reached Ready
	// before it is called wedged.
	//
	// Measured on the dev fleet over 14 days, 1119 nodes, from creationTimestamp
	// to the first Ready=True: p50 1.0m, p90 1.3m, p99 3.1m, max 3.4m. Every node
	// that was going to come up did so inside 3.4 minutes. 30 minutes is roughly
	// nine times that worst legitimate case, so bootstrap variance cannot reach
	// it, and it is still far inside the 22 hours the INT node
	// aks-userswft3-65765115-vmss00000g actually sat NotReady before a human
	// cordoned it.
	neverReadyDwell = 30 * time.Minute

	// neverReadyTolerance is how far after creation the Ready condition's first
	// transition may fall while the node still counts as never having been Ready.
	// The condition is written at registration, a moment after the object is
	// created, so exact equality would be too strict. It is far below the
	// dwell, so it cannot let a node that genuinely came up and later failed be
	// read as born broken.
	neverReadyTolerance = 2 * time.Minute
)

// neverReady detects a node that registered and never reached Ready.
//
// The healer's Ready precondition exists because a node that was Ready and
// dropped out (reboot, upgrade, drain) belongs to node lifecycle. That premise
// does not hold for a node that never got there: nothing in node lifecycle
// rescues it, so it sits NotReady until a human notices. The INT node above sat
// for 22 hours after its VMSS instance failed provisioning with a terminal
// OSProvisioningClientError.
//
// The detector is deliberately generic rather than keyed on a cause. Dead IMDS,
// a failed disk, a kubelet certificate problem and a failed image pull are all
// born broken and all remedied the same way, by reimaging the instance, and one
// detector is one fault with one remedy. The cause is carried as the signature
// annotation for triage instead. That also keeps the evidence to the Node object
// alone: a cause-specific detector would have to read azure-cns container logs,
// which this controller never does.
var neverReady = neverReadyDetector{}

type neverReadyDetector struct{}

// Name returns the detector's stable identifier.
func (neverReadyDetector) Name() string { return "never-ready" }

// Reason returns the human-readable explanation recorded on a labeled node.
func (neverReadyDetector) Reason() string {
	return "node registered but never reached Ready: node lifecycle does not rescue a node that never started, so it needs reimaging"
}

// Applies scopes the detector to SWIFT-v2 nodes, matching swiftVFTeardown.
//
// The fault itself is not SWIFT-specific, so this is narrower than the fault.
// It is deliberate. AnyApplies runs ahead of the Ready gate and is what decides
// which nodes the controller holds labels on at all: a detector that applied to
// every node would make DecisionNotApplicable unreachable, and that is the only
// thing that retires a label left on a node that stopped being a detector's
// concern. Widening ownership is a separate decision with its own consequences,
// and the INT node this was built for was in a userswft pool anyway.
func (neverReadyDetector) Applies(node *corev1.Node) bool { return isSwiftV2Node(node) }

// Window returns the dwell. This detector has no lookback over Events, so the
// dwell is the only duration it has.
func (neverReadyDetector) Window() time.Duration { return neverReadyDwell }

// Evaluate satisfies Detector for the Ready path, where this detector has
// nothing to say. Its evidence is the Node object, which Evaluate does not
// receive, so it returns an empty snapshot that MeetsThreshold always rejects.
// The real work is in EvaluateNode.
func (d neverReadyDetector) Evaluate(_ []*corev1.Event, _ []*corev1.Pod, _ time.Time) Snapshot {
	return Snapshot{DetectorName: d.Name(), Window: neverReadyDwell}
}

// EvaluateNode reads the born-broken signal from the Node alone.
//
// The discriminator is that the Ready condition has never been True and its
// first transition coincides with the node's creation. A node that reached Ready
// at any point has a strictly later transition, which is what separates "never
// started" from "started and later failed" and leaves the latter to node
// lifecycle.
func (d neverReadyDetector) EvaluateNode(node *corev1.Node, now time.Time) Snapshot {
	snap := Snapshot{DetectorName: d.Name(), Window: neverReadyDwell}
	if node == nil {
		return snap
	}
	cond := nodeReadyCondition(node)
	if cond == nil || cond.Status == corev1.ConditionTrue {
		return snap
	}

	created := node.CreationTimestamp.Time
	transitioned := cond.LastTransitionTime.Time
	if created.IsZero() || transitioned.IsZero() {
		// Without both timestamps the discriminator cannot be evaluated, and
		// guessing would risk labeling a node that simply came up before this
		// controller started watching.
		return snap
	}
	if transitioned.Before(created) {
		// A Ready transition predating the object cannot happen; treat it as bad
		// data rather than evidence.
		return snap
	}
	if transitioned.After(created.Add(neverReadyTolerance)) {
		// The node was Ready at some point and lost it later. Node lifecycle owns
		// that case, exactly as it did before this detector existed.
		return snap
	}

	snap.StuckSince = created
	// FailureCount and SustainedCount stay zero: they are defined as counts of
	// stuck pods, and this detector reads the node only. Reporting a pod count
	// here would make logs and telemetry imply pod evidence that does not exist.
	// MeetsThreshold keys on StuckSince, so leaving them zero changes nothing.
	//
	// The Ready condition's reason names the cause within the family, which is
	// what the signature annotation is for. It is triage detail only; mitigation
	// keys on the detector name.
	snap.MatchedSignature = cond.Reason
	snap.Detail = fmt.Sprintf("node never reached Ready in %s since creation (%s)",
		now.Sub(created).Round(time.Minute), readyReasonOrUnknown(cond.Reason))
	return snap
}

// MeetsThreshold reports whether the node has been never-Ready for the dwell.
// A snapshot from Evaluate carries no StuckSince, so the Ready path can never
// fire this detector.
func (neverReadyDetector) MeetsThreshold(snap Snapshot, now time.Time) bool {
	if snap.StuckSince.IsZero() {
		return false
	}
	return now.Sub(snap.StuckSince) >= neverReadyDwell
}

// nodeReadyCondition returns the node's Ready condition, or nil when it is
// absent. It is kept here rather than folded into isNodeReady so this detector
// adds no edits to the shared helpers.
func nodeReadyCondition(node *corev1.Node) *corev1.NodeCondition {
	for i := range node.Status.Conditions {
		if node.Status.Conditions[i].Type == corev1.NodeReady {
			return &node.Status.Conditions[i]
		}
	}
	return nil
}

// readyReasonOrUnknown keeps the reason annotation readable when the kubelet
// left the Ready condition's reason empty.
func readyReasonOrUnknown(reason string) string {
	if reason == "" {
		return "no reason reported"
	}
	return reason
}
