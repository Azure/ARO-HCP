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

package clusterdeletion

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

func TestClusterChildResourcesCleanupController_SyncOnce(t *testing.T) {
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
			CosmosMetadata: arm.CosmosMetadata{
				ResourceID:   spcResourceID,
				PartitionKey: strings.ToLower(spcResourceID.SubscriptionID),
			},
			Status: api.ServiceProviderClusterStatus{
				ManagementClusterResourceID: mcResourceID,
			},
		}
	}
	newTestClusterScopedManagementClusterContent := func(name string) *api.ManagementClusterContent {
		resourceID := api.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + testSubscriptionID +
				"/resourceGroups/" + testResourceGroupName +
				"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
				"/managementClusterContents/" + name))
		return &api.ManagementClusterContent{
			CosmosMetadata: api.CosmosMetadata{
				ResourceID:   resourceID,
				PartitionKey: strings.ToLower(resourceID.SubscriptionID),
			},
		}
	}
	newTestClusterController := func(name string) *api.Controller {
		resourceID := api.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + testSubscriptionID +
				"/resourceGroups/" + testResourceGroupName +
				"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
				"/hcpOpenShiftControllers/" + name))
		return &api.Controller{
			CosmosMetadata: api.CosmosMetadata{
				ResourceID:   resourceID,
				PartitionKey: strings.ToLower(resourceID.SubscriptionID),
			},
			ExternalID: api.Must(azcorearm.ParseResourceID(
				"/subscriptions/" + testSubscriptionID +
					"/resourceGroups/" + testResourceGroupName +
					"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName)),
			Status: api.ControllerStatus{
				Conditions: []metav1.Condition{},
			},
		}
	}

	newTestReadDesire := func(resourceIDString string) *kubeapplier.ReadDesire {
		resourceID := api.Must(azcorearm.ParseResourceID(resourceIDString))
		return &kubeapplier.ReadDesire{
			CosmosMetadata: api.CosmosMetadata{
				ResourceID:   resourceID,
				PartitionKey: strings.ToLower(managementClusterResourceID.String()),
			},
			Spec: kubeapplier.ReadDesireSpec{
				ManagementCluster: managementClusterResourceID,
			},
		}
	}
	newTestClusterScopedReadDesire := func(name string) *kubeapplier.ReadDesire {
		return newTestReadDesire(kubeapplier.ToClusterScopedReadDesireResourceIDString(
			testSubscriptionID, testResourceGroupName, testClusterName, name))
	}
	newTestNodePoolScopedReadDesire := func(nodePoolName, name string) *kubeapplier.ReadDesire {
		return newTestReadDesire(kubeapplier.ToNodePoolScopedReadDesireResourceIDString(
			testSubscriptionID, testResourceGroupName, testClusterName, nodePoolName, name))
	}
	newTestClusterScopedApplyDesire := func(name string) *kubeapplier.ApplyDesire {
		resourceID := api.Must(azcorearm.ParseResourceID(
			kubeapplier.ToClusterScopedApplyDesireResourceIDString(
				testSubscriptionID, testResourceGroupName, testClusterName, name)))
		return &kubeapplier.ApplyDesire{
			CosmosMetadata: api.CosmosMetadata{
				ResourceID:   resourceID,
				PartitionKey: strings.ToLower(managementClusterResourceID.String()),
			},
			Spec: kubeapplier.ApplyDesireSpec{
				ManagementCluster: managementClusterResourceID,
			},
		}
	}
	newTestNodePoolScopedDeleteDesire := func(nodePoolName, name string) *kubeapplier.DeleteDesire {
		resourceID := api.Must(azcorearm.ParseResourceID(
			kubeapplier.ToNodePoolScopedDeleteDesireResourceIDString(
				testSubscriptionID, testResourceGroupName, testClusterName, nodePoolName, name)))
		return &kubeapplier.DeleteDesire{
			CosmosMetadata: api.CosmosMetadata{
				ResourceID:   resourceID,
				PartitionKey: strings.ToLower(managementClusterResourceID.String()),
			},
			Spec: kubeapplier.DeleteDesireSpec{
				ManagementCluster: managementClusterResourceID,
			},
		}
	}
	assertNoClusterScopedKubeApplierResources := func(
		t *testing.T,
		ctx context.Context,
		kubeApplierDBClients *databasetesting.MockKubeApplierDBClients,
	) {
		t.Helper()
		client := kubeApplierDBClients.For(ctx, managementClusterResourceID)
		require.NotNil(t, client)
		clusterResourceID := api.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + testSubscriptionID +
				"/resourceGroups/" + testResourceGroupName +
				"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName))
		untypedCRUD, err := client.UntypedCRUD(*clusterResourceID)
		require.NoError(t, err)
		iter, err := untypedCRUD.List(ctx, nil)
		require.NoError(t, err)
		for _, resource := range iter.Items(ctx) {
			if resource.ResourceID != nil {
				t.Fatalf("expected no cluster-scoped kube-applier resources, found %q", resource.ResourceID)
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
	readyToDeleteClusterOptsFunc := func(c *api.HCPOpenShiftCluster) {
		c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-time.Hour)}
		c.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-30 * time.Minute)}
		c.ServiceProviderProperties.ClusterServiceID = nil
	}

	testKey := controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}

	testCases := []struct {
		name               string
		existingCluster    *api.HCPOpenShiftCluster
		childResources     []any
		kubeApplierDesires []any
		wantErr            bool
		verifyDB           func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, kubeApplierDBClients *databasetesting.MockKubeApplierDBClients,
		)
	}{
		{
			name:            "when no DeletionTimestamp is set performs a no-op",
			existingCluster: newTestClusterWithNewDeletionApproach(t, nil),
			childResources:  []any{newTestClusterScopedManagementClusterContent("untouched-mcc")},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, _ *databasetesting.MockKubeApplierDBClients) {
				mccCRUD := db.HCPClusters(testSubscriptionID, testResourceGroupName).ManagementClusterContents(testClusterName)
				_, err := mccCRUD.Get(ctx, "untouched-mcc")
				require.NoError(t, err, "expected child resource to still exist")
			},
		},
		{
			name: "when no ClusterServiceDeletionTimestamp is set performs a no-op",
			existingCluster: newTestClusterWithNewDeletionApproach(t, func(c *api.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-time.Hour)}
				c.ServiceProviderProperties.ClusterServiceDeletionTimestamp = nil
				c.ServiceProviderProperties.ClusterServiceID = nil
			}),
			childResources: []any{newTestClusterScopedManagementClusterContent("untouched-mcc")},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, _ *databasetesting.MockKubeApplierDBClients) {
				mccCRUD := db.HCPClusters(testSubscriptionID, testResourceGroupName).ManagementClusterContents(testClusterName)
				_, err := mccCRUD.Get(ctx, "untouched-mcc")
				require.NoError(t, err, "expected child resource to still exist")
			},
		},
		{
			name: "when ClusterServiceID is set performs a no-op",
			existingCluster: newTestClusterWithNewDeletionApproach(t, func(c *api.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-time.Hour)}
				c.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-30 * time.Minute)}
			}),
			childResources: []any{newTestClusterScopedManagementClusterContent("untouched-mcc")},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, _ *databasetesting.MockKubeApplierDBClients) {
				mccCRUD := db.HCPClusters(testSubscriptionID, testResourceGroupName).ManagementClusterContents(testClusterName)
				_, err := mccCRUD.Get(ctx, "untouched-mcc")
				require.NoError(t, err, "expected child resource to still exist")
			},
		},
		{
			name:            "when all conditions met and there are no children performs a no-op",
			existingCluster: newTestClusterWithNewDeletionApproach(t, readyToDeleteClusterOptsFunc),
		},
		{
			name:            "when there is a child resource it deletes it",
			existingCluster: newTestClusterWithNewDeletionApproach(t, readyToDeleteClusterOptsFunc),
			childResources:  []any{newTestClusterScopedManagementClusterContent("test-mcc")},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, _ *databasetesting.MockKubeApplierDBClients) {
				mccCRUD := db.HCPClusters(testSubscriptionID, testResourceGroupName).ManagementClusterContents(testClusterName)
				_, err := mccCRUD.Get(ctx, "test-mcc")
				require.True(t, database.IsNotFoundError(err), "expected MCC to be deleted")
			},
		},
		{
			name:            "deletion of cluster controllers is skipped",
			existingCluster: newTestClusterWithNewDeletionApproach(t, readyToDeleteClusterOptsFunc),
			childResources:  []any{newTestClusterController("test-controller")},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, _ *databasetesting.MockKubeApplierDBClients) {
				cluster := newTestClusterWithNewDeletionApproach(t, nil)
				untypedCRUD, err := db.UntypedCRUD(*cluster.ID)
				require.NoError(t, err)
				childIterator, err := untypedCRUD.ListRecursive(ctx, nil)
				require.NoError(t, err)

				var controllerCount int
				for _, child := range childIterator.Items(ctx) {
					if strings.EqualFold(child.ResourceType, api.ClusterControllerResourceType.String()) {
						controllerCount++
					}
				}
				require.NoError(t, childIterator.GetError())
				assert.Equal(t, 1, controllerCount, "expected controller child to remain")
			},
		},
		{
			name:            "when there are controller and non-controller children it deletes only non-controller children",
			existingCluster: newTestClusterWithNewDeletionApproach(t, readyToDeleteClusterOptsFunc),
			childResources:  []any{newTestClusterScopedManagementClusterContent("test-mcc"), newTestClusterController("test-controller")},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, _ *databasetesting.MockKubeApplierDBClients) {
				cluster := newTestClusterWithNewDeletionApproach(t, nil)
				untypedCRUD, err := db.UntypedCRUD(*cluster.ID)
				require.NoError(t, err)
				childIterator, err := untypedCRUD.ListRecursive(ctx, nil)
				require.NoError(t, err)

				var remainingCount int
				var controllerCount int
				for _, child := range childIterator.Items(ctx) {
					remainingCount++
					if strings.EqualFold(child.ResourceType, api.ClusterControllerResourceType.String()) {
						controllerCount++
					}
				}
				require.NoError(t, childIterator.GetError())
				assert.Equal(t, 1, remainingCount, "expected only controller child to remain")
				assert.Equal(t, 1, controllerCount, "expected the remaining child to be a controller")
			},
		},
		{
			name: "when the cluster is not found performs a no-op",
		},
		{
			name:            "when there is a child ServiceProviderCluster without Maestro bundles it deletes it",
			existingCluster: newTestClusterWithNewDeletionApproach(t, readyToDeleteClusterOptsFunc),
			childResources:  []any{newTestSPC(t, nil)},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, _ *databasetesting.MockKubeApplierDBClients) {
				spcCRUD := db.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName)
				_, err := spcCRUD.Get(ctx, api.ServiceProviderClusterResourceName)
				require.True(t, database.IsNotFoundError(err), "expected SPC to be deleted")
			},
		},
		{
			name:            "when SPC has kube-applier desires it deletes desires then deletes SPC",
			existingCluster: newTestClusterWithNewDeletionApproach(t, readyToDeleteClusterOptsFunc),
			childResources: []any{
				newTestSPCWithManagementCluster(managementClusterResourceID),
			},
			kubeApplierDesires: []any{newTestClusterScopedReadDesire("readonly-hostedcluster")},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, kubeApplierDBClients *databasetesting.MockKubeApplierDBClients) {
				spcCRUD := db.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName)
				_, err := spcCRUD.Get(ctx, api.ServiceProviderClusterResourceName)
				require.True(t, database.IsNotFoundError(err), "expected SPC to be deleted")

				assertNoClusterScopedKubeApplierResources(t, ctx, kubeApplierDBClients)
			},
		},
		{
			name:            "when cluster has cluster and nodepool scoped kube-applier resources it deletes only cluster scoped ones",
			existingCluster: newTestClusterWithNewDeletionApproach(t, readyToDeleteClusterOptsFunc),
			childResources: []any{
				newTestSPCWithManagementCluster(managementClusterResourceID),
			},
			kubeApplierDesires: []any{
				newTestClusterScopedReadDesire("readonly-hostedcluster"),
				newTestClusterScopedApplyDesire("apply-example"),
				newTestNodePoolScopedReadDesire("workers", "readonly-nodepool"),
				newTestNodePoolScopedDeleteDesire("workers", "delete-example"),
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, kubeApplierDBClients *databasetesting.MockKubeApplierDBClients) {
				spcCRUD := db.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName)
				_, err := spcCRUD.Get(ctx, api.ServiceProviderClusterResourceName)
				require.True(t, database.IsNotFoundError(err), "expected SPC to be deleted")

				assertNoClusterScopedKubeApplierResources(t, ctx, kubeApplierDBClients)
				assertClusterScopedKubeApplierResourceExists(t, ctx, kubeApplierDBClients,
					kubeapplier.ToNodePoolScopedReadDesireResourceIDString(
						testSubscriptionID, testResourceGroupName, testClusterName, "workers", "readonly-nodepool"))
				assertClusterScopedKubeApplierResourceExists(t, ctx, kubeApplierDBClients,
					kubeapplier.ToNodePoolScopedDeleteDesireResourceIDString(
						testSubscriptionID, testResourceGroupName, testClusterName, "workers", "delete-example"))
			},
		},
		{
			name:            "when SPC has kube-applier desires but no kube-applier client it deletes SPC best-effort",
			existingCluster: newTestClusterWithNewDeletionApproach(t, readyToDeleteClusterOptsFunc),
			childResources: []any{
				newTestSPCWithManagementCluster(unregisteredManagementClusterResourceID),
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, kubeApplierDBClients *databasetesting.MockKubeApplierDBClients) {
				spcCRUD := db.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName)
				_, err := spcCRUD.Get(ctx, api.ServiceProviderClusterResourceName)
				require.True(t, database.IsNotFoundError(err), "expected SPC to be deleted")

				require.Nil(t, kubeApplierDBClients.For(ctx, unregisteredManagementClusterResourceID))
			},
		},
		{
			name:            "when there is a child ServiceProviderCluster with Maestro bundles it does not delete it",
			existingCluster: newTestClusterWithNewDeletionApproach(t, readyToDeleteClusterOptsFunc),
			childResources: []any{newTestSPC(t, api.MaestroBundleReferenceList{
				{Name: "bundle-a", MaestroAPIMaestroBundleName: "name-a", MaestroAPIMaestroBundleID: "id-a"},
			})},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, _ *databasetesting.MockKubeApplierDBClients) {
				spcCRUD := db.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName)
				_, err := spcCRUD.Get(ctx, api.ServiceProviderClusterResourceName)
				require.NoError(t, err, "expected SPC to still exist")
			},
		},
		{
			name:            "when there are children including SPC with Maestro bundles it deletes all except SPC",
			existingCluster: newTestClusterWithNewDeletionApproach(t, readyToDeleteClusterOptsFunc),
			childResources: []any{
				newTestClusterScopedManagementClusterContent("gate-mcc"),
				newTestSPC(t, api.MaestroBundleReferenceList{
					{Name: "bundle-a", MaestroAPIMaestroBundleName: "name-a", MaestroAPIMaestroBundleID: "id-a"},
				}),
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, _ *databasetesting.MockKubeApplierDBClients) {
				mccCRUD := db.HCPClusters(testSubscriptionID, testResourceGroupName).ManagementClusterContents(testClusterName)
				_, err := mccCRUD.Get(ctx, "gate-mcc")
				require.True(t, database.IsNotFoundError(err), "expected MCC to be deleted")

				spcCRUD := db.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName)
				_, err = spcCRUD.Get(ctx, api.ServiceProviderClusterResourceName)
				require.NoError(t, err, "expected SPC to still exist")
			},
		},
		{
			name:            "orphaned nodepool-subtree resource is skipped",
			existingCluster: newTestClusterWithNewDeletionApproach(t, readyToDeleteClusterOptsFunc),
			childResources:  []any{newTestNodePoolController(t, "orphaned-np-controller")},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, _ *databasetesting.MockKubeApplierDBClients) {
				cluster := newTestClusterWithNewDeletionApproach(t, nil)
				untypedCRUD, err := db.UntypedCRUD(*cluster.ID)
				require.NoError(t, err)
				childIterator, err := untypedCRUD.ListRecursive(ctx, nil)
				require.NoError(t, err)

				var remainingCount int
				for range childIterator.Items(ctx) {
					remainingCount++
				}
				require.NoError(t, childIterator.GetError())
				assert.Equal(t, 1, remainingCount, "expected orphaned nodepool-subtree resource to remain")
			},
		},
		{
			name:            "orphaned externalauth-subtree resource is skipped",
			existingCluster: newTestClusterWithNewDeletionApproach(t, readyToDeleteClusterOptsFunc),
			childResources:  []any{newTestExternalAuthController(t, "orphaned-ea-controller")},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, _ *databasetesting.MockKubeApplierDBClients) {
				cluster := newTestClusterWithNewDeletionApproach(t, nil)
				untypedCRUD, err := db.UntypedCRUD(*cluster.ID)
				require.NoError(t, err)
				childIterator, err := untypedCRUD.ListRecursive(ctx, nil)
				require.NoError(t, err)

				var remainingCount int
				for range childIterator.Items(ctx) {
					remainingCount++
				}
				require.NoError(t, childIterator.GetError())
				assert.Equal(t, 1, remainingCount, "expected orphaned externalauth-subtree resource to remain")
			},
		},
		{
			name:            "deletable MCC is deleted while orphaned nodepool-subtree resource is skipped",
			existingCluster: newTestClusterWithNewDeletionApproach(t, readyToDeleteClusterOptsFunc),
			childResources:  []any{newTestClusterScopedManagementClusterContent("test-mcc"), newTestNodePoolController(t, "orphaned-np-controller")},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, _ *databasetesting.MockKubeApplierDBClients) {
				mccCRUD := db.HCPClusters(testSubscriptionID, testResourceGroupName).ManagementClusterContents(testClusterName)
				_, err := mccCRUD.Get(ctx, "test-mcc")
				require.True(t, database.IsNotFoundError(err), "expected MCC to be deleted")

				cluster := newTestClusterWithNewDeletionApproach(t, nil)
				untypedCRUD, err := db.UntypedCRUD(*cluster.ID)
				require.NoError(t, err)
				childIterator, err := untypedCRUD.ListRecursive(ctx, nil)
				require.NoError(t, err)

				var remainingCount int
				for range childIterator.Items(ctx) {
					remainingCount++
				}
				require.NoError(t, childIterator.GetError())
				assert.Equal(t, 1, remainingCount, "expected only orphaned nodepool-subtree resource to remain")
			},
		},
		{
			name:            "blocks when nodepools still exist",
			existingCluster: newTestClusterWithNewDeletionApproach(t, readyToDeleteClusterOptsFunc),
			childResources:  []any{newTestNodePool(t), newTestClusterScopedManagementClusterContent("untouched-mcc")},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, _ *databasetesting.MockKubeApplierDBClients) {
				mccCRUD := db.HCPClusters(testSubscriptionID, testResourceGroupName).ManagementClusterContents(testClusterName)
				_, err := mccCRUD.Get(ctx, "untouched-mcc")
				require.NoError(t, err, "expected child resource to still exist")
			},
		},
		{
			name:            "blocks when external auths still exist",
			existingCluster: newTestClusterWithNewDeletionApproach(t, readyToDeleteClusterOptsFunc),
			childResources:  []any{newTestExternalAuth(t), newTestClusterScopedManagementClusterContent("untouched-mcc")},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, _ *databasetesting.MockKubeApplierDBClients) {
				mccCRUD := db.HCPClusters(testSubscriptionID, testResourceGroupName).ManagementClusterContents(testClusterName)
				_, err := mccCRUD.Get(ctx, "untouched-mcc")
				require.NoError(t, err, "expected child resource to still exist")
			},
		},
		{
			name:            "UsesNewClusterDeletionApproach false -- no-op even when all cleanup conditions met and children exist",
			existingCluster: newTestClusterWithOldDeletionApproach(t, readyToDeleteClusterOptsFunc),
			childResources:  []any{newTestClusterScopedManagementClusterContent("untouched-mcc"), newTestSPC(t, nil)},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, _ *databasetesting.MockKubeApplierDBClients) {
				mccCRUD := db.HCPClusters(testSubscriptionID, testResourceGroupName).ManagementClusterContents(testClusterName)
				_, err := mccCRUD.Get(ctx, "untouched-mcc")
				require.NoError(t, err, "expected child resource to still exist")

				spcCRUD := db.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName)
				_, err = spcCRUD.Get(ctx, api.ServiceProviderClusterResourceName)
				require.NoError(t, err, "expected SPC to still exist")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

			resources := []any{}
			if tc.existingCluster != nil {
				resources = append(resources, tc.existingCluster)
			}
			resources = append(resources, tc.childResources...)
			mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			clustersForLister := []*api.HCPOpenShiftCluster{}
			if tc.existingCluster != nil {
				clustersForLister = append(clustersForLister, tc.existingCluster)
			}

			mockKubeApplierDBClients := databasetesting.NewMockKubeApplierDBClients()
			mockKubeApplierClient, err := databasetesting.NewMockKubeApplierDBClientWithResources(ctx, tc.kubeApplierDesires)
			require.NoError(t, err)
			mockKubeApplierDBClients.Register(managementClusterResourceID, mockKubeApplierClient)

			syncer := &clusterChildResourcesCleanupController{
				cooldownChecker:      &alwaysSyncCooldownChecker{},
				clusterLister:        &listertesting.SliceClusterLister{Clusters: clustersForLister},
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

func TestIsUnderSkippedSubtree(t *testing.T) {
	skipSubtreeTypes := []azcorearm.ResourceType{
		api.NodePoolResourceType,
		api.ExternalAuthResourceType,
	}

	testCases := []struct {
		name       string
		resourceID string
		want       bool
	}{
		{
			name:       "cluster itself is not under a skipped subtree",
			resourceID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster",
			want:       false,
		},
		{
			name:       "service provider cluster is not under a skipped subtree",
			resourceID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/serviceProviderClusters/default",
			want:       false,
		},
		{
			name:       "cluster controller is not under a skipped subtree",
			resourceID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/controllers/SomeController",
			want:       false,
		},
		{
			name:       "nodepool is under a skipped subtree",
			resourceID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/nodePools/np1",
			want:       true,
		},
		{
			name:       "service provider nodepool is a descendant of a skipped subtree",
			resourceID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/nodePools/np1/serviceProviderNodePools/default",
			want:       true,
		},
		{
			name:       "nodepool controller is a descendant of a skipped subtree",
			resourceID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/nodePools/np1/controllers/SomeController",
			want:       true,
		},
		{
			name:       "externalauth is under a skipped subtree",
			resourceID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/externalAuths/auth1",
			want:       true,
		},
		{
			name:       "externalauth controller is a descendant of a skipped subtree",
			resourceID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/externalAuths/auth1/controllers/SomeController",
			want:       true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			resourceID := api.Must(azcorearm.ParseResourceID(tc.resourceID))
			got := hasSkippedResourceTypePrefix(resourceID, skipSubtreeTypes)
			assert.Equal(t, tc.want, got)
		})
	}
}
