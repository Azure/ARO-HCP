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

// Package detectors holds the pure detection core of the node-health controller.
// It decides, from a node plus the Events and Pods currently held for it,
// whether the node is wedged, and is free of any Kubernetes I/O so it can be
// exhaustively table-tested. The controller package consumes Decide and acts on
// the returned Decision by labeling or unlabeling the node.
package detectors

import (
	"fmt"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// Decision is the desired health state a reconcile derives for a node. It is the
// output of the pure Decide function and drives the label/unlabel action.
type Decision int

const (
	// DecisionUnknown means the evidence is insufficient to make a call: leave
	// the node's label exactly as it is. This covers a NotReady node (deferred to
	// node lifecycle) and the cold view just after a controller restart, so an
	// existing wedged label is retained until recovery is positively observed.
	DecisionUnknown Decision = iota
	// DecisionHealthy means recovery is confirmed (pods starting again): ensure
	// the node is not labeled.
	DecisionHealthy
	// DecisionWedged means a detector fired: ensure the node is labeled.
	DecisionWedged
	// DecisionNotApplicable means no detector applies to the node at all, so no
	// detector can ever mark it wedged: ensure the node is not labeled. This is
	// a definitive statement about ownership, unlike DecisionUnknown which is a
	// statement about evidence, and it is what retires a label left behind on a
	// node that has since stopped being a detector's concern.
	DecisionNotApplicable
)

func (d Decision) String() string {
	switch d {
	case DecisionHealthy:
		return "Healthy"
	case DecisionWedged:
		return "Wedged"
	case DecisionNotApplicable:
		return "NotApplicable"
	default:
		return "Unknown"
	}
}

// Detector is one fault family. Each concrete detector lives in its own file and
// contributes only its specifics (applicability, signals, thresholds); the
// evaluation primitives are shared through a common base (see signatureDetector).
// Decide iterates the registry and never depends on a concrete type, so adding a
// family is adding a Detector, not editing Decide.
type Detector interface {
	// Name is a stable identifier used in logs, events, metrics, and the
	// detector annotation.
	Name() string
	// Reason is a short human-readable explanation recorded on the node when the
	// detector fires.
	Reason() string
	// Applies reports whether the detector is a candidate for the node. A node it
	// does not apply to is never evaluated.
	Applies(node *corev1.Node) bool
	// Window is the detector's evaluation window. It is a fixed property of the
	// detector, independent of any node, so callers that only need the window do
	// not have to evaluate the detector to learn it.
	Window() time.Duration
	// Evaluate reads the detector's failure signals for the node from the Events
	// and Pods currently held for it. The success signal is not read here: it is
	// a recorded per-node history the controller passes into Decide, since a
	// point-in-time scan cannot see a success whose Pod was already deleted.
	Evaluate(events []*corev1.Event, pods []*corev1.Pod, now time.Time) Snapshot
	// MeetsThreshold reports whether the evidence already gathered from the
	// informers meets this detector's threshold. It is a pure predicate over that
	// evidence, not an edge trigger: the reconcile is level-driven, so this is
	// asked again on every pass from the current state. observedSince is when the
	// controller began observing (caches synced, or the last disabled->enabled
	// transition); the threshold is not met until a full window has been watched,
	// so a cold view is never misread as zero successes.
	MeetsThreshold(snap Snapshot, now, observedSince time.Time) bool
}

// registry is the hard-coded set of detectors. A new fault family is a new
// Detector added here, reusing the shared primitives, shipped and tested as code.
var registry = []Detector{swiftVFTeardown}

// AnyApplies reports whether any detector owns this node. It reads only the
// node, so a caller can answer the ownership question before doing the work of
// gathering the node's Pods and Events. Decide applies the same gate, so a node
// this rejects can only ever produce DecisionNotApplicable.
func AnyApplies(node *corev1.Node) bool {
	if node == nil {
		return false
	}
	for _, d := range registry {
		if d.Applies(node) {
			return true
		}
	}
	return false
}

// MaxWindow returns the longest evaluation window across all detectors. The
// controller uses it to bound its recorded per-node success history: a success
// older than the widest window can never affect any detector, so it can be
// pruned. The registry is hard-coded and the windows are fixed, so the result is
// computed once: the controller calls this on every reconcile while holding the
// observation lock.
var MaxWindow = sync.OnceValue(func() time.Duration {
	var max time.Duration
	for _, d := range registry {
		if w := d.Window(); w > max {
			max = w
		}
	}
	return max
})

// Snapshot is the read-only evidence a detector evaluated for a node, retained
// for logging, the reason annotation, and the firing decision. Its fields are
// exported so the controller and labeler can render them.
type Snapshot struct {
	// DetectorName is the name of the detector this snapshot belongs to.
	DetectorName string
	// Reason is the detector's human-readable explanation (set when it fires).
	Reason string
	// Window is the detector's evaluation window, for the reason string.
	Window time.Duration
	// FailureCount is the number of distinct pods on the node currently stuck
	// without a sandbox (PodReadyToStartContainers=False) that are the subject of
	// a matching failure Event in the window, regardless of how long each has been
	// stuck. It is a live count of Pod state (not a windowed tally of Events),
	// reported for observability.
	FailureCount int
	// SustainedCount is the subset of FailureCount whose pods have each
	// individually been stuck for at least the dwell. It is the count the floor is
	// tested against, so the floor means "this many pods each stuck past the
	// dwell", not "this many stuck right now with only the oldest one aged".
	SustainedCount int
	// RecentSuccess reports whether the controller has a recorded per-node success
	// (a non-host-network PodReadyToStartContainers=True transition) inside the
	// window. It is set by Decide from the recorded lastSuccessAt, not from a scan
	// of the pods passed in, so a success whose Pod was already deleted still
	// counts.
	RecentSuccess bool
	// StuckSince is the oldest PodReadyToStartContainers=False lastTransitionTime
	// among the node's currently-stuck failing pods, a GC-independent dwell signal
	// read from durable Pod state, reported for observability.
	StuckSince time.Time
}

// ReasonString renders a short human-readable summary for the reason annotation.
func (s Snapshot) ReasonString() string {
	success := "no recent success"
	if s.RecentSuccess {
		success = "a recent success"
	}
	return fmt.Sprintf("%d pods stuck past dwell (%d stuck total), %s in %s window",
		s.SustainedCount, s.FailureCount, success, s.Window)
}

// Decide is the pure core of the controller: given a node, the Events and Pods
// currently held for it, a clock, the time the controller began observing, and
// the node's recorded last success time, it returns the desired health state. It
// performs no I/O and is exhaustively table-tested.
func Decide(node *corev1.Node, events []*corev1.Event, pods []*corev1.Pod, now, observedSince, lastSuccessAt time.Time) (Decision, Snapshot) {
	if node == nil {
		return DecisionUnknown, Snapshot{}
	}
	// Ownership precondition, checked before readiness: if no detector applies,
	// none can ever fire for this node, so any label we left on it is stale and
	// must be retired. This is deliberately evaluated ahead of the Ready gate,
	// because a node that is not a detector's concern is not ours to hold a label
	// on whether it is Ready or not.
	if !AnyApplies(node) {
		return DecisionNotApplicable, Snapshot{}
	}
	// Node-Ready precondition: a NotReady node is left to node lifecycle.
	if !isNodeReady(node) {
		return DecisionUnknown, Snapshot{}
	}

	sawSuccess := false
	for _, d := range registry {
		if !d.Applies(node) {
			continue
		}
		snap := d.Evaluate(events, pods, now)
		// A recorded success counts when it falls inside this detector's window.
		// The distance is measured in absolute terms: the timestamp comes from a
		// Pod condition stamped by the kubelet's clock, so skew can place it in the
		// future, and a signed comparison would read any future timestamp as an
		// arbitrarily fresh success and suppress detection until the wall clock
		// caught up. Skew inside the window is benign, beyond it the timestamp is
		// not usable evidence either way.
		if !lastSuccessAt.IsZero() && now.Sub(lastSuccessAt).Abs() < snap.Window {
			snap.RecentSuccess = true
			sawSuccess = true
		}
		if d.MeetsThreshold(snap, now, observedSince) {
			snap.Reason = d.Reason()
			return DecisionWedged, snap
		}
	}

	// No detector fired. Only declare recovery on positive evidence (a recorded
	// success in the window). An empty/insufficient view stays Unknown so an
	// existing wedged label is retained across a restart until recovery is
	// observed.
	if sawSuccess {
		return DecisionHealthy, Snapshot{}
	}
	return DecisionUnknown, Snapshot{}
}
