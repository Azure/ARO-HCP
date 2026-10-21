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
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/onsi/ginkgo/v2"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"

	"sigs.k8s.io/yaml"
)

// CapturePodFailureDiagnostics collects restricted Pod YAML and associated events for
// one named pod when failureErr is non-nil; nil is a no-op. Call it before cleanup
// and assert the original failure afterward. Collection is best effort and uses an
// independent DefaultDiagnoseTimeout budget even if ctx has been canceled.
// Artifacts are written under artifactDir/pod-diagnostics/<namespace>/<podName>,
// falling back to GinkgoWriter when artifactDir is empty or writing fails. Pod spec
// and metadata are restricted to diagnostic fields; status and events are retained.
// Secret resources and pod logs are not fetched. Callers should use test workloads
// that do not expose credentials in status or event messages.
func CapturePodFailureDiagnostics(ctx context.Context, failureErr error, client kubernetes.Interface, namespace, podName, artifactDir string) {
	if failureErr == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), DefaultDiagnoseTimeout)
	defer cancel()
	capturePodDiagnostics(ctx, client, namespace, podName, artifactDir)
}

func capturePodDiagnostics(ctx context.Context, client kubernetes.Interface, namespace, podName, artifactDir string) {
	selector := fields.Set{"involvedObject.kind": "Pod", "involvedObject.name": podName}
	pod, err := client.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		ginkgo.GinkgoWriter.Printf("[pod diagnostics] pod %s/%s unavailable: %s\n", namespace, podName, podDiagnosticError(err))
	} else {
		if pod.UID != "" {
			selector["involvedObject.uid"] = string(pod.UID)
		}
		writePodDiagnostic(artifactDir, namespace, podName, "pod.yaml", podDiagnosticPod(pod))
	}

	events, err := client.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{FieldSelector: selector.String()})
	if err != nil {
		ginkgo.GinkgoWriter.Printf("[pod diagnostics] events for %s/%s unavailable: %s\n", namespace, podName, podDiagnosticError(err))
		return
	}
	events.TypeMeta = metav1.TypeMeta{APIVersion: "v1", Kind: "EventList"}
	for index := range events.Items {
		events.Items[index].ObjectMeta = podDiagnosticMetadata(events.Items[index].ObjectMeta)
	}
	writePodDiagnostic(artifactDir, namespace, podName, "events.yaml", events)
}

func podDiagnosticError(err error) string {
	for _, contextErr := range []error{context.Canceled, context.DeadlineExceeded} {
		if errors.Is(err, contextErr) {
			return contextErr.Error()
		}
	}
	if reason := apierrors.ReasonForError(err); reason != "" {
		return string(reason)
	}
	return "request failed"
}

func podDiagnosticMetadata(metadata metav1.ObjectMeta) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Name: metadata.Name, Namespace: metadata.Namespace, UID: metadata.UID,
		CreationTimestamp: metadata.CreationTimestamp,
	}
}

func podDiagnosticPod(pod *corev1.Pod) *corev1.Pod {
	result := &corev1.Pod{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"},
		ObjectMeta: podDiagnosticMetadata(pod.ObjectMeta),
		Spec: corev1.PodSpec{
			NodeName:           pod.Spec.NodeName,
			ServiceAccountName: pod.Spec.ServiceAccountName,
			RestartPolicy:      pod.Spec.RestartPolicy,
			SchedulerName:      pod.Spec.SchedulerName,
		},
		Status: *pod.Status.DeepCopy(),
	}
	for _, container := range pod.Spec.Containers {
		result.Spec.Containers = append(result.Spec.Containers, corev1.Container{
			Name: container.Name, Image: container.Image, ImagePullPolicy: container.ImagePullPolicy,
		})
	}
	for _, container := range pod.Spec.InitContainers {
		result.Spec.InitContainers = append(result.Spec.InitContainers, corev1.Container{
			Name: container.Name, Image: container.Image, ImagePullPolicy: container.ImagePullPolicy,
		})
	}
	return result
}

func writePodDiagnostic(artifactDir, namespace, podName, name string, object interface{}) {
	data, err := yaml.Marshal(object)
	if err != nil {
		ginkgo.GinkgoWriter.Printf("[pod diagnostics] could not encode %s\n", name)
		return
	}
	if artifactDir != "" {
		directory := filepath.Join(artifactDir, "pod-diagnostics", namespace, podName)
		if err := os.MkdirAll(directory, 0700); err == nil {
			path := filepath.Join(directory, name)
			if err := os.WriteFile(path, data, 0600); err == nil {
				ginkgo.GinkgoWriter.Printf("[pod diagnostics] wrote %s (pod spec and metadata restricted to diagnostic fields)\n", path)
				return
			}
		}
		ginkgo.GinkgoWriter.Printf("[pod diagnostics] could not write %s; logging instead\n", name)
	}
	fmt.Fprintf(ginkgo.GinkgoWriter, "[pod diagnostics] %s/%s/%s (pod spec and metadata restricted to diagnostic fields)\n%s\n", namespace, podName, name, data)
}
