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

	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
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
		namespacesGVR:     "NamespaceList",
		hostedClustersGVR: "HostedClusterList",
	}
}

// terminatingNamespace builds a namespace in the state this operation exists
// for: Terminating, past the dwell, holding only the built-in finalizer.
func terminatingNamespace(mutate func(*unstructured.Unstructured)) *unstructured.Unstructured {
	deletedAt := metav1.NewTime(time.Now().Add(-2 * finalizeTestDwell))
	ns := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Namespace",
		"metadata": map[string]any{
			"name":              finalizeTestNamespace,
			"uid":               finalizeTestUID,
			"deletionTimestamp": deletedAt.UTC().Format(time.RFC3339),
		},
		"spec":   map[string]any{"finalizers": []any{"kubernetes"}},
		"status": map[string]any{"phase": "Terminating"},
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
		metadata["finalizers"] = []any{"hypershift.openshift.io/finalizer"}
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "hypershift.openshift.io/v1beta1",
		"kind":       "HostedCluster",
		"metadata":   metadata,
	}}
}

func finalizeDesire(mutate func(*kubeapplierapi.FinalizeNamespaceConfig)) *kubeapplierapi.ApplyDesire {
	cfg := &kubeapplierapi.FinalizeNamespaceConfig{
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

// TestFinalizeNamespaceRefusesLiveHostedCluster is the execution-time half of
// the most important refusal in this design. A HostedCluster that still exists
// must block finalization even when the backend observed it absent earlier.
func TestFinalizeNamespaceRefusesLiveHostedCluster(t *testing.T) {
	desire := runFinalize(t, finalizeDesire(nil), terminatingNamespace(nil), hostedCluster(false))

	requireNamespaceSurvives(t, desire)
	condition := apimeta.FindStatusCondition(desire.Status.Conditions, kubeapplierapi.ConditionTypeNamespaceFinalized)
	require.Contains(t, condition.Message, "still exists",
		"the refusal must name why a live HostedCluster blocks")
}

func TestFinalizeNamespaceRefusesDeletingHostedCluster(t *testing.T) {
	desire := runFinalize(t, finalizeDesire(nil), terminatingNamespace(nil), hostedCluster(true))
	requireNamespaceSurvives(t, desire)
	condition := apimeta.FindStatusCondition(desire.Status.Conditions, kubeapplierapi.ConditionTypeNamespaceFinalized)
	require.Contains(t, condition.Message, "still exists",
		"a deletionTimestamp must not bypass the HostedCluster finalizer")
}

// TestFinalizeNamespacePreconditions walks every gate that must hold before the
// finalizer is cleared. Each case leaves the namespace intact.
func TestFinalizeNamespacePreconditions(t *testing.T) {
	for _, tc := range []struct {
		name      string
		namespace *unstructured.Unstructured
		desire    *kubeapplierapi.ApplyDesire
	}{
		{
			name: "pinned UID no longer matches",
			namespace: terminatingNamespace(func(ns *unstructured.Unstructured) {
				_ = unstructured.SetNestedField(ns.Object, "99999999-9999-9999-9999-999999999999", "metadata", "uid")
			}),
			desire: finalizeDesire(nil),
		},
		{
			name: "namespace is not terminating",
			namespace: terminatingNamespace(func(ns *unstructured.Unstructured) {
				unstructured.RemoveNestedField(ns.Object, "metadata", "deletionTimestamp")
			}),
			desire: finalizeDesire(nil),
		},
		{
			name: "dwell has not elapsed",
			namespace: terminatingNamespace(func(ns *unstructured.Unstructured) {
				recent := metav1.NewTime(time.Now().Add(-time.Minute))
				_ = unstructured.SetNestedField(ns.Object, recent.UTC().Format(time.RFC3339), "metadata", "deletionTimestamp")
			}),
			desire: finalizeDesire(nil),
		},
		{
			name: "an extra finalizer is present",
			namespace: terminatingNamespace(func(ns *unstructured.Unstructured) {
				_ = unstructured.SetNestedStringSlice(ns.Object, []string{"kubernetes", "example.com/other"}, "spec", "finalizers")
			}),
			desire: finalizeDesire(nil),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			desire := runFinalize(t, tc.desire, tc.namespace, hostedCluster(true))

			requireNamespaceSurvives(t, desire)
		})
	}
}

// TestFinalizeNamespaceRejectsNonNamespaceTarget guards the blast radius: this
// controller removes a finalizer from exactly one kind of object.
func TestFinalizeNamespaceRejectsNonNamespaceTarget(t *testing.T) {
	desire := finalizeDesire(nil)
	desire.Spec.TargetItem.Resource = "secrets"
	desire.Spec.TargetItem.Namespace = "somewhere"

	runFinalize(t, desire, terminatingNamespace(nil))

	requireFinalizeCondition(t, desire, metav1.ConditionFalse, kubeapplierapi.ConditionReasonPreCheckFailed)
}

// TestFinalizeNamespaceRequiresPinnedUID rejects a desire that never bound an
// identity, rather than acting on whatever happens to hold the name.
func TestFinalizeNamespaceRequiresPinnedUID(t *testing.T) {
	desire := runFinalize(t,
		finalizeDesire(func(cfg *kubeapplierapi.FinalizeNamespaceConfig) { cfg.UID = "" }),
		terminatingNamespace(nil), hostedCluster(true))

	requireFinalizeCondition(t, desire, metav1.ConditionFalse, kubeapplierapi.ConditionReasonPreCheckFailed)
}

func TestFinalizeNamespaceRequiresHostedClusterIdentity(t *testing.T) {
	desire := runFinalize(t,
		finalizeDesire(func(cfg *kubeapplierapi.FinalizeNamespaceConfig) { cfg.HostedClusterName = "" }),
		terminatingNamespace(nil))
	requireFinalizeCondition(t, desire, metav1.ConditionFalse, kubeapplierapi.ConditionReasonPreCheckFailed)
}

// TestFinalizeNamespaceAbsentIsSuccess covers the steady state after the
// finalize lands: the namespace is gone, which must not be reported as an error
// on every subsequent pass.
func TestFinalizeNamespaceAbsentIsSuccess(t *testing.T) {
	desire := runFinalize(t, finalizeDesire(nil))

	requireFinalizeCondition(t, desire, metav1.ConditionTrue, kubeapplierapi.ConditionReasonNoErrors)
}

// TestFinalizeNamespaceClearsFinalizerWhenEveryGateHolds is the positive path.
func TestFinalizeNamespaceClearsFinalizerWhenEveryGateHolds(t *testing.T) {
	desire := finalizeDesire(nil)
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(), finalizeListKinds(), terminatingNamespace(nil))
	controller := &ApplyDesireController{dyn: dyn}

	evaluation := controller.evaluateFinalizeNamespace(context.Background(), desire)
	evaluation.mutate(desire)

	// The fake client does not run the namespace controller, so the object
	// survives the finalize call; what this asserts is that the mutation was
	// issued and the finalizers were cleared, not that the object vanished.
	updated, err := dyn.Resource(namespacesGVR).Get(context.Background(), finalizeTestNamespace, metav1.GetOptions{})
	require.NoError(t, err)
	finalizers, _, err := unstructured.NestedStringSlice(updated.Object, "spec", "finalizers")
	require.NoError(t, err)
	require.Empty(t, finalizers, "the kubernetes finalizer must have been cleared once every gate held")
}
