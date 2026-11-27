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

	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/kubeapplierapi"
)

func unblockListKinds() map[schema.GroupVersionResource]string {
	kinds := finalizeListKinds()
	kinds[nodePoolsGVR] = "NodePoolList"
	kinds[deploymentsGVR] = "DeploymentList"
	kinds[clustersGVR] = "ClusterList"
	kinds[hostedControlPlanesGVR] = "HostedControlPlaneList"
	for _, target := range capiFinalizerTargets {
		switch target.gvr.Resource {
		case "machines":
			kinds[target.gvr] = "MachineList"
		case "machinesets":
			kinds[target.gvr] = "MachineSetList"
		case "machinedeployments":
			kinds[target.gvr] = "MachineDeploymentList"
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

func TestUnblockHostedClusterDrainsOneLayerAtATimeAcrossNamespaces(t *testing.T) {
	ctx := context.Background()
	cpNamespaceName := finalizeTestHCNS + "-" + finalizeTestHCName
	cpNamespace := deletingObject("v1", "Namespace", cpNamespaceName, "", "33333333-3333-3333-3333-333333333333", "kubernetes")
	clusterID := clusterIDFromNamespace(finalizeTestHCNS)
	machine := deletingObject("cluster.x-k8s.io/v1beta1", "Machine", finalizeTestHCName+"-np1-machine", cpNamespaceName,
		"44444444-4444-4444-4444-444444444444", "machine.cluster.x-k8s.io")
	deployment := deletingObject("apps/v1", "Deployment", "capi-provider", cpNamespaceName,
		"66666666-6666-6666-6666-666666666666", hypershiftComponentFinalizer)
	cluster := deletingObject("cluster.x-k8s.io/v1beta1", "Cluster", clusterID, cpNamespaceName,
		"77777777-7777-7777-7777-777777777777", clusterFinalizer)
	hcp := deletingObject("hypershift.openshift.io/v1beta1", "HostedControlPlane", finalizeTestHCName, cpNamespaceName,
		"88888888-8888-8888-8888-888888888888", hypershiftFinalizer)
	_ = unstructured.SetNestedField(hcp.Object, time.Now().Add(-3*time.Hour).UTC().Format(time.RFC3339), "metadata", "creationTimestamp")
	_ = unstructured.SetNestedSlice(hcp.Object, []any{map[string]any{
		"apiVersion": "cluster.x-k8s.io/v1beta1", "kind": "Cluster", "name": clusterID, "uid": "77777777-7777-7777-7777-777777777777",
	}}, "metadata", "ownerReferences")
	nodePool := deletingObject("hypershift.openshift.io/v1beta1", "NodePool", finalizeTestHCName+"-np1", finalizeTestHCNS,
		"55555555-5555-5555-5555-555555555555", hypershiftFinalizer)
	_ = unstructured.SetNestedField(nodePool.Object, finalizeTestHCName, "spec", "clusterName")
	hc := hostedCluster(true)
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), unblockListKinds(),
		terminatingNamespace(nil), cpNamespace, machine, deployment, cluster, hcp, nodePool, hc)
	controller := &ApplyDesireController{dyn: dyn}
	desire := unblockDesire()
	var condition *metav1.Condition

	for _, step := range []struct {
		resource schema.GroupVersionResource
		ns, name string
	}{
		{capiFinalizerTargets[0].gvr, cpNamespaceName, machine.GetName()},
		{deploymentsGVR, cpNamespaceName, deployment.GetName()},
		{clustersGVR, cpNamespaceName, cluster.GetName()},
		{hostedControlPlanesGVR, cpNamespaceName, hcp.GetName()},
	} {
		evaluation := controller.evaluateUnblockHostedCluster(ctx, desire)
		evaluation.mutate(desire)
		condition = apiMeta.FindStatusCondition(desire.Status.Conditions, kubeapplierapi.ConditionTypeHostedClusterUnblocked)
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
	condition = apiMeta.FindStatusCondition(desire.Status.Conditions, kubeapplierapi.ConditionTypeHostedClusterUnblocked)
	require.Equal(t, kubeapplierapi.ConditionReasonWaitingForControlPlaneNamespace, condition.Reason)
	require.Contains(t, condition.Message, cpNamespaceName)
	nodePoolBeforeCPRemoval, err := dyn.Resource(nodePoolsGVR).Namespace(finalizeTestHCNS).Get(ctx, nodePool.GetName(), metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, []string{hypershiftFinalizer}, nodePoolBeforeCPRemoval.GetFinalizers(), "NodePools must not be touched while the control-plane namespace exists")
	hcBeforeCPRemoval, err := dyn.Resource(hostedClustersGVR).Namespace(finalizeTestHCNS).Get(ctx, hc.GetName(), metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, []string{hypershiftFinalizer}, hcBeforeCPRemoval.GetFinalizers(), "the HostedCluster must not be touched while the control-plane namespace exists")
	_, err = dyn.Resource(namespacesGVR).Get(ctx, cpNamespaceName, metav1.GetOptions{})
	require.NoError(t, err, "the controller must wait for the control-plane namespace to disappear")
	require.NoError(t, dyn.Resource(namespacesGVR).Delete(ctx, cpNamespaceName, metav1.DeleteOptions{}))

	for _, step := range []struct {
		resource schema.GroupVersionResource
		ns, name string
	}{
		{nodePoolsGVR, finalizeTestHCNS, nodePool.GetName()},
		{hostedClustersGVR, finalizeTestHCNS, hc.GetName()},
	} {
		evaluation = controller.evaluateUnblockHostedCluster(ctx, desire)
		evaluation.mutate(desire)
		condition = apiMeta.FindStatusCondition(desire.Status.Conditions, kubeapplierapi.ConditionTypeHostedClusterUnblocked)
		require.Equal(t, metav1.ConditionFalse, condition.Status)
		updated, err := dyn.Resource(step.resource).Namespace(step.ns).Get(ctx, step.name, metav1.GetOptions{})
		require.NoError(t, err)
		require.Empty(t, updated.GetFinalizers())
		evaluation = controller.evaluateUnblockHostedCluster(ctx, desire)
		evaluation.mutate(desire)
		require.NoError(t, dyn.Resource(step.resource).Namespace(step.ns).Delete(ctx, step.name, metav1.DeleteOptions{}))
	}
	evaluation = controller.evaluateUnblockHostedCluster(ctx, desire)
	evaluation.mutate(desire)
	condition = apiMeta.FindStatusCondition(desire.Status.Conditions, kubeapplierapi.ConditionTypeHostedClusterUnblocked)
	require.Equal(t, metav1.ConditionTrue, condition.Status)
	legacy := apiMeta.FindStatusCondition(desire.Status.Conditions, kubeapplierapi.ConditionTypeSuccessful)
	require.NotNil(t, legacy)
	require.Equal(t, condition.Status, legacy.Status)
	require.Equal(t, condition.Reason, legacy.Reason)
}

func TestUnblockHostedClusterAcceptsFixedComponentDeploymentsAndRejectsUnrelated(t *testing.T) {
	ctx := context.Background()
	cpNamespaceName := finalizeTestHCNS + "-" + finalizeTestHCName
	cpNamespace := deletingObject("v1", "Namespace", cpNamespaceName, "", "33333333-3333-3333-3333-333333333333", "kubernetes")
	for _, name := range []string{"capi-provider", "cluster-api"} {
		t.Run(name, func(t *testing.T) {
			deployment := deletingObject("apps/v1", "Deployment", name, cpNamespaceName, "dep-uid", hypershiftComponentFinalizer)
			dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), unblockListKinds(),
				terminatingNamespace(nil), cpNamespace.DeepCopy(), deployment, hostedCluster(true))
			evaluation := (&ApplyDesireController{dyn: dyn}).evaluateUnblockHostedCluster(ctx, unblockDesire())
			require.NotNil(t, evaluation.mutate)
			updated, err := dyn.Resource(deploymentsGVR).Namespace(cpNamespaceName).Get(ctx, name, metav1.GetOptions{})
			require.NoError(t, err)
			require.Empty(t, updated.GetFinalizers())
		})
	}

	unrelated := deletingObject("apps/v1", "Deployment", "unrelated", cpNamespaceName, "dep-uid", hypershiftComponentFinalizer)
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), unblockListKinds(),
		terminatingNamespace(nil), cpNamespace.DeepCopy(), unrelated, hostedCluster(true))
	desire := unblockDesire()
	evaluation := (&ApplyDesireController{dyn: dyn}).evaluateUnblockHostedCluster(ctx, desire)
	evaluation.mutate(desire)
	condition := apiMeta.FindStatusCondition(desire.Status.Conditions, kubeapplierapi.ConditionTypeHostedClusterUnblocked)
	require.Equal(t, kubeapplierapi.ConditionReasonRecreatedDuringCleanup, condition.Reason)
	require.Contains(t, condition.Message, "not owned")
	updated, err := dyn.Resource(deploymentsGVR).Namespace(cpNamespaceName).Get(ctx, unrelated.GetName(), metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, []string{hypershiftComponentFinalizer}, updated.GetFinalizers())
}

func TestUnblockHostedClusterStopsWhenControlPlaneNamespaceUIDChanges(t *testing.T) {
	cpNamespaceName := finalizeTestHCNS + "-" + finalizeTestHCName
	cpNamespace := deletingObject("v1", "Namespace", cpNamespaceName, "", "replacement-uid", "kubernetes")
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), unblockListKinds(),
		terminatingNamespace(nil), cpNamespace, hostedCluster(true))
	desire := unblockDesire()
	evaluation := (&ApplyDesireController{dyn: dyn}).evaluateUnblockHostedCluster(context.Background(), desire)
	evaluation.mutate(desire)
	condition := apiMeta.FindStatusCondition(desire.Status.Conditions, kubeapplierapi.ConditionTypeHostedClusterUnblocked)
	require.Equal(t, kubeapplierapi.ConditionReasonRecreatedDuringCleanup, condition.Reason)
	require.Contains(t, condition.Message, "UID changed")
}

func TestUnblockHostedClusterRejectsHostedControlPlaneOwnerMismatchAndRecreation(t *testing.T) {
	ctx := context.Background()
	cpNamespaceName := finalizeTestHCNS + "-" + finalizeTestHCName
	cpNamespace := deletingObject("v1", "Namespace", cpNamespaceName, "", "33333333-3333-3333-3333-333333333333", "kubernetes")
	clusterID := clusterIDFromNamespace(finalizeTestHCNS)
	for _, tc := range []struct {
		name   string
		mutate func(*unstructured.Unstructured)
	}{
		{name: "wrong owner", mutate: func(hcp *unstructured.Unstructured) {
			_ = unstructured.SetNestedSlice(hcp.Object, []any{map[string]any{"kind": "Cluster", "name": "other-cluster"}}, "metadata", "ownerReferences")
		}},
		{name: "recreated after request", mutate: func(hcp *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(hcp.Object, time.Now().UTC().Format(time.RFC3339), "metadata", "creationTimestamp")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hcp := deletingObject("hypershift.openshift.io/v1beta1", "HostedControlPlane", finalizeTestHCName, cpNamespaceName, "hcp-uid", hypershiftFinalizer)
			_ = unstructured.SetNestedField(hcp.Object, time.Now().Add(-3*time.Hour).UTC().Format(time.RFC3339), "metadata", "creationTimestamp")
			_ = unstructured.SetNestedSlice(hcp.Object, []any{map[string]any{"kind": "Cluster", "name": clusterID}}, "metadata", "ownerReferences")
			tc.mutate(hcp)
			dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), unblockListKinds(),
				terminatingNamespace(nil), cpNamespace.DeepCopy(), hcp, hostedCluster(true))
			desire := unblockDesire()
			evaluation := (&ApplyDesireController{dyn: dyn}).evaluateUnblockHostedCluster(ctx, desire)
			evaluation.mutate(desire)
			condition := apiMeta.FindStatusCondition(desire.Status.Conditions, kubeapplierapi.ConditionTypeHostedClusterUnblocked)
			require.Equal(t, kubeapplierapi.ConditionReasonRecreatedDuringCleanup, condition.Reason)
			hcpUpdated, err := dyn.Resource(hostedControlPlanesGVR).Namespace(cpNamespaceName).Get(ctx, hcp.GetName(), metav1.GetOptions{})
			require.NoError(t, err)
			require.Equal(t, []string{hypershiftFinalizer}, hcpUpdated.GetFinalizers())
		})
	}
}
