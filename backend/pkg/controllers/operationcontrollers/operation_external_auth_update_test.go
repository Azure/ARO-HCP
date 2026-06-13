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
	"fmt"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	utilsclock "k8s.io/utils/clock"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestOperationExternalAuthUpdate_SynchronizeOperation(t *testing.T) {
	tests := []struct {
		name              string
		externalAuthProps *api.HCPOpenShiftClusterExternalAuthProperties
		// hashMode controls how the ServiceProviderExternalAuth hash is set:
		//   "matching" - compute from the round-tripped ExternalAuth (hashes match)
		//   "stale"    - use a stale/wrong hash
		//   "none"     - no ServiceProviderExternalAuth doc (GetOrCreate will make one with empty hash)
		hashMode    string
		setupMock   func(ctrl *gomock.Controller, fixture *externalAuthTestFixture) ocm.ClusterServiceClientSpec
		expectError bool
		verify      func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *externalAuthTestFixture)
	}{
		{
			name: "external auth exists and hash matches transitions to succeeded",
			externalAuthProps: &api.HCPOpenShiftClusterExternalAuthProperties{
				ProvisioningState: arm.ProvisioningStateAccepted,
				Issuer: api.TokenIssuerProfile{
					URL:       "https://issuer.example.com",
					Audiences: []string{"aud1"},
				},
				Claim: api.ExternalAuthClaimProfile{
					Mappings: api.TokenClaimMappingsProfile{
						Username: api.UsernameClaimProfile{
							Claim: "sub",
						},
					},
				},
			},
			hashMode: "matching",
			setupMock: func(ctrl *gomock.Controller, fixture *externalAuthTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				externalAuth, _ := arohcpv1alpha1.NewExternalAuth().
					ID(testExternalAuthIDStr).
					Build()
				mockCSClient.EXPECT().
					GetExternalAuth(gomock.Any(), fixture.externalAuthInternalID).
					Return(externalAuth, nil)
				return mockCSClient
			},
			expectError: false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *externalAuthTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, op.Status)

				externalAuth, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).ExternalAuth(testClusterName).Get(ctx, testExternalAuthName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, externalAuth.Properties.ProvisioningState)
				assert.Empty(t, externalAuth.ServiceProviderProperties.ActiveOperationID)
			},
		},
		{
			name: "external auth exists but hash mismatch returns gate error",
			externalAuthProps: &api.HCPOpenShiftClusterExternalAuthProperties{
				ProvisioningState: arm.ProvisioningStateAccepted,
				Issuer: api.TokenIssuerProfile{
					URL:       "https://issuer.example.com",
					Audiences: []string{"aud1"},
				},
				Claim: api.ExternalAuthClaimProfile{
					Mappings: api.TokenClaimMappingsProfile{
						Username: api.UsernameClaimProfile{
							Claim: "sub",
						},
					},
				},
			},
			hashMode: "stale",
			setupMock: func(ctrl *gomock.Controller, fixture *externalAuthTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				externalAuth, _ := arohcpv1alpha1.NewExternalAuth().
					ID(testExternalAuthIDStr).
					Build()
				mockCSClient.EXPECT().
					GetExternalAuth(gomock.Any(), fixture.externalAuthInternalID).
					Return(externalAuth, nil)
				return mockCSClient
			},
			expectError: true,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *externalAuthTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name: "external auth exists but no service provider doc yet returns gate error",
			externalAuthProps: &api.HCPOpenShiftClusterExternalAuthProperties{
				ProvisioningState: arm.ProvisioningStateAccepted,
				Issuer: api.TokenIssuerProfile{
					URL:       "https://issuer.example.com",
					Audiences: []string{"aud1"},
				},
				Claim: api.ExternalAuthClaimProfile{
					Mappings: api.TokenClaimMappingsProfile{
						Username: api.UsernameClaimProfile{
							Claim: "sub",
						},
					},
				},
			},
			hashMode: "none",
			setupMock: func(ctrl *gomock.Controller, fixture *externalAuthTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				externalAuth, _ := arohcpv1alpha1.NewExternalAuth().
					ID(testExternalAuthIDStr).
					Build()
				mockCSClient.EXPECT().
					GetExternalAuth(gomock.Any(), fixture.externalAuthInternalID).
					Return(externalAuth, nil)
				return mockCSClient
			},
			expectError: true,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *externalAuthTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name: "external auth get error from CS returns error",
			externalAuthProps: &api.HCPOpenShiftClusterExternalAuthProperties{
				ProvisioningState: arm.ProvisioningStateAccepted,
				Issuer: api.TokenIssuerProfile{
					URL:       "https://issuer.example.com",
					Audiences: []string{"aud1"},
				},
				Claim: api.ExternalAuthClaimProfile{
					Mappings: api.TokenClaimMappingsProfile{
						Username: api.UsernameClaimProfile{
							Claim: "sub",
						},
					},
				},
			},
			hashMode: "matching",
			setupMock: func(ctrl *gomock.Controller, fixture *externalAuthTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				mockCSClient.EXPECT().
					GetExternalAuth(gomock.Any(), fixture.externalAuthInternalID).
					Return(nil, fmt.Errorf("cluster service error"))
				return mockCSClient
			},
			expectError: true,
			verify:      nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			ctx = utils.ContextWithLogger(ctx, testr.New(t))
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			fixture := newExternalAuthTestFixture()
			cluster := fixture.newCluster()
			externalAuth := fixture.newExternalAuth()
			if tt.externalAuthProps != nil {
				externalAuth.Properties = *tt.externalAuthProps
			}
			operation := fixture.newOperation(database.OperationRequestUpdate)

			// Create the mock DB with cluster, ExternalAuth, and operation.
			mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, externalAuth, operation})
			require.NoError(t, err)

			// For "matching" hash mode, read the ExternalAuth back from the DB
			// to account for cosmos round-trip transformations, then compute the
			// hash from the round-tripped version.
			switch tt.hashMode {
			case "matching":
				roundTripped, err := mockResourcesDBClient.HCPClusters(testSubscriptionID, testResourceGroupName).ExternalAuth(testClusterName).Get(ctx, testExternalAuthName)
				require.NoError(t, err)
				hash, err := ocm.ExternalAuthUpdatableConfigHash(roundTripped)
				require.NoError(t, err)
				spDoc := newServiceProviderExternalAuth(fixture.externalAuthResourceID, hash)
				_, err = mockResourcesDBClient.ServiceProviderExternalAuths(testSubscriptionID, testResourceGroupName, testClusterName, testExternalAuthName).Create(ctx, spDoc, nil)
				require.NoError(t, err)
			case "stale":
				spDoc := newServiceProviderExternalAuth(fixture.externalAuthResourceID, "stale-hash-from-previous-update")
				_, err = mockResourcesDBClient.ServiceProviderExternalAuths(testSubscriptionID, testResourceGroupName, testClusterName, testExternalAuthName).Create(ctx, spDoc, nil)
				require.NoError(t, err)
			case "none":
				// No SP doc created; GetOrCreateServiceProviderExternalAuth in the
				// controller will create one with an empty hash.
			}

			mockCSClient := tt.setupMock(ctrl, fixture)

			controller := &operationExternalAuthUpdate{
				clock:                utilsclock.RealClock{},
				resourcesDBClient:    mockResourcesDBClient,
				clusterServiceClient: mockCSClient,
				notificationClient:   nil,
			}

			err = controller.SynchronizeOperation(ctx, fixture.operationKey())

			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			if tt.verify != nil {
				tt.verify(t, ctx, mockResourcesDBClient, fixture)
			}
		})
	}
}

func newServiceProviderExternalAuth(externalAuthResourceID *azcorearm.ResourceID, hash string) *api.ServiceProviderExternalAuth {
	spResourceID := api.Must(azcorearm.ParseResourceID(
		externalAuthResourceID.String() + "/" + api.ServiceProviderExternalAuthResourceTypeName + "/" + api.ServiceProviderExternalAuthResourceName,
	))
	return &api.ServiceProviderExternalAuth{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: spResourceID,
		},
		Status: api.ServiceProviderExternalAuthStatus{
			ClusterServiceUpdatableConfigHashForUpdateDispatch:        hash,
			ClusterServiceUpdatableConfigHashVersionForUpdateDispatch: ptr.To(ocm.ExternalAuthUpdatableConfigHashVersion),
		},
	}
}
