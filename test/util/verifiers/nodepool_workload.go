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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type verifyNodePoolWorkload struct {
	nodePoolName string
}

func (v verifyNodePoolWorkload) Name() string {
	return "VerifyNodePoolWorkload"
}

func (v verifyNodePoolWorkload) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	// Find nodes that belong to this nodepool
	nodes, err := kubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list nodes: %w", err)
	}

	var nodeSelector map[string]string
	for _, node := range nodes.Items {
		if labelValue, exists := node.Labels["hypershift.openshift.io/nodePool"]; exists && strings.HasSuffix(labelValue, v.nodePoolName) {
			nodeSelector = map[string]string{"hypershift.openshift.io/nodePool": labelValue}
			break
		}
	}

	if nodeSelector == nil {
		return fmt.Errorf("could not find nodes for nodepool %s", v.nodePoolName)
	}

	podName := fmt.Sprintf("np-verify-%s", strings.ReplaceAll(v.nodePoolName, ".", "-"))

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: "default",
			Labels: map[string]string{
				"test":     "nodepool-verification",
				"nodepool": v.nodePoolName,
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			NodeSelector:  nodeSelector,
			Tolerations: []corev1.Toleration{{
				Key:      "node.cloudprovider.kubernetes.io/uninitialized",
				Operator: corev1.TolerationOpExists,
				Effect:   corev1.TaintEffectNoSchedule,
			}},
			Containers: []corev1.Container{{
				Name:  "test-workload",
				Image: "registry.k8s.io/pause:3.8",
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceMemory: resource.MustParse("10Mi"),
						corev1.ResourceCPU:    resource.MustParse("10m"),
					},
				},
			}},
		},
	}

	_, err = kubeClient.CoreV1().Pods("default").Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create verification pod for nodepool %s: %w", v.nodePoolName, err)
	}

	err = wait.PollUntilContextTimeout(ctx, 5*time.Second, 60*time.Second, false, func(ctx context.Context) (bool, error) {
		createdPod, err := kubeClient.CoreV1().Pods("default").Get(ctx, podName, metav1.GetOptions{})
		if err != nil {
			return false, fmt.Errorf("failed to get pod %s: %w", podName, err)
		}

		switch createdPod.Status.Phase {
		case corev1.PodRunning, corev1.PodSucceeded:
			return true, nil
		case corev1.PodFailed:
			return false, fmt.Errorf("pod %s failed to run on nodepool %s", podName, v.nodePoolName)
		case corev1.PodPending:
			for _, condition := range createdPod.Status.Conditions {
				if condition.Type == corev1.PodScheduled && condition.Status == corev1.ConditionFalse {
					return false, fmt.Errorf("pod %s failed to schedule on nodepool %s: %s", podName, v.nodePoolName, condition.Message)
				}
			}
			return false, nil
		default:
			return false, nil
		}
	})

	// Clean up the pod regardless of outcome
	_ = kubeClient.CoreV1().Pods("default").Delete(ctx, podName, metav1.DeleteOptions{})

	if err != nil {
		return fmt.Errorf("nodepool %s failed workload verification: %w", v.nodePoolName, err)
	}

	return nil
}

func VerifyNodePoolWorkload(nodePoolName string) HostedClusterVerifier {
	return verifyNodePoolWorkload{nodePoolName: nodePoolName}
}
