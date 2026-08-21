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

package identity

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
)

const (
	testSubscriptionID     = "00000000-0000-0000-0000-000000000000"
	testResourceGroupName  = "test-rg"
	testClusterName        = "test-cluster"
	testIdentityResourceID = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-identity"
	testClientID           = "client-id-123"
	testPrincipalID        = "principal-id-456"
	testLocation           = "test-location"
)

func TestClusterIdentitySyncer_SyncOnce(t *testing.T) {
	mixedCaseIdentityResourceID := "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/Test-RG/providers/Microsoft.ManagedIdentity/userAssignedIdentities/Test-Identity"

	testCases := []struct {
		name                           string
		existingCluster                *coreapi.HCPOpenShiftCluster // cluster in cosmos + cache
		existingServiceProviderCluster *coreapi.ServiceProviderCluster
		expectError                    bool
		expectedHasIdentity            bool
		expectedIdentityCount          int
		expectedIdentityResourceIDs    []string
		expectedClientID               *string
		expectedPrincipalID            *string
	}{
		{
			name: "no work to do - identity already matches ServiceProviderCluster",
			existingCluster: newTestClusterForClusterIdentitySync(func(c *coreapi.HCPOpenShiftCluster) {
				c.Identity = &coreapi.ManagedServiceIdentity{
					UserAssignedIdentities: map[string]*coreapi.UserAssignedIdentity{
						testIdentityResourceID: {
							ClientID:    ptr.To(testClientID),
							PrincipalID: ptr.To(testPrincipalID),
						},
					},
				}
			}),
			existingServiceProviderCluster: newTestServiceProviderClusterWithMSIIdentity(testIdentityResourceID, testClientID, testPrincipalID),
			expectError:                    false,
			expectedHasIdentity:            true,
			expectedIdentityCount:          1,
			expectedIdentityResourceIDs:    []string{testIdentityResourceID},
			expectedClientID:               ptr.To(testClientID),
			expectedPrincipalID:            ptr.To(testPrincipalID),
		},
		{
			name: "no work to do - nil ClientID/PrincipalID already match ServiceProviderCluster not-found values",
			existingCluster: newTestClusterForClusterIdentitySync(func(c *coreapi.HCPOpenShiftCluster) {
				c.Identity = &coreapi.ManagedServiceIdentity{
					UserAssignedIdentities: map[string]*coreapi.UserAssignedIdentity{
						testIdentityResourceID: {},
					},
				}
			}),
			existingServiceProviderCluster: newTestServiceProviderClusterWithMSIIdentityPtrs(testIdentityResourceID, nil, nil),
			expectError:                    false,
			expectedHasIdentity:            true,
			expectedIdentityCount:          1,
			expectedIdentityResourceIDs:    []string{testIdentityResourceID},
			expectedClientID:               nil,
			expectedPrincipalID:            nil,
		},
		{
			name: "success - update ClientID/PrincipalID when ServiceProviderCluster values change",
			existingCluster: newTestClusterForClusterIdentitySync(func(c *coreapi.HCPOpenShiftCluster) {
				c.Identity = &coreapi.ManagedServiceIdentity{
					UserAssignedIdentities: map[string]*coreapi.UserAssignedIdentity{
						testIdentityResourceID: {
							ClientID:    ptr.To("old-client-id"),
							PrincipalID: ptr.To("old-principal-id"),
						},
					},
				}
			}),
			existingServiceProviderCluster: newTestServiceProviderClusterWithMSIIdentity(testIdentityResourceID, testClientID, testPrincipalID),
			expectError:                    false,
			expectedHasIdentity:            true,
			expectedIdentityCount:          1,
			expectedIdentityResourceIDs:    []string{testIdentityResourceID},
			expectedClientID:               ptr.To(testClientID),
			expectedPrincipalID:            ptr.To(testPrincipalID),
		},
		{
			name: "no work to do - deleting cluster",
			existingCluster: newTestClusterForClusterIdentitySync(func(c *coreapi.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: time.Now()}
				c.Identity = &coreapi.ManagedServiceIdentity{
					UserAssignedIdentities: map[string]*coreapi.UserAssignedIdentity{
						testIdentityResourceID: {},
					},
				}
			}),
			existingServiceProviderCluster: newTestServiceProviderClusterWithMSIIdentity(testIdentityResourceID, testClientID, testPrincipalID),
			expectError:                    false,
			expectedHasIdentity:            true,
			expectedIdentityCount:          1,
			expectedIdentityResourceIDs:    []string{testIdentityResourceID},
			expectedClientID:               nil,
			expectedPrincipalID:            nil,
		},
		{
			name:                           "no work to do - identity is nil",
			existingCluster:                newTestClusterForClusterIdentitySync(),
			existingServiceProviderCluster: newTestServiceProviderClusterWithMSIIdentity(testIdentityResourceID, testClientID, testPrincipalID),
			expectError:                    false,
			expectedHasIdentity:            false,
			expectedIdentityCount:          0,
		},
		{
			name: "success - fill ClientID/PrincipalID from ServiceProviderCluster",
			existingCluster: newTestClusterForClusterIdentitySync(func(c *coreapi.HCPOpenShiftCluster) {
				c.Identity = &coreapi.ManagedServiceIdentity{
					UserAssignedIdentities: map[string]*coreapi.UserAssignedIdentity{
						testIdentityResourceID: {},
					},
				}
			}),
			existingServiceProviderCluster: newTestServiceProviderClusterWithMSIIdentity(testIdentityResourceID, testClientID, testPrincipalID),
			expectError:                    false,
			expectedHasIdentity:            true,
			expectedIdentityCount:          1,
			expectedIdentityResourceIDs:    []string{testIdentityResourceID},
			expectedClientID:               ptr.To(testClientID),
			expectedPrincipalID:            ptr.To(testPrincipalID),
		},
		{
			name: "success - fill nil identity map entry from ServiceProviderCluster",
			existingCluster: newTestClusterForClusterIdentitySync(func(c *coreapi.HCPOpenShiftCluster) {
				c.Identity = &coreapi.ManagedServiceIdentity{
					UserAssignedIdentities: map[string]*coreapi.UserAssignedIdentity{
						testIdentityResourceID: nil,
					},
				}
			}),
			existingServiceProviderCluster: newTestServiceProviderClusterWithMSIIdentity(testIdentityResourceID, testClientID, testPrincipalID),
			expectError:                    false,
			expectedHasIdentity:            true,
			expectedIdentityCount:          1,
			expectedIdentityResourceIDs:    []string{testIdentityResourceID},
			expectedClientID:               ptr.To(testClientID),
			expectedPrincipalID:            ptr.To(testPrincipalID),
		},
		{
			name: "preserves mixed-case identity keys and looks up ServiceProviderCluster by lowercase",
			existingCluster: newTestClusterForClusterIdentitySync(func(c *coreapi.HCPOpenShiftCluster) {
				c.Identity = &coreapi.ManagedServiceIdentity{
					UserAssignedIdentities: map[string]*coreapi.UserAssignedIdentity{
						mixedCaseIdentityResourceID: {},
					},
				}
			}),
			existingServiceProviderCluster: newTestServiceProviderClusterWithMSIIdentity(mixedCaseIdentityResourceID, testClientID, testPrincipalID),
			expectError:                    false,
			expectedHasIdentity:            true,
			expectedIdentityCount:          1,
			expectedIdentityResourceIDs:    []string{mixedCaseIdentityResourceID},
			expectedClientID:               ptr.To(testClientID),
			expectedPrincipalID:            ptr.To(testPrincipalID),
		},
		{
			name: "sets empty identity when ServiceProviderCluster has no matching entry",
			existingCluster: newTestClusterForClusterIdentitySync(func(c *coreapi.HCPOpenShiftCluster) {
				c.Identity = &coreapi.ManagedServiceIdentity{
					UserAssignedIdentities: map[string]*coreapi.UserAssignedIdentity{
						testIdentityResourceID: {},
					},
				}
			}),
			existingServiceProviderCluster: newTestServiceProviderCluster(),
			expectError:                    false,
			expectedHasIdentity:            true,
			expectedIdentityCount:          1,
			expectedIdentityResourceIDs:    []string{testIdentityResourceID},
			expectedClientID:               nil,
			expectedPrincipalID:            nil,
		},
		{
			name: "clears stale identity values when ServiceProviderCluster has no matching entry",
			existingCluster: newTestClusterForClusterIdentitySync(func(c *coreapi.HCPOpenShiftCluster) {
				c.Identity = &coreapi.ManagedServiceIdentity{
					UserAssignedIdentities: map[string]*coreapi.UserAssignedIdentity{
						testIdentityResourceID: {
							ClientID:    ptr.To("stale-client-id"),
							PrincipalID: ptr.To("stale-principal-id"),
						},
					},
				}
			}),
			existingServiceProviderCluster: newTestServiceProviderCluster(),
			expectError:                    false,
			expectedHasIdentity:            true,
			expectedIdentityCount:          1,
			expectedIdentityResourceIDs:    []string{testIdentityResourceID},
			expectedClientID:               nil,
			expectedPrincipalID:            nil,
		},
		{
			name: "keeps identity keys unchanged when ServiceProviderCluster is missing",
			existingCluster: newTestClusterForClusterIdentitySync(func(c *coreapi.HCPOpenShiftCluster) {
				c.Identity = &coreapi.ManagedServiceIdentity{
					UserAssignedIdentities: map[string]*coreapi.UserAssignedIdentity{
						testIdentityResourceID: {},
					},
				}
			}),
			existingServiceProviderCluster: nil,
			expectError:                    false,
			expectedHasIdentity:            true,
			expectedIdentityCount:          1,
			expectedIdentityResourceIDs:    []string{testIdentityResourceID},
			expectedClientID:               nil,
			expectedPrincipalID:            nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()

			// Setup mock DB and create the cluster in cosmos.
			mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()
			clusterCRUD := mockResourcesDBClient.HCPClusters(testSubscriptionID, testResourceGroupName)
			_, err := clusterCRUD.Create(ctx, tc.existingCluster, nil)
			require.NoError(t, err)

			// Read the cluster back so the cached copy carries the stored etag.
			// The syncer works from the cache and its Replace relies on that etag.
			cachedCluster, err := clusterCRUD.Get(ctx, testClusterName)
			require.NoError(t, err)
			sliceClusterLister := &corelistertesting.SliceClusterLister{
				Clusters: []*coreapi.HCPOpenShiftCluster{cachedCluster},
			}

			var serviceProviderClusterList []*coreapi.ServiceProviderCluster
			if tc.existingServiceProviderCluster != nil {
				serviceProviderClusterList = []*coreapi.ServiceProviderCluster{tc.existingServiceProviderCluster}
			}
			sliceServiceProviderClusterLister := &corelistertesting.SliceServiceProviderClusterLister{
				ServiceProviderClusters: serviceProviderClusterList,
			}

			syncer := &clusterIdentitySyncer{
				clusterLister:                sliceClusterLister,
				serviceProviderClusterLister: sliceServiceProviderClusterLister,
				resourcesDBClient:            mockResourcesDBClient,
			}

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

// newTestClusterForClusterIdentitySync creates a test HCPOpenShiftCluster with default values
// for cluster identity sync testing.
func newTestClusterForClusterIdentitySync(opts ...func(*coreapi.HCPOpenShiftCluster)) *coreapi.HCPOpenShiftCluster {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName,
	))

	cluster := &coreapi.HCPOpenShiftCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID:   resourceID,
				Name: testClusterName,
				Type: resourceID.ResourceType.String(),
			},
		},
	}
	cluster.Location = testLocation

	for _, opt := range opts {
		opt(cluster)
	}

	return cluster
}

func newTestServiceProviderCluster() *coreapi.ServiceProviderCluster {
	clusterResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName,
	))
	serviceProviderClusterResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		coreapi.ToServiceProviderClusterResourceIDString(testSubscriptionID, testResourceGroupName, testClusterName),
	))
	return &coreapi.ServiceProviderCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   serviceProviderClusterResourceID,
			PartitionKey: strings.ToLower(clusterResourceID.SubscriptionID),
		},
	}
}

func newTestServiceProviderClusterWithMSIIdentity(identityResourceID, clientID, principalID string) *coreapi.ServiceProviderCluster {
	return newTestServiceProviderClusterWithMSIIdentityPtrs(identityResourceID, ptr.To(clientID), ptr.To(principalID))
}

func newTestServiceProviderClusterWithMSIIdentityPtrs(identityResourceID string, clientID, principalID *string) *coreapi.ServiceProviderCluster {
	serviceProviderCluster := newTestServiceProviderCluster()
	lowerResourceIDStr := strings.ToLower(identityResourceID)
	serviceProviderCluster.Status.MSIManagedIdentities = coreapi.ServiceProviderClusterMSIManagedIdentities{
		ControlPlaneOperatorsIdentities: map[string]*coreapi.ServiceProviderClusterControlPlaneOperatorIdentity{
			lowerResourceIDStr: {
				ResourceID:  metadataapi.Must(azcorearm.ParseResourceID(lowerResourceIDStr)),
				ClientID:    clientID,
				PrincipalID: principalID,
			},
		},
	}
	return serviceProviderCluster
}
