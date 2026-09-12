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
	"github.com/Azure/ARO-HCP/internal/backup"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/kubeappliercosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestClusterChildResourcesCleanupController_SyncOnce(t *testing.T) {
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
	newTestClusterScopedManagementClusterContent := func(name string) *coreapi.ManagementClusterContent {
		resourceID := metadataapi.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + testSubscriptionID +
				"/resourceGroups/" + testResourceGroupName +
				"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
				"/managementClusterContents/" + name))
		return &coreapi.ManagementClusterContent{
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID:   resourceID,
				PartitionKey: strings.ToLower(resourceID.SubscriptionID),
			},
		}
	}
	newTestClusterController := func(name string) *coreapi.Controller {
		resourceID := metadataapi.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + testSubscriptionID +
				"/resourceGroups/" + testResourceGroupName +
				"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
				"/hcpOpenShiftControllers/" + name))
		return &coreapi.Controller{
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID:   resourceID,
				PartitionKey: strings.ToLower(resourceID.SubscriptionID),
			},
			ExternalID: metadataapi.Must(azcorearm.ParseResourceID(
				"/subscriptions/" + testSubscriptionID +
					"/resourceGroups/" + testResourceGroupName +
					"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName)),
			Status: coreapi.ControllerStatus{
				Conditions: []metav1.Condition{},
			},
		}
	}

	newTestReadDesire := func(resourceIDString string) *kubeapplierapi.ReadDesire {
		resourceID := metadataapi.Must(azcorearm.ParseResourceID(resourceIDString))
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
		return newTestReadDesire(kubeapplierapi.ToClusterScopedReadDesireResourceIDString(
			testSubscriptionID, testResourceGroupName, testClusterName, name))
	}
	newTestNodePoolScopedReadDesire := func(nodePoolName, name string) *kubeapplierapi.ReadDesire {
		return newTestReadDesire(kubeapplierapi.ToNodePoolScopedReadDesireResourceIDString(
			testSubscriptionID, testResourceGroupName, testClusterName, nodePoolName, name))
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
	newTestBackupApplyDesire := func(name string) *kubeapplierapi.ApplyDesire {
		applyDesire := newTestClusterScopedApplyDesire(name)
		applyDesire.Spec.Type = kubeapplierapi.ApplyDesireTypeServerSideApply
		applyDesire.Spec.TargetItem = kubeapplierapi.ResourceReference{
			Group: "velero.io", Version: "v1", Resource: "schedules",
			Namespace: "velero", Name: name,
		}
		return applyDesire
	}
	assertNoClusterScopedKubeApplierResources := func(
		t *testing.T,
		ctx context.Context,
		kubeApplierDBClients *kubeappliercosmosstoragetesting.MockKubeApplierDBClients,
	) {
		t.Helper()
		client := kubeApplierDBClients.For(ctx, managementClusterResourceID)
		require.NotNil(t, client)

		clusterResourceID := metadataapi.Must(azcorearm.ParseResourceID(
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
	readyToDeleteClusterOptsFunc := func(c *coreapi.HCPOpenShiftCluster) {
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
		existingCluster    *coreapi.HCPOpenShiftCluster
		childResources     []any
		kubeApplierDesires []any
		wantErr            bool
		verifyDB           func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, kubeApplierDBClients *kubeappliercosmosstoragetesting.MockKubeApplierDBClients,
		)
	}{
		{
			name:            "when no DeletionTimestamp is set performs a no-op",
			existingCluster: newTestClusterWithNewDeletionApproach(t, nil),
			childResources:  []any{newTestClusterScopedManagementClusterContent("untouched-mcc")},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, _ *kubeappliercosmosstoragetesting.MockKubeApplierDBClients) {
				mccCRUD := db.HCPClusters(testSubscriptionID, testResourceGroupName).ManagementClusterContents(testClusterName)
				_, err := mccCRUD.Get(ctx, "untouched-mcc")
				require.NoError(t, err, "expected child resource to still exist")
			},
		},
		{
			name: "when no ClusterServiceDeletionTimestamp is set performs a no-op",
			existingCluster: newTestClusterWithNewDeletionApproach(t, func(c *coreapi.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-time.Hour)}
				c.ServiceProviderProperties.ClusterServiceDeletionTimestamp = nil
				c.ServiceProviderProperties.ClusterServiceID = nil
			}),
			childResources: []any{newTestClusterScopedManagementClusterContent("untouched-mcc")},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, _ *kubeappliercosmosstoragetesting.MockKubeApplierDBClients) {
				mccCRUD := db.HCPClusters(testSubscriptionID, testResourceGroupName).ManagementClusterContents(testClusterName)
				_, err := mccCRUD.Get(ctx, "untouched-mcc")
				require.NoError(t, err, "expected child resource to still exist")
			},
		},
		{
			name: "when ClusterServiceID is set performs a no-op",
			existingCluster: newTestClusterWithNewDeletionApproach(t, func(c *coreapi.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-time.Hour)}
				c.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-30 * time.Minute)}
			}),
			childResources: []any{newTestClusterScopedManagementClusterContent("untouched-mcc")},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, _ *kubeappliercosmosstoragetesting.MockKubeApplierDBClients) {
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
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, _ *kubeappliercosmosstoragetesting.MockKubeApplierDBClients) {
				mccCRUD := db.HCPClusters(testSubscriptionID, testResourceGroupName).ManagementClusterContents(testClusterName)
				_, err := mccCRUD.Get(ctx, "test-mcc")
				require.True(t, cosmosstorageutils.IsNotFoundError(err), "expected MCC to be deleted")
			},
		},
		{
			name:            "deletion of cluster controllers is skipped",
			existingCluster: newTestClusterWithNewDeletionApproach(t, readyToDeleteClusterOptsFunc),
			childResources:  []any{newTestClusterController("test-controller")},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, _ *kubeappliercosmosstoragetesting.MockKubeApplierDBClients) {
				cluster := newTestClusterWithNewDeletionApproach(t, nil)
				untypedCRUD, err := db.UntypedCRUD(*cluster.ID)
				require.NoError(t, err)
				childIterator, err := untypedCRUD.ListRecursive(ctx, nil)
				require.NoError(t, err)

				var controllerCount int
				for _, child := range childIterator.Items(ctx) {
					if strings.EqualFold(child.ResourceType, coreapi.ClusterControllerResourceType.String()) {
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
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, _ *kubeappliercosmosstoragetesting.MockKubeApplierDBClients) {
				cluster := newTestClusterWithNewDeletionApproach(t, nil)
				untypedCRUD, err := db.UntypedCRUD(*cluster.ID)
				require.NoError(t, err)
				childIterator, err := untypedCRUD.ListRecursive(ctx, nil)
				require.NoError(t, err)

				var remainingCount int
				var controllerCount int
				for _, child := range childIterator.Items(ctx) {
					remainingCount++
					if strings.EqualFold(child.ResourceType, coreapi.ClusterControllerResourceType.String()) {
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
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, _ *kubeappliercosmosstoragetesting.MockKubeApplierDBClients) {
				spcCRUD := db.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName)
				_, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.True(t, cosmosstorageutils.IsNotFoundError(err), "expected SPC to be deleted")
			},
		},
		{
			name:            "when SPC has kube-applier desires it deletes desires then deletes SPC",
			existingCluster: newTestClusterWithNewDeletionApproach(t, readyToDeleteClusterOptsFunc),
			childResources: []any{
				newTestSPCWithManagementCluster(managementClusterResourceID),
			},
			kubeApplierDesires: []any{newTestClusterScopedReadDesire("readonly-hostedcluster")},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, kubeApplierDBClients *kubeappliercosmosstoragetesting.MockKubeApplierDBClients) {
				spcCRUD := db.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName)
				_, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.True(t, cosmosstorageutils.IsNotFoundError(err), "expected SPC to be deleted")

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
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, kubeApplierDBClients *kubeappliercosmosstoragetesting.MockKubeApplierDBClients) {
				spcCRUD := db.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName)
				_, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.True(t, cosmosstorageutils.IsNotFoundError(err), "expected SPC to be deleted")

				assertNoClusterScopedKubeApplierResources(t, ctx, kubeApplierDBClients)
				assertClusterScopedKubeApplierResourceExists(t, ctx, kubeApplierDBClients,
					kubeapplierapi.ToNodePoolScopedReadDesireResourceIDString(
						testSubscriptionID, testResourceGroupName, testClusterName, "workers", "readonly-nodepool"))
			},
		},
		{
			name:            "when SPC has kube-applier desires but no kube-applier client it deletes SPC best-effort",
			existingCluster: newTestClusterWithNewDeletionApproach(t, readyToDeleteClusterOptsFunc),
			childResources: []any{
				newTestSPCWithManagementCluster(unregisteredManagementClusterResourceID),
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, kubeApplierDBClients *kubeappliercosmosstoragetesting.MockKubeApplierDBClients) {
				spcCRUD := db.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName)
				_, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.True(t, cosmosstorageutils.IsNotFoundError(err), "expected SPC to be deleted")

				require.Nil(t, kubeApplierDBClients.For(ctx, unregisteredManagementClusterResourceID))
			},
		},
		{
			name:            "when there is a child ServiceProviderCluster with Maestro bundles it does not delete it",
			existingCluster: newTestClusterWithNewDeletionApproach(t, readyToDeleteClusterOptsFunc),
			childResources: []any{newTestSPC(t, coreapi.MaestroBundleReferenceList{
				{Name: "bundle-a", MaestroAPIMaestroBundleName: "name-a", MaestroAPIMaestroBundleID: "id-a"},
			})},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, _ *kubeappliercosmosstoragetesting.MockKubeApplierDBClients) {
				spcCRUD := db.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName)
				_, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.NoError(t, err, "expected SPC to still exist")
			},
		},
		{
			name:            "when there are children including SPC with Maestro bundles it deletes all except SPC",
			existingCluster: newTestClusterWithNewDeletionApproach(t, readyToDeleteClusterOptsFunc),
			childResources: []any{
				newTestClusterScopedManagementClusterContent("gate-mcc"),
				newTestSPC(t, coreapi.MaestroBundleReferenceList{
					{Name: "bundle-a", MaestroAPIMaestroBundleName: "name-a", MaestroAPIMaestroBundleID: "id-a"},
				}),
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, _ *kubeappliercosmosstoragetesting.MockKubeApplierDBClients) {
				mccCRUD := db.HCPClusters(testSubscriptionID, testResourceGroupName).ManagementClusterContents(testClusterName)
				_, err := mccCRUD.Get(ctx, "gate-mcc")
				require.True(t, cosmosstorageutils.IsNotFoundError(err), "expected MCC to be deleted")

				spcCRUD := db.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName)
				_, err = spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.NoError(t, err, "expected SPC to still exist")
			},
		},
		{
			name:            "orphaned nodepool-subtree resource is skipped",
			existingCluster: newTestClusterWithNewDeletionApproach(t, readyToDeleteClusterOptsFunc),
			childResources:  []any{newTestNodePoolController(t, "orphaned-np-controller")},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, _ *kubeappliercosmosstoragetesting.MockKubeApplierDBClients) {
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
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, _ *kubeappliercosmosstoragetesting.MockKubeApplierDBClients) {
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
			name:            "orphaned credential-request-subtree resource is skipped",
			existingCluster: newTestClusterWithNewDeletionApproach(t, readyToDeleteClusterOptsFunc),
			childResources:  []any{newTestCredentialRequestController(t, "orphaned-cred-controller")},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, _ *kubeappliercosmosstoragetesting.MockKubeApplierDBClients) {
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
				assert.Equal(t, 1, remainingCount, "expected orphaned credential-request-subtree resource to remain")
			},
		},
		{
			name:            "orphaned credential-revocation-subtree resource is skipped",
			existingCluster: newTestClusterWithNewDeletionApproach(t, readyToDeleteClusterOptsFunc),
			childResources:  []any{newTestCredentialRevocationController(t, "orphaned-rev-controller")},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, _ *kubeappliercosmosstoragetesting.MockKubeApplierDBClients) {
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
				assert.Equal(t, 1, remainingCount, "expected orphaned credential-revocation-subtree resource to remain")
			},
		},
		{
			name:            "deletable MCC is deleted while orphaned nodepool-subtree resource is skipped",
			existingCluster: newTestClusterWithNewDeletionApproach(t, readyToDeleteClusterOptsFunc),
			childResources:  []any{newTestClusterScopedManagementClusterContent("test-mcc"), newTestNodePoolController(t, "orphaned-np-controller")},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, _ *kubeappliercosmosstoragetesting.MockKubeApplierDBClients) {
				mccCRUD := db.HCPClusters(testSubscriptionID, testResourceGroupName).ManagementClusterContents(testClusterName)
				_, err := mccCRUD.Get(ctx, "test-mcc")
				require.True(t, cosmosstorageutils.IsNotFoundError(err), "expected MCC to be deleted")

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
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, _ *kubeappliercosmosstoragetesting.MockKubeApplierDBClients) {
				mccCRUD := db.HCPClusters(testSubscriptionID, testResourceGroupName).ManagementClusterContents(testClusterName)
				_, err := mccCRUD.Get(ctx, "untouched-mcc")
				require.NoError(t, err, "expected child resource to still exist")
			},
		},
		{
			name:            "blocks when external auths still exist",
			existingCluster: newTestClusterWithNewDeletionApproach(t, readyToDeleteClusterOptsFunc),
			childResources:  []any{newTestExternalAuth(t), newTestClusterScopedManagementClusterContent("untouched-mcc")},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, _ *kubeappliercosmosstoragetesting.MockKubeApplierDBClients) {
				mccCRUD := db.HCPClusters(testSubscriptionID, testResourceGroupName).ManagementClusterContents(testClusterName)
				_, err := mccCRUD.Get(ctx, "untouched-mcc")
				require.NoError(t, err, "expected child resource to still exist")
			},
		},
		{
			name:            "blocks when credential requests still exist",
			existingCluster: newTestClusterWithNewDeletionApproach(t, readyToDeleteClusterOptsFunc),
			childResources:  []any{newTestCredentialRequest(t, "cred-1"), newTestClusterScopedManagementClusterContent("untouched-mcc")},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, _ *kubeappliercosmosstoragetesting.MockKubeApplierDBClients) {
				mccCRUD := db.HCPClusters(testSubscriptionID, testResourceGroupName).ManagementClusterContents(testClusterName)
				_, err := mccCRUD.Get(ctx, "untouched-mcc")
				require.NoError(t, err, "expected child resource to still exist")
			},
		},
		{
			name:            "blocks when credential revocations still exist",
			existingCluster: newTestClusterWithNewDeletionApproach(t, readyToDeleteClusterOptsFunc),
			childResources:  []any{newTestCredentialRevocation(t, "revoke-1"), newTestClusterScopedManagementClusterContent("untouched-mcc")},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, _ *kubeappliercosmosstoragetesting.MockKubeApplierDBClients) {
				mccCRUD := db.HCPClusters(testSubscriptionID, testResourceGroupName).ManagementClusterContents(testClusterName)
				_, err := mccCRUD.Get(ctx, "untouched-mcc")
				require.NoError(t, err, "expected child resource to still exist")
			},
		},
		{
			name:            "UsesNewClusterDeletionApproach false -- no-op even when all cleanup conditions met and children exist",
			existingCluster: newTestClusterWithOldDeletionApproach(t, readyToDeleteClusterOptsFunc),
			childResources:  []any{newTestClusterScopedManagementClusterContent("untouched-mcc"), newTestSPC(t, nil)},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, _ *kubeappliercosmosstoragetesting.MockKubeApplierDBClients) {
				mccCRUD := db.HCPClusters(testSubscriptionID, testResourceGroupName).ManagementClusterContents(testClusterName)
				_, err := mccCRUD.Get(ctx, "untouched-mcc")
				require.NoError(t, err, "expected child resource to still exist")

				spcCRUD := db.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName)
				_, err = spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.NoError(t, err, "expected SPC to still exist")
			},
		},
		{
			name:            "backup *Desires are skipped by the general sweep",
			existingCluster: newTestClusterWithNewDeletionApproach(t, readyToDeleteClusterOptsFunc),
			childResources: []any{
				newTestSPCWithManagementCluster(managementClusterResourceID),
			},
			kubeApplierDesires: []any{
				newTestBackupApplyDesire(backup.BackupScheduleDesireNamePrefix + "hourly"),
				newTestClusterScopedReadDesire(backup.BackupScheduleDesireNamePrefix + "hourly"),
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, kubeApplierDBClients *kubeappliercosmosstoragetesting.MockKubeApplierDBClients) {
				client := kubeApplierDBClients.For(ctx, managementClusterResourceID)
				require.NotNil(t, client)

				applyDesireCRUD, err := client.ApplyDesiresForCluster(testSubscriptionID, testResourceGroupName, testClusterName)
				require.NoError(t, err)
				applyDesire, err := applyDesireCRUD.Get(ctx, backup.BackupScheduleDesireNamePrefix+"hourly")
				require.NoError(t, err, "backup ApplyDesire should still exist (skipped by general sweep)")
				assert.Equal(t, kubeapplierapi.ApplyDesireTypeServerSideApply, applyDesire.Spec.Type, "backup ApplyDesire should not be converted")

				readDesireCRUD, err := client.ReadDesiresForCluster(testSubscriptionID, testResourceGroupName, testClusterName)
				require.NoError(t, err)
				readDesire, err := readDesireCRUD.Get(ctx, backup.BackupScheduleDesireNamePrefix+"hourly")
				require.NoError(t, err)
				assert.NotEmpty(t, readDesire)

				serviceProviderClusterCRUD := db.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName)
				_, err = serviceProviderClusterCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.NoError(t, err, "serviceProviderCluster should still exist (backup ApplyDesire remains)")
			},
		},
		{
			name:            "non-backup ApplyDesires are still swept normally",
			existingCluster: newTestClusterWithNewDeletionApproach(t, readyToDeleteClusterOptsFunc),
			childResources: []any{
				newTestSPCWithManagementCluster(managementClusterResourceID),
			},
			kubeApplierDesires: []any{
				newTestClusterScopedApplyDesire("non-backup-desire"),
				newTestClusterScopedReadDesire("non-backup-desire"),
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, kubeApplierDBClients *kubeappliercosmosstoragetesting.MockKubeApplierDBClients) {
				assertNoClusterScopedKubeApplierResources(t, ctx, kubeApplierDBClients)

				serviceProviderCRUD := db.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName)
				_, err := serviceProviderCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.True(t, cosmosstorageutils.IsNotFoundError(err), "SPC should be deleted")
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
			mockResourcesDBClient, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			clustersForLister := []*coreapi.HCPOpenShiftCluster{}
			if tc.existingCluster != nil {
				clustersForLister = append(clustersForLister, tc.existingCluster)
			}

			mockKubeApplierDBClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
			mockKubeApplierClient, err := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClientWithResources(ctx, tc.kubeApplierDesires)
			require.NoError(t, err)
			mockKubeApplierDBClients.Register(managementClusterResourceID, mockKubeApplierClient)

			syncer := &clusterChildResourcesCleanupController{
				clusterLister:        &corelistertesting.SliceClusterLister{Clusters: clustersForLister},
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
		coreapi.NodePoolResourceType,
		coreapi.ExternalAuthResourceType,
		coreapi.SystemAdminCredentialRequestResourceType,
		coreapi.SystemAdminCredentialRevocationResourceType,
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
		{
			name:       "credential request is under a skipped subtree",
			resourceID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/systemAdminCredentialRequests/cred1",
			want:       true,
		},
		{
			name:       "credential request controller is a descendant of a skipped subtree",
			resourceID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/systemAdminCredentialRequests/cred1/hcpOpenShiftControllers/SomeController",
			want:       true,
		},
		{
			name:       "credential revocation is under a skipped subtree",
			resourceID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/systemAdminCredentialRevocations/rev1",
			want:       true,
		},
		{
			name:       "credential revocation controller is a descendant of a skipped subtree",
			resourceID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/systemAdminCredentialRevocations/rev1/hcpOpenShiftControllers/SomeController",
			want:       true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			resourceID := metadataapi.Must(azcorearm.ParseResourceID(tc.resourceID))
			got := hasSkippedResourceTypePrefix(resourceID, skipSubtreeTypes)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestClusterChildResourcesCleanupController_remainingApplyDesires(t *testing.T) {
	managementClusterResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/providers/microsoft.redhatopenshift/stamps/1/managementclusters/default"))
	unregisteredManagementClusterResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/providers/microsoft.redhatopenshift/stamps/1/managementclusters/unregistered"))

	newSPC := func(mc *azcorearm.ResourceID) *coreapi.ServiceProviderCluster {
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
				ManagementClusterResourceID: mc,
			},
		}
	}
	newApplyDesire := func(name string, tags map[string]string) *kubeapplierapi.ApplyDesire {
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
			Tags: tags,
		}
	}
	taggedDesire := func(name, controllerName string) *kubeapplierapi.ApplyDesire {
		return newApplyDesire(name, map[string]string{kubeapplierapi.TagControllerName: controllerName})
	}
	untaggedDesire := func(name string) *kubeapplierapi.ApplyDesire {
		return newApplyDesire(name, nil)
	}

	testCases := []struct {
		name               string
		spc                *coreapi.ServiceProviderCluster
		kubeApplierDesires []any
		wantTotal          int
		wantBreakdown      string
	}{
		{
			name:               "tagged ApplyDesire present -> counted by controller",
			spc:                newSPC(managementClusterResourceID),
			kubeApplierDesires: []any{taggedDesire("desire-a", "test-controller")},
			wantTotal:          1,
			wantBreakdown:      "1 for controller test-controller",
		},
		{
			name:               "untagged ApplyDesire present -> counted as unknown",
			spc:                newSPC(managementClusterResourceID),
			kubeApplierDesires: []any{untaggedDesire("desire-a")},
			wantTotal:          1,
			wantBreakdown:      "1 for controller unknown",
		},
		{
			// Breakdown is sorted by controller NAME, not by count: "aaa-controller"
			// has more desires than the "unknown" bucket yet still sorts first. This
			// would fail if the formatted "%d for controller %s" strings were sorted
			// (that orders by leading count).
			name: "multiple controllers -> breakdown sorted by controller name, not count",
			spc:  newSPC(managementClusterResourceID),
			kubeApplierDesires: []any{
				taggedDesire("desire-a", "aaa-controller"),
				taggedDesire("desire-b", "aaa-controller"),
				untaggedDesire("desire-c"),
			},
			wantTotal:     3,
			wantBreakdown: "2 for controller aaa-controller, 1 for controller unknown",
		},
		{
			name:          "no ApplyDesires -> none remaining",
			spc:           newSPC(managementClusterResourceID),
			wantTotal:     0,
			wantBreakdown: "",
		},
		{
			name:          "nil management cluster resource ID -> none remaining",
			spc:           newSPC(nil),
			wantTotal:     0,
			wantBreakdown: "",
		},
		{
			name:               "unregistered management cluster (nil client) -> none remaining",
			spc:                newSPC(unregisteredManagementClusterResourceID),
			kubeApplierDesires: []any{taggedDesire("desire-a", "test-controller")},
			wantTotal:          0,
			wantBreakdown:      "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

			mockKubeApplierDBClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
			mockKubeApplierClient, err := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClientWithResources(ctx, tc.kubeApplierDesires)
			require.NoError(t, err)
			mockKubeApplierDBClients.Register(managementClusterResourceID, mockKubeApplierClient)

			syncer := &clusterChildResourcesCleanupController{
				kubeApplierDBClients: mockKubeApplierDBClients,
			}

			total, breakdown, err := syncer.remainingApplyDesires(ctx, tc.spc, testSubscriptionID, testResourceGroupName, testClusterName)
			require.NoError(t, err)
			assert.Equal(t, tc.wantTotal, total)
			assert.Equal(t, tc.wantBreakdown, breakdown)
		})
	}
}

func TestClusterChildResourcesCleanupController_extraDeleteGate_ApplyDesires(t *testing.T) {
	managementClusterResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/providers/microsoft.redhatopenshift/stamps/1/managementclusters/default"))

	spcResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
			"/serviceProviderClusters/default"))

	newSPC := func() *coreapi.ServiceProviderCluster {
		return &coreapi.ServiceProviderCluster{
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID:   spcResourceID,
				PartitionKey: strings.ToLower(spcResourceID.SubscriptionID),
			},
			Status: coreapi.ServiceProviderClusterStatus{
				ManagementClusterResourceID: managementClusterResourceID,
			},
		}
	}
	newApplyDesire := func(name string, tags map[string]string) *kubeapplierapi.ApplyDesire {
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
			Tags: tags,
		}
	}

	testCases := []struct {
		name               string
		kubeApplierDesires []any
		wantShouldDelete   bool
	}{
		{
			name:               "tagged ApplyDesire present -> SPC deletion blocked",
			kubeApplierDesires: []any{newApplyDesire("desire-a", map[string]string{kubeapplierapi.TagControllerName: "test-controller"})},
			wantShouldDelete:   false,
		},
		{
			name:               "untagged ApplyDesire present -> SPC deletion blocked",
			kubeApplierDesires: []any{newApplyDesire("desire-a", nil)},
			wantShouldDelete:   false,
		},
		{
			name:             "no kube-applier desires -> SPC deletion allowed",
			wantShouldDelete: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

			mockResourcesDBClient, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{newSPC()})
			require.NoError(t, err)

			mockKubeApplierDBClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
			mockKubeApplierClient, err := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClientWithResources(ctx, tc.kubeApplierDesires)
			require.NoError(t, err)
			mockKubeApplierDBClients.Register(managementClusterResourceID, mockKubeApplierClient)

			syncer := &clusterChildResourcesCleanupController{
				resourcesDBClient:    mockResourcesDBClient,
				kubeApplierDBClients: mockKubeApplierDBClients,
			}

			shouldDelete, err := syncer.extraDeleteGateShouldDeleteServiceProviderCluster(ctx, spcResourceID)
			require.NoError(t, err)
			assert.Equal(t, tc.wantShouldDelete, shouldDelete)
		})
	}
}
