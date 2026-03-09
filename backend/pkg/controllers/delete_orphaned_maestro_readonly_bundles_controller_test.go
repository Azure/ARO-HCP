// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controllers

import (
	"context"
	"fmt"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	workv1 "open-cluster-management.io/api/work/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/maestro"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestDeleteOrphanedMaestroReadonlyBundles_getAllServiceProviderClusters(t *testing.T) {
	ctx := context.Background()
	clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster"))
	spcResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/serviceProviderClusters/default"))

	tests := []struct {
		name                string
		setupDB             func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient)
		wantLen             int
		wantFirstResourceID string
	}{
		{
			name:    "empty DB returns no SPCs",
			setupDB: nil,
			wantLen: 0,
		},
		{
			name: "returns SPCs created via CRUD",
			setupDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient) {
				t.Helper()
				spc := &api.ServiceProviderCluster{
					CosmosMetadata: arm.CosmosMetadata{ResourceID: spcResourceID},
					ResourceID:     *spcResourceID,
				}
				spcCRUD := mockDB.ServiceProviderClusters(clusterResourceID.SubscriptionID, clusterResourceID.ResourceGroupName, clusterResourceID.Name)
				_, err := spcCRUD.Create(ctx, spc, nil)
				require.NoError(t, err)
			},
			wantLen:             1,
			wantFirstResourceID: spcResourceID.String(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockDB := databasetesting.NewMockDBClient()
			if tt.setupDB != nil {
				tt.setupDB(t, ctx, mockDB)
			}
			c := &deleteOrphanedMaestroReadonlyBundles{cosmosClient: mockDB}
			all, err := c.getAllServiceProviderClusters(ctx)
			require.NoError(t, err)
			require.Len(t, all, tt.wantLen)
			if tt.wantFirstResourceID != "" {
				assert.Equal(t, tt.wantFirstResourceID, all[0].ResourceID.String())
			}
		})
	}
}

// paginationSPCGlobalLister returns different iterators based on ContinuationToken to simulate pagination.
type paginationSPCGlobalLister struct {
	iter1 database.DBClientIterator[api.ServiceProviderCluster]
	iter2 database.DBClientIterator[api.ServiceProviderCluster]
	token string
}

func (p *paginationSPCGlobalLister) List(ctx context.Context, opts *database.DBClientListResourceDocsOptions) (database.DBClientIterator[api.ServiceProviderCluster], error) {
	tok := ""
	if opts != nil && opts.ContinuationToken != nil {
		tok = *opts.ContinuationToken
	}
	if tok == "" {
		return p.iter1, nil
	}
	if tok == p.token {
		return p.iter2, nil
	}
	return nil, fmt.Errorf("unexpected continuation token %q", tok)
}

// iteratorErrorSPCGlobalLister returns an iterator that reports an error from GetError().
type iteratorErrorSPCGlobalLister struct {
	iter database.DBClientIterator[api.ServiceProviderCluster]
}

func (i *iteratorErrorSPCGlobalLister) List(ctx context.Context, opts *database.DBClientListResourceDocsOptions) (database.DBClientIterator[api.ServiceProviderCluster], error) {
	return i.iter, nil
}

// panicGlobalLister is a GlobalLister that panics if List is called (used for unused listers in test doubles).
type panicGlobalLister[T any] struct{}

func (n *panicGlobalLister[T]) List(context.Context, *database.DBClientListResourceDocsOptions) (database.DBClientIterator[T], error) {
	panic("panicGlobalLister.List should not be called")
}

// paginationGlobalListers implements GlobalListers with pagination only for ServiceProviderClusters.
type paginationGlobalListers struct {
	spcLister database.GlobalLister[api.ServiceProviderCluster]
}

func (p *paginationGlobalListers) Subscriptions() database.GlobalLister[arm.Subscription] {
	return &panicGlobalLister[arm.Subscription]{}
}
func (p *paginationGlobalListers) Clusters() database.GlobalLister[api.HCPOpenShiftCluster] {
	return &panicGlobalLister[api.HCPOpenShiftCluster]{}
}
func (p *paginationGlobalListers) NodePools() database.GlobalLister[api.HCPOpenShiftClusterNodePool] {
	return &panicGlobalLister[api.HCPOpenShiftClusterNodePool]{}
}
func (p *paginationGlobalListers) ExternalAuths() database.GlobalLister[api.HCPOpenShiftClusterExternalAuth] {
	return &panicGlobalLister[api.HCPOpenShiftClusterExternalAuth]{}
}
func (p *paginationGlobalListers) ServiceProviderClusters() database.GlobalLister[api.ServiceProviderCluster] {
	return p.spcLister
}
func (p *paginationGlobalListers) Operations() database.GlobalLister[api.Operation] {
	return &panicGlobalLister[api.Operation]{}
}
func (p *paginationGlobalListers) ActiveOperations() database.GlobalLister[api.Operation] {
	return &panicGlobalLister[api.Operation]{}
}
func (p *paginationGlobalListers) Controllers() database.GlobalLister[api.Controller] {
	return &panicGlobalLister[api.Controller]{}
}
func (p *paginationGlobalListers) ServiceProviderNodePools() database.GlobalLister[api.ServiceProviderNodePool] {
	return &panicGlobalLister[api.ServiceProviderNodePool]{}
}

var _ database.GlobalListers = (*paginationGlobalListers)(nil)

func TestDeleteOrphanedMaestroReadonlyBundles_getAllServiceProviderClusters_Pagination(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	spc1ResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/serviceProviderClusters/default"))
	spc2ResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub2/resourceGroups/rg2/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster2/serviceProviderClusters/default"))
	page1SPC := &api.ServiceProviderCluster{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: spc1ResourceID},
		ResourceID:     *spc1ResourceID,
	}
	page2SPC := &api.ServiceProviderCluster{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: spc2ResourceID},
		ResourceID:     *spc2ResourceID,
	}

	iter1 := database.NewMockDBClientIterator[api.ServiceProviderCluster](ctrl)
	iter1.EXPECT().Items(gomock.Any()).DoAndReturn(func(ctx context.Context) database.DBClientIteratorItem[api.ServiceProviderCluster] {
		return func(yield func(string, *api.ServiceProviderCluster) bool) {
			yield("id1", page1SPC)
		}
	})
	iter1.EXPECT().GetError().Return(nil)
	iter1.EXPECT().GetContinuationToken().Return("token1")

	iter2 := database.NewMockDBClientIterator[api.ServiceProviderCluster](ctrl)
	iter2.EXPECT().Items(gomock.Any()).DoAndReturn(func(ctx context.Context) database.DBClientIteratorItem[api.ServiceProviderCluster] {
		return func(yield func(string, *api.ServiceProviderCluster) bool) {
			yield("id2", page2SPC)
		}
	})
	iter2.EXPECT().GetError().Return(nil)
	iter2.EXPECT().GetContinuationToken().Return("")

	paginationListers := &paginationGlobalListers{
		spcLister: &paginationSPCGlobalLister{iter1: iter1, iter2: iter2, token: "token1"},
	}
	mockDB := database.NewMockDBClient(ctrl)
	mockDB.EXPECT().GlobalListers().Return(paginationListers).Times(2)

	c := &deleteOrphanedMaestroReadonlyBundles{cosmosClient: mockDB}
	all, err := c.getAllServiceProviderClusters(ctx)
	require.NoError(t, err)
	require.Len(t, all, 2)
	assert.Equal(t, spc1ResourceID.String(), all[0].ResourceID.String())
	assert.Equal(t, spc2ResourceID.String(), all[1].ResourceID.String())
}

func TestDeleteOrphanedMaestroReadonlyBundles_getAllServiceProviderClusters_IteratorError(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	iter := database.NewMockDBClientIterator[api.ServiceProviderCluster](ctrl)
	iter.EXPECT().Items(gomock.Any()).DoAndReturn(func(ctx context.Context) database.DBClientIteratorItem[api.ServiceProviderCluster] {
		return func(yield func(string, *api.ServiceProviderCluster) bool) {}
	})
	iterErr := fmt.Errorf("iteration error")
	iter.EXPECT().GetError().Return(iterErr)
	// GetContinuationToken is not called when GetError returns non-nil (controller returns early).

	mockDB := database.NewMockDBClient(ctrl)
	mockDB.EXPECT().GlobalListers().Return(&paginationGlobalListers{
		spcLister: &iteratorErrorSPCGlobalLister{iter: iter},
	})

	c := &deleteOrphanedMaestroReadonlyBundles{cosmosClient: mockDB}
	all, err := c.getAllServiceProviderClusters(ctx)
	require.Error(t, err)
	assert.Nil(t, all)
	assert.Contains(t, err.Error(), "failed iterating ServiceProviderClusters")
	assert.Contains(t, err.Error(), "iteration error")
}

// alwaysErrorGlobalListers is a test double that makes the returned global listers
// always return an error
type alwaysErrorGlobalListers struct {
	err error
}

func (f *alwaysErrorGlobalListers) Subscriptions() database.GlobalLister[arm.Subscription] {
	return &alwaysErrorGlobalLister[arm.Subscription]{err: f.err}
}
func (f *alwaysErrorGlobalListers) Clusters() database.GlobalLister[api.HCPOpenShiftCluster] {
	return &alwaysErrorGlobalLister[api.HCPOpenShiftCluster]{err: f.err}
}
func (f *alwaysErrorGlobalListers) NodePools() database.GlobalLister[api.HCPOpenShiftClusterNodePool] {
	return &alwaysErrorGlobalLister[api.HCPOpenShiftClusterNodePool]{err: f.err}
}
func (f *alwaysErrorGlobalListers) ExternalAuths() database.GlobalLister[api.HCPOpenShiftClusterExternalAuth] {
	return &alwaysErrorGlobalLister[api.HCPOpenShiftClusterExternalAuth]{err: f.err}
}
func (f *alwaysErrorGlobalListers) ServiceProviderClusters() database.GlobalLister[api.ServiceProviderCluster] {
	return &alwaysErrorGlobalLister[api.ServiceProviderCluster]{err: f.err}
}
func (f *alwaysErrorGlobalListers) Operations() database.GlobalLister[api.Operation] {
	return &alwaysErrorGlobalLister[api.Operation]{err: f.err}
}
func (f *alwaysErrorGlobalListers) ActiveOperations() database.GlobalLister[api.Operation] {
	return &alwaysErrorGlobalLister[api.Operation]{err: f.err}
}

func (f *alwaysErrorGlobalListers) Controllers() database.GlobalLister[api.Controller] {
	return &alwaysErrorGlobalLister[api.Controller]{err: f.err}
}
func (f *alwaysErrorGlobalListers) ServiceProviderNodePools() database.GlobalLister[api.ServiceProviderNodePool] {
	return &alwaysErrorGlobalLister[api.ServiceProviderNodePool]{err: f.err}
}

var _ database.GlobalListers = (*alwaysErrorGlobalListers)(nil)

type alwaysErrorGlobalLister[T any] struct {
	err error
}

func (f *alwaysErrorGlobalLister[T]) List(ctx context.Context, options *database.DBClientListResourceDocsOptions) (database.DBClientIterator[T], error) {
	return nil, f.err
}

var _ database.GlobalLister[any] = (*alwaysErrorGlobalLister[any])(nil)

func TestDeleteOrphanedMaestroReadonlyBundles_SyncOnce_ListServiceProviderClustersError(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := database.NewMockDBClient(ctrl)
	listErr := fmt.Errorf("list SPCs error")
	mockDB.EXPECT().GlobalListers().Return(&alwaysErrorGlobalListers{err: listErr})

	c := &deleteOrphanedMaestroReadonlyBundles{
		cosmosClient: mockDB,
	}
	err := c.SyncOnce(context.Background(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get all ServiceProviderClusters")
	assert.Contains(t, err.Error(), "list SPCs error")
}

func TestDeleteOrphanedMaestroReadonlyBundles_SyncOnce_NoServiceProviderClusters_Success(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	ctrl := gomock.NewController(t)
	mockDB := databasetesting.NewMockDBClient()
	mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
	mockCS.EXPECT().ListProvisionShards().Return(ocm.NewSimpleProvisionShardListIterator(nil, nil))
	controller := NewDeleteOrphanedMaestroReadonlyBundlesController(mockDB, mockCS, nil, "test-env")

	err := controller.SyncOnce(ctx, nil)
	require.NoError(t, err)
}

func TestDeleteOrphanedMaestroReadonlyBundles_buildProvisionShardToServiceProviderClustersIndex(t *testing.T) {
	ctx := context.Background()
	clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster"))
	spcResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/serviceProviderClusters/default"))

	tests := []struct {
		name        string
		setup       func(t *testing.T, ctrl *gomock.Controller) (c *deleteOrphanedMaestroReadonlyBundles, allSPCs []*api.ServiceProviderCluster)
		wantErr     bool
		errSubstr   string
		validateIdx func(t *testing.T, idx map[string]*provisionShardServiceProviderClusters)
	}{
		{
			name: "Get cluster error",
			setup: func(t *testing.T, ctrl *gomock.Controller) (*deleteOrphanedMaestroReadonlyBundles, []*api.ServiceProviderCluster) {
				t.Helper()
				mockDB := database.NewMockDBClient(ctrl)
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				mockClusters := database.NewMockHCPClusterCRUD(ctrl)
				spc := &api.ServiceProviderCluster{
					CosmosMetadata: arm.CosmosMetadata{ResourceID: spcResourceID},
					ResourceID:     *spcResourceID,
				}
				mockDB.EXPECT().HCPClusters("sub", "rg").Return(mockClusters)
				mockClusters.EXPECT().Get(gomock.Any(), "cluster").Return(nil, fmt.Errorf("cluster not found"))
				mockCS.EXPECT().ListProvisionShards().Return(ocm.NewSimpleProvisionShardListIterator(nil, nil))
				return &deleteOrphanedMaestroReadonlyBundles{cosmosClient: mockDB, clusterServiceClient: mockCS}, []*api.ServiceProviderCluster{spc}
			},
			wantErr:   true,
			errSubstr: "failed to get Cluster",
		},
		{
			name: "Get provision shard error",
			setup: func(t *testing.T, ctrl *gomock.Controller) (*deleteOrphanedMaestroReadonlyBundles, []*api.ServiceProviderCluster) {
				t.Helper()
				mockDB := databasetesting.NewMockDBClient()
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				cluster := &api.HCPOpenShiftCluster{
					TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: clusterResourceID}},
					ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
						ClusterServiceID: api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/csid")),
					},
				}
				_, err := mockDB.HCPClusters("sub", "rg").Create(ctx, cluster, nil)
				require.NoError(t, err)
				spc := &api.ServiceProviderCluster{
					CosmosMetadata: arm.CosmosMetadata{ResourceID: spcResourceID},
					ResourceID:     *spcResourceID,
				}
				mockCS.EXPECT().ListProvisionShards().Return(ocm.NewSimpleProvisionShardListIterator(nil, nil))
				mockCS.EXPECT().GetClusterProvisionShard(gomock.Any(), cluster.ServiceProviderProperties.ClusterServiceID).Return(nil, fmt.Errorf("provision shard error"))
				return &deleteOrphanedMaestroReadonlyBundles{cosmosClient: mockDB, clusterServiceClient: mockCS}, []*api.ServiceProviderCluster{spc}
			},
			wantErr:   true,
			errSubstr: "failed to get Cluster Provision Shard",
		},
		{
			name: "provision shard not in index",
			setup: func(t *testing.T, ctrl *gomock.Controller) (*deleteOrphanedMaestroReadonlyBundles, []*api.ServiceProviderCluster) {
				t.Helper()
				mockDB := databasetesting.NewMockDBClient()
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				mockMaestroBuilder := maestro.NewMockMaestroClientBuilder(ctrl)
				mockMaestro := maestro.NewMockClient(ctrl)
				cluster := &api.HCPOpenShiftCluster{
					TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: clusterResourceID}},
					ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
						ClusterServiceID: api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/csid")),
					},
				}
				_, err := mockDB.HCPClusters("sub", "rg").Create(ctx, cluster, nil)
				require.NoError(t, err)
				spc := &api.ServiceProviderCluster{
					CosmosMetadata: arm.CosmosMetadata{ResourceID: spcResourceID},
					ResourceID:     *spcResourceID,
				}
				shardInList := buildTestProvisionShard("consumer-in-list")
				shard2ID := "33333333333333333333333333333333"
				shardReturnedByCS, err := arohcpv1alpha1.NewProvisionShard().
					ID(shard2ID).
					MaestroConfig(
						arohcpv1alpha1.NewProvisionShardMaestroConfig().
							ConsumerName("other-consumer").
							RestApiConfig(arohcpv1alpha1.NewProvisionShardMaestroRestApiConfig().Url("https://other.example.com:443")).
							GrpcApiConfig(arohcpv1alpha1.NewProvisionShardMaestroGrpcApiConfig().Url("https://other.example.com:444")),
					).
					Build()
				require.NoError(t, err)
				mockCS.EXPECT().ListProvisionShards().Return(ocm.NewSimpleProvisionShardListIterator([]*arohcpv1alpha1.ProvisionShard{shardInList}, nil))
				mockMaestroBuilder.EXPECT().NewClient(gomock.Any(), shardInList.MaestroConfig().RestApiConfig().Url(), shardInList.MaestroConfig().GrpcApiConfig().Url(), shardInList.MaestroConfig().ConsumerName(), maestro.GenerateMaestroSourceID("test-env", shardInList.ID())).Return(mockMaestro, nil)
				mockCS.EXPECT().GetClusterProvisionShard(gomock.Any(), cluster.ServiceProviderProperties.ClusterServiceID).Return(shardReturnedByCS, nil)
				return &deleteOrphanedMaestroReadonlyBundles{
					cosmosClient:                       mockDB,
					clusterServiceClient:               mockCS,
					maestroClientBuilder:               mockMaestroBuilder,
					maestroSourceEnvironmentIdentifier: "test-env",
				}, []*api.ServiceProviderCluster{spc}
			},
			wantErr:   true,
			errSubstr: "not present in provision shards index",
		},
		{
			name: "success builds index and creates maestro client",
			setup: func(t *testing.T, ctrl *gomock.Controller) (*deleteOrphanedMaestroReadonlyBundles, []*api.ServiceProviderCluster) {
				t.Helper()
				mockDB := databasetesting.NewMockDBClient()
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				mockMaestroBuilder := maestro.NewMockMaestroClientBuilder(ctrl)
				mockMaestro := maestro.NewMockClient(ctrl)
				cluster := &api.HCPOpenShiftCluster{
					TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: clusterResourceID}},
					ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
						ClusterServiceID: api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/csid")),
					},
				}
				_, err := mockDB.HCPClusters("sub", "rg").Create(ctx, cluster, nil)
				require.NoError(t, err)
				spc := &api.ServiceProviderCluster{
					CosmosMetadata: arm.CosmosMetadata{ResourceID: spcResourceID},
					ResourceID:     *spcResourceID,
				}
				provisionShard := buildTestProvisionShard("test-consumer")
				mockCS.EXPECT().ListProvisionShards().Return(ocm.NewSimpleProvisionShardListIterator([]*arohcpv1alpha1.ProvisionShard{provisionShard}, nil))
				restEndpoint := provisionShard.MaestroConfig().RestApiConfig().Url()
				grpcEndpoint := provisionShard.MaestroConfig().GrpcApiConfig().Url()
				consumerName := provisionShard.MaestroConfig().ConsumerName()
				sourceID := maestro.GenerateMaestroSourceID("test-env", provisionShard.ID())
				mockMaestroBuilder.EXPECT().NewClient(gomock.Any(), restEndpoint, grpcEndpoint, consumerName, sourceID).Return(mockMaestro, nil)
				mockCS.EXPECT().GetClusterProvisionShard(gomock.Any(), cluster.ServiceProviderProperties.ClusterServiceID).Return(provisionShard, nil)
				return &deleteOrphanedMaestroReadonlyBundles{
					cosmosClient:                       mockDB,
					clusterServiceClient:               mockCS,
					maestroClientBuilder:               mockMaestroBuilder,
					maestroSourceEnvironmentIdentifier: "test-env",
				}, []*api.ServiceProviderCluster{spc}
			},
			validateIdx: func(t *testing.T, idx map[string]*provisionShardServiceProviderClusters) {
				t.Helper()
				require.Len(t, idx, 1)
				provisionShard := buildTestProvisionShard("test-consumer")
				entry, ok := idx[provisionShard.ID()]
				require.True(t, ok)
				require.Len(t, entry.serviceProviderClusters, 1)
				assert.Equal(t, spcResourceID.String(), entry.serviceProviderClusters[0].ResourceID.String())
			},
		},
		{
			name: "multiple provision shards each get own index entry",
			setup: func(t *testing.T, ctrl *gomock.Controller) (*deleteOrphanedMaestroReadonlyBundles, []*api.ServiceProviderCluster) {
				t.Helper()
				mockDB := databasetesting.NewMockDBClient()
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				mockMaestroBuilder := maestro.NewMockMaestroClientBuilder(ctrl)
				mockMaestro1 := maestro.NewMockClient(ctrl)
				mockMaestro2 := maestro.NewMockClient(ctrl)

				cluster1ResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster1"))
				cluster2ResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub2/resourceGroups/rg2/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster2"))
				cluster1 := &api.HCPOpenShiftCluster{
					TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: cluster1ResourceID}},
					ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
						ClusterServiceID: api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/csid1")),
					},
				}
				cluster2 := &api.HCPOpenShiftCluster{
					TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: cluster2ResourceID}},
					ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
						ClusterServiceID: api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/csid2")),
					},
				}
				_, err := mockDB.HCPClusters("sub1", "rg1").Create(ctx, cluster1, nil)
				require.NoError(t, err)
				_, err = mockDB.HCPClusters("sub2", "rg2").Create(ctx, cluster2, nil)
				require.NoError(t, err)

				spc1ResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster1/serviceProviderClusters/default"))
				spc2ResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub2/resourceGroups/rg2/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster2/serviceProviderClusters/default"))
				spc1 := &api.ServiceProviderCluster{
					CosmosMetadata: arm.CosmosMetadata{ResourceID: spc1ResourceID},
					ResourceID:     *spc1ResourceID,
				}
				spc2 := &api.ServiceProviderCluster{
					CosmosMetadata: arm.CosmosMetadata{ResourceID: spc2ResourceID},
					ResourceID:     *spc2ResourceID,
				}

				shard1 := buildTestProvisionShard("consumer1")
				shard2ID := "33333333333333333333333333333333"
				shard2, err := arohcpv1alpha1.NewProvisionShard().
					ID(shard2ID).
					MaestroConfig(
						arohcpv1alpha1.NewProvisionShardMaestroConfig().
							ConsumerName("consumer2").
							RestApiConfig(arohcpv1alpha1.NewProvisionShardMaestroRestApiConfig().Url("https://maestro2.example.com:443")).
							GrpcApiConfig(arohcpv1alpha1.NewProvisionShardMaestroGrpcApiConfig().Url("https://maestro2.example.com:444")),
					).
					Build()
				require.NoError(t, err)

				mockCS.EXPECT().ListProvisionShards().Return(ocm.NewSimpleProvisionShardListIterator([]*arohcpv1alpha1.ProvisionShard{shard1, shard2}, nil))
				mockMaestroBuilder.EXPECT().NewClient(gomock.Any(), shard1.MaestroConfig().RestApiConfig().Url(), shard1.MaestroConfig().GrpcApiConfig().Url(), shard1.MaestroConfig().ConsumerName(), maestro.GenerateMaestroSourceID("test-env", shard1.ID())).Return(mockMaestro1, nil)
				mockMaestroBuilder.EXPECT().NewClient(gomock.Any(), shard2.MaestroConfig().RestApiConfig().Url(), shard2.MaestroConfig().GrpcApiConfig().Url(), shard2.MaestroConfig().ConsumerName(), maestro.GenerateMaestroSourceID("test-env", shard2.ID())).Return(mockMaestro2, nil)
				mockCS.EXPECT().GetClusterProvisionShard(gomock.Any(), cluster1.ServiceProviderProperties.ClusterServiceID).Return(shard1, nil)
				mockCS.EXPECT().GetClusterProvisionShard(gomock.Any(), cluster2.ServiceProviderProperties.ClusterServiceID).Return(shard2, nil)

				return &deleteOrphanedMaestroReadonlyBundles{
					cosmosClient:                       mockDB,
					clusterServiceClient:               mockCS,
					maestroClientBuilder:               mockMaestroBuilder,
					maestroSourceEnvironmentIdentifier: "test-env",
				}, []*api.ServiceProviderCluster{spc1, spc2}
			},
			validateIdx: func(t *testing.T, idx map[string]*provisionShardServiceProviderClusters) {
				t.Helper()
				require.Len(t, idx, 2)
				shard1 := buildTestProvisionShard("consumer1")
				entry1, ok := idx[shard1.ID()]
				require.True(t, ok)
				require.Len(t, entry1.serviceProviderClusters, 1)
				spc1RID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster1/serviceProviderClusters/default"))
				assert.Equal(t, spc1RID.String(), entry1.serviceProviderClusters[0].ResourceID.String())
				shard2ID := "33333333333333333333333333333333"
				entry2, ok := idx[shard2ID]
				require.True(t, ok)
				require.Len(t, entry2.serviceProviderClusters, 1)
				spc2RID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub2/resourceGroups/rg2/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster2/serviceProviderClusters/default"))
				assert.Equal(t, spc2RID.String(), entry2.serviceProviderClusters[0].ResourceID.String())
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			c, allSPCs := tt.setup(t, ctrl)
			idx, err := c.buildProvisionShardToServiceProviderClustersIndex(ctx, allSPCs)
			defer cancelMaestroClientsInIndex(idx)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			} else {
				require.NoError(t, err)
				require.NotNil(t, idx)
				if tt.validateIdx != nil {
					tt.validateIdx(t, idx)
				}
			}
		})
	}
}

func TestDeleteOrphanedMaestroReadonlyBundles_ensureOrphanedMaestroReadonlyBundlesAreDeleted(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	spcResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/serviceProviderClusters/default"))

	tests := []struct {
		name      string
		setupMock func(*maestro.MockClient) map[string]*provisionShardServiceProviderClusters
		wantErr   bool
		errSubstr string
	}{
		{
			name: "empty index success",
			setupMock: func(*maestro.MockClient) map[string]*provisionShardServiceProviderClusters {
				return nil
			},
		},
		{
			name: "list error",
			setupMock: func(m *maestro.MockClient) map[string]*provisionShardServiceProviderClusters {
				m.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, fmt.Errorf("maestro list error"))
				return map[string]*provisionShardServiceProviderClusters{
					"shard-1": {maestroClient: m, maestroClientCancelFunc: func() {}, serviceProviderClusters: []*api.ServiceProviderCluster{}},
				}
			},
			wantErr:   true,
			errSubstr: "failed to list Maestro Bundles",
		},
		{
			name: "skips bundle without readonly managed-by label",
			setupMock: func(m *maestro.MockClient) map[string]*provisionShardServiceProviderClusters {
				bundleList := &workv1.ManifestWorkList{
					Items: []workv1.ManifestWork{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "other-bundle",
								Namespace: "consumer",
								Labels:    map[string]string{readonlyBundleManagedByK8sLabelKey: "other-value"},
							},
						},
					},
				}
				m.EXPECT().List(gomock.Any(), gomock.Any()).Return(bundleList, nil)
				return map[string]*provisionShardServiceProviderClusters{
					"shard-1": {maestroClient: m, maestroClientCancelFunc: func() {}, serviceProviderClusters: []*api.ServiceProviderCluster{}},
				}
			},
		},
		{
			name: "skips referenced bundle",
			setupMock: func(m *maestro.MockClient) map[string]*provisionShardServiceProviderClusters {
				spc := &api.ServiceProviderCluster{
					ResourceID: *spcResourceID,
					Status: api.ServiceProviderClusterStatus{
						MaestroReadonlyBundles: api.MaestroBundleReferenceList{
							{MaestroAPIMaestroBundleName: "referenced-bundle"},
						},
					},
				}
				bundleList := &workv1.ManifestWorkList{
					Items: []workv1.ManifestWork{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "referenced-bundle",
								Namespace: "consumer",
								Labels:    map[string]string{readonlyBundleManagedByK8sLabelKey: readonlyBundleManagedByK8sLabelValue},
							},
						},
					},
				}
				m.EXPECT().List(gomock.Any(), gomock.Any()).Return(bundleList, nil)
				return map[string]*provisionShardServiceProviderClusters{
					"shard-1": {maestroClient: m, maestroClientCancelFunc: func() {}, serviceProviderClusters: []*api.ServiceProviderCluster{spc}},
				}
			},
		},
		{
			name: "deletes orphaned bundle",
			setupMock: func(m *maestro.MockClient) map[string]*provisionShardServiceProviderClusters {
				spc := &api.ServiceProviderCluster{
					ResourceID: *spcResourceID,
					Status: api.ServiceProviderClusterStatus{
						MaestroReadonlyBundles: api.MaestroBundleReferenceList{
							{MaestroAPIMaestroBundleName: "referenced-bundle"},
						},
					},
				}
				bundleList := &workv1.ManifestWorkList{
					Items: []workv1.ManifestWork{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "orphaned-bundle",
								Namespace: "consumer",
								Labels:    map[string]string{readonlyBundleManagedByK8sLabelKey: readonlyBundleManagedByK8sLabelValue},
							},
						},
					},
				}
				m.EXPECT().List(gomock.Any(), gomock.Any()).Return(bundleList, nil)
				m.EXPECT().Delete(gomock.Any(), "orphaned-bundle", gomock.Any()).Return(nil)
				return map[string]*provisionShardServiceProviderClusters{
					"shard-1": {maestroClient: m, maestroClientCancelFunc: func() {}, serviceProviderClusters: []*api.ServiceProviderCluster{spc}},
				}
			},
		},
		{
			name: "delete error joined but not fatal",
			setupMock: func(m *maestro.MockClient) map[string]*provisionShardServiceProviderClusters {
				bundleList := &workv1.ManifestWorkList{
					Items: []workv1.ManifestWork{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "orphaned-bundle",
								Namespace: "consumer",
								Labels:    map[string]string{readonlyBundleManagedByK8sLabelKey: readonlyBundleManagedByK8sLabelValue},
							},
						},
					},
				}
				m.EXPECT().List(gomock.Any(), gomock.Any()).Return(bundleList, nil)
				m.EXPECT().Delete(gomock.Any(), "orphaned-bundle", gomock.Any()).Return(fmt.Errorf("delete failed"))
				return map[string]*provisionShardServiceProviderClusters{
					"shard-1": {maestroClient: m, maestroClientCancelFunc: func() {}, serviceProviderClusters: []*api.ServiceProviderCluster{}},
				}
			},
			wantErr:   true,
			errSubstr: "failed to delete Maestro Bundle",
		},
		{
			name: "pagination lists and deletes across pages",
			setupMock: func(m *maestro.MockClient) map[string]*provisionShardServiceProviderClusters {
				page1 := &workv1.ManifestWorkList{
					ListMeta: metav1.ListMeta{Continue: "token"},
					Items: []workv1.ManifestWork{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "orphan-page1",
								Namespace: "consumer",
								Labels:    map[string]string{readonlyBundleManagedByK8sLabelKey: readonlyBundleManagedByK8sLabelValue},
							},
						},
					},
				}
				page2 := &workv1.ManifestWorkList{Items: []workv1.ManifestWork{}}
				labelSelector := fmt.Sprintf("%s=%s", readonlyBundleManagedByK8sLabelKey, readonlyBundleManagedByK8sLabelValue)
				m.EXPECT().List(gomock.Any(), metav1.ListOptions{Limit: 400, Continue: "", LabelSelector: labelSelector}).Return(page1, nil)
				m.EXPECT().Delete(gomock.Any(), "orphan-page1", gomock.Any()).Return(nil)
				m.EXPECT().List(gomock.Any(), metav1.ListOptions{Limit: 400, Continue: "token", LabelSelector: labelSelector}).Return(page2, nil)
				return map[string]*provisionShardServiceProviderClusters{
					"shard-1": {maestroClient: m, maestroClientCancelFunc: func() {}, serviceProviderClusters: []*api.ServiceProviderCluster{}},
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockMaestro := maestro.NewMockClient(ctrl)
			index := tt.setupMock(mockMaestro)
			c := &deleteOrphanedMaestroReadonlyBundles{}
			err := c.ensureOrphanedMaestroReadonlyBundlesAreDeleted(ctx, index)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestDeleteOrphanedMaestroReadonlyBundles_ensureOrphanedMaestroReadonlyBundlesAreDeleted_BundleReferencedOnOtherShardDeletedOnThisShard
// verifies that when processing a shard, a bundle whose name is referenced by an SPC on a different shard
// (but not by any SPC on this shard) is deleted as orphaned on this shard.
func TestDeleteOrphanedMaestroReadonlyBundles_ensureOrphanedMaestroReadonlyBundlesAreDeleted_BundleReferencedOnOtherShardDeletedOnThisShard(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	ctrl := gomock.NewController(t)
	mockShard1 := maestro.NewMockClient(ctrl)
	mockShard2 := maestro.NewMockClient(ctrl)

	spcOnShard1ResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster1/serviceProviderClusters/default"))
	spcOnShard1 := &api.ServiceProviderCluster{
		ResourceID: *spcOnShard1ResourceID,
		Status: api.ServiceProviderClusterStatus{
			MaestroReadonlyBundles: api.MaestroBundleReferenceList{
				{MaestroAPIMaestroBundleName: "bundle-X"},
			},
		},
	}

	index := map[string]*provisionShardServiceProviderClusters{
		"shard-1": {maestroClient: mockShard1, maestroClientCancelFunc: func() {}, serviceProviderClusters: []*api.ServiceProviderCluster{spcOnShard1}},
		"shard-2": {maestroClient: mockShard2, maestroClientCancelFunc: func() {}, serviceProviderClusters: []*api.ServiceProviderCluster{}},
	}

	// Shard 1: list returns empty (no orphaned bundles there).
	mockShard1.EXPECT().List(gomock.Any(), gomock.Any()).Return(&workv1.ManifestWorkList{Items: []workv1.ManifestWork{}}, nil)

	// Shard 2: list returns a bundle named "bundle-X". No SPC on shard-2 references it, so it is orphaned on this shard and must be deleted.
	bundleListShard2 := &workv1.ManifestWorkList{
		Items: []workv1.ManifestWork{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "bundle-X",
					Namespace: "consumer2",
					Labels:    map[string]string{readonlyBundleManagedByK8sLabelKey: readonlyBundleManagedByK8sLabelValue},
				},
			},
		},
	}
	mockShard2.EXPECT().List(gomock.Any(), gomock.Any()).Return(bundleListShard2, nil)
	mockShard2.EXPECT().Delete(gomock.Any(), "bundle-X", gomock.Any()).Return(nil)

	c := &deleteOrphanedMaestroReadonlyBundles{}
	err := c.ensureOrphanedMaestroReadonlyBundlesAreDeleted(ctx, index)
	require.NoError(t, err)
}

func TestDeleteOrphanedMaestroReadonlyBundles_SyncOnce_FullFlow_DeletesOrphanedBundle(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	ctrl := gomock.NewController(t)
	mockDB := databasetesting.NewMockDBClient()
	mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
	mockMaestroBuilder := maestro.NewMockMaestroClientBuilder(ctrl)
	mockMaestro := maestro.NewMockClient(ctrl)

	clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster"))
	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: clusterResourceID}},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/csid")),
		},
	}
	clustersCRUD := mockDB.HCPClusters(clusterResourceID.SubscriptionID, clusterResourceID.ResourceGroupName)
	_, err := clustersCRUD.Create(ctx, cluster, nil)
	require.NoError(t, err)

	spcResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/serviceProviderClusters/default"))
	spc := &api.ServiceProviderCluster{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: spcResourceID},
		ResourceID:     *spcResourceID,
		Status: api.ServiceProviderClusterStatus{
			MaestroReadonlyBundles: api.MaestroBundleReferenceList{
				{MaestroAPIMaestroBundleName: "kept-bundle"},
			},
		},
	}
	spcCRUD := mockDB.ServiceProviderClusters(clusterResourceID.SubscriptionID, clusterResourceID.ResourceGroupName, clusterResourceID.Name)
	_, err = spcCRUD.Create(ctx, spc, nil)
	require.NoError(t, err)

	provisionShard := buildTestProvisionShard("test-consumer")
	mockCS.EXPECT().ListProvisionShards().Return(ocm.NewSimpleProvisionShardListIterator([]*arohcpv1alpha1.ProvisionShard{provisionShard}, nil))
	mockCS.EXPECT().GetClusterProvisionShard(gomock.Any(), cluster.ServiceProviderProperties.ClusterServiceID).Return(provisionShard, nil)
	restEndpoint := provisionShard.MaestroConfig().RestApiConfig().Url()
	grpcEndpoint := provisionShard.MaestroConfig().GrpcApiConfig().Url()
	consumerName := provisionShard.MaestroConfig().ConsumerName()
	sourceID := maestro.GenerateMaestroSourceID("test-env", provisionShard.ID())
	mockMaestroBuilder.EXPECT().NewClient(gomock.Any(), restEndpoint, grpcEndpoint, consumerName, sourceID).Return(mockMaestro, nil)

	bundleList := &workv1.ManifestWorkList{
		Items: []workv1.ManifestWork{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kept-bundle",
					Namespace: "consumer",
					Labels:    map[string]string{readonlyBundleManagedByK8sLabelKey: readonlyBundleManagedByK8sLabelValue},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "orphaned-bundle",
					Namespace: "consumer",
					Labels:    map[string]string{readonlyBundleManagedByK8sLabelKey: readonlyBundleManagedByK8sLabelValue},
				},
			},
		},
	}
	mockMaestro.EXPECT().List(gomock.Any(), gomock.Any()).Return(bundleList, nil)
	mockMaestro.EXPECT().Delete(gomock.Any(), "orphaned-bundle", gomock.Any()).Return(nil)

	controller := NewDeleteOrphanedMaestroReadonlyBundlesController(mockDB, mockCS, mockMaestroBuilder, "test-env")
	err = controller.SyncOnce(ctx, nil)
	require.NoError(t, err)
}
