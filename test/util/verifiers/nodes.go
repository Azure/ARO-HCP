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
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/blang/semver/v4"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/utils/set"

	"github.com/Azure/ARO-HCP/test/util/framework"
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
		if !nodeReady(&node) {
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

type verifyNodePoolUpgrade struct {
	expectedVersion       string
	nodePoolName          string
	previousReleaseImages set.Set[string]
}

func (v verifyNodePoolUpgrade) Name() string {
	return fmt.Sprintf("VerifyNodePoolUpgrade(expected=%s, nodePool=%s)", v.expectedVersion, v.nodePoolName)
}

func (v verifyNodePoolUpgrade) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	expectedSemver, err := semver.ParseTolerant(v.expectedVersion)
	if err != nil {
		return fmt.Errorf("parse expected version %q: %w", v.expectedVersion, err)
	}

	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}
	nodes, err := kubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list nodes (nodePool=%s): %w", v.nodePoolName, err)
	}
	matchingNodes, err := framework.SelectNodesBelongingToNodePool(nodes.Items, v.nodePoolName)
	if err != nil {
		return err
	}
	var reasons []string
	for i := range matchingNodes {
		node := &matchingNodes[i]
		if !nodeReady(node) {
			reasons = append(reasons, fmt.Sprintf("%s (not ready)", node.Name))
			continue
		}
		if reason := v.nodeVersionInMinor(node, expectedSemver); len(reason) > 0 {
			reasons = append(reasons, reason)
			continue
		}
		if reason := v.nodeReleaseImagesUpdated(node); len(reason) > 0 {
			reasons = append(reasons, reason)
		}
	}
	if len(matchingNodes) == 0 {
		return fmt.Errorf("no nodes found in node pool %q", v.nodePoolName)
	}
	if len(reasons) == 0 {
		return nil
	}

	msg := fmt.Sprintf("node pool upgrade verification failed: %s", strings.Join(reasons, "; "))
	nodesJSON, err := json.Marshal(matchingNodes)
	if err != nil {
		return fmt.Errorf("%s; marshal nodes: %w", msg, err)
	}
	return fmt.Errorf("%s; nodes=%s", msg, string(nodesJSON))
}

// VerifyNodePoolUpgrade verifies after a node pool upgrade (y-stream or z-stream) for nodes in the
// given node pool only: (1) all those nodes are ready, (2) they report a version in the same
// major.minor as expectedVersion, and (3) each node's release images differ from previousReleaseImages.
// nodePoolName must be non-empty. Nodes are selected with
// framework.SelectNodesBelongingToNodePool (hypershift nodePool label ends with -<nodePoolName>; shortest label wins).
func VerifyNodePoolUpgrade(expectedVersion string, nodePoolName string, previousReleaseImages set.Set[string]) HostedClusterVerifier {
	return verifyNodePoolUpgrade{
		expectedVersion:       expectedVersion,
		nodePoolName:          nodePoolName,
		previousReleaseImages: previousReleaseImages,
	}
}

// nodeReady returns true if the node has NodeReady condition status True.
func nodeReady(node *corev1.Node) bool {
	for _, c := range node.Status.Conditions {
		if c.Type == corev1.NodeReady && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// nodeVersionInMinor returns a non-empty reason if the node's version is not in the same major.minor as expectedSemver.
func (v verifyNodePoolUpgrade) nodeVersionInMinor(node *corev1.Node, expectedSemver semver.Version) string {
	cri := node.Status.NodeInfo.ContainerRuntimeVersion
	m := regexp.MustCompile(`rhaos(\d+)\.(\d+)`).FindStringSubmatch(cri)
	nodeVerStr := ""
	if len(m) == 3 {
		nodeVerStr = m[1] + "." + m[2]
	}
	if len(nodeVerStr) == 0 {
		return fmt.Sprintf("%s (no version in containerRuntimeVersion %q)", node.Name, node.Status.NodeInfo.ContainerRuntimeVersion)
	}
	nodeVer, err := semver.ParseTolerant(nodeVerStr)
	if err != nil {
		return fmt.Sprintf("%s (invalid version %q)", node.Name, nodeVerStr)
	}
	if nodeVer.Major != expectedSemver.Major || nodeVer.Minor != expectedSemver.Minor {
		return fmt.Sprintf("%s (version %s not in same minor as expected %s)", node.Name, nodeVerStr, v.expectedVersion)
	}
	return ""
}

// nodeReleaseImagesUpdated returns a non-empty reason if no release image on the node differs from previous.
func (v verifyNodePoolUpgrade) nodeReleaseImagesUpdated(node *corev1.Node) string {
	var currentImgs []string
	for _, img := range node.Status.Images {
		currentImgs = append(currentImgs, img.Names...)
	}
	for _, name := range currentImgs {
		if !v.previousReleaseImages.Has(name) {
			return "" // at least one new image differs from previous
		}
	}
	return fmt.Sprintf("%s (release images unchanged: %v)", node.Name, currentImgs)
}
