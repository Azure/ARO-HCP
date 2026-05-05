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

package operationcontrollers

import (
	"context"
	"strings"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"k8s.io/utils/ptr"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestDispatchClusterCreate_SynchronizeOperation(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(*clusterTestFixture) (*api.HCPOpenShiftCluster, *api.Operation)
		expectError bool
		verify      func(t *testing.T, ctx context.Context, db *databasetesting.MockDBClient, fixture *clusterTestFixture, mockCS *ocm.MockClusterServiceClientSpec)
	}{
		{
			name: "successful dispatch records cluster service ID on cluster and operation",
			setup: func(f *clusterTestFixture) (*api.HCPOpenShiftCluster, *api.Operation) {
				cluster := api.MinimumValidClusterTestCase()
				cluster.ID = f.clusterResourceID
				cluster.Name = testClusterName
				cluster.Type = f.clusterResourceID.ResourceType.String()
				cluster.ServiceProviderProperties.ClusterServiceID = nil
				cluster.ServiceProviderProperties.ActiveOperationID = testOperationName
				cluster.ServiceProviderProperties.ClusterUID = testClusterUID
				op := f.newOperation(database.OperationRequestCreate)
				op.InternalID = api.InternalID{}
				return cluster, op
			},
			expectError: false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockDBClient, fixture *clusterTestFixture, _ *ocm.MockClusterServiceClientSpec) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, testClusterServiceIDStr, op.InternalID.String())
				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				require.NotNil(t, cluster.ServiceProviderProperties.ClusterServiceID)
				assert.Equal(t, testClusterServiceIDStr, cluster.ServiceProviderProperties.ClusterServiceID.String())
			},
		},
		{
			name: "recovery when cluster document already has ClusterServiceID",
			setup: func(f *clusterTestFixture) (*api.HCPOpenShiftCluster, *api.Operation) {
				cluster := f.newCluster(nil)
				op := f.newOperation(database.OperationRequestCreate)
				op.InternalID = api.InternalID{}
				return cluster, op
			},
			expectError: false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockDBClient, fixture *clusterTestFixture, _ *ocm.MockClusterServiceClientSpec) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, testClusterServiceIDStr, op.InternalID.String())
			},
		},
		{
			name: "active operation mismatch skips dispatch",
			setup: func(f *clusterTestFixture) (*api.HCPOpenShiftCluster, *api.Operation) {
				cluster := api.MinimumValidClusterTestCase()
				cluster.ID = f.clusterResourceID
				cluster.Name = testClusterName
				cluster.Type = f.clusterResourceID.ResourceType.String()
				cluster.ServiceProviderProperties.ClusterServiceID = nil
				cluster.ServiceProviderProperties.ActiveOperationID = "other-op"
				cluster.ServiceProviderProperties.ClusterUID = testClusterUID
				op := f.newOperation(database.OperationRequestCreate)
				op.InternalID = api.InternalID{}
				return cluster, op
			},
			expectError: false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockDBClient, fixture *clusterTestFixture, _ *ocm.MockClusterServiceClientSpec) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, "", op.InternalID.String())
			},
		},
		{
			name: "missing managed resource group returns error",
			setup: func(f *clusterTestFixture) (*api.HCPOpenShiftCluster, *api.Operation) {
				cluster := api.MinimumValidClusterTestCase()
				cluster.ID = f.clusterResourceID
				cluster.Name = testClusterName
				cluster.Type = f.clusterResourceID.ResourceType.String()
				cluster.ServiceProviderProperties.ClusterServiceID = nil
				cluster.ServiceProviderProperties.ActiveOperationID = testOperationName
				cluster.ServiceProviderProperties.ClusterUID = testClusterUID
				cluster.CustomerProperties.Platform.ManagedResourceGroup = ""
				op := f.newOperation(database.OperationRequestCreate)
				op.InternalID = api.InternalID{}
				return cluster, op
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			ctx = utils.ContextWithLogger(ctx, testr.New(t))
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			fixture := newClusterTestFixture()
			cluster, operation := tt.setup(fixture)

			subscriptionResourceID := api.Must(arm.ToSubscriptionResourceID(testSubscriptionID))
			subscription := &arm.Subscription{
				CosmosMetadata: api.CosmosMetadata{
					ResourceID: subscriptionResourceID,
				},
				ResourceID: subscriptionResourceID,
				Properties: &arm.SubscriptionProperties{
					TenantId: ptr.To(testTenantID),
				},
			}

			mockDB, err := databasetesting.NewMockDBClientWithResources(ctx, []any{subscription, cluster, operation})
			require.NoError(t, err)

			mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)

			switch tt.name {
			case "successful dispatch records cluster service ID on cluster and operation":
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
			case "recovery when cluster document already has ClusterServiceID":
				// no Cluster Service calls
			case "active operation mismatch skips dispatch":
				// no Cluster Service calls
			case "missing managed resource group returns error":
				// no Cluster Service calls
			}

			dispatcher := &dispatchClusterCreate{
				cosmosClient:          mockDB,
				clustersServiceClient: mockCS,
			}

			err = dispatcher.SynchronizeOperation(ctx, fixture.operationKey())

			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			if tt.verify != nil {
				tt.verify(t, ctx, mockDB, fixture, mockCS)
			}
		})
	}
}

func TestDispatchClusterCreate_findAROHCPClusterByAzureInfo(t *testing.T) {
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

	wantSearch := "azure.subscription_id = '" + strings.ToLower(sub) + "' and azure.resource_group_name = '" + strings.ToLower(rg) + "' and azure.resource_name = '" + strings.ToLower(resName) + "'" +
		" and azure.tenant_id = '" + tenant + "'" +
		" and azure.managed_resource_group_name = '" + mrg + "'"

	t.Run("found on primary search", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		match := azureTestCluster(t, strings.ToLower(sub), strings.ToLower(rg), strings.ToLower(resName), tenant, mrg)
		mock := ocm.NewMockClusterServiceClientSpec(ctrl)
		mock.EXPECT().
			ListClusters(wantSearch).
			Return(ocm.NewSimpleClusterListIterator([]*arohcpv1alpha1.Cluster{match}, nil))

		d := &dispatchClusterCreate{clustersServiceClient: mock}
		got, err := d.findAROHCPClusterByAzureInfo(ctx, sub, rg, resName, tenant, mrg)
		require.NoError(t, err)
		require.Same(t, match, got)
	})

	t.Run("not found", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		mock := ocm.NewMockClusterServiceClientSpec(ctrl)
		mock.EXPECT().
			ListClusters(wantSearch).
			Return(ocm.NewSimpleClusterListIterator(nil, nil))

		d := &dispatchClusterCreate{clustersServiceClient: mock}
		got, err := d.findAROHCPClusterByAzureInfo(ctx, sub, rg, resName, tenant, mrg)
		require.NoError(t, err)
		require.Nil(t, got)
	})

	t.Run("multiple matches error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		a := azureTestCluster(t, strings.ToLower(sub), strings.ToLower(rg), strings.ToLower(resName), tenant, mrg)
		b := azureTestCluster(t, strings.ToLower(sub), strings.ToLower(rg), strings.ToLower(resName), tenant, mrg)
		mock := ocm.NewMockClusterServiceClientSpec(ctrl)
		mock.EXPECT().
			ListClusters(wantSearch).
			Return(ocm.NewSimpleClusterListIterator([]*arohcpv1alpha1.Cluster{a, b}, nil))

		d := &dispatchClusterCreate{clustersServiceClient: mock}
		_, err := d.findAROHCPClusterByAzureInfo(ctx, sub, rg, resName, tenant, mrg)
		require.Error(t, err)
	})
}
