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
	clocktesting "k8s.io/utils/clock/testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestClusterDeletionController_SyncOnce(t *testing.T) {
	fixedClockTime := time.Now().UTC().Truncate(time.Second)

	testKey := controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}

	readyForDeletionCluster := func(t *testing.T) *api.HCPOpenShiftCluster {
		return newTestClusterWithNewDeletionApproach(t, func(c *api.HCPOpenShiftCluster) {
			c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-time.Hour)}
			c.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-30 * time.Minute)}
			c.ServiceProviderProperties.ClusterServiceID = nil
		})
	}

	verifyClusterStillExists := func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
		t.Helper()
		_, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
		require.NoError(t, err, "expected cluster to still exist in Cosmos")
	}

	verifyClusterDeleted := func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
		t.Helper()
		_, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
		assert.True(t, database.IsNotFoundError(err), "expected cluster to be deleted from Cosmos")
	}

	testCases := []struct {
		name            string
		existingCluster *api.HCPOpenShiftCluster
		extraResources  []any
		wantErr         bool
		verifyDB        func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient)
	}{
		{
			name:            "all preconditions met, no children -- cluster is deleted",
			existingCluster: readyForDeletionCluster(t),
			verifyDB:        verifyClusterDeleted,
		},
		{
			name:            "no DeletionTimestamp -- no-op",
			existingCluster: newTestClusterWithNewDeletionApproach(t, nil),
			verifyDB:        verifyClusterStillExists,
		},
		{
			name: "DeletionTimestamp set but ClusterServiceDeletionTimestamp not -- no-op",
			existingCluster: newTestClusterWithNewDeletionApproach(t, func(c *api.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-time.Hour)}
			}),
			verifyDB: verifyClusterStillExists,
		},
		{
			name: "ClusterServiceID still set -- no-op",
			existingCluster: newTestClusterWithNewDeletionApproach(t, func(c *api.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-time.Hour)}
				c.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-30 * time.Minute)}
			}),
			verifyDB: verifyClusterStillExists,
		},
		{
			name:            "cluster still has nodepools -- blocks",
			existingCluster: readyForDeletionCluster(t),
			extraResources:  []any{newTestNodePool(t)},
			verifyDB:        verifyClusterStillExists,
		},
		{
			name:            "cluster still has external auths -- blocks",
			existingCluster: readyForDeletionCluster(t),
			extraResources:  []any{newTestExternalAuth(t)},
			verifyDB:        verifyClusterStillExists,
		},
		{
			name:            "cluster still has maestro readonly bundles -- blocks",
			existingCluster: readyForDeletionCluster(t),
			extraResources: []any{
				newTestSPC(t, api.MaestroBundleReferenceList{
					{Name: "test-bundle"},
				}),
			},
			verifyDB: verifyClusterStillExists,
		},
		{
			name:            "SPC with no bundles still present as child -- blocks",
			existingCluster: readyForDeletionCluster(t),
			extraResources:  []any{newTestSPC(t, nil)},
			verifyDB:        verifyClusterStillExists,
		},
		{
			name:            "non-controller child resource still exists -- blocks",
			existingCluster: readyForDeletionCluster(t),
			extraResources:  []any{newTestClusterScopedManagementClusterContent(t, "test-mcc")},
			verifyDB:        verifyClusterStillExists,
		},
		{
			name:            "only cluster controller children remain -- deletes cluster",
			existingCluster: readyForDeletionCluster(t),
			extraResources:  []any{newTestClusterController(t, "test-controller")},
			verifyDB:        verifyClusterDeleted,
		},
		{
			name:            "orphaned nodepool controller docs do not block deletion",
			existingCluster: readyForDeletionCluster(t),
			extraResources:  []any{newTestNodePoolController(t, "orphaned-np-controller")},
			verifyDB:        verifyClusterDeleted,
		},
		{
			name:            "orphaned externalauth controller docs do not block deletion",
			existingCluster: readyForDeletionCluster(t),
			extraResources:  []any{newTestExternalAuthController(t, "orphaned-ea-controller")},
			verifyDB:        verifyClusterDeleted,
		},
		{
			name: "feature flag false -- no-op even when all delete conditions met",
			existingCluster: newTestClusterWithOldDeletionApproach(t, func(c *api.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-time.Hour)}
				c.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-30 * time.Minute)}
				c.ServiceProviderProperties.ClusterServiceID = nil
			}),
			verifyDB: verifyClusterStillExists,
		},
		{
			name: "cluster not found -- no-op",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

			resources := []any{}
			if tc.existingCluster != nil {
				resources = append(resources, tc.existingCluster)
			}
			resources = append(resources, tc.extraResources...)

			mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			mockBillingDBClient := databasetesting.NewMockBillingDBClient()

			clustersForLister := []*api.HCPOpenShiftCluster{}
			if tc.existingCluster != nil {
				clustersForLister = append(clustersForLister, tc.existingCluster)
			}

			spcForLister := []*api.ServiceProviderCluster{}
			for _, r := range tc.extraResources {
				if spc, ok := r.(*api.ServiceProviderCluster); ok {
					spcForLister = append(spcForLister, spc)
				}
			}

			syncer := &clusterDeletionController{
				clusterLister:                &listertesting.SliceClusterLister{Clusters: clustersForLister},
				serviceProviderClusterLister: &listertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: spcForLister},
				resourcesDBClient:            mockResourcesDBClient,
				billingDBClient:              mockBillingDBClient,
				passiveClock:                 clocktesting.NewFakePassiveClock(fixedClockTime),
			}

			_, err = syncer.SyncOnce(ctx, testKey)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			if tc.verifyDB != nil {
				tc.verifyDB(t, ctx, mockResourcesDBClient)
			}
		})
	}
}

func TestClusterDeletionController_NeedsWork(t *testing.T) {
	fixedClockTime := time.Now().UTC().Truncate(time.Second)

	testCases := []struct {
		name    string
		cluster *api.HCPOpenShiftCluster
		want    bool
	}{
		{
			name:    "feature flag false",
			cluster: newTestClusterWithOldDeletionApproach(t, nil),
			want:    false,
		},
		{
			name:    "no DeletionTimestamp",
			cluster: newTestClusterWithNewDeletionApproach(t, nil),
			want:    false,
		},
		{
			name: "DeletionTimestamp set but no ClusterServiceDeletionTimestamp",
			cluster: newTestClusterWithNewDeletionApproach(t, func(c *api.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime}
			}),
			want: false,
		},
		{
			name: "both timestamps set but ClusterServiceID not nil",
			cluster: newTestClusterWithNewDeletionApproach(t, func(c *api.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime}
				c.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: fixedClockTime}
			}),
			want: false,
		},
		{
			name: "all conditions met",
			cluster: newTestClusterWithNewDeletionApproach(t, func(c *api.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime}
				c.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: fixedClockTime}
				c.ServiceProviderProperties.ClusterServiceID = nil
			}),
			want: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			controller := &clusterDeletionController{}
			assert.Equal(t, tc.want, controller.NeedsWork(tc.cluster))
		})
	}
}

func newTestNodePool(t *testing.T) *api.HCPOpenShiftClusterNodePool {
	t.Helper()
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
			"/nodePools/test-nodepool"))
	return &api.HCPOpenShiftClusterNodePool{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   resourceID,
				Name: "test-nodepool",
				Type: api.NodePoolResourceType.String(),
			},
			Location: "eastus",
		},
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		Properties: api.HCPOpenShiftClusterNodePoolProperties{
			Platform: api.NodePoolPlatformProfile{
				OSDisk: api.OSDiskProfile{
					DiskStorageAccountType: api.DiskStorageAccountTypePremium_LRS,
					DiskType:               api.OsDiskTypeManaged,
				},
			},
		},
	}
}

func newTestExternalAuth(t *testing.T) *api.HCPOpenShiftClusterExternalAuth {
	t.Helper()
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
			"/externalAuths/test-auth"))
	return &api.HCPOpenShiftClusterExternalAuth{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		ProxyResource: arm.ProxyResource{
			Resource: arm.Resource{
				ID:   resourceID,
				Name: "test-auth",
				Type: api.ExternalAuthResourceType.String(),
			},
		},
	}
}

func newTestSPC(t *testing.T, bundles api.MaestroBundleReferenceList) *api.ServiceProviderCluster {
	t.Helper()
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
			MaestroReadonlyBundles: bundles,
		},
	}
}

func newTestClusterScopedManagementClusterContent(t *testing.T, name string) *api.ManagementClusterContent {
	t.Helper()
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

func newTestClusterController(t *testing.T, name string) *api.Controller {
	t.Helper()
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

func newTestNodePoolController(t *testing.T, name string) *api.Controller {
	t.Helper()
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
			"/nodePools/test-nodepool" +
			"/hcpOpenShiftControllers/" + name))
	return &api.Controller{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		Status: api.ControllerStatus{
			Conditions: []metav1.Condition{},
		},
	}
}

func newTestExternalAuthController(t *testing.T, name string) *api.Controller {
	t.Helper()
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
			"/externalAuths/test-auth" +
			"/hcpOpenShiftControllers/" + name))
	return &api.Controller{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		Status: api.ControllerStatus{
			Conditions: []metav1.Condition{},
		},
	}
}
