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
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/blang/semver/v4"
	"github.com/onsi/ginkgo/v2"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/utils/set"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	hcpsdk "github.com/Azure/ARO-HCP/test/sdk/v20251223preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
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
	clusterName string
	expected    int
}

func (v verifyNodeCount) Name() string {
	return fmt.Sprintf("VerifyNodeCount(cluster=%s, expected=%d)", v.clusterName, v.expected)
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
		if len(nodes.Items) == 0 {
			return fmt.Errorf("cluster %s: expected %d nodes, found 0", v.clusterName, v.expected)
		}
		return fmt.Errorf("cluster %s: expected %d nodes, found %d; nodes per pool: %s",
			v.clusterName, v.expected, len(nodes.Items), formatNodesByPool(nodes.Items))
	}

	return nil
}

func VerifyNodeCount(clusterName string, expected int) HostedClusterVerifier {
	return verifyNodeCount{
		clusterName: clusterName,
		expected:    expected,
	}
}

// nodePoolNameRegex matches valid ARO-HCP node pool resource names
// Same pattern as internal/validation/validators.go nodePoolResourceName, which is unexported.
var nodePoolNameRegex = regexp.MustCompile(`^[a-zA-Z][-a-zA-Z0-9]{1,13}[a-zA-Z0-9]$`)

// extractNodePoolName extracts the node pool name from a node's node pool label value.
// The node pool label format is "<HostedCluster prefix>-<nodePoolName>". This scans left to right
// for each "-" and returns the first suffix that matches the node pool name regex.
// Falls back to the full label if no suffix matches.
func extractNodePoolName(nodePoolLabel string) string {
	for i := range len(nodePoolLabel) {
		if nodePoolLabel[i] == '-' {
			suffix := nodePoolLabel[i+1:]
			if nodePoolNameRegex.MatchString(suffix) {
				return suffix
			}
		}
	}
	return nodePoolLabel
}

// formatNodesByPool groups node names by their node pool name (extracted from the
// node's node pool label value) and returns a sorted, human-readable summary such as:
//
//	np-scale-down: [node-1, node-2], np-scale-up: [node-3]
func formatNodesByPool(nodes []corev1.Node) string {
	const noLabel = "<no-nodepool-label>"
	byNodePool := make(map[string][]string)
	for i := range nodes {
		nodePoolLabel, ok := nodes[i].Labels[hypershiftv1beta1.NodePoolLabel]
		nodePoolName := noLabel
		if ok {
			nodePoolName = extractNodePoolName(nodePoolLabel)
		}
		byNodePool[nodePoolName] = append(byNodePool[nodePoolName], nodes[i].Name)
	}

	nodePoolNames := make([]string, 0, len(byNodePool))
	for name := range byNodePool {
		nodePoolNames = append(nodePoolNames, name)
	}
	sort.Strings(nodePoolNames)

	parts := make([]string, 0, len(nodePoolNames))
	for _, nodePoolName := range nodePoolNames {
		nodeNames := byNodePool[nodePoolName]
		sort.Strings(nodeNames)
		parts = append(parts, fmt.Sprintf("%s: [%s]", nodePoolName, strings.Join(nodeNames, ", ")))
	}
	return strings.Join(parts, ", ")
}

type verifyNodePoolUpgrade struct {
	expectedVersion       string
	nodePoolName          string
	previousReleaseImages set.Set[string]
}

// nodeSummary is a compact representation of a node for error messages.
// Full node objects can be 10KB+ due to annotations and are too large for error output.
type nodeSummary struct {
	Name                    string   `json:"name"`
	Ready                   bool     `json:"ready"`
	ContainerRuntimeVersion string   `json:"containerRuntimeVersion"`
	ReleaseImages           []string `json:"releaseImages,omitempty"`
}

func summarizeNodes(nodes []corev1.Node) []nodeSummary {
	summaries := make([]nodeSummary, len(nodes))
	for i, node := range nodes {
		var releaseImages []string
		for _, img := range node.Status.Images {
			releaseImages = append(releaseImages, img.Names...)
		}
		summaries[i] = nodeSummary{
			Name:                    node.Name,
			Ready:                   nodeReady(to.Ptr(node)),
			ContainerRuntimeVersion: node.Status.NodeInfo.ContainerRuntimeVersion,
			ReleaseImages:           releaseImages,
		}
	}
	return summaries
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
	nodeSummaries := summarizeNodes(matchingNodes)
	nodeSummariesJSON, err := json.Marshal(nodeSummaries)
	if err != nil {
		return fmt.Errorf("%s; marshal node summaries: %w", msg, err)
	}
	return fmt.Errorf("%s; nodes=%s", msg, string(nodeSummariesJSON))
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

// nodeReadyAndSchedulable returns true if the node is ready and schedulable.
// Excludes cordoned nodes (Unschedulable=true), which occurs during rolling
// upgrades when old nodes are being replaced.
func nodeReadyAndSchedulable(node *corev1.Node) bool {
	return nodeReady(node) && !node.Spec.Unschedulable
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

type verifyNodesSchedulable struct{}

func (v verifyNodesSchedulable) Name() string { return "VerifyNodesSchedulable" }

func (v verifyNodesSchedulable) Verify(ctx context.Context, restConfig *rest.Config) error {
	logger := ginkgo.GinkgoLogr
	kubeClient, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	var lastErr error
	var previousError string
	err = wait.PollUntilContextTimeout(ctx, 30*time.Second, 10*time.Minute, true, func(ctx context.Context) (bool, error) {
		nodes, err := kubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
		if err != nil {
			lastErr = fmt.Errorf("can't list nodes: %w", err)
			return false, nil
		}
		if len(nodes.Items) == 0 {
			lastErr = fmt.Errorf("no nodes found")
			return false, nil
		}
		var unschedulable []string
		for i := range nodes.Items {
			if nodes.Items[i].Spec.Unschedulable {
				unschedulable = append(unschedulable, nodes.Items[i].Name)
			}
		}
		if len(unschedulable) > 0 {
			lastErr = fmt.Errorf("%d of %d nodes unschedulable (cordoned): %v", len(unschedulable), len(nodes.Items), unschedulable)
			currentError := lastErr.Error()
			if currentError != previousError {
				logger.Info("Verifier check", "name", v.Name(), "status", "failed", "error", currentError)
				previousError = currentError
			}
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		if lastErr != nil {
			return fmt.Errorf("%s timed out: %w", v.Name(), lastErr)
		}
		return fmt.Errorf("%s: %w", v.Name(), err)
	}
	logger.Info("All nodes schedulable")
	return nil
}

// VerifyNodesSchedulable returns a verifier that polls until no nodes are
// cordoned (Unschedulable=true). Polls every 30s for up to 10 minutes with
// delta-only logging.
func VerifyNodesSchedulable() HostedClusterVerifier {
	return verifyNodesSchedulable{}
}

type verifyNodePoolNodeCount struct {
	nodePoolsClient *hcpsdk.NodePoolsClient
	resourceGroup   string
	clusterName     string
}

func (v verifyNodePoolNodeCount) Name() string {
	return fmt.Sprintf("VerifyNodePoolNodeCount(cluster=%s)", v.clusterName)
}

func (v verifyNodePoolNodeCount) Verify(ctx context.Context, restConfig *rest.Config) error {
	logger := ginkgo.GinkgoLogr
	kubeClient, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	var lastErr error
	var previousError string
	err = wait.PollUntilContextTimeout(ctx, 30*time.Second, 10*time.Minute, true, func(ctx context.Context) (bool, error) {
		expectedPools := make(map[string]int)
		pager := v.nodePoolsClient.NewListByParentPager(v.resourceGroup, v.clusterName, nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				lastErr = fmt.Errorf("list node pools: %w", err)
				return false, nil
			}
			for _, np := range page.Value {
				if np.Name == nil || np.Properties == nil {
					continue
				}
				if np.Properties.ProvisioningState == nil || *np.Properties.ProvisioningState != hcpsdk.ProvisioningStateSucceeded {
					continue
				}
				if np.Properties.Replicas == nil {
					continue
				}
				expectedPools[*np.Name] = int(*np.Properties.Replicas)
			}
		}

		if len(expectedPools) == 0 {
			lastErr = fmt.Errorf("no successfully provisioned node pools found for cluster %s", v.clusterName)
			return false, nil
		}

		nodes, err := kubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
		if err != nil {
			lastErr = fmt.Errorf("can't list nodes: %w", err)
			return false, nil
		}

		var errs []error
		expectedTotal := 0
		for poolName, expectedReplicas := range expectedPools {
			expectedTotal += expectedReplicas
			matchingNodes, err := framework.SelectNodesBelongingToNodePool(nodes.Items, poolName)
			if err != nil {
				errs = append(errs, fmt.Errorf("pool %q: %w", poolName, err))
				continue
			}
			if len(matchingNodes) != expectedReplicas {
				errs = append(errs, fmt.Errorf("pool %q: expected %d nodes, found %d",
					poolName, expectedReplicas, len(matchingNodes)))
			}
		}
		if len(nodes.Items) != expectedTotal {
			errs = append(errs, fmt.Errorf("expected %d total nodes, found %d; nodes per pool: %s",
				expectedTotal, len(nodes.Items), formatNodesByPool(nodes.Items)))
		}

		if len(errs) > 0 {
			lastErr = errors.Join(errs...)
			currentError := lastErr.Error()
			if currentError != previousError {
				logger.Info("Verifier check", "name", v.Name(), "status", "failed", "error", currentError)
				previousError = currentError
			}
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		if lastErr != nil {
			return fmt.Errorf("%s timed out: %w", v.Name(), lastErr)
		}
		return fmt.Errorf("%s: %w", v.Name(), err)
	}
	logger.Info("Node pool node counts verified", "cluster", v.clusterName)
	return nil
}

// VerifyNodePoolNodeCount returns a verifier that queries the ARM API for
// successfully provisioned node pools, then polls until the per-pool
// Kubernetes node counts match the expected replicas. Polls every 30s for
// up to 10 minutes with delta-only logging.
func VerifyNodePoolNodeCount(nodePoolsClient *hcpsdk.NodePoolsClient, resourceGroup, clusterName string) HostedClusterVerifier {
	return verifyNodePoolNodeCount{
		nodePoolsClient: nodePoolsClient,
		resourceGroup:   resourceGroup,
		clusterName:     clusterName,
	}
}

// NodePoolVerifiers returns the standard set of node pool verifiers:
// node readiness, schedulability, per-pool count matching, and deployment
// log accessibility.
func NodePoolVerifiers(nodePoolsClient *hcpsdk.NodePoolsClient, resourceGroup, clusterName string) []HostedClusterVerifier {
	return []HostedClusterVerifier{
		VerifyNodesReady(),
		VerifyNodesSchedulable(),
		VerifyNodePoolNodeCount(nodePoolsClient, resourceGroup, clusterName),
		VerifyGetDeploymentLogs("openshift-ingress", "router-default", "router"),
	}
}
