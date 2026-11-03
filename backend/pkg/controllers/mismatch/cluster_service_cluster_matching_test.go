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
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/apitesting/coreapitesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
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
		cosmosClusterBuilder            func(t *testing.T) *coreapi.Cluster
		clusterServiceClusterCreatedAgo time.Duration
		expectDeleteCluster             bool
	}{
		{
			name: "matched cluster service cluster indexed by ClusterServiceID is not deleted",
			cosmosClusterBuilder: func(t *testing.T) *coreapi.Cluster {
				return testCosmosCluster(t, func(c *coreapi.Cluster) {
					clusterServiceID := metadataapi.Must(metadataapi.NewInternalID(testClusterServiceIDStr))
					c.ServiceProviderProperties.ClusterServiceID = &clusterServiceID
				})
			},
			clusterServiceClusterCreatedAgo: 61 * time.Minute,
			expectDeleteCluster:             false,
		},
		{
			name: "matched cluster service cluster indexed by PendingClusterServiceID is not deleted",
			cosmosClusterBuilder: func(t *testing.T) *coreapi.Cluster {
				return testCosmosCluster(t, func(c *coreapi.Cluster) {
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
			cosmosClusterBuilder: func(t *testing.T) *coreapi.Cluster {
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
				cluster := testCase.cosmosClusterBuilder(t)
				cosmosResources = append(cosmosResources, cluster, coreapitesting.ServiceProviderIDsForCluster(cluster))
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

func TestSynchronizeAllClustersMissingProvider(t *testing.T) {
	fixedNow := time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC)
	for _, phase := range []string{"before initialization", "unexpected provider loss", "deletion dispatched", "dispatch without initialization marker", "completion without dispatch", "after child cleanup"} {
		t.Run(phase, func(t *testing.T) {
			ctx := utils.ContextWithLogger(t.Context(), testr.New(t))
			ctrl := gomock.NewController(t)
			subscription := testSubscription()
			cluster := testCosmosCluster(t)
			cluster.ServiceProviderProperties.ServiceProviderClusterInitialized = phase != "before initialization" && phase != "dispatch without initialization marker"
			if phase == "after child cleanup" || phase == "deletion dispatched" || phase == "dispatch without initialization marker" {
				deletedAt := metav1.NewTime(fixedNow.Add(-time.Hour))
				cluster.ServiceProviderProperties.DeletionTimestamp = &deletedAt
				cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &deletedAt
			}
			cluster.ServiceProviderProperties.ClusterServiceDeletionCompleted = phase == "after child cleanup" || phase == "completion without dispatch"
			db, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{subscription, cluster})
			require.NoError(t, err)
			before := db.GetAllDocuments()
			parentCluster := testClusterServiceCluster(t, fixedNow.Add(-2*time.Hour))
			orphanID := metadataapi.Must(metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/clusters/orphan"))
			orphan, err := arohcpv1alpha1.NewCluster().
				CreationTimestamp(fixedNow.Add(-2 * time.Hour)).
				ID("orphan").HREF(orphanID.String()).
				Azure(arohcpv1alpha1.NewAzure().
					SubscriptionID(testSubscriptionID).
					ResourceGroupName(testCSMatchingResourceGroup).
					ResourceName("orphan")).Build()
			require.NoError(t, err)
			csClient := ocm.NewMockClusterServiceClientSpec(ctrl)
			csClient.EXPECT().ListClusters("").Return(ocm.NewSimpleClusterListIterator([]*arohcpv1alpha1.Cluster{parentCluster, orphan}, nil))
			csClient.EXPECT().DeleteCluster(gomock.Any(), orphanID).Return(nil)
			controller := &clusterServiceClusterMatching{
				clock:                clocktesting.NewFakePassiveClock(fixedNow),
				subscriptionLister:   &corelistertesting.SliceSubscriptionLister{Subscriptions: []*coreapi.Subscription{subscription}},
				resourcesDBClient:    db,
				clusterServiceClient: csClient,
			}
			err = controller.SyncOnce(ctx, nil)
			if phase == "before initialization" || phase == "after child cleanup" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, "provider state missing for initialized or deleting cluster")
				require.ErrorContains(t, err, cluster.ID.String())
			}
			require.Equal(t, before, db.GetAllDocuments())
		})
	}
}

func TestSynchronizeAllClustersMissingProviderWithClusterServiceError(t *testing.T) {
	for _, initialized := range []bool{false, true} {
		for _, message := range []string{"no healthy upstream", "cluster service list failed"} {
			t.Run(fmt.Sprintf("initialized=%t/%s", initialized, message), func(t *testing.T) {
				ctx := utils.ContextWithLogger(t.Context(), testr.New(t))
				subscription := testSubscription()
				cluster := testCosmosCluster(t)
				cluster.ServiceProviderProperties.ServiceProviderClusterInitialized = initialized
				db, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{subscription, cluster})
				require.NoError(t, err)
				listErr := errors.New(message)
				csClient := ocm.NewMockClusterServiceClientSpec(gomock.NewController(t))
				csClient.EXPECT().ListClusters("").Return(ocm.NewSimpleClusterListIterator(nil, listErr))
				controller := &clusterServiceClusterMatching{
					subscriptionLister:   &corelistertesting.SliceSubscriptionLister{Subscriptions: []*coreapi.Subscription{subscription}},
					resourcesDBClient:    db,
					clusterServiceClient: csClient,
				}
				err = controller.SyncOnce(ctx, nil)
				if initialized {
					require.ErrorContains(t, err, "provider state missing for initialized or deleting cluster")
					require.ErrorContains(t, err, cluster.ID.String())
				} else if message == "no healthy upstream" {
					require.NoError(t, err)
				}
				if message != "no healthy upstream" {
					require.ErrorIs(t, err, listErr)
				}
			})
		}
	}
}

func TestSynchronizeAllClustersProviderReadError(t *testing.T) {
	for _, statusCode := range []int{http.StatusForbidden, http.StatusTooManyRequests, http.StatusInternalServerError} {
		t.Run(fmt.Sprint(statusCode), func(t *testing.T) {
			ctx := utils.ContextWithLogger(t.Context(), testr.New(t))
			subscription := testSubscription()
			db, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{subscription, testCosmosCluster(t)})
			require.NoError(t, err)
			readErr := &azcore.ResponseError{StatusCode: statusCode}
			controller := &clusterServiceClusterMatching{
				subscriptionLister:   &corelistertesting.SliceSubscriptionLister{Subscriptions: []*coreapi.Subscription{subscription}},
				resourcesDBClient:    &providerReadErrorDB{ResourcesDBClient: db, err: readErr},
				clusterServiceClient: ocm.NewMockClusterServiceClientSpec(gomock.NewController(t)),
			}
			require.ErrorIs(t, controller.synchronizeAllClusters(ctx), readErr)
		})
	}
}

type providerReadErrorDB struct {
	corecosmosstorage.ResourcesDBClient
	err error
}

func (db *providerReadErrorDB) ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName string) cosmosstorageutils.ResourceCRUD[coreapi.ServiceProviderCluster, *coreapi.ServiceProviderCluster] {
	return &providerReadErrorCRUD{err: db.err}
}

type providerReadErrorCRUD struct {
	cosmosstorageutils.ResourceCRUD[coreapi.ServiceProviderCluster, *coreapi.ServiceProviderCluster]
	err error
}

func (crud *providerReadErrorCRUD) Get(ctx context.Context, resourceName string) (*coreapi.ServiceProviderCluster, error) {
	return nil, crud.err
}

func testSubscription() *coreapi.Subscription {
	rid := metadataapi.Must(coreapihelpers.ToSubscriptionResourceID(testSubscriptionID))
	return &coreapi.Subscription{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   rid,
			PartitionKey: strings.ToLower(rid.SubscriptionID),
		},
		State: coreapi.SubscriptionStateRegistered,
	}
}

func testCosmosCluster(t *testing.T, opts ...func(*coreapi.Cluster)) *coreapi.Cluster {
	t.Helper()
	rid := metadataapi.Must(coreapihelpers.ToClusterResourceID(testSubscriptionID, testCSMatchingResourceGroup, testCSMatchingClusterName))
	cluster := &coreapi.Cluster{
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
