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

const (
	creatorTestSubscriptionID = "00000000-0000-0000-0000-000000000000"
	creatorTestResourceGroup  = "test-rg"
	creatorTestClusterName    = "test-cluster"
)

func newCreatorTestClusterKey() controllerutils.HCPClusterKey {
	return controllerutils.HCPClusterKey{
		SubscriptionID:    creatorTestSubscriptionID,
		ResourceGroupName: creatorTestResourceGroup,
		HCPClusterName:    creatorTestClusterName,
	}
}

func newCreatorTestCluster(t *testing.T) *api.HCPOpenShiftCluster {
	t.Helper()
	resourceID := api.Must(api.ToClusterResourceID(creatorTestSubscriptionID, creatorTestResourceGroup, creatorTestClusterName))
	return &api.HCPOpenShiftCluster{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: resourceID},
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   resourceID,
				Name: creatorTestClusterName,
				Type: api.ClusterResourceType.String(),
			},
			Location: "eastus",
		},
	}
}

// boomClusterLister returns the configured error from every Get call. Used to
// exercise the "lister error is propagated" branch.
type boomClusterLister struct {
	listers.ClusterLister
	err error
}

func (b *boomClusterLister) Get(_ context.Context, _, _, _ string) (*api.HCPOpenShiftCluster, error) {
	return nil, b.err
}

// boomServiceProviderClusterLister returns the configured error from every
// Get call.
type boomServiceProviderClusterLister struct {
	listers.ServiceProviderClusterLister
	err error
}

func (b *boomServiceProviderClusterLister) Get(_ context.Context, _, _, _ string) (*api.ServiceProviderCluster, error) {
	return nil, b.err
}

func TestCreateServiceProviderClusterSyncer_SyncOnce(t *testing.T) {
	clusterResourceID := api.Must(api.ToClusterResourceID(creatorTestSubscriptionID, creatorTestResourceGroup, creatorTestClusterName))
	listerBoom := errors.New("lister exploded")

	tests := []struct {
		name             string
		buildSyncer      func(t *testing.T, mockDB *databasetesting.MockResourcesDBClient) *createServiceProviderClusterSyncer
		seedDB           func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient)
		wantErrSubstring string
		// wantCreated indicates whether the syncer is expected to have written
		// a ServiceProviderCluster to Cosmos by the end of the run.
		wantCreated bool
	}{
		{
			name: "cluster missing from lister returns nil and does not write",
			buildSyncer: func(t *testing.T, mockDB *databasetesting.MockResourcesDBClient) *createServiceProviderClusterSyncer {
				return &createServiceProviderClusterSyncer{
					resourcesDBClient:            mockDB,
					clusterLister:                &listertesting.SliceClusterLister{},
					serviceProviderClusterLister: &listertesting.SliceServiceProviderClusterLister{},
				}
			},
			wantCreated: false,
		},
		{
			name: "cluster lister error is propagated",
			buildSyncer: func(t *testing.T, mockDB *databasetesting.MockResourcesDBClient) *createServiceProviderClusterSyncer {
				return &createServiceProviderClusterSyncer{
					resourcesDBClient:            mockDB,
					clusterLister:                &boomClusterLister{err: listerBoom},
					serviceProviderClusterLister: &listertesting.SliceServiceProviderClusterLister{},
				}
			},
			wantErrSubstring: "failed to get HCPCluster from lister",
			wantCreated:      false,
		},
		{
			name: "ServiceProviderCluster already in lister is a no-op",
			buildSyncer: func(t *testing.T, mockDB *databasetesting.MockResourcesDBClient) *createServiceProviderClusterSyncer {
				spcResourceID := api.Must(azcorearm.ParseResourceID(clusterResourceID.String() + "/" + api.ServiceProviderClusterResourceTypeName + "/" + api.ServiceProviderClusterResourceName))
				return &createServiceProviderClusterSyncer{
					resourcesDBClient: mockDB,
					clusterLister: &listertesting.SliceClusterLister{
						Clusters: []*api.HCPOpenShiftCluster{newCreatorTestCluster(t)},
					},
					serviceProviderClusterLister: &listertesting.SliceServiceProviderClusterLister{
						ServiceProviderClusters: []*api.ServiceProviderCluster{{
							CosmosMetadata: api.CosmosMetadata{ResourceID: spcResourceID},
						}},
					},
				}
			},
			// Cosmos was never asked to create — verify by checking that no
			// document exists in the mock DB after the call.
			wantCreated: false,
		},
		{
			name: "ServiceProviderCluster lister error other than NotFound is propagated",
			buildSyncer: func(t *testing.T, mockDB *databasetesting.MockResourcesDBClient) *createServiceProviderClusterSyncer {
				return &createServiceProviderClusterSyncer{
					resourcesDBClient: mockDB,
					clusterLister: &listertesting.SliceClusterLister{
						Clusters: []*api.HCPOpenShiftCluster{newCreatorTestCluster(t)},
					},
					serviceProviderClusterLister: &boomServiceProviderClusterLister{err: listerBoom},
				}
			},
			wantErrSubstring: "failed to get ServiceProviderCluster from lister",
			wantCreated:      false,
		},
		{
			name: "missing ServiceProviderCluster is created",
			buildSyncer: func(t *testing.T, mockDB *databasetesting.MockResourcesDBClient) *createServiceProviderClusterSyncer {
				return &createServiceProviderClusterSyncer{
					resourcesDBClient: mockDB,
					clusterLister: &listertesting.SliceClusterLister{
						Clusters: []*api.HCPOpenShiftCluster{newCreatorTestCluster(t)},
					},
					serviceProviderClusterLister: &listertesting.SliceServiceProviderClusterLister{},
				}
			},
			wantCreated: true,
		},
		{
			name: "create is idempotent when ServiceProviderCluster already exists in cosmos",
			buildSyncer: func(t *testing.T, mockDB *databasetesting.MockResourcesDBClient) *createServiceProviderClusterSyncer {
				return &createServiceProviderClusterSyncer{
					resourcesDBClient: mockDB,
					clusterLister: &listertesting.SliceClusterLister{
						Clusters: []*api.HCPOpenShiftCluster{newCreatorTestCluster(t)},
					},
					// Lister is stale (does not know about the SPC yet) but
					// Cosmos already has it — GetOrCreate must absorb the 409.
					serviceProviderClusterLister: &listertesting.SliceServiceProviderClusterLister{},
				}
			},
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient) {
				_, err := database.GetOrCreateServiceProviderCluster(ctx, mockDB, clusterResourceID)
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

			err := syncer.SyncOnce(ctx, newCreatorTestClusterKey())
			if tc.wantErrSubstring != "" {
				require.Error(t, err)
				assert.ErrorContains(t, err, tc.wantErrSubstring)
			} else {
				require.NoError(t, err)
			}

			_, getErr := mockDB.ServiceProviderClusters(creatorTestSubscriptionID, creatorTestResourceGroup, creatorTestClusterName).Get(ctx, api.ServiceProviderClusterResourceName)
			if tc.wantCreated {
				assert.NoError(t, getErr, "expected ServiceProviderCluster to exist in cosmos")
			} else {
				assert.True(t, database.IsNotFoundError(getErr), "expected ServiceProviderCluster to be absent, got err=%v", getErr)
			}
		})
	}
}
