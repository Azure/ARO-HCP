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
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type verifyFIPSEnabledImpl struct{}

func (v verifyFIPSEnabledImpl) Name() string {
	return "VerifyFIPSEnabled"
}

func (v verifyFIPSEnabledImpl) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	nodes, err := kubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list nodes: %w", err)
	}

	if len(nodes.Items) == 0 {
		return fmt.Errorf("no nodes found in the cluster")
	}

	for _, node := range nodes.Items {
		fipsEnabled, err := checkNodeFIPSMode(ctx, kubeClient, &node)
		if err != nil {
			return fmt.Errorf("failed to check FIPS mode on node %s: %w", node.Name, err)
		}
		if !fipsEnabled {
			return fmt.Errorf("FIPS mode is not enabled on node %s", node.Name)
		}
	}

	return nil
}

func checkNodeFIPSMode(ctx context.Context, kubeClient *kubernetes.Clientset, node *corev1.Node) (bool, error) {
	namespace, err := kubeClient.CoreV1().Namespaces().Create(
		ctx,
		&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "e2e-fips-test-",
			},
		},
		metav1.CreateOptions{})
	if err != nil {
		return false, fmt.Errorf("failed to create namespace: %w", err)
	}

	defer func() {
		_ = kubeClient.CoreV1().Namespaces().Delete(ctx, namespace.Name, metav1.DeleteOptions{})
	}()

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "fips-check-",
			Namespace:    namespace.Name,
		},
		Spec: corev1.PodSpec{
			NodeName:      node.Name,
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				{
					Name:    "fips-check",
					Image:   "mcr.microsoft.com/azurelinux/distroless/debug:3.0",
					Command: []string{"busybox", "cat", "/proc/sys/crypto/fips_enabled"},
				},
			},
		},
	}

	createdPod, err := kubeClient.CoreV1().Pods(namespace.Name).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return false, fmt.Errorf("failed to create FIPS check pod: %w", err)
	}

	defer func() {
		_ = kubeClient.CoreV1().Pods(namespace.Name).Delete(ctx, createdPod.Name, metav1.DeleteOptions{})
	}()

	var prevPodPhase corev1.PodPhase
	err = wait.PollUntilContextTimeout(ctx, 2*time.Second, 10*time.Minute, true, func(ctx context.Context) (bool, error) {
		p, err := kubeClient.CoreV1().Pods(namespace.Name).Get(ctx, createdPod.Name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}

		if prevPodPhase == "" {
			prevPodPhase = p.Status.Phase
		}

		// track pod state transitions for better visibility in test output
		if prevPodPhase != p.Status.Phase {
			ginkgo.GinkgoWriter.Printf("Pod state transitioned from '%s' to '%s'\n", prevPodPhase, p.Status.Phase)
			prevPodPhase = p.Status.Phase
		}

		if p.Status.Phase == corev1.PodFailed {
			// Log container status for debugging
			var errMsg strings.Builder
			errMsg.WriteString(fmt.Sprintf("pod %s failed", createdPod.Name))
			for _, cs := range p.Status.ContainerStatuses {
				if cs.State.Terminated != nil {
					errMsg.WriteString(fmt.Sprintf("; container %s terminated with exit code %d, reason: %s, message: %s",
						cs.Name, cs.State.Terminated.ExitCode, cs.State.Terminated.Reason, cs.State.Terminated.Message))
				}
			}
			// Attempt to fetch pod logs for diagnostics
			logs, logErr := kubeClient.CoreV1().Pods(namespace.Name).GetLogs(createdPod.Name, &corev1.PodLogOptions{}).Do(ctx).Raw()
			if logErr == nil && len(logs) > 0 {
				errMsg.WriteString(fmt.Sprintf("; pod logs: %s", string(logs)))
			}
			return false, fmt.Errorf("%s", errMsg.String())
		}

		return p.Status.Phase == corev1.PodSucceeded, nil
	})
	if err != nil {
		return false, fmt.Errorf("failed waiting for pod to complete: %w", err)
	}

	// Read pod logs
	logs, err := kubeClient.CoreV1().Pods(namespace.Name).GetLogs(createdPod.Name, &corev1.PodLogOptions{}).Do(ctx).Raw()
	if err != nil {
		return false, fmt.Errorf("failed to get pod logs: %w", err)
	}

	output := strings.TrimSpace(string(logs))
	return output == "1", nil
}

func VerifyFIPSEnabled() HostedClusterVerifier {
	return verifyFIPSEnabledImpl{}
}
