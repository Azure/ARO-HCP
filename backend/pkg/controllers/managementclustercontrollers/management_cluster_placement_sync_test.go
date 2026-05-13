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

package managementclustercontrollers

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	fleetapi "github.com/Azure/ARO-HCP/internal/apis/fleet"
	resourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources"
	armresourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources/arm"
	dblistertesting "github.com/Azure/ARO-HCP/internal/database/listertesting"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

const (
	testClusterSubscriptionID = "00000000-0000-0000-0000-000000000000"
	testClusterResourceGroup  = "test-rg"
	testClusterName           = "test-cluster"
	testClusterServiceIDStr   = "/api/clusters_mgmt/v1/clusters/abc123"
	testProvisionShardIDStr   = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	testMgmtClusterName       = "mc1"
)

func testClusterResourceID() *azcorearm.ResourceID {
	return resourcesapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testClusterSubscriptionID +
			"/resourceGroups/" + testClusterResourceGroup +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName,
	))
}

func testMgmtClusterResourceID() *azcorearm.ResourceID {
	return resourcesapi.Must(fleetapi.ToManagementClusterResourceID(testMgmtClusterName))
}

func newTestHCPCluster(opts ...func(*resourcesapi.HCPOpenShiftCluster)) *resourcesapi.HCPOpenShiftCluster {
	resourceID := testClusterResourceID()
	clusterServiceID := resourcesapi.Must(resourcesapi.NewInternalID(testClusterServiceIDStr))

	cluster := &resourcesapi.HCPOpenShiftCluster{
		TrackedResource: armresourcesapi.TrackedResource{
			Resource: armresourcesapi.Resource{
				ID:   resourceID,
				Name: testClusterName,
				Type: resourceID.ResourceType.String(),
			},
		},
		ServiceProviderProperties: resourcesapi.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: &clusterServiceID,
		},
	}
	for _, opt := range opts {
		opt(cluster)
	}
	return cluster
}

func newTestSPC(opts ...func(*resourcesapi.ServiceProviderCluster)) *resourcesapi.ServiceProviderCluster {
	clusterResourceID := testClusterResourceID()
	spcResourceID := resourcesapi.Must(azcorearm.ParseResourceID(
		clusterResourceID.String() + "/" + resourcesapi.ServiceProviderClusterResourceTypeName + "/" + resourcesapi.ServiceProviderClusterResourceName,
	))

	spc := &resourcesapi.ServiceProviderCluster{
		CosmosMetadata: resourcesapi.CosmosMetadata{
			ResourceID: spcResourceID,
		},
	}
	for _, opt := range opts {
		opt(spc)
	}
	return spc
}

func newTestManagementCluster() *fleetapi.ManagementCluster {
	resourceID := testMgmtClusterResourceID()
	return &fleetapi.ManagementCluster{
		CosmosMetadata: resourcesapi.CosmosMetadata{
			ResourceID: resourceID,
		},
		ResourceID: resourceID,
		Status: fleetapi.ManagementClusterStatus{
			ClusterServiceProvisionShardID: ptr.To(resourcesapi.Must(resourcesapi.NewInternalID(testProvisionShardHREF(testProvisionShardIDStr)))),
		},
	}
}

// alwaysSyncCooldownChecker always allows syncing
type alwaysSyncCooldownChecker struct{}

func (c *alwaysSyncCooldownChecker) CanSync(ctx context.Context, key any) bool {
	return true
}

func TestManagementClusterPlacementSyncer_SyncOnce(t *testing.T) {
	testCases := []struct {
		name                                string
		cachedSPC                           *resourcesapi.ServiceProviderCluster // SPC in cache, nil means use same as existingSPC
		existingSPC                         *resourcesapi.ServiceProviderCluster // SPC in cosmos
		cachedCluster                       *resourcesapi.HCPOpenShiftCluster    // cluster in cache
		csShard                             *arohcpv1alpha1.ProvisionShard
		csError                             error
		managementClusters                  []*fleetapi.ManagementCluster
		expectCSCall                        bool
		expectError                         bool
		expectedManagementClusterResourceID string // empty means nil
	}{
		{
			name: "cache indicates no work needed - ManagementClusterResourceID already set",
			cachedSPC: newTestSPC(func(spc *resourcesapi.ServiceProviderCluster) {
				spc.Status.ManagementClusterResourceID = testMgmtClusterResourceID()
			}),
			existingSPC: newTestSPC(func(spc *resourcesapi.ServiceProviderCluster) {
				spc.Status.ManagementClusterResourceID = testMgmtClusterResourceID()
			}),
			cachedCluster:                       newTestHCPCluster(),
			expectCSCall:                        false,
			expectError:                         false,
			expectedManagementClusterResourceID: testMgmtClusterResourceID().String(),
		},
		{
			name:      "cache says work needed but live data has ManagementClusterResourceID",
			cachedSPC: newTestSPC(), // cache has no ManagementClusterResourceID
			existingSPC: newTestSPC(func(spc *resourcesapi.ServiceProviderCluster) {
				// cosmos has it (cache is stale)
				spc.Status.ManagementClusterResourceID = testMgmtClusterResourceID()
			}),
			cachedCluster:                       newTestHCPCluster(),
			expectCSCall:                        false,
			expectError:                         false,
			expectedManagementClusterResourceID: testMgmtClusterResourceID().String(),
		},
		{
			name:        "no cluster service ID - skip",
			cachedSPC:   newTestSPC(),
			existingSPC: newTestSPC(),
			cachedCluster: newTestHCPCluster(func(c *resourcesapi.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.ClusterServiceID = &resourcesapi.InternalID{}
			}),
			expectCSCall:                        false,
			expectError:                         false,
			expectedManagementClusterResourceID: "",
		},
		{
			name:                                "provision shard not allocated yet - skip",
			cachedSPC:                           newTestSPC(),
			existingSPC:                         newTestSPC(),
			cachedCluster:                       newTestHCPCluster(),
			csShard:                             resourcesapi.Must(arohcpv1alpha1.NewProvisionShard().Build()),
			managementClusters:                  []*fleetapi.ManagementCluster{newTestManagementCluster()},
			expectCSCall:                        true,
			expectError:                         false,
			expectedManagementClusterResourceID: "",
		},
		{
			name:          "success - resolves provision shard to management cluster",
			cachedSPC:     newTestSPC(),
			existingSPC:   newTestSPC(),
			cachedCluster: newTestHCPCluster(),
			csShard: resourcesapi.Must(arohcpv1alpha1.NewProvisionShard().
				HREF(testProvisionShardHREF(testProvisionShardIDStr)).
				Build()),
			managementClusters:                  []*fleetapi.ManagementCluster{newTestManagementCluster()},
			expectCSCall:                        true,
			expectError:                         false,
			expectedManagementClusterResourceID: testMgmtClusterResourceID().String(),
		},
		{
			name:                                "error - CS call fails",
			cachedSPC:                           newTestSPC(),
			existingSPC:                         newTestSPC(),
			cachedCluster:                       newTestHCPCluster(),
			csError:                             fmt.Errorf("connection refused"),
			managementClusters:                  []*fleetapi.ManagementCluster{newTestManagementCluster()},
			expectCSCall:                        true,
			expectError:                         true,
			expectedManagementClusterResourceID: "",
		},
		{
			name:          "error - invalid provision shard HREF",
			cachedSPC:     newTestSPC(),
			existingSPC:   newTestSPC(),
			cachedCluster: newTestHCPCluster(),
			csShard: resourcesapi.Must(arohcpv1alpha1.NewProvisionShard().
				HREF("unknown-shard-id").
				Build()),
			managementClusters:                  []*fleetapi.ManagementCluster{newTestManagementCluster()},
			expectCSCall:                        true,
			expectError:                         true,
			expectedManagementClusterResourceID: "",
		},
		{
			name:          "error - no management cluster found for provision shard",
			cachedSPC:     newTestSPC(),
			existingSPC:   newTestSPC(),
			cachedCluster: newTestHCPCluster(),
			csShard: resourcesapi.Must(arohcpv1alpha1.NewProvisionShard().
				HREF(testProvisionShardHREF(testProvisionShardIDStr)).
				Build()),
			managementClusters:                  []*fleetapi.ManagementCluster{}, // empty — no match
			expectCSCall:                        true,
			expectError:                         true,
			expectedManagementClusterResourceID: "",
		},
		{
			name:          "error - multiple management clusters for same provision shard",
			cachedSPC:     newTestSPC(),
			existingSPC:   newTestSPC(),
			cachedCluster: newTestHCPCluster(),
			csShard: resourcesapi.Must(arohcpv1alpha1.NewProvisionShard().
				HREF(testProvisionShardHREF(testProvisionShardIDStr)).
				Build()),
			managementClusters: []*fleetapi.ManagementCluster{
				newTestManagementCluster(),
				func() *fleetapi.ManagementCluster {
					mc := newTestManagementCluster()
					mc.ResourceID = resourcesapi.Must(fleetapi.ToManagementClusterResourceID("mc2"))
					return mc
				}(),
			},
			expectCSCall:                        true,
			expectError:                         true,
			expectedManagementClusterResourceID: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			// Setup mock DB
			mockDB := databasetesting.NewMockResourcesDBClient()

			// Create the SPC in cosmos
			spcCRUD := mockDB.ServiceProviderClusters(testClusterSubscriptionID, testClusterResourceGroup, testClusterName)
			_, err := spcCRUD.Create(ctx, tc.existingSPC, nil)
			require.NoError(t, err)

			// Setup SPC lister (cache)
			cachedSPC := tc.cachedSPC
			if cachedSPC == nil {
				cachedSPC = tc.existingSPC
			}
			spcLister := &listertesting.SliceServiceProviderClusterLister{
				ServiceProviderClusters: []*resourcesapi.ServiceProviderCluster{cachedSPC},
			}

			// Setup cluster lister (cache)
			clusterLister := &listertesting.SliceClusterLister{
				Clusters: []*resourcesapi.HCPOpenShiftCluster{tc.cachedCluster},
			}

			// Setup management cluster lister
			mgmtClusterLister := &dblistertesting.SliceManagementClusterLister{
				ManagementClusters: tc.managementClusters,
			}

			// Setup mock CS client
			mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
			if tc.expectCSCall {
				mockCSClient.EXPECT().
					GetClusterProvisionShard(gomock.Any(), resourcesapi.Must(resourcesapi.NewInternalID(testClusterServiceIDStr))).
					Return(tc.csShard, tc.csError)
			}

			// Create syncer
			syncer := &managementClusterPlacementSyncer{
				cooldownChecker:              &alwaysSyncCooldownChecker{},
				serviceProviderClusterLister: spcLister,
				clusterLister:                clusterLister,
				managementClusterLister:      mgmtClusterLister,
				cosmosClient:                 mockDB,
				clusterServiceClient:         mockCSClient,
			}

			// Execute
			key := controllerutils.HCPClusterKey{
				SubscriptionID:    testClusterSubscriptionID,
				ResourceGroupName: testClusterResourceGroup,
				HCPClusterName:    testClusterName,
			}
			err = syncer.SyncOnce(ctx, key)

			if tc.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			// Verify the SPC state in Cosmos
			updatedSPC, err := spcCRUD.Get(ctx, resourcesapi.ServiceProviderClusterResourceName)
			require.NoError(t, err)

			if tc.expectedManagementClusterResourceID != "" {
				require.NotNil(t, updatedSPC.Status.ManagementClusterResourceID)
				assert.Equal(t, tc.expectedManagementClusterResourceID, updatedSPC.Status.ManagementClusterResourceID.String())
			} else {
				assert.Nil(t, updatedSPC.Status.ManagementClusterResourceID)
			}
		})
	}
}
