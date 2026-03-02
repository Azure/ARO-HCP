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

package clusterpropertiescontroller

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

const (
	testIdentityResourceID = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-identity"
	testClientID           = "client-id-123"
	testPrincipalID        = "principal-id-456"
	testLocation           = "test-location"
)

func TestIdentityMigrationSyncer_SyncOnce(t *testing.T) {
	testCases := []struct {
		name                        string
		cachedCluster               *api.HCPOpenShiftCluster // cluster in cache, nil means use same as existingCluster
		existingCluster             *api.HCPOpenShiftCluster // cluster in cosmos
		csCluster                   *arohcpv1alpha1.Cluster
		csError                     error
		expectCosmosGet             bool
		expectCSCall                bool
		expectCosmosUpdate          bool
		expectError                 bool
		expectedHasIdentity         bool
		expectedIdentityCount       int
		expectedIdentityResourceIDs []string
	}{
		{
			name: "cache indicates no work needed - identity already set",
			cachedCluster: newTestClusterForIdentityMigration(func(c *api.HCPOpenShiftCluster) {
				c.Identity = &arm.ManagedServiceIdentity{
					UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{
						testIdentityResourceID: {
							ClientID:    stringPtr(testClientID),
							PrincipalID: stringPtr(testPrincipalID),
						},
					},
				}
			}),
			existingCluster: newTestClusterForIdentityMigration(func(c *api.HCPOpenShiftCluster) {
				c.Identity = &arm.ManagedServiceIdentity{
					UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{
						testIdentityResourceID: {
							ClientID:    stringPtr(testClientID),
							PrincipalID: stringPtr(testPrincipalID),
						},
					},
				}
			}),
			expectCosmosGet:             false,
			expectCSCall:                false,
			expectCosmosUpdate:          false,
			expectError:                 false,
			expectedHasIdentity:         true,
			expectedIdentityCount:       1,
			expectedIdentityResourceIDs: []string{testIdentityResourceID},
		},
		{
			name:          "cache says work needed but live data has identity",
			cachedCluster: newTestClusterForIdentityMigration(), // cache has no identity
			existingCluster: newTestClusterForIdentityMigration(func(c *api.HCPOpenShiftCluster) {
				// cosmos has identity (cache is stale)
				c.Identity = &arm.ManagedServiceIdentity{
					UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{
						testIdentityResourceID: {
							ClientID:    stringPtr(testClientID),
							PrincipalID: stringPtr(testPrincipalID),
						},
					},
				}
			}),
			expectCosmosGet:             true,
			expectCSCall:                false,
			expectCosmosUpdate:          false,
			expectError:                 false,
			expectedHasIdentity:         true,
			expectedIdentityCount:       1,
			expectedIdentityResourceIDs: []string{testIdentityResourceID},
		},
		{
			name: "no work to do - identity already populated",
			existingCluster: newTestClusterForIdentityMigration(func(c *api.HCPOpenShiftCluster) {
				c.Identity = &arm.ManagedServiceIdentity{
					UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{
						testIdentityResourceID: {
							ClientID:    stringPtr(testClientID),
							PrincipalID: stringPtr(testPrincipalID),
						},
					},
				}
			}),
			expectCosmosGet:             false,
			expectCSCall:                false,
			expectCosmosUpdate:          false,
			expectError:                 false,
			expectedHasIdentity:         true,
			expectedIdentityCount:       1,
			expectedIdentityResourceIDs: []string{testIdentityResourceID},
		},
		{
			name:                  "error reading from cluster-service",
			existingCluster:       newTestClusterForIdentityMigration(),
			csError:               fmt.Errorf("connection refused"),
			expectCosmosGet:       true,
			expectCSCall:          true,
			expectCosmosUpdate:    false,
			expectError:           true,
			expectedHasIdentity:   false,
			expectedIdentityCount: 0,
		},
		{
			name:                        "success - migrate identity when nil",
			existingCluster:             newTestClusterForIdentityMigration(),
			csCluster:                   buildCSClusterWithIdentity(testIdentityResourceID, testClientID, testPrincipalID),
			expectCosmosGet:             true,
			expectCSCall:                true,
			expectCosmosUpdate:          true,
			expectError:                 false,
			expectedHasIdentity:         true,
			expectedIdentityCount:       1,
			expectedIdentityResourceIDs: []string{testIdentityResourceID},
		},
		{
			name: "success - migrate identity when empty map",
			existingCluster: newTestClusterForIdentityMigration(func(c *api.HCPOpenShiftCluster) {
				c.Identity = &arm.ManagedServiceIdentity{
					UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{},
				}
			}),
			csCluster:                   buildCSClusterWithIdentity(testIdentityResourceID, testClientID, testPrincipalID),
			expectCosmosGet:             true,
			expectCSCall:                true,
			expectCosmosUpdate:          true,
			expectError:                 false,
			expectedHasIdentity:         true,
			expectedIdentityCount:       1,
			expectedIdentityResourceIDs: []string{testIdentityResourceID},
		},
		{
			name: "success - migrate identity when Identity is set but UserAssignedIdentities is nil",
			existingCluster: newTestClusterForIdentityMigration(func(c *api.HCPOpenShiftCluster) {
				c.Identity = &arm.ManagedServiceIdentity{
					Type: arm.ManagedServiceIdentityTypeUserAssigned,
				}
			}),
			csCluster:                   buildCSClusterWithIdentity(testIdentityResourceID, testClientID, testPrincipalID),
			expectCosmosGet:             true,
			expectCSCall:                true,
			expectCosmosUpdate:          true,
			expectError:                 false,
			expectedHasIdentity:         true,
			expectedIdentityCount:       1,
			expectedIdentityResourceIDs: []string{testIdentityResourceID},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			// Setup mock DB
			mockDB := databasetesting.NewMockDBClient()

			// Create the cluster in the mock DB (cosmos)
			clusterCRUD := mockDB.HCPClusters(testSubscriptionID, testResourceGroupName)
			_, err := clusterCRUD.Create(ctx, tc.existingCluster, nil)
			require.NoError(t, err)

			// Setup slice cluster lister (cache)
			// If cachedCluster is nil, use the same as existingCluster
			cachedCluster := tc.cachedCluster
			if cachedCluster == nil {
				cachedCluster = tc.existingCluster
			}
			sliceClusterLister := &listertesting.SliceClusterLister{
				Clusters: []*api.HCPOpenShiftCluster{cachedCluster},
			}

			// Setup mock CS client
			mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)

			if tc.expectCSCall {
				mockCSClient.EXPECT().
					GetCluster(gomock.Any(), api.Must(api.NewInternalID(testClusterServiceIDStr))).
					Return(tc.csCluster, tc.csError)
			}

			// Create syncer
			syncer := &identityMigrationSyncer{
				cooldownChecker:      &alwaysSyncCooldownChecker{},
				clusterLister:        sliceClusterLister,
				cosmosClient:         mockDB,
				clusterServiceClient: mockCSClient,
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
					_, exists := updatedCluster.Identity.UserAssignedIdentities[expectedID]
					assert.True(t, exists, "expected identity %s to exist", expectedID)
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
func newTestClusterForIdentityMigration(opts ...func(*api.HCPOpenShiftCluster)) *api.HCPOpenShiftCluster {
	cluster := newTestCluster(opts...)
	cluster.Location = testLocation
	return cluster
}

// buildCSClusterWithIdentity creates a mock Cluster Service cluster with managed identity information.
func buildCSClusterWithIdentity(identityResourceID, clientID, principalID string) *arohcpv1alpha1.Cluster {
	cluster, err := arohcpv1alpha1.NewCluster().
		Azure(arohcpv1alpha1.NewAzure().
			OperatorsAuthentication(arohcpv1alpha1.NewAzureOperatorsAuthentication().
				ManagedIdentities(arohcpv1alpha1.NewAzureOperatorsAuthenticationManagedIdentities().
					ControlPlaneOperatorsManagedIdentities(map[string]*arohcpv1alpha1.AzureControlPlaneManagedIdentityBuilder{
						"test-operator": arohcpv1alpha1.NewAzureControlPlaneManagedIdentity().
							ResourceID(identityResourceID).
							ClientID(clientID).
							PrincipalID(principalID),
					}).
					DataPlaneOperatorsManagedIdentities(make(map[string]*arohcpv1alpha1.AzureDataPlaneManagedIdentityBuilder)).
					ManagedIdentitiesDataPlaneIdentityUrl("")))).
		Console(arohcpv1alpha1.NewClusterConsole().URL(testConsoleURL)).
		DNS(arohcpv1alpha1.NewDNS().BaseDomain(testBaseDomain)).
		DomainPrefix(testBaseDomainPrefix).
		Build()
	if err != nil {
		panic(err)
	}
	return cluster
}

func stringPtr(s string) *string {
	return &s
}
