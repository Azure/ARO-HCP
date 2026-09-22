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
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/Azure/ARO-HCP/internal/kuberesources"
)

const SwiftPodSandboxStalled = "swift-pod-sandbox-stalled"

// Detection is fresh evidence for a mitigation candidate, not an authorization
// to mutate it. The node-wide wedge verdict remains independent of pod rescue.
type Detection struct {
	Detector     string
	NodeUID      types.UID
	PodUIDs      []types.UID
	Since        time.Time
	LastEvidence time.Time
}

// MitigationDetections reuses the node-health evidence core without changing
// Decide's node-wide health label or its zero-success requirement.
func MitigationDetections(node *corev1.Node, events []*corev1.Event, pods []*corev1.Pod, now time.Time) []Detection {
	if node == nil || node.UID == "" || node.DeletionTimestamp != nil {
		return nil
	}
	var result []Detection
	if decision, snapshot := Decide(node, events, pods, now); decision == DecisionWedged {
		result = append(result, Detection{
			Detector: snapshot.DetectorName, NodeUID: node.UID,
			Since: snapshot.StuckSince, LastEvidence: now,
		})
	}
	if !isSwiftV2Node(node) || !isNodeReady(node) {
		return result
	}
	for _, pod := range pods {
		if pod == nil || pod.Spec.NodeName != node.Name {
			continue
		}
		if since, latest, ok := SwiftSandboxStalled(pod, events, now); ok {
			result = append(result, Detection{
				Detector: SwiftPodSandboxStalled, NodeUID: node.UID,
				PodUIDs: []types.UID{pod.UID}, Since: since, LastEvidence: latest,
			})
		}
	}
	return result
}

// SwiftSandboxStalled requires a sustained, fresh failure span for an initial
// sandbox. Event counts are not evidence of elapsed time or separate failures.
func SwiftSandboxStalled(pod *corev1.Pod, events []*corev1.Event, now time.Time) (time.Time, time.Time, bool) {
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
	var first, last time.Time
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
		if start.IsZero() || end.IsZero() || end.Before(start) || end.After(now) || start.Before(pod.CreationTimestamp.Time) {
			continue
		}
		if first.IsZero() || start.Before(first) {
			first = start
		}
		if end.After(last) {
			last = end
		}
	}
	if first.After(since) {
		since = first
	}
	ok := !first.IsZero() && last.Sub(since) >= time.Minute && now.Sub(last) <= time.Minute
	return since, last, ok
}
