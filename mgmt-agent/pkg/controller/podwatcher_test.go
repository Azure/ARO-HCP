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

package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
)

func TestContainerStateChanged(t *testing.T) {
	waiting := corev1.ContainerState{
		Waiting: &corev1.ContainerStateWaiting{Reason: "PodInitializing"},
	}
	running := corev1.ContainerState{
		Running: &corev1.ContainerStateRunning{StartedAt: metav1.Now()},
	}
	terminated := corev1.ContainerState{
		Terminated: &corev1.ContainerStateTerminated{ExitCode: 0},
	}

	tests := []struct {
		name string
		old  *corev1.Pod
		new  *corev1.Pod
		want bool
	}{
		{
			name: "both pods have nil container statuses",
			old:  &corev1.Pod{},
			new:  &corev1.Pod{},
			want: false,
		},
		{
			name: "container goes from Waiting to Running",
			old: &corev1.Pod{
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{Name: "app", State: waiting},
					},
				},
			},
			new: &corev1.Pod{
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{Name: "app", State: running},
					},
				},
			},
			want: true,
		},
		{
			name: "container goes from Running to Terminated",
			old: &corev1.Pod{
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{Name: "app", State: running},
					},
				},
			},
			new: &corev1.Pod{
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{Name: "app", State: terminated},
					},
				},
			},
			want: true,
		},
		{
			name: "same state on both old and new",
			old: &corev1.Pod{
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{Name: "app", State: running},
					},
				},
			},
			new: &corev1.Pod{
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{Name: "app", State: running},
					},
				},
			},
			want: false,
		},
		{
			name: "new container added",
			old: &corev1.Pod{
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{Name: "app", State: running},
					},
				},
			},
			new: &corev1.Pod{
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{Name: "app", State: running},
						{Name: "sidecar", State: waiting},
					},
				},
			},
			want: true,
		},
		{
			name: "container removed",
			old: &corev1.Pod{
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{Name: "app", State: running},
						{Name: "sidecar", State: running},
					},
				},
			},
			new: &corev1.Pod{
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{Name: "app", State: running},
					},
				},
			},
			want: true,
		},
		{
			name: "init container state change",
			old: &corev1.Pod{
				Status: corev1.PodStatus{
					InitContainerStatuses: []corev1.ContainerStatus{
						{Name: "init", State: waiting},
					},
				},
			},
			new: &corev1.Pod{
				Status: corev1.PodStatus{
					InitContainerStatuses: []corev1.ContainerStatus{
						{Name: "init", State: terminated},
					},
				},
			},
			want: true,
		},
		{
			name: "waiting reason changes within same state type",
			old: &corev1.Pod{
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{Name: "app", State: corev1.ContainerState{
							Waiting: &corev1.ContainerStateWaiting{Reason: "PodInitializing"},
						}},
					},
				},
			},
			new: &corev1.Pod{
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{Name: "app", State: corev1.ContainerState{
							Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff"},
						}},
					},
				},
			},
			want: false,
		},
		{
			name: "multiple containers, only one changes",
			old: &corev1.Pod{
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{Name: "app", State: running},
						{Name: "sidecar", State: running},
					},
				},
			},
			new: &corev1.Pod{
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{Name: "app", State: running},
						{Name: "sidecar", State: terminated},
					},
				},
			},
			want: true,
		},
		{
			name: "ephemeral container state change",
			old: &corev1.Pod{
				Status: corev1.PodStatus{
					EphemeralContainerStatuses: []corev1.ContainerStatus{
						{Name: "debugger", State: waiting},
					},
				},
			},
			new: &corev1.Pod{
				Status: corev1.PodStatus{
					EphemeralContainerStatuses: []corev1.ContainerStatus{
						{Name: "debugger", State: running},
					},
				},
			},
			want: true,
		},
		{
			name: "ephemeral container added",
			old:  &corev1.Pod{},
			new: &corev1.Pod{
				Status: corev1.PodStatus{
					EphemeralContainerStatuses: []corev1.ContainerStatus{
						{Name: "debugger", State: waiting},
					},
				},
			},
			want: true,
		},
		{
			name: "ephemeral container same state",
			old: &corev1.Pod{
				Status: corev1.PodStatus{
					EphemeralContainerStatuses: []corev1.ContainerStatus{
						{Name: "debugger", State: running},
					},
				},
			},
			new: &corev1.Pod{
				Status: corev1.PodStatus{
					EphemeralContainerStatuses: []corev1.ContainerStatus{
						{Name: "debugger", State: running},
					},
				},
			},
			want: false,
		},
		{
			name: "ephemeral container changes but regular containers unchanged",
			old: &corev1.Pod{
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{Name: "app", State: running},
					},
					EphemeralContainerStatuses: []corev1.ContainerStatus{
						{Name: "debugger", State: running},
					},
				},
			},
			new: &corev1.Pod{
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{Name: "app", State: running},
					},
					EphemeralContainerStatuses: []corev1.ContainerStatus{
						{Name: "debugger", State: terminated},
					},
				},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := containerStateChanged(tt.old, tt.new); got != tt.want {
				t.Errorf("containerStateChanged() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuildContainerStateMap(t *testing.T) {
	waiting := corev1.ContainerState{
		Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff"},
	}
	running := corev1.ContainerState{
		Running: &corev1.ContainerStateRunning{StartedAt: metav1.Now()},
	}
	terminated := corev1.ContainerState{
		Terminated: &corev1.ContainerStateTerminated{ExitCode: 0},
	}

	tests := []struct {
		name     string
		statuses []corev1.ContainerStatus
		want     map[string]string
	}{
		{
			name:     "nil statuses",
			statuses: nil,
			want:     map[string]string{},
		},
		{
			name:     "empty statuses",
			statuses: []corev1.ContainerStatus{},
			want:     map[string]string{},
		},
		{
			name: "single running container",
			statuses: []corev1.ContainerStatus{
				{Name: "app", State: running},
			},
			want: map[string]string{"app": "running"},
		},
		{
			name: "multiple containers with different states",
			statuses: []corev1.ContainerStatus{
				{Name: "app", State: running},
				{Name: "sidecar", State: waiting},
				{Name: "done", State: terminated},
			},
			want: map[string]string{"app": "running", "sidecar": "waiting", "done": "terminated"},
		},
		{
			name: "container with zero-value state returns unknown",
			statuses: []corev1.ContainerStatus{
				{Name: "empty", State: corev1.ContainerState{}},
			},
			want: map[string]string{"empty": "unknown"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildContainerStateMap(tt.statuses)
			if len(got) != len(tt.want) {
				t.Fatalf("buildContainerStateMap() returned %d entries, want %d", len(got), len(tt.want))
			}
			for key, wantVal := range tt.want {
				gotVal, ok := got[key]
				if !ok {
					t.Errorf("buildContainerStateMap() missing key %q", key)
				} else if gotVal != wantVal {
					t.Errorf("buildContainerStateMap()[%q] = %q, want %q", key, gotVal, wantVal)
				}
			}
		})
	}
}

func TestLogPodEvent(t *testing.T) {
	tests := []struct {
		name      string
		eventType string
		pod       *corev1.Pod
	}{
		{
			name:      "Add event",
			eventType: "Add",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
			},
		},
		{
			name:      "Delete event",
			eventType: "Delete",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "kube-system",
				},
			},
		},
		{
			name:      "Update event",
			eventType: "Update",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// logPodEvent should not panic for any valid pod.
			logPodEvent(tt.eventType, tt.pod)
		})
	}
}

func TestNewPodWatcher(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	factory := kubeinformers.NewSharedInformerFactory(clientset, 0)

	pw, err := NewPodWatcher(factory.Core().V1().Pods())
	if err != nil {
		t.Fatalf("NewPodWatcher() returned error: %v", err)
	}
	if pw == nil {
		t.Fatal("NewPodWatcher() returned nil")
	}
	if pw.podSynced == nil {
		t.Fatal("NewPodWatcher() did not set podSynced")
	}
}
