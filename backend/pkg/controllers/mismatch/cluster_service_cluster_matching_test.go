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

func TestSynchronizeAllClusters(t *testing.T) {
	fixedNow := time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name                            string
		cosmosClusterBuilder            func(t *testing.T) *coreapi.HCPOpenShiftCluster
		clusterServiceClusterCreatedAgo time.Duration
		expectDeleteCluster             bool
	}{
		{
			name: "matched cluster service cluster indexed by ClusterServiceID is not deleted",
			cosmosClusterBuilder: func(t *testing.T) *coreapi.HCPOpenShiftCluster {
				return testCosmosCluster(t, func(c *coreapi.HCPOpenShiftCluster) {
					clusterServiceID := metadataapi.Must(metadataapi.NewInternalID(testClusterServiceIDStr))
					c.ServiceProviderProperties.ClusterServiceID = &clusterServiceID
				})
			},
			clusterServiceClusterCreatedAgo: 61 * time.Minute,
			expectDeleteCluster:             false,
		},
		{
			name: "matched cluster service cluster indexed by PendingClusterServiceID is not deleted",
			cosmosClusterBuilder: func(t *testing.T) *coreapi.HCPOpenShiftCluster {
				return testCosmosCluster(t, func(c *coreapi.HCPOpenShiftCluster) {
					pendingClusterServiceID := metadataapi.Must(metadataapi.NewInternalID(testClusterServiceIDStr))
					c.ServiceProviderProperties.PendingClusterServiceID = &pendingClusterServiceID
					c.ServiceProviderProperties.ClusterServiceID = nil
				})
			},
			clusterServiceClusterCreatedAgo: 61 * time.Minute,
			expectDeleteCluster:             false,
		},
		{
			name:                            "unmatched young cluster service cluster is not deleted",
			cosmosClusterBuilder:            nil,
			clusterServiceClusterCreatedAgo: 30 * time.Minute,
			expectDeleteCluster:             false,
		},
		{
			name: "unmatched old cluster service cluster is not deleted when cosmos cluster exists",
			cosmosClusterBuilder: func(t *testing.T) *coreapi.HCPOpenShiftCluster {
				// No ClusterServiceID or PendingClusterServiceID, so getAllCosmosObjs does not
				// index by CS HREF. The double-check Get by Azure coordinates must still find it.
				return testCosmosCluster(t)
			},
			clusterServiceClusterCreatedAgo: 61 * time.Minute,
			expectDeleteCluster:             false,
		},
		{
			name:                            "unmatched old orphaned cluster service cluster is deleted",
			cosmosClusterBuilder:            nil,
			clusterServiceClusterCreatedAgo: 61 * time.Minute,
			expectDeleteCluster:             true,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			ctrl := gomock.NewController(t)

			subscription := testSubscription()
			cosmosResources := []any{subscription}
			if testCase.cosmosClusterBuilder != nil {
				cosmosResources = append(cosmosResources, testCase.cosmosClusterBuilder(t))
			}

			resourcesDBClient, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, cosmosResources)
			require.NoError(t, err)

			clusterServiceClusterCreatedAt := fixedNow.Add(-testCase.clusterServiceClusterCreatedAgo)
			clusterServiceCluster := testClusterServiceCluster(t, clusterServiceClusterCreatedAt)

			clusterServiceClient := ocm.NewMockClusterServiceClientSpec(ctrl)
			clusterServiceClient.EXPECT().
				ListClusters("").
				Return(ocm.NewSimpleClusterListIterator([]*arohcpv1alpha1.Cluster{clusterServiceCluster}, nil))
			if testCase.expectDeleteCluster {
				clusterServiceID := metadataapi.Must(metadataapi.NewInternalID(testClusterServiceIDStr))
				clusterServiceClient.EXPECT().
					DeleteCluster(gomock.Any(), clusterServiceID).
					Return(nil)
			}

			controller := &clusterServiceClusterMatching{
				clock: clocktesting.NewFakePassiveClock(fixedNow),
				subscriptionLister: &corelistertesting.SliceSubscriptionLister{
					Subscriptions: []*coreapi.Subscription{subscription},
				},
				resourcesDBClient:    resourcesDBClient,
				clusterServiceClient: clusterServiceClient,
			}

			require.NoError(t, controller.synchronizeAllClusters(ctx))
		})
	}
}

func testSubscription() *coreapi.Subscription {
	rid := metadataapi.Must(coreapi.ToSubscriptionResourceID(testSubscriptionID))
	return &coreapi.Subscription{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   rid,
			PartitionKey: strings.ToLower(rid.SubscriptionID),
		},
		State: coreapi.SubscriptionStateRegistered,
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
