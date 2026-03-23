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

package framework

import (
	"context"
	"errors"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/utils/set"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

// SelectNodesBelongingToNodePool groups nodes by the HyperShift node label (hypershift/v1beta1.NodePoolLabel).
//
// HyperShift sets that label to "<HostedCluster prefix>-<nodePoolName>" (hyphen-separated), e.g. label
// "e2e-cluster-np-init-0" for customer node pool name "np-init-0". A node matches when the label value ends
// with "-<nodePoolName>" (the pool name is the final segment, not a prefix of the whole label).
//
// A Kubernetes LabelSelector is not enough here: it only matches on the full label value (equality / set-based),
// not on a suffix or substring. Tests and callers typically know only the ARM node pool name, not the
// HostedCluster-specific prefix, so the complete label value is unknown until nodes are listed and inspected.
//
// If several distinct label values match (e.g. one nodePoolName is a suffix of another pool’s name so both
// match HasSuffix), it returns the nodes for the label with the shortest length (tightest match). Under one
// HostedCluster the prefix is shared, so the real pool label is a single map key; same-length ties for
// different strings do not occur in that case.
//
// nodePoolName must be non-empty (otherwise returns an error).
func SelectNodesBelongingToNodePool(nodes []corev1.Node, nodePoolName string) ([]corev1.Node, error) {
	if len(nodePoolName) == 0 {
		return nil, errors.New("nodePoolName is required")
	}
	byLabel := make(map[string][]corev1.Node)
	for i := range nodes {
		lv, ok := nodes[i].Labels[hypershiftv1beta1.NodePoolLabel]
		if !ok {
			continue
		}
		if !strings.HasSuffix(lv, "-"+nodePoolName) {
			continue
		}
		byLabel[lv] = append(byLabel[lv], nodes[i])
	}
	if len(byLabel) == 0 {
		return nil, nil
	}
	// Shortest label is the tightest suffix match when multiple label values matched.
	shortestLabelValue := ""
	for labelValue := range byLabel {
		if len(shortestLabelValue) == 0 || len(labelValue) < len(shortestLabelValue) {
			shortestLabelValue = labelValue
		}
	}
	return byLabel[shortestLabelValue], nil
}

// NodePoolReleaseImages returns release image refs from node.Status.Images for nodes in the given pool
// (lists nodes, then SelectNodesBelongingToNodePool). nodePoolName must be non-empty.
func NodePoolReleaseImages(ctx context.Context, adminRESTConfig *rest.Config, nodePoolName string) (set.Set[string], error) {
	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}
	list, err := kubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list nodes (nodePool=%s): %w", nodePoolName, err)
	}
	nodes, err := SelectNodesBelongingToNodePool(list.Items, nodePoolName)
	if err != nil {
		return nil, err
	}
	images := set.Set[string]{}
	for i := range nodes {
		for _, img := range nodes[i].Status.Images {
			for _, name := range img.Names {
				images.Insert(name)
			}
		}
	}
	return images, nil
}
