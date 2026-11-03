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
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

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

	clusterServiceID := metadataapi.Must(metadataapi.NewInternalID(testClusterServiceIDStr))
	tests := []struct {
		pending, final                  *metadataapi.InternalID
		name                            string
		cosmosClusterBuilder            func(t *testing.T) *coreapi.Cluster
		clusterServiceClusterCreatedAgo time.Duration
		expectDeleteCluster             bool
	}{
		{
			name:                            "matched cluster service cluster indexed by ClusterServiceID is not deleted",
			cosmosClusterBuilder:            func(t *testing.T) *coreapi.Cluster { return testCosmosCluster(t) },
			final:                           &clusterServiceID,
			clusterServiceClusterCreatedAgo: 61 * time.Minute,
			expectDeleteCluster:             false,
		},
		{
			name:                            "matched cluster service cluster indexed by PendingClusterServiceID is not deleted",
			cosmosClusterBuilder:            func(t *testing.T) *coreapi.Cluster { return testCosmosCluster(t) },
			pending:                         &clusterServiceID,
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
				cosmosResources = append(cosmosResources, cluster, coreapitesting.ServiceProviderForCluster(cluster, testCase.pending, testCase.final))
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
	for _, phase := range []string{"before initialization", "unexpected provider loss", "after child cleanup"} {
		t.Run(phase, func(t *testing.T) {
			ctx := utils.ContextWithLogger(t.Context(), testr.New(t))
			ctrl := gomock.NewController(t)
			subscription := testSubscription()
			cluster := testCosmosCluster(t)
			cluster.ServiceProviderProperties.ServiceProviderClusterInitialized = phase != "before initialization"
			if phase == "after child cleanup" {
				deletedAt := metav1.NewTime(fixedNow.Add(-time.Hour))
				cluster.ServiceProviderProperties.DeletionTimestamp = &deletedAt
				cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &deletedAt
				cluster.ServiceProviderProperties.ClusterServiceDeletionCompleted = true
			}
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
			indexed, clusters, err := controller.getAllCosmosObjs(ctx)
			require.NoError(t, err)
			require.Empty(t, indexed)
			require.Len(t, clusters, 1)
			require.NoError(t, controller.synchronizeAllClusters(ctx))
			require.Equal(t, before, db.GetAllDocuments())
		})
	}
}

func TestSynchronizeAllClustersProviderListError(t *testing.T) {
	for _, statusCode := range []int{http.StatusNotFound, http.StatusForbidden, http.StatusTooManyRequests, http.StatusInternalServerError} {
		t.Run(fmt.Sprint(statusCode), func(t *testing.T) {
			ctx := utils.ContextWithLogger(t.Context(), testr.New(t))
			subscription := testSubscription()
			db, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{subscription, testCosmosCluster(t)})
			require.NoError(t, err)
			readErr := &azcore.ResponseError{StatusCode: statusCode}
			controller := &clusterServiceClusterMatching{
				subscriptionLister:   &corelistertesting.SliceSubscriptionLister{Subscriptions: []*coreapi.Subscription{subscription}},
				resourcesDBClient:    &providerInventoryDB{ResourcesDBClient: db, pages: &providerPages{listErr: readErr}},
				clusterServiceClient: ocm.NewMockClusterServiceClientSpec(gomock.NewController(t)),
			}
			require.ErrorIs(t, controller.synchronizeAllClusters(ctx), readErr)
		})
	}
}

func TestProviderInventoryPages(t *testing.T) {
	ctx := t.Context()
	subscription := testSubscription()
	otherSubscription := testSubscription()
	otherSubscription.ResourceID = metadataapi.Must(coreapihelpers.ToSubscriptionResourceID("00000000-0000-0000-0000-000000000002"))
	otherSubscription.PartitionKey = otherSubscription.ResourceID.SubscriptionID
	resources := []any{subscription, otherSubscription}
	var providers []*coreapi.ServiceProviderCluster
	want := map[string]*coreapi.Cluster{}
	for index := range 3 {
		cluster := testCosmosCluster(t)
		cluster.ID = metadataapi.Must(coreapihelpers.ToClusterResourceID(testSubscriptionID, fmt.Sprintf("group-%d", index), testCSMatchingClusterName))
		cluster.ResourceID = cluster.ID
		if index == 2 {
			cluster.ID = metadataapi.Must(coreapihelpers.ToClusterResourceID(otherSubscription.ResourceID.SubscriptionID, "group-0", testCSMatchingClusterName))
			cluster.ResourceID = cluster.ID
			cluster.PartitionKey = otherSubscription.PartitionKey
		}
		id := metadataapi.Must(metadataapi.NewInternalID(fmt.Sprintf("/api/aro_hcp/v1alpha1/clusters/id%d", index)))
		provider := coreapitesting.ServiceProviderForCluster(cluster, &id, nil)
		provider.ResourceID = metadataapi.Must(azcorearm.ParseResourceID(strings.ToUpper(provider.ResourceID.String())))
		resources = append(resources, cluster, provider)
		providers = append(providers, provider)
		want[id.String()] = cluster
	}
	extra := providers[0].DeepCopy()
	extra.ResourceID = metadataapi.Must(azcorearm.ParseResourceID(providers[0].ResourceID.Parent.String() + "/serviceProviderClusters/other"))
	providers = append(providers, extra)
	providers = append(providers, nil)
	extra = providers[0].DeepCopy()
	extra.ResourceID = metadataapi.Must(azcorearm.ParseResourceID(strings.ReplaceAll(providers[0].ResourceID.String(), strings.ToUpper(testCSMatchingClusterName), "absent-parent")))
	providers = append(providers, extra)
	db, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, resources)
	require.NoError(t, err)
	pages := &providerPages{providers: providers}
	counted := &providerInventoryDB{ResourcesDBClient: db, pages: pages}
	controller := &clusterServiceClusterMatching{
		subscriptionLister: &corelistertesting.SliceSubscriptionLister{Subscriptions: []*coreapi.Subscription{subscription, otherSubscription}},
		resourcesDBClient:  counted,
	}
	indexed, clusters, err := controller.getAllCosmosObjs(ctx)
	require.NoError(t, err)
	require.Len(t, clusters, 3)
	require.Len(t, indexed, 3)
	for id, cluster := range want {
		require.True(t, strings.EqualFold(cluster.ID.String(), indexed[id].ID.String()))
	}
	require.Equal(t, 0, counted.gets, "inventory must not issue one provider GET per parent")
	require.Equal(t, []string{"", "1", "2", "3", "4", "5"}, pages.tokens)
	for _, size := range pages.pageSizes {
		require.EqualValues(t, 100, size)
	}
}

func TestProviderInventoryInvalidIdentity(t *testing.T) {
	for _, scenario := range []string{"missing identity", "wrong parent", "duplicate parent", "duplicate cluster service ID"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := t.Context()
			subscription := testSubscription()
			cluster := testCosmosCluster(t)
			id := metadataapi.Must(metadataapi.NewInternalID(testClusterServiceIDStr))
			provider := coreapitesting.ServiceProviderForCluster(cluster, nil, &id)
			providers := []*coreapi.ServiceProviderCluster{provider}
			resources := []any{subscription, cluster}
			switch scenario {
			case "missing identity":
				provider.ResourceID = nil
			case "wrong parent":
				provider.ResourceID = metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/wrong/serviceProviderClusters/default"))
			case "duplicate parent":
				providers = append(providers, provider.DeepCopy())
			case "duplicate cluster service ID":
				other := testCosmosCluster(t)
				other.ID = metadataapi.Must(coreapihelpers.ToClusterResourceID(testSubscriptionID, "other-group", testCSMatchingClusterName))
				other.ResourceID = other.ID
				resources = append(resources, other)
				providers = append(providers, coreapitesting.ServiceProviderForCluster(other, nil, &id))
			}
			db, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)
			controller := &clusterServiceClusterMatching{
				subscriptionLister:   &corelistertesting.SliceSubscriptionLister{Subscriptions: []*coreapi.Subscription{subscription}},
				resourcesDBClient:    &providerInventoryDB{ResourcesDBClient: db, pages: &providerPages{providers: providers}},
				clusterServiceClient: ocm.NewMockClusterServiceClientSpec(gomock.NewController(t)),
			}
			require.Error(t, controller.synchronizeAllClusters(ctx))
		})
	}
}

func TestProviderInventoryIncompleteListStopsReconciliation(t *testing.T) {
	for _, failure := range []string{"list", "iteration"} {
		t.Run(failure, func(t *testing.T) {
			ctx := t.Context()
			subscription := testSubscription()
			cluster := testCosmosCluster(t)
			id := metadataapi.Must(metadataapi.NewInternalID(testClusterServiceIDStr))
			provider := coreapitesting.ServiceProviderForCluster(cluster, nil, &id)
			db, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{subscription, cluster, provider})
			require.NoError(t, err)
			listErr := &azcore.ResponseError{StatusCode: http.StatusTooManyRequests}
			pages := &providerPages{providers: []*coreapi.ServiceProviderCluster{provider, provider}, failPage: 1}
			if failure == "list" {
				pages.listErr = listErr
			} else {
				pages.iterationErr = listErr
			}
			counted := &providerInventoryDB{ResourcesDBClient: db, pages: pages}
			controller := &clusterServiceClusterMatching{
				subscriptionLister:   &corelistertesting.SliceSubscriptionLister{Subscriptions: []*coreapi.Subscription{subscription}},
				resourcesDBClient:    counted,
				clusterServiceClient: ocm.NewMockClusterServiceClientSpec(gomock.NewController(t)),
			}
			before := db.GetAllDocuments()
			require.ErrorIs(t, controller.synchronizeAllClusters(ctx), listErr)
			require.Equal(t, []string{"", "1"}, pages.tokens)
			require.Zero(t, counted.gets)
			require.Equal(t, before, db.GetAllDocuments())
		})
	}
}

type providerInventoryDB struct {
	corecosmosstorage.ResourcesDBClient
	pages *providerPages
	gets  int
}

func (db *providerInventoryDB) ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName string) cosmosstorageutils.ResourceCRUD[coreapi.ServiceProviderCluster, *coreapi.ServiceProviderCluster] {
	return &countedProviderCRUD{ResourceCRUD: db.ResourcesDBClient.ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName), db: db}
}

func (db *providerInventoryDB) ResourcesGlobalListers() corecosmosstorage.ResourcesGlobalListers {
	return &providerGlobalListers{ResourcesGlobalListers: db.ResourcesDBClient.ResourcesGlobalListers(), providers: db.pages}
}

type countedProviderCRUD struct {
	cosmosstorageutils.ResourceCRUD[coreapi.ServiceProviderCluster, *coreapi.ServiceProviderCluster]
	db *providerInventoryDB
}

func (crud *countedProviderCRUD) Get(ctx context.Context, name string) (*coreapi.ServiceProviderCluster, error) {
	crud.db.gets++
	return crud.ResourceCRUD.Get(ctx, name)
}

type providerGlobalListers struct {
	corecosmosstorage.ResourcesGlobalListers
	providers cosmosstorageutils.GlobalLister[coreapi.ServiceProviderCluster]
}

func (listers *providerGlobalListers) ServiceProviderClusters() cosmosstorageutils.GlobalLister[coreapi.ServiceProviderCluster] {
	return listers.providers
}

type providerPages struct {
	providers    []*coreapi.ServiceProviderCluster
	tokens       []string
	pageSizes    []int32
	listErr      error
	iterationErr error
	failPage     int
}

func (pages *providerPages) List(ctx context.Context, options *cosmosstorageutils.DBClientListResourceDocsOptions) (cosmosstorageutils.DBClientIterator[coreapi.ServiceProviderCluster], error) {
	token := ""
	if options != nil && options.ContinuationToken != nil {
		token = *options.ContinuationToken
	}
	pages.tokens = append(pages.tokens, token)
	if options != nil && options.PageSizeHint != nil {
		pages.pageSizes = append(pages.pageSizes, *options.PageSizeHint)
	}
	index, _ := strconv.Atoi(token)
	if index == pages.failPage && pages.listErr != nil {
		return nil, pages.listErr
	}
	page := &providerPage{}
	if index < len(pages.providers) {
		page.provider = pages.providers[index]
	}
	if index+1 < len(pages.providers) {
		page.token = strconv.Itoa(index + 1)
	}
	if index == pages.failPage {
		page.err = pages.iterationErr
	}
	return page, nil
}

type providerPage struct {
	provider *coreapi.ServiceProviderCluster
	token    string
	err      error
}

func (page *providerPage) Items(ctx context.Context) cosmosstorageutils.DBClientIteratorItem[coreapi.ServiceProviderCluster] {
	return func(yield func(string, *coreapi.ServiceProviderCluster) bool) {
		if page.provider != nil {
			yield("provider", page.provider)
		}
	}
}

func (page *providerPage) GetContinuationToken() string { return page.token }
func (page *providerPage) GetError() error              { return page.err }

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
