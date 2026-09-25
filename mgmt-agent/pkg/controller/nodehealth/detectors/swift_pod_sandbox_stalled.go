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

package detectors

import (
	"slices"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/Azure/ARO-HCP/internal/kuberesources"
)

const SwiftPodSandboxStalled = "swift-pod-sandbox-stalled"

type swiftPodSandboxStalledDetector struct{}

func (swiftPodSandboxStalledDetector) Name() string { return SwiftPodSandboxStalled }
func (swiftPodSandboxStalledDetector) Scope() Scope { return PodScope }
func (swiftPodSandboxStalledDetector) Reason() string {
	return "initial SWIFT pod sandbox has sustained network failures"
}
func (swiftPodSandboxStalledDetector) Applies(node *corev1.Node) bool {
	return node != nil && isSwiftV2Node(node) && isNodeReady(node)
}
func (swiftPodSandboxStalledDetector) Window() time.Duration { return time.Minute }

// EvaluatePod requires a sustained, fresh failure span for an initial
// sandbox. Event counts are not evidence of elapsed time or separate failures.
func (d swiftPodSandboxStalledDetector) EvaluatePod(pod *corev1.Pod, events []*corev1.Event, now time.Time) (time.Time, time.Time, bool) {
	if !kuberesources.PodRequestsSwiftNIC(pod) || pod.UID == "" ||
		pod.Spec.NodeName == "" || pod.Spec.HostNetwork || pod.DeletionTimestamp != nil {
		return time.Time{}, time.Time{}, false
	}
	since, stuck := stuckSince(pod)
	if !stuck || since.IsZero() || since.After(now) || pod.CreationTimestamp.IsZero() {
		return time.Time{}, time.Time{}, false
	}
	for _, statuses := range [][]corev1.ContainerStatus{pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses} {
		for _, status := range statuses {
			if status.RestartCount != 0 || status.State.Running != nil ||
				status.State.Terminated != nil || status.LastTerminationState.Terminated != nil ||
				(status.Started != nil && *status.Started) {
				return time.Time{}, time.Time{}, false
			}
		}
	}
	var scheduled time.Time
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodScheduled && condition.Status == corev1.ConditionTrue {
			scheduled = condition.LastTransitionTime.Time
		}
	}
	if scheduled.IsZero() || scheduled.After(now) {
		return time.Time{}, time.Time{}, false
	}
	if scheduled.After(since) {
		since = scheduled
	}
	if pod.CreationTimestamp.After(since) {
		since = pod.CreationTimestamp.Time
	}
	type interval struct{ start, end time.Time }
	var intervals []interval
	for _, event := range events {
		if event == nil || event.InvolvedObject.Kind != "Pod" ||
			event.InvolvedObject.UID != pod.UID || event.InvolvedObject.Namespace != pod.Namespace ||
			event.Source.Host != pod.Spec.NodeName || event.Reason != reasonFailedCreatePodSandBox {
			continue
		}
		if _, matches := swiftVFTeardown.matchSignature(event.Message); !matches {
			continue
		}
		start := event.FirstTimestamp.Time
		if start.IsZero() {
			start = event.EventTime.Time
		}
		end := eventLastTime(event)
		if event.Series != nil && event.Series.LastObservedTime.After(end) {
			end = event.Series.LastObservedTime.Time
		}
		if start.IsZero() || end.IsZero() || end.Before(start) || end.After(now) ||
			now.Sub(end) > d.Window() || start.Before(pod.CreationTimestamp.Time) {
			continue
		}
		if start.Before(since) {
			start = since
		}
		if !end.Before(start) {
			intervals = append(intervals, interval{start, end})
		}
	}
	slices.SortFunc(intervals, func(a, b interval) int { return a.start.Compare(b.start) })
	merged := intervals[:0]
	for _, span := range intervals {
		if len(merged) == 0 || span.start.After(merged[len(merged)-1].end) {
			merged = append(merged, span)
		} else if span.end.After(merged[len(merged)-1].end) {
			merged[len(merged)-1].end = span.end
		}
	}
	for i := len(merged) - 1; i >= 0; i-- {
		if span := merged[i]; span.end.Sub(span.start) >= d.Window() {
			return span.start, span.end, true
		}
	}
	return time.Time{}, time.Time{}, false
}
