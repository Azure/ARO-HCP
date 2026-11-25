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

package apply_desire

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/kubeapplierapi"
)

const (
	finalizeTestHCNS      = "ocm-production-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	finalizeTestNamespace = finalizeTestHCNS
	finalizeTestUID       = "11111111-1111-1111-1111-111111111111"
	finalizeTestHCName    = "hcp1"
	finalizeTestDwell     = time.Hour
)

func finalizeListKinds() map[schema.GroupVersionResource]string {
	return map[schema.GroupVersionResource]string{
		namespacesGVR:          "NamespaceList",
		hostedClustersGVR:      "HostedClusterList",
		hostedControlPlanesGVR: "HostedControlPlaneList",
	}
}

// terminatingNamespace builds a namespace in the state this operation exists
// for: Terminating, past the dwell, holding only the built-in finalizer.
func terminatingNamespace(mutate func(*unstructured.Unstructured)) *unstructured.Unstructured {
	return terminatingNamespaceNamed(finalizeTestNamespace, finalizeTestUID, mutate)
}

func terminatingNamespaceNamed(name, uid string, mutate func(*unstructured.Unstructured)) *unstructured.Unstructured {
	deletedAt := metav1.NewTime(time.Now().Add(-2 * finalizeTestDwell))
	ns := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Namespace",
		"metadata": map[string]any{
			"name":              name,
			"uid":               uid,
			"deletionTimestamp": deletedAt.UTC().Format(time.RFC3339),
		},
		"spec": map[string]any{"finalizers": []any{"kubernetes"}},
		"status": map[string]any{
			"phase": "Terminating",
			"conditions": []any{
				map[string]any{"type": "NamespaceDeletionDiscoveryFailure", "status": "False"},
				map[string]any{"type": "NamespaceDeletionGroupVersionParsingFailure", "status": "False"},
				map[string]any{"type": "NamespaceDeletionContentFailure", "status": "False"},
				map[string]any{"type": "NamespaceContentRemaining", "status": "False"},
				map[string]any{"type": "NamespaceFinalizersRemaining", "status": "False"},
			},
		},
	}}
	if mutate != nil {
		mutate(ns)
	}
	return ns
}

func hostedCluster(deleting bool) *unstructured.Unstructured {
	metadata := map[string]any{
		"name":      finalizeTestHCName,
		"namespace": finalizeTestHCNS,
		"uid":       "22222222-2222-2222-2222-222222222222",
	}
	if deleting {
		now := metav1.NewTime(time.Now().Add(-time.Hour))
		metadata["deletionTimestamp"] = now.UTC().Format(time.RFC3339)
		metadata["finalizers"] = []any{hypershiftFinalizer}
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "hypershift.openshift.io/v1beta1",
		"kind":       "HostedCluster",
		"metadata":   metadata,
	}}
}

func hostedControlPlane() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "hypershift.openshift.io/v1beta1",
		"kind":       "HostedControlPlane",
		"metadata": map[string]any{
			"name":      finalizeTestHCName,
			"namespace": finalizeTestHCNS + "-" + finalizeTestHCName,
		},
	}}
}

func finalizeDesire(mutate func(*kubeapplierapi.FinalizeNamespaceConfig)) *kubeapplierapi.ApplyDesire {
	cfg := &kubeapplierapi.FinalizeNamespaceConfig{
		AuthorizationExpiresAt: metav1.NewTime(time.Now().Add(time.Minute)),
		UID:                    finalizeTestUID,
		MinimumDwell:           metav1.Duration{Duration: finalizeTestDwell},
		HostedClusterNamespace: finalizeTestHCNS,
		HostedClusterName:      finalizeTestHCName,
	}
	if mutate != nil {
		mutate(cfg)
	}
	return &kubeapplierapi.ApplyDesire{
		Spec: kubeapplierapi.ApplyDesireSpec{
			Type: kubeapplierapi.ApplyDesireTypeFinalizeNamespace,
			TargetItem: kubeapplierapi.ResourceReference{
				Group: "", Version: "v1", Resource: "namespaces", Name: finalizeTestNamespace,
			},
			FinalizeNamespace: cfg,
		},
	}
}

func controlPlaneFinalizeDesire(mutate func(*kubeapplierapi.FinalizeNamespaceConfig)) *kubeapplierapi.ApplyDesire {
	desire := finalizeDesire(mutate)
	desire.Spec.TargetItem.Name = finalizeTestHCNS + "-" + finalizeTestHCName
	desire.Spec.FinalizeNamespace.UID = "33333333-3333-3333-3333-333333333333"
	return desire
}

func runFinalize(t *testing.T, desire *kubeapplierapi.ApplyDesire, objects ...runtime.Object) *kubeapplierapi.ApplyDesire {
	t.Helper()
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), finalizeListKinds(), objects...)
	controller := &ApplyDesireController{dyn: dyn}

	evaluation := controller.evaluateFinalizeNamespace(context.Background(), desire)
	require.NotNil(t, evaluation.mutate, "every evaluation must record an outcome")
	evaluation.mutate(desire)
	return desire
}

func requireFinalizeCondition(t *testing.T, desire *kubeapplierapi.ApplyDesire, wantStatus metav1.ConditionStatus, wantReason string) {
	t.Helper()
	condition := apimeta.FindStatusCondition(desire.Status.Conditions, kubeapplierapi.ConditionTypeNamespaceFinalized)
	require.NotNil(t, condition, "the NamespaceFinalized condition must always be set")
	require.Equal(t, wantStatus, condition.Status, "unexpected condition status: %s", condition.Message)
	require.Equal(t, wantReason, condition.Reason, "unexpected condition reason: %s", condition.Message)
}

func requireNamespaceSurvives(t *testing.T, desire *kubeapplierapi.ApplyDesire) {
	t.Helper()
	requireFinalizeCondition(t, desire, metav1.ConditionFalse, kubeapplierapi.ConditionReasonFinalizePreconditionFailed)
}

func TestFinalizeHostedClusterNamespaceRequiresHostedClusterAbsent(t *testing.T) {
	desire := runFinalize(t, finalizeDesire(nil), terminatingNamespace(nil), hostedCluster(false))
	requireNamespaceSurvives(t, desire)
	condition := apimeta.FindStatusCondition(desire.Status.Conditions, kubeapplierapi.ConditionTypeNamespaceFinalized)
	require.Contains(t, condition.Message, "HostedCluster")

	desire = runFinalize(t, finalizeDesire(nil), terminatingNamespace(nil), hostedCluster(true))
	requireNamespaceSurvives(t, desire)
	condition = apimeta.FindStatusCondition(desire.Status.Conditions, kubeapplierapi.ConditionTypeNamespaceFinalized)
	require.Contains(t, condition.Message, "still exists")
}

func TestFinalizeHostedClusterNamespaceRequiresControlPlaneNamespaceAbsent(t *testing.T) {
	cpName := finalizeTestHCNS + "-" + finalizeTestHCName
	desire := runFinalize(t, finalizeDesire(nil), terminatingNamespace(nil),
		terminatingNamespaceNamed(cpName, "33333333-3333-3333-3333-333333333333", nil))
	requireNamespaceSurvives(t, desire)
	condition := apimeta.FindStatusCondition(desire.Status.Conditions, kubeapplierapi.ConditionTypeNamespaceFinalized)
	require.Contains(t, condition.Message, "control-plane namespace")
}

func TestFinalizeControlPlaneNamespaceAllowsHostedClusterButRequiresHostedControlPlaneAbsent(t *testing.T) {
	cpName := finalizeTestHCNS + "-" + finalizeTestHCName
	desire := runFinalize(t, controlPlaneFinalizeDesire(nil),
		terminatingNamespaceNamed(cpName, "33333333-3333-3333-3333-333333333333", nil),
		hostedCluster(false))
	condition := apimeta.FindStatusCondition(desire.Status.Conditions, kubeapplierapi.ConditionTypeNamespaceFinalized)
	require.NotNil(t, condition)
	require.NotContains(t, condition.Message, "HostedCluster")

	controller := &ApplyDesireController{dyn: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), finalizeListKinds(),
		terminatingNamespaceNamed(cpName, "33333333-3333-3333-3333-333333333333", nil), hostedCluster(false))}
	controller.evaluateFinalizeNamespace(context.Background(), controlPlaneFinalizeDesire(nil))
	updated, err := controller.dyn.Resource(namespacesGVR).Get(context.Background(), cpName, metav1.GetOptions{})
	require.NoError(t, err)
	finalizers, _, err := unstructured.NestedStringSlice(updated.Object, "spec", "finalizers")
	require.NoError(t, err)
	require.Empty(t, finalizers, "control-plane finalization may proceed while the HostedCluster exists")

	desire = runFinalize(t, controlPlaneFinalizeDesire(nil),
		terminatingNamespaceNamed(cpName, "33333333-3333-3333-3333-333333333333", nil),
		hostedControlPlane())
	requireNamespaceSurvives(t, desire)
	condition = apimeta.FindStatusCondition(desire.Status.Conditions, kubeapplierapi.ConditionTypeNamespaceFinalized)
	require.Contains(t, condition.Message, "HostedControlPlane")
}

func TestFinalizeNamespaceRejectsCrossRoleTarget(t *testing.T) {
	desire := finalizeDesire(nil)
	desire.Spec.TargetItem.Name = "ocm-production-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	runFinalize(t, desire, terminatingNamespace(nil))
	requireFinalizeCondition(t, desire, metav1.ConditionFalse, kubeapplierapi.ConditionReasonPreCheckFailed)
}

func TestFinalizeNamespaceRequiresClearInventory(t *testing.T) {
	desire := runFinalize(t, finalizeDesire(nil), terminatingNamespace(func(ns *unstructured.Unstructured) {
		conditions, _, _ := unstructured.NestedSlice(ns.Object, "status", "conditions")
		conditions[3].(map[string]any)["status"] = "True"
		conditions[3].(map[string]any)["message"] = "pods still exist"
		_ = unstructured.SetNestedSlice(ns.Object, conditions, "status", "conditions")
	}))
	requireNamespaceSurvives(t, desire)
	condition := apimeta.FindStatusCondition(desire.Status.Conditions, kubeapplierapi.ConditionTypeNamespaceFinalized)
	require.Contains(t, condition.Message, "NamespaceContentRemaining")
	require.Contains(t, condition.Message, "pods still exist")
}

func TestFinalizeNamespaceRejectsExpiredAuthorizationAndUnsafeIdentity(t *testing.T) {
	desire := runFinalize(t, finalizeDesire(func(cfg *kubeapplierapi.FinalizeNamespaceConfig) {
		cfg.AuthorizationExpiresAt = metav1.NewTime(time.Now().Add(-time.Minute))
	}), terminatingNamespace(nil))
	requireNamespaceSurvives(t, desire)
	condition := apimeta.FindStatusCondition(desire.Status.Conditions, kubeapplierapi.ConditionTypeNamespaceFinalized)
	require.Contains(t, condition.Message, "authorization has expired")

	desire = runFinalize(t, finalizeDesire(func(cfg *kubeapplierapi.FinalizeNamespaceConfig) { cfg.UID = "replacement" }), terminatingNamespace(nil))
	requireNamespaceSurvives(t, desire)

	desire = runFinalize(t, finalizeDesire(func(cfg *kubeapplierapi.FinalizeNamespaceConfig) { cfg.HostedClusterName = "" }), terminatingNamespace(nil))
	requireFinalizeCondition(t, desire, metav1.ConditionFalse, kubeapplierapi.ConditionReasonPreCheckFailed)
}

func TestFinalizeNamespaceRejectsNonNamespaceTargetAndRequiresUID(t *testing.T) {
	desire := finalizeDesire(nil)
	desire.Spec.TargetItem.Resource = "secrets"
	desire.Spec.TargetItem.Namespace = "somewhere"
	runFinalize(t, desire, terminatingNamespace(nil))
	requireFinalizeCondition(t, desire, metav1.ConditionFalse, kubeapplierapi.ConditionReasonPreCheckFailed)

	desire = runFinalize(t, finalizeDesire(func(cfg *kubeapplierapi.FinalizeNamespaceConfig) { cfg.UID = "" }), terminatingNamespace(nil))
	requireFinalizeCondition(t, desire, metav1.ConditionFalse, kubeapplierapi.ConditionReasonPreCheckFailed)
}

func TestFinalizeNamespaceAbsentIsSuccess(t *testing.T) {
	desire := runFinalize(t, finalizeDesire(nil))
	requireFinalizeCondition(t, desire, metav1.ConditionTrue, kubeapplierapi.ConditionReasonNoErrors)
}
