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

package creation

import (
	"context"
	"errors"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	creatorTestSubscriptionID = "00000000-0000-0000-0000-000000000000"
	creatorTestResourceGroup  = "test-rg"
	creatorTestClusterName    = "test-cluster"
	creatorTestNodePoolName   = "test-nodepool"
)

func newCreatorTestNodePoolKey() controllerutils.HCPNodePoolKey {
	return controllerutils.HCPNodePoolKey{
		SubscriptionID:    creatorTestSubscriptionID,
		ResourceGroupName: creatorTestResourceGroup,
		HCPClusterName:    creatorTestClusterName,
		HCPNodePoolName:   creatorTestNodePoolName,
	}
}

func newCreatorTestNodePool(t *testing.T) *coreapi.HCPOpenShiftClusterNodePool {
	t.Helper()
	resourceID := metadataapi.Must(coreapi.ToNodePoolResourceID(creatorTestSubscriptionID, creatorTestResourceGroup, creatorTestClusterName, creatorTestNodePoolName))
	return &coreapi.HCPOpenShiftClusterNodePool{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: resourceID},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID:   resourceID,
				Name: creatorTestNodePoolName,
				Type: coreapi.NodePoolResourceType.String(),
			},
			Location: "eastus",
		},
	}
}

// boomNodePoolLister returns the configured error from every Get call.
type boomNodePoolLister struct {
	corelisters.NodePoolLister
	err error
}

func (b *boomNodePoolLister) Get(_ context.Context, _, _, _, _ string) (*coreapi.HCPOpenShiftClusterNodePool, error) {
	return nil, b.err
}

// boomServiceProviderNodePoolLister returns the configured error from every
// Get call.
type boomServiceProviderNodePoolLister struct {
	corelisters.ServiceProviderNodePoolLister
	err error
}

func (b *boomServiceProviderNodePoolLister) Get(_ context.Context, _, _, _, _ string) (*coreapi.ServiceProviderNodePool, error) {
	return nil, b.err
}

func TestCreateServiceProviderNodePoolSyncer_SyncOnce(t *testing.T) {
	nodePoolResourceID := metadataapi.Must(coreapi.ToNodePoolResourceID(creatorTestSubscriptionID, creatorTestResourceGroup, creatorTestClusterName, creatorTestNodePoolName))
	listerBoom := errors.New("lister exploded")

	tests := []struct {
		name             string
		buildSyncer      func(t *testing.T, mockDB *corecosmosstoragetesting.MockResourcesDBClient) *createServiceProviderNodePoolSyncer
		seedDB           func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient)
		wantErrSubstring string
		// wantCreated indicates whether the syncer is expected to have written
		// a ServiceProviderNodePool to Cosmos by the end of the run.
		wantCreated bool
	}{
		{
			name: "node pool missing from lister returns nil and does not write",
			buildSyncer: func(t *testing.T, mockDB *corecosmosstoragetesting.MockResourcesDBClient) *createServiceProviderNodePoolSyncer {
				return &createServiceProviderNodePoolSyncer{
					resourcesDBClient:             mockDB,
					nodePoolLister:                &corelistertesting.SliceNodePoolLister{},
					serviceProviderNodePoolLister: &corelistertesting.SliceServiceProviderNodePoolLister{},
				}
			},
			wantCreated: false,
		},
		{
			name: "node pool lister error is propagated",
			buildSyncer: func(t *testing.T, mockDB *corecosmosstoragetesting.MockResourcesDBClient) *createServiceProviderNodePoolSyncer {
				return &createServiceProviderNodePoolSyncer{
					resourcesDBClient:             mockDB,
					nodePoolLister:                &boomNodePoolLister{err: listerBoom},
					serviceProviderNodePoolLister: &corelistertesting.SliceServiceProviderNodePoolLister{},
				}
			},
			wantErrSubstring: "failed to get HCPNodePool from lister",
			wantCreated:      false,
		},
		{
			name: "ServiceProviderNodePool already in lister is a no-op",
			buildSyncer: func(t *testing.T, mockDB *corecosmosstoragetesting.MockResourcesDBClient) *createServiceProviderNodePoolSyncer {
				spnpResourceID := metadataapi.Must(azcorearm.ParseResourceID(nodePoolResourceID.String() + "/" + coreapi.ServiceProviderNodePoolResourceTypeName + "/" + coreapi.ServiceProviderNodePoolResourceName))
				return &createServiceProviderNodePoolSyncer{
					resourcesDBClient: mockDB,
					nodePoolLister: &corelistertesting.SliceNodePoolLister{
						NodePools: []*coreapi.HCPOpenShiftClusterNodePool{newCreatorTestNodePool(t)},
					},
					serviceProviderNodePoolLister: &corelistertesting.SliceServiceProviderNodePoolLister{
						ServiceProviderNodePools: []*coreapi.ServiceProviderNodePool{{
							CosmosMetadata: coreapi.CosmosMetadata{ResourceID: spnpResourceID},
						}},
					},
				}
			},
			wantCreated: false,
		},
		{
			name: "ServiceProviderNodePool lister error other than NotFound is propagated",
			buildSyncer: func(t *testing.T, mockDB *corecosmosstoragetesting.MockResourcesDBClient) *createServiceProviderNodePoolSyncer {
				return &createServiceProviderNodePoolSyncer{
					resourcesDBClient: mockDB,
					nodePoolLister: &corelistertesting.SliceNodePoolLister{
						NodePools: []*coreapi.HCPOpenShiftClusterNodePool{newCreatorTestNodePool(t)},
					},
					serviceProviderNodePoolLister: &boomServiceProviderNodePoolLister{err: listerBoom},
				}
			},
			wantErrSubstring: "failed to get ServiceProviderNodePool from lister",
			wantCreated:      false,
		},
		{
			name: "node pool marked for deletion is a no-op (do not recreate child during cleanup)",
			buildSyncer: func(t *testing.T, mockDB *corecosmosstoragetesting.MockResourcesDBClient) *createServiceProviderNodePoolSyncer {
				deletingNodePool := newCreatorTestNodePool(t)
				deletingNodePool.ServiceProviderProperties.DeletionTimestamp = ptr.To(metav1.Now())
				return &createServiceProviderNodePoolSyncer{
					resourcesDBClient: mockDB,
					nodePoolLister: &corelistertesting.SliceNodePoolLister{
						NodePools: []*coreapi.HCPOpenShiftClusterNodePool{deletingNodePool},
					},
					serviceProviderNodePoolLister: &corelistertesting.SliceServiceProviderNodePoolLister{},
				}
			},
			wantCreated: false,
		},
		{
			name: "missing ServiceProviderNodePool is created",
			buildSyncer: func(t *testing.T, mockDB *corecosmosstoragetesting.MockResourcesDBClient) *createServiceProviderNodePoolSyncer {
				return &createServiceProviderNodePoolSyncer{
					resourcesDBClient: mockDB,
					nodePoolLister: &corelistertesting.SliceNodePoolLister{
						NodePools: []*coreapi.HCPOpenShiftClusterNodePool{newCreatorTestNodePool(t)},
					},
					serviceProviderNodePoolLister: &corelistertesting.SliceServiceProviderNodePoolLister{},
				}
			},
			wantCreated: true,
		},
		{
			name: "create is idempotent when ServiceProviderNodePool already exists in cosmos",
			buildSyncer: func(t *testing.T, mockDB *corecosmosstoragetesting.MockResourcesDBClient) *createServiceProviderNodePoolSyncer {
				return &createServiceProviderNodePoolSyncer{
					resourcesDBClient: mockDB,
					nodePoolLister: &corelistertesting.SliceNodePoolLister{
						NodePools: []*coreapi.HCPOpenShiftClusterNodePool{newCreatorTestNodePool(t)},
					},
					serviceProviderNodePoolLister: &corelistertesting.SliceServiceProviderNodePoolLister{},
				}
			},
			seedDB: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				_, err := corecosmosstorage.GetOrCreateServiceProviderNodePool(ctx, mockDB, nodePoolResourceID)
				require.NoError(t, err)
			},
			wantCreated: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()

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

			_, getErr := mockDB.ServiceProviderNodePools(creatorTestSubscriptionID, creatorTestResourceGroup, creatorTestClusterName, creatorTestNodePoolName).Get(ctx, coreapi.ServiceProviderNodePoolResourceName)
			if tc.wantCreated {
				assert.NoError(t, getErr, "expected ServiceProviderNodePool to exist in cosmos")
			} else {
				assert.True(t, cosmosstorageutils.IsNotFoundError(getErr), "expected ServiceProviderNodePool to be absent, got err=%v", getErr)
			}
		})
	}
}
