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
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

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

func TestBuildClusterScopedMaestroAPIMaestroBundleNamesByShard(t *testing.T) {
	spcRID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/serviceProviderClusters/default"))

	tests := []struct {
		name           string
		spcsByShard    map[string][]*api.ServiceProviderCluster
		wantErr        bool
		errSubstr      string
		wantShard      string
		wantBundleName string
	}{
		{
			name: "empty maestroAPIMaestroBundleName",
			spcsByShard: map[string][]*api.ServiceProviderCluster{
				"shard1": {
					{
						ResourceID: *spcRID,
						Status: api.ServiceProviderClusterStatus{
							MaestroReadonlyBundles: api.MaestroBundleReferenceList{
								{Name: "logical-name", MaestroAPIMaestroBundleName: ""},
							},
						},
					},
				},
			},
			wantErr:   true,
			errSubstr: "has empty maestroAPIMaestroBundleName",
		},
		{
			name: "nil ref entry",
			spcsByShard: map[string][]*api.ServiceProviderCluster{
				"shard1": {
					{
						ResourceID: *spcRID,
						Status: api.ServiceProviderClusterStatus{
							MaestroReadonlyBundles: api.MaestroBundleReferenceList{nil},
						},
					},
				},
			},
			wantErr:   true,
			errSubstr: "is nil",
		},
		{
			name: "nil ServiceProviderCluster",
			spcsByShard: map[string][]*api.ServiceProviderCluster{
				"shard1": {nil},
			},
			wantErr:   true,
			errSubstr: "nil ServiceProviderCluster",
		},
		{
			name: "success",
			spcsByShard: map[string][]*api.ServiceProviderCluster{
				"s": {
					{
						ResourceID: *spcRID,
						Status: api.ServiceProviderClusterStatus{
							MaestroReadonlyBundles: api.MaestroBundleReferenceList{
								{MaestroAPIMaestroBundleName: "bundle-one"},
							},
						},
					},
				},
			},
			wantShard:      "s",
			wantBundleName: "bundle-one",
		},
	}
	c := &deleteOrphanedMaestroReadonlyBundles{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := c.buildClusterScopedMaestroAPIMaestroBundleNamesByShard(tt.spcsByShard)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
				return
			}
			require.NoError(t, err)
			shardSet := out[tt.wantShard]
			require.NotNil(t, shardSet, "expected shard %q in result", tt.wantShard)
			_, ok := shardSet[tt.wantBundleName]
			assert.True(t, ok, "expected bundle name %q under shard %q", tt.wantBundleName, tt.wantShard)
		})
	}
}

func TestBuildNodePoolScopedMaestroAPIMaestroBundleNamesByShard(t *testing.T) {
	spnpRID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/nodePools/np/serviceProviderNodePools/default"))

	tests := []struct {
		name           string
		spnpsByShard   map[string][]*api.ServiceProviderNodePool
		wantErr        bool
		errSubstr      string
		wantShard      string
		wantBundleName string
	}{
		{
			name: "empty maestroAPIMaestroBundleName",
			spnpsByShard: map[string][]*api.ServiceProviderNodePool{
				"shard1": {
					{
						ResourceID: *spnpRID,
						Status: api.ServiceProviderNodePoolStatus{
							MaestroReadonlyBundles: api.MaestroBundleReferenceList{
								{Name: "logical-name", MaestroAPIMaestroBundleName: ""},
							},
						},
					},
				},
			},
			wantErr:   true,
			errSubstr: "has empty maestroAPIMaestroBundleName",
		},
		{
			name: "nil ref entry",
			spnpsByShard: map[string][]*api.ServiceProviderNodePool{
				"shard1": {
					{
						ResourceID: *spnpRID,
						Status: api.ServiceProviderNodePoolStatus{
							MaestroReadonlyBundles: api.MaestroBundleReferenceList{nil},
						},
					},
				},
			},
			wantErr:   true,
			errSubstr: "is nil",
		},
		{
			name: "nil ServiceProviderNodePool",
			spnpsByShard: map[string][]*api.ServiceProviderNodePool{
				"shard1": {nil},
			},
			wantErr:   true,
			errSubstr: "nil ServiceProviderNodePool",
		},
		{
			name: "success",
			spnpsByShard: map[string][]*api.ServiceProviderNodePool{
				"s": {
					{
						ResourceID: *spnpRID,
						Status: api.ServiceProviderNodePoolStatus{
							MaestroReadonlyBundles: api.MaestroBundleReferenceList{
								{MaestroAPIMaestroBundleName: "bundle-np"},
							},
						},
					},
				},
			},
			wantShard:      "s",
			wantBundleName: "bundle-np",
		},
	}
	c := &deleteOrphanedMaestroReadonlyBundles{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := c.buildNodePoolScopedMaestroAPIMaestroBundleNamesByShard(tt.spnpsByShard)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
				return
			}
			require.NoError(t, err)
			shardSet := out[tt.wantShard]
			require.NotNil(t, shardSet, "expected shard %q in result", tt.wantShard)
			_, ok := shardSet[tt.wantBundleName]
			assert.True(t, ok, "expected bundle name %q under shard %q", tt.wantBundleName, tt.wantShard)
		})
	}
}

// panicGlobalLister is a GlobalLister that panics if List is called (used for unused listers in test doubles).
type panicGlobalLister[T any] struct{}

func (n *panicGlobalLister[T]) List(context.Context, *database.DBClientListResourceDocsOptions) (database.DBClientIterator[T], error) {
	panic("panicGlobalLister.List should not be called")
}

// defaultPanicResourcesGlobalListers implements database.ResourcesGlobalListers with panic-on-List listers for every resource type.
// Embed it in a test double and override only the accessors the test cares about.
type defaultPanicResourcesGlobalListers struct{}

func (defaultPanicResourcesGlobalListers) Subscriptions() database.GlobalLister[arm.Subscription] {
	return &panicGlobalLister[arm.Subscription]{}
}
func (defaultPanicResourcesGlobalListers) Clusters() database.GlobalLister[api.HCPOpenShiftCluster] {
	return &panicGlobalLister[api.HCPOpenShiftCluster]{}
}
func (defaultPanicResourcesGlobalListers) NodePools() database.GlobalLister[api.HCPOpenShiftClusterNodePool] {
	return &panicGlobalLister[api.HCPOpenShiftClusterNodePool]{}
}
func (defaultPanicResourcesGlobalListers) ExternalAuths() database.GlobalLister[api.HCPOpenShiftClusterExternalAuth] {
	return &panicGlobalLister[api.HCPOpenShiftClusterExternalAuth]{}
}
func (defaultPanicResourcesGlobalListers) ServiceProviderClusters() database.GlobalLister[api.ServiceProviderCluster] {
	return &panicGlobalLister[api.ServiceProviderCluster]{}
}
func (defaultPanicResourcesGlobalListers) ServiceProviderNodePools() database.GlobalLister[api.ServiceProviderNodePool] {
	return &panicGlobalLister[api.ServiceProviderNodePool]{}
}
func (defaultPanicResourcesGlobalListers) Controllers() database.GlobalLister[api.Controller] {
	return &panicGlobalLister[api.Controller]{}
}
func (defaultPanicResourcesGlobalListers) ManagementClusterContents() database.GlobalLister[api.ManagementClusterContent] {
	return &panicGlobalLister[api.ManagementClusterContent]{}
}
func (defaultPanicResourcesGlobalListers) Operations() database.GlobalLister[api.Operation] {
	return &panicGlobalLister[api.Operation]{}
}
func (defaultPanicResourcesGlobalListers) ActiveOperations() database.GlobalLister[api.Operation] {
	return &panicGlobalLister[api.Operation]{}
}

var _ database.ResourcesGlobalListers = defaultPanicResourcesGlobalListers{}

// simpleIterator is a simple iterator implementation for testing that doesn't use gomock.
type simpleIterator[T any] struct {
	ids               []string
	items             []*T
	continuationToken string
	err               error
}

func (s *simpleIterator[T]) Items(ctx context.Context) database.DBClientIteratorItem[T] {
	return func(yield func(string, *T) bool) {
		for i, item := range s.items {
			if !yield(s.ids[i], item) {
				return
			}
		}
	}
}

func (s *simpleIterator[T]) GetContinuationToken() string {
	return s.continuationToken
}

func (s *simpleIterator[T]) GetError() error {
	return s.err
}

var _ database.DBClientIterator[api.ServiceProviderCluster] = &simpleIterator[api.ServiceProviderCluster]{}

func TestDeleteOrphanedMaestroReadonlyBundles_getAllServiceProviderClusters(t *testing.T) {
	ctx := context.Background()
	clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster"))
	spcResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/serviceProviderClusters/default"))

	tests := []struct {
		name                string
		setupDB             func(t *testing.T, ctx context.Context, mockResourcesDBClient *databasetesting.MockResourcesDBClient)
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
			setupDB: func(t *testing.T, ctx context.Context, mockResourcesDBClient *databasetesting.MockResourcesDBClient) {
				spc := &api.ServiceProviderCluster{
					CosmosMetadata: arm.CosmosMetadata{ResourceID: spcResourceID},
					ResourceID:     *spcResourceID,
				}
				spcCRUD := mockResourcesDBClient.ServiceProviderClusters(clusterResourceID.SubscriptionID, clusterResourceID.ResourceGroupName, clusterResourceID.Name)
				_, err := spcCRUD.Create(ctx, spc, nil)
				require.NoError(t, err)
			},
			wantLen:             1,
			wantFirstResourceID: spcResourceID.String(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
			if tt.setupDB != nil {
				tt.setupDB(t, ctx, mockResourcesDBClient)
			}
			c := &deleteOrphanedMaestroReadonlyBundles{resourcesDBClient: mockResourcesDBClient}
			all, err := c.getAllServiceProviderClusters(ctx)
			require.NoError(t, err)
			require.Len(t, all, tt.wantLen)
			if tt.wantFirstResourceID != "" {
				assert.Equal(t, tt.wantFirstResourceID, all[0].ResourceID.String())
			}
		})
	}
}

var _ database.DBClientIterator[api.ServiceProviderNodePool] = &simpleIterator[api.ServiceProviderNodePool]{}

func TestDeleteOrphanedMaestroReadonlyBundles_getAllServiceProviderNodePools(t *testing.T) {
	ctx := context.Background()
	clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster"))
	spnpResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/nodePools/worker/serviceProviderNodePools/default"))

	tests := []struct {
		name                string
		setupDB             func(t *testing.T, ctx context.Context, mockResourcesDBClient *databasetesting.MockResourcesDBClient)
		wantLen             int
		wantFirstResourceID string
	}{
		{
			name:    "empty DB returns no SPNPs",
			setupDB: nil,
			wantLen: 0,
		},
		{
			name: "returns SPNPs created via CRUD",
			setupDB: func(t *testing.T, ctx context.Context, mockResourcesDBClient *databasetesting.MockResourcesDBClient) {
				spnp := &api.ServiceProviderNodePool{
					CosmosMetadata: arm.CosmosMetadata{ResourceID: spnpResourceID},
					ResourceID:     *spnpResourceID,
				}
				spnpCRUD := mockResourcesDBClient.ServiceProviderNodePools(clusterResourceID.SubscriptionID, clusterResourceID.ResourceGroupName, clusterResourceID.Name, "worker")
				_, err := spnpCRUD.Create(ctx, spnp, nil)
				require.NoError(t, err)
			},
			wantLen:             1,
			wantFirstResourceID: spnpResourceID.String(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
			if tt.setupDB != nil {
				tt.setupDB(t, ctx, mockResourcesDBClient)
			}
			c := &deleteOrphanedMaestroReadonlyBundles{resourcesDBClient: mockResourcesDBClient}
			all, err := c.getAllServiceProviderNodePools(ctx)
			require.NoError(t, err)
			require.Len(t, all, tt.wantLen)
			if tt.wantFirstResourceID != "" {
				assert.Equal(t, tt.wantFirstResourceID, all[0].ResourceID.String())
			}
		})
	}
}

// alwaysErrorResourcesGlobalListers is a test double that makes the returned global listers
// always return an error
type alwaysErrorResourcesGlobalListers struct {
	err error
}

func (f *alwaysErrorResourcesGlobalListers) Subscriptions() database.GlobalLister[arm.Subscription] {
	return &alwaysErrorGlobalLister[arm.Subscription]{err: f.err}
}
func (f *alwaysErrorResourcesGlobalListers) Clusters() database.GlobalLister[api.HCPOpenShiftCluster] {
	return &alwaysErrorGlobalLister[api.HCPOpenShiftCluster]{err: f.err}
}
func (f *alwaysErrorResourcesGlobalListers) NodePools() database.GlobalLister[api.HCPOpenShiftClusterNodePool] {
	return &alwaysErrorGlobalLister[api.HCPOpenShiftClusterNodePool]{err: f.err}
}
func (f *alwaysErrorResourcesGlobalListers) ExternalAuths() database.GlobalLister[api.HCPOpenShiftClusterExternalAuth] {
	return &alwaysErrorGlobalLister[api.HCPOpenShiftClusterExternalAuth]{err: f.err}
}
func (f *alwaysErrorResourcesGlobalListers) ServiceProviderClusters() database.GlobalLister[api.ServiceProviderCluster] {
	return &alwaysErrorGlobalLister[api.ServiceProviderCluster]{err: f.err}
}
func (f *alwaysErrorResourcesGlobalListers) Operations() database.GlobalLister[api.Operation] {
	return &alwaysErrorGlobalLister[api.Operation]{err: f.err}
}
func (f *alwaysErrorResourcesGlobalListers) ActiveOperations() database.GlobalLister[api.Operation] {
	return &alwaysErrorGlobalLister[api.Operation]{err: f.err}
}

func (f *alwaysErrorResourcesGlobalListers) Controllers() database.GlobalLister[api.Controller] {
	return &alwaysErrorGlobalLister[api.Controller]{err: f.err}
}
func (f *alwaysErrorResourcesGlobalListers) ManagementClusterContents() database.GlobalLister[api.ManagementClusterContent] {
	return &alwaysErrorGlobalLister[api.ManagementClusterContent]{err: f.err}
}
func (f *alwaysErrorResourcesGlobalListers) ServiceProviderNodePools() database.GlobalLister[api.ServiceProviderNodePool] {
	return &alwaysErrorGlobalLister[api.ServiceProviderNodePool]{err: f.err}
}

var _ database.ResourcesGlobalListers = (*alwaysErrorResourcesGlobalListers)(nil)

type alwaysErrorGlobalLister[T any] struct {
	err error
}

func (f *alwaysErrorGlobalLister[T]) List(ctx context.Context, options *database.DBClientListResourceDocsOptions) (database.DBClientIterator[T], error) {
	return nil, f.err
}

var _ database.GlobalLister[any] = (*alwaysErrorGlobalLister[any])(nil)

// emptyGlobalLister returns an empty page for global list tests.
type emptyGlobalLister[T any] struct{}

func (e *emptyGlobalLister[T]) List(ctx context.Context, opts *database.DBClientListResourceDocsOptions) (database.DBClientIterator[T], error) {
	return &simpleIterator[T]{}, nil
}

// failOnSecondSPNPGlobalLister fails ServiceProviderNodePools.List starting on the second call (initial list inside nodepool ensure succeeds, fresh list inside nodepool delete pass fails).
type failOnSecondSPNPGlobalLister struct {
	call int
	err  error
}

func (f *failOnSecondSPNPGlobalLister) List(ctx context.Context, opts *database.DBClientListResourceDocsOptions) (database.DBClientIterator[api.ServiceProviderNodePool], error) {
	f.call++
	if f.call >= 2 {
		return nil, f.err
	}
	return &simpleIterator[api.ServiceProviderNodePool]{}, nil
}

// syncOnceSPCOKFailSecondSPNPResourcesGlobalListers lists empty SPCs always, and fails the second global SPNP list during nodepool ensure (for SyncOnce integration).
type syncOnceSPCOKFailSecondSPNPResourcesGlobalListers struct {
	defaultPanicResourcesGlobalListers
	spnp *failOnSecondSPNPGlobalLister
}

func (g *syncOnceSPCOKFailSecondSPNPResourcesGlobalListers) ServiceProviderClusters() database.GlobalLister[api.ServiceProviderCluster] {
	return &emptyGlobalLister[api.ServiceProviderCluster]{}
}

func (g *syncOnceSPCOKFailSecondSPNPResourcesGlobalListers) ServiceProviderNodePools() database.GlobalLister[api.ServiceProviderNodePool] {
	return g.spnp
}

var _ database.ResourcesGlobalListers = (*syncOnceSPCOKFailSecondSPNPResourcesGlobalListers)(nil)

// failOnSecondServiceProviderClusterGlobalLister fails ServiceProviderClusters.List starting on the second call.
type failOnSecondServiceProviderClusterGlobalLister struct {
	call int
	err  error
}

func (f *failOnSecondServiceProviderClusterGlobalLister) List(ctx context.Context, opts *database.DBClientListResourceDocsOptions) (database.DBClientIterator[api.ServiceProviderCluster], error) {
	f.call++
	if f.call >= 2 {
		return nil, f.err
	}
	return &simpleIterator[api.ServiceProviderCluster]{}, nil
}

// emptyFirstThenServiceProviderClusterGlobalLister returns an empty first ServiceProviderClusters list, then yields items on subsequent calls.
type emptyFirstThenServiceProviderClusterGlobalLister struct {
	call  int
	items []*api.ServiceProviderCluster
}

func (e *emptyFirstThenServiceProviderClusterGlobalLister) List(ctx context.Context, opts *database.DBClientListResourceDocsOptions) (database.DBClientIterator[api.ServiceProviderCluster], error) {
	e.call++
	if e.call == 1 {
		return &simpleIterator[api.ServiceProviderCluster]{}, nil
	}
	ids := make([]string, len(e.items))
	for i, spc := range e.items {
		ids[i] = spc.ResourceID.String()
	}
	return &simpleIterator[api.ServiceProviderCluster]{ids: ids, items: e.items}, nil
}

// orphanTestResourcesGlobalListersSPCOnly is a ResourcesGlobalListers test double that only customizes ServiceProviderClusters().
type orphanTestResourcesGlobalListersSPCOnly struct {
	defaultPanicResourcesGlobalListers
	spc database.GlobalLister[api.ServiceProviderCluster]
}

func newOrphanTestResourcesGlobalListersSPCOnly(spc database.GlobalLister[api.ServiceProviderCluster]) *orphanTestResourcesGlobalListersSPCOnly {
	return &orphanTestResourcesGlobalListersSPCOnly{spc: spc}
}

func (g *orphanTestResourcesGlobalListersSPCOnly) ServiceProviderClusters() database.GlobalLister[api.ServiceProviderCluster] {
	return g.spc
}

func (g *orphanTestResourcesGlobalListersSPCOnly) ServiceProviderNodePools() database.GlobalLister[api.ServiceProviderNodePool] {
	return &emptyGlobalLister[api.ServiceProviderNodePool]{}
}

var _ database.ResourcesGlobalListers = (*orphanTestResourcesGlobalListersSPCOnly)(nil)

// orphanTestResourcesGlobalListersSPNPOnly is a ResourcesGlobalListers test double that only customizes ServiceProviderNodePools().
type orphanTestResourcesGlobalListersSPNPOnly struct {
	defaultPanicResourcesGlobalListers
	spnp database.GlobalLister[api.ServiceProviderNodePool]
}

func newOrphanTestResourcesGlobalListersSPNPOnly(spnp database.GlobalLister[api.ServiceProviderNodePool]) *orphanTestResourcesGlobalListersSPNPOnly {
	return &orphanTestResourcesGlobalListersSPNPOnly{spnp: spnp}
}

func (g *orphanTestResourcesGlobalListersSPNPOnly) ServiceProviderClusters() database.GlobalLister[api.ServiceProviderCluster] {
	return &emptyGlobalLister[api.ServiceProviderCluster]{}
}

func (g *orphanTestResourcesGlobalListersSPNPOnly) ServiceProviderNodePools() database.GlobalLister[api.ServiceProviderNodePool] {
	return g.spnp
}

var _ database.ResourcesGlobalListers = (*orphanTestResourcesGlobalListersSPNPOnly)(nil)

// hcpClusterGetErrorInjectingResourcesDBClient wraps a mockResourcesDBClient and forces HCPClusters().Get to return a configurable
// non-NotFound error. Lets tests exercise the "real Get failure" path without modifying the in-memory mock.
type hcpClusterGetErrorInjectingResourcesDBClient struct {
	*databasetesting.MockResourcesDBClient
	getErr error
}

func (e *hcpClusterGetErrorInjectingResourcesDBClient) HCPClusters(subscriptionID, resourceGroupName string) database.HCPClusterCRUD {
	return &hcpClusterGetErrorInjectingCRUD{
		HCPClusterCRUD: e.MockResourcesDBClient.HCPClusters(subscriptionID, resourceGroupName),
		getErr:         e.getErr,
	}
}

type hcpClusterGetErrorInjectingCRUD struct {
	database.HCPClusterCRUD
	getErr error
}

func (e *hcpClusterGetErrorInjectingCRUD) Get(_ context.Context, _ string) (*api.HCPOpenShiftCluster, error) {
	return nil, e.getErr
}

func TestDeleteOrphanedMaestroReadonlyBundles_SyncOnce_ListServiceProviderClustersError(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	ctrl := gomock.NewController(t)
	mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
	listErr := fmt.Errorf("list SPCs error")
	mockResourcesDBClient.SetResourcesGlobalListers(&alwaysErrorResourcesGlobalListers{err: listErr})

	mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
	mockCS.EXPECT().ListProvisionShards().Return(ocm.NewSimpleProvisionShardListIterator(nil, nil))

	c := NewDeleteOrphanedMaestroReadonlyBundlesController(mockResourcesDBClient, mockCS, nil, "test-env")
	err := c.SyncOnce(ctx, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get all ServiceProviderClusters")
	assert.Contains(t, err.Error(), "list SPCs error")
}

func TestDeleteOrphanedMaestroReadonlyBundles_SyncOnce_ListServiceProviderNodePoolsError(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	ctrl := gomock.NewController(t)
	mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
	listErr := fmt.Errorf("list SPNPs error")
	mockResourcesDBClient.SetResourcesGlobalListers(&syncOnceSPCOKFailSecondSPNPResourcesGlobalListers{
		spnp: &failOnSecondSPNPGlobalLister{err: listErr},
	})

	mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
	mockCS.EXPECT().ListProvisionShards().Return(ocm.NewSimpleProvisionShardListIterator(nil, nil))

	c := NewDeleteOrphanedMaestroReadonlyBundlesController(mockResourcesDBClient, mockCS, nil, "test-env")
	err := c.SyncOnce(ctx, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get all ServiceProviderNodePools")
	assert.Contains(t, err.Error(), "list SPNPs error")
}

func TestDeleteOrphanedMaestroReadonlyBundles_SyncOnce_NoServiceProviderClusters_Success(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	ctrl := gomock.NewController(t)
	mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
	mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
	mockCS.EXPECT().ListProvisionShards().Return(ocm.NewSimpleProvisionShardListIterator(nil, nil))
	controller := NewDeleteOrphanedMaestroReadonlyBundlesController(mockResourcesDBClient, mockCS, nil, "test-env")

	err := controller.SyncOnce(ctx, nil)
	require.NoError(t, err)
}

func TestDeleteOrphanedMaestroReadonlyBundles_buildMaestroClientsByProvisionShard(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name        string
		setup       func(ctrl *gomock.Controller) *deleteOrphanedMaestroReadonlyBundles
		wantErr     bool
		errSubstr   string
		validateOut func(t *testing.T, clients map[string]*shardMaestroClient)
	}{
		{
			name: "empty provision shard list",
			setup: func(ctrl *gomock.Controller) *deleteOrphanedMaestroReadonlyBundles {
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				mockCS.EXPECT().ListProvisionShards().Return(ocm.NewSimpleProvisionShardListIterator(nil, nil))
				return &deleteOrphanedMaestroReadonlyBundles{clusterServiceClient: mockCS}
			},
			validateOut: func(t *testing.T, clients map[string]*shardMaestroClient) {
				require.Empty(t, clients)
			},
		},
		{
			name: "list provision shards iterator reports error after iteration",
			setup: func(ctrl *gomock.Controller) *deleteOrphanedMaestroReadonlyBundles {
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				listErr := fmt.Errorf("CS list provision shards failed")
				mockCS.EXPECT().ListProvisionShards().Return(ocm.NewSimpleProvisionShardListIterator(nil, listErr))
				return &deleteOrphanedMaestroReadonlyBundles{clusterServiceClient: mockCS}
			},
			wantErr:   true,
			errSubstr: "failed to list Cluster Service provision shards",
		},
		{
			name: "success single shard",
			setup: func(ctrl *gomock.Controller) *deleteOrphanedMaestroReadonlyBundles {
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				mockMaestroBuilder := maestro.NewMockMaestroClientBuilder(ctrl)
				mockMaestro := maestro.NewMockClient(ctrl)
				provisionShard := buildTestProvisionShard("test-consumer")
				mockCS.EXPECT().ListProvisionShards().Return(ocm.NewSimpleProvisionShardListIterator([]*arohcpv1alpha1.ProvisionShard{provisionShard}, nil))
				restEndpoint := provisionShard.MaestroConfig().RestApiConfig().Url()
				grpcEndpoint := provisionShard.MaestroConfig().GrpcApiConfig().Url()
				consumerName := provisionShard.MaestroConfig().ConsumerName()
				sourceID := maestro.GenerateMaestroSourceID("test-env", provisionShard.ID())
				mockMaestroBuilder.EXPECT().NewClient(gomock.Any(), restEndpoint, grpcEndpoint, consumerName, sourceID).Return(mockMaestro, nil)
				return &deleteOrphanedMaestroReadonlyBundles{
					clusterServiceClient:               mockCS,
					maestroClientBuilder:               mockMaestroBuilder,
					maestroSourceEnvironmentIdentifier: "test-env",
				}
			},
			validateOut: func(t *testing.T, clients map[string]*shardMaestroClient) {
				provisionShard := buildTestProvisionShard("test-consumer")
				require.Len(t, clients, 1)
				_, ok := clients[provisionShard.ID()]
				require.True(t, ok)
			},
		},
		{
			name: "success two shards",
			setup: func(ctrl *gomock.Controller) *deleteOrphanedMaestroReadonlyBundles {
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				mockMaestroBuilder := maestro.NewMockMaestroClientBuilder(ctrl)
				mockMaestro1 := maestro.NewMockClient(ctrl)
				mockMaestro2 := maestro.NewMockClient(ctrl)
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
				return &deleteOrphanedMaestroReadonlyBundles{
					clusterServiceClient:               mockCS,
					maestroClientBuilder:               mockMaestroBuilder,
					maestroSourceEnvironmentIdentifier: "test-env",
				}
			},
			validateOut: func(t *testing.T, clients map[string]*shardMaestroClient) {
				require.Len(t, clients, 2)
			},
		},
		{
			name: "NewClient error",
			setup: func(ctrl *gomock.Controller) *deleteOrphanedMaestroReadonlyBundles {
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				mockMaestroBuilder := maestro.NewMockMaestroClientBuilder(ctrl)
				provisionShard := buildTestProvisionShard("test-consumer")
				mockCS.EXPECT().ListProvisionShards().Return(ocm.NewSimpleProvisionShardListIterator([]*arohcpv1alpha1.ProvisionShard{provisionShard}, nil))
				restEndpoint := provisionShard.MaestroConfig().RestApiConfig().Url()
				grpcEndpoint := provisionShard.MaestroConfig().GrpcApiConfig().Url()
				consumerName := provisionShard.MaestroConfig().ConsumerName()
				sourceID := maestro.GenerateMaestroSourceID("test-env", provisionShard.ID())
				mockMaestroBuilder.EXPECT().NewClient(gomock.Any(), restEndpoint, grpcEndpoint, consumerName, sourceID).Return(nil, fmt.Errorf("maestro client error"))
				return &deleteOrphanedMaestroReadonlyBundles{
					clusterServiceClient:               mockCS,
					maestroClientBuilder:               mockMaestroBuilder,
					maestroSourceEnvironmentIdentifier: "test-env",
				}
			},
			wantErr:   true,
			errSubstr: "failed to create Maestro client",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			c := tt.setup(ctrl)
			clients, err := c.buildMaestroClientsByProvisionShard(ctx)
			defer cancelMaestroClientsByProvisionShard(clients)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
				return
			}
			require.NoError(t, err)
			if tt.validateOut != nil {
				tt.validateOut(t, clients)
			}
		})
	}
}

func TestDeleteOrphanedMaestroReadonlyBundles_mapServiceProviderClustersByProvisionShard(t *testing.T) {
	ctx := context.Background()
	// mapServiceProviderClustersByProvisionShard does not use the Maestro client; tests only need shard IDs in the map.
	noopMaestroShardClient := &shardMaestroClient{
		maestroClient:           nil,
		maestroClientCancelFunc: func() {},
	}
	clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster"))
	spcResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/serviceProviderClusters/default"))

	tests := []struct {
		name        string
		setup       func(ctrl *gomock.Controller) (c *deleteOrphanedMaestroReadonlyBundles, clients map[string]*shardMaestroClient, spcs []*api.ServiceProviderCluster)
		wantErr     bool
		errSubstr   string
		validateOut func(t *testing.T, shardToSPCs map[string][]*api.ServiceProviderCluster)
	}{
		{
			name: "Get cluster error",
			setup: func(ctrl *gomock.Controller) (*deleteOrphanedMaestroReadonlyBundles, map[string]*shardMaestroClient, []*api.ServiceProviderCluster) {
				mockResourcesDBClient := &hcpClusterGetErrorInjectingResourcesDBClient{
					MockResourcesDBClient: databasetesting.NewMockResourcesDBClient(),
					getErr:                fmt.Errorf("boom"),
				}
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				spc := &api.ServiceProviderCluster{
					CosmosMetadata: arm.CosmosMetadata{ResourceID: spcResourceID},
					ResourceID:     *spcResourceID,
				}
				return &deleteOrphanedMaestroReadonlyBundles{resourcesDBClient: mockResourcesDBClient, clusterServiceClient: mockCS},
					map[string]*shardMaestroClient{"unused-shard": noopMaestroShardClient},
					[]*api.ServiceProviderCluster{spc}
			},
			wantErr:   true,
			errSubstr: "failed to get Cluster",
		},
		{
			name: "cluster not found is silently skipped so its bundles can be reaped as orphans",
			setup: func(ctrl *gomock.Controller) (*deleteOrphanedMaestroReadonlyBundles, map[string]*shardMaestroClient, []*api.ServiceProviderCluster) {
				mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				spc := &api.ServiceProviderCluster{
					CosmosMetadata: arm.CosmosMetadata{ResourceID: spcResourceID},
					ResourceID:     *spcResourceID,
				}
				return &deleteOrphanedMaestroReadonlyBundles{resourcesDBClient: mockResourcesDBClient, clusterServiceClient: mockCS},
					map[string]*shardMaestroClient{"unused-shard": noopMaestroShardClient},
					[]*api.ServiceProviderCluster{spc}
			},
			validateOut: func(t *testing.T, shardToSPCs map[string][]*api.ServiceProviderCluster) {
				assert.Empty(t, shardToSPCs)
			},
		},
		{
			name: "Get provision shard error",
			setup: func(ctrl *gomock.Controller) (*deleteOrphanedMaestroReadonlyBundles, map[string]*shardMaestroClient, []*api.ServiceProviderCluster) {
				mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				cluster := &api.HCPOpenShiftCluster{
					TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: clusterResourceID}},
					ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
						ClusterServiceID: api.Ptr(api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/csid"))),
					},
				}
				_, err := mockResourcesDBClient.HCPClusters("sub", "rg").Create(ctx, cluster, nil)
				require.NoError(t, err)
				spc := &api.ServiceProviderCluster{
					CosmosMetadata: arm.CosmosMetadata{ResourceID: spcResourceID},
					ResourceID:     *spcResourceID,
				}
				mockCS.EXPECT().GetClusterProvisionShard(gomock.Any(), *cluster.ServiceProviderProperties.ClusterServiceID).Return(nil, fmt.Errorf("provision shard error"))
				return &deleteOrphanedMaestroReadonlyBundles{resourcesDBClient: mockResourcesDBClient, clusterServiceClient: mockCS},
					map[string]*shardMaestroClient{"unused-shard": noopMaestroShardClient},
					[]*api.ServiceProviderCluster{spc}
			},
			wantErr:   true,
			errSubstr: "failed to get Cluster Provision Shard",
		},
		{
			name: "provision shard not in clients map",
			setup: func(ctrl *gomock.Controller) (*deleteOrphanedMaestroReadonlyBundles, map[string]*shardMaestroClient, []*api.ServiceProviderCluster) {
				mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				cluster := &api.HCPOpenShiftCluster{
					TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: clusterResourceID}},
					ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
						ClusterServiceID: api.Ptr(api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/csid"))),
					},
				}
				_, err := mockResourcesDBClient.HCPClusters("sub", "rg").Create(ctx, cluster, nil)
				require.NoError(t, err)
				spc := &api.ServiceProviderCluster{
					CosmosMetadata: arm.CosmosMetadata{ResourceID: spcResourceID},
					ResourceID:     *spcResourceID,
				}
				shardInClientsMap := buildTestProvisionShard("consumer-in-list")
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
				mockCS.EXPECT().GetClusterProvisionShard(gomock.Any(), *cluster.ServiceProviderProperties.ClusterServiceID).Return(shardReturnedByCS, nil)
				clients := map[string]*shardMaestroClient{
					shardInClientsMap.ID(): noopMaestroShardClient,
				}
				return &deleteOrphanedMaestroReadonlyBundles{resourcesDBClient: mockResourcesDBClient, clusterServiceClient: mockCS}, clients, []*api.ServiceProviderCluster{spc}
			},
			wantErr:   true,
			errSubstr: "not present in provision shards map",
		},
		{
			name: "success single shard",
			setup: func(ctrl *gomock.Controller) (*deleteOrphanedMaestroReadonlyBundles, map[string]*shardMaestroClient, []*api.ServiceProviderCluster) {
				mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				cluster := &api.HCPOpenShiftCluster{
					TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: clusterResourceID}},
					ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
						ClusterServiceID: api.Ptr(api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/csid"))),
					},
				}
				_, err := mockResourcesDBClient.HCPClusters("sub", "rg").Create(ctx, cluster, nil)
				require.NoError(t, err)
				spc := &api.ServiceProviderCluster{
					CosmosMetadata: arm.CosmosMetadata{ResourceID: spcResourceID},
					ResourceID:     *spcResourceID,
				}
				provisionShard := buildTestProvisionShard("test-consumer")
				mockCS.EXPECT().GetClusterProvisionShard(gomock.Any(), *cluster.ServiceProviderProperties.ClusterServiceID).Return(provisionShard, nil)
				clients := map[string]*shardMaestroClient{provisionShard.ID(): noopMaestroShardClient}
				return &deleteOrphanedMaestroReadonlyBundles{resourcesDBClient: mockResourcesDBClient, clusterServiceClient: mockCS}, clients, []*api.ServiceProviderCluster{spc}
			},
			validateOut: func(t *testing.T, shardToSPCs map[string][]*api.ServiceProviderCluster) {
				provisionShard := buildTestProvisionShard("test-consumer")
				require.Len(t, shardToSPCs, 1)
				spcs := shardToSPCs[provisionShard.ID()]
				require.Len(t, spcs, 1)
				assert.Equal(t, spcResourceID.String(), spcs[0].ResourceID.String())
			},
		},
		{
			name: "multiple provision shards each get own map entry",
			setup: func(ctrl *gomock.Controller) (*deleteOrphanedMaestroReadonlyBundles, map[string]*shardMaestroClient, []*api.ServiceProviderCluster) {
				mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)

				cluster1ResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster1"))
				cluster2ResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub2/resourceGroups/rg2/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster2"))
				cluster1 := &api.HCPOpenShiftCluster{
					TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: cluster1ResourceID}},
					ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
						ClusterServiceID: api.Ptr(api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/csid1"))),
					},
				}
				cluster2 := &api.HCPOpenShiftCluster{
					TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: cluster2ResourceID}},
					ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
						ClusterServiceID: api.Ptr(api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/csid2"))),
					},
				}
				_, err := mockResourcesDBClient.HCPClusters("sub1", "rg1").Create(ctx, cluster1, nil)
				require.NoError(t, err)
				_, err = mockResourcesDBClient.HCPClusters("sub2", "rg2").Create(ctx, cluster2, nil)
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

				mockCS.EXPECT().GetClusterProvisionShard(gomock.Any(), *cluster1.ServiceProviderProperties.ClusterServiceID).Return(shard1, nil)
				mockCS.EXPECT().GetClusterProvisionShard(gomock.Any(), *cluster2.ServiceProviderProperties.ClusterServiceID).Return(shard2, nil)

				clients := map[string]*shardMaestroClient{
					shard1.ID(): noopMaestroShardClient,
					shard2.ID(): noopMaestroShardClient,
				}
				return &deleteOrphanedMaestroReadonlyBundles{resourcesDBClient: mockResourcesDBClient, clusterServiceClient: mockCS}, clients, []*api.ServiceProviderCluster{spc1, spc2}
			},
			validateOut: func(t *testing.T, shardToSPCs map[string][]*api.ServiceProviderCluster) {
				require.Len(t, shardToSPCs, 2)
				shard1 := buildTestProvisionShard("consumer1")
				spcs1 := shardToSPCs[shard1.ID()]
				require.Len(t, spcs1, 1)
				spc1RID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster1/serviceProviderClusters/default"))
				assert.Equal(t, spc1RID.String(), spcs1[0].ResourceID.String())
				shard2ID := "33333333333333333333333333333333"
				spcs2 := shardToSPCs[shard2ID]
				require.Len(t, spcs2, 1)
				spc2RID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub2/resourceGroups/rg2/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster2/serviceProviderClusters/default"))
				assert.Equal(t, spc2RID.String(), spcs2[0].ResourceID.String())
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			c, clients, spcs := tt.setup(ctrl)
			defer cancelMaestroClientsByProvisionShard(clients)
			shardToSPCs, err := c.mapServiceProviderClustersByProvisionShard(ctx, spcs, clients)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
				return
			}
			require.NoError(t, err)
			if tt.validateOut != nil {
				tt.validateOut(t, shardToSPCs)
			}
		})
	}
}

func TestDeleteOrphanedMaestroReadonlyBundles_provisionShardIDFromCluster(t *testing.T) {
	ctx := context.Background()

	t.Run("skip when ClusterServiceID is empty", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
		c := &deleteOrphanedMaestroReadonlyBundles{clusterServiceClient: mockCS}
		cluster := &api.HCPOpenShiftCluster{
			ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
				ClusterServiceID: nil,
			},
		}
		shardID, skip, err := c.provisionShardIDFromCluster(ctx, cluster)
		require.NoError(t, err)
		assert.True(t, skip)
		assert.Empty(t, shardID)
	})

	t.Run("returns shard ID when ClusterServiceID is set", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
		c := &deleteOrphanedMaestroReadonlyBundles{clusterServiceClient: mockCS}
		csID := api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/csid"))
		cluster := &api.HCPOpenShiftCluster{
			ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
				ClusterServiceID: &csID,
			},
		}
		provisionShard := buildTestProvisionShard("consumer")
		mockCS.EXPECT().GetClusterProvisionShard(gomock.Any(), csID).Return(provisionShard, nil)
		shardID, skip, err := c.provisionShardIDFromCluster(ctx, cluster)
		require.NoError(t, err)
		assert.False(t, skip)
		assert.Equal(t, provisionShard.ID(), shardID)
	})
}

func TestDeleteOrphanedMaestroReadonlyBundles_mapServiceProviderNodePoolsByProvisionShard(t *testing.T) {
	ctx := context.Background()
	// mapServiceProviderNodePoolsByProvisionShard does not use the Maestro client; tests only need shard IDs in the map.
	noopMaestroShardClient := &shardMaestroClient{
		maestroClient:           nil,
		maestroClientCancelFunc: func() {},
	}
	clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster"))
	spnpResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/nodePools/worker/serviceProviderNodePools/default"))

	tests := []struct {
		name        string
		setup       func(ctrl *gomock.Controller) (c *deleteOrphanedMaestroReadonlyBundles, clients map[string]*shardMaestroClient, spnps []*api.ServiceProviderNodePool)
		wantErr     bool
		errSubstr   string
		validateOut func(t *testing.T, shardToSPNPs map[string][]*api.ServiceProviderNodePool)
	}{
		{
			name: "no parent resource ID",
			setup: func(ctrl *gomock.Controller) (*deleteOrphanedMaestroReadonlyBundles, map[string]*shardMaestroClient, []*api.ServiceProviderNodePool) {
				mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				broken := *spnpResourceID
				broken.Parent = nil
				spnp := &api.ServiceProviderNodePool{
					CosmosMetadata: arm.CosmosMetadata{ResourceID: &broken},
					ResourceID:     broken,
				}
				return &deleteOrphanedMaestroReadonlyBundles{resourcesDBClient: mockResourcesDBClient, clusterServiceClient: mockCS},
					map[string]*shardMaestroClient{"unused-shard": noopMaestroShardClient},
					[]*api.ServiceProviderNodePool{spnp}
			},
			wantErr:   true,
			errSubstr: "has no parent resource ID",
		},
		{
			name: "no grandparent cluster resource ID",
			setup: func(ctrl *gomock.Controller) (*deleteOrphanedMaestroReadonlyBundles, map[string]*shardMaestroClient, []*api.ServiceProviderNodePool) {
				mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				noGrand := *spnpResourceID
				npOnly := *noGrand.Parent
				npOnly.Parent = nil
				noGrand.Parent = &npOnly
				spnp := &api.ServiceProviderNodePool{
					CosmosMetadata: arm.CosmosMetadata{ResourceID: &noGrand},
					ResourceID:     noGrand,
				}
				return &deleteOrphanedMaestroReadonlyBundles{resourcesDBClient: mockResourcesDBClient, clusterServiceClient: mockCS},
					map[string]*shardMaestroClient{"unused-shard": noopMaestroShardClient},
					[]*api.ServiceProviderNodePool{spnp}
			},
			wantErr:   true,
			errSubstr: "has no grandparent cluster resource ID",
		},
		{
			name: "Get cluster error",
			setup: func(ctrl *gomock.Controller) (*deleteOrphanedMaestroReadonlyBundles, map[string]*shardMaestroClient, []*api.ServiceProviderNodePool) {
				mockResourcesDBClient := &hcpClusterGetErrorInjectingResourcesDBClient{
					MockResourcesDBClient: databasetesting.NewMockResourcesDBClient(),
					getErr:                fmt.Errorf("boom"),
				}
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				spnp := &api.ServiceProviderNodePool{
					CosmosMetadata: arm.CosmosMetadata{ResourceID: spnpResourceID},
					ResourceID:     *spnpResourceID,
				}
				return &deleteOrphanedMaestroReadonlyBundles{resourcesDBClient: mockResourcesDBClient, clusterServiceClient: mockCS},
					map[string]*shardMaestroClient{"unused-shard": noopMaestroShardClient},
					[]*api.ServiceProviderNodePool{spnp}
			},
			wantErr:   true,
			errSubstr: "failed to get Cluster",
		},
		{
			name: "cluster not found is silently skipped so its bundles can be reaped as orphans",
			setup: func(ctrl *gomock.Controller) (*deleteOrphanedMaestroReadonlyBundles, map[string]*shardMaestroClient, []*api.ServiceProviderNodePool) {
				mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				spnp := &api.ServiceProviderNodePool{
					CosmosMetadata: arm.CosmosMetadata{ResourceID: spnpResourceID},
					ResourceID:     *spnpResourceID,
				}
				return &deleteOrphanedMaestroReadonlyBundles{resourcesDBClient: mockResourcesDBClient, clusterServiceClient: mockCS},
					map[string]*shardMaestroClient{"unused-shard": noopMaestroShardClient},
					[]*api.ServiceProviderNodePool{spnp}
			},
			validateOut: func(t *testing.T, shardToSPNPs map[string][]*api.ServiceProviderNodePool) {
				assert.Empty(t, shardToSPNPs)
			},
		},
		{
			name: "Get provision shard error",
			setup: func(ctrl *gomock.Controller) (*deleteOrphanedMaestroReadonlyBundles, map[string]*shardMaestroClient, []*api.ServiceProviderNodePool) {
				mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				cluster := &api.HCPOpenShiftCluster{
					TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: clusterResourceID}},
					ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
						ClusterServiceID: ptr.To(api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/csid"))),
					},
				}
				_, err := mockResourcesDBClient.HCPClusters("sub", "rg").Create(ctx, cluster, nil)
				require.NoError(t, err)
				spnp := &api.ServiceProviderNodePool{
					CosmosMetadata: arm.CosmosMetadata{ResourceID: spnpResourceID},
					ResourceID:     *spnpResourceID,
				}
				mockCS.EXPECT().GetClusterProvisionShard(gomock.Any(), *cluster.ServiceProviderProperties.ClusterServiceID).Return(nil, fmt.Errorf("provision shard error"))
				return &deleteOrphanedMaestroReadonlyBundles{resourcesDBClient: mockResourcesDBClient, clusterServiceClient: mockCS},
					map[string]*shardMaestroClient{"unused-shard": noopMaestroShardClient},
					[]*api.ServiceProviderNodePool{spnp}
			},
			wantErr:   true,
			errSubstr: "failed to get Cluster Provision Shard",
		},
		{
			name: "provision shard not in clients map",
			setup: func(ctrl *gomock.Controller) (*deleteOrphanedMaestroReadonlyBundles, map[string]*shardMaestroClient, []*api.ServiceProviderNodePool) {
				mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				cluster := &api.HCPOpenShiftCluster{
					TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: clusterResourceID}},
					ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
						ClusterServiceID: ptr.To(api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/csid"))),
					},
				}
				_, err := mockResourcesDBClient.HCPClusters("sub", "rg").Create(ctx, cluster, nil)
				require.NoError(t, err)
				spnp := &api.ServiceProviderNodePool{
					CosmosMetadata: arm.CosmosMetadata{ResourceID: spnpResourceID},
					ResourceID:     *spnpResourceID,
				}
				shardInClientsMap := buildTestProvisionShard("consumer-in-list")
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
				mockCS.EXPECT().GetClusterProvisionShard(gomock.Any(), *cluster.ServiceProviderProperties.ClusterServiceID).Return(shardReturnedByCS, nil)
				clients := map[string]*shardMaestroClient{
					shardInClientsMap.ID(): noopMaestroShardClient,
				}
				return &deleteOrphanedMaestroReadonlyBundles{resourcesDBClient: mockResourcesDBClient, clusterServiceClient: mockCS}, clients, []*api.ServiceProviderNodePool{spnp}
			},
			wantErr:   true,
			errSubstr: "not present in provision shards map",
		},
		{
			name: "success single shard",
			setup: func(ctrl *gomock.Controller) (*deleteOrphanedMaestroReadonlyBundles, map[string]*shardMaestroClient, []*api.ServiceProviderNodePool) {
				mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				cluster := &api.HCPOpenShiftCluster{
					TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: clusterResourceID}},
					ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
						ClusterServiceID: ptr.To(api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/csid"))),
					},
				}
				_, err := mockResourcesDBClient.HCPClusters("sub", "rg").Create(ctx, cluster, nil)
				require.NoError(t, err)
				spnp := &api.ServiceProviderNodePool{
					CosmosMetadata: arm.CosmosMetadata{ResourceID: spnpResourceID},
					ResourceID:     *spnpResourceID,
				}
				provisionShard := buildTestProvisionShard("test-consumer")
				mockCS.EXPECT().GetClusterProvisionShard(gomock.Any(), *cluster.ServiceProviderProperties.ClusterServiceID).Return(provisionShard, nil)
				clients := map[string]*shardMaestroClient{provisionShard.ID(): noopMaestroShardClient}
				return &deleteOrphanedMaestroReadonlyBundles{resourcesDBClient: mockResourcesDBClient, clusterServiceClient: mockCS}, clients, []*api.ServiceProviderNodePool{spnp}
			},
			validateOut: func(t *testing.T, shardToSPNPs map[string][]*api.ServiceProviderNodePool) {
				provisionShard := buildTestProvisionShard("test-consumer")
				require.Len(t, shardToSPNPs, 1)
				spnps := shardToSPNPs[provisionShard.ID()]
				require.Len(t, spnps, 1)
				assert.Equal(t, spnpResourceID.String(), spnps[0].ResourceID.String())
			},
		},
		{
			name: "multiple provision shards each get own map entry",
			setup: func(ctrl *gomock.Controller) (*deleteOrphanedMaestroReadonlyBundles, map[string]*shardMaestroClient, []*api.ServiceProviderNodePool) {
				mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)

				cluster1ResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster1"))
				cluster2ResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub2/resourceGroups/rg2/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster2"))
				cluster1 := &api.HCPOpenShiftCluster{
					TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: cluster1ResourceID}},
					ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
						ClusterServiceID: ptr.To(api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/csid1"))),
					},
				}
				cluster2 := &api.HCPOpenShiftCluster{
					TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: cluster2ResourceID}},
					ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
						ClusterServiceID: ptr.To(api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/csid2"))),
					},
				}
				_, err := mockResourcesDBClient.HCPClusters("sub1", "rg1").Create(ctx, cluster1, nil)
				require.NoError(t, err)
				_, err = mockResourcesDBClient.HCPClusters("sub2", "rg2").Create(ctx, cluster2, nil)
				require.NoError(t, err)

				spnp1ResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster1/nodePools/worker/serviceProviderNodePools/default"))
				spnp2ResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub2/resourceGroups/rg2/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster2/nodePools/worker/serviceProviderNodePools/default"))
				spnp1 := &api.ServiceProviderNodePool{
					CosmosMetadata: arm.CosmosMetadata{ResourceID: spnp1ResourceID},
					ResourceID:     *spnp1ResourceID,
				}
				spnp2 := &api.ServiceProviderNodePool{
					CosmosMetadata: arm.CosmosMetadata{ResourceID: spnp2ResourceID},
					ResourceID:     *spnp2ResourceID,
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

				mockCS.EXPECT().GetClusterProvisionShard(gomock.Any(), *cluster1.ServiceProviderProperties.ClusterServiceID).Return(shard1, nil)
				mockCS.EXPECT().GetClusterProvisionShard(gomock.Any(), *cluster2.ServiceProviderProperties.ClusterServiceID).Return(shard2, nil)

				clients := map[string]*shardMaestroClient{
					shard1.ID(): noopMaestroShardClient,
					shard2.ID(): noopMaestroShardClient,
				}
				return &deleteOrphanedMaestroReadonlyBundles{resourcesDBClient: mockResourcesDBClient, clusterServiceClient: mockCS}, clients, []*api.ServiceProviderNodePool{spnp1, spnp2}
			},
			validateOut: func(t *testing.T, shardToSPNPs map[string][]*api.ServiceProviderNodePool) {
				require.Len(t, shardToSPNPs, 2)
				shard1 := buildTestProvisionShard("consumer1")
				spnps1 := shardToSPNPs[shard1.ID()]
				require.Len(t, spnps1, 1)
				spnp1RID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster1/nodePools/worker/serviceProviderNodePools/default"))
				assert.Equal(t, spnp1RID.String(), spnps1[0].ResourceID.String())
				shard2ID := "33333333333333333333333333333333"
				spnps2 := shardToSPNPs[shard2ID]
				require.Len(t, spnps2, 1)
				spnp2RID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub2/resourceGroups/rg2/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster2/nodePools/worker/serviceProviderNodePools/default"))
				assert.Equal(t, spnp2RID.String(), spnps2[0].ResourceID.String())
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			c, clients, spnps := tt.setup(ctrl)
			defer cancelMaestroClientsByProvisionShard(clients)
			shardToSPNPs, err := c.mapServiceProviderNodePoolsByProvisionShard(ctx, spnps, clients)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
				return
			}
			require.NoError(t, err)
			if tt.validateOut != nil {
				tt.validateOut(t, shardToSPNPs)
			}
		})
	}
}

func TestDeleteOrphanedMaestroReadonlyBundles_ensureClusterScopedOrphanedMaestroReadonlyBundlesAreDeleted(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	spcResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/serviceProviderClusters/default"))

	tests := []struct {
		name      string
		setupMock func(*testing.T, *maestro.MockClient, *databasetesting.MockResourcesDBClient, *ocm.MockClusterServiceClientSpec, *arohcpv1alpha1.ProvisionShard) map[string]*shardMaestroClient
		wantErr   bool
		errSubstr string
	}{
		{
			name: "empty provision shards map does not perform anything",
			setupMock: func(t *testing.T, _ *maestro.MockClient, _ *databasetesting.MockResourcesDBClient, _ *ocm.MockClusterServiceClientSpec, _ *arohcpv1alpha1.ProvisionShard) map[string]*shardMaestroClient {
				return nil
			},
			wantErr: false,
		},
		{
			name: "second global SPC list error",
			setupMock: func(_ *testing.T, m *maestro.MockClient, mockResourcesDBClient *databasetesting.MockResourcesDBClient, _ *ocm.MockClusterServiceClientSpec, shard *arohcpv1alpha1.ProvisionShard) map[string]*shardMaestroClient {
				listErr := fmt.Errorf("fresh list SPCs error")
				mockResourcesDBClient.SetResourcesGlobalListers(newOrphanTestResourcesGlobalListersSPCOnly(&failOnSecondServiceProviderClusterGlobalLister{err: listErr}))
				m.EXPECT().List(gomock.Any(), gomock.Any()).Return(&workv1.ManifestWorkList{}, nil)
				return map[string]*shardMaestroClient{
					shard.ID(): {maestroClient: m, maestroClientCancelFunc: func() {}},
				}
			},
			wantErr:   true,
			errSubstr: "failed to get all ServiceProviderClusters",
		},
		{
			name: "an error is returned when listing Maestro bundles fails",
			setupMock: func(_ *testing.T, m *maestro.MockClient, _ *databasetesting.MockResourcesDBClient, _ *ocm.MockClusterServiceClientSpec, shard *arohcpv1alpha1.ProvisionShard) map[string]*shardMaestroClient {
				m.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, fmt.Errorf("maestro list error"))
				return map[string]*shardMaestroClient{
					shard.ID(): {maestroClient: m, maestroClientCancelFunc: func() {}},
				}
			},
			wantErr:   true,
			errSubstr: "failed to list Maestro Bundles",
		},
		{
			name: "skips bundle without readonly managed-by label",
			setupMock: func(_ *testing.T, m *maestro.MockClient, _ *databasetesting.MockResourcesDBClient, _ *ocm.MockClusterServiceClientSpec, shard *arohcpv1alpha1.ProvisionShard) map[string]*shardMaestroClient {
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
				return map[string]*shardMaestroClient{
					shard.ID(): {maestroClient: m, maestroClientCancelFunc: func() {}},
				}
			},
		},
		{
			name: "skips Maestro bundle that is referenced by a ServiceProviderCluster on the shard",
			setupMock: func(t *testing.T, m *maestro.MockClient, mockResourcesDBClient *databasetesting.MockResourcesDBClient, mockCS *ocm.MockClusterServiceClientSpec, shard *arohcpv1alpha1.ProvisionShard) map[string]*shardMaestroClient {
				clusterRID := spcResourceID.Parent
				cluster := &api.HCPOpenShiftCluster{
					TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: clusterRID}},
					ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
						ClusterServiceID: ptr.To(api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/csid"))),
					},
				}
				_, err := mockResourcesDBClient.HCPClusters(clusterRID.SubscriptionID, clusterRID.ResourceGroupName).Create(ctx, cluster, nil)
				require.NoError(t, err)
				mockCS.EXPECT().GetClusterProvisionShard(gomock.Any(), *cluster.ServiceProviderProperties.ClusterServiceID).Return(shard, nil).AnyTimes()
				spc := &api.ServiceProviderCluster{
					CosmosMetadata: arm.CosmosMetadata{ResourceID: spcResourceID},
					ResourceID:     *spcResourceID,
					Status: api.ServiceProviderClusterStatus{
						MaestroReadonlyBundles: api.MaestroBundleReferenceList{
							{MaestroAPIMaestroBundleName: "referenced-bundle"},
						},
					},
				}
				_, err = mockResourcesDBClient.ServiceProviderClusters(clusterRID.SubscriptionID, clusterRID.ResourceGroupName, clusterRID.Name).Create(context.Background(), spc, nil)
				require.NoError(t, err)
				bundleList := &workv1.ManifestWorkList{
					Items: []workv1.ManifestWork{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "referenced-bundle",
								Namespace: "consumer",
								Labels:    map[string]string{readonlyBundleManagedByK8sLabelKey: readonlyBundleManagedByK8sLabelValueClusterScoped},
							},
						},
					},
				}
				m.EXPECT().List(gomock.Any(), gomock.Any()).Return(bundleList, nil)
				return map[string]*shardMaestroClient{
					shard.ID(): {maestroClient: m, maestroClientCancelFunc: func() {}},
				}
			},
		},
		{
			name: "deletes Maestro bundle that is not referenced by any ServiceProviderCluster on the shard",
			setupMock: func(t *testing.T, m *maestro.MockClient, mockResourcesDBClient *databasetesting.MockResourcesDBClient, mockCS *ocm.MockClusterServiceClientSpec, shard *arohcpv1alpha1.ProvisionShard) map[string]*shardMaestroClient {
				clusterRID := spcResourceID.Parent
				cluster := &api.HCPOpenShiftCluster{
					TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: clusterRID}},
					ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
						ClusterServiceID: ptr.To(api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/csid"))),
					},
				}
				_, err := mockResourcesDBClient.HCPClusters(clusterRID.SubscriptionID, clusterRID.ResourceGroupName).Create(ctx, cluster, nil)
				require.NoError(t, err)
				mockCS.EXPECT().GetClusterProvisionShard(gomock.Any(), *cluster.ServiceProviderProperties.ClusterServiceID).Return(shard, nil).AnyTimes()
				spc := &api.ServiceProviderCluster{
					CosmosMetadata: arm.CosmosMetadata{ResourceID: spcResourceID},
					ResourceID:     *spcResourceID,
					Status: api.ServiceProviderClusterStatus{
						MaestroReadonlyBundles: api.MaestroBundleReferenceList{
							{MaestroAPIMaestroBundleName: "referenced-bundle"},
						},
					},
				}
				_, err = mockResourcesDBClient.ServiceProviderClusters(clusterRID.SubscriptionID, clusterRID.ResourceGroupName, clusterRID.Name).Create(context.Background(), spc, nil)
				require.NoError(t, err)
				orphanMeta := metav1.ObjectMeta{
					Name:      "orphaned-bundle",
					Namespace: "consumer",
					UID:       types.UID("orphan-uid"),
					Labels:    map[string]string{readonlyBundleManagedByK8sLabelKey: readonlyBundleManagedByK8sLabelValueClusterScoped},
				}
				bundleList := &workv1.ManifestWorkList{
					Items: []workv1.ManifestWork{
						{ObjectMeta: orphanMeta},
					},
				}
				m.EXPECT().List(gomock.Any(), gomock.Any()).Return(bundleList, nil)
				m.EXPECT().Delete(gomock.Any(), "orphaned-bundle", metav1.DeleteOptions{}).Return(nil)
				return map[string]*shardMaestroClient{
					shard.ID(): {maestroClient: m, maestroClientCancelFunc: func() {}},
				}
			},
		},
		{
			// Two orphans: first Delete fails (appended to syncErrors, loop continues), second Delete succeeds;
			// gomock call order proves the second delete was still attempted; errors.Join still returns an error.
			name: "continues deleting remaining orphans after a delete failure",
			setupMock: func(_ *testing.T, m *maestro.MockClient, _ *databasetesting.MockResourcesDBClient, _ *ocm.MockClusterServiceClientSpec, shard *arohcpv1alpha1.ProvisionShard) map[string]*shardMaestroClient {
				metaA := metav1.ObjectMeta{
					Name:      "orphan-a",
					Namespace: "consumer",
					UID:       types.UID("orphan-a-uid"),
					Labels:    map[string]string{readonlyBundleManagedByK8sLabelKey: readonlyBundleManagedByK8sLabelValueClusterScoped},
				}
				metaB := metav1.ObjectMeta{
					Name:      "orphan-b",
					Namespace: "consumer",
					UID:       types.UID("orphan-b-uid"),
					Labels:    map[string]string{readonlyBundleManagedByK8sLabelKey: readonlyBundleManagedByK8sLabelValueClusterScoped},
				}
				bundleList := &workv1.ManifestWorkList{
					Items: []workv1.ManifestWork{
						{ObjectMeta: metaA},
						{ObjectMeta: metaB},
					},
				}
				m.EXPECT().List(gomock.Any(), gomock.Any()).Return(bundleList, nil)
				m.EXPECT().Delete(gomock.Any(), "orphan-a", metav1.DeleteOptions{}).Return(fmt.Errorf("delete failed"))
				m.EXPECT().Delete(gomock.Any(), "orphan-b", metav1.DeleteOptions{}).Return(nil)
				return map[string]*shardMaestroClient{
					shard.ID(): {maestroClient: m, maestroClientCancelFunc: func() {}},
				}
			},
			wantErr:   true,
			errSubstr: "failed to delete Maestro Bundle",
		},
		{
			name: "pagination lists and deletes across pages",
			setupMock: func(_ *testing.T, m *maestro.MockClient, _ *databasetesting.MockResourcesDBClient, _ *ocm.MockClusterServiceClientSpec, shard *arohcpv1alpha1.ProvisionShard) map[string]*shardMaestroClient {
				page1Meta := metav1.ObjectMeta{
					Name:      "orphan-page1",
					Namespace: "consumer",
					UID:       types.UID("page1-uid"),
					Labels:    map[string]string{readonlyBundleManagedByK8sLabelKey: readonlyBundleManagedByK8sLabelValueClusterScoped},
				}
				page1 := &workv1.ManifestWorkList{
					ListMeta: metav1.ListMeta{Continue: "token"},
					Items: []workv1.ManifestWork{
						{ObjectMeta: page1Meta},
					},
				}
				page2 := &workv1.ManifestWorkList{Items: []workv1.ManifestWork{}}
				labelSelector := fmt.Sprintf("%s=%s", readonlyBundleManagedByK8sLabelKey, readonlyBundleManagedByK8sLabelValueClusterScoped)
				m.EXPECT().List(gomock.Any(), metav1.ListOptions{Limit: 400, Continue: "", LabelSelector: labelSelector}).Return(page1, nil)
				m.EXPECT().List(gomock.Any(), metav1.ListOptions{Limit: 400, Continue: "token", LabelSelector: labelSelector}).Return(page2, nil)
				m.EXPECT().Delete(gomock.Any(), "orphan-page1", metav1.DeleteOptions{}).Return(nil)
				return map[string]*shardMaestroClient{
					shard.ID(): {maestroClient: m, maestroClientCancelFunc: func() {}},
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockMaestro := maestro.NewMockClient(ctrl)
			mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
			mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
			shard := buildTestProvisionShard("test-consumer")
			clients := tt.setupMock(t, mockMaestro, mockResourcesDBClient, mockCS, shard)
			c := &deleteOrphanedMaestroReadonlyBundles{resourcesDBClient: mockResourcesDBClient, clusterServiceClient: mockCS}
			err := c.ensureClusterScopedOrphanedMaestroReadonlyBundlesAreDeleted(ctx, clients)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestDeleteOrphanedMaestroReadonlyBundles_ensureOrphanedNodePoolScopedMaestroReadonlyBundlesAreDeleted(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	spnpResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/nodePools/worker/serviceProviderNodePools/default"))

	tests := []struct {
		name      string
		setupMock func(*testing.T, *maestro.MockClient, *databasetesting.MockResourcesDBClient, *ocm.MockClusterServiceClientSpec, *arohcpv1alpha1.ProvisionShard) map[string]*shardMaestroClient
		wantErr   bool
		errSubstr string
	}{
		{
			name: "empty maestro clients map does not perform anything",
			setupMock: func(t *testing.T, _ *maestro.MockClient, _ *databasetesting.MockResourcesDBClient, _ *ocm.MockClusterServiceClientSpec, _ *arohcpv1alpha1.ProvisionShard) map[string]*shardMaestroClient {
				return nil
			},
			wantErr: false,
		},
		{
			name: "second global ServiceProviderNodePools list error",
			setupMock: func(_ *testing.T, m *maestro.MockClient, mockResourcesDBClient *databasetesting.MockResourcesDBClient, _ *ocm.MockClusterServiceClientSpec, shard *arohcpv1alpha1.ProvisionShard) map[string]*shardMaestroClient {
				listErr := fmt.Errorf("fresh list SPNPs error")
				mockResourcesDBClient.SetResourcesGlobalListers(newOrphanTestResourcesGlobalListersSPNPOnly(&failOnSecondSPNPGlobalLister{err: listErr}))
				m.EXPECT().List(gomock.Any(), gomock.Any()).Return(&workv1.ManifestWorkList{}, nil)
				return map[string]*shardMaestroClient{
					shard.ID(): {maestroClient: m, maestroClientCancelFunc: func() {}},
				}
			},
			wantErr:   true,
			errSubstr: "failed to get all ServiceProviderNodePools",
		},
		{
			name: "an error is returned when listing Maestro bundles fails",
			setupMock: func(_ *testing.T, m *maestro.MockClient, _ *databasetesting.MockResourcesDBClient, _ *ocm.MockClusterServiceClientSpec, shard *arohcpv1alpha1.ProvisionShard) map[string]*shardMaestroClient {
				m.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, fmt.Errorf("maestro list error"))
				return map[string]*shardMaestroClient{
					shard.ID(): {maestroClient: m, maestroClientCancelFunc: func() {}},
				}
			},
			wantErr:   true,
			errSubstr: "failed to list Maestro Bundles for shard",
		},
		{
			name: "skips bundle without nodepool readonly managed-by label",
			setupMock: func(_ *testing.T, m *maestro.MockClient, _ *databasetesting.MockResourcesDBClient, _ *ocm.MockClusterServiceClientSpec, shard *arohcpv1alpha1.ProvisionShard) map[string]*shardMaestroClient {
				bundleList := &workv1.ManifestWorkList{
					Items: []workv1.ManifestWork{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "other-bundle",
								Namespace: "consumer",
								Labels:    map[string]string{readonlyBundleManagedByK8sLabelKey: readonlyBundleManagedByK8sLabelValueClusterScoped},
							},
						},
					},
				}
				m.EXPECT().List(gomock.Any(), gomock.Any()).Return(bundleList, nil)
				return map[string]*shardMaestroClient{
					shard.ID(): {maestroClient: m, maestroClientCancelFunc: func() {}},
				}
			},
		},
		{
			name: "skips Maestro bundle referenced by a ServiceProviderNodePool on the shard",
			setupMock: func(t *testing.T, m *maestro.MockClient, mockResourcesDBClient *databasetesting.MockResourcesDBClient, mockCS *ocm.MockClusterServiceClientSpec, shard *arohcpv1alpha1.ProvisionShard) map[string]*shardMaestroClient {
				clusterRID := spnpResourceID.Parent.Parent
				cluster := &api.HCPOpenShiftCluster{
					TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: clusterRID}},
					ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
						ClusterServiceID: ptr.To(api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/csid"))),
					},
				}
				_, err := mockResourcesDBClient.HCPClusters(clusterRID.SubscriptionID, clusterRID.ResourceGroupName).Create(ctx, cluster, nil)
				require.NoError(t, err)
				mockCS.EXPECT().GetClusterProvisionShard(gomock.Any(), *cluster.ServiceProviderProperties.ClusterServiceID).Return(shard, nil).AnyTimes()
				spnp := &api.ServiceProviderNodePool{
					CosmosMetadata: arm.CosmosMetadata{ResourceID: spnpResourceID},
					ResourceID:     *spnpResourceID,
					Status: api.ServiceProviderNodePoolStatus{
						MaestroReadonlyBundles: api.MaestroBundleReferenceList{
							{MaestroAPIMaestroBundleName: "referenced-np-bundle"},
						},
					},
				}
				_, err = mockResourcesDBClient.ServiceProviderNodePools(clusterRID.SubscriptionID, clusterRID.ResourceGroupName, clusterRID.Name, spnpResourceID.Parent.Name).Create(context.Background(), spnp, nil)
				require.NoError(t, err)
				bundleList := &workv1.ManifestWorkList{
					Items: []workv1.ManifestWork{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "referenced-np-bundle",
								Namespace: "consumer",
								Labels:    map[string]string{readonlyBundleManagedByK8sLabelKey: readonlyBundleManagedByK8sLabelValueNodePoolScoped},
							},
						},
					},
				}
				m.EXPECT().List(gomock.Any(), gomock.Any()).Return(bundleList, nil)
				return map[string]*shardMaestroClient{
					shard.ID(): {maestroClient: m, maestroClientCancelFunc: func() {}},
				}
			},
		},
		{
			name: "deletes Maestro bundle not referenced by any ServiceProviderNodePool on the shard",
			setupMock: func(t *testing.T, m *maestro.MockClient, mockResourcesDBClient *databasetesting.MockResourcesDBClient, mockCS *ocm.MockClusterServiceClientSpec, shard *arohcpv1alpha1.ProvisionShard) map[string]*shardMaestroClient {
				clusterRID := spnpResourceID.Parent.Parent
				cluster := &api.HCPOpenShiftCluster{
					TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: clusterRID}},
					ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
						ClusterServiceID: ptr.To(api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/csid"))),
					},
				}
				_, err := mockResourcesDBClient.HCPClusters(clusterRID.SubscriptionID, clusterRID.ResourceGroupName).Create(ctx, cluster, nil)
				require.NoError(t, err)
				mockCS.EXPECT().GetClusterProvisionShard(gomock.Any(), *cluster.ServiceProviderProperties.ClusterServiceID).Return(shard, nil).AnyTimes()
				spnp := &api.ServiceProviderNodePool{
					CosmosMetadata: arm.CosmosMetadata{ResourceID: spnpResourceID},
					ResourceID:     *spnpResourceID,
					Status: api.ServiceProviderNodePoolStatus{
						MaestroReadonlyBundles: api.MaestroBundleReferenceList{
							{MaestroAPIMaestroBundleName: "kept-np-bundle"},
						},
					},
				}
				_, err = mockResourcesDBClient.ServiceProviderNodePools(clusterRID.SubscriptionID, clusterRID.ResourceGroupName, clusterRID.Name, spnpResourceID.Parent.Name).Create(context.Background(), spnp, nil)
				require.NoError(t, err)
				orphanMeta := metav1.ObjectMeta{
					Name:      "orphaned-np-bundle",
					Namespace: "consumer",
					UID:       types.UID("orphan-np-uid"),
					Labels:    map[string]string{readonlyBundleManagedByK8sLabelKey: readonlyBundleManagedByK8sLabelValueNodePoolScoped},
				}
				bundleList := &workv1.ManifestWorkList{Items: []workv1.ManifestWork{{ObjectMeta: orphanMeta}}}
				m.EXPECT().List(gomock.Any(), gomock.Any()).Return(bundleList, nil)
				m.EXPECT().Delete(gomock.Any(), "orphaned-np-bundle", metav1.DeleteOptions{}).Return(nil)
				return map[string]*shardMaestroClient{
					shard.ID(): {maestroClient: m, maestroClientCancelFunc: func() {}},
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockMaestro := maestro.NewMockClient(ctrl)
			mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
			mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
			shard := buildTestProvisionShard("test-consumer")
			clients := tt.setupMock(t, mockMaestro, mockResourcesDBClient, mockCS, shard)
			c := &deleteOrphanedMaestroReadonlyBundles{resourcesDBClient: mockResourcesDBClient, clusterServiceClient: mockCS}
			err := c.ensureOrphanedNodePoolScopedMaestroReadonlyBundlesAreDeleted(ctx, clients)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestDeleteOrphanedMaestroReadonlyBundles_ensureClusterScopedOrphanedMaestroReadonlyBundlesAreDeleted_shardScopedDeletes
// verifies that when processing a Maestro bundle in shard A, if it doesn't have a corresponding SPC associated to shard A
// it is deleted, even if there's a SPC associated to other shards that contains the same maestro bundle name (per-shard reference scope).
func TestDeleteOrphanedMaestroReadonlyBundles_ensureClusterScopedOrphanedMaestroReadonlyBundlesAreDeleted_clusterScopedDeletes(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	ctrl := gomock.NewController(t)
	mockShard1 := maestro.NewMockClient(ctrl)
	mockShard2 := maestro.NewMockClient(ctrl)
	mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
	mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)

	shard1, err := arohcpv1alpha1.NewProvisionShard().
		ID("11111111111111111111111111111111").
		MaestroConfig(
			arohcpv1alpha1.NewProvisionShardMaestroConfig().
				ConsumerName("consumer1").
				RestApiConfig(arohcpv1alpha1.NewProvisionShardMaestroRestApiConfig().Url("https://maestro1.example.com:443")).
				GrpcApiConfig(arohcpv1alpha1.NewProvisionShardMaestroGrpcApiConfig().Url("https://maestro1.example.com:444")),
		).
		Build()
	require.NoError(t, err)
	shard2, err := arohcpv1alpha1.NewProvisionShard().
		ID("33333333333333333333333333333333").
		MaestroConfig(
			arohcpv1alpha1.NewProvisionShardMaestroConfig().
				ConsumerName("consumer2").
				RestApiConfig(arohcpv1alpha1.NewProvisionShardMaestroRestApiConfig().Url("https://maestro2.example.com:443")).
				GrpcApiConfig(arohcpv1alpha1.NewProvisionShardMaestroGrpcApiConfig().Url("https://maestro2.example.com:444")),
		).
		Build()
	require.NoError(t, err)

	spcOnShard1ResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster1/serviceProviderClusters/default"))
	clusterRID := spcOnShard1ResourceID.Parent
	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: clusterRID}},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: ptr.To(api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/csid"))),
		},
	}
	_, err = mockResourcesDBClient.HCPClusters(clusterRID.SubscriptionID, clusterRID.ResourceGroupName).Create(ctx, cluster, nil)
	require.NoError(t, err)
	mockCS.EXPECT().GetClusterProvisionShard(gomock.Any(), *cluster.ServiceProviderProperties.ClusterServiceID).Return(shard1, nil).AnyTimes()

	spcOnShard1 := &api.ServiceProviderCluster{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: spcOnShard1ResourceID},
		ResourceID:     *spcOnShard1ResourceID,
		Status: api.ServiceProviderClusterStatus{
			MaestroReadonlyBundles: api.MaestroBundleReferenceList{
				{MaestroAPIMaestroBundleName: "bundle-X"},
			},
		},
	}
	parent := spcOnShard1ResourceID.Parent
	_, err = mockResourcesDBClient.ServiceProviderClusters(parent.SubscriptionID, parent.ResourceGroupName, parent.Name).Create(ctx, spcOnShard1, nil)
	require.NoError(t, err)

	clients := map[string]*shardMaestroClient{
		shard1.ID(): {maestroClient: mockShard1, maestroClientCancelFunc: func() {}},
		shard2.ID(): {maestroClient: mockShard2, maestroClientCancelFunc: func() {}},
	}

	mockShard1.EXPECT().List(gomock.Any(), gomock.Any()).Return(&workv1.ManifestWorkList{Items: []workv1.ManifestWork{}}, nil)

	bundleXMeta := metav1.ObjectMeta{
		Name:      "bundle-X",
		Namespace: "consumer2",
		UID:       types.UID("bundle-x-uid"),
		Labels:    map[string]string{readonlyBundleManagedByK8sLabelKey: readonlyBundleManagedByK8sLabelValueClusterScoped},
	}
	bundleListShard2 := &workv1.ManifestWorkList{
		Items: []workv1.ManifestWork{
			{ObjectMeta: bundleXMeta},
		},
	}
	mockShard2.EXPECT().List(gomock.Any(), gomock.Any()).Return(bundleListShard2, nil)
	mockShard2.EXPECT().Delete(gomock.Any(), "bundle-X", metav1.DeleteOptions{}).Return(nil)

	c := &deleteOrphanedMaestroReadonlyBundles{resourcesDBClient: mockResourcesDBClient, clusterServiceClient: mockCS}
	err = c.ensureClusterScopedOrphanedMaestroReadonlyBundlesAreDeleted(ctx, clients)
	require.NoError(t, err)
}

// TestDeleteOrphanedMaestroReadonlyBundles_ensureClusterScopedOrphanedMaestroReadonlyBundlesAreDeleted_bundleOnlyOnShardANoDeleteOnShardB
// verifies that bundle N exists only on shard A's Maestro and is referenced by an SPC on shard A, while shard B's
// Maestro lists no such bundle: processing shard B never issues a Delete for N (and shard A skips N as referenced).
func TestDeleteOrphanedMaestroReadonlyBundles_ensureClusterScopedOrphanedMaestroReadonlyBundlesAreDeleted_bundleOnlyOnShardANoDeleteOnShardB(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	ctrl := gomock.NewController(t)
	mockShardA := maestro.NewMockClient(ctrl)
	mockShardB := maestro.NewMockClient(ctrl)
	mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
	mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)

	shardA, err := arohcpv1alpha1.NewProvisionShard().
		ID("11111111111111111111111111111111").
		MaestroConfig(
			arohcpv1alpha1.NewProvisionShardMaestroConfig().
				ConsumerName("consumer-a").
				RestApiConfig(arohcpv1alpha1.NewProvisionShardMaestroRestApiConfig().Url("https://maestro-a.example.com:443")).
				GrpcApiConfig(arohcpv1alpha1.NewProvisionShardMaestroGrpcApiConfig().Url("https://maestro-a.example.com:444")),
		).
		Build()
	require.NoError(t, err)
	shardB, err := arohcpv1alpha1.NewProvisionShard().
		ID("33333333333333333333333333333333").
		MaestroConfig(
			arohcpv1alpha1.NewProvisionShardMaestroConfig().
				ConsumerName("consumer-b").
				RestApiConfig(arohcpv1alpha1.NewProvisionShardMaestroRestApiConfig().Url("https://maestro-b.example.com:443")).
				GrpcApiConfig(arohcpv1alpha1.NewProvisionShardMaestroGrpcApiConfig().Url("https://maestro-b.example.com:444")),
		).
		Build()
	require.NoError(t, err)

	spcResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-a/serviceProviderClusters/default"))
	clusterRID := spcResourceID.Parent
	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: clusterRID}},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: ptr.To(api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/csid"))),
		},
	}
	_, err = mockResourcesDBClient.HCPClusters(clusterRID.SubscriptionID, clusterRID.ResourceGroupName).Create(ctx, cluster, nil)
	require.NoError(t, err)
	mockCS.EXPECT().GetClusterProvisionShard(gomock.Any(), *cluster.ServiceProviderProperties.ClusterServiceID).Return(shardA, nil).AnyTimes()

	spc := &api.ServiceProviderCluster{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: spcResourceID},
		ResourceID:     *spcResourceID,
		Status: api.ServiceProviderClusterStatus{
			MaestroReadonlyBundles: api.MaestroBundleReferenceList{
				{MaestroAPIMaestroBundleName: "bundle-N"},
			},
		},
	}
	parent := spcResourceID.Parent
	_, err = mockResourcesDBClient.ServiceProviderClusters(parent.SubscriptionID, parent.ResourceGroupName, parent.Name).Create(ctx, spc, nil)
	require.NoError(t, err)

	clients := map[string]*shardMaestroClient{
		shardA.ID(): {maestroClient: mockShardA, maestroClientCancelFunc: func() {}},
		shardB.ID(): {maestroClient: mockShardB, maestroClientCancelFunc: func() {}},
	}

	bundleNMeta := metav1.ObjectMeta{
		Name:      "bundle-N",
		Namespace: "consumer-a",
		UID:       types.UID("bundle-n-uid"),
		Labels:    map[string]string{readonlyBundleManagedByK8sLabelKey: readonlyBundleManagedByK8sLabelValueClusterScoped},
	}
	bundleListShardA := &workv1.ManifestWorkList{
		Items: []workv1.ManifestWork{{ObjectMeta: bundleNMeta}},
	}
	mockShardA.EXPECT().List(gomock.Any(), gomock.Any()).Return(bundleListShardA, nil)
	mockShardB.EXPECT().List(gomock.Any(), gomock.Any()).Return(&workv1.ManifestWorkList{Items: []workv1.ManifestWork{}}, nil)

	c := &deleteOrphanedMaestroReadonlyBundles{resourcesDBClient: mockResourcesDBClient, clusterServiceClient: mockCS}
	err = c.ensureClusterScopedOrphanedMaestroReadonlyBundlesAreDeleted(ctx, clients)
	require.NoError(t, err)
}

// TestDeleteOrphanedMaestroReadonlyBundles_ensureClusterScopedOrphanedMaestroReadonlyBundlesAreDeleted_ReferenceOnlyOnFreshGlobalList
// verifies that an SPC document present only on the second global Cosmos list (not on the first)
// still prevents deletion of the referenced bundle.
func TestDeleteOrphanedMaestroReadonlyBundles_ensureClusterScopedOrphanedMaestroReadonlyBundlesAreDeleted_ReferenceOnlyOnFreshGlobalList(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	ctrl := gomock.NewController(t)
	mockMaestro := maestro.NewMockClient(ctrl)
	mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
	mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
	shard := buildTestProvisionShard("test-consumer")

	spcResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/serviceProviderClusters/default"))
	clusterRID := spcResourceID.Parent
	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: clusterRID}},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: ptr.To(api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/csid"))),
		},
	}
	_, err := mockResourcesDBClient.HCPClusters(clusterRID.SubscriptionID, clusterRID.ResourceGroupName).Create(ctx, cluster, nil)
	require.NoError(t, err)
	mockCS.EXPECT().GetClusterProvisionShard(gomock.Any(), *cluster.ServiceProviderProperties.ClusterServiceID).Return(shard, nil).AnyTimes()

	spc := &api.ServiceProviderCluster{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: spcResourceID},
		ResourceID:     *spcResourceID,
		Status: api.ServiceProviderClusterStatus{
			MaestroReadonlyBundles: api.MaestroBundleReferenceList{
				{MaestroAPIMaestroBundleName: "only-in-global-list"},
			},
		},
	}
	_, err = mockResourcesDBClient.ServiceProviderClusters(clusterRID.SubscriptionID, clusterRID.ResourceGroupName, clusterRID.Name).Create(ctx, spc, nil)
	require.NoError(t, err)

	mockResourcesDBClient.SetResourcesGlobalListers(newOrphanTestResourcesGlobalListersSPCOnly(&emptyFirstThenServiceProviderClusterGlobalLister{items: []*api.ServiceProviderCluster{spc}}))

	clients := map[string]*shardMaestroClient{
		shard.ID(): {
			maestroClient:           mockMaestro,
			maestroClientCancelFunc: func() {},
		},
	}

	bundleList := &workv1.ManifestWorkList{
		Items: []workv1.ManifestWork{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "only-in-global-list",
					Namespace: "consumer",
					Labels:    map[string]string{readonlyBundleManagedByK8sLabelKey: readonlyBundleManagedByK8sLabelValueClusterScoped},
				},
			},
		},
	}
	mockMaestro.EXPECT().List(gomock.Any(), gomock.Any()).Return(bundleList, nil)

	c := &deleteOrphanedMaestroReadonlyBundles{resourcesDBClient: mockResourcesDBClient, clusterServiceClient: mockCS}
	err = c.ensureClusterScopedOrphanedMaestroReadonlyBundlesAreDeleted(ctx, clients)
	require.NoError(t, err)
}

func TestDeleteOrphanedMaestroReadonlyBundles_SyncOnce_FullFlow_DeletesOrphanedBundle(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	ctrl := gomock.NewController(t)
	mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
	mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
	mockMaestroBuilder := maestro.NewMockMaestroClientBuilder(ctrl)
	mockMaestro := maestro.NewMockClient(ctrl)

	clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster"))
	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: clusterResourceID}},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: api.Ptr(api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/csid"))),
		},
	}
	clustersCRUD := mockResourcesDBClient.HCPClusters(clusterResourceID.SubscriptionID, clusterResourceID.ResourceGroupName)
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
	spcCRUD := mockResourcesDBClient.ServiceProviderClusters(clusterResourceID.SubscriptionID, clusterResourceID.ResourceGroupName, clusterResourceID.Name)
	_, err = spcCRUD.Create(ctx, spc, nil)
	require.NoError(t, err)

	provisionShard := buildTestProvisionShard("test-consumer")
	mockCS.EXPECT().ListProvisionShards().Return(ocm.NewSimpleProvisionShardListIterator([]*arohcpv1alpha1.ProvisionShard{provisionShard}, nil))
	mockCS.EXPECT().GetClusterProvisionShard(gomock.Any(), *cluster.ServiceProviderProperties.ClusterServiceID).Return(provisionShard, nil).AnyTimes()
	restEndpoint := provisionShard.MaestroConfig().RestApiConfig().Url()
	grpcEndpoint := provisionShard.MaestroConfig().GrpcApiConfig().Url()
	consumerName := provisionShard.MaestroConfig().ConsumerName()
	sourceID := maestro.GenerateMaestroSourceID("test-env", provisionShard.ID())
	mockMaestroBuilder.EXPECT().NewClient(gomock.Any(), restEndpoint, grpcEndpoint, consumerName, sourceID).Return(mockMaestro, nil)

	orphanFullFlowMeta := metav1.ObjectMeta{
		Name:      "orphaned-bundle",
		Namespace: "consumer",
		UID:       types.UID("fullflow-orphan-uid"),
		Labels:    map[string]string{readonlyBundleManagedByK8sLabelKey: readonlyBundleManagedByK8sLabelValueClusterScoped},
	}
	bundleList := &workv1.ManifestWorkList{
		Items: []workv1.ManifestWork{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kept-bundle",
					Namespace: "consumer",
					Labels:    map[string]string{readonlyBundleManagedByK8sLabelKey: readonlyBundleManagedByK8sLabelValueClusterScoped},
				},
			},
			{ObjectMeta: orphanFullFlowMeta},
		},
	}
	mockMaestro.EXPECT().List(gomock.Any(), gomock.Any()).Return(bundleList, nil)
	mockMaestro.EXPECT().Delete(gomock.Any(), "orphaned-bundle", metav1.DeleteOptions{}).Return(nil)
	// Second Maestro list: nodepool-scoped bundles pass after cluster-scoped orphan cleanup.
	mockMaestro.EXPECT().List(gomock.Any(), gomock.Any()).Return(&workv1.ManifestWorkList{}, nil)

	controller := NewDeleteOrphanedMaestroReadonlyBundlesController(mockResourcesDBClient, mockCS, mockMaestroBuilder, "test-env")
	err = controller.SyncOnce(ctx, nil)
	require.NoError(t, err)
}
