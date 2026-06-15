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
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	createSPNPSubscriptionID    = "00000000-0000-0000-0000-000000000000"
	createSPNPResourceGroupName = "test-rg"
	createSPNPClusterName       = "test-cluster"
	createSPNPNodePoolName      = "test-nodepool"
)

func newCreateSPNPNodePoolKey() controllerutils.HCPNodePoolKey {
	return controllerutils.HCPNodePoolKey{
		SubscriptionID:    createSPNPSubscriptionID,
		ResourceGroupName: createSPNPResourceGroupName,
		HCPClusterName:    createSPNPClusterName,
		HCPNodePoolName:   createSPNPNodePoolName,
	}
}

func newCreateSPNPNodePoolResourceID(t *testing.T) *azcorearm.ResourceID {
	t.Helper()
	return api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + createSPNPSubscriptionID +
			"/resourceGroups/" + createSPNPResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + createSPNPClusterName +
			"/nodePools/" + createSPNPNodePoolName))
}

func newCreateSPNPNodePool(t *testing.T, opts ...func(*api.HCPOpenShiftClusterNodePool)) *api.HCPOpenShiftClusterNodePool {
	t.Helper()
	nodePoolResourceID := newCreateSPNPNodePoolResourceID(t)
	nodePool := &api.HCPOpenShiftClusterNodePool{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: nodePoolResourceID},
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   nodePoolResourceID,
				Name: createSPNPNodePoolName,
				Type: api.NodePoolResourceType.String(),
			},
			Location: "eastus",
		},
	}
	for _, opt := range opts {
		opt(nodePool)
	}
	return nodePool
}

func newCreateSPNPServiceProviderNodePool(t *testing.T) *api.ServiceProviderNodePool {
	t.Helper()
	nodePoolResourceID := newCreateSPNPNodePoolResourceID(t)
	spnpResourceID := api.Must(azcorearm.ParseResourceID(
		nodePoolResourceID.String() + "/" + api.ServiceProviderNodePoolResourceTypeName + "/" + api.ServiceProviderNodePoolResourceName))
	return &api.ServiceProviderNodePool{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: spnpResourceID},
	}
}

func TestCreateServiceProviderNodePoolSyncer_SyncOnce(t *testing.T) {
	testKey := newCreateSPNPNodePoolKey()

	tests := []struct {
		name          string
		nodePools     []*api.HCPOpenShiftClusterNodePool
		spnpForLister []*api.ServiceProviderNodePool
		seedSPNPInDB  bool
		wantSPNPInDB  bool
		expectError   bool
	}{
		{
			name:         "node pool missing from cache is a no-op",
			nodePools:    nil,
			wantSPNPInDB: false,
		},
		{
			name: "node pool with deletion timestamp is a no-op",
			nodePools: []*api.HCPOpenShiftClusterNodePool{
				newCreateSPNPNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) {
					np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: time.Now()}
				}),
			},
			wantSPNPInDB: false,
		},
		{
			name: "service provider node pool already in cache is a no-op",
			nodePools: []*api.HCPOpenShiftClusterNodePool{
				newCreateSPNPNodePool(t),
			},
			spnpForLister: []*api.ServiceProviderNodePool{
				newCreateSPNPServiceProviderNodePool(t),
			},
			wantSPNPInDB: false,
		},
		{
			name: "creates service provider node pool when missing from cache and DB",
			nodePools: []*api.HCPOpenShiftClusterNodePool{
				newCreateSPNPNodePool(t),
			},
			wantSPNPInDB: true,
		},
		{
			name: "create conflict when document already exists in DB is treated as success",
			nodePools: []*api.HCPOpenShiftClusterNodePool{
				newCreateSPNPNodePool(t),
			},
			seedSPNPInDB: true,
			wantSPNPInDB: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
			mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
			if tt.seedSPNPInDB {
				_, err := mockResourcesDBClient.ServiceProviderNodePools(
					createSPNPSubscriptionID, createSPNPResourceGroupName, createSPNPClusterName, createSPNPNodePoolName,
				).Create(ctx, newCreateSPNPServiceProviderNodePool(t), nil)
				require.NoError(t, err)
			}

			syncer := &createServiceProviderNodePoolSyncer{
				cooldownChecker: &alwaysAllowCooldownChecker{},
				nodePoolLister: &listertesting.SliceNodePoolLister{
					NodePools: tt.nodePools,
				},
				serviceProviderNodePoolLister: &listertesting.SliceServiceProviderNodePoolLister{
					ServiceProviderNodePools: tt.spnpForLister,
				},
				resourcesDBClient: mockResourcesDBClient,
			}

			err := syncer.SyncOnce(ctx, testKey)
			if tt.expectError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			_, getErr := mockResourcesDBClient.ServiceProviderNodePools(
				createSPNPSubscriptionID, createSPNPResourceGroupName, createSPNPClusterName, createSPNPNodePoolName,
			).Get(ctx, api.ServiceProviderNodePoolResourceName)
			if tt.wantSPNPInDB {
				require.NoError(t, getErr)
			} else {
				assert.True(t, database.IsNotFoundError(getErr))
			}
		})
	}
}
