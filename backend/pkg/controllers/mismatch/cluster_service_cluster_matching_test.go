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

package mismatch

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	clocktesting "k8s.io/utils/clock/testing"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	testCSMatchingResourceGroup = "cs-matching-rg"
	testCSMatchingClusterName   = "cs-matching-cluster"
	testClusterServiceIDStr     = "/api/aro_hcp/v1alpha1/clusters/abc123def456"
)

func TestSynchronizeAllClusters_CSClusterMatchesCosmos(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	ctrl := gomock.NewController(t)
	now := time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC)

	subscription := testSubscription()
	cosmosCluster := testCosmosCluster(t, func(c *coreapi.HCPOpenShiftCluster) {
		clusterServiceID := metadataapi.Must(metadataapi.NewInternalID(testClusterServiceIDStr))
		c.ServiceProviderProperties.ClusterServiceID = &clusterServiceID
	})

	mockDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{
		subscription,
		cosmosCluster,
	})
	require.NoError(t, err)

	matchedCSCluster := testClusterServiceCluster(t, now.Add(-61*time.Minute))

	mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
	mockCS.EXPECT().
		ListClusters("").
		Return(ocm.NewSimpleClusterListIterator([]*arohcpv1alpha1.Cluster{matchedCSCluster}, nil))

	c := &clusterServiceClusterMatching{
		clock: clocktesting.NewFakePassiveClock(now),
		subscriptionLister: &corelistertesting.SliceSubscriptionLister{
			Subscriptions: []*coreapi.Subscription{subscription},
		},
		resourcesDBClient:    mockDB,
		clusterServiceClient: mockCS,
	}

	require.NoError(t, c.synchronizeAllClusters(ctx))
}

func TestSynchronizeAllClusters_CSClusterMatchesCosmosPendingClusterServiceID(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	ctrl := gomock.NewController(t)
	now := time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC)

	subscription := testSubscription()
	cosmosCluster := testCosmosCluster(t, func(c *coreapi.HCPOpenShiftCluster) {
		pendingClusterServiceID := metadataapi.Must(metadataapi.NewInternalID(testClusterServiceIDStr))
		c.ServiceProviderProperties.PendingClusterServiceID = &pendingClusterServiceID
		c.ServiceProviderProperties.ClusterServiceID = nil
	})

	mockDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{
		subscription,
		cosmosCluster,
	})
	require.NoError(t, err)

	// Old enough to delete if unmatched; must match via PendingClusterServiceID.
	matchedCSCluster := testClusterServiceCluster(t, now.Add(-61*time.Minute))

	mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
	mockCS.EXPECT().
		ListClusters("").
		Return(ocm.NewSimpleClusterListIterator([]*arohcpv1alpha1.Cluster{matchedCSCluster}, nil))

	c := &clusterServiceClusterMatching{
		clock: clocktesting.NewFakePassiveClock(now),
		subscriptionLister: &corelistertesting.SliceSubscriptionLister{
			Subscriptions: []*coreapi.Subscription{subscription},
		},
		resourcesDBClient:    mockDB,
		clusterServiceClient: mockCS,
	}

	require.NoError(t, c.synchronizeAllClusters(ctx))
}

func TestSynchronizeAllClusters_CSClusterMismatchNotOldEnough(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	ctrl := gomock.NewController(t)
	now := time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC)

	subscription := testSubscription()

	mockDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{
		subscription,
	})
	require.NoError(t, err)

	mismatchedYoungCSCluster := testClusterServiceCluster(t, now.Add(-30*time.Minute))

	mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
	mockCS.EXPECT().
		ListClusters("").
		Return(ocm.NewSimpleClusterListIterator([]*arohcpv1alpha1.Cluster{mismatchedYoungCSCluster}, nil))

	c := &clusterServiceClusterMatching{
		clock: clocktesting.NewFakePassiveClock(now),
		subscriptionLister: &corelistertesting.SliceSubscriptionLister{
			Subscriptions: []*coreapi.Subscription{subscription},
		},
		resourcesDBClient:    mockDB,
		clusterServiceClient: mockCS,
	}

	require.NoError(t, c.synchronizeAllClusters(ctx))
}

func TestSynchronizeAllClusters_CSClusterMismatchCosmosClusterExistsSkipsDelete(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	ctrl := gomock.NewController(t)
	now := time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC)

	subscription := testSubscription()
	// Cluster is in Cosmos but has no ClusterServiceID yet, so getAllCosmosObjs does not
	// index it by CS HREF. The double-check Get by Azure coordinates must still find it.
	cosmosCluster := testCosmosCluster(t)

	mockDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{
		subscription,
		cosmosCluster,
	})
	require.NoError(t, err)

	mismatchedOlderCSClusterInCosmos := testClusterServiceCluster(t, now.Add(-61*time.Minute))

	mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
	mockCS.EXPECT().
		ListClusters("").
		Return(ocm.NewSimpleClusterListIterator([]*arohcpv1alpha1.Cluster{mismatchedOlderCSClusterInCosmos}, nil))

	c := &clusterServiceClusterMatching{
		clock: clocktesting.NewFakePassiveClock(now),
		subscriptionLister: &corelistertesting.SliceSubscriptionLister{
			Subscriptions: []*coreapi.Subscription{subscription},
		},
		resourcesDBClient:    mockDB,
		clusterServiceClient: mockCS,
	}

	require.NoError(t, c.synchronizeAllClusters(ctx))
}

func TestSynchronizeAllClusters_CSClusterMismatchOlderThanOneHourDeletes(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	ctrl := gomock.NewController(t)
	now := time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC)

	subscription := testSubscription()

	mockDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{
		subscription,
	})
	require.NoError(t, err)

	mismatchedOlderOrphanedCSCluster := testClusterServiceCluster(t, now.Add(-61*time.Minute))
	clusterServiceID := metadataapi.Must(metadataapi.NewInternalID(testClusterServiceIDStr))

	mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
	mockCS.EXPECT().
		ListClusters("").
		Return(ocm.NewSimpleClusterListIterator([]*arohcpv1alpha1.Cluster{mismatchedOlderOrphanedCSCluster}, nil))
	mockCS.EXPECT().
		DeleteCluster(gomock.Any(), clusterServiceID).
		Return(nil)

	c := &clusterServiceClusterMatching{
		clock: clocktesting.NewFakePassiveClock(now),
		subscriptionLister: &corelistertesting.SliceSubscriptionLister{
			Subscriptions: []*coreapi.Subscription{subscription},
		},
		resourcesDBClient:    mockDB,
		clusterServiceClient: mockCS,
	}

	require.NoError(t, c.synchronizeAllClusters(ctx))
}

func testSubscription() *coreapi.Subscription {
	rid := metadataapi.Must(coreapi.ToSubscriptionResourceID(testSubscriptionID))
	return &coreapi.Subscription{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   rid,
			PartitionKey: strings.ToLower(rid.SubscriptionID),
		},
		ResourceID: rid,
		State:      coreapi.SubscriptionStateRegistered,
	}
}

func testCosmosCluster(t *testing.T, opts ...func(*coreapi.HCPOpenShiftCluster)) *coreapi.HCPOpenShiftCluster {
	t.Helper()
	rid := metadataapi.Must(coreapi.ToClusterResourceID(testSubscriptionID, testCSMatchingResourceGroup, testCSMatchingClusterName))
	cluster := &coreapi.HCPOpenShiftCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   rid,
			PartitionKey: strings.ToLower(rid.SubscriptionID),
		},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID:   rid,
				Name: testCSMatchingClusterName,
				Type: coreapi.ClusterResourceType.String(),
			},
			Location: "eastus",
		},
	}
	for _, opt := range opts {
		opt(cluster)
	}
	return cluster
}

func testClusterServiceCluster(t *testing.T, createdAt time.Time) *arohcpv1alpha1.Cluster {
	t.Helper()
	cluster, err := arohcpv1alpha1.NewCluster().
		HREF(testClusterServiceIDStr).
		ID("abc123def456").
		CreationTimestamp(createdAt).
		Azure(arohcpv1alpha1.NewAzure().
			SubscriptionID(strings.ToLower(testSubscriptionID)).
			ResourceGroupName(strings.ToLower(testCSMatchingResourceGroup)).
			ResourceName(strings.ToLower(testCSMatchingClusterName))).
		Build()
	require.NoError(t, err)
	return cluster
}
