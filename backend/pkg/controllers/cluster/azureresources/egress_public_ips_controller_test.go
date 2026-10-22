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

package azureresources

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
)

func TestEgressPublicIPsSyncOnce(t *testing.T) {
	for _, testCase := range []struct {
		name           string
		found          int
		status         int
		builderFailure bool
		conflict       bool
	}{
		{name: "zero", status: 404},
		{name: "partial", found: 4, status: 404},
		{name: "all", found: 9},
		{name: "authorization", found: 4, status: 403},
		{name: "throttling", status: 429},
		{name: "service failure", status: 503},
		{name: "transport failure", status: -1},
		{name: "client failure clears stale missing", builderFailure: true},
		{name: "persistence conflict", found: 9, conflict: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := context.Background()
			cluster := newTestCluster(false)
			clusterID := metadataapi.Must(metadataapi.NewInternalID("/api/clusters_mgmt/v1/clusters/abc123"))
			cluster.ServiceProviderProperties.ClusterServiceID = &clusterID
			expected, err := coreapihelpers.EgressPublicIPResourceIDs(cluster)
			require.NoError(t, err)
			require.Len(t, expected, 9)
			serviceProvider := newTestServiceProviderCluster(coreapi.AzureReference{})
			serviceProvider.Status.AzureResources.EgressPublicIPs = map[string]coreapi.AzureResourceObservation{"unrelated": coreapi.AzureResourceFound}
			for index, resourceID := range expected {
				require.Equal(t, fmt.Sprintf("abc123-%d", index), resourceID.Name)
				require.Equal(t, testManagedRGName, resourceID.ResourceGroupName)
				serviceProvider.Status.AzureResources.EgressPublicIPs[resourceID.String()] = coreapi.AzureResourceNotFound
			}
			database, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProvider})
			require.NoError(t, err)
			mockController := gomock.NewController(t)
			builder := azureclient.NewMockFirstPartyApplicationClientBuilder(mockController)
			client := azureclient.NewMockPublicIPAddressesClient(mockController)
			if testCase.builderFailure {
				builder.EXPECT().PublicIPAddressesClient(testTenantID, testSubscriptionID).Return(nil, errors.New("credential failure"))
			} else {
				builder.EXPECT().PublicIPAddressesClient(testTenantID, testSubscriptionID).Return(client, nil).AnyTimes()
				for index, resourceID := range expected {
					var getErr error
					if index >= testCase.found {
						getErr = &azcore.ResponseError{StatusCode: testCase.status}
						if testCase.status == -1 {
							getErr = errors.New("transport failure")
						}
					}
					client.EXPECT().Get(gomock.Any(), testManagedRGName, resourceID.Name, nil).Return(armnetwork.PublicIPAddressesClientGetResponse{}, getErr).AnyTimes()
				}
			}
			syncer := &egressPublicIPsSyncer{
				resourcesDBClient:            database,
				clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: database},
				serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: database},
				subscriptionLister:           &corelistertesting.SliceSubscriptionLister{Subscriptions: []*coreapi.Subscription{newTestSubscription(ptr.To(testTenantID))}},
				azureFPAClientBuilder:        builder,
			}
			if testCase.conflict {
				serviceProvider.CosmosETag = "stale"
				syncer.serviceProviderClusterLister = &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{serviceProvider}}
			}
			key := controllerutils.HCPClusterKey{SubscriptionID: testSubscriptionID, ResourceGroupName: testResourceGroupName, HCPClusterName: testClusterName}
			err = syncer.SyncOnce(ctx, key)
			if testCase.conflict {
				require.True(t, cosmosstorageutils.IsPreconditionFailedError(err))
				syncer.serviceProviderClusterLister = &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: database}
				require.NoError(t, syncer.SyncOnce(ctx, key))
			} else if testCase.builderFailure || (testCase.status != 0 && testCase.status != 404) {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			updated, err := database.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
			require.NoError(t, err)
			require.Len(t, updated.Status.AzureResources.EgressPublicIPs, 9)
			for index, resourceID := range expected {
				want := coreapi.AzureResourceUnknown
				if index < testCase.found {
					want = coreapi.AzureResourceFound
				} else if testCase.status == 404 {
					want = coreapi.AzureResourceNotFound
				}
				require.Equal(t, want, updated.Status.AzureResources.EgressPublicIPs[resourceID.String()])
			}
			if !testCase.builderFailure {
				retryErr := syncer.SyncOnce(ctx, key)
				if testCase.status != 0 && testCase.status != 404 {
					require.Error(t, retryErr)
				} else {
					require.NoError(t, retryErr)
				}
				again, err := database.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				require.Equal(t, updated.CosmosETag, again.CosmosETag, "unchanged observations must not write")
			}
		})
	}
}

func TestEgressPublicIPsPrerequisites(t *testing.T) {
	for _, name := range []string{"UDR", "deleting", "missing cluster ID", "missing managed group", "missing tenant", "missing subscription", "missing service provider"} {
		t.Run(name, func(t *testing.T) {
			cluster := newTestCluster(name == "deleting")
			clusterID := metadataapi.Must(metadataapi.NewInternalID("/api/clusters_mgmt/v1/clusters/abc123"))
			cluster.ServiceProviderProperties.ClusterServiceID = &clusterID
			serviceProvider := newTestServiceProviderCluster(coreapi.AzureReference{})
			if name == "UDR" {
				cluster.CustomerProperties.Platform.OutboundType = metadataapi.OutboundType("UserDefinedRouting")
			}
			if name == "missing cluster ID" {
				cluster.ServiceProviderProperties.ClusterServiceID = nil
			}
			if name == "missing managed group" {
				cluster.CustomerProperties.Platform.ManagedResourceGroup = ""
			}
			subscription := newTestSubscription(ptr.To(testTenantID))
			if name == "missing tenant" {
				subscription.Properties.TenantId = nil
			}
			subscriptions := []*coreapi.Subscription{subscription}
			if name == "missing subscription" {
				subscriptions = nil
			}
			ctx := context.Background()
			database, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProvider})
			require.NoError(t, err)
			syncer := &egressPublicIPsSyncer{
				resourcesDBClient:            database,
				clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: database},
				serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: database},
				subscriptionLister:           &corelistertesting.SliceSubscriptionLister{Subscriptions: subscriptions},
				azureFPAClientBuilder:        azureclient.NewMockFirstPartyApplicationClientBuilder(gomock.NewController(t)),
			}
			if name == "missing service provider" {
				syncer.serviceProviderClusterLister = &corelistertesting.SliceServiceProviderClusterLister{}
			}
			require.NoError(t, syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{SubscriptionID: testSubscriptionID, ResourceGroupName: testResourceGroupName, HCPClusterName: testClusterName}))
		})
	}
}
