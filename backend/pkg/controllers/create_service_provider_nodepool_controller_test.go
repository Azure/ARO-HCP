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

package controllers

import (
	"context"
	"errors"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const creatorTestNodePoolName = "test-nodepool"

func newCreatorTestNodePoolKey() controllerutils.HCPNodePoolKey {
	return controllerutils.HCPNodePoolKey{
		SubscriptionID:    creatorTestSubscriptionID,
		ResourceGroupName: creatorTestResourceGroup,
		HCPClusterName:    creatorTestClusterName,
		HCPNodePoolName:   creatorTestNodePoolName,
	}
}

func newCreatorTestNodePool(t *testing.T) *api.HCPOpenShiftClusterNodePool {
	t.Helper()
	resourceID := api.Must(api.ToNodePoolResourceID(creatorTestSubscriptionID, creatorTestResourceGroup, creatorTestClusterName, creatorTestNodePoolName))
	return &api.HCPOpenShiftClusterNodePool{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: resourceID},
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   resourceID,
				Name: creatorTestNodePoolName,
				Type: api.NodePoolResourceType.String(),
			},
			Location: "eastus",
		},
	}
}

// boomNodePoolLister returns the configured error from every Get call.
type boomNodePoolLister struct {
	listers.NodePoolLister
	err error
}

func (b *boomNodePoolLister) Get(_ context.Context, _, _, _, _ string) (*api.HCPOpenShiftClusterNodePool, error) {
	return nil, b.err
}

// boomServiceProviderNodePoolLister returns the configured error from every
// Get call.
type boomServiceProviderNodePoolLister struct {
	listers.ServiceProviderNodePoolLister
	err error
}

func (b *boomServiceProviderNodePoolLister) Get(_ context.Context, _, _, _, _ string) (*api.ServiceProviderNodePool, error) {
	return nil, b.err
}

func TestCreateServiceProviderNodePoolSyncer_SyncOnce(t *testing.T) {
	nodePoolResourceID := api.Must(api.ToNodePoolResourceID(creatorTestSubscriptionID, creatorTestResourceGroup, creatorTestClusterName, creatorTestNodePoolName))
	listerBoom := errors.New("lister exploded")

	tests := []struct {
		name             string
		buildSyncer      func(t *testing.T, mockDB *databasetesting.MockResourcesDBClient) *createServiceProviderNodePoolSyncer
		seedDB           func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient)
		wantErrSubstring string
		// wantCreated indicates whether the syncer is expected to have written
		// a ServiceProviderNodePool to Cosmos by the end of the run.
		wantCreated bool
	}{
		{
			name: "node pool missing from lister returns nil and does not write",
			buildSyncer: func(t *testing.T, mockDB *databasetesting.MockResourcesDBClient) *createServiceProviderNodePoolSyncer {
				return &createServiceProviderNodePoolSyncer{
					resourcesDBClient:             mockDB,
					nodePoolLister:                &listertesting.SliceNodePoolLister{},
					serviceProviderNodePoolLister: &listertesting.SliceServiceProviderNodePoolLister{},
				}
			},
			wantCreated: false,
		},
		{
			name: "node pool lister error is propagated",
			buildSyncer: func(t *testing.T, mockDB *databasetesting.MockResourcesDBClient) *createServiceProviderNodePoolSyncer {
				return &createServiceProviderNodePoolSyncer{
					resourcesDBClient:             mockDB,
					nodePoolLister:                &boomNodePoolLister{err: listerBoom},
					serviceProviderNodePoolLister: &listertesting.SliceServiceProviderNodePoolLister{},
				}
			},
			wantErrSubstring: "failed to get HCPNodePool from lister",
			wantCreated:      false,
		},
		{
			name: "ServiceProviderNodePool already in lister is a no-op",
			buildSyncer: func(t *testing.T, mockDB *databasetesting.MockResourcesDBClient) *createServiceProviderNodePoolSyncer {
				spnpResourceID := api.Must(azcorearm.ParseResourceID(nodePoolResourceID.String() + "/" + api.ServiceProviderNodePoolResourceTypeName + "/" + api.ServiceProviderNodePoolResourceName))
				return &createServiceProviderNodePoolSyncer{
					resourcesDBClient: mockDB,
					nodePoolLister: &listertesting.SliceNodePoolLister{
						NodePools: []*api.HCPOpenShiftClusterNodePool{newCreatorTestNodePool(t)},
					},
					serviceProviderNodePoolLister: &listertesting.SliceServiceProviderNodePoolLister{
						ServiceProviderNodePools: []*api.ServiceProviderNodePool{{
							CosmosMetadata: api.CosmosMetadata{ResourceID: spnpResourceID},
						}},
					},
				}
			},
			wantCreated: false,
		},
		{
			name: "ServiceProviderNodePool lister error other than NotFound is propagated",
			buildSyncer: func(t *testing.T, mockDB *databasetesting.MockResourcesDBClient) *createServiceProviderNodePoolSyncer {
				return &createServiceProviderNodePoolSyncer{
					resourcesDBClient: mockDB,
					nodePoolLister: &listertesting.SliceNodePoolLister{
						NodePools: []*api.HCPOpenShiftClusterNodePool{newCreatorTestNodePool(t)},
					},
					serviceProviderNodePoolLister: &boomServiceProviderNodePoolLister{err: listerBoom},
				}
			},
			wantErrSubstring: "failed to get ServiceProviderNodePool from lister",
			wantCreated:      false,
		},
		{
			name: "missing ServiceProviderNodePool is created",
			buildSyncer: func(t *testing.T, mockDB *databasetesting.MockResourcesDBClient) *createServiceProviderNodePoolSyncer {
				return &createServiceProviderNodePoolSyncer{
					resourcesDBClient: mockDB,
					nodePoolLister: &listertesting.SliceNodePoolLister{
						NodePools: []*api.HCPOpenShiftClusterNodePool{newCreatorTestNodePool(t)},
					},
					serviceProviderNodePoolLister: &listertesting.SliceServiceProviderNodePoolLister{},
				}
			},
			wantCreated: true,
		},
		{
			name: "create is idempotent when ServiceProviderNodePool already exists in cosmos",
			buildSyncer: func(t *testing.T, mockDB *databasetesting.MockResourcesDBClient) *createServiceProviderNodePoolSyncer {
				return &createServiceProviderNodePoolSyncer{
					resourcesDBClient: mockDB,
					nodePoolLister: &listertesting.SliceNodePoolLister{
						NodePools: []*api.HCPOpenShiftClusterNodePool{newCreatorTestNodePool(t)},
					},
					serviceProviderNodePoolLister: &listertesting.SliceServiceProviderNodePoolLister{},
				}
			},
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient) {
				_, err := database.GetOrCreateServiceProviderNodePool(ctx, mockDB, nodePoolResourceID)
				require.NoError(t, err)
			},
			wantCreated: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			mockDB := databasetesting.NewMockResourcesDBClient()

			if tc.seedDB != nil {
				tc.seedDB(t, ctx, mockDB)
			}

			syncer := tc.buildSyncer(t, mockDB)

			err := syncer.SyncOnce(ctx, newCreatorTestNodePoolKey())
			if tc.wantErrSubstring != "" {
				require.Error(t, err)
				assert.ErrorContains(t, err, tc.wantErrSubstring)
			} else {
				require.NoError(t, err)
			}

			_, getErr := mockDB.ServiceProviderNodePools(creatorTestSubscriptionID, creatorTestResourceGroup, creatorTestClusterName, creatorTestNodePoolName).Get(ctx, api.ServiceProviderNodePoolResourceName)
			if tc.wantCreated {
				assert.NoError(t, getErr, "expected ServiceProviderNodePool to exist in cosmos")
			} else {
				assert.True(t, database.IsNotFoundError(getErr), "expected ServiceProviderNodePool to be absent, got err=%v", getErr)
			}
		})
	}
}
