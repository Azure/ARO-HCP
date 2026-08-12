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

package properties

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
)

const (
	testIdentityResourceID = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-identity"
	testClientID           = "client-id-123"
	testPrincipalID        = "principal-id-456"
	testLocation           = "test-location"
)

func TestIdentityMigrationSyncer_SyncOnce(t *testing.T) {
	mixedCaseIdentityResourceID := "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/Test-RG/providers/Microsoft.ManagedIdentity/userAssignedIdentities/Test-Identity"

	testCases := []struct {
		name                        string
		cachedCluster               *coreapi.HCPOpenShiftCluster // cluster in cache, nil means use same as existingCluster
		existingCluster             *coreapi.HCPOpenShiftCluster // cluster in cosmos
		existingSPC                 *coreapi.ServiceProviderCluster
		expectError                 bool
		expectedHasIdentity         bool
		expectedIdentityCount       int
		expectedIdentityResourceIDs []string
		expectedClientID            *string
		expectedPrincipalID         *string
	}{
		{
			name: "cache indicates no work needed - identity already set",
			cachedCluster: newTestClusterForIdentityMigration(func(c *coreapi.HCPOpenShiftCluster) {
				c.Identity = &coreapi.ManagedServiceIdentity{
					UserAssignedIdentities: map[string]*coreapi.UserAssignedIdentity{
						testIdentityResourceID: {
							ClientID:    stringPtr(testClientID),
							PrincipalID: stringPtr(testPrincipalID),
						},
					},
				}
			}),
			existingCluster: newTestClusterForIdentityMigration(func(c *coreapi.HCPOpenShiftCluster) {
				c.Identity = &coreapi.ManagedServiceIdentity{
					UserAssignedIdentities: map[string]*coreapi.UserAssignedIdentity{
						testIdentityResourceID: {
							ClientID:    stringPtr(testClientID),
							PrincipalID: stringPtr(testPrincipalID),
						},
					},
				}
			}),
			expectError:                 false,
			expectedHasIdentity:         true,
			expectedIdentityCount:       1,
			expectedIdentityResourceIDs: []string{testIdentityResourceID},
			expectedClientID:            stringPtr(testClientID),
			expectedPrincipalID:         stringPtr(testPrincipalID),
		},
		{
			name:          "cache says work needed but live data has identity",
			cachedCluster: newTestClusterForIdentityMigration(func(c *coreapi.HCPOpenShiftCluster) {
				c.Identity = &coreapi.ManagedServiceIdentity{
					UserAssignedIdentities: map[string]*coreapi.UserAssignedIdentity{
						testIdentityResourceID: {},
					},
				}
			}),
			existingCluster: newTestClusterForIdentityMigration(func(c *coreapi.HCPOpenShiftCluster) {
				// cosmos has identity filled (cache is stale)
				c.Identity = &coreapi.ManagedServiceIdentity{
					UserAssignedIdentities: map[string]*coreapi.UserAssignedIdentity{
						testIdentityResourceID: {
							ClientID:    stringPtr(testClientID),
							PrincipalID: stringPtr(testPrincipalID),
						},
					},
				}
			}),
			expectError:                 false,
			expectedHasIdentity:         true,
			expectedIdentityCount:       1,
			expectedIdentityResourceIDs: []string{testIdentityResourceID},
			expectedClientID:            stringPtr(testClientID),
			expectedPrincipalID:         stringPtr(testPrincipalID),
		},
		{
			name: "no work to do - identity already populated",
			existingCluster: newTestClusterForIdentityMigration(func(c *coreapi.HCPOpenShiftCluster) {
				c.Identity = &coreapi.ManagedServiceIdentity{
					UserAssignedIdentities: map[string]*coreapi.UserAssignedIdentity{
						testIdentityResourceID: {
							ClientID:    stringPtr(testClientID),
							PrincipalID: stringPtr(testPrincipalID),
						},
					},
				}
			}),
			expectError:                 false,
			expectedHasIdentity:         true,
			expectedIdentityCount:       1,
			expectedIdentityResourceIDs: []string{testIdentityResourceID},
			expectedClientID:            stringPtr(testClientID),
			expectedPrincipalID:         stringPtr(testPrincipalID),
		},
		{
			name: "no work to do - identity is nil",
			existingCluster: newTestClusterForIdentityMigration(),
			existingSPC:     newTestSPCWithMSIIdentity("test-operator", testIdentityResourceID, testClientID, testPrincipalID),
			expectError:     false,
			expectedHasIdentity: false,
			expectedIdentityCount: 0,
		},
		{
			name: "success - fill ClientID/PrincipalID from SPC",
			existingCluster: newTestClusterForIdentityMigration(func(c *coreapi.HCPOpenShiftCluster) {
				c.Identity = &coreapi.ManagedServiceIdentity{
					UserAssignedIdentities: map[string]*coreapi.UserAssignedIdentity{
						testIdentityResourceID: {},
					},
				}
			}),
			existingSPC:                 newTestSPCWithMSIIdentity("test-operator", testIdentityResourceID, testClientID, testPrincipalID),
			expectError:                 false,
			expectedHasIdentity:         true,
			expectedIdentityCount:       1,
			expectedIdentityResourceIDs: []string{testIdentityResourceID},
			expectedClientID:            stringPtr(testClientID),
			expectedPrincipalID:         stringPtr(testPrincipalID),
		},
		{
			name: "success - fill nil identity map entry from SPC",
			existingCluster: newTestClusterForIdentityMigration(func(c *coreapi.HCPOpenShiftCluster) {
				c.Identity = &coreapi.ManagedServiceIdentity{
					UserAssignedIdentities: map[string]*coreapi.UserAssignedIdentity{
						testIdentityResourceID: nil,
					},
				}
			}),
			existingSPC:                 newTestSPCWithMSIIdentity("test-operator", testIdentityResourceID, testClientID, testPrincipalID),
			expectError:                 false,
			expectedHasIdentity:         true,
			expectedIdentityCount:       1,
			expectedIdentityResourceIDs: []string{testIdentityResourceID},
			expectedClientID:            stringPtr(testClientID),
			expectedPrincipalID:         stringPtr(testPrincipalID),
		},
		{
			name: "preserves mixed-case identity keys and looks up SPC by lowercase",
			existingCluster: newTestClusterForIdentityMigration(func(c *coreapi.HCPOpenShiftCluster) {
				c.Identity = &coreapi.ManagedServiceIdentity{
					UserAssignedIdentities: map[string]*coreapi.UserAssignedIdentity{
						mixedCaseIdentityResourceID: {},
					},
				}
			}),
			existingSPC:                 newTestSPCWithMSIIdentity("test-operator", mixedCaseIdentityResourceID, testClientID, testPrincipalID),
			expectError:                 false,
			expectedHasIdentity:         true,
			expectedIdentityCount:       1,
			expectedIdentityResourceIDs: []string{mixedCaseIdentityResourceID},
			expectedClientID:            stringPtr(testClientID),
			expectedPrincipalID:         stringPtr(testPrincipalID),
		},
		{
			name: "keeps identity keys unchanged when SPC has no matching entry",
			existingCluster: newTestClusterForIdentityMigration(func(c *coreapi.HCPOpenShiftCluster) {
				c.Identity = &coreapi.ManagedServiceIdentity{
					UserAssignedIdentities: map[string]*coreapi.UserAssignedIdentity{
						testIdentityResourceID: {},
					},
				}
			}),
			existingSPC:                 newTestSPC(),
			expectError:                 false,
			expectedHasIdentity:         true,
			expectedIdentityCount:       1,
			expectedIdentityResourceIDs: []string{testIdentityResourceID},
			expectedClientID:            nil,
			expectedPrincipalID:         nil,
		},
		{
			name: "keeps identity keys unchanged when SPC is missing",
			existingCluster: newTestClusterForIdentityMigration(func(c *coreapi.HCPOpenShiftCluster) {
				c.Identity = &coreapi.ManagedServiceIdentity{
					UserAssignedIdentities: map[string]*coreapi.UserAssignedIdentity{
						testIdentityResourceID: {},
					},
				}
			}),
			existingSPC:                 nil,
			expectError:                 false,
			expectedHasIdentity:         true,
			expectedIdentityCount:       1,
			expectedIdentityResourceIDs: []string{testIdentityResourceID},
			expectedClientID:            nil,
			expectedPrincipalID:         nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()

			// Setup mock DB
			mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()

			// Create the cluster in the mock DB (cosmos)
			clusterCRUD := mockResourcesDBClient.HCPClusters(testSubscriptionID, testResourceGroupName)
			_, err := clusterCRUD.Create(ctx, tc.existingCluster, nil)
			require.NoError(t, err)

			// Setup slice cluster lister (cache)
			// If cachedCluster is nil, use the same as existingCluster
			cachedCluster := tc.cachedCluster
			if cachedCluster == nil {
				cachedCluster = tc.existingCluster
			}
			sliceClusterLister := &corelistertesting.SliceClusterLister{
				Clusters: []*coreapi.HCPOpenShiftCluster{cachedCluster},
			}

			var spcList []*coreapi.ServiceProviderCluster
			if tc.existingSPC != nil {
				spcList = []*coreapi.ServiceProviderCluster{tc.existingSPC}
			}
			sliceSPCLister := &corelistertesting.SliceServiceProviderClusterLister{
				ServiceProviderClusters: spcList,
			}

			// Create syncer
			syncer := &identityMigrationSyncer{
				clusterLister:                sliceClusterLister,
				serviceProviderClusterLister: sliceSPCLister,
				resourcesDBClient:            mockResourcesDBClient,
			}

			// Execute
			key := controllerutils.HCPClusterKey{
				SubscriptionID:    testSubscriptionID,
				ResourceGroupName: testResourceGroupName,
				HCPClusterName:    testClusterName,
			}
			err = syncer.SyncOnce(ctx, key)

			if tc.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			// Verify the cluster state in Cosmos
			updatedCluster, err := clusterCRUD.Get(ctx, testClusterName)
			require.NoError(t, err)

			if tc.expectedHasIdentity {
				require.NotNil(t, updatedCluster.Identity)
				assert.Len(t, updatedCluster.Identity.UserAssignedIdentities, tc.expectedIdentityCount)
				for _, expectedID := range tc.expectedIdentityResourceIDs {
					identity, exists := updatedCluster.Identity.UserAssignedIdentities[expectedID]
					assert.True(t, exists, "expected identity %s to exist", expectedID)
					if !exists {
						continue
					}
					require.NotNil(t, identity)
					if tc.expectedClientID == nil {
						assert.True(t, identity.ClientID == nil || len(*identity.ClientID) == 0)
					} else {
						require.NotNil(t, identity.ClientID)
						assert.Equal(t, *tc.expectedClientID, *identity.ClientID)
					}
					if tc.expectedPrincipalID == nil {
						assert.True(t, identity.PrincipalID == nil || len(*identity.PrincipalID) == 0)
					} else {
						require.NotNil(t, identity.PrincipalID)
						assert.Equal(t, *tc.expectedPrincipalID, *identity.PrincipalID)
					}
				}
			} else {
				if updatedCluster.Identity != nil {
					assert.Len(t, updatedCluster.Identity.UserAssignedIdentities, tc.expectedIdentityCount)
				}
			}
		})
	}
}

// newTestClusterForIdentityMigration creates a test HCPOpenShiftCluster with default values
// for identity migration testing.
func newTestClusterForIdentityMigration(opts ...func(*coreapi.HCPOpenShiftCluster)) *coreapi.HCPOpenShiftCluster {
	cluster := newTestCluster(testClusterName, opts...)
	cluster.Location = testLocation
	return cluster
}

func newTestSPC() *coreapi.ServiceProviderCluster {
	clusterResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName,
	))
	spcResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		coreapi.ToServiceProviderClusterResourceIDString(testSubscriptionID, testResourceGroupName, testClusterName),
	))
	return &coreapi.ServiceProviderCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   spcResourceID,
			PartitionKey: strings.ToLower(clusterResourceID.SubscriptionID),
		},
	}
}

func newTestSPCWithMSIIdentity(operatorName, identityResourceID, clientID, principalID string) *coreapi.ServiceProviderCluster {
	spc := newTestSPC()
	lowerResourceIDStr := strings.ToLower(identityResourceID)
	spc.Status.MSIManagedIdentities = coreapi.ServiceProviderClusterMSIManagedIdentities{
		Identities: map[string]*coreapi.ServiceProviderClusterMSIManagedIdentity{
			lowerResourceIDStr: {
				OperatorName: operatorName,
				ResourceID:   metadataapi.Must(azcorearm.ParseResourceID(lowerResourceIDStr)),
				ClientID:     stringPtr(clientID),
				PrincipalID:  stringPtr(principalID),
			},
		},
	}
	return spc
}

func stringPtr(s string) *string {
	return &s
}
