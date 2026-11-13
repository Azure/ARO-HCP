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
	"slices"
	"strings"
	"time"

	"github.com/blang/semver/v4"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/Azure/ARO-HCP/test/util/framework"
)

// aksNodeClassHashAnnotation is the provider annotation that Karpenter compares
// to decide static drift.
const aksNodeClassHashAnnotation = "karpenter.azure.com/aksnodeclass-hash"

// NodeClassUpgradeEvidence captures the state that must change when a NodeClass
// version changes, so the verifier can prove drift instead of only replacement.
type NodeClassUpgradeEvidence struct {
	PreviousNodeNames      []string
	PreviousNodeClaimNames []string
	PreviousNodeClassHash  string
}

// NodeClassUpgradeState is the drift state of a NodeClass before an upgrade.
type NodeClassUpgradeState struct {
	NodeClassHash   string
	NodeClaimNames  []string
	NodeClaimHashes map[string]string
}

type verifyKarpenterNodeClassVersion struct {
	name     string
	version  string
	evidence NodeClassUpgradeEvidence
	timeout  time.Duration
}

func (v verifyKarpenterNodeClassVersion) Name() string {
	return fmt.Sprintf("VerifyKarpenterNodeClassVersion(name=%s, expected=%s)", v.name, v.version)
}

func (v verifyKarpenterNodeClassVersion) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	expected, err := semver.Parse(v.version)
	if err != nil {
		return fmt.Errorf("failed to parse expected OpenShift version %q: %w", v.version, err)
	}
	if _, found := ocpToK8sMinor[expected.Minor]; expected.Major != 4 || !found {
		return fmt.Errorf("no Kubernetes minor mapping for OpenShift %s", v.version)
	}
	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %w", err)
	}
	dynamicClient, err := dynamic.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}
	selector := framework.KarpenterNodePoolLabel + "=" + v.name
	return pollUntilReady(ctx, v.Name(), v.timeout, DefaultPollInterval, adminRESTConfig, DefaultDiagnoseTimeout,
		func(ctx context.Context, _ *rest.Config) string {
			return fmt.Sprintf("%s; %s", nodeClassDiagnostics(ctx, dynamicClient, v.name), nodeClaimSummaries(ctx, dynamicClient, v.name))
		}, func(ctx context.Context) error {
			nodeClass, err := dynamicClient.Resource(openShiftAzureNodeClassGVR).Get(ctx, v.name, metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("failed to get OpenShiftAzureNodeClass %q: %w", v.name, err)
			}
			var reasons []string
			for _, field := range []string{"spec", "status"} {
				version, _, err := unstructured.NestedString(nodeClass.Object, field, "version")
				if err != nil || version != v.version {
					reasons = append(reasons, fmt.Sprintf("NodeClass %s.version=%q; expected %q (error=%v)", field, version, v.version, err))
				}
			}
			image, _, err := unstructured.NestedString(nodeClass.Object, "status", "releaseImage")
			if err != nil || image == "" {
				reasons = append(reasons, fmt.Sprintf("NodeClass status.releaseImage=%q; expected a resolved image (error=%v)", image, err))
			}
			if status, _ := unstructuredConditionStatus(nodeClass, "Ready"); status != "True" {
				reasons = append(reasons, fmt.Sprintf("NodeClass Ready=%q; expected True", status))
			}

			// Read each old name directly. A label change must not count as removal.
			for _, name := range v.evidence.PreviousNodeNames {
				node, err := kubeClient.CoreV1().Nodes().Get(ctx, name, metav1.GetOptions{})
				if apierrors.IsNotFound(err) {
					continue
				}
				if err != nil {
					return fmt.Errorf("failed to check removal of old node %q: %w", name, err)
				}
				reasons = append(reasons, fmt.Sprintf("old node %q still exists; expected NotFound (uid=%s kubelet=%s deletionTimestamp=%v finalizers=%v)",
					name, node.UID, node.Status.NodeInfo.KubeletVersion, node.DeletionTimestamp, node.Finalizers))
			}
			for _, name := range v.evidence.PreviousNodeClaimNames {
				if _, err := dynamicClient.Resource(karpenterNodeClaimGVR).Get(ctx, name, metav1.GetOptions{}); apierrors.IsNotFound(err) {
					continue
				} else if err != nil {
					return fmt.Errorf("failed to check removal of old NodeClaim %q: %w", name, err)
				}
				reasons = append(reasons, fmt.Sprintf("old NodeClaim %q still exists; expected NotFound", name))
			}
			currentHash := ""
			if v.evidence.PreviousNodeClassHash != "" {
				aksNodeClass, err := dynamicClient.Resource(aksNodeClassGVR).Get(ctx, v.name, metav1.GetOptions{})
				if err != nil {
					return fmt.Errorf("failed to get AKSNodeClass %q: %w", v.name, err)
				}
				currentHash = aksNodeClass.GetAnnotations()[aksNodeClassHashAnnotation]
				switch {
				case currentHash == "":
					reasons = append(reasons, fmt.Sprintf("AKSNodeClass %q has no %s annotation", v.name, aksNodeClassHashAnnotation))
				case currentHash == v.evidence.PreviousNodeClassHash:
					reasons = append(reasons, fmt.Sprintf("AKSNodeClass %q hash %q did not change; the version change must change the drift source", v.name, currentHash))
				}
			}
			nodes, err := kubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{LabelSelector: selector})
			if err != nil {
				return fmt.Errorf("failed to list Karpenter nodes for %q: %w", v.name, err)
			}
			if len(nodes.Items) == 0 {
				reasons = append(reasons, fmt.Sprintf("no nodes match %s; expected nodes on OpenShift %s", selector, v.version))
			}
			claims, err := dynamicClient.Resource(karpenterNodeClaimGVR).List(ctx, metav1.ListOptions{LabelSelector: selector})
			if err != nil {
				return fmt.Errorf("failed to list Karpenter NodeClaims for %q: %w", v.name, err)
			}
			versionCheck := verifyNodePoolUpgrade{expectedVersion: v.version}
			for i := range nodes.Items {
				node := &nodes.Items[i]
				if node.DeletionTimestamp != nil {
					reasons = append(reasons, fmt.Sprintf("node %q is terminating; expected a live replacement", node.Name))
				}
				if reason := karpenterNodeNotReadyReason(node); reason != "" {
					reasons = append(reasons, fmt.Sprintf("node %q: %s", node.Name, reason))
				}
				if reason := karpenterAdditionalNodeReason(node, claims.Items, KarpenterProvisioningChecks{RequireInitialized: true}); reason != "" {
					reasons = append(reasons, fmt.Sprintf("node %q: %s", node.Name, reason))
				}
				if reason := versionCheck.nodeVersionInMinor(node, expected); reason != "" {
					reasons = append(reasons, reason)
				}
			}
			if len(reasons) > 0 {
				slices.Sort(reasons)
				return fmt.Errorf("expected all Karpenter nodes on OpenShift %s: %s", v.version, strings.Join(reasons, "; "))
			}
			return nil
		})
}

// VerifyKarpenterNodeClassVersion checks the resolved NodeClass version and the
// kubelet minor of every node in its NodePool. All nodes must be initialized,
// Ready, and linked to Azure. Every previous node name must return NotFound.
// Pass no previous names to check the version before an upgrade.
func VerifyKarpenterNodeClassVersion(name, version string, evidence NodeClassUpgradeEvidence, timeout time.Duration) HostedClusterVerifier {
	evidence.PreviousNodeNames = slices.Clone(evidence.PreviousNodeNames)
	evidence.PreviousNodeClaimNames = slices.Clone(evidence.PreviousNodeClaimNames)
	return verifyKarpenterNodeClassVersion{name: name, version: version, evidence: evidence, timeout: timeout}
}

// CaptureNodeClassUpgradeState records the drift state of a NodeClass and its
// NodeClaims. Pass the result to VerifyKarpenterNodeClassVersion after the
// version change.
func CaptureNodeClassUpgradeState(ctx context.Context, adminRESTConfig *rest.Config, name string) (NodeClassUpgradeState, error) {
	state := NodeClassUpgradeState{NodeClaimHashes: map[string]string{}}
	dynamicClient, err := dynamic.NewForConfig(adminRESTConfig)
	if err != nil {
		return state, fmt.Errorf("failed to create dynamic client: %w", err)
	}
	nodeClass, err := dynamicClient.Resource(aksNodeClassGVR).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return state, fmt.Errorf("failed to get AKSNodeClass %q: %w", name, err)
	}
	state.NodeClassHash = nodeClass.GetAnnotations()[aksNodeClassHashAnnotation]
	claims, err := dynamicClient.Resource(karpenterNodeClaimGVR).List(ctx, metav1.ListOptions{
		LabelSelector: framework.KarpenterNodePoolLabel + "=" + name,
	})
	if err != nil {
		return state, fmt.Errorf("failed to list NodeClaims for %q: %w", name, err)
	}
	for i := range claims.Items {
		claim := &claims.Items[i]
		state.NodeClaimNames = append(state.NodeClaimNames, claim.GetName())
		state.NodeClaimHashes[claim.GetName()] = claim.GetAnnotations()[aksNodeClassHashAnnotation]
	}
	slices.Sort(state.NodeClaimNames)
	return state, nil
}
