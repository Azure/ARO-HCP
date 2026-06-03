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
	"sort"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type verifyImagePulled struct {
	namespace       string
	imageRepository string
	imageName       string // optional - if empty, any image from repository is checked
	wait            waitSettings
}

func (v verifyImagePulled) Name() string {
	if v.imageName != "" {
		return fmt.Sprintf("VerifyImagePulled(%s/%s)", v.imageRepository, v.imageName)
	}
	return fmt.Sprintf("VerifyImagePulled(%s)", v.imageRepository)
}

func (v verifyImagePulled) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	return verifyOnceOrPoll(ctx, v.Name(), adminRESTConfig, v.wait, nil, v.checkOnce)
}

func (v verifyImagePulled) checkOnce(ctx context.Context, adminRESTConfig *rest.Config) error {
	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	pods, err := kubeClient.CoreV1().Pods(v.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list pods in namespace %s: %w", v.namespace, err)
	}

	if len(pods.Items) == 0 {
		return fmt.Errorf("expected image from %s in namespace %s: no pods found",
			v.imageRef(), v.namespace)
	}

	imagePulledSuccessfully := false
	var imagePullErrors []string
	var waitingStates []string

	for _, pod := range pods.Items {
		for _, containerStatus := range pod.Status.ContainerStatuses {
			imageMatches := strings.Contains(containerStatus.Image, v.imageRepository)
			if v.imageName != "" {
				imageMatches = imageMatches && strings.Contains(containerStatus.Image, v.imageName)
			}

			if imageMatches && containerStatus.ImageID != "" {
				imagePulledSuccessfully = true
			}

			if containerStatus.State.Waiting != nil {
				reason := containerStatus.State.Waiting.Reason
				message := containerStatus.State.Waiting.Message
				if imagePullErrorReasons.Has(reason) {
					imagePullErrors = append(imagePullErrors, fmt.Sprintf("pod %s container %s: %s - %s",
						pod.Name, containerStatus.Name, reason, message))
				} else if imageMatches || v.imageName == "" {
					waitingStates = append(waitingStates, fmt.Sprintf("pod %s container %s: %s (%s)",
						pod.Name, containerStatus.Name, reason, message))
				}
			}
		}

		for _, condition := range pod.Status.Conditions {
			if condition.Type == "PodScheduled" && condition.Status == "False" {
				for reason := range imagePullErrorReasons {
					if strings.Contains(condition.Message, reason) {
						imagePullErrors = append(imagePullErrors, fmt.Sprintf("pod %s: %s", pod.Name, condition.Message))
						break
					}
				}
			}
		}
	}

	sort.Strings(imagePullErrors)
	sort.Strings(waitingStates)

	if len(imagePullErrors) > 0 {
		return fmt.Errorf("expected image from %s in namespace %s: image pull errors:\n  - %s",
			v.imageRef(), v.namespace, strings.Join(imagePullErrors, "\n  - "))
	}

	if !imagePulledSuccessfully {
		msg := fmt.Sprintf("expected image from %s in namespace %s: %d pod(s), no matching container with ImageID yet",
			v.imageRef(), v.namespace, len(pods.Items))
		if len(waitingStates) > 0 {
			msg += fmt.Sprintf(";\n  - %s", strings.Join(waitingStates, "\n  - "))
		}
		return fmt.Errorf("%s", msg)
	}

	return nil
}

func (v verifyImagePulled) imageRef() string {
	if v.imageName != "" {
		return v.imageRepository + "/" + v.imageName
	}
	return v.imageRepository
}

// VerifyImagePulled creates a verifier that checks if an image has been successfully pulled.
// Pass [WithWait] when pods may not have pulled the image yet. This verifier returns stable,
// sorted error text so repeated polls with unchanged state do not produce duplicate log lines.
//
// Parameters:
//   - namespace: the namespace to check for pods
//   - imageRepository: the repository to match (e.g., "registry.redhat.io")
//   - imageName: optional specific image name to match (empty string matches any image from repository)
func VerifyImagePulled(namespace, imageRepository, imageName string, opts ...WaitOption) HostedClusterVerifier {
	return verifyImagePulled{
		namespace:       namespace,
		imageRepository: imageRepository,
		imageName:       imageName,
		wait:            applyWaitOptions(opts),
	}
}
