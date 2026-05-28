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
	"fmt"
	"time"

	"github.com/onsi/ginkgo/v2"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type verifyCiliumOperational struct {
	ciliumNamespace     string
	ciliumLabelSelector string
}

func (v verifyCiliumOperational) Name() string {
	return "VerifyCiliumOperational"
}

func (v verifyCiliumOperational) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	logger := ginkgo.GinkgoLogr

	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	// Verify that cilium namespace exists
	_, err = kubeClient.CoreV1().Namespaces().Get(ctx, v.ciliumNamespace, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get cilium namespace: %w", err)
	}

	// Wait for all cilium pods to be running
	var lastErr error
	err = wait.PollUntilContextTimeout(ctx, 30*time.Second, 10*time.Minute, true, func(ctx context.Context) (done bool, err error) {
		listOptions := metav1.ListOptions{}
		if v.ciliumLabelSelector != "" {
			listOptions = metav1.ListOptions{LabelSelector: v.ciliumLabelSelector}
		}
		pods, err := kubeClient.CoreV1().Pods(v.ciliumNamespace).List(ctx, listOptions)
		if err != nil {
			lastErr = fmt.Errorf("failed to list pods in %s namespace: %w", v.ciliumNamespace, err)
			logger.Info("failed to list pods", "error", err)
			return false, nil
		}

		if len(pods.Items) == 0 {
			lastErr = fmt.Errorf("no cilium pods found in %s namespace", v.ciliumNamespace)
			logger.Info("no cilium pods found yet in namespace", "namespace", v.ciliumNamespace)
			return false, nil
		}

		notRunningPods := []string{}
		for _, pod := range pods.Items {
			if pod.Status.Phase != corev1.PodRunning {
				notRunningPods = append(notRunningPods, fmt.Sprintf("%s (phase: %s)", pod.Name, pod.Status.Phase))
			}
		}

		if len(notRunningPods) > 0 {
			lastErr = fmt.Errorf("cilium pods not yet running: %v", notRunningPods)
			logger.Info("waiting for cilium pods to be running", "notRunning", notRunningPods)
			return false, nil
		}

		return true, nil
	})
	if err != nil {
		// Log all events in cilium namespace to help debug issues
		events, eventsErr := kubeClient.CoreV1().Events(v.ciliumNamespace).List(ctx, metav1.ListOptions{})
		if eventsErr != nil {
			logger.Error(eventsErr, "failed to list events for debugging", "namespace", v.ciliumNamespace)
		} else {
			logger.Info("listing events for debugging", "namespace", v.ciliumNamespace, "eventCount", len(events.Items))
			for _, event := range events.Items {
				logger.Info("event",
					"type", event.Type,
					"reason", event.Reason,
					"message", event.Message,
					"object", fmt.Sprintf("%s/%s", event.InvolvedObject.Kind, event.InvolvedObject.Name),
					"count", event.Count,
					"firstTimestamp", event.FirstTimestamp,
					"lastTimestamp", event.LastTimestamp,
				)
			}
		}

		if lastErr != nil {
			return fmt.Errorf("not all pods in %s namespace are running: %w", v.ciliumNamespace, lastErr)
		}
		return fmt.Errorf("timeout waiting for pods in %s namespace to be running: %w", v.ciliumNamespace, err)
	}
	logger.Info("all pods are running", "namespace", v.ciliumNamespace)

	return nil
}

// Verifies that all Cilium pods are running in given namespace.
func VerifyCiliumOperational(ciliumNamespace string, ciliumLabelSelector string) HostedClusterVerifier {
	return verifyCiliumOperational{ciliumNamespace: ciliumNamespace, ciliumLabelSelector: ciliumLabelSelector}
}
