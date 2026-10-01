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

package detection

import (
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

// Decision is the desired health state a reconcile derives for a node. It is the
// output of the pure Decide function and drives the label/unlabel action.
type Decision int

const (
	// DecisionUnknown means the evidence is insufficient to make a call: leave
	// the node's label exactly as it is. This covers a NotReady node (deferred to
	// node lifecycle) and a quiet node showing neither a sustained failure storm
	// nor a success in the window, so an existing wedged label is retained until
	// recovery is positively observed.
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

// Detector is what every fault family has in common: an identity and a scope.
// Evaluation interfaces distinguish the evidence and result scope. A detector implements this
// plus exactly one of PodDetector, NodeDetector or PodScopedDetector.
type Detector interface {
	// Name is a stable identifier used in logs, events, metrics, and the
	// detector annotation.
	Name() string
	// Scope identifies whether the result describes a node or an individual Pod.
	Scope() Scope
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
}

// PodDetector reads a node's health from the Pods and Events held for it. It
// runs only on a Ready node, where a pod population exists to be read.
type PodDetector interface {
	Detector
	// Evaluate reads the detector's signals for the node from the Events and Pods
	// currently held for it. Both the failure and the success signal come out of
	// the Pods passed in, so the result depends on nothing but what a LIST
	// returns.
	Evaluate(events []*corev1.Event, pods []*corev1.Pod, now time.Time) Snapshot
	// MeetsThreshold reports whether the evidence already gathered from the
	// informers meets this detector's threshold. It is a pure predicate over that
	// evidence, not an edge trigger: the reconcile is level-driven, so this is
	// asked again on every pass from the current state.
	MeetsThreshold(snap Snapshot, now time.Time) bool
}

// NodeDetector reads a node's health from the Node object alone. It runs only on
// a node that is not Ready, whose subject never started and so has no pod
// population worth reading.
//
// It returns the Decision rather than a snapshot plus a separate threshold
// predicate. The two are one judgement here, and splitting them would mean
// handing the Ready path a snapshot it must be trusted not to act on.
type NodeDetector interface {
	Detector
	// EvaluateNode decides the node's state from the Node alone. It performs no
	// I/O and depends on nothing a LIST cannot return, exactly as Evaluate.
	// It returns DecisionWedged with the supporting evidence, or DecisionUnknown.
	EvaluateNode(node *corev1.Node, now time.Time) (Decision, Snapshot)
}

// PodEvidence is the pod-derived evidence a PodDetector gathered. It is a
// separate type so that a detector which reads no pods has no pod counts to
// report: it leaves Snapshot.Pods nil, rather than carrying zeroes that read as
// "no pods were stuck" when the truth is "pods were never the evidence".
type PodEvidence struct {
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
	// RecentSuccess reports whether any pod on the node proves a sandbox was built
	// inside the window (see SuccessAt). It is read from the same Pods the failure
	// signal is read from, so it needs no history and survives a restart.
	RecentSuccess bool
}

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
	// Pods is the pod-derived evidence, set by a PodDetector and nil for a
	// NodeDetector, whose evidence is the Node object alone.
	Pods *PodEvidence
	// StuckSince is the dwell signal: for a PodDetector the oldest
	// PodReadyToStartContainers=False lastTransitionTime among the node's
	// currently-stuck failing pods, for a NodeDetector the point the node's own
	// state started. Both are read from durable state, so neither depends on
	// Event retention.
	StuckSince time.Time
	// MatchedSignature names the failure mode inside the detector's family, as its
	// raw pattern. It is triage detail only: it saves an operator reading Events
	// that may already have been collected. It is never a decision input, and
	// mitigation must key on DetectorName, not on this: one detector is one fault
	// with one remedy, and branching on the signature would require knowing
	// detector internals.
	MatchedSignature string
	// Detail is the reason-annotation summary for a detector whose evidence is
	// not pod counts. ReasonString renders it in place of the pod-centric summary.
	Detail string
}

// ReasonString renders a short human-readable summary for the reason annotation.
func (s Snapshot) ReasonString() string {
	if s.Pods == nil {
		return s.Detail
	}
	success := "no recent success"
	if s.Pods.RecentSuccess {
		success = "a recent success"
	}
	return fmt.Sprintf("%d pods stuck past dwell (%d stuck total), %s in %s window",
		s.Pods.SustainedCount, s.Pods.FailureCount, success, s.Window)
}

// Detection is scoped fault evidence, not authorization to mutate a resource.
type Detection struct {
	Detector     string
	Scope        Scope
	NodeUID      types.UID
	PodUIDs      []types.UID
	Since        time.Time
	LastEvidence time.Time
}

// PodScopedDetector evaluates an individual Pod, not its node's health.
type PodScopedDetector interface {
	Detector
	EvaluatePod(pod *corev1.Pod, events []*corev1.Event, now time.Time) (since, latest time.Time, matches bool)
}

type Scope string

const (
	NodeScope Scope = "node"
	PodScope  Scope = "pod"
)
