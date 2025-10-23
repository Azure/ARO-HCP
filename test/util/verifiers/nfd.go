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

type verifyNFDWorkerDaemonSet struct {
	namespace string
}

func (v verifyNFDWorkerDaemonSet) Name() string {
	return "VerifyNFDWorkerDaemonSet"
}

func (v verifyNFDWorkerDaemonSet) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	// Check NFD worker DaemonSet exists and is ready
	daemonSet, err := kubeClient.AppsV1().DaemonSets(v.namespace).Get(ctx, "nfd-worker", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get nfd-worker DaemonSet: %w", err)
	}

	if daemonSet.Status.NumberReady == 0 {
		return fmt.Errorf("nfd-worker DaemonSet has no ready pods (desired: %d, ready: %d)", daemonSet.Status.DesiredNumberScheduled, daemonSet.Status.NumberReady)
	}

	if daemonSet.Status.NumberReady < daemonSet.Status.DesiredNumberScheduled {
		return fmt.Errorf("nfd-worker DaemonSet not fully ready (desired: %d, ready: %d)", daemonSet.Status.DesiredNumberScheduled, daemonSet.Status.NumberReady)
	}

	return nil
}

func VerifyNFDWorkerDaemonSet(namespace string) HostedClusterVerifier {
	return verifyNFDWorkerDaemonSet{namespace: namespace}
}

type verifyNFDNodeLabels struct{}

func (v verifyNFDNodeLabels) Name() string {
	return "VerifyNFDNodeLabels"
}

func (v verifyNFDNodeLabels) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	// Get all nodes
	nodes, err := kubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list nodes: %w", err)
	}

	if len(nodes.Items) == 0 {
		return fmt.Errorf("no nodes found in cluster")
	}

	// Check that at least one node has NFD labels
	// NFD labels are prefixed with "feature.node.kubernetes.io/"
	nfdLabelPrefix := "feature.node.kubernetes.io/"
	foundNFDLabels := false

	for _, node := range nodes.Items {
		for label := range node.Labels {
			if strings.HasPrefix(label, nfdLabelPrefix) {
				foundNFDLabels = true
				break
			}
		}
		if foundNFDLabels {
			break
		}
	}

	if !foundNFDLabels {
		return fmt.Errorf("no NFD labels found on any nodes (expected labels with prefix %q)", nfdLabelPrefix)
	}

	return nil
}

func VerifyNFDNodeLabels() HostedClusterVerifier {
	return verifyNFDNodeLabels{}
}
