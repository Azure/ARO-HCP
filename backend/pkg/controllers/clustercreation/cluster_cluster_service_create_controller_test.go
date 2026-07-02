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

package clustercreation

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/blang/semver/v4"
	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// Test constants
const (
	testSubscriptionID      = "00000000-0000-0000-0000-000000000000"
	testResourceGroupName   = "test-rg"
	testClusterName         = "test-cluster"
	testClusterServiceIDStr = "/api/aro_hcp/v1alpha1/clusters/abc123"
	testTenantID            = "11111111-1111-1111-1111-111111111111"
	testClusterUID          = "00000000-0000-0000-0000-000000000000"
	// testManagedResourceGroup must match what api.MinimumValidClusterTestCase() sets.
	testManagedResourceGroup = "testManagedResourceGroup"
)

// testClusterResourceID builds the ARM resource ID for the test cluster.
func testClusterResourceID() *azcorearm.ResourceID {
	return api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName,
	))
}

// newTestCluster returns an HCPOpenShiftCluster based on MinimumValidClusterTestCase with
// test-constant IDs. Callers can further customize it via functional opts.
// MinimumValidClusterTestCase is used as the base because createClusterServiceCluster
// calls ocm.BuildCSCluster, which requires a fully-populated cluster (version, DNS, subnet, etc.).
func newTestCluster(opts ...func(*api.HCPOpenShiftCluster)) *api.HCPOpenShiftCluster {
	rid := testClusterResourceID()
	cluster := api.MinimumValidClusterTestCase()
	cluster.CosmosMetadata = arm.CosmosMetadata{
		ResourceID:   rid,
		PartitionKey: strings.ToLower(rid.SubscriptionID),
	}
	cluster.ID = rid
	cluster.Name = testClusterName
	cluster.Type = rid.ResourceType.String()
	cluster.ServiceProviderProperties.ClusterServiceID = nil
	cluster.ServiceProviderProperties.ClusterUID = testClusterUID
	for _, opt := range opts {
		opt(cluster)
	}
	return cluster
}

// newTestSubscription returns a minimal Subscription with tenant ID set.
func newTestSubscription() *arm.Subscription {
	rid := api.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID))
	return &arm.Subscription{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID:   rid,
			PartitionKey: strings.ToLower(rid.SubscriptionID),
		},
		ResourceID: rid,
		Properties: &arm.SubscriptionProperties{TenantId: ptr.To(testTenantID)},
	}
}

// newTestSPC returns a ServiceProviderCluster for the test cluster.
// Callers can customize it via functional opts.
func newTestSPC(opts ...func(*api.ServiceProviderCluster)) *api.ServiceProviderCluster {
	resourceID := api.Must(azcorearm.ParseResourceID(fmt.Sprintf("%s/%s/%s",
		testClusterResourceID().String(),
		api.ServiceProviderClusterResourceTypeName,
		api.ServiceProviderClusterResourceName,
	)))
	spc := &api.ServiceProviderCluster{
		CosmosMetadata: api.CosmosMetadata{ResourceID: resourceID},
		Spec:           api.ServiceProviderClusterSpec{},
	}
	spc.SetPartitionKey(testSubscriptionID)
	for _, opt := range opts {
		opt(spc)
	}
	return spc
}

func TestClusterClusterServiceCreate_SyncOnce(t *testing.T) {
	desiredVersion := ptr.To(semver.MustParse("4.20.0"))
	clusterInternalID := api.Must(api.NewInternalID(testClusterServiceIDStr))

	tests := []struct {
		name                           string
		listCluster                    *api.HCPOpenShiftCluster    // cluster seeded into the lister (nil = not found)
		dbCluster                      *api.HCPOpenShiftCluster    // cluster stored in the DB
		existingServiceProviderCluster *api.ServiceProviderCluster // nil = not pre-seeded; controller get-or-creates
		setupMockCS                    func(ctrl *gomock.Controller) ocm.ClusterServiceClientSpec
		expectError                    bool
		verifyDB                       func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient)
	}{
		{
			name:        "successful sync records cluster service ID on cluster",
			listCluster: newTestCluster(),
			dbCluster:   newTestCluster(),
			existingServiceProviderCluster: newTestSPC(func(spc *api.ServiceProviderCluster) {
				spc.Spec.ControlPlaneVersion.DesiredVersion = desiredVersion
			}),
			setupMockCS: func(ctrl *gomock.Controller) ocm.ClusterServiceClientSpec {
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				mockCS.EXPECT().
					ListClusters(gomock.Any()).
					Return(ocm.NewSimpleClusterListIterator(nil, nil))
				csCluster, err := arohcpv1alpha1.NewCluster().
					HREF(testClusterServiceIDStr).
					Build()
				require.NoError(t, err)
				mockCS.EXPECT().
					PostCluster(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(csCluster, nil)
				return mockCS
			},
			expectError: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				require.NotNil(t, cluster.ServiceProviderProperties.ClusterServiceID)
				assert.Equal(t, testClusterServiceIDStr, cluster.ServiceProviderProperties.ClusterServiceID.String())
			},
		},
		{
			name: "skip when cluster already has ClusterServiceID",
			listCluster: newTestCluster(func(c *api.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.ClusterServiceID = &clusterInternalID
			}),
			dbCluster: newTestCluster(func(c *api.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.ClusterServiceID = &clusterInternalID
			}),
			setupMockCS: func(ctrl *gomock.Controller) ocm.ClusterServiceClientSpec {
				return ocm.NewMockClusterServiceClientSpec(ctrl)
			},
			expectError: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				require.NotNil(t, cluster.ServiceProviderProperties.ClusterServiceID)
				assert.Equal(t, testClusterServiceIDStr, cluster.ServiceProviderProperties.ClusterServiceID.String())
			},
		},
		{
			name:        "desired version not set waits without dispatching",
			listCluster: newTestCluster(),
			dbCluster:   newTestCluster(),
			setupMockCS: func(ctrl *gomock.Controller) ocm.ClusterServiceClientSpec {
				return ocm.NewMockClusterServiceClientSpec(ctrl)
			},
			expectError: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				assert.Nil(t, cluster.ServiceProviderProperties.ClusterServiceID)
			},
		},
		{
			name:        "adopts existing Cluster Service cluster for Azure resource",
			listCluster: newTestCluster(),
			dbCluster:   newTestCluster(),
			existingServiceProviderCluster: newTestSPC(func(spc *api.ServiceProviderCluster) {
				spc.Spec.ControlPlaneVersion.DesiredVersion = desiredVersion
			}),
			setupMockCS: func(ctrl *gomock.Controller) ocm.ClusterServiceClientSpec {
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				// Build the CS cluster with Azure fields matching the test cluster so it
				// passes the csClustersMatchingClusterByAzureInfo filter.
				csCluster, err := arohcpv1alpha1.NewCluster().
					HREF(testClusterServiceIDStr).
					Azure(arohcpv1alpha1.NewAzure().
						SubscriptionID(strings.ToLower(testSubscriptionID)).
						ResourceGroupName(strings.ToLower(testResourceGroupName)).
						ResourceName(strings.ToLower(testClusterName)).
						TenantID(testTenantID).
						ManagedResourceGroupName(testManagedResourceGroup)).
					Build()
				require.NoError(t, err)
				mockCS.EXPECT().
					ListClusters(gomock.Any()).
					Return(ocm.NewSimpleClusterListIterator([]*arohcpv1alpha1.Cluster{csCluster}, nil))
				return mockCS
			},
			expectError: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				require.NotNil(t, cluster.ServiceProviderProperties.ClusterServiceID)
				assert.Equal(t, testClusterServiceIDStr, cluster.ServiceProviderProperties.ClusterServiceID.String())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			ctx = utils.ContextWithLogger(ctx, testr.New(t))
			ctrl := gomock.NewController(t)

			subscription := newTestSubscription()
			mockDB, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{subscription, tt.dbCluster})
			require.NoError(t, err)

			if tt.existingServiceProviderCluster != nil {
				_, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Create(ctx, tt.existingServiceProviderCluster, nil)
				require.NoError(t, err)
			}

			mockCS := tt.setupMockCS(ctrl)

			var listerClusters []*api.HCPOpenShiftCluster
			if tt.listCluster != nil {
				listerClusters = []*api.HCPOpenShiftCluster{tt.listCluster}
			}
			syncer := &clusterClusterServiceCreateSyncer{
				resourcesDBClient:     mockDB,
				clusterLister:         &listertesting.SliceClusterLister{Clusters: listerClusters},
				subscriptionLister:    &listertesting.SliceSubscriptionLister{Subscriptions: []*arm.Subscription{subscription}},
				clustersServiceClient: mockCS,
			}

			key := controllerutils.HCPClusterKey{
				SubscriptionID:    testSubscriptionID,
				ResourceGroupName: testResourceGroupName,
				HCPClusterName:    testClusterName,
			}

			err = syncer.SyncOnce(ctx, key)

			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			if tt.verifyDB != nil {
				tt.verifyDB(t, ctx, mockDB)
			}
		})
	}
}

func TestClusterClusterServiceCreate_findAROHCPClusterByAzureInfo(t *testing.T) {
	azureTestCluster := func(t *testing.T, sub, rg, name, tenant, mrg string) *arohcpv1alpha1.Cluster {
		t.Helper()
		c, err := arohcpv1alpha1.NewCluster().
			Name(name).
			Azure(arohcpv1alpha1.NewAzure().
				SubscriptionID(sub).
				ResourceGroupName(rg).
				ResourceName(name).
				TenantID(tenant).
				ManagedResourceGroupName(mrg)).
			Build()
		require.NoError(t, err)
		return c
	}

	ctx := context.Background()
	sub := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	rg := "my-rg"
	resName := "MyCluster"
	tenant := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	mrg := "arohcp-mycluster-uuid"

	wantSub := strings.ToLower(sub)
	wantRG := strings.ToLower(rg)
	wantName := strings.ToLower(resName)
	wantSearch := (&clusterClusterServiceCreateSyncer{}).clustersServiceClusterByAzureInfoSearchString(wantSub, wantRG, wantName, tenant, mrg)

	tests := []struct {
		name        string
		setupMockCS func(t *testing.T, ctrl *gomock.Controller) (ocm.ClusterServiceClientSpec, *arohcpv1alpha1.Cluster)
		wantErr     bool
	}{
		{
			name: "found on primary search",
			setupMockCS: func(t *testing.T, ctrl *gomock.Controller) (ocm.ClusterServiceClientSpec, *arohcpv1alpha1.Cluster) {
				match := azureTestCluster(t, wantSub, wantRG, wantName, tenant, mrg)
				mock := ocm.NewMockClusterServiceClientSpec(ctrl)
				mock.EXPECT().
					ListClusters(wantSearch).
					Return(ocm.NewSimpleClusterListIterator([]*arohcpv1alpha1.Cluster{match}, nil))
				return mock, match
			},
		},
		{
			name: "not found",
			setupMockCS: func(t *testing.T, ctrl *gomock.Controller) (ocm.ClusterServiceClientSpec, *arohcpv1alpha1.Cluster) {
				mock := ocm.NewMockClusterServiceClientSpec(ctrl)
				mock.EXPECT().
					ListClusters(wantSearch).
					Return(ocm.NewSimpleClusterListIterator(nil, nil))
				return mock, nil
			},
		},
		{
			name: "multiple matches error",
			setupMockCS: func(t *testing.T, ctrl *gomock.Controller) (ocm.ClusterServiceClientSpec, *arohcpv1alpha1.Cluster) {
				a := azureTestCluster(t, wantSub, wantRG, wantName, tenant, mrg)
				b := azureTestCluster(t, wantSub, wantRG, wantName, tenant, mrg)
				mock := ocm.NewMockClusterServiceClientSpec(ctrl)
				mock.EXPECT().
					ListClusters(wantSearch).
					Return(ocm.NewSimpleClusterListIterator([]*arohcpv1alpha1.Cluster{a, b}, nil))
				return mock, nil
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockCS, want := tt.setupMockCS(t, ctrl)

			s := &clusterClusterServiceCreateSyncer{clustersServiceClient: mockCS}
			got, err := s.findAROHCPClusterByAzureInfo(ctx, sub, rg, resName, tenant, mrg)

			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			if want != nil {
				require.Same(t, want, got)
			} else {
				require.Nil(t, got)
			}
		})
	}
}
