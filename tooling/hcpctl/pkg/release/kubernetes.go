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

package release

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// queryActualWorkloadImage queries the Kubernetes API to get the actual container image for a workload
func queryActualWorkloadImage(ctx context.Context, clientset kubernetes.Interface, workload *WorkloadInfo) (string, error) {
	switch workload.Kind {
	case "Deployment":
		return queryDeploymentImage(ctx, clientset, workload.Name, workload.Namespace)
	case "DaemonSet":
		return queryDaemonSetImage(ctx, clientset, workload.Name, workload.Namespace)
	case "StatefulSet":
		return queryStatefulSetImage(ctx, clientset, workload.Name, workload.Namespace)
	default:
		return "", fmt.Errorf("unsupported workload kind: %s", workload.Kind)
	}
}

// queryDeploymentImage queries a Deployment for its first container image
func queryDeploymentImage(ctx context.Context, clientset kubernetes.Interface, name, namespace string) (string, error) {
	deployment, err := clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get deployment %s/%s: %w", namespace, name, err)
	}
	return extractFirstContainerImageFromPodSpec(deployment.Spec.Template.Spec), nil
}

// queryDaemonSetImage queries a DaemonSet for its first container image
func queryDaemonSetImage(ctx context.Context, clientset kubernetes.Interface, name, namespace string) (string, error) {
	daemonSet, err := clientset.AppsV1().DaemonSets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get daemonset %s/%s: %w", namespace, name, err)
	}
	return extractFirstContainerImageFromPodSpec(daemonSet.Spec.Template.Spec), nil
}

// queryStatefulSetImage queries a StatefulSet for its first container image
func queryStatefulSetImage(ctx context.Context, clientset kubernetes.Interface, name, namespace string) (string, error) {
	statefulSet, err := clientset.AppsV1().StatefulSets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get statefulset %s/%s: %w", namespace, name, err)
	}
	return extractFirstContainerImageFromPodSpec(statefulSet.Spec.Template.Spec), nil
}

// extractFirstContainerImageFromPodSpec extracts the first container image from a PodSpec
func extractFirstContainerImageFromPodSpec(podSpec corev1.PodSpec) string {
	if len(podSpec.Containers) > 0 {
		return podSpec.Containers[0].Image
	}
	return ""
}
