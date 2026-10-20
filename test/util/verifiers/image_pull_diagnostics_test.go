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

package verifiers

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	clienttesting "k8s.io/client-go/testing"

	"sigs.k8s.io/yaml"
)

func TestCaptureImagePullFailureDiagnostics(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pull-test", Namespace: "image-pull-test", UID: "pod-uid", Annotations: map[string]string{"private": "annotation-marker"}},
		Spec: corev1.PodSpec{
			NodeName:            "worker-1",
			Containers:          []corev1.Container{{Name: "pull-test", Image: "registry.example/image", Env: []corev1.EnvVar{{Name: "PRIVATE", Value: "env-marker"}}, Command: []string{"command-marker"}}},
			InitContainers:      []corev1.Container{{Name: "init", Image: "registry.example/init", Args: []string{"args-marker"}}},
			EphemeralContainers: []corev1.EphemeralContainer{{EphemeralContainerCommon: corev1.EphemeralContainerCommon{Name: "debug", Env: []corev1.EnvVar{{Name: "PRIVATE", Value: "ephemeral-marker"}}}}},
			Volumes:             []corev1.Volume{{Name: "volume", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: "secret-marker"}}}},
		},
		Status: corev1.PodStatus{
			Conditions:        []corev1.PodCondition{{Type: corev1.PodScheduled, Status: corev1.ConditionFalse, Reason: "Unschedulable", Message: "Insufficient cpu"}},
			ContainerStatuses: []corev1.ContainerStatus{{Name: "pull-test", Image: "registry.example/image", State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff", Message: "pull failed"}}}},
		},
	}
	event := &corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{Name: "pull-failed", Namespace: pod.Namespace, Annotations: map[string]string{"private": "event-annotation-marker"}},
		InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: pod.Name, Namespace: pod.Namespace, UID: pod.UID},
		Reason:         "Failed", Message: "Failed to pull image", Count: 2,
	}
	for _, scenario := range []struct {
		name        string
		failed      bool
		missingPod  bool
		eventsError bool
	}{
		{name: "success"},
		{name: "failure", failed: true},
		{name: "deleted pod", failed: true, missingPod: true},
		{name: "events unavailable", failed: true, eventsError: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			client := fake.NewClientset(event)
			var output bytes.Buffer
			ginkgo.GinkgoWriter.TeeTo(&output)
			defer ginkgo.GinkgoWriter.ClearTeeWriters()
			if !scenario.missingPod {
				require.NoError(t, client.Tracker().Add(pod.DeepCopy()))
			}
			if scenario.eventsError {
				client.PrependReactor("list", "events", func(clienttesting.Action) (bool, runtime.Object, error) {
					return true, nil, errors.New("private-error-marker")
				})
			}
			directory := t.TempDir()
			var verificationErr error
			if scenario.failed {
				verificationErr = errors.New("original image pull failure")
			}
			CaptureImagePullFailureDiagnostics(context.Background(), verificationErr, client, pod.Namespace, pod.Name, directory)
			require.NotContains(t, output.String(), "private-error-marker")
			if !scenario.failed {
				require.Empty(t, client.Actions(), "successful verification must not collect diagnostics")
				entries, err := os.ReadDir(directory)
				require.NoError(t, err)
				require.Empty(t, entries)
				return
			}
			require.EqualError(t, verificationErr, "original image pull failure")
			actions := client.Actions()
			require.Len(t, actions, 2, "only pod GET and event LIST are allowed")
			require.True(t, actions[0].Matches("get", "pods"))
			require.True(t, actions[1].Matches("list", "events"))
			require.Equal(t, pod.Namespace, actions[0].GetNamespace())
			require.Equal(t, pod.Namespace, actions[1].GetNamespace())
			selector := actions[1].(clienttesting.ListAction).GetListRestrictions().Fields.String()
			require.Contains(t, selector, "involvedObject.kind=Pod")
			require.Contains(t, selector, "involvedObject.name=pull-test")
			if !scenario.missingPod {
				require.Contains(t, selector, "involvedObject.uid=pod-uid")
			} else {
				require.NotContains(t, selector, "involvedObject.uid")
			}
			base := filepath.Join(directory, "image-pull-test", pod.Namespace, pod.Name)
			if scenario.missingPod {
				require.NoFileExists(t, filepath.Join(base, "pod.yaml"))
			} else {
				data, err := os.ReadFile(filepath.Join(base, "pod.yaml"))
				require.NoError(t, err)
				for _, marker := range []string{"env-marker", "annotation-marker", "command-marker", "args-marker", "ephemeral-marker", "secret-marker"} {
					require.NotContains(t, string(data), marker)
				}
				var captured corev1.Pod
				require.NoError(t, yaml.Unmarshal(data, &captured))
				require.Equal(t, "worker-1", captured.Spec.NodeName)
				require.Equal(t, "ImagePullBackOff", captured.Status.ContainerStatuses[0].State.Waiting.Reason)
				require.Equal(t, pod.Status, captured.Status)
				require.Equal(t, "registry.example/init", captured.Spec.InitContainers[0].Image)
				info, err := os.Stat(filepath.Join(base, "pod.yaml"))
				require.NoError(t, err)
				require.Equal(t, os.FileMode(0600), info.Mode().Perm())
			}
			if scenario.eventsError {
				require.NoFileExists(t, filepath.Join(base, "events.yaml"))
				require.Contains(t, output.String(), "events for image-pull-test/pull-test unavailable: request failed")
			} else {
				data, err := os.ReadFile(filepath.Join(base, "events.yaml"))
				require.NoError(t, err)
				require.Contains(t, string(data), "Failed to pull image")
				require.NotContains(t, string(data), "event-annotation-marker")
				var captured corev1.EventList
				require.NoError(t, yaml.Unmarshal(data, &captured))
				require.Len(t, captured.Items, 1)
				require.Equal(t, event.InvolvedObject, captured.Items[0].InvolvedObject)
				require.Equal(t, event.Count, captured.Items[0].Count)
			}
		})
	}
	require.Equal(t, "env-marker", pod.Spec.Containers[0].Env[0].Value, "diagnostics must not mutate the original pod")
}

type imagePullDiagnosticTransport func(*http.Request) (*http.Response, error)

func (transport imagePullDiagnosticTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestImagePullDiagnosticsContext(t *testing.T) {
	requests := 0
	client, err := kubernetes.NewForConfig(&rest.Config{
		Host: "https://example.invalid",
		Transport: imagePullDiagnosticTransport(func(request *http.Request) (*http.Response, error) {
			requests++
			require.NoError(t, request.Context().Err(), "diagnostics must survive caller cancellation")
			deadline, ok := request.Context().Deadline()
			require.True(t, ok, "diagnostic requests must have a deadline")
			require.Positive(t, time.Until(deadline))
			require.LessOrEqual(t, time.Until(deadline), DefaultDiagnoseTimeout)
			return &http.Response{StatusCode: http.StatusNotFound, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"kind":"Status","apiVersion":"v1","reason":"NotFound","code":404}`))}, nil
		}),
	})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	CaptureImagePullFailureDiagnostics(ctx, errors.New("original"), client, "image-pull-test", "pull-test", t.TempDir())
	require.Equal(t, 2, requests)
}

func TestImagePullDiagnosticsLogFallback(t *testing.T) {
	for _, scenario := range []string{"no artifacts", "unwritable artifacts"} {
		t.Run(scenario, func(t *testing.T) {
			directory := ""
			if scenario == "unwritable artifacts" {
				directory = filepath.Join(t.TempDir(), "file")
				require.NoError(t, os.WriteFile(directory, []byte("not a directory"), 0600))
			}
			var output bytes.Buffer
			ginkgo.GinkgoWriter.TeeTo(&output)
			defer ginkgo.GinkgoWriter.ClearTeeWriters()
			CaptureImagePullFailureDiagnostics(context.Background(), errors.New("original"), fake.NewClientset(), "image-pull-test", "deleted", directory)
			require.Contains(t, output.String(), "pod image-pull-test/deleted unavailable: NotFound")
			require.Contains(t, output.String(), "events.yaml")
			require.Contains(t, output.String(), "kind: EventList")
		})
	}
}

func TestImagePullDiagnosticsDeadline(t *testing.T) {
	var output bytes.Buffer
	ginkgo.GinkgoWriter.TeeTo(&output)
	defer ginkgo.GinkgoWriter.ClearTeeWriters()
	client, err := kubernetes.NewForConfig(&rest.Config{
		Host: "https://example.invalid",
		Transport: imagePullDiagnosticTransport(func(request *http.Request) (*http.Response, error) {
			<-request.Context().Done()
			return nil, request.Context().Err()
		}),
	})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	captureImagePullDiagnostics(ctx, client, "image-pull-test", "pull-test", t.TempDir())
	require.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
	require.Contains(t, output.String(), "unavailable: context deadline exceeded")
	require.Less(t, time.Since(started), time.Second, "blocked Kubernetes requests must respect the collection deadline")
}
