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
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/Azure/ARO-HCP/test/util/framework"
)

// karpenterGuestCRDs lists the guest cluster CRDs that the standalone
// karpenter-operator installs at startup.
//
// Source: karpenter-operator/pkg/controllers/controllers.go and pkg/assets. The
// CoreCRDs set supplies NodePool and NodeClaim. The Azure CloudProvider supplies
// AKSNodeClass and OpenShiftAzureNodeClass.
//
// A missing CRD shows that the operator is broken or did not start. It is not a
// normal timing state. Therefore this list is a hard precondition. The test does
// not create these CRDs.
//
// NodeOverlay is excluded on purpose. The operator adds it to the Azure CRD set
// as a temporary measure while RFE-9604 is open, because Azure does not formally
// support it. A dependency on NodeOverlay would break this test when that
// workaround goes away.
var karpenterGuestCRDs = []schema.GroupVersionResource{
	{Group: "karpenter.sh", Version: "v1", Resource: "nodepools"},
	{Group: "karpenter.sh", Version: "v1", Resource: "nodeclaims"},
	{Group: "karpenter.azure.com", Version: "v1beta1", Resource: "aksnodeclasses"},
	{Group: "karpenter.hypershift.openshift.io", Version: "v1alpha1", Resource: "openshiftazurenodeclasses"},
}

var karpenterNodeClaimGVR = schema.GroupVersionResource{Group: "karpenter.sh", Version: "v1", Resource: "nodeclaims"}

var openShiftAzureNodeClassGVR = schema.GroupVersionResource{Group: "karpenter.hypershift.openshift.io", Version: "v1alpha1", Resource: "openshiftazurenodeclasses"}
var aksNodeClassGVR = schema.GroupVersionResource{Group: "karpenter.azure.com", Version: "v1beta1", Resource: "aksnodeclasses"}

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
				"guest Karpenter CRDs are not served: missing %s. "+
					"The karpenter-operator installs them at startup. "+
					"A missing CRD means the container is broken or did not start, not a startup delay",
				strings.Join(missing, ", "),
			)
		}
		return nil
	})
}

// VerifyKarpenterCRDsInstalled checks that the API server serves the guest
// Karpenter CRDs: NodePool, NodeClaim, AKSNodeClass, and
// OpenShiftAzureNodeClass. Call this function before the test applies a
// Karpenter custom resource.
func VerifyKarpenterCRDsInstalled(timeout time.Duration) HostedClusterVerifier {
	return verifyKarpenterCRDsInstalled{timeout: timeout}
}

type verifyOpenShiftAzureNodeClassReady struct {
	name    string
	timeout time.Duration
}

func (v verifyOpenShiftAzureNodeClassReady) Name() string {
	return fmt.Sprintf("VerifyOpenShiftAzureNodeClassReady(name=%s)", v.name)
}

func (v verifyOpenShiftAzureNodeClassReady) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	dynamicClient, err := dynamic.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}
	return pollUntilReady(ctx, v.Name(), v.timeout, DefaultPollInterval, adminRESTConfig, DefaultDiagnoseTimeout,
		func(ctx context.Context, _ *rest.Config) string {
			return nodeClassDiagnostics(ctx, dynamicClient, v.name)
		}, func(ctx context.Context) error {
			obj, err := dynamicClient.Resource(openShiftAzureNodeClassGVR).Get(ctx, v.name, metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("failed to get OpenShiftAzureNodeClass %q: %w", v.name, err)
			}
			status, ok := unstructuredConditionStatus(obj, "Ready")
			if !ok {
				return fmt.Errorf("OpenShiftAzureNodeClass %q has no Ready condition yet: %s", v.name, nodeClassStatus(obj))
			}
			if status != "True" {
				return fmt.Errorf("OpenShiftAzureNodeClass %q Ready condition is %q, not True: %s", v.name, status, nodeClassStatus(obj))
			}
			return nil
		})
}

// VerifyOpenShiftAzureNodeClassReady checks the canonical Ready condition. That
// condition covers version resolution and provider readiness. Provider GET calls
// are for diagnostics only.
func VerifyOpenShiftAzureNodeClassReady(name string, timeout time.Duration) HostedClusterVerifier {
	return verifyOpenShiftAzureNodeClassReady{name: name, timeout: timeout}
}

func nodeClassStatus(obj *unstructured.Unstructured) string {
	status, err := json.Marshal(obj.Object["status"])
	if err != nil {
		return fmt.Sprintf("cannot encode status: %v", err)
	}
	return string(status)
}

func nodeClassDiagnostics(ctx context.Context, client dynamic.Interface, name string) string {
	var parts []string
	for _, gvr := range []schema.GroupVersionResource{openShiftAzureNodeClassGVR, aksNodeClassGVR} {
		obj, err := client.Resource(gvr).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			parts = append(parts, fmt.Sprintf("%s %q: %v", gvr.Resource, name, err))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s %q: status=%s deletionTimestamp=%v finalizers=%v",
			gvr.Resource, name, nodeClassStatus(obj), obj.GetDeletionTimestamp(), obj.GetFinalizers()))
	}
	return strings.Join(parts, "; ")
}

func verifyNodeClassesDeleted(ctx context.Context, client dynamic.Interface, name string) error {
	for _, gvr := range []schema.GroupVersionResource{openShiftAzureNodeClassGVR, aksNodeClassGVR} {
		obj, err := client.Resource(gvr).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("failed to check deletion of %s %q: %w", gvr.Resource, name, err)
		}
		return fmt.Errorf("%s %q remains: deletionTimestamp=%v finalizers=%v", gvr.Resource, name, obj.GetDeletionTimestamp(), obj.GetFinalizers())
	}
	return nil
}

// KarpenterProvisioningChecks adds optional checks to the default readiness
// gate. A zero value keeps the default behavior.
type KarpenterProvisioningChecks struct {
	ExpectedStartupTaints   []corev1.Taint
	RequireInitialized      bool
	AbsentTaintKeys         []string
	MaxConcurrentNodeClaims int
	NodeName                string
}

type verifyKarpenterNodeProvisioned struct {
	nodePoolName string
	timeout      time.Duration
	checks       KarpenterProvisioningChecks
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

	pollCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	var boundErr error
	err = pollUntilReady(pollCtx, v.Name(), v.timeout, DefaultPollInterval, adminRESTConfig, DefaultDiagnoseTimeout,
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
			claims, claimErr := dynamicClient.Resource(karpenterNodeClaimGVR).List(ctx, metav1.ListOptions{
				LabelSelector: framework.KarpenterNodePoolLabel + "=" + v.nodePoolName,
			})
			nodeClaims := fmt.Sprintf("failed to list NodeClaims for diagnostics: %v", claimErr)
			if claimErr == nil {
				nodeClaims = summarizeNodeClaims(claims.Items)
				if err := checkKarpenterNodeClaims(claims.Items, v.checks); err != nil {
					if v.checks.MaxConcurrentNodeClaims > 0 && activeNodeClaimCount(claims.Items) > v.checks.MaxConcurrentNodeClaims {
						boundErr = fmt.Errorf("%w; nodes: %s (%s)", err, nodeSummaries(nodes.Items), nodeClaims)
						cancel(boundErr)
						return boundErr
					}
					return fmt.Errorf("%w (%s)", err, nodeClaims)
				}
			} else if len(v.checks.ExpectedStartupTaints) > 0 || v.checks.MaxConcurrentNodeClaims > 0 {
				return fmt.Errorf("failed to list NodeClaims for provisioning checks: %w", claimErr)
			}
			if len(nodes.Items) == 0 {
				return fmt.Errorf("no node yet carries label %s=%s (%s)", framework.KarpenterNodePoolLabel, v.nodePoolName, nodeClaims)
			}

			var reasons []string
			for i := range nodes.Items {
				node := &nodes.Items[i]
				if v.checks.NodeName != "" && node.Name != v.checks.NodeName {
					continue
				}
				if reason := karpenterNodeNotReadyReason(node); reason != "" {
					reasons = append(reasons, fmt.Sprintf("node %q: %s", node.Name, reason))
					continue
				}
				var claimItems []unstructured.Unstructured
				if claims != nil {
					claimItems = claims.Items
				}
				if reason := karpenterAdditionalNodeReason(node, claimItems, v.checks); reason != "" {
					reasons = append(reasons, fmt.Sprintf("node %q: %s", node.Name, reason))
					continue
				}
				return nil // found one fully-ready, fully-linked node
			}
			slices.Sort(reasons)
			return fmt.Errorf("no node satisfies readiness/link checks (required node=%q): %s; nodes: %s (%s)", v.checks.NodeName, strings.Join(reasons, "; "), nodeSummaries(nodes.Items), nodeClaims)
		},
	)
	if boundErr != nil {
		return context.Cause(pollCtx)
	}
	return err
}

func activeNodeClaimCount(claims []unstructured.Unstructured) int {
	count := 0
	for i := range claims {
		if claims[i].GetDeletionTimestamp() == nil {
			count++
		}
	}
	return count
}

func checkKarpenterNodeClaims(claims []unstructured.Unstructured, checks KarpenterProvisioningChecks) error {
	if count := activeNodeClaimCount(claims); checks.MaxConcurrentNodeClaims > 0 && count > checks.MaxConcurrentNodeClaims {
		return fmt.Errorf("concurrent NodeClaim count %d exceeds limit %d: check startup taint propagation and node replacement", count, checks.MaxConcurrentNodeClaims)
	}
	var mismatches []string
	for i := range claims {
		claim := &claims[i]
		if claim.GetDeletionTimestamp() != nil || len(checks.ExpectedStartupTaints) == 0 {
			continue
		}
		declared, _, err := unstructured.NestedSlice(claim.Object, "spec", "startupTaints")
		pairs := map[string]bool{}
		for _, item := range declared {
			if taint, ok := item.(map[string]any); ok {
				pairs[fmt.Sprintf("%s/%s", taint["key"], taint["effect"])] = true
			}
		}
		for _, expected := range checks.ExpectedStartupTaints {
			pair := expected.Key + "/" + string(expected.Effect)
			if err != nil || !pairs[pair] {
				mismatches = append(mismatches, fmt.Sprintf("NodeClaim %q missing %s", claim.GetName(), pair))
			}
		}
	}
	if len(mismatches) > 0 {
		slices.Sort(mismatches)
		return fmt.Errorf("startup-taint declaration mismatch: %s", strings.Join(slices.Compact(mismatches), "; "))
	}
	return nil
}

func karpenterAdditionalNodeReason(node *corev1.Node, claims []unstructured.Unstructured, checks KarpenterProvisioningChecks) string {
	for _, key := range checks.AbsentTaintKeys {
		for _, taint := range node.Spec.Taints {
			if taint.Key == key {
				return fmt.Sprintf("still carries taint %s=%s:%s", taint.Key, taint.Value, taint.Effect)
			}
		}
	}
	if checks.RequireInitialized && node.Labels["karpenter.sh/initialized"] != "true" {
		for i := range claims {
			claim := &claims[i]
			if claim.GetDeletionTimestamp() != nil {
				continue
			}
			nodeName, _, _ := unstructured.NestedString(claim.Object, "status", "nodeName")
			providerID, _, _ := unstructured.NestedString(claim.Object, "status", "providerID")
			if nodeName != node.Name && (node.Spec.ProviderID == "" || providerID != node.Spec.ProviderID) {
				continue
			}
			if status, _ := unstructuredConditionStatus(claim, "Initialized"); status == "True" {
				return ""
			}
		}
		return "Ready but not initialized: karpenter.sh/initialized label is not true and no matching NodeClaim has Initialized=True"
	}
	return ""
}

// nodeClaimSummaries lists the NodeClaims of nodePoolName with their creation
// time and the status of the Launched, Registered, and Initialized conditions.
// Thus a reader can see when Karpenter decided to provision each NodeClaim. The
// Node object appears later, after the VM boots and registers.
func nodeClaimSummaries(ctx context.Context, dynamicClient dynamic.Interface, nodePoolName string) string {
	claims, err := dynamicClient.Resource(karpenterNodeClaimGVR).List(ctx, metav1.ListOptions{
		LabelSelector: framework.KarpenterNodePoolLabel + "=" + nodePoolName,
	})
	if err != nil {
		return fmt.Sprintf("failed to list NodeClaims for diagnostics: %v", err)
	}
	return summarizeNodeClaims(claims.Items)
}

func summarizeNodeClaims(claims []unstructured.Unstructured) string {
	parts := make([]string, 0, len(claims))
	for i := range claims {
		claim := &claims[i]
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
		conditions, _, _ := unstructured.NestedSlice(claim.Object, "status", "conditions")
		var lifecycle []string
		for _, condition := range conditions {
			encoded, _ := json.Marshal(condition)
			lifecycle = append(lifecycle, string(encoded))
		}
		slices.Sort(lifecycle)
		parts = append(parts, fmt.Sprintf("%s(uid=%s,created=%s,%s,conditions=%v,deletionTimestamp=%s,finalizers=%v)",
			claim.GetName(), claim.GetUID(), claim.GetCreationTimestamp().Format(time.RFC3339), strings.Join(conditionSummaries, ","), lifecycle,
			deletionTimestamp, claim.GetFinalizers()))
	}
	slices.Sort(parts)
	return fmt.Sprintf("%d NodeClaim(s): %s", len(claims), strings.Join(parts, ", "))
}

// karpenterNodeNotReadyReason returns a reason when the node is not yet ready,
// not schedulable, or not linked to its Azure VM instance.
//
// The check of Spec.ProviderID and of the cloud-provider uninitialized taint
// validates the Azure-only nodeproviderid controller of the HyperShift fork.
// Without that controller, Karpenter nodes never link and never become Ready in
// ARO-HCP. A test that stops at Ready=True could pass after a regression in that
// controller.
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
		node := &nodes[i]
		var ready corev1.NodeCondition
		for _, condition := range node.Status.Conditions {
			if condition.Type == corev1.NodeReady {
				ready = condition
			}
		}
		var taints []string
		for _, taint := range node.Spec.Taints {
			taints = append(taints, fmt.Sprintf("%s=%s:%s", taint.Key, taint.Value, taint.Effect))
		}
		slices.Sort(taints)
		labels, _ := json.Marshal(node.Labels)
		parts = append(parts, fmt.Sprintf("%s(uid=%s,Ready=%s,reason=%q,message=%q,schedulable=%v,initialized=%q,labels=%s,taints=%v,providerID=%q)",
			node.Name, node.UID, ready.Status, ready.Reason, ready.Message, !node.Spec.Unschedulable,
			node.Labels["karpenter.sh/initialized"], labels, taints, node.Spec.ProviderID))
	}
	slices.Sort(parts)
	return strings.Join(parts, ", ")
}

// VerifyKarpenterNodeProvisioned checks that a Node with
// framework.KarpenterNodePoolLabel=nodePoolName exists, is Ready, is
// schedulable, has Spec.ProviderID set, and no longer carries the
// cloud-provider uninitialized taint. The checks argument adds optional gates.
func VerifyKarpenterNodeProvisioned(nodePoolName string, timeout time.Duration, checks KarpenterProvisioningChecks) HostedClusterVerifier {
	checks.ExpectedStartupTaints = slices.Clone(checks.ExpectedStartupTaints)
	checks.AbsentTaintKeys = slices.Clone(checks.AbsentTaintKeys)
	return verifyKarpenterNodeProvisioned{nodePoolName: nodePoolName, timeout: timeout, checks: checks}
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
			return fmt.Sprintf("%s; %s; %s", nodeTerminationSummaries(nodes.Items), nodeClaimSummaries(ctx, dynamicClient, v.nodePoolName), nodeClassDiagnostics(ctx, dynamicClient, v.nodePoolName))
		},
		func(ctx context.Context) error {
			nodes, err := kubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{LabelSelector: selector})
			if err != nil {
				return fmt.Errorf("failed to list nodes: %w", err)
			}
			// Include these fields on every failure, not only in the diagnose
			// callback. The deletionTimestamp and the finalizers show which case
			// applies:
			//   - blocked drain: the node has a deletionTimestamp, but the pods do
			//     not evict
			//   - stuck finalizer: the deletionTimestamp is set, the finalizers are
			//     not empty, and the node looks abandoned
			//   - no termination: there is no deletionTimestamp, so Karpenter or
			//     Azure never started the termination
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
			return verifyNodeClassesDeleted(ctx, dynamicClient, v.nodePoolName)
		})
}

// nodeTerminationSummaries lists the nodes with their deletionTimestamp,
// finalizers, and taints. These fields show why a node does not disappear
// during teardown.
//
// No deletionTimestamp means Karpenter or Azure never started the termination. A
// deletionTimestamp with finalizers means a stuck drain or a stuck finalizer
// controller blocks the removal.
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

// VerifyKarpenterDeprovisioned checks that no Node and no NodeClaim with
// framework.KarpenterNodePoolLabel=nodePoolName remains. Use it during
// teardown. It also requires a NotFound response for both NodeClasses with the
// same name.
func VerifyKarpenterDeprovisioned(nodePoolName string, timeout time.Duration) HostedClusterVerifier {
	return verifyKarpenterDeprovisioned{nodePoolName: nodePoolName, timeout: timeout}
}

// unstructuredConditionStatus reads status.conditions[type=conditionType].status
// from an unstructured object. The object must use the metav1.Condition shape.
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
