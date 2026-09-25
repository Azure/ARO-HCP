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
			_, _, got := (swiftPodSandboxStalledDetector{}).EvaluatePod(pod, []*corev1.Event{event}, now)
			if got != test.want {
				t.Fatalf("stalled=%v, want %v", got, test.want)
			}
		})
	}
}

func TestSwiftSandboxStalledEventIntervals(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name      string
		intervals [][2]time.Duration
		want      bool
		wantStart time.Duration
		wantEnd   time.Duration
		scheduled time.Duration
	}{
		{name: "isolated old and fresh failures", intervals: [][2]time.Duration{{-90 * time.Second, -90 * time.Second}, {0, 0}}},
		{name: "disconnected fresh intervals", intervals: [][2]time.Duration{{-70 * time.Second, -50 * time.Second}, {-30 * time.Second, 0}}},
		{name: "stale interval cannot extend dwell", intervals: [][2]time.Duration{{-100 * time.Second, -61 * time.Second}, {-65 * time.Second, -10 * time.Second}}},
		{name: "overlapping intervals", intervals: [][2]time.Duration{{-70 * time.Second, -30 * time.Second}, {-40 * time.Second, 0}}, want: true, wantStart: -70 * time.Second},
		{name: "touching intervals", intervals: [][2]time.Duration{{-time.Minute, -30 * time.Second}, {-30 * time.Second, 0}}, want: true, wantStart: -time.Minute},
		{name: "unordered intervals", intervals: [][2]time.Duration{{-30 * time.Second, 0}, {-time.Minute, -30 * time.Second}}, want: true, wantStart: -time.Minute},
		{name: "nested intervals", intervals: [][2]time.Duration{{-70 * time.Second, 0}, {-50 * time.Second, -30 * time.Second}}, want: true, wantStart: -70 * time.Second},
		{name: "duplicate points", intervals: [][2]time.Duration{{0, 0}, {0, 0}}},
		{name: "one second gap", intervals: [][2]time.Duration{{-time.Minute, -31 * time.Second}, {-30 * time.Second, 0}}},
		{name: "freshness boundary", intervals: [][2]time.Duration{{-2 * time.Minute, -time.Minute}}, want: true, wantStart: -2 * time.Minute, wantEnd: -time.Minute},
		{name: "stale span", intervals: [][2]time.Duration{{-2 * time.Minute, -61 * time.Second}}},
		{name: "fresh qualifying span after old point", intervals: [][2]time.Duration{{-110 * time.Second, -110 * time.Second}, {-time.Minute, 0}}, want: true, wantStart: -time.Minute},
		{name: "scheduling bounds dwell", intervals: [][2]time.Duration{{-90 * time.Second, 0}}, scheduled: -30 * time.Second},
		{name: "scheduling clips qualifying span", intervals: [][2]time.Duration{{-90 * time.Second, 0}}, scheduled: -time.Minute, want: true, wantStart: -time.Minute},
		{name: "below duration", intervals: [][2]time.Duration{{-59 * time.Second, 0}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Namespace: "hcp", UID: "pod-uid", CreationTimestamp: metav1.NewTime(now.Add(-5 * time.Minute))},
				Spec: corev1.PodSpec{NodeName: "node", Containers: []corev1.Container{{
					Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{swiftNICResourceName: resource.MustParse("1")}},
				}}},
				Status: corev1.PodStatus{Phase: corev1.PodPending, Conditions: []corev1.PodCondition{
					{Type: corev1.PodScheduled, Status: corev1.ConditionTrue, LastTransitionTime: metav1.NewTime(now.Add(-2 * time.Minute))},
					{Type: corev1.PodReadyToStartContainers, Status: corev1.ConditionFalse, LastTransitionTime: metav1.NewTime(now.Add(-2 * time.Minute))},
				}},
			}
			if test.scheduled != 0 {
				pod.Status.Conditions[0].LastTransitionTime = metav1.NewTime(now.Add(test.scheduled))
			}
			var events []*corev1.Event
			for _, interval := range test.intervals {
				events = append(events, &corev1.Event{
					InvolvedObject: corev1.ObjectReference{Kind: "Pod", UID: pod.UID, Namespace: pod.Namespace},
					Source:         corev1.EventSource{Host: pod.Spec.NodeName},
					Reason:         reasonFailedCreatePodSandBox, Message: "route ip+net: no such network interface",
					FirstTimestamp: metav1.NewTime(now.Add(interval[0])), LastTimestamp: metav1.NewTime(now.Add(interval[1])),
				})
			}
			start, end, got := (swiftPodSandboxStalledDetector{}).EvaluatePod(pod, events, now)
			if got != test.want {
				t.Fatalf("stalled=%v, want %v (span %s to %s)", got, test.want, start, end)
			}
			if got && (!start.Equal(now.Add(test.wantStart)) || !end.Equal(now.Add(test.wantEnd))) {
				t.Fatalf("span = [%s, %s], want offsets [%s, %s]", start, end, test.wantStart, test.wantEnd)
			}
		})
	}
}
