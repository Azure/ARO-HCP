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
	"regexp"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

// signatureDetector is the shared Detector base for fault families whose signal
// is an Event-reason storm matched by regex signatures, gated by a sustained
// floor, a dwell, and the load-bearing zero-successful-start check. A concrete
// family (see swift_vf.go) is a signatureDetector value with its own constants;
// it reuses every method below rather than duplicating the evaluation logic.
type signatureDetector struct {
	// name is a stable identifier used in logs, events, metrics, and the
	// detector annotation.
	name string
	// reason is a short human-readable explanation recorded on the node when the
	// detector fires.
	reason string
	// appliesTo limits the detector to nodes that can physically exhibit the
	// fault. A node it does not apply to is never a candidate. A nil predicate
	// means the detector applies to every node.
	appliesTo func(*corev1.Node) bool
	// eventReason is the kubelet Event reason that carries the failure signal.
	eventReason string
	// signatures match the failure Event message; any match is a hit.
	signatures []*regexp.Regexp
	// failuresFloor is the minimum number of distinct stuck failing pods on the
	// node required to fire. It is a floor to require the storm to be sustained,
	// not the trigger; the wedge/flap call is made by the zero-success rule.
	failuresFloor int
	// window is the rolling window over which failures and successes are counted.
	window time.Duration
	// dwell is how long the failure must have been sustained before the detector
	// fires, filtering transient bursts.
	dwell time.Duration
	// requireZeroSuccess makes firing require zero fresh pod-sandbox successes in
	// the window (no PodReadyToStartContainers=True transition). This is the
	// load-bearing discriminator between a hard-wedge (zero successes, VF gone)
	// and a flap (some successes, VF present).
	requireZeroSuccess bool
}

// Name returns the detector's stable identifier.
func (d signatureDetector) Name() string { return d.name }

// Reason returns the detector's human-readable explanation.
func (d signatureDetector) Reason() string { return d.reason }

// Window is the detector's fixed evaluation window.
func (d signatureDetector) Window() time.Duration { return d.window }

// Applies reports whether the detector is a candidate for the node. A nil
// predicate applies everywhere.
func (d signatureDetector) Applies(node *corev1.Node) bool {
	return d.appliesTo == nil || d.appliesTo(node)
}

// Evaluate reads the failure signal for this detector from the node's Events and
// Pods within the detector window. Every number it returns comes from live Pod
// state; Events only classify which pods are failing. The success signal is not
// read here (see Decide and the controller's recorded success history).
func (d signatureDetector) Evaluate(events []*corev1.Event, pods []*corev1.Pod, now time.Time) Snapshot {
	windowStart := now.Add(-d.window)
	snap := Snapshot{DetectorName: d.name, Window: d.window}

	// A pod is "failing" for this detector when it is the subject of a matching
	// failure Event seen within the window. Correlation is by the Event's
	// InvolvedObject.UID, not namespace/name: a UID is unique to a single Pod
	// object, so a recycled name (a new Pod reusing a deleted Pod's name) cannot
	// inherit the old Pod's failure Events. Events whose involved object carries
	// no UID are skipped rather than matched loosely. The Events are used only to
	// identify failing pods; they are never counted.
	failing := make(map[types.UID]bool)
	for _, ev := range events {
		if ev == nil || ev.InvolvedObject.Kind != "Pod" || ev.InvolvedObject.UID == "" {
			continue
		}
		if ev.Reason != d.eventReason || !d.matches(ev.Message) {
			continue
		}
		if eventLastTime(ev).Before(windowStart) {
			continue
		}
		failing[ev.InvolvedObject.UID] = true
	}

	for _, p := range pods {
		if p == nil || p.DeletionTimestamp != nil {
			continue
		}
		// Floor and dwell are per pod. A pod counts toward the floor only once it
		// has individually been stuck without a sandbox
		// (PodReadyToStartContainers=False) for at least the dwell, so the floor is
		// a count of pods each sustained past the dwell, not a snapshot of pods
		// stuck right now with only the oldest one aged. This is what stops a
		// single old failure alongside brand-new ones from firing, and what stops
		// one long-stuck pod from firing alone.
		if failing[p.UID] {
			if since, stuck := stuckSince(p); stuck {
				snap.FailureCount++
				if snap.StuckSince.IsZero() || since.Before(snap.StuckSince) {
					snap.StuckSince = since
				}
				if now.Sub(since) >= d.dwell {
					snap.SustainedCount++
				}
			}
		}
	}

	return snap
}

// MeetsThreshold reports whether the threshold is met: the floor of pods that
// have each individually been stuck for at least the dwell, with no recorded
// success in the window (when required and observable). A cold view that has not
// yet watched a full window can never meet the threshold, so an unobserved
// success signal is treated as indeterminate, never as zero.
func (d signatureDetector) MeetsThreshold(snap Snapshot, now, observedSince time.Time) bool {
	// The floor counts pods each sustained past the dwell (computed in Evaluate),
	// so meeting it already proves the storm held continuously; there is no
	// separate oldest-pod dwell check.
	if snap.SustainedCount < d.failuresFloor {
		return false
	}
	if d.requireZeroSuccess {
		// The success signal is only trustworthy once a full window has been
		// observed since the controller began observing; before that, treat it as
		// indeterminate and do not fire.
		if observedSince.IsZero() || now.Sub(observedSince) < d.window {
			return false
		}
		if snap.RecentSuccess {
			return false
		}
	}
	return true
}

// matches reports whether the Event message matches any of the detector's
// signature regexes.
func (d signatureDetector) matches(message string) bool {
	for _, re := range d.signatures {
		if re.MatchString(message) {
			return true
		}
	}
	return false
}

func isNodeReady(node *corev1.Node) bool {
	for _, c := range node.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// podReadyToStartCondition returns the pod's PodReadyToStartContainers condition,
// or nil if it is absent (for example when the feature gate is off).
func podReadyToStartCondition(p *corev1.Pod) *corev1.PodCondition {
	for i := range p.Status.Conditions {
		if p.Status.Conditions[i].Type == corev1.PodReadyToStartContainers {
			return &p.Status.Conditions[i]
		}
	}
	return nil
}

// stuckSince reports whether a pod is currently stuck without a sandbox
// (PodReadyToStartContainers=False) and, if so, when it entered that state. The
// timestamp is the condition's lastTransitionTime, durable across Event GC and a
// controller restart. A pod whose condition is absent is not counted as stuck, so
// when the feature gate is off the detector reads Unknown rather than wedging.
func stuckSince(p *corev1.Pod) (time.Time, bool) {
	// A pod that reached a terminal phase already ran, so its sandbox was torn
	// down on completion and PodReadyToStartContainers goes False as a matter of
	// course. That is a finished pod, not a pod that cannot start, and counting
	// it would inflate the stuck count with ordinary Job and CronJob turnover.
	// Real wedged nodes carry several of these at any time.
	if p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
		return time.Time{}, false
	}
	cond := podReadyToStartCondition(p)
	if cond == nil || cond.Status != corev1.ConditionFalse {
		return time.Time{}, false
	}
	return cond.LastTransitionTime.Time, true
}

// SuccessAt reports whether a non-host-network pod currently shows a fresh
// sandbox (PodReadyToStartContainers=True) and, if so, when that condition last
// transitioned to True: proof the pod's sandbox and its SWIFT network were
// created, which a hard-wedge cannot do. Host-network pods are excluded, since
// they reach the condition without a delegated NIC. Keying on the transition, not
// pod age, means a container restart inside an existing sandbox is not counted as
// success. The controller uses this to record a durable per-node lastSuccessAt,
// so a success survives even after its short-lived pod is deleted.
func SuccessAt(p *corev1.Pod) (time.Time, bool) {
	if p == nil || p.Spec.HostNetwork {
		return time.Time{}, false
	}
	cond := podReadyToStartCondition(p)
	if cond == nil || cond.Status != corev1.ConditionTrue {
		return time.Time{}, false
	}
	return cond.LastTransitionTime.Time, true
}

// eventLastTime returns the latest activity time of a (possibly aggregated)
// Event: its lastTimestamp, falling back to eventTime then firstTimestamp.
func eventLastTime(ev *corev1.Event) time.Time {
	if !ev.LastTimestamp.IsZero() {
		return ev.LastTimestamp.Time
	}
	if ev.EventTime.Time != (time.Time{}) && !ev.EventTime.IsZero() {
		return ev.EventTime.Time
	}
	return ev.FirstTimestamp.Time
}

func mustCompileSignatures(patterns ...string) []*regexp.Regexp {
	out := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		out = append(out, regexp.MustCompile(p))
	}
	return out
}
