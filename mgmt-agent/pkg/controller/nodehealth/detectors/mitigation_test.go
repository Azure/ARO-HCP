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
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSwiftSandboxStalled(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name   string
		change func(*corev1.Pod, *corev1.Event)
		want   bool
	}{
		{"sixty seconds", func(*corev1.Pod, *corev1.Event) {}, true},
		{"below threshold", func(_ *corev1.Pod, e *corev1.Event) { e.FirstTimestamp = metav1.NewTime(now.Add(-59 * time.Second)) }, false},
		{"stale activity", func(_ *corev1.Pod, e *corev1.Event) {
			e.FirstTimestamp = metav1.NewTime(now.Add(-3 * time.Minute))
			e.LastTimestamp = metav1.NewTime(now.Add(-61 * time.Second))
		}, false},
		{"one warning high count", func(_ *corev1.Pod, e *corev1.Event) { e.FirstTimestamp = e.LastTimestamp; e.Count = 100000 }, false},
		{"wrong uid", func(_ *corev1.Pod, e *corev1.Event) { e.InvolvedObject.UID = "another" }, false},
		{"wrong namespace", func(_ *corev1.Pod, e *corev1.Event) { e.InvolvedObject.Namespace = "another" }, false},
		{"wrong node", func(_ *corev1.Pod, e *corev1.Event) { e.Source.Host = "another" }, false},
		{"image pull", func(_ *corev1.Pod, e *corev1.Event) { e.Reason = "Failed"; e.Message = "image pull failed" }, false},
		{"no matching signature", func(_ *corev1.Pod, e *corev1.Event) { e.Message = "ordinary startup warning" }, false},
		{"future event", func(_ *corev1.Pod, e *corev1.Event) { e.LastTimestamp = metav1.NewTime(now.Add(time.Second)) }, false},
		{"terminating", func(p *corev1.Pod, _ *corev1.Event) { at := metav1.NewTime(now); p.DeletionTimestamp = &at }, false},
		{"sandbox ready", func(p *corev1.Pod, _ *corev1.Event) { p.Status.Conditions[1].Status = corev1.ConditionTrue }, false},
		{"container already ran", func(p *corev1.Pod, _ *corev1.Event) {
			p.Status.ContainerStatuses = []corev1.ContainerStatus{{State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}}
		}, false},
		{"container restarted", func(p *corev1.Pod, _ *corev1.Event) {
			p.Status.ContainerStatuses = []corev1.ContainerStatus{{RestartCount: 1}}
		}, false},
		{"not swift", func(p *corev1.Pod, _ *corev1.Event) { p.Spec.Containers[0].Resources = corev1.ResourceRequirements{} }, false},
		{"new sandbox transition", func(p *corev1.Pod, _ *corev1.Event) {
			p.Status.Conditions[1].LastTransitionTime = metav1.NewTime(now.Add(-10 * time.Second))
		}, false},
		{"series activity", func(_ *corev1.Pod, e *corev1.Event) {
			e.LastTimestamp = e.FirstTimestamp
			e.Series = &corev1.EventSeries{Count: 20, LastObservedTime: metav1.NewMicroTime(now)}
		}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod", Namespace: "hcp", UID: "pod-uid", CreationTimestamp: metav1.NewTime(now.Add(-5 * time.Minute))},
				Spec: corev1.PodSpec{NodeName: "node", Containers: []corev1.Container{{Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{swiftNICResourceName: resource.MustParse("1")}}}}},
				Status: corev1.PodStatus{Phase: corev1.PodPending, Conditions: []corev1.PodCondition{
					{Type: corev1.PodScheduled, Status: corev1.ConditionTrue, LastTransitionTime: metav1.NewTime(now.Add(-2 * time.Minute))},
					{Type: corev1.PodReadyToStartContainers, Status: corev1.ConditionFalse, LastTransitionTime: metav1.NewTime(now.Add(-2 * time.Minute))},
				}}}
			event := &corev1.Event{InvolvedObject: corev1.ObjectReference{Kind: "Pod", UID: pod.UID, Namespace: pod.Namespace},
				Source: corev1.EventSource{Host: "node"}, Reason: reasonFailedCreatePodSandBox, Message: "route ip+net: no such network interface",
				FirstTimestamp: metav1.NewTime(now.Add(-time.Minute)), LastTimestamp: metav1.NewTime(now)}
			test.change(pod, event)
			_, _, got := SwiftSandboxStalled(pod, []*corev1.Event{event}, now)
			if got != test.want {
				t.Fatalf("stalled=%v, want %v", got, test.want)
			}
		})
	}
}
