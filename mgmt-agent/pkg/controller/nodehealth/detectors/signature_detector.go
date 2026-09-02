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
	// successScope narrows which pods may serve as success evidence. A nil scope
	// counts every pod, which is right for a fault that breaks a node's only pod
	// network. A fault that breaks one of several networking paths needs the
	// scope, because pods that never travel the broken path keep starting
	// normally and would otherwise mask it forever. See swiftVFTeardown.
	successScope func(*corev1.Pod) bool
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

// Evaluate reads both the failure and the success signal for this detector from
// the node's Events and Pods within the detector window. Every number it returns
// comes from live Pod state; Events only classify which pods are failing. Both
// signals are derived from the Pods passed in, so the whole evaluation is a pure
// function of what a LIST returns and nothing has to be remembered between calls.
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
	//
	// The value is the index of the signature that classified the pod. When a pod
	// has Events matching several signatures, the lowest index wins, so the
	// classification does not depend on the order the informer hands back Events.
	failing := make(map[types.UID]int)
	for _, ev := range events {
		if ev == nil || ev.InvolvedObject.Kind != "Pod" || ev.InvolvedObject.UID == "" {
			continue
		}
		if ev.Reason != d.eventReason {
			continue
		}
		idx, ok := d.matchSignature(ev.Message)
		if !ok {
			continue
		}
		if eventLastTime(ev).Before(windowStart) {
			continue
		}
		if prev, seen := failing[ev.InvolvedObject.UID]; !seen || idx < prev {
			failing[ev.InvolvedObject.UID] = idx
		}
	}

	// Tally the classifications of exactly the pods counted in FailureCount, so
	// the reported signature describes this snapshot and not Events for pods that
	// are no longer stuck.
	sigCounts := make([]int, len(d.signatures))

	for _, p := range pods {
		if p == nil {
			continue
		}
		// Success is read from every pod the LIST returns, including one that is
		// terminating: a pod that got a sandbox proves the node could build one,
		// and that stays true while it is being torn down.
		//
		// When the detector sets a successScope, only pods inside it are read.
		// A success proves the node can build the kind of sandbox that pod
		// needed, and nothing more, so a pod that never exercises the broken
		// path is not evidence the path works.
		if d.inSuccessScope(p) {
			if at, ok := SuccessAt(p); ok && now.Sub(at).Abs() < d.window {
				snap.RecentSuccess = true
			}
		}
		if p.DeletionTimestamp != nil {
			continue
		}
		// Floor and dwell are per pod. A pod counts toward the floor only once it
		// has individually been stuck without a sandbox
		// (PodReadyToStartContainers=False) for at least the dwell, so the floor is
		// a count of pods each sustained past the dwell, not a snapshot of pods
		// stuck right now with only the oldest one aged. This is what stops a
		// single old failure alongside brand-new ones from firing, and what stops
		// one long-stuck pod from firing alone.
		if idx, ok := failing[p.UID]; ok {
			if since, stuck := stuckSince(p); stuck {
				snap.FailureCount++
				sigCounts[idx]++
				if snap.StuckSince.IsZero() || since.Before(snap.StuckSince) {
					snap.StuckSince = since
				}
				if now.Sub(since) >= d.dwell {
					snap.SustainedCount++
				}
			}
		}
	}

	// Dominant signature: the one classifying the most counted pods, ties broken
	// by declaration order so the result is stable across evaluations.
	best := -1
	for i, n := range sigCounts {
		if n > 0 && (best < 0 || n > sigCounts[best]) {
			best = i
		}
	}
	if best >= 0 {
		snap.MatchedSignature = d.signatures[best].String()
	}

	return snap
}

// MeetsThreshold reports whether the threshold is met: the floor of pods that
// have each individually been stuck for at least the dwell, with no success in
// the window (when required). It is a pure predicate over the snapshot, which is
// itself a pure function of the Pods and Events a LIST returns, so a restarted
// controller reaches the same verdict as one that has been running for hours.
func (d signatureDetector) MeetsThreshold(snap Snapshot, now time.Time) bool {
	// The floor counts pods each sustained past the dwell (computed in Evaluate),
	// so meeting it already proves the storm held continuously; there is no
	// separate oldest-pod dwell check.
	if snap.SustainedCount < d.failuresFloor {
		return false
	}
	if d.requireZeroSuccess && snap.RecentSuccess {
		return false
	}
	return true
}

// inSuccessScope reports whether a pod may serve as success evidence for this
// detector. A detector with no scope accepts every pod.
func (d signatureDetector) inSuccessScope(p *corev1.Pod) bool {
	return d.successScope == nil || d.successScope(p)
}

// matchSignature returns the index of the first of the detector's signature
// regexes that matches the Event message.
func (d signatureDetector) matchSignature(message string) (int, bool) {
	for i, re := range d.signatures {
		if re.MatchString(message) {
			return i, true
		}
	}
	return 0, false
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

// SuccessAt reports whether a pod proves the node built it a sandbox and, if so,
// when: proof the pod's SWIFT network was created, which a hard-wedge cannot do.
// Host-network pods are excluded, since they reach a running state without a
// delegated NIC.
//
// Two shapes count, and both are read straight off the Pod, so the signal is
// fully reconstructible from a LIST:
//
//   - A live pod showing PodReadyToStartContainers=True, timed by the condition's
//     transition. Keying on the transition, not pod age, means a container
//     restart inside an existing sandbox is not counted: the sandbox is what
//     needs the network, and restarting a container in one proves nothing.
//   - A pod that has reached a terminal phase with a container that ran exactly
//     once, timed by that container's start. A finished pod drops
//     PodReadyToStartContainers back to False as a matter of course, so without
//     this a node whose recent traffic was short-lived Job and CronJob pods would
//     read as having had no success at all. A container can only start inside a
//     sandbox, so a first start is proof the sandbox was built, and this cannot be
//     reached by a pod that failed to get a network.
func SuccessAt(p *corev1.Pod) (time.Time, bool) {
	if p == nil || p.Spec.HostNetwork {
		return time.Time{}, false
	}
	if p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
		return firstRunStart(p)
	}
	cond := podReadyToStartCondition(p)
	if cond == nil || cond.Status != corev1.ConditionTrue {
		return time.Time{}, false
	}
	return cond.LastTransitionTime.Time, true
}

// firstRunStart returns the most recent start among a terminal pod's containers
// that ran exactly once and terminated.
//
// The restart count is the load-bearing part. A container's terminated state
// describes its latest run, so on a container that restarted, StartedAt is the
// start of a run inside the sandbox the pod already had. An established sandbox
// survives the VF teardown, so that run needs no working network and proves
// nothing: counting it would let a wedged node suppress its own detection, the
// same reason kubelet Started events are unusable here. A restart count of zero
// means the terminated state is the container's one and only run, which can only
// have followed a sandbox the node built. Containers that did restart are simply
// not evidence, in either direction.
//
// Only the terminated state is read: a waiting container never got as far as
// needing a sandbox. Taking the latest qualifying start keeps the timestamp as
// close as possible to when the node last demonstrably had working networking.
func firstRunStart(p *corev1.Pod) (time.Time, bool) {
	var latest time.Time
	for _, css := range [][]corev1.ContainerStatus{p.Status.InitContainerStatuses, p.Status.ContainerStatuses} {
		for _, cs := range css {
			if cs.RestartCount != 0 {
				continue
			}
			t := cs.State.Terminated
			if t == nil || t.StartedAt.IsZero() {
				continue
			}
			if latest.IsZero() || t.StartedAt.After(latest) {
				latest = t.StartedAt.Time
			}
		}
	}
	return latest, !latest.IsZero()
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
