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
	"time"

	corev1 "k8s.io/api/core/v1"
)

// AnyApplies reports whether any node-scoped detector owns this node. It reads only the
// node, so a caller can answer the ownership question before doing the work of
// gathering the node's Pods and Events. Decide applies the same gate, so a node
// this rejects can only ever produce DecisionNotApplicable.
func (r *Registry) AnyApplies(node *corev1.Node) bool {
	if node == nil {
		return false
	}
	for _, d := range r.detectors {
		if d.Scope() == NodeScope && d.Applies(node) {
			return true
		}
	}
	return false
}

// Decide is the pure core of the controller: given a node, the Events and Pods
// currently held for it, and a clock, it returns the desired health state. It
// performs no I/O, keeps no state between calls, and is exhaustively
// table-tested. Every input is something a LIST can hand back, so a controller
// that has just restarted decides exactly what a long-running one would.
func (r *Registry) Decide(node *corev1.Node, events []*corev1.Event, pods []*corev1.Pod, now time.Time) (Decision, Snapshot) {
	if node == nil {
		return DecisionUnknown, Snapshot{}
	}
	// Ownership precondition, checked before readiness: if no detector applies,
	// none can ever fire for this node, so any label we left on it is stale and
	// must be retired. This is deliberately evaluated ahead of the Ready gate,
	// because a node that is not a detector's concern is not ours to hold a label
	// on whether it is Ready or not.
	if !r.AnyApplies(node) {
		return DecisionNotApplicable, Snapshot{}
	}
	// Node-Ready precondition. A node that was Ready and dropped out (reboot,
	// upgrade, drain) is left to node lifecycle, which rescues it. A node that
	// never reached Ready is not rescued by anything, so it is ours.
	if !NodeReady(node) {
		for _, detector := range r.detectors {
			d, ok := detector.(NodeDetector)
			if !ok || d.Scope() != NodeScope || !d.Applies(node) {
				continue
			}
			if decision, snap := d.EvaluateNode(node, now); decision == DecisionWedged {
				snap.Reason = d.Reason()
				return DecisionWedged, snap
			}
		}
		// Nothing fired. Stay Unknown rather than Healthy: a NotReady node is not
		// evidence of recovery, so an existing wedged label is retained.
		return DecisionUnknown, Snapshot{}
	}

	sawSuccess := false
	for _, detector := range r.detectors {
		d, ok := detector.(PodDetector)
		if !ok || d.Scope() != NodeScope || !d.Applies(node) {
			continue
		}
		snap := d.Evaluate(events, pods, now)
		if snap.Pods != nil && snap.Pods.RecentSuccess {
			sawSuccess = true
		}
		if d.MeetsThreshold(snap, now) {
			snap.Reason = d.Reason()
			return DecisionWedged, snap
		}
	}

	// No detector fired. Only declare recovery on positive evidence (a success in
	// the window). An empty view stays Unknown so an existing wedged label is
	// retained until recovery is actually observed, rather than being dropped
	// because the node happens to be quiet.
	if sawSuccess {
		return DecisionHealthy, Snapshot{}
	}
	return DecisionUnknown, Snapshot{}
}
