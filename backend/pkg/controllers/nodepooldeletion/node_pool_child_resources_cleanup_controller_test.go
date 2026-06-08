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

package nodepooldeletion

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

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func newTestManagementClusterContent(t *testing.T, name string) *api.ManagementClusterContent {
	t.Helper()
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
			"/nodePools/" + testNodePoolName +
			"/managementClusterContents/" + name))
	return &api.ManagementClusterContent{
		CosmosMetadata: api.CosmosMetadata{ResourceID: resourceID},
	}
}

func newTestNodePoolController(t *testing.T, name string) *api.Controller {
	t.Helper()
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
			"/nodePools/" + testNodePoolName +
			"/hcpOpenShiftControllers/" + name))
	return &api.Controller{
		CosmosMetadata: api.CosmosMetadata{ResourceID: resourceID},
		ExternalID: api.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + testSubscriptionID +
				"/resourceGroups/" + testResourceGroupName +
				"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
				"/nodePools/" + testNodePoolName)),
		Status: api.ControllerStatus{
			Conditions: []metav1.Condition{},
		},
	}
}

func TestNodePoolChildResourcesCleanupController_SyncOnce(t *testing.T) {
	managementClusterResourceID := api.Must(azcorearm.ParseResourceID(
		"/providers/microsoft.redhatopenshift/stamps/1/managementclusters/default"))
	unregisteredManagementClusterResourceID := api.Must(azcorearm.ParseResourceID(
		"/providers/microsoft.redhatopenshift/stamps/1/managementclusters/unregistered"))

	newTestSPCWithManagementCluster := func(mcResourceID *azcorearm.ResourceID) *api.ServiceProviderCluster {
		spcResourceID := api.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + testSubscriptionID +
				"/resourceGroups/" + testResourceGroupName +
				"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
				"/serviceProviderClusters/default"))
		return &api.ServiceProviderCluster{
			CosmosMetadata: arm.CosmosMetadata{ResourceID: spcResourceID},
			Status: api.ServiceProviderClusterStatus{
				ManagementClusterResourceID: mcResourceID,
			},
		}
	}
	newTestNodePoolScopedReadDesire := func(name string) *kubeapplier.ReadDesire {
		resourceID := api.Must(azcorearm.ParseResourceID(
			kubeapplier.ToNodePoolScopedReadDesireResourceIDString(
				testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName, name)))
		return &kubeapplier.ReadDesire{
			CosmosMetadata: api.CosmosMetadata{ResourceID: resourceID},
			Spec: kubeapplier.ReadDesireSpec{
				ManagementCluster: managementClusterResourceID,
			},
		}
	}
	newTestClusterScopedReadDesire := func(name string) *kubeapplier.ReadDesire {
		resourceID := api.Must(azcorearm.ParseResourceID(
			kubeapplier.ToClusterScopedReadDesireResourceIDString(
				testSubscriptionID, testResourceGroupName, testClusterName, name)))
		return &kubeapplier.ReadDesire{
			CosmosMetadata: api.CosmosMetadata{ResourceID: resourceID},
			Spec: kubeapplier.ReadDesireSpec{
				ManagementCluster: managementClusterResourceID,
			},
		}
	}
	newTestClusterScopedApplyDesire := func(name string) *kubeapplier.ApplyDesire {
		resourceID := api.Must(azcorearm.ParseResourceID(
			kubeapplier.ToClusterScopedApplyDesireResourceIDString(
				testSubscriptionID, testResourceGroupName, testClusterName, name)))
		return &kubeapplier.ApplyDesire{
			CosmosMetadata: api.CosmosMetadata{ResourceID: resourceID},
			Spec: kubeapplier.ApplyDesireSpec{
				ManagementCluster: managementClusterResourceID,
			},
		}
	}
	newTestNodePoolScopedApplyDesire := func(name string) *kubeapplier.ApplyDesire {
		resourceID := api.Must(azcorearm.ParseResourceID(
			kubeapplier.ToNodePoolScopedApplyDesireResourceIDString(
				testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName, name)))
		return &kubeapplier.ApplyDesire{
			CosmosMetadata: api.CosmosMetadata{ResourceID: resourceID},
			Spec: kubeapplier.ApplyDesireSpec{
				ManagementCluster: managementClusterResourceID,
			},
		}
	}
	newTestNodePoolScopedDeleteDesire := func(name string) *kubeapplier.DeleteDesire {
		resourceID := api.Must(azcorearm.ParseResourceID(
			kubeapplier.ToNodePoolScopedDeleteDesireResourceIDString(
				testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName, name)))
		return &kubeapplier.DeleteDesire{
			CosmosMetadata: api.CosmosMetadata{ResourceID: resourceID},
			Spec: kubeapplier.DeleteDesireSpec{
				ManagementCluster: managementClusterResourceID,
			},
		}
	}
	assertNoNodePoolScopedKubeApplierResources := func(
		t *testing.T,
		ctx context.Context,
		kubeApplierDBClients *databasetesting.MockKubeApplierDBClients,
	) {
		t.Helper()
		client := kubeApplierDBClients.For(ctx, managementClusterResourceID)
		require.NotNil(t, client)
		nodePoolResourceID := api.Must(azcorearm.ParseResourceID(
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
		kubeApplierDBClients *databasetesting.MockKubeApplierDBClients,
		resourceIDString string,
	) {
		t.Helper()
		client := kubeApplierDBClients.For(ctx, managementClusterResourceID)
		require.NotNil(t, client)
		resourceID := api.Must(azcorearm.ParseResourceID(resourceIDString))
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
	readyToDeleteNodePoolOptsFunc := func(np *api.HCPOpenShiftClusterNodePool) {
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
		existingNodePool   *api.HCPOpenShiftClusterNodePool
		childResources     []any
		kubeApplierDesires []any
		wantErr            bool
		verifyDB           func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, kubeApplierDBClients *databasetesting.MockKubeApplierDBClients)
	}{
		{
			name:             "when no DeletionTimestamp, no ClusterServiceDeletionTimestamp are set and ClusterServiceID is set performs a no-op",
			existingNodePool: newTestNodePoolWithNewDeletionApproach(t, nil),
			childResources:   []any{newTestManagementClusterContent(t, "untouched-mcc")},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, _ *databasetesting.MockKubeApplierDBClients) {
				mccCRUD := db.HCPClusters(testSubscriptionID, testResourceGroupName).
					NodePools(testClusterName).ManagementClusterContents(testNodePoolName)
				_, err := mccCRUD.Get(ctx, "untouched-mcc")
				require.NoError(t, err, "expected child resource to still exist")
			},
		},
		{
			name: "when no ClusterServiceDeletionTimestamp is set performs a no-op",
			existingNodePool: newTestNodePoolWithNewDeletionApproach(t, func(np *api.HCPOpenShiftClusterNodePool) {
				np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-time.Hour)}
				np.ServiceProviderProperties.ClusterServiceDeletionTimestamp = nil
				np.ServiceProviderProperties.ClusterServiceID = nil
			}),
			childResources: []any{newTestManagementClusterContent(t, "untouched-mcc")},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, _ *databasetesting.MockKubeApplierDBClients) {
				mccCRUD := db.HCPClusters(testSubscriptionID, testResourceGroupName).
					NodePools(testClusterName).ManagementClusterContents(testNodePoolName)
				_, err := mccCRUD.Get(ctx, "untouched-mcc")
				require.NoError(t, err, "expected child resource to still exist")
			},
		},
		{
			name: "when ClusterServiceID is set performs a no-op",
			existingNodePool: newTestNodePoolWithNewDeletionApproach(t, func(np *api.HCPOpenShiftClusterNodePool) {
				np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-time.Hour)}
				np.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-30 * time.Minute)}
			}),
			childResources: []any{newTestManagementClusterContent(t, "untouched-mcc")},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, _ *databasetesting.MockKubeApplierDBClients) {
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
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, _ *databasetesting.MockKubeApplierDBClients) {
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
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, _ *databasetesting.MockKubeApplierDBClients) {
				nodePoolResourceID := testKey.GetResourceID()
				untypedCRUD, err := db.UntypedCRUD(*nodePoolResourceID)
				require.NoError(t, err)
				childIterator, err := untypedCRUD.ListRecursive(ctx, nil)
				require.NoError(t, err)

				var controllerCount int
				for _, child := range childIterator.Items(ctx) {
					if strings.EqualFold(child.ResourceType, api.NodePoolControllerResourceType.String()) {
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
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, _ *databasetesting.MockKubeApplierDBClients) {
				nodePoolResourceID := testKey.GetResourceID()
				untypedCRUD, err := db.UntypedCRUD(*nodePoolResourceID)
				require.NoError(t, err)
				childIterator, err := untypedCRUD.ListRecursive(ctx, nil)
				require.NoError(t, err)

				var remainingCount int
				var controllerCount int
				for _, child := range childIterator.Items(ctx) {
					remainingCount++
					if strings.EqualFold(child.ResourceType, api.NodePoolControllerResourceType.String()) {
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
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, _ *databasetesting.MockKubeApplierDBClients) {
				spnpCRUD := db.ServiceProviderNodePools(
					testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName)
				_, err := spnpCRUD.Get(ctx, api.ServiceProviderNodePoolResourceName)
				require.True(t, database.IsNotFoundError(err), "expected SPNP to be deleted")
			},
		},
		{
			name:             "when there is a child ServiceProviderNodePool and it does not have Maestro bundle references it deletes it",
			existingNodePool: newTestNodePoolWithNewDeletionApproach(t, readyToDeleteNodePoolOptsFunc),
			childResources: []any{
				newTestSPCWithManagementCluster(nil),
				newTestSPNP(t, nil),
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, _ *databasetesting.MockKubeApplierDBClients) {
				spnpCRUD := db.ServiceProviderNodePools(
					testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName)
				_, err := spnpCRUD.Get(ctx, api.ServiceProviderNodePoolResourceName)
				require.True(t, database.IsNotFoundError(err), "expected SPNP to be deleted")
			},
		},
		{
			name:             "when there is a child ServiceProviderNodePool and it has Maestro bundle references it does not delete it",
			existingNodePool: newTestNodePoolWithNewDeletionApproach(t, readyToDeleteNodePoolOptsFunc),
			childResources: []any{newTestSPNP(t, api.MaestroBundleReferenceList{
				{Name: "bundle-a", MaestroAPIMaestroBundleName: "name-a", MaestroAPIMaestroBundleID: "id-a"},
			})},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, _ *databasetesting.MockKubeApplierDBClients) {
				spnpCRUD := db.ServiceProviderNodePools(
					testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName)
				_, err := spnpCRUD.Get(ctx, api.ServiceProviderNodePoolResourceName)
				require.NoError(t, err, "expected SPNP to still exist")
			},
		},
		{
			name:             "when there are child resources including a ServiceProviderNodePool with Maestro bundle references it deletes them excluding it",
			existingNodePool: newTestNodePoolWithNewDeletionApproach(t, readyToDeleteNodePoolOptsFunc),
			childResources: []any{
				newTestManagementClusterContent(t, "gate-mcc"),
				newTestSPNP(t, api.MaestroBundleReferenceList{
					{Name: "bundle-a", MaestroAPIMaestroBundleName: "name-a", MaestroAPIMaestroBundleID: "id-a"},
				}),
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, _ *databasetesting.MockKubeApplierDBClients) {
				mccCRUD := db.HCPClusters(testSubscriptionID, testResourceGroupName).
					NodePools(testClusterName).ManagementClusterContents(testNodePoolName)
				_, err := mccCRUD.Get(ctx, "gate-mcc")
				require.True(t, database.IsNotFoundError(err), "expected MCC gate-mcc to be deleted")

				spnpCRUD := db.ServiceProviderNodePools(
					testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName)
				_, err = spnpCRUD.Get(ctx, api.ServiceProviderNodePoolResourceName)
				require.NoError(t, err, "expected SPNP to still exist")
			},
		},
		{
			name:             "UsesNewNodePoolDeletionApproach false -- no-op even when all cleanup conditions met and children exist",
			existingNodePool: newTestNodePoolWithOldDeletionApproach(t, readyToDeleteNodePoolOptsFunc),
			childResources:   []any{newTestManagementClusterContent(t, "untouched-mcc"), newTestSPNP(t, nil)},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, _ *databasetesting.MockKubeApplierDBClients) {
				mccCRUD := db.HCPClusters(testSubscriptionID, testResourceGroupName).
					NodePools(testClusterName).ManagementClusterContents(testNodePoolName)
				_, err := mccCRUD.Get(ctx, "untouched-mcc")
				require.NoError(t, err, "expected child resource to still exist")

				spnpCRUD := db.ServiceProviderNodePools(
					testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName)
				_, err = spnpCRUD.Get(ctx, api.ServiceProviderNodePoolResourceName)
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
			verifyDB: func(t *testing.T, ctx context.Context, _ *databasetesting.MockResourcesDBClient, kubeApplierDBClients *databasetesting.MockKubeApplierDBClients) {
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
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, kubeApplierDBClients *databasetesting.MockKubeApplierDBClients) {
				spnpCRUD := db.ServiceProviderNodePools(
					testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName)
				_, err := spnpCRUD.Get(ctx, api.ServiceProviderNodePoolResourceName)
				require.True(t, database.IsNotFoundError(err), "expected SPNP to be deleted")

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
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, kubeApplierDBClients *databasetesting.MockKubeApplierDBClients) {
				spnpCRUD := db.ServiceProviderNodePools(
					testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName)
				_, err := spnpCRUD.Get(ctx, api.ServiceProviderNodePoolResourceName)
				require.True(t, database.IsNotFoundError(err), "expected SPNP to be deleted")

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
				newTestNodePoolScopedDeleteDesire("delete-example"),
			},
			verifyDB: func(t *testing.T, ctx context.Context, _ *databasetesting.MockResourcesDBClient, kubeApplierDBClients *databasetesting.MockKubeApplierDBClients) {
				assertNoNodePoolScopedKubeApplierResources(t, ctx, kubeApplierDBClients)
				assertClusterScopedKubeApplierResourceExists(t, ctx, kubeApplierDBClients,
					kubeapplier.ToClusterScopedReadDesireResourceIDString(
						testSubscriptionID, testResourceGroupName, testClusterName, "readonly-hostedcluster"))
				assertClusterScopedKubeApplierResourceExists(t, ctx, kubeApplierDBClients,
					kubeapplier.ToClusterScopedApplyDesireResourceIDString(
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
			mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			mockKubeApplierDBClients := databasetesting.NewMockKubeApplierDBClients()
			mockKubeApplierClient, err := databasetesting.NewMockKubeApplierDBClientWithResources(ctx, tc.kubeApplierDesires)
			require.NoError(t, err)
			mockKubeApplierDBClients.Register(managementClusterResourceID, mockKubeApplierClient)

			nodePoolsForLister := []*api.HCPOpenShiftClusterNodePool{}
			if tc.existingNodePool != nil {
				nodePoolsForLister = append(nodePoolsForLister, tc.existingNodePool)
			}

			syncer := &nodePoolChildResourcesCleanupController{
				cooldownChecker:      &alwaysSyncCooldownChecker{},
				nodePoolLister:       &listertesting.SliceNodePoolLister{NodePools: nodePoolsForLister},
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
