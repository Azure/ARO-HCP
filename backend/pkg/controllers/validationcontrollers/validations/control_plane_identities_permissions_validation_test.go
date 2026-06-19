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

package validations

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
	checkaccessv2 "github.com/Azure/checkaccess-v2-go-sdk/client"

	"github.com/Azure/ARO-HCP/backend/pkg/azure/cachedreader"
	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/azure"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// --- Test helpers ---

func newTestCluster() *api.HCPOpenShiftCluster {
	cluster := api.MinimumValidClusterTestCase()
	cluster.CustomerProperties.Platform.OperatorsAuthentication = api.OperatorsAuthenticationProfile{
		UserAssignedIdentities: api.UserAssignedIdentitiesProfile{
			ControlPlaneOperators: map[string]*azcorearm.ResourceID{
				"CloudController": api.Must(azcorearm.ParseResourceID("/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/testResourceGroup/providers/Microsoft.ManagedIdentity/userAssignedIdentities/cloud-controller")),
			},
			ServiceManagedIdentity: api.Must(azcorearm.ParseResourceID("/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/testResourceGroup/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi")),
		},
	}
	return cluster
}

func newTestSubscription() *arm.Subscription {
	sub := api.CreateTestSubscription()
	sub.Properties.TenantId = ptr.To(api.TestTenantID)
	return sub
}

func newRoleDefinitionResponseWithDataActions(actions []string, dataActions []string) armauthorization.RoleDefinitionsClientGetByIDResponse {
	actionsSlice := make([]*string, len(actions))
	for i := range actions {
		actionsSlice[i] = ptr.To(actions[i])
	}
	dataActionsSlice := make([]*string, len(dataActions))
	for i := range dataActions {
		dataActionsSlice[i] = ptr.To(dataActions[i])
	}
	permissions := []*armauthorization.Permission{
		{Actions: actionsSlice, DataActions: dataActionsSlice},
	}
	return armauthorization.RoleDefinitionsClientGetByIDResponse{
		RoleDefinition: armauthorization.RoleDefinition{
			ID: ptr.To("/subscriptions/11111111-1111-1111-1111-111111111111/providers/Microsoft.Authorization/roleDefinitions/test-role-def"),
			Properties: &armauthorization.RoleDefinitionProperties{
				Permissions: permissions,
			},
		},
	}
}

// fakeToken returns a non-expired access token for use in tests.
func fakeToken() azcore.AccessToken {
	return azcore.AccessToken{Token: "fake-token", ExpiresOn: time.Now().Add(time.Hour)}
}

// successfulRetriever returns a mock retriever that always yields fakeToken().
func successfulRetriever(ctrl *gomock.Controller) *azureclient.MockMIDataplaneBasedIdentityAccessTokenRetriever {
	mock := azureclient.NewMockMIDataplaneBasedIdentityAccessTokenRetriever(ctrl)
	mock.EXPECT().GetToken(gomock.Any(), gomock.Any()).Return(fakeToken(), nil).AnyTimes()
	return mock
}

// mockSuccessfulTokenRetrieverBuilder returns a builder whose Build always returns
// a retriever that issues a valid fake token, with no MI Dataplane interaction.
func mockSuccessfulTokenRetrieverBuilder(ctrl *gomock.Controller) *azureclient.MockMIDataplaneBasedIdentityAccessTokenRetrieverBuilder {
	mock := azureclient.NewMockMIDataplaneBasedIdentityAccessTokenRetrieverBuilder(ctrl)
	mock.EXPECT().Build(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ string, _ *azcorearm.ResourceID) (azureclient.MIDataplaneBasedIdentityAccessTokenRetriever, error) {
			return successfulRetriever(ctrl), nil
		}).AnyTimes()
	return mock
}

// testIdentitiesConfig builds a ClusterScopedIdentitiesConfig with a single
// "CloudController" operator referencing the given role definition resource ID.
func testIdentitiesConfig(roleDefResourceID *azcorearm.ResourceID) *azure.ClusterScopedIdentitiesConfig {
	return &azure.ClusterScopedIdentitiesConfig{
		ControlPlaneOperatorsIdentities: azure.ControlPlaneOperatorsIdentities{
			"CloudController": &azure.ControlPlaneOperatorIdentity{
				BaseClusterScopedOperatorIdentity: azure.BaseClusterScopedOperatorIdentity{
					BaseClusterScopedIdentity: azure.BaseClusterScopedIdentity{
						RoleDefinitions: []*azure.ClusterScopedIdentityRoleDefinition{
							{ResourceID: roleDefResourceID},
						},
					},
				},
			},
		},
	}
}

// testAllNetworkActions returns the full set of network role actions used across tests.
func testAllNetworkActions() []string {
	return []string{
		"Microsoft.Network/networkSecurityGroups/read",
		"Microsoft.Network/networkSecurityGroups/write",
		"Microsoft.Network/networkSecurityGroups/join/action",
		"Microsoft.Network/virtualNetworks/join/action",
		"Microsoft.Network/virtualNetworks/read",
		"Microsoft.Network/virtualNetworks/write",
		"Microsoft.Network/virtualNetworks/subnets/join/action",
		"Microsoft.Network/virtualNetworks/subnets/read",
		"Microsoft.Network/virtualNetworks/subnets/write",
	}
}

// mockSimpleSubnetsClient returns a mock SubnetsClient that returns a subnet with
// no attached resources, or with the optional RouteTable set.
func mockSimpleSubnetsClient(ctrl *gomock.Controller, routeTable *armnetwork.RouteTable) *azureclient.MockSubnetsClient {
	mock := azureclient.NewMockSubnetsClient(ctrl)
	mock.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(
		armnetwork.SubnetsClientGetResponse{
			Subnet: armnetwork.Subnet{
				Properties: &armnetwork.SubnetPropertiesFormat{
					RouteTable: routeTable,
				},
			},
		}, nil).AnyTimes()
	return mock
}

// testCheckAccessV2Scope is the CheckAccessV2 scope used in all Validate tests.
const testCheckAccessV2Scope = "https://management.azure.com/.default"

// --- Tests for Validate ---

func TestValidate(t *testing.T) {
	roleDefResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/11111111-1111-1111-1111-111111111111/providers/Microsoft.Authorization/roleDefinitions/test-role-def"))

	tests := []struct {
		name string

		// emptyIdentitiesConfig uses an empty ClusterScopedIdentitiesConfig (no operators). When false (the default), testIdentitiesConfig(roleDefResourceID) is used.
		emptyIdentitiesConfig bool

		// role definition fields (only relevant when emptyIdentitiesConfig is false)
		roleDefActions     []string
		roleDefDataActions []string

		// failure injection
		checkAccessBuildErr error
		subnetClientErr     error
		tokenRetrieveErr    error

		// CheckAccess decisions returned for every call (nil means CheckAccess is never reached)
		checkAccessDecisions []checkaccessv2.AuthorizationDecision

		// optional route table attached to the subnet
		routeTable *armnetwork.RouteTable

		// if > 0, assert the exact number of CheckAccess calls made
		wantCheckAccessCalls int

		wantErr         bool
		wantErrContains []string
	}{
		{
			name:           "all permissions allowed",
			roleDefActions: testAllNetworkActions(),
			checkAccessDecisions: []checkaccessv2.AuthorizationDecision{
				{ActionId: "Microsoft.Network/networkSecurityGroups/read", AccessDecision: checkaccessv2.Allowed},
				{ActionId: "Microsoft.Network/networkSecurityGroups/write", AccessDecision: checkaccessv2.Allowed},
				{ActionId: "Microsoft.Network/networkSecurityGroups/join/action", AccessDecision: checkaccessv2.Allowed},
			},
		},
		{
			name:           "missing permissions",
			roleDefActions: testAllNetworkActions(),
			checkAccessDecisions: []checkaccessv2.AuthorizationDecision{
				{ActionId: "Microsoft.Network/networkSecurityGroups/read", AccessDecision: checkaccessv2.Allowed},
				{ActionId: "Microsoft.Network/networkSecurityGroups/write", AccessDecision: checkaccessv2.NotAllowed},
				{ActionId: "Microsoft.Network/networkSecurityGroups/join/action", AccessDecision: checkaccessv2.Denied},
			},
			wantErr:         true,
			wantErrContains: []string{"control plane operators missing required permissions", "not allowed", "denied"},
		},
		{
			name:             "get token error",
			roleDefActions:   []string{"Microsoft.Network/networkSecurityGroups/read"},
			tokenRetrieveErr: errors.New("failed to acquire token from MI dataplane"),
			wantErr:          true,
			wantErrContains:  []string{"failed to acquire token from MI dataplane"},
		},
		{
			name:                  "subnet client error",
			emptyIdentitiesConfig: true,
			subnetClientErr:       errors.New("subnet client creation failed"),
			wantErr:               true,
			wantErrContains:       []string{"failed to get subnets client"},
		},
		{
			name:                  "check access client build error",
			emptyIdentitiesConfig: true,
			checkAccessBuildErr:   errors.New("check access client build failed"),
			wantErr:               true,
			wantErrContains:       []string{"failed to build check access client"},
		},
		{
			name:           "route table permissions checked",
			roleDefActions: append(testAllNetworkActions(), "Microsoft.Network/routeTables/join/action"),
			checkAccessDecisions: []checkaccessv2.AuthorizationDecision{
				{ActionId: "Microsoft.Network/routeTables/join/action", AccessDecision: checkaccessv2.Allowed},
			},
			routeTable:           &armnetwork.RouteTable{ID: ptr.To("/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/testResourceGroup/providers/Microsoft.Network/routeTables/testRouteTable")},
			wantCheckAccessCalls: 3, // NSG + VNet + RouteTable
		},
		{
			name:           "data actions extracted from role definitions",
			roleDefActions: testAllNetworkActions(),
			roleDefDataActions: []string{
				"Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read",
				"Microsoft.Storage/storageAccounts/blobServices/containers/blobs/write",
			},
			checkAccessDecisions: []checkaccessv2.AuthorizationDecision{
				{ActionId: "Microsoft.Network/networkSecurityGroups/read", AccessDecision: checkaccessv2.Allowed},
				{ActionId: "Microsoft.Network/networkSecurityGroups/write", AccessDecision: checkaccessv2.Allowed},
				{ActionId: "Microsoft.Network/networkSecurityGroups/join/action", AccessDecision: checkaccessv2.Allowed},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
			ctrl := gomock.NewController(t)

			// --- CheckAccess builder and client ---
			mockCheckAccessClient := azureclient.NewMockCheckAccessV2Client(ctrl)
			mockCheckAccessBuilder := azureclient.NewMockCheckAccessV2ClientBuilder(ctrl)
			if tc.checkAccessBuildErr != nil {
				mockCheckAccessBuilder.EXPECT().Build(gomock.Any()).Return(nil, tc.checkAccessBuildErr)
			} else {
				mockCheckAccessBuilder.EXPECT().Build(gomock.Any()).Return(mockCheckAccessClient, nil)
			}

			// Set up CheckAccess call expectations when it can be reached.
			checkAccessCallCount := 0
			if tc.checkAccessBuildErr == nil && tc.subnetClientErr == nil && tc.tokenRetrieveErr == nil {
				mockCheckAccessClient.EXPECT().CreateAuthorizationRequest(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&checkaccessv2.AuthorizationRequest{}, nil).AnyTimes()
				mockCheckAccessClient.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					DoAndReturn(func(_ context.Context, _ checkaccessv2.AuthorizationRequest) (*checkaccessv2.AuthorizationDecisionResponse, error) {
						checkAccessCallCount++
						return &checkaccessv2.AuthorizationDecisionResponse{Value: tc.checkAccessDecisions}, nil
					}).AnyTimes()
			}

			// --- SMI / subnets builder ---
			mockSMIBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
			if tc.checkAccessBuildErr == nil {
				if tc.subnetClientErr != nil {
					mockSMIBuilder.EXPECT().SubnetsClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
						Return(nil, tc.subnetClientErr)
				} else {
					mockSMIBuilder.EXPECT().SubnetsClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
						Return(mockSimpleSubnetsClient(ctrl, tc.routeTable), nil)
				}
			}

			// --- Identities config, role definition reader, and token retriever ---
			var identitiesConfig *azure.ClusterScopedIdentitiesConfig
			var readers *cachedreader.BackendIdentityAzureCachedReaders
			var tokenRetrieverBuilder azureclient.MIDataplaneBasedIdentityAccessTokenRetrieverBuilder

			if tc.emptyIdentitiesConfig {
				identitiesConfig = &azure.ClusterScopedIdentitiesConfig{}
				readers = &cachedreader.BackendIdentityAzureCachedReaders{}
				tokenRetrieverBuilder = mockSuccessfulTokenRetrieverBuilder(ctrl)
			} else {
				identitiesConfig = testIdentitiesConfig(roleDefResourceID)

				mockRoleDefReader := cachedreader.NewMockRoleDefinitionsCachedReader(ctrl)
				if tc.checkAccessBuildErr == nil && tc.subnetClientErr == nil {
					mockRoleDefReader.EXPECT().GetCachedByID(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(newRoleDefinitionResponseWithDataActions(tc.roleDefActions, tc.roleDefDataActions), nil).AnyTimes()
				}
				readers = &cachedreader.BackendIdentityAzureCachedReaders{RoleDefinitionsCachedReader: mockRoleDefReader}

				if tc.tokenRetrieveErr != nil {
					failingRetriever := azureclient.NewMockMIDataplaneBasedIdentityAccessTokenRetriever(ctrl)
					failingRetriever.EXPECT().GetToken(gomock.Any(), gomock.Any()).Return(azcore.AccessToken{}, tc.tokenRetrieveErr)
					mockTokenRetrieverBuilder := azureclient.NewMockMIDataplaneBasedIdentityAccessTokenRetrieverBuilder(ctrl)
					mockTokenRetrieverBuilder.EXPECT().Build(gomock.Any(), gomock.Any()).Return(failingRetriever, nil)
					tokenRetrieverBuilder = mockTokenRetrieverBuilder
				} else {
					tokenRetrieverBuilder = mockSuccessfulTokenRetrieverBuilder(ctrl)
				}
			}

			validation := NewControlPlaneIdentitiesPermissionsValidation(
				mockSMIBuilder,
				identitiesConfig,
				readers,
				mockCheckAccessBuilder,
				tokenRetrieverBuilder,
				testCheckAccessV2Scope,
			)

			err := validation.Validate(ctx, newTestSubscription(), newTestCluster())

			if tc.wantErr {
				require.Error(t, err)
				for _, s := range tc.wantErrContains {
					assert.Contains(t, err.Error(), s)
				}
			} else {
				assert.NoError(t, err)
			}
			if tc.wantCheckAccessCalls > 0 {
				assert.Equal(t, tc.wantCheckAccessCalls, checkAccessCallCount)
			}
		})
	}
}

// --- Tests for checkNotAllowedAndDeniedActionsForNetworkSecurityGroup ---

func TestCheckNotAllowedAndDeniedActionsForNSG(t *testing.T) {
	nsgResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/testRG/providers/Microsoft.Network/networkSecurityGroups/testNSG"))

	tests := []struct {
		name            string
		roleActions     []string
		decisions       []checkaccessv2.AuthorizationDecision
		checkAccessErr  error
		noCheckAccess   bool // when true, CheckAccess must NOT be called
		wantLen         int
		wantDecisions   []checkaccessv2.AccessDecision
		wantErr         bool
		wantErrContains string
	}{
		{
			name:        "all allowed",
			roleActions: testAllNetworkActions(),
			decisions: []checkaccessv2.AuthorizationDecision{
				{ActionId: "Microsoft.Network/networkSecurityGroups/read", AccessDecision: checkaccessv2.Allowed},
				{ActionId: "Microsoft.Network/networkSecurityGroups/write", AccessDecision: checkaccessv2.Allowed},
				{ActionId: "Microsoft.Network/networkSecurityGroups/join/action", AccessDecision: checkaccessv2.Allowed},
			},
			wantLen: 0,
		},
		{
			name:        "some denied",
			roleActions: testAllNetworkActions(),
			decisions: []checkaccessv2.AuthorizationDecision{
				{ActionId: "Microsoft.Network/networkSecurityGroups/read", AccessDecision: checkaccessv2.Allowed},
				{ActionId: "Microsoft.Network/networkSecurityGroups/write", AccessDecision: checkaccessv2.NotAllowed},
				{ActionId: "Microsoft.Network/networkSecurityGroups/join/action", AccessDecision: checkaccessv2.Denied},
			},
			wantLen:       2,
			wantDecisions: []checkaccessv2.AccessDecision{checkaccessv2.NotAllowed, checkaccessv2.Denied},
		},
		{
			// Only route-table actions are passed; none overlap with NSG actions so CheckAccess is skipped.
			name:          "no matching role actions",
			roleActions:   []string{"Microsoft.Network/routeTables/join/action"},
			noCheckAccess: true,
			wantLen:       0,
		},
		{
			name:            "check access error",
			roleActions:     testAllNetworkActions(),
			checkAccessErr:  errors.New("check access API unavailable"),
			wantErr:         true,
			wantErrContains: "check access API unavailable",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
			ctrl := gomock.NewController(t)

			checkAccessV2Client := azureclient.NewMockCheckAccessV2Client(ctrl)
			if !tc.noCheckAccess {
				checkAccessV2Client.EXPECT().CreateAuthorizationRequest(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&checkaccessv2.AuthorizationRequest{}, nil)
				if tc.checkAccessErr != nil {
					checkAccessV2Client.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).Return(nil, tc.checkAccessErr)
				} else {
					checkAccessV2Client.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
						Return(&checkaccessv2.AuthorizationDecisionResponse{Value: tc.decisions}, nil)
				}
			}

			v := &ControlPlaneIdentitiesPermissionsValidation{}
			result, err := v.checkNotAllowedAndDeniedActionsForNetworkSecurityGroup(ctx, checkAccessV2Client, nsgResourceID, tc.roleActions, nil, fakeToken())

			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErrContains)
				return
			}
			require.NoError(t, err)
			require.Len(t, result, tc.wantLen)
			for i, d := range tc.wantDecisions {
				assert.Equal(t, d, result[i].AccessDecision)
			}
		})
	}
}

// --- Tests for checkNotAllowedAndDeniedActionsForVNet ---

func TestCheckNotAllowedAndDeniedActionsForVNet(t *testing.T) {
	vnetResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/testRG/providers/Microsoft.Network/virtualNetworks/testVNet"))

	tests := []struct {
		name            string
		roleActions     []string
		decisions       []checkaccessv2.AuthorizationDecision
		checkAccessErr  error
		noCheckAccess   bool
		wantLen         int
		wantDecisions   []checkaccessv2.AccessDecision
		wantErr         bool
		wantErrContains string
	}{
		{
			name:        "all allowed",
			roleActions: testAllNetworkActions(),
			decisions: []checkaccessv2.AuthorizationDecision{
				{ActionId: "Microsoft.Network/virtualNetworks/join/action", AccessDecision: checkaccessv2.Allowed},
				{ActionId: "Microsoft.Network/virtualNetworks/read", AccessDecision: checkaccessv2.Allowed},
				{ActionId: "Microsoft.Network/virtualNetworks/subnets/join/action", AccessDecision: checkaccessv2.Allowed},
			},
			wantLen: 0,
		},
		{
			name:        "some denied",
			roleActions: testAllNetworkActions(),
			decisions: []checkaccessv2.AuthorizationDecision{
				{ActionId: "Microsoft.Network/virtualNetworks/join/action", AccessDecision: checkaccessv2.Denied},
				{ActionId: "Microsoft.Network/virtualNetworks/read", AccessDecision: checkaccessv2.NotAllowed},
			},
			wantLen:       2,
			wantDecisions: []checkaccessv2.AccessDecision{checkaccessv2.Denied, checkaccessv2.NotAllowed},
		},
		{
			// Only NSG actions are passed; none overlap with VNet actions so CheckAccess is skipped.
			name:          "no matching role actions",
			roleActions:   []string{"Microsoft.Network/networkSecurityGroups/read"},
			noCheckAccess: true,
			wantLen:       0,
		},
		{
			name:            "check access error",
			roleActions:     testAllNetworkActions(),
			checkAccessErr:  errors.New("check access API unavailable"),
			wantErr:         true,
			wantErrContains: "check access API unavailable",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
			ctrl := gomock.NewController(t)

			checkAccessV2Client := azureclient.NewMockCheckAccessV2Client(ctrl)
			if !tc.noCheckAccess {
				checkAccessV2Client.EXPECT().CreateAuthorizationRequest(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&checkaccessv2.AuthorizationRequest{}, nil)
				if tc.checkAccessErr != nil {
					checkAccessV2Client.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).Return(nil, tc.checkAccessErr)
				} else {
					checkAccessV2Client.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
						Return(&checkaccessv2.AuthorizationDecisionResponse{Value: tc.decisions}, nil)
				}
			}

			v := &ControlPlaneIdentitiesPermissionsValidation{}
			result, err := v.checkNotAllowedAndDeniedActionsForVNet(ctx, checkAccessV2Client, vnetResourceID, tc.roleActions, nil, fakeToken())

			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErrContains)
				return
			}
			require.NoError(t, err)
			require.Len(t, result, tc.wantLen)
			for i, d := range tc.wantDecisions {
				assert.Equal(t, d, result[i].AccessDecision)
			}
		})
	}
}

// --- Tests for checkNotAllowedAndDeniedActionsForRouteTable ---

func TestCheckNotAllowedAndDeniedActionsForRouteTable(t *testing.T) {
	validRouteTable := &armnetwork.RouteTable{ID: ptr.To("/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/testRG/providers/Microsoft.Network/routeTables/testRT")}

	tests := []struct {
		name            string
		routeTable      *armnetwork.RouteTable
		roleActions     []string
		decisions       []checkaccessv2.AuthorizationDecision
		checkAccessErr  error
		noCheckAccess   bool
		wantLen         int
		wantDecisions   []checkaccessv2.AccessDecision
		wantErr         bool
		wantErrContains string
	}{
		{
			name:        "all allowed",
			routeTable:  validRouteTable,
			roleActions: []string{"Microsoft.Network/routeTables/join/action"},
			decisions: []checkaccessv2.AuthorizationDecision{
				{ActionId: "Microsoft.Network/routeTables/join/action", AccessDecision: checkaccessv2.Allowed},
			},
			wantLen: 0,
		},
		{
			name:        "denied",
			routeTable:  validRouteTable,
			roleActions: []string{"Microsoft.Network/routeTables/join/action"},
			decisions: []checkaccessv2.AuthorizationDecision{
				{ActionId: "Microsoft.Network/routeTables/join/action", AccessDecision: checkaccessv2.Denied},
			},
			wantLen:       1,
			wantDecisions: []checkaccessv2.AccessDecision{checkaccessv2.Denied},
		},
		{
			// Only NSG actions are passed; none overlap with route table actions so CheckAccess is skipped.
			name:          "no matching role actions",
			routeTable:    validRouteTable,
			roleActions:   []string{"Microsoft.Network/networkSecurityGroups/read"},
			noCheckAccess: true,
			wantLen:       0,
		},
		{
			name:            "invalid resource ID",
			routeTable:      &armnetwork.RouteTable{ID: ptr.To("not-a-valid-resource-id")},
			roleActions:     []string{"Microsoft.Network/routeTables/join/action"},
			noCheckAccess:   true,
			wantErr:         true,
			wantErrContains: "failed to parse route table resource ID",
		},
		{
			name:            "check access error",
			routeTable:      validRouteTable,
			roleActions:     []string{"Microsoft.Network/routeTables/join/action"},
			checkAccessErr:  errors.New("check access API unavailable"),
			wantErr:         true,
			wantErrContains: "check access API unavailable",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
			ctrl := gomock.NewController(t)

			checkAccessV2Client := azureclient.NewMockCheckAccessV2Client(ctrl)
			if !tc.noCheckAccess {
				checkAccessV2Client.EXPECT().CreateAuthorizationRequest(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&checkaccessv2.AuthorizationRequest{}, nil)
				if tc.checkAccessErr != nil {
					checkAccessV2Client.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).Return(nil, tc.checkAccessErr)
				} else {
					checkAccessV2Client.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
						Return(&checkaccessv2.AuthorizationDecisionResponse{Value: tc.decisions}, nil)
				}
			}

			v := &ControlPlaneIdentitiesPermissionsValidation{}
			result, err := v.checkNotAllowedAndDeniedActionsForRouteTable(ctx, checkAccessV2Client, tc.routeTable, tc.roleActions, nil, fakeToken())

			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErrContains)
				return
			}
			require.NoError(t, err)
			require.Len(t, result, tc.wantLen)
			for i, d := range tc.wantDecisions {
				assert.Equal(t, d, result[i].AccessDecision)
			}
		})
	}
}

// --- Tests for checkNotAllowedAndDeniedActionsForResourceID ---

func TestCheckNotAllowedAndDeniedActionsForResourceID(t *testing.T) {
	type wantAction struct {
		id           string
		isDataAction bool
	}
	type wantDecision struct {
		actionId string
		decision checkaccessv2.AccessDecision
	}

	resourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/testRG/providers/Microsoft.Network/networkSecurityGroups/testNSG"))

	tests := []struct {
		name               string
		actions            []string
		dataActions        []string
		decisions          []checkaccessv2.AuthorizationDecision
		noCheckAccess      bool
		wantRequestActions []wantAction
		wantResult         []wantDecision
	}{
		{
			name:        "data actions included alongside regular actions",
			actions:     []string{"Microsoft.Network/networkSecurityGroups/read"},
			dataActions: []string{"Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read"},
			decisions: []checkaccessv2.AuthorizationDecision{
				{ActionId: "Microsoft.Network/networkSecurityGroups/read", AccessDecision: checkaccessv2.Allowed},
				{ActionId: "Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read", AccessDecision: checkaccessv2.NotAllowed, IsDataAction: true},
			},
			wantRequestActions: []wantAction{
				{id: "Microsoft.Network/networkSecurityGroups/read", isDataAction: false},
				{id: "Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read", isDataAction: true},
			},
			wantResult: []wantDecision{
				{actionId: "Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read", decision: checkaccessv2.NotAllowed},
			},
		},
		{
			name:        "only data actions",
			dataActions: []string{"Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read"},
			decisions: []checkaccessv2.AuthorizationDecision{
				{ActionId: "Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read", AccessDecision: checkaccessv2.Allowed, IsDataAction: true},
			},
			wantRequestActions: []wantAction{
				{id: "Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read", isDataAction: true},
			},
			wantResult: nil,
		},
		{
			// Both slices empty: returns nil early without any API calls.
			name:          "no actions and no data actions",
			noCheckAccess: true,
			wantResult:    nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
			ctrl := gomock.NewController(t)

			var capturedRequest checkaccessv2.AuthorizationRequest
			checkAccessV2Client := azureclient.NewMockCheckAccessV2Client(ctrl)
			if !tc.noCheckAccess {
				// Use DoAndReturn so regular actions are reflected in the captured request,
				// mirroring how the real client populates ActionInfo entries from the actions slice.
				checkAccessV2Client.EXPECT().CreateAuthorizationRequest(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(_ string, actions []string, _ string) (*checkaccessv2.AuthorizationRequest, error) {
						req := &checkaccessv2.AuthorizationRequest{}
						for _, a := range actions {
							req.Actions = append(req.Actions, checkaccessv2.ActionInfo{Id: a})
						}
						return req, nil
					})
				checkAccessV2Client.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					DoAndReturn(func(_ context.Context, req checkaccessv2.AuthorizationRequest) (*checkaccessv2.AuthorizationDecisionResponse, error) {
						capturedRequest = req
						return &checkaccessv2.AuthorizationDecisionResponse{Value: tc.decisions}, nil
					})
			}

			v := &ControlPlaneIdentitiesPermissionsValidation{}
			result, err := v.checkNotAllowedAndDeniedActionsForResourceID(ctx, checkAccessV2Client, resourceID, tc.actions, tc.dataActions, fakeToken())
			require.NoError(t, err)

			if len(tc.wantRequestActions) > 0 {
				require.Len(t, capturedRequest.Actions, len(tc.wantRequestActions))
				for i, wa := range tc.wantRequestActions {
					assert.Equal(t, wa.isDataAction, capturedRequest.Actions[i].IsDataAction, "action[%d].IsDataAction", i)
					assert.Equal(t, wa.id, capturedRequest.Actions[i].Id, "action[%d].Id", i)
				}
			}

			require.Len(t, result, len(tc.wantResult))
			for i, wr := range tc.wantResult {
				assert.Equal(t, wr.decision, result[i].AccessDecision)
				assert.Equal(t, wr.actionId, result[i].ActionId)
			}
		})
	}
}

// --- Tests for formatMissingPermissionsMessage ---

func TestFormatMissingPermissionsMessage(t *testing.T) {
	identity := api.Must(azcorearm.ParseResourceID("/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/cloud-controller"))
	resource := api.Must(azcorearm.ParseResourceID("/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/rg/providers/Microsoft.Network/networkSecurityGroups/nsg"))

	for _, tc := range []struct {
		name     string
		input    []*IdentityResourceMissingPermissions
		expected string
	}{
		{
			name:     "empty input",
			input:    nil,
			expected: "",
		},
		{
			name: "not allowed regular actions only",
			input: []*IdentityResourceMissingPermissions{
				{
					Identity: identity,
					Resource: resource,
					Decisions: &[]checkaccessv2.AuthorizationDecision{
						{ActionId: "Microsoft.Network/networkSecurityGroups/write", AccessDecision: checkaccessv2.NotAllowed, IsDataAction: false},
					},
				},
			},
			expected: "identity '" + identity.String() + "' on resource '" + resource.String() + "': not allowed actions: [Microsoft.Network/networkSecurityGroups/write]",
		},
		{
			name: "not allowed dataActions only",
			input: []*IdentityResourceMissingPermissions{
				{
					Identity: identity,
					Resource: resource,
					Decisions: &[]checkaccessv2.AuthorizationDecision{
						{ActionId: "Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read", AccessDecision: checkaccessv2.NotAllowed, IsDataAction: true},
					},
				},
			},
			expected: "identity '" + identity.String() + "' on resource '" + resource.String() + "': not allowed dataActions: [Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read]",
		},
		{
			name: "denied regular actions only",
			input: []*IdentityResourceMissingPermissions{
				{
					Identity: identity,
					Resource: resource,
					Decisions: &[]checkaccessv2.AuthorizationDecision{
						{ActionId: "Microsoft.Network/networkSecurityGroups/join/action", AccessDecision: checkaccessv2.Denied, IsDataAction: false},
					},
				},
			},
			expected: "identity '" + identity.String() + "' on resource '" + resource.String() + "': denied actions: [Microsoft.Network/networkSecurityGroups/join/action]",
		},
		{
			name: "denied dataActions only",
			input: []*IdentityResourceMissingPermissions{
				{
					Identity: identity,
					Resource: resource,
					Decisions: &[]checkaccessv2.AuthorizationDecision{
						{ActionId: "Microsoft.Storage/storageAccounts/blobServices/containers/blobs/write", AccessDecision: checkaccessv2.Denied, IsDataAction: true},
					},
				},
			},
			expected: "identity '" + identity.String() + "' on resource '" + resource.String() + "': denied dataActions: [Microsoft.Storage/storageAccounts/blobServices/containers/blobs/write]",
		},
		{
			name: "all four buckets present",
			input: []*IdentityResourceMissingPermissions{
				{
					Identity: identity,
					Resource: resource,
					Decisions: &[]checkaccessv2.AuthorizationDecision{
						{ActionId: "Microsoft.Network/networkSecurityGroups/write", AccessDecision: checkaccessv2.NotAllowed, IsDataAction: false},
						{ActionId: "Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read", AccessDecision: checkaccessv2.NotAllowed, IsDataAction: true},
						{ActionId: "Microsoft.Network/networkSecurityGroups/join/action", AccessDecision: checkaccessv2.Denied, IsDataAction: false},
						{ActionId: "Microsoft.Storage/storageAccounts/blobServices/containers/blobs/write", AccessDecision: checkaccessv2.Denied, IsDataAction: true},
					},
				},
			},
			expected: "identity '" + identity.String() + "' on resource '" + resource.String() + "':" +
				" not allowed actions: [Microsoft.Network/networkSecurityGroups/write]" +
				" not allowed dataActions: [Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read]" +
				" denied actions: [Microsoft.Network/networkSecurityGroups/join/action]" +
				" denied dataActions: [Microsoft.Storage/storageAccounts/blobServices/containers/blobs/write]",
		},
		{
			name: "multiple results joined by semicolon",
			input: []*IdentityResourceMissingPermissions{
				{
					Identity: identity,
					Resource: resource,
					Decisions: &[]checkaccessv2.AuthorizationDecision{
						{ActionId: "Microsoft.Network/networkSecurityGroups/write", AccessDecision: checkaccessv2.NotAllowed, IsDataAction: false},
					},
				},
				{
					Identity: identity,
					Resource: resource,
					Decisions: &[]checkaccessv2.AuthorizationDecision{
						{ActionId: "Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read", AccessDecision: checkaccessv2.Denied, IsDataAction: true},
					},
				},
			},
			expected: "identity '" + identity.String() + "' on resource '" + resource.String() + "': not allowed actions: [Microsoft.Network/networkSecurityGroups/write]" +
				"; identity '" + identity.String() + "' on resource '" + resource.String() + "': denied dataActions: [Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read]",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, formatMissingPermissionsMessage(tc.input))
		})
	}
}
