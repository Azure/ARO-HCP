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

package deletion

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/kubeappliercosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func newTestManagementClusterContent(t *testing.T, name string) *coreapi.ManagementClusterContent {
	t.Helper()
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
			"/nodePools/" + testNodePoolName +
			"/managementClusterContents/" + name))
	return &coreapi.ManagementClusterContent{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: resourceID, PartitionKey: strings.ToLower(resourceID.SubscriptionID)},
	}
}

func newTestNodePoolController(t *testing.T, name string) *coreapi.Controller {
	t.Helper()
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
			"/nodePools/" + testNodePoolName +
			"/hcpOpenShiftControllers/" + name))
	return &coreapi.Controller{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: resourceID, PartitionKey: strings.ToLower(resourceID.SubscriptionID)},
		ExternalID: metadataapi.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + testSubscriptionID +
				"/resourceGroups/" + testResourceGroupName +
				"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
				"/nodePools/" + testNodePoolName)),
		Status: coreapi.ControllerStatus{
			Conditions: []metav1.Condition{},
		},
	}
}

func TestNodePoolChildResourcesCleanupController_SyncOnce(t *testing.T) {
	managementClusterResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/providers/microsoft.redhatopenshift/stamps/1/managementclusters/default"))
	unregisteredManagementClusterResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/providers/microsoft.redhatopenshift/stamps/1/managementclusters/unregistered"))

	newTestSPCWithManagementCluster := func(mcResourceID *azcorearm.ResourceID) *coreapi.ServiceProviderCluster {
		spcResourceID := metadataapi.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + testSubscriptionID +
				"/resourceGroups/" + testResourceGroupName +
				"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
				"/serviceProviderClusters/default"))
		return &coreapi.ServiceProviderCluster{
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID:   spcResourceID,
				PartitionKey: strings.ToLower(spcResourceID.SubscriptionID),
			},
			Status: coreapi.ServiceProviderClusterStatus{
				ManagementClusterResourceID: mcResourceID,
			},
		}
	}
	newTestNodePoolScopedReadDesire := func(name string) *kubeapplierapi.ReadDesire {
		resourceID := metadataapi.Must(azcorearm.ParseResourceID(
			kubeapplierapi.ToNodePoolScopedReadDesireResourceIDString(
				testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName, name)))
		return &kubeapplierapi.ReadDesire{
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID:   resourceID,
				PartitionKey: strings.ToLower(managementClusterResourceID.String()),
			},
			Spec: kubeapplierapi.ReadDesireSpec{
				ManagementCluster: managementClusterResourceID,
			},
		}
	}
	newTestClusterScopedReadDesire := func(name string) *kubeapplierapi.ReadDesire {
		resourceID := metadataapi.Must(azcorearm.ParseResourceID(
			kubeapplierapi.ToClusterScopedReadDesireResourceIDString(
				testSubscriptionID, testResourceGroupName, testClusterName, name)))
		return &kubeapplierapi.ReadDesire{
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID:   resourceID,
				PartitionKey: strings.ToLower(managementClusterResourceID.String()),
			},
			Spec: kubeapplierapi.ReadDesireSpec{
				ManagementCluster: managementClusterResourceID,
			},
		}
	}
	newTestClusterScopedApplyDesire := func(name string) *kubeapplierapi.ApplyDesire {
		resourceID := metadataapi.Must(azcorearm.ParseResourceID(
			kubeapplierapi.ToClusterScopedApplyDesireResourceIDString(
				testSubscriptionID, testResourceGroupName, testClusterName, name)))
		return &kubeapplierapi.ApplyDesire{
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID:   resourceID,
				PartitionKey: strings.ToLower(managementClusterResourceID.String()),
			},
			Spec: kubeapplierapi.ApplyDesireSpec{
				ManagementCluster: managementClusterResourceID,
			},
		}
	}
	newTestNodePoolScopedApplyDesire := func(name string) *kubeapplierapi.ApplyDesire {
		resourceID := metadataapi.Must(azcorearm.ParseResourceID(
			kubeapplierapi.ToNodePoolScopedApplyDesireResourceIDString(
				testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName, name)))
		return &kubeapplierapi.ApplyDesire{
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID:   resourceID,
				PartitionKey: strings.ToLower(managementClusterResourceID.String()),
			},
			Spec: kubeapplierapi.ApplyDesireSpec{
				ManagementCluster: managementClusterResourceID,
			},
		}
	}
	assertNoNodePoolScopedKubeApplierResources := func(
		t *testing.T,
		ctx context.Context,
		kubeApplierDBClients *kubeappliercosmosstoragetesting.MockKubeApplierDBClients,
	) {
		t.Helper()
		client := kubeApplierDBClients.For(ctx, managementClusterResourceID)
		require.NotNil(t, client)
		nodePoolResourceID := metadataapi.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + testSubscriptionID +
				"/resourceGroups/" + testResourceGroupName +
				"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
				"/nodePools/" + testNodePoolName))
		untypedCRUD, err := client.UntypedCRUD(*nodePoolResourceID)
		require.NoError(t, err)
		iter, err := untypedCRUD.List(ctx, nil)
		require.NoError(t, err)
		for _, resource := range iter.Items(ctx) {
			if resource.ResourceID != nil {
				t.Fatalf("expected no nodepool-scoped kube-applier resources, found %q", resource.ResourceID)
			}
		}
		require.NoError(t, iter.GetError())
	}
	assertClusterScopedKubeApplierResourceExists := func(
		t *testing.T,
		ctx context.Context,
		kubeApplierDBClients *kubeappliercosmosstoragetesting.MockKubeApplierDBClients,
		resourceIDString string,
	) {
		t.Helper()
		client := kubeApplierDBClients.For(ctx, managementClusterResourceID)
		require.NotNil(t, client)
		resourceID := metadataapi.Must(azcorearm.ParseResourceID(resourceIDString))
		untypedCRUD, err := client.UntypedCRUD(*resourceID.Parent)
		require.NoError(t, err)
		iter, err := untypedCRUD.ListRecursive(ctx, nil)
		require.NoError(t, err)
		for _, resource := range iter.Items(ctx) {
			if resource.ResourceID != nil && strings.EqualFold(resource.ResourceID.String(), resourceIDString) {
				require.NoError(t, iter.GetError())
				return
			}
		}
		require.NoError(t, iter.GetError())
		t.Fatalf("expected kube-applier resource %q to still exist", resourceIDString)
	}

	fixedNow := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	readyToDeleteNodePoolOptsFunc := func(np *coreapi.HCPOpenShiftClusterNodePool) {
		np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-time.Hour)}
		np.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-30 * time.Minute)}
		np.ServiceProviderProperties.ClusterServiceID = nil
	}

	testKey := controllerutils.HCPNodePoolKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
		HCPNodePoolName:   testNodePoolName,
	}

	testCases := []struct {
		name               string
		existingNodePool   *coreapi.HCPOpenShiftClusterNodePool
		childResources     []any
		kubeApplierDesires []any
		wantErr            bool
		verifyDB           func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, kubeApplierDBClients *kubeappliercosmosstoragetesting.MockKubeApplierDBClients)
	}{
		{
			name:             "when no DeletionTimestamp, no ClusterServiceDeletionTimestamp are set and ClusterServiceID is set performs a no-op",
			existingNodePool: newTestNodePoolWithNewDeletionApproach(t, nil),
			childResources:   []any{newTestManagementClusterContent(t, "untouched-mcc")},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, _ *kubeappliercosmosstoragetesting.MockKubeApplierDBClients) {
				mccCRUD := db.HCPClusters(testSubscriptionID, testResourceGroupName).
					NodePools(testClusterName).ManagementClusterContents(testNodePoolName)
				_, err := mccCRUD.Get(ctx, "untouched-mcc")
				require.NoError(t, err, "expected child resource to still exist")
			},
		},
		{
			name: "when no ClusterServiceDeletionTimestamp is set performs a no-op",
			existingNodePool: newTestNodePoolWithNewDeletionApproach(t, func(np *coreapi.HCPOpenShiftClusterNodePool) {
				np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-time.Hour)}
				np.ServiceProviderProperties.ClusterServiceDeletionTimestamp = nil
				np.ServiceProviderProperties.ClusterServiceID = nil
			}),
			childResources: []any{newTestManagementClusterContent(t, "untouched-mcc")},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, _ *kubeappliercosmosstoragetesting.MockKubeApplierDBClients) {
				mccCRUD := db.HCPClusters(testSubscriptionID, testResourceGroupName).
					NodePools(testClusterName).ManagementClusterContents(testNodePoolName)
				_, err := mccCRUD.Get(ctx, "untouched-mcc")
				require.NoError(t, err, "expected child resource to still exist")
			},
		},
		{
			name: "when ClusterServiceID is set performs a no-op",
			existingNodePool: newTestNodePoolWithNewDeletionApproach(t, func(np *coreapi.HCPOpenShiftClusterNodePool) {
				np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-time.Hour)}
				np.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-30 * time.Minute)}
			}),
			childResources: []any{newTestManagementClusterContent(t, "untouched-mcc")},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, _ *kubeappliercosmosstoragetesting.MockKubeApplierDBClients) {
				mccCRUD := db.HCPClusters(testSubscriptionID, testResourceGroupName).
					NodePools(testClusterName).ManagementClusterContents(testNodePoolName)
				_, err := mccCRUD.Get(ctx, "untouched-mcc")
				require.NoError(t, err, "expected child resource to still exist")
			},
		},
		{
			name:             "when all conditions met and there are no children performs a no-op",
			existingNodePool: newTestNodePoolWithNewDeletionApproach(t, readyToDeleteNodePoolOptsFunc),
		},
		{
			name:             "when there is a children resource it deletes it",
			existingNodePool: newTestNodePoolWithNewDeletionApproach(t, readyToDeleteNodePoolOptsFunc),
			childResources:   []any{newTestManagementClusterContent(t, "test-mcc")},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, _ *kubeappliercosmosstoragetesting.MockKubeApplierDBClients) {
				nodePoolResourceID := testKey.GetResourceID()
				untypedCRUD, err := db.UntypedCRUD(*nodePoolResourceID)
				require.NoError(t, err)
				childIterator, err := untypedCRUD.ListRecursive(ctx, nil)
				require.NoError(t, err)

				var remainingCount int
				for range childIterator.Items(ctx) {
					remainingCount++
				}
				require.NoError(t, childIterator.GetError())
				assert.Equal(t, 0, remainingCount, "expected no children to remain")
			},
		},
		{
			name:             "deletion of node pool controllers is skipped",
			existingNodePool: newTestNodePoolWithNewDeletionApproach(t, readyToDeleteNodePoolOptsFunc),
			childResources:   []any{newTestNodePoolController(t, "test-controller")},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, _ *kubeappliercosmosstoragetesting.MockKubeApplierDBClients) {
				nodePoolResourceID := testKey.GetResourceID()
				untypedCRUD, err := db.UntypedCRUD(*nodePoolResourceID)
				require.NoError(t, err)
				childIterator, err := untypedCRUD.ListRecursive(ctx, nil)
				require.NoError(t, err)

				var controllerCount int
				for _, child := range childIterator.Items(ctx) {
					if strings.EqualFold(child.ResourceType, coreapi.NodePoolControllerResourceType.String()) {
						controllerCount++
					}
				}
				require.NoError(t, childIterator.GetError())
				assert.Equal(t, 1, controllerCount, "expected controller child to remain")
			},
		},
		{
			name:             "when there are nodepool controller and non nodepool controller children it deletes the non nodepool controller children",
			existingNodePool: newTestNodePoolWithNewDeletionApproach(t, readyToDeleteNodePoolOptsFunc),
			childResources:   []any{newTestManagementClusterContent(t, "test-mcc"), newTestNodePoolController(t, "test-controller")},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, _ *kubeappliercosmosstoragetesting.MockKubeApplierDBClients) {
				nodePoolResourceID := testKey.GetResourceID()
				untypedCRUD, err := db.UntypedCRUD(*nodePoolResourceID)
				require.NoError(t, err)
				childIterator, err := untypedCRUD.ListRecursive(ctx, nil)
				require.NoError(t, err)

				var remainingCount int
				var controllerCount int
				for _, child := range childIterator.Items(ctx) {
					remainingCount++
					if strings.EqualFold(child.ResourceType, coreapi.NodePoolControllerResourceType.String()) {
						controllerCount++
					}
				}
				require.NoError(t, childIterator.GetError())
				assert.Equal(t, 1, remainingCount, "expected only controller child to remain")
				assert.Equal(t, 1, controllerCount, "expected the remaining child to be a controller")
			},
		},
		{
			name: "when the node pool is not found performs a no-op",
		},
		{
			name:             "when there is a child ServiceProviderNodePool and ServiceProviderCluster is missing it deletes SPNP best-effort",
			existingNodePool: newTestNodePoolWithNewDeletionApproach(t, readyToDeleteNodePoolOptsFunc),
			childResources:   []any{newTestSPNP(t, nil)},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, _ *kubeappliercosmosstoragetesting.MockKubeApplierDBClients) {
				spnpCRUD := db.ServiceProviderNodePools(
					testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName)
				_, err := spnpCRUD.Get(ctx, coreapi.ServiceProviderNodePoolResourceName)
				require.True(t, cosmosstorageutils.IsNotFoundError(err), "expected SPNP to be deleted")
			},
		},
		{
			name:             "when there is a child ServiceProviderNodePool and it does not have Maestro bundle references it deletes it",
			existingNodePool: newTestNodePoolWithNewDeletionApproach(t, readyToDeleteNodePoolOptsFunc),
			childResources: []any{
				newTestSPCWithManagementCluster(nil),
				newTestSPNP(t, nil),
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, _ *kubeappliercosmosstoragetesting.MockKubeApplierDBClients) {
				spnpCRUD := db.ServiceProviderNodePools(
					testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName)
				_, err := spnpCRUD.Get(ctx, coreapi.ServiceProviderNodePoolResourceName)
				require.True(t, cosmosstorageutils.IsNotFoundError(err), "expected SPNP to be deleted")
			},
		},
		{
			name:             "when there is a child ServiceProviderNodePool and it has Maestro bundle references it does not delete it",
			existingNodePool: newTestNodePoolWithNewDeletionApproach(t, readyToDeleteNodePoolOptsFunc),
			childResources: []any{newTestSPNP(t, coreapi.MaestroBundleReferenceList{
				{Name: "bundle-a", MaestroAPIMaestroBundleName: "name-a", MaestroAPIMaestroBundleID: "id-a"},
			})},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, _ *kubeappliercosmosstoragetesting.MockKubeApplierDBClients) {
				spnpCRUD := db.ServiceProviderNodePools(
					testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName)
				_, err := spnpCRUD.Get(ctx, coreapi.ServiceProviderNodePoolResourceName)
				require.NoError(t, err, "expected SPNP to still exist")
			},
		},
		{
			name:             "when there are child resources including a ServiceProviderNodePool with Maestro bundle references it deletes them excluding it",
			existingNodePool: newTestNodePoolWithNewDeletionApproach(t, readyToDeleteNodePoolOptsFunc),
			childResources: []any{
				newTestManagementClusterContent(t, "gate-mcc"),
				newTestSPNP(t, coreapi.MaestroBundleReferenceList{
					{Name: "bundle-a", MaestroAPIMaestroBundleName: "name-a", MaestroAPIMaestroBundleID: "id-a"},
				}),
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, _ *kubeappliercosmosstoragetesting.MockKubeApplierDBClients) {
				mccCRUD := db.HCPClusters(testSubscriptionID, testResourceGroupName).
					NodePools(testClusterName).ManagementClusterContents(testNodePoolName)
				_, err := mccCRUD.Get(ctx, "gate-mcc")
				require.True(t, cosmosstorageutils.IsNotFoundError(err), "expected MCC gate-mcc to be deleted")

				spnpCRUD := db.ServiceProviderNodePools(
					testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName)
				_, err = spnpCRUD.Get(ctx, coreapi.ServiceProviderNodePoolResourceName)
				require.NoError(t, err, "expected SPNP to still exist")
			},
		},
		{
			name:             "UsesNewNodePoolDeletionApproach false -- no-op even when all cleanup conditions met and children exist",
			existingNodePool: newTestNodePoolWithOldDeletionApproach(t, readyToDeleteNodePoolOptsFunc),
			childResources:   []any{newTestManagementClusterContent(t, "untouched-mcc"), newTestSPNP(t, nil)},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, _ *kubeappliercosmosstoragetesting.MockKubeApplierDBClients) {
				mccCRUD := db.HCPClusters(testSubscriptionID, testResourceGroupName).
					NodePools(testClusterName).ManagementClusterContents(testNodePoolName)
				_, err := mccCRUD.Get(ctx, "untouched-mcc")
				require.NoError(t, err, "expected child resource to still exist")

				spnpCRUD := db.ServiceProviderNodePools(
					testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName)
				_, err = spnpCRUD.Get(ctx, coreapi.ServiceProviderNodePoolResourceName)
				require.NoError(t, err, "expected SPNP to still exist")
			},
		},
		{
			name:             "when nodepool has nodepool-scoped kube-applier desires it deletes them",
			existingNodePool: newTestNodePoolWithNewDeletionApproach(t, readyToDeleteNodePoolOptsFunc),
			childResources: []any{
				newTestSPCWithManagementCluster(managementClusterResourceID),
			},
			kubeApplierDesires: []any{newTestNodePoolScopedReadDesire("readonly-nodepool")},
			verifyDB: func(t *testing.T, ctx context.Context, _ *corecosmosstoragetesting.MockResourcesDBClient, kubeApplierDBClients *kubeappliercosmosstoragetesting.MockKubeApplierDBClients) {
				assertNoNodePoolScopedKubeApplierResources(t, ctx, kubeApplierDBClients)
			},
		},
		{
			name:             "when SPNP has nodepool-scoped kube-applier desires but no kube-applier client it deletes SPNP best-effort",
			existingNodePool: newTestNodePoolWithNewDeletionApproach(t, readyToDeleteNodePoolOptsFunc),
			childResources: []any{
				newTestSPCWithManagementCluster(unregisteredManagementClusterResourceID),
				newTestSPNP(t, nil),
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, kubeApplierDBClients *kubeappliercosmosstoragetesting.MockKubeApplierDBClients) {
				spnpCRUD := db.ServiceProviderNodePools(
					testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName)
				_, err := spnpCRUD.Get(ctx, coreapi.ServiceProviderNodePoolResourceName)
				require.True(t, cosmosstorageutils.IsNotFoundError(err), "expected SPNP to be deleted")

				require.Nil(t, kubeApplierDBClients.For(ctx, unregisteredManagementClusterResourceID))
			},
		},
		{
			name:             "when SPNP has nodepool-scoped kube-applier desires it deletes desires then SPNP",
			existingNodePool: newTestNodePoolWithNewDeletionApproach(t, readyToDeleteNodePoolOptsFunc),
			childResources: []any{
				newTestSPCWithManagementCluster(managementClusterResourceID),
				newTestSPNP(t, nil),
			},
			kubeApplierDesires: []any{newTestNodePoolScopedReadDesire("readonly-nodepool")},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, kubeApplierDBClients *kubeappliercosmosstoragetesting.MockKubeApplierDBClients) {
				spnpCRUD := db.ServiceProviderNodePools(
					testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName)
				_, err := spnpCRUD.Get(ctx, coreapi.ServiceProviderNodePoolResourceName)
				require.True(t, cosmosstorageutils.IsNotFoundError(err), "expected SPNP to be deleted")

				assertNoNodePoolScopedKubeApplierResources(t, ctx, kubeApplierDBClients)
			},
		},
		{
			name:             "when nodepool has cluster and nodepool scoped kube-applier resources it deletes only nodepool scoped ones",
			existingNodePool: newTestNodePoolWithNewDeletionApproach(t, readyToDeleteNodePoolOptsFunc),
			childResources: []any{
				newTestSPCWithManagementCluster(managementClusterResourceID),
			},
			kubeApplierDesires: []any{
				newTestClusterScopedReadDesire("readonly-hostedcluster"),
				newTestClusterScopedApplyDesire("apply-example"),
				newTestNodePoolScopedReadDesire("readonly-nodepool"),
				newTestNodePoolScopedApplyDesire("apply-nodepool"),
			},
			verifyDB: func(t *testing.T, ctx context.Context, _ *corecosmosstoragetesting.MockResourcesDBClient, kubeApplierDBClients *kubeappliercosmosstoragetesting.MockKubeApplierDBClients) {
				assertNoNodePoolScopedKubeApplierResources(t, ctx, kubeApplierDBClients)
				assertClusterScopedKubeApplierResourceExists(t, ctx, kubeApplierDBClients,
					kubeapplierapi.ToClusterScopedReadDesireResourceIDString(
						testSubscriptionID, testResourceGroupName, testClusterName, "readonly-hostedcluster"))
				assertClusterScopedKubeApplierResourceExists(t, ctx, kubeApplierDBClients,
					kubeapplierapi.ToClusterScopedApplyDesireResourceIDString(
						testSubscriptionID, testResourceGroupName, testClusterName, "apply-example"))
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

			resources := []any{}
			if tc.existingNodePool != nil {
				resources = append(resources, tc.existingNodePool)
			}
			resources = append(resources, tc.childResources...)
			mockResourcesDBClient, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			mockKubeApplierDBClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
			mockKubeApplierClient, err := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClientWithResources(ctx, tc.kubeApplierDesires)
			require.NoError(t, err)
			mockKubeApplierDBClients.Register(managementClusterResourceID, mockKubeApplierClient)

			nodePoolsForLister := []*coreapi.HCPOpenShiftClusterNodePool{}
			if tc.existingNodePool != nil {
				nodePoolsForLister = append(nodePoolsForLister, tc.existingNodePool)
			}

			syncer := &nodePoolChildResourcesCleanupController{
				nodePoolLister:       &corelistertesting.SliceNodePoolLister{NodePools: nodePoolsForLister},
				resourcesDBClient:    mockResourcesDBClient,
				kubeApplierDBClients: mockKubeApplierDBClients,
			}

			err = syncer.SyncOnce(ctx, testKey)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			if tc.verifyDB != nil {
				tc.verifyDB(t, ctx, mockResourcesDBClient, mockKubeApplierDBClients)
			}
		})
	}
}
