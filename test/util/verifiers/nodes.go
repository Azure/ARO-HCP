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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type verifyNodesReady struct{}

func (v verifyNodesReady) Name() string {
	return "VerifyNodesReady"
}

func (v verifyNodesReady) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	nodes, err := kubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("can't list nodes in the cluster: %w", err)
	}

	if len(nodes.Items) == 0 {
		return fmt.Errorf("no nodes found in the cluster")
	}

	var notReadyNodes []string
	for _, node := range nodes.Items {
		var nodeIsReady = false
		for _, condition := range node.Status.Conditions {
			if condition.Type == "Ready" && condition.Status == "True" {
				nodeIsReady = true
			}
		}
		if !nodeIsReady {
			notReadyNodes = append(notReadyNodes, node.Name)
		}
	}
	if len(notReadyNodes) > 0 {
		return fmt.Errorf("there are not ready nodes: %s", notReadyNodes)
	}

	return nil
}

func VerifyNodesReady() HostedClusterVerifier {
	return verifyNodesReady{}
}

type verifyNodeCount struct {
	expected int
}

func (v verifyNodeCount) Name() string {
	return fmt.Sprintf("VerifyNodeCount(%d)", v.expected)
}

func (v verifyNodeCount) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	nodes, err := kubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("can't list nodes in the cluster: %w", err)
	}

	if len(nodes.Items) != v.expected {
		return fmt.Errorf("expected %d nodes, found %d", v.expected, len(nodes.Items))
	}

	return nil
}

func VerifyNodeCount(expected int) HostedClusterVerifier {
	return verifyNodeCount{
		expected: expected,
	}
}
