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
	"time"

	"github.com/onsi/ginkgo/v2"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type verifyImagePulled struct {
	namespace       string
	imageRepository string
	imageName       string // optional - if empty, any image from repository is checked
}

func (v verifyImagePulled) Name() string {
	return "VerifyImagePulled"
}

func (v verifyImagePulled) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	logger := ginkgo.GinkgoLogr
	startTime := time.Now()
	logger.Info("Starting image pull verification",
		"namespace", v.namespace,
		"imageRepository", v.imageRepository,
		"imageName", v.imageName,
		"startTime", startTime.Format(time.RFC3339))

	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	// Get all pods in the namespace
	pods, err := kubeClient.CoreV1().Pods(v.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list pods in namespace %s: %w", v.namespace, err)
	}

	if len(pods.Items) == 0 {
		return fmt.Errorf("no pods found in namespace %s", v.namespace)
	}

	// Check if at least one pod successfully pulled the specified image
	imagePulledSuccessfully := false
	var imagePullErrors []string

	for _, pod := range pods.Items {
		// Check container statuses for image pull success
		for _, containerStatus := range pod.Status.ContainerStatuses {
			// Check if the image matches our criteria
			imageMatches := strings.Contains(containerStatus.Image, v.imageRepository)
			if v.imageName != "" {
				imageMatches = imageMatches && strings.Contains(containerStatus.Image, v.imageName)
			}

			if imageMatches {
				// If ImageID is set, the image was pulled successfully
				if containerStatus.ImageID != "" {
					imagePulledSuccessfully = true
					logger.Info("Successfully pulled image",
						"pod", pod.Name,
						"container", containerStatus.Name,
						"image", containerStatus.Image,
						"imageID", containerStatus.ImageID)
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

		// Check container statuses for waiting state
		for _, containerStatus := range pod.Status.ContainerStatuses {
			if containerStatus.State.Waiting != nil {
				reason := containerStatus.State.Waiting.Reason
				message := containerStatus.State.Waiting.Message

				// Log all waiting states
				logger.Info("Container waiting",
					"pod", pod.Name,
					"container", containerStatus.Name,
					"reason", reason,
					"message", message,
					"image", containerStatus.Image)

				// Track image pull errors specifically
				if reason == "ImagePullBackOff" || reason == "ErrImagePull" {
					imagePullErrors = append(imagePullErrors, fmt.Sprintf("pod %s container %s: %s - %s",
						pod.Name, containerStatus.Name, reason, message))
				}
			}
		}
	}

	endTime := time.Now()
	duration := endTime.Sub(startTime)

	if len(imagePullErrors) > 0 {
		logger.Error(fmt.Errorf("image pull errors detected"), "verification failed",
			"namespace", v.namespace,
			"imageRepository", v.imageRepository,
			"imageName", v.imageName,
			"duration", duration,
			"endTime", endTime.Format(time.RFC3339))
		return fmt.Errorf("image pull errors detected:\n%s", strings.Join(imagePullErrors, "\n"))
	}

	if !imagePulledSuccessfully {
		logger.Error(fmt.Errorf("no matching images pulled"), "verification failed",
			"namespace", v.namespace,
			"imageRepository", v.imageRepository,
			"imageName", v.imageName,
			"duration", duration,
			"endTime", endTime.Format(time.RFC3339))
		return fmt.Errorf("no pods found with successfully pulled images from %s", v.imageRepository)
	}

	logger.Info("Image pull verification completed successfully",
		"namespace", v.namespace,
		"imageRepository", v.imageRepository,
		"imageName", v.imageName,
		"duration", duration,
		"endTime", endTime.Format(time.RFC3339))

	return nil
}

// VerifyImagePulled creates a verifier that checks if an image has been successfully pulled
// Parameters:
//   - namespace: the namespace to check for pods
//   - imageRepository: the repository to match (e.g., "registry.redhat.io")
//   - imageName: optional specific image name to match (empty string matches any image from repository)
func VerifyImagePulled(namespace, imageRepository, imageName string) HostedClusterVerifier {
	return verifyImagePulled{
		namespace:       namespace,
		imageRepository: imageRepository,
		imageName:       imageName,
	}
}
