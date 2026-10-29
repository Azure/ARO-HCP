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

package k8sresources

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	ocmv1 "open-cluster-management.io/api/work/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/kubeapplierapihelpers"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
)

func newNamespaceFixture(t *testing.T) (*certificateFixture, *namespaceSyncer) {
	t.Helper()
	fixture := newCertificateFixture(t)
	fixture.serviceProvider.Status.HostedClusterNamespace = "ocm-test-env-abc123"
	fixture.serviceProvider.Status.ControlPlaneNamespace = "ocm-test-env-abc123-cluster"
	return fixture, &namespaceSyncer{
		envIdentifier:                "test-env",
		clusterLister:                fixture.syncer.clusterLister,
		serviceProviderClusterLister: fixture.syncer.serviceProviderClusterLister,
		kubeApplierDBClients:         fixture.syncer.kubeApplierDBClients,
		applyDesireLister:            fixture.syncer.applyDesireLister,
		readDesireLister:             fixture.syncer.readDesireLister,
	}
}

func TestNamespaceContentsAdoptionAndIdempotence(t *testing.T) {
	fixture, syncer := newNamespaceFixture(t)
	ctx := t.Context()
	require.NoError(t, syncer.SyncOnce(ctx, fixture.key))
	applies, reads := fixture.applies(t), fixture.reads(t)
	require.Len(t, applies, 2)
	require.Len(t, reads, 2)
	applyCRUD, err := fixture.client.ApplyDesiresForCluster(fixture.key.SubscriptionID, fixture.key.ResourceGroupName, fixture.key.HCPClusterName)
	require.NoError(t, err)
	readCRUD, err := fixture.client.ReadDesiresForCluster(fixture.key.SubscriptionID, fixture.key.ResourceGroupName, fixture.key.HCPClusterName)
	require.NoError(t, err)
	for _, desire := range applies {
		require.Equal(t, NamespaceControllerName, desire.Tags[kubeapplierapi.TagControllerName])
		require.Equal(t, kubeapplierapi.ApplyDesireTypeServerSideApply, desire.Spec.Type)
		require.Equal(t, ocmv1.DefaultFieldManager, *desire.Spec.ServerSideApply.FieldManager)
		require.Equal(t, fixture.managementCluster.ResourceID, desire.Spec.ManagementCluster)
		require.Equal(t, strings.ToLower(fixture.managementCluster.ResourceID.String()), desire.PartitionKey)
		require.Equal(t, "namespaces", desire.Spec.TargetItem.Resource)
		require.Empty(t, desire.Spec.TargetItem.Namespace)
		var content map[string]any
		require.NoError(t, json.Unmarshal(desire.Spec.ServerSideApply.KubeContent.Raw, &content))
		require.Equal(t, "v1", content["apiVersion"])
		require.Equal(t, "Namespace", content["kind"])
		metadata := content["metadata"].(map[string]any)
		require.Equal(t, desire.Spec.TargetItem.Name, metadata["name"])
		require.Equal(t, map[string]any{controllerutil.HcpClusterAzureResourceIdAnnotation: fixture.cluster.ID.String()}, metadata["annotations"])
		if strings.EqualFold(desire.ResourceID.Name, HostedClusterNamespaceDesireName) {
			require.Equal(t, "ocm-test-env-abc123", metadata["name"])
			require.Equal(t, map[string]any{
				"api.openshift.com/environment": "test-env",
				"api.openshift.com/id":          "abc123",
				"api.openshift.com/name":        fixture.key.HCPClusterName,
			}, metadata["labels"])
		} else {
			require.Equal(t, fixture.serviceProvider.Status.ControlPlaneNamespace, metadata["name"])
			require.Equal(t, map[string]any{"app.kubernetes.io/managed-by": "aro-hcp-clusters-service", "hypershift.openshift.io/cluster": "abc123"}, metadata["labels"])
		}
		// Adopt both names in place even when their old owner tag remains.
		desire.Tags[kubeapplierapi.TagControllerName] = "ClusterResources"
		_, err = applyCRUD.Replace(ctx, desire, nil)
		require.NoError(t, err)
	}
	for _, read := range reads {
		read.Tags[kubeapplierapi.TagControllerName] = "ClusterResources"
		_, err = readCRUD.Replace(ctx, read, nil)
		require.NoError(t, err)
	}
	require.NoError(t, syncer.SyncOnce(ctx, fixture.key))
	adopted, adoptedReads := fixture.applies(t), fixture.reads(t)
	for _, desire := range adopted {
		require.Equal(t, NamespaceControllerName, desire.Tags[kubeapplierapi.TagControllerName])
	}
	for _, read := range adoptedReads {
		require.Equal(t, NamespaceControllerName, read.Tags[kubeapplierapi.TagControllerName])
	}
	require.NoError(t, syncer.SyncOnce(ctx, fixture.key))
	require.ElementsMatch(t, adopted, fixture.applies(t), "unchanged applies must not be rewritten")
	require.ElementsMatch(t, adoptedReads, fixture.reads(t), "unchanged reads must not be rewritten")
}

func TestNamespaceBootstrapWithoutHostedClusterObservation(t *testing.T) {
	fixture, syncer := newNamespaceFixture(t)
	fixture.serviceProvider.Status.HostedClusterNamespace = ""
	fixture.serviceProvider.Status.ControlPlaneNamespace = ""
	require.NoError(t, syncer.SyncOnce(t.Context(), fixture.key))
	desires := fixture.applies(t)
	require.Len(t, desires, 1, "HostedCluster namespace must be created before the HostedCluster can exist")
	require.Equal(t, "ocm-test-env-abc123", desires[0].Spec.TargetItem.Name)
	require.Equal(t, strings.ToLower(HostedClusterNamespaceDesireName), desires[0].ResourceID.Name)
}

func TestNamespaceDeletionWaitsForEveryPrecedingDesire(t *testing.T) {
	for name := range NamespaceDeletionPreconditionDesireNames() {
		t.Run(name, func(t *testing.T) {
			fixture, syncer := newNamespaceFixture(t)
			ctx := t.Context()
			require.NoError(t, syncer.SyncOnce(ctx, fixture.key))
			resourceIDString := kubeapplierapihelpers.ToClusterScopedApplyDesireResourceIDString(fixture.key.SubscriptionID, fixture.key.ResourceGroupName, fixture.key.HCPClusterName, name)
			if name == "nodepool" {
				resourceIDString = kubeapplierapihelpers.ToNodePoolScopedApplyDesireResourceIDString(fixture.key.SubscriptionID, fixture.key.ResourceGroupName, fixture.key.HCPClusterName, "worker", name)
			}
			resourceID := metadataapi.Must(azcorearm.ParseResourceID(resourceIDString))
			dependency := &kubeapplierapi.ApplyDesire{
				CosmosMetadata: coreapi.CosmosMetadata{ResourceID: resourceID, PartitionKey: strings.ToLower(fixture.managementCluster.ResourceID.String())},
				Spec:           kubeapplierapi.ApplyDesireSpec{ManagementCluster: fixture.managementCluster.ResourceID, Type: kubeapplierapi.ApplyDesireTypeDelete},
				Status:         kubeapplierapi.ApplyDesireStatus{Conditions: []metav1.Condition{{Type: kubeapplierapi.ConditionTypeSuccessfullyDeleted, Status: metav1.ConditionTrue}}},
			}
			scope, err := fixture.client.ApplyDesiresForCluster(fixture.key.SubscriptionID, fixture.key.ResourceGroupName, fixture.key.HCPClusterName)
			require.NoError(t, err)
			if name == "nodepool" {
				scope, err = fixture.client.ApplyDesiresForNodePool(fixture.key.SubscriptionID, fixture.key.ResourceGroupName, fixture.key.HCPClusterName, "worker")
				require.NoError(t, err)
			}
			_, err = scope.Create(ctx, dependency, nil)
			require.NoError(t, err)
			now := metav1.Now()
			fixture.cluster.ServiceProviderProperties.DeletionTimestamp = &now
			// The cache misses the blocker; the live precondition must still stop deletion.
			syncer.applyDesireLister = &kubeapplierlistertesting.SliceApplyDesireLister{}
			require.NoError(t, syncer.SyncOnce(ctx, fixture.key))
			for _, desire := range fixture.applies(t) {
				if isNamespaceDesire(desire.ResourceID.Name) {
					require.Equal(t, kubeapplierapi.ApplyDesireTypeServerSideApply, desire.Spec.Type, "namespace must wait until prerequisite document is removed")
				}
			}
			require.NoError(t, scope.Delete(ctx, name))
			require.NoError(t, syncer.SyncOnce(ctx, fixture.key))
			for _, desire := range fixture.applies(t) {
				require.Equal(t, kubeapplierapi.ApplyDesireTypeDelete, desire.Spec.Type, "both namespaces must enter the normal delete flow after preconditions pass")
			}
		})
	}
}

func TestNamespaceDeletionWaitsForSuccessfulDeletion(t *testing.T) {
	fixture, syncer := newNamespaceFixture(t)
	ctx := t.Context()
	require.NoError(t, syncer.SyncOnce(ctx, fixture.key))
	now := metav1.Now()
	fixture.cluster.ServiceProviderProperties.DeletionTimestamp = &now
	fixture.cluster.ServiceProviderProperties.ClusterServiceID = nil
	fixture.serviceProvider.Status.ControlPlaneNamespace = ""
	fixture.serviceProvider.Status.HostedClusterNamespace = ""
	require.NoError(t, syncer.SyncOnce(ctx, fixture.key))
	require.Len(t, fixture.applies(t), 2)
	require.Len(t, fixture.reads(t), 2, "reads remain while deletion is pending")
	applyCRUD, err := fixture.client.ApplyDesiresForCluster(fixture.key.SubscriptionID, fixture.key.ResourceGroupName, fixture.key.HCPClusterName)
	require.NoError(t, err)
	for _, desire := range fixture.applies(t) {
		require.Equal(t, kubeapplierapi.ApplyDesireTypeDelete, desire.Spec.Type)
		require.Nil(t, desire.Spec.ServerSideApply)
		desire.Status.Conditions = []metav1.Condition{{Type: kubeapplierapi.ConditionTypeSuccessful, Status: metav1.ConditionTrue}}
		_, err = applyCRUD.Replace(ctx, desire, nil)
		require.NoError(t, err)
	}
	require.NoError(t, syncer.SyncOnce(ctx, fixture.key))
	require.Len(t, fixture.applies(t), 2, "Successful alone does not confirm deletion")
	for _, desire := range fixture.applies(t) {
		desire.Status.Conditions = []metav1.Condition{{Type: kubeapplierapi.ConditionTypeSuccessfullyDeleted, Status: metav1.ConditionTrue}}
		_, err = applyCRUD.Replace(ctx, desire, nil)
		require.NoError(t, err)
	}
	require.NoError(t, syncer.SyncOnce(ctx, fixture.key))
	require.Empty(t, fixture.applies(t))
	require.Empty(t, fixture.reads(t))
	require.NoError(t, syncer.SyncOnce(ctx, fixture.key), "deletion is idempotent")
}

func TestNamespaceDeletionSweepsOrphanReads(t *testing.T) {
	fixture, syncer := newNamespaceFixture(t)
	ctx := t.Context()
	now := metav1.Now()
	fixture.cluster.ServiceProviderProperties.DeletionTimestamp = &now
	// Retry orphan reads when their ApplyDesires have already vanished.
	readCRUD, err := fixture.client.ReadDesiresForCluster(fixture.key.SubscriptionID, fixture.key.ResourceGroupName, fixture.key.HCPClusterName)
	require.NoError(t, err)
	for _, name := range []string{HostedClusterNamespaceDesireName, ControlPlaneNamespaceDesireName} {
		id := metadataapi.Must(azcorearm.ParseResourceID(kubeapplierapihelpers.ToClusterScopedReadDesireResourceIDString(fixture.key.SubscriptionID, fixture.key.ResourceGroupName, fixture.key.HCPClusterName, name)))
		_, err = readCRUD.Create(ctx, &kubeapplierapi.ReadDesire{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: id, PartitionKey: strings.ToLower(fixture.managementCluster.ResourceID.String())}, Spec: kubeapplierapi.ReadDesireSpec{ManagementCluster: fixture.managementCluster.ResourceID}}, nil)
		require.NoError(t, err)
	}
	require.NoError(t, syncer.SyncOnce(ctx, fixture.key))
	require.Empty(t, fixture.reads(t))
}
