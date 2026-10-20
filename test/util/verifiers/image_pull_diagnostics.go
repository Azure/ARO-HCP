package verifiers

import (
	"context"
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

func CaptureImagePullFailureDiagnostics(ctx context.Context, verificationErr error, client kubernetes.Interface, namespace, podName, artifactDir string) {
	if verificationErr == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), DefaultDiagnoseTimeout)
	defer cancel()
	captureImagePullDiagnostics(ctx, client, namespace, podName, artifactDir)
}

func captureImagePullDiagnostics(ctx context.Context, client kubernetes.Interface, namespace, podName, artifactDir string) {
	selector := fields.Set{"involvedObject.kind": "Pod", "involvedObject.name": podName}
	pod, err := client.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		ginkgo.GinkgoWriter.Printf("[image-pull diagnostics] pod %s/%s unavailable: %s\n", namespace, podName, apierrors.ReasonForError(err))
	} else {
		if pod.UID != "" {
			selector["involvedObject.uid"] = string(pod.UID)
		}
		writeImagePullDiagnostic(artifactDir, namespace, podName, "pod.yaml", imagePullDiagnosticPod(pod))
	}

	events, err := client.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{FieldSelector: selector.String()})
	if err != nil {
		ginkgo.GinkgoWriter.Printf("[image-pull diagnostics] events for %s/%s unavailable: %s\n", namespace, podName, apierrors.ReasonForError(err))
		return
	}
	events.TypeMeta = metav1.TypeMeta{APIVersion: "v1", Kind: "EventList"}
	for index := range events.Items {
		events.Items[index].ObjectMeta = imagePullDiagnosticMetadata(events.Items[index].ObjectMeta)
	}
	writeImagePullDiagnostic(artifactDir, namespace, podName, "events.yaml", events)
}

func imagePullDiagnosticMetadata(metadata metav1.ObjectMeta) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Name: metadata.Name, Namespace: metadata.Namespace, UID: metadata.UID,
		CreationTimestamp: metadata.CreationTimestamp,
	}
}

func imagePullDiagnosticPod(pod *corev1.Pod) *corev1.Pod {
	result := &corev1.Pod{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"},
		ObjectMeta: imagePullDiagnosticMetadata(pod.ObjectMeta),
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

func writeImagePullDiagnostic(artifactDir, namespace, podName, name string, object interface{}) {
	data, err := yaml.Marshal(object)
	if err != nil {
		ginkgo.GinkgoWriter.Printf("[image-pull diagnostics] could not encode %s\n", name)
		return
	}
	if artifactDir != "" {
		directory := filepath.Join(artifactDir, "image-pull-test", namespace, podName)
		if err := os.MkdirAll(directory, 0700); err == nil {
			path := filepath.Join(directory, name)
			if err := os.WriteFile(path, data, 0600); err == nil {
				ginkgo.GinkgoWriter.Printf("[image-pull diagnostics] wrote %s (pod spec and metadata restricted to diagnostic fields)\n", path)
				return
			}
		}
		ginkgo.GinkgoWriter.Printf("[image-pull diagnostics] could not write %s; logging instead\n", name)
	}
	fmt.Fprintf(ginkgo.GinkgoWriter, "[image-pull diagnostics] %s/%s/%s (pod spec and metadata restricted to diagnostic fields)\n%s\n", namespace, podName, name, data)
}
