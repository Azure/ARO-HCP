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

package verifiers

import (
	"context"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type verifyNFDImagePulled struct {
	namespace string
}

func (v verifyNFDImagePulled) Name() string {
	return "VerifyNFDImagePulled"
}

func (v verifyNFDImagePulled) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	// Get all pods in the NFD namespace
	pods, err := kubeClient.CoreV1().Pods(v.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list pods in namespace %s: %w", v.namespace, err)
	}

	if len(pods.Items) == 0 {
		return fmt.Errorf("no pods found in namespace %s", v.namespace)
	}

	// Check if at least one pod successfully pulled an image from registry.redhat.io
	imagePulledSuccessfully := false
	var imagePullErrors []string

	for _, pod := range pods.Items {
		// Check container statuses for image pull success
		for _, containerStatus := range pod.Status.ContainerStatuses {
			// Check if the image is from registry.redhat.io
			if strings.Contains(containerStatus.Image, "registry.redhat.io") {
				// If ImageID is set, the image was pulled successfully
				if containerStatus.ImageID != "" {
					imagePulledSuccessfully = true
				}
			}
		}

		// Also check for ImagePullBackOff errors
		for _, condition := range pod.Status.Conditions {
			if condition.Type == "PodScheduled" && condition.Status == "False" {
				if strings.Contains(condition.Message, "ImagePullBackOff") || strings.Contains(condition.Message, "ErrImagePull") {
					imagePullErrors = append(imagePullErrors, fmt.Sprintf("pod %s: %s", pod.Name, condition.Message))
				}
			}
		}

		// Check container statuses for waiting state with image pull errors
		for _, containerStatus := range pod.Status.ContainerStatuses {
			if containerStatus.State.Waiting != nil {
				reason := containerStatus.State.Waiting.Reason
				if reason == "ImagePullBackOff" || reason == "ErrImagePull" {
					imagePullErrors = append(imagePullErrors, fmt.Sprintf("pod %s container %s: %s - %s",
						pod.Name, containerStatus.Name, reason, containerStatus.State.Waiting.Message))
				}
			}
		}
	}

	if len(imagePullErrors) > 0 {
		return fmt.Errorf("image pull errors detected:\n%s", strings.Join(imagePullErrors, "\n"))
	}

	if !imagePulledSuccessfully {
		return fmt.Errorf("no pods found with successfully pulled images from registry.redhat.io")
	}

	return nil
}

func VerifyNFDImagePulled(namespace string) HostedClusterVerifier {
	return verifyNFDImagePulled{namespace: namespace}
}
