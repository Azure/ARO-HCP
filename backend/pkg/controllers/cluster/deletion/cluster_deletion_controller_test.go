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
	clocktesting "k8s.io/utils/clock/testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/billingcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestClusterDeletionController_SyncOnce(t *testing.T) {
	fixedClockTime := time.Now().UTC().Truncate(time.Second)

	testKey := controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}

	readyForDeletionCluster := func(t *testing.T) *coreapi.HCPOpenShiftCluster {
		return newTestClusterWithNewDeletionApproach(t, func(c *coreapi.HCPOpenShiftCluster) {
			c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-time.Hour)}
			c.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-30 * time.Minute)}
			c.ServiceProviderProperties.ClusterServiceID = nil
		})
	}

	verifyClusterStillExists := func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
		t.Helper()
		_, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
		require.NoError(t, err, "expected cluster to still exist in Cosmos")
	}

	verifyClusterDeleted := func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
		t.Helper()
		_, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
		assert.True(t, cosmosstorageutils.IsNotFoundError(err), "expected cluster to be deleted from Cosmos")
	}

	testCases := []struct {
		name            string
		existingCluster *coreapi.HCPOpenShiftCluster
		extraResources  []any
		wantErr         bool
		verifyDB        func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient)
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
			existingCluster: newTestClusterWithNewDeletionApproach(t, func(c *coreapi.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-time.Hour)}
			}),
			verifyDB: verifyClusterStillExists,
		},
		{
			name: "ClusterServiceID still set -- no-op",
			existingCluster: newTestClusterWithNewDeletionApproach(t, func(c *coreapi.HCPOpenShiftCluster) {
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
				newTestSPC(t, coreapi.MaestroBundleReferenceList{
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
			name:            "cluster still has credential requests -- blocks",
			existingCluster: readyForDeletionCluster(t),
			extraResources:  []any{newTestCredentialRequest(t, "cred-1")},
			verifyDB:        verifyClusterStillExists,
		},
		{
			name:            "cluster still has credential revocations -- blocks",
			existingCluster: readyForDeletionCluster(t),
			extraResources:  []any{newTestCredentialRevocation(t, "revoke-1")},
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
			name:            "orphaned credential request controller docs do not block deletion",
			existingCluster: readyForDeletionCluster(t),
			extraResources:  []any{newTestCredentialRequestController(t, "orphaned-cred-controller")},
			verifyDB:        verifyClusterDeleted,
		},
		{
			name:            "orphaned credential revocation controller docs do not block deletion",
			existingCluster: readyForDeletionCluster(t),
			extraResources:  []any{newTestCredentialRevocationController(t, "orphaned-rev-controller")},
			verifyDB:        verifyClusterDeleted,
		},
		{
			name: "feature flag false -- no-op even when all delete conditions met",
			existingCluster: newTestClusterWithOldDeletionApproach(t, func(c *coreapi.HCPOpenShiftCluster) {
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

			mockResourcesDBClient, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			mockBillingDBClient := billingcosmosstoragetesting.NewMockBillingDBClient()

			clustersForLister := []*coreapi.HCPOpenShiftCluster{}
			if tc.existingCluster != nil {
				clustersForLister = append(clustersForLister, tc.existingCluster)
			}

			spcForLister := []*coreapi.ServiceProviderCluster{}
			for _, r := range tc.extraResources {
				if spc, ok := r.(*coreapi.ServiceProviderCluster); ok {
					spcForLister = append(spcForLister, spc)
				}
			}

			syncer := &clusterDeletionController{
				clusterLister:                &corelistertesting.SliceClusterLister{Clusters: clustersForLister},
				serviceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: spcForLister},
				resourcesDBClient:            mockResourcesDBClient,
				billingDBClient:              mockBillingDBClient,
				passiveClock:                 clocktesting.NewFakePassiveClock(fixedClockTime),
			}

			err = syncer.SyncOnce(ctx, testKey)
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
		cluster *coreapi.HCPOpenShiftCluster
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
			cluster: newTestClusterWithNewDeletionApproach(t, func(c *coreapi.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime}
			}),
			want: false,
		},
		{
			name: "both timestamps set but ClusterServiceID not nil",
			cluster: newTestClusterWithNewDeletionApproach(t, func(c *coreapi.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime}
				c.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: fixedClockTime}
			}),
			want: false,
		},
		{
			name: "all conditions met",
			cluster: newTestClusterWithNewDeletionApproach(t, func(c *coreapi.HCPOpenShiftCluster) {
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

func newTestNodePool(t *testing.T) *coreapi.HCPOpenShiftClusterNodePool {
	t.Helper()
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
			"/nodePools/test-nodepool"))
	return &coreapi.HCPOpenShiftClusterNodePool{
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID:   resourceID,
				Name: "test-nodepool",
				Type: coreapi.NodePoolResourceType.String(),
			},
			Location: "eastus",
		},
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		Properties: coreapi.HCPOpenShiftClusterNodePoolProperties{
			Platform: coreapi.NodePoolPlatformProfile{
				OSDisk: coreapi.OSDiskProfile{
					DiskStorageAccountType: metadataapi.DiskStorageAccountTypePremium_LRS,
					DiskType:               metadataapi.OsDiskTypeManaged,
				},
			},
		},
	}
}

func newTestExternalAuth(t *testing.T) *coreapi.HCPOpenShiftClusterExternalAuth {
	t.Helper()
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
			"/externalAuths/test-auth"))
	return &coreapi.HCPOpenShiftClusterExternalAuth{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		ProxyResource: coreapi.ProxyResource{
			Resource: coreapi.Resource{
				ID:   resourceID,
				Name: "test-auth",
				Type: coreapi.ExternalAuthResourceType.String(),
			},
		},
	}
}

func newTestSPC(t *testing.T, bundles coreapi.MaestroBundleReferenceList) *coreapi.ServiceProviderCluster {
	t.Helper()
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
			MaestroReadonlyBundles: bundles,
		},
	}
}

func newTestClusterScopedManagementClusterContent(t *testing.T, name string) *coreapi.ManagementClusterContent {
	t.Helper()
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

func newTestClusterController(t *testing.T, name string) *coreapi.Controller {
	t.Helper()
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

func newTestNodePoolController(t *testing.T, name string) *coreapi.Controller {
	t.Helper()
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
			"/nodePools/test-nodepool" +
			"/hcpOpenShiftControllers/" + name))
	return &coreapi.Controller{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		Status: coreapi.ControllerStatus{
			Conditions: []metav1.Condition{},
		},
	}
}

func newTestExternalAuthController(t *testing.T, name string) *coreapi.Controller {
	t.Helper()
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
			"/externalAuths/test-auth" +
			"/hcpOpenShiftControllers/" + name))
	return &coreapi.Controller{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		Status: coreapi.ControllerStatus{
			Conditions: []metav1.Condition{},
		},
	}
}

func newTestCredentialRequestController(t *testing.T, name string) *coreapi.Controller {
	t.Helper()
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
			"/systemAdminCredentialRequests/test-cred" +
			"/hcpOpenShiftControllers/" + name))
	return &coreapi.Controller{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		Status: coreapi.ControllerStatus{
			Conditions: []metav1.Condition{},
		},
	}
}

func newTestCredentialRevocationController(t *testing.T, name string) *coreapi.Controller {
	t.Helper()
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
			"/systemAdminCredentialRevocations/test-revoke" +
			"/hcpOpenShiftControllers/" + name))
	return &coreapi.Controller{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		Status: coreapi.ControllerStatus{
			Conditions: []metav1.Condition{},
		},
	}
}
