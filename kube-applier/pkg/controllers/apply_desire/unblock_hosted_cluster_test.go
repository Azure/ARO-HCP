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

	apiMeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
)

func unblockListKinds() map[schema.GroupVersionResource]string {
	kinds := finalizeListKinds()
	kinds[nodePoolsGVR] = "NodePoolList"
	for _, target := range capiFinalizerTargets {
		switch target.gvr.Resource {
		case "machines":
			kinds[target.gvr] = "MachineList"
		case "machinesets":
			kinds[target.gvr] = "MachineSetList"
		case "machinedeployments":
			kinds[target.gvr] = "MachineDeploymentList"
		case "clusters":
			kinds[target.gvr] = "ClusterList"
		}
	}
	return kinds
}

func deletingObject(apiVersion, kind, name, namespace, uid, finalizer string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": apiVersion, "kind": kind,
		"metadata": map[string]any{
			"name": name, "namespace": namespace, "uid": uid,
			"deletionTimestamp": time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339),
			"finalizers":        []any{finalizer},
		},
	}}
}

func unblockDesire() *kubeapplierapi.ApplyDesire {
	return &kubeapplierapi.ApplyDesire{Spec: kubeapplierapi.ApplyDesireSpec{
		Type:       kubeapplierapi.ApplyDesireTypeUnblockHostedCluster,
		TargetItem: kubeapplierapi.ResourceReference{Version: "v1", Resource: "namespaces", Name: finalizeTestHCNS},
		UnblockHostedCluster: &kubeapplierapi.UnblockHostedClusterConfig{
			NamespaceUID: finalizeTestUID, HostedClusterName: finalizeTestHCName,
			HostedClusterUID:         "22222222-2222-2222-2222-222222222222",
			ControlPlaneNamespaceUID: "33333333-3333-3333-3333-333333333333",
			MinimumDwell:             metav1.Duration{Duration: time.Hour},
			AuthorizationExpiresAt:   metav1.NewTime(time.Now().Add(time.Minute)),
		},
	}}
}

func TestUnblockHostedClusterClearsOnlyExpectedDeletingObjects(t *testing.T) {
	ctx := context.Background()
	cpNamespaceName := finalizeTestHCNS + "-" + finalizeTestHCName
	cpNamespace := deletingObject("v1", "Namespace", cpNamespaceName, "", "33333333-3333-3333-3333-333333333333", "kubernetes")
	machine := deletingObject("cluster.x-k8s.io/v1beta1", "Machine", finalizeTestHCName+"-np1-machine", cpNamespaceName,
		"44444444-4444-4444-4444-444444444444", "machine.cluster.x-k8s.io")
	np := deletingObject("hypershift.openshift.io/v1beta1", "NodePool", finalizeTestHCName+"-np1", finalizeTestHCNS,
		"55555555-5555-5555-5555-555555555555", "hypershift.openshift.io/finalizer")
	_ = unstructured.SetNestedField(np.Object, finalizeTestHCName, "spec", "clusterName")
	hc := hostedCluster(true)
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), unblockListKinds(),
		terminatingNamespace(nil), cpNamespace, machine, np, hc)
	controller := &ApplyDesireController{dyn: dyn}
	desire := unblockDesire()

	for _, step := range []struct {
		resource schema.GroupVersionResource
		ns, name string
	}{
		{capiFinalizerTargets[0].gvr, cpNamespaceName, machine.GetName()},
		{nodePoolsGVR, finalizeTestHCNS, np.GetName()},
		{hostedClustersGVR, finalizeTestHCNS, hc.GetName()},
	} {
		evaluation := controller.evaluateUnblockHostedCluster(ctx, desire)
		evaluation.mutate(desire)
		condition := apiMeta.FindStatusCondition(desire.Status.Conditions, kubeapplierapi.ConditionTypeHostedClusterUnblocked)
		require.Equal(t, metav1.ConditionFalse, condition.Status)
		updated, err := dyn.Resource(step.resource).Namespace(step.ns).Get(ctx, step.name, metav1.GetOptions{})
		require.NoError(t, err)
		require.Empty(t, updated.GetFinalizers(), "only the expected finalizer should have been cleared")
		waiting := controller.evaluateUnblockHostedCluster(ctx, desire)
		waiting.mutate(desire)
		condition = apiMeta.FindStatusCondition(desire.Status.Conditions, kubeapplierapi.ConditionTypeHostedClusterUnblocked)
		require.Contains(t, condition.Message, "waiting", "a parent finalizer must wait for the lower object to disappear")
		require.NoError(t, dyn.Resource(step.resource).Namespace(step.ns).Delete(ctx, step.name, metav1.DeleteOptions{}))
	}
	evaluation := controller.evaluateUnblockHostedCluster(ctx, desire)
	evaluation.mutate(desire)
	condition := apiMeta.FindStatusCondition(desire.Status.Conditions, kubeapplierapi.ConditionTypeHostedClusterUnblocked)
	require.Equal(t, metav1.ConditionTrue, condition.Status)
	legacy := apiMeta.FindStatusCondition(desire.Status.Conditions, kubeapplierapi.ConditionTypeSuccessful)
	require.NotNil(t, legacy)
	require.Equal(t, condition.Status, legacy.Status)
	require.Equal(t, condition.Reason, legacy.Reason)
	_, err := dyn.Resource(namespacesGVR).Get(ctx, cpNamespaceName, metav1.GetOptions{})
	require.NoError(t, err, "the control-plane namespace must not be modified")
}

func TestUnblockHostedClusterStopsOnUnexpectedHostedCluster(t *testing.T) {
	hc := hostedCluster(true)
	hc.SetUID("replacement")
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), unblockListKinds(), terminatingNamespace(nil), hc)
	desire := unblockDesire()
	evaluation := (&ApplyDesireController{dyn: dyn}).evaluateUnblockHostedCluster(context.Background(), desire)
	evaluation.mutate(desire)
	condition := apiMeta.FindStatusCondition(desire.Status.Conditions, kubeapplierapi.ConditionTypeHostedClusterUnblocked)
	require.Equal(t, "PreconditionFailed", condition.Reason)
	legacy := apiMeta.FindStatusCondition(desire.Status.Conditions, kubeapplierapi.ConditionTypeSuccessful)
	require.NotNil(t, legacy)
	require.Equal(t, condition.Status, legacy.Status)
	require.Equal(t, condition.Reason, legacy.Reason)
	require.Contains(t, condition.Message, "identity")
	require.Equal(t, []string{"hypershift.openshift.io/finalizer"}, hc.GetFinalizers())
}

func TestUnblockHostedClusterStopsOnManifestWork(t *testing.T) {
	hc := hostedCluster(true)
	work := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "work.open-cluster-management.io/v1", "kind": "ManifestWork",
		"metadata": map[string]any{
			"name":      clusterIDFromNamespace(finalizeTestHCNS) + "-00-hcp-namespaces",
			"namespace": "local-cluster",
		},
	}}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), unblockListKinds(),
		terminatingNamespace(nil), hc, work)
	desire := unblockDesire()
	evaluation := (&ApplyDesireController{dyn: dyn}).evaluateUnblockHostedCluster(context.Background(), desire)
	evaluation.mutate(desire)
	condition := apiMeta.FindStatusCondition(desire.Status.Conditions, kubeapplierapi.ConditionTypeHostedClusterUnblocked)
	require.Equal(t, "PreconditionFailed", condition.Reason)
	require.Contains(t, condition.Message, "ManifestWork")
	updated, err := dyn.Resource(hostedClustersGVR).Namespace(finalizeTestHCNS).Get(context.Background(), hc.GetName(), metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, hc.GetFinalizers(), updated.GetFinalizers(), "a surviving ManifestWork must prevent finalizer removal")
}
