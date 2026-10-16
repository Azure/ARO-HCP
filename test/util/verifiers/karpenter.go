// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/Azure/ARO-HCP/test/util/framework"
)

// karpenterGuestCRDs are the guest-cluster CRDs the standalone karpenter-operator
// installs unconditionally on startup (confirmed against
// karpenter-operator/pkg/controllers/controllers.go and pkg/assets: CoreCRDs
// contribute NodePool/NodeClaim, the Azure CloudProvider contributes
// AKSNodeClass). Their absence means the karpenter-operator container itself
// is broken or never started - not a normal/expected timing state to work
// around - so this is a hard precondition, not a build-them-ourselves fallback.
//
// NodeOverlay is deliberately excluded: it's a temporary inclusion in the
// operator's Azure CRD set pending RFE-9604 (Azure doesn't formally support it
// yet), so treating it as load-bearing here would break this test the day
// that workaround is removed upstream.
var karpenterGuestCRDs = []schema.GroupVersionResource{
	{Group: "karpenter.sh", Version: "v1", Resource: "nodepools"},
	{Group: "karpenter.sh", Version: "v1", Resource: "nodeclaims"},
	{Group: "karpenter.azure.com", Version: "v1beta1", Resource: "aksnodeclasses"},
}

var karpenterNodeClaimGVR = schema.GroupVersionResource{Group: "karpenter.sh", Version: "v1", Resource: "nodeclaims"}

type verifyKarpenterCRDsInstalled struct {
	timeout time.Duration
}

func (v verifyKarpenterCRDsInstalled) Name() string {
	return "VerifyKarpenterCRDsInstalled"
}

func (v verifyKarpenterCRDsInstalled) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	return pollUntilReady(ctx, v.Name(), v.timeout, DefaultPollInterval, adminRESTConfig, DefaultDiagnoseTimeout, nil, func(ctx context.Context) error {
		kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
		if err != nil {
			return fmt.Errorf("failed to create kubernetes client: %w", err)
		}

		var missing []string
		for _, gvr := range karpenterGuestCRDs {
			groupVersion := gvr.Group + "/" + gvr.Version
			resourceList, err := kubeClient.Discovery().ServerResourcesForGroupVersion(groupVersion)
			if err != nil {
				missing = append(missing, fmt.Sprintf("%s.%s (group version not served: %v)", gvr.Resource, gvr.Group, err))
				continue
			}
			found := false
			for _, r := range resourceList.APIResources {
				if r.Name == gvr.Resource {
					found = true
					break
				}
			}
			if !found {
				missing = append(missing, fmt.Sprintf("%s.%s", gvr.Resource, gvr.Group))
			}
		}
		if len(missing) > 0 {
			return fmt.Errorf(
				"guest Karpenter CRDs not served by the API server (missing: %s) - the standalone "+
					"karpenter-operator installs these unconditionally on startup, so their absence means "+
					"the karpenter-operator container is broken or never started, not a normal startup delay",
				strings.Join(missing, ", "),
			)
		}
		return nil
	})
}

// VerifyKarpenterCRDsInstalled asserts that the guest Karpenter CRDs
// (NodePool, NodeClaim, AKSNodeClass) are served by the guest API server, as
// a hard precondition before applying any Karpenter custom resource.
func VerifyKarpenterCRDsInstalled(timeout time.Duration) HostedClusterVerifier {
	return verifyKarpenterCRDsInstalled{timeout: timeout}
}

type verifyAKSNodeClassReady struct {
	name    string
	timeout time.Duration
}

func (v verifyAKSNodeClassReady) Name() string {
	return fmt.Sprintf("VerifyAKSNodeClassReady(name=%s)", v.name)
}

func (v verifyAKSNodeClassReady) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	aksNodeClassGVR := schema.GroupVersionResource{Group: "karpenter.azure.com", Version: "v1beta1", Resource: "aksnodeclasses"}
	return pollUntilReady(ctx, v.Name(), v.timeout, DefaultPollInterval, adminRESTConfig, DefaultDiagnoseTimeout, nil, func(ctx context.Context) error {
		dynamicClient, err := dynamic.NewForConfig(adminRESTConfig)
		if err != nil {
			return fmt.Errorf("failed to create dynamic client: %w", err)
		}
		obj, err := dynamicClient.Resource(aksNodeClassGVR).Get(ctx, v.name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get AKSNodeClass %q: %w", v.name, err)
		}
		status, ok := unstructuredConditionStatus(obj, "Ready")
		if !ok {
			return fmt.Errorf("AKSNodeClass %q has no Ready condition yet", v.name)
		}
		if status != "True" {
			return fmt.Errorf("AKSNodeClass %q Ready condition is %q, not True", v.name, status)
		}
		return nil
	})
}

// VerifyAKSNodeClassReady asserts that the named AKSNodeClass reports
// Ready=True.
func VerifyAKSNodeClassReady(name string, timeout time.Duration) HostedClusterVerifier {
	return verifyAKSNodeClassReady{name: name, timeout: timeout}
}

type verifyKarpenterNodeProvisioned struct {
	nodePoolName string
	timeout      time.Duration
}

func (v verifyKarpenterNodeProvisioned) Name() string {
	return fmt.Sprintf("VerifyKarpenterNodeProvisioned(nodePool=%s)", v.nodePoolName)
}

func (v verifyKarpenterNodeProvisioned) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}
	dynamicClient, err := dynamic.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	return pollUntilReady(ctx, v.Name(), v.timeout, DefaultPollInterval, adminRESTConfig, DefaultDiagnoseTimeout,
		func(ctx context.Context, restConfig *rest.Config) string {
			nodes, err := kubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{
				LabelSelector: framework.KarpenterNodePoolLabel + "=" + v.nodePoolName,
			})
			if err != nil {
				return fmt.Sprintf("failed to list nodes for diagnostics: %v", err)
			}
			return fmt.Sprintf("found %d node(s) with label %s=%s: %s; %s",
				len(nodes.Items), framework.KarpenterNodePoolLabel, v.nodePoolName, nodeSummaries(nodes.Items),
				nodeClaimSummaries(ctx, dynamicClient, v.nodePoolName))
		},
		func(ctx context.Context) error {
			nodes, err := kubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{
				LabelSelector: framework.KarpenterNodePoolLabel + "=" + v.nodePoolName,
			})
			if err != nil {
				return fmt.Errorf("failed to list nodes: %w", err)
			}
			// Included even while len(nodes.Items) == 0: NodeClaim creation
			// timestamps/conditions appear before the corresponding Node object
			// does, so this is the only way to see, e.g., a second NodeClaim
			// being created while the first is still unregistered - the
			// scenario that produced more nodes than the workload needed in a
			// prior run (see cluster_autonode.go's "provision a real node via
			// Karpenter" test).
			nodeClaims := nodeClaimSummaries(ctx, dynamicClient, v.nodePoolName)
			if len(nodes.Items) == 0 {
				return fmt.Errorf("no node yet carries label %s=%s (%s)", framework.KarpenterNodePoolLabel, v.nodePoolName, nodeClaims)
			}

			var lastReason string
			for i := range nodes.Items {
				node := &nodes.Items[i]
				if reason := karpenterNodeNotReadyReason(node); reason != "" {
					lastReason = fmt.Sprintf("node %q: %s", node.Name, reason)
					continue
				}
				return nil // found one fully-ready, fully-linked node
			}
			return fmt.Errorf("%d node(s) present but not yet ready/linked; most recent: %s (%s)", len(nodes.Items), lastReason, nodeClaims)
		},
	)
}

// nodeClaimSummaries lists NodeClaims for nodePoolName with their creation
// timestamp and Launched/Registered/Initialized condition statuses, to
// correlate exactly when Karpenter decided to provision each one - the
// corresponding Node object (and this package's other node-based
// diagnostics) only appears later, once the VM has booted and registered.
func nodeClaimSummaries(ctx context.Context, dynamicClient dynamic.Interface, nodePoolName string) string {
	claims, err := dynamicClient.Resource(karpenterNodeClaimGVR).List(ctx, metav1.ListOptions{
		LabelSelector: framework.KarpenterNodePoolLabel + "=" + nodePoolName,
	})
	if err != nil {
		return fmt.Sprintf("failed to list NodeClaims for diagnostics: %v", err)
	}
	parts := make([]string, 0, len(claims.Items))
	for i := range claims.Items {
		claim := &claims.Items[i]
		conditionSummaries := make([]string, 0, 3)
		for _, conditionType := range []string{"Launched", "Registered", "Initialized"} {
			status, ok := unstructuredConditionStatus(claim, conditionType)
			if !ok {
				status = "Unknown"
			}
			conditionSummaries = append(conditionSummaries, fmt.Sprintf("%s=%s", conditionType, status))
		}
		deletionTimestamp := "none"
		if dt := claim.GetDeletionTimestamp(); dt != nil {
			deletionTimestamp = dt.Format(time.RFC3339)
		}
		parts = append(parts, fmt.Sprintf("%s(created=%s,%s,deletionTimestamp=%s,finalizers=%v)",
			claim.GetName(), claim.GetCreationTimestamp().Format(time.RFC3339), strings.Join(conditionSummaries, ","),
			deletionTimestamp, claim.GetFinalizers()))
	}
	return fmt.Sprintf("%d NodeClaim(s): %s", len(claims.Items), strings.Join(parts, ", "))
}

// karpenterNodeNotReadyReason returns a non-empty reason if node is not yet
// ready, schedulable, and linked to its Azure VM instance. Checking
// Spec.ProviderID and the absence of the cloud-provider uninitialized taint
// specifically validates the HyperShift fork's Azure-only nodeproviderid
// controller: without it, Karpenter nodes never link/become Ready in ARO-HCP,
// so an assertion that stops at "Ready=True" could pass even if that
// controller silently regressed in a future image.
func karpenterNodeNotReadyReason(node *corev1.Node) string {
	if !nodeReadyAndSchedulable(node) {
		return "not Ready+schedulable yet"
	}
	if node.Spec.ProviderID == "" {
		return "Ready but Spec.ProviderID is still empty"
	}
	for _, t := range node.Spec.Taints {
		if t.Key == framework.NodeUninitializedTaint {
			return fmt.Sprintf("Ready but still carries the %q taint", framework.NodeUninitializedTaint)
		}
	}
	return ""
}

func nodeSummaries(nodes []corev1.Node) string {
	parts := make([]string, 0, len(nodes))
	for i := range nodes {
		parts = append(parts, fmt.Sprintf("%s(ready=%v,providerID=%q)", nodes[i].Name, nodeReadyAndSchedulable(&nodes[i]), nodes[i].Spec.ProviderID))
	}
	return strings.Join(parts, ", ")
}

// VerifyKarpenterNodeProvisioned asserts that a Node carrying
// framework.KarpenterNodePoolLabel=nodePoolName exists, is Ready and
// schedulable, has Spec.ProviderID set, and no longer carries the
// cloud-provider uninitialized taint.
func VerifyKarpenterNodeProvisioned(nodePoolName string, timeout time.Duration) HostedClusterVerifier {
	return verifyKarpenterNodeProvisioned{nodePoolName: nodePoolName, timeout: timeout}
}

type verifyKarpenterDeprovisioned struct {
	nodePoolName string
	timeout      time.Duration
}

func (v verifyKarpenterDeprovisioned) Name() string {
	return fmt.Sprintf("VerifyKarpenterDeprovisioned(nodePool=%s)", v.nodePoolName)
}

func (v verifyKarpenterDeprovisioned) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}
	dynamicClient, err := dynamic.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	selector := framework.KarpenterNodePoolLabel + "=" + v.nodePoolName
	return pollUntilReady(ctx, v.Name(), v.timeout, DefaultPollInterval, adminRESTConfig, DefaultDiagnoseTimeout,
		func(ctx context.Context, restConfig *rest.Config) string {
			nodes, err := kubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{LabelSelector: selector})
			if err != nil {
				return fmt.Sprintf("failed to list nodes for diagnostics: %v", err)
			}
			return fmt.Sprintf("%s; %s", nodeTerminationSummaries(nodes.Items), nodeClaimSummaries(ctx, dynamicClient, v.nodePoolName))
		},
		func(ctx context.Context) error {
			nodes, err := kubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{LabelSelector: selector})
			if err != nil {
				return fmt.Errorf("failed to list nodes: %w", err)
			}
			// Included on every failure (not just via the diagnose callback):
			// deletionTimestamp/finalizers pinpoint whether a stuck teardown is a
			// blocked drain (node still has a deletionTimestamp but pods won't
			// evict), a stuck finalizer (deletionTimestamp set, finalizers
			// non-empty, node otherwise looks abandoned), or Karpenter/Azure never
			// even starting termination (no deletionTimestamp at all).
			if len(nodes.Items) > 0 {
				return fmt.Errorf("%d node(s) with label %s still present: %s (%s)",
					len(nodes.Items), selector, nodeTerminationSummaries(nodes.Items), nodeClaimSummaries(ctx, dynamicClient, v.nodePoolName))
			}

			claims, err := dynamicClient.Resource(karpenterNodeClaimGVR).List(ctx, metav1.ListOptions{LabelSelector: selector})
			if err != nil {
				return fmt.Errorf("failed to list NodeClaims: %w", err)
			}
			if len(claims.Items) > 0 {
				return fmt.Errorf("%d NodeClaim(s) with label %s still present: %s",
					len(claims.Items), selector, nodeClaimSummaries(ctx, dynamicClient, v.nodePoolName))
			}
			return nil
		})
}

// nodeTerminationSummaries lists nodes with their deletionTimestamp,
// finalizers, and taints, to distinguish why a node isn't disappearing during
// teardown: no deletionTimestamp at all means Karpenter/Azure never started
// terminating it; a deletionTimestamp with finalizers still set means
// something (a stuck drain, a stuck finalizer controller) is blocking removal.
func nodeTerminationSummaries(nodes []corev1.Node) string {
	parts := make([]string, 0, len(nodes))
	for i := range nodes {
		node := &nodes[i]
		deletionTimestamp := "none"
		if dt := node.GetDeletionTimestamp(); dt != nil {
			deletionTimestamp = dt.Format(time.RFC3339)
		}
		taints := make([]string, 0, len(node.Spec.Taints))
		for _, t := range node.Spec.Taints {
			taints = append(taints, t.Key)
		}
		parts = append(parts, fmt.Sprintf("%s(deletionTimestamp=%s,finalizers=%v,taints=%v)",
			node.Name, deletionTimestamp, node.Finalizers, taints))
	}
	return fmt.Sprintf("%d node(s): %s", len(nodes), strings.Join(parts, ", "))
}

// VerifyKarpenterDeprovisioned asserts that no Node or NodeClaim carrying
// framework.KarpenterNodePoolLabel=nodePoolName remains, for use during
// teardown to confirm Karpenter actually deprovisioned what it created
// (avoiding a leaked Azure VM going unnoticed).
func VerifyKarpenterDeprovisioned(nodePoolName string, timeout time.Duration) HostedClusterVerifier {
	return verifyKarpenterDeprovisioned{nodePoolName: nodePoolName, timeout: timeout}
}

// unstructuredConditionStatus reads status.conditions[type=conditionType].status
// from an unstructured object following the standard metav1.Condition shape.
func unstructuredConditionStatus(obj *unstructured.Unstructured, conditionType string) (string, bool) {
	conditions, found, err := unstructured.NestedSlice(obj.Object, "status", "conditions")
	if err != nil || !found {
		return "", false
	}
	for _, c := range conditions {
		condition, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if condition["type"] != conditionType {
			continue
		}
		status, ok := condition["status"].(string)
		return status, ok
	}
	return "", false
}
