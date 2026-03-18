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
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
	azurecheckaccessv2client "github.com/Azure/checkaccess-v2-go-sdk/client"

	"github.com/Azure/ARO-HCP/backend/pkg/azure/cachedreader"
	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/azure"
)

func TestCollectNotAllowedAndDeniedActions(t *testing.T) {
	v := &ControlPlaneIdentitiesPermissionsClusterValidation{}

	tests := []struct {
		name     string
		input    []azurecheckaccessv2client.AuthorizationDecision
		expected []*checkaccessv2AuthorizationDecisionData
	}{
		{
			name:     "empty input returns nil",
			input:    []azurecheckaccessv2client.AuthorizationDecision{},
			expected: nil,
		},
		{
			name: "all allowed returns nil",
			input: []azurecheckaccessv2client.AuthorizationDecision{
				{ActionId: "Microsoft.Network/networkSecurityGroups/read", AccessDecision: azurecheckaccessv2client.Allowed},
				{ActionId: "Microsoft.Network/networkSecurityGroups/write", AccessDecision: azurecheckaccessv2client.Allowed},
			},
			expected: nil,
		},
		{
			name: "mix of allowed, not allowed, and denied returns only non-allowed",
			input: []azurecheckaccessv2client.AuthorizationDecision{
				{ActionId: "Microsoft.Network/networkSecurityGroups/read", AccessDecision: azurecheckaccessv2client.Allowed},
				{ActionId: "Microsoft.Network/networkSecurityGroups/write", AccessDecision: azurecheckaccessv2client.NotAllowed},
				{ActionId: "Microsoft.Network/networkSecurityGroups/join/action", AccessDecision: azurecheckaccessv2client.Denied},
			},
			expected: []*checkaccessv2AuthorizationDecisionData{
				{ActionID: "Microsoft.Network/networkSecurityGroups/write", IsDataAction: false, AccessDecision: azurecheckaccessv2client.NotAllowed},
				{ActionID: "Microsoft.Network/networkSecurityGroups/join/action", IsDataAction: false, AccessDecision: azurecheckaccessv2client.Denied},
			},
		},
		{
			name: "all not allowed or denied returns all",
			input: []azurecheckaccessv2client.AuthorizationDecision{
				{ActionId: "Microsoft.Network/networkSecurityGroups/read", AccessDecision: azurecheckaccessv2client.NotAllowed},
				{ActionId: "Microsoft.Network/networkSecurityGroups/write", AccessDecision: azurecheckaccessv2client.Denied},
			},
			expected: []*checkaccessv2AuthorizationDecisionData{
				{ActionID: "Microsoft.Network/networkSecurityGroups/read", IsDataAction: false, AccessDecision: azurecheckaccessv2client.NotAllowed},
				{ActionID: "Microsoft.Network/networkSecurityGroups/write", IsDataAction: false, AccessDecision: azurecheckaccessv2client.Denied},
			},
		},
		{
			name: "data actions are correctly propagated",
			input: []azurecheckaccessv2client.AuthorizationDecision{
				{ActionId: "Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read", AccessDecision: azurecheckaccessv2client.NotAllowed, IsDataAction: true},
				{ActionId: "Microsoft.Network/networkSecurityGroups/read", AccessDecision: azurecheckaccessv2client.Allowed, IsDataAction: false},
			},
			expected: []*checkaccessv2AuthorizationDecisionData{
				{ActionID: "Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read", IsDataAction: true, AccessDecision: azurecheckaccessv2client.NotAllowed},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := v.collectNotAllowedAndDeniedActions(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCheckNotAllowedAndDeniedActionsForResourceID(t *testing.T) {
	testResourceID, err := azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.Network/networkSecurityGroups/test-nsg")
	require.NoError(t, err)

	fakeToken := azcore.AccessToken{Token: "fake-jwt-token"}

	tests := []struct {
		name        string
		actions     []string
		dataActions []string
		setupMock   func(*azureclient.MockCheckAccessV2Client)
		wantResult  []*checkaccessv2AuthorizationDecisionData
		wantErr     bool
	}{
		{
			name:        "empty actions and data actions returns nil without API call",
			actions:     nil,
			dataActions: nil,
			wantResult:  nil,
			wantErr:     false,
		},
		{
			name:        "all actions allowed returns nil",
			actions:     []string{"Microsoft.Network/networkSecurityGroups/read"},
			dataActions: nil,
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
				m.EXPECT().CreateAuthorizationRequest(testResourceID.String(), []string{"Microsoft.Network/networkSecurityGroups/read"}, fakeToken.Token).
					Return(&azurecheckaccessv2client.AuthorizationRequest{}, nil)
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationDecisionResponse{
						Value: []azurecheckaccessv2client.AuthorizationDecision{
							{ActionId: "Microsoft.Network/networkSecurityGroups/read", AccessDecision: azurecheckaccessv2client.Allowed},
						},
					}, nil)
			},
			wantResult: nil,
			wantErr:    false,
		},
		{
			name:        "some actions denied returns denied decisions",
			actions:     []string{"Microsoft.Network/networkSecurityGroups/read", "Microsoft.Network/networkSecurityGroups/write"},
			dataActions: nil,
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
				m.EXPECT().CreateAuthorizationRequest(testResourceID.String(), []string{"Microsoft.Network/networkSecurityGroups/read", "Microsoft.Network/networkSecurityGroups/write"}, fakeToken.Token).
					Return(&azurecheckaccessv2client.AuthorizationRequest{}, nil)
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationDecisionResponse{
						Value: []azurecheckaccessv2client.AuthorizationDecision{
							{ActionId: "Microsoft.Network/networkSecurityGroups/read", AccessDecision: azurecheckaccessv2client.Allowed},
							{ActionId: "Microsoft.Network/networkSecurityGroups/write", AccessDecision: azurecheckaccessv2client.NotAllowed},
						},
					}, nil)
			},
			wantResult: []*checkaccessv2AuthorizationDecisionData{
				{ActionID: "Microsoft.Network/networkSecurityGroups/write", IsDataAction: false, AccessDecision: azurecheckaccessv2client.NotAllowed},
			},
			wantErr: false,
		},
		{
			name:        "CreateAuthorizationRequest error returns error",
			actions:     []string{"Microsoft.Network/networkSecurityGroups/read"},
			dataActions: nil,
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
				m.EXPECT().CreateAuthorizationRequest(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(nil, fmt.Errorf("request creation failed"))
			},
			wantResult: nil,
			wantErr:    true,
		},
		{
			name:        "CheckAccess error returns error",
			actions:     []string{"Microsoft.Network/networkSecurityGroups/read"},
			dataActions: nil,
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
				m.EXPECT().CreateAuthorizationRequest(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationRequest{}, nil)
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(nil, fmt.Errorf("check access failed"))
			},
			wantResult: nil,
			wantErr:    true,
		},
		{
			name:        "nil AuthorizationDecisionResponse returns error",
			actions:     []string{"Microsoft.Network/networkSecurityGroups/read"},
			dataActions: nil,
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
				m.EXPECT().CreateAuthorizationRequest(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationRequest{}, nil)
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(nil, nil)
			},
			wantResult: nil,
			wantErr:    true,
		},
		{
			name:        "mismatch in expected vs returned action count returns error",
			actions:     []string{"Microsoft.Network/networkSecurityGroups/read", "Microsoft.Network/networkSecurityGroups/write"},
			dataActions: nil,
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
				m.EXPECT().CreateAuthorizationRequest(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationRequest{}, nil)
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationDecisionResponse{
						Value: []azurecheckaccessv2client.AuthorizationDecision{
							{ActionId: "Microsoft.Network/networkSecurityGroups/read", AccessDecision: azurecheckaccessv2client.Allowed},
						},
					}, nil)
			},
			wantResult: nil,
			wantErr:    true,
		},
		{
			name:        "data actions sent with IsDataAction true",
			actions:     []string{"Microsoft.Network/networkSecurityGroups/read"},
			dataActions: []string{"Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read"},
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
				m.EXPECT().CreateAuthorizationRequest(testResourceID.String(), []string{"Microsoft.Network/networkSecurityGroups/read"}, fakeToken.Token).
					Return(&azurecheckaccessv2client.AuthorizationRequest{
						Actions: []azurecheckaccessv2client.ActionInfo{
							{Id: "Microsoft.Network/networkSecurityGroups/read"},
						},
					}, nil)
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					DoAndReturn(func(_ context.Context, req azurecheckaccessv2client.AuthorizationRequest) (*azurecheckaccessv2client.AuthorizationDecisionResponse, error) {
						require.Len(t, req.Actions, 2)
						assert.False(t, req.Actions[0].IsDataAction)
						assert.True(t, req.Actions[1].IsDataAction)
						assert.Equal(t, "Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read", req.Actions[1].Id)
						return &azurecheckaccessv2client.AuthorizationDecisionResponse{
							Value: []azurecheckaccessv2client.AuthorizationDecision{
								{ActionId: "Microsoft.Network/networkSecurityGroups/read", AccessDecision: azurecheckaccessv2client.Allowed},
								{ActionId: "Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read", AccessDecision: azurecheckaccessv2client.Allowed, IsDataAction: true},
							},
						}, nil
					})
			},
			wantResult: nil,
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockClient := azureclient.NewMockCheckAccessV2Client(ctrl)
			if tt.setupMock != nil {
				tt.setupMock(mockClient)
			}

			v := &ControlPlaneIdentitiesPermissionsClusterValidation{}
			result, err := v.checkNotAllowedAndDeniedActionsForResourceID(context.Background(), mockClient, testResourceID, tt.actions, tt.dataActions, fakeToken)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.wantResult, result)
		})
	}
}

func TestCheckNotAllowedAndDeniedActionsForNetworkSecurityGroup(t *testing.T) {
	nsgResourceID, err := azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.Network/networkSecurityGroups/test-nsg")
	require.NoError(t, err)

	fakeToken := azcore.AccessToken{Token: "fake-jwt-token"}

	tests := []struct {
		name                      string
		roleDefinitionActions     []string
		roleDefinitionDataActions []string
		setupMock                 func(*azureclient.MockCheckAccessV2Client)
		wantResult                []*checkaccessv2AuthorizationDecisionData
		wantErr                   bool
	}{
		{
			name:                      "no overlap with NSG actions returns nil without API call",
			roleDefinitionActions:     []string{"Microsoft.Compute/virtualMachines/read"},
			roleDefinitionDataActions: nil,
			wantResult:                nil,
			wantErr:                   false,
		},
		{
			name:                      "overlap with NSG actions checks intersection and returns allowed",
			roleDefinitionActions:     []string{"Microsoft.Network/networkSecurityGroups/read", "Microsoft.Compute/virtualMachines/read"},
			roleDefinitionDataActions: nil,
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
				m.EXPECT().CreateAuthorizationRequest(nsgResourceID.String(), gomock.Any(), fakeToken.Token).
					Return(&azurecheckaccessv2client.AuthorizationRequest{}, nil)
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationDecisionResponse{
						Value: []azurecheckaccessv2client.AuthorizationDecision{
							{ActionId: "Microsoft.Network/networkSecurityGroups/read", AccessDecision: azurecheckaccessv2client.Allowed},
						},
					}, nil)
			},
			wantResult: nil,
			wantErr:    false,
		},
		{
			name:                      "CheckAccessV2 returns denied produces decisions",
			roleDefinitionActions:     []string{"Microsoft.Network/networkSecurityGroups/read", "Microsoft.Network/networkSecurityGroups/write"},
			roleDefinitionDataActions: nil,
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
				m.EXPECT().CreateAuthorizationRequest(nsgResourceID.String(), gomock.Any(), fakeToken.Token).
					Return(&azurecheckaccessv2client.AuthorizationRequest{}, nil)
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationDecisionResponse{
						Value: []azurecheckaccessv2client.AuthorizationDecision{
							{ActionId: "Microsoft.Network/networkSecurityGroups/read", AccessDecision: azurecheckaccessv2client.Allowed},
							{ActionId: "Microsoft.Network/networkSecurityGroups/write", AccessDecision: azurecheckaccessv2client.Denied},
						},
					}, nil)
			},
			wantResult: []*checkaccessv2AuthorizationDecisionData{
				{ActionID: "Microsoft.Network/networkSecurityGroups/write", IsDataAction: false, AccessDecision: azurecheckaccessv2client.Denied},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockClient := azureclient.NewMockCheckAccessV2Client(ctrl)
			if tt.setupMock != nil {
				tt.setupMock(mockClient)
			}

			v := &ControlPlaneIdentitiesPermissionsClusterValidation{}
			result, err := v.checkNotAllowedAndDeniedActionsForNetworkSecurityGroup(context.Background(), mockClient, nsgResourceID, tt.roleDefinitionActions, tt.roleDefinitionDataActions, fakeToken)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.wantResult, result)
		})
	}
}

func TestCheckNotAllowedAndDeniedActionsForVNet(t *testing.T) {
	vnetResourceID, err := azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet")
	require.NoError(t, err)

	fakeToken := azcore.AccessToken{Token: "fake-jwt-token"}

	tests := []struct {
		name                      string
		roleDefinitionActions     []string
		roleDefinitionDataActions []string
		setupMock                 func(*azureclient.MockCheckAccessV2Client)
		wantResult                []*checkaccessv2AuthorizationDecisionData
		wantErr                   bool
	}{
		{
			name:                      "no overlap with VNet actions returns nil without API call",
			roleDefinitionActions:     []string{"Microsoft.Compute/virtualMachines/read"},
			roleDefinitionDataActions: nil,
			wantResult:                nil,
			wantErr:                   false,
		},
		{
			name:                      "overlap with VNet actions checks intersection and returns allowed",
			roleDefinitionActions:     []string{"Microsoft.Network/virtualNetworks/read", "Microsoft.Network/virtualNetworks/subnets/read"},
			roleDefinitionDataActions: nil,
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
				m.EXPECT().CreateAuthorizationRequest(vnetResourceID.String(), gomock.Any(), fakeToken.Token).
					Return(&azurecheckaccessv2client.AuthorizationRequest{}, nil)
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationDecisionResponse{
						Value: []azurecheckaccessv2client.AuthorizationDecision{
							{ActionId: "Microsoft.Network/virtualNetworks/read", AccessDecision: azurecheckaccessv2client.Allowed},
							{ActionId: "Microsoft.Network/virtualNetworks/subnets/read", AccessDecision: azurecheckaccessv2client.Allowed},
						},
					}, nil)
			},
			wantResult: nil,
			wantErr:    false,
		},
		{
			name:                      "CheckAccessV2 returns not allowed produces decisions",
			roleDefinitionActions:     []string{"Microsoft.Network/virtualNetworks/join/action"},
			roleDefinitionDataActions: nil,
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
				m.EXPECT().CreateAuthorizationRequest(vnetResourceID.String(), gomock.Any(), fakeToken.Token).
					Return(&azurecheckaccessv2client.AuthorizationRequest{}, nil)
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationDecisionResponse{
						Value: []azurecheckaccessv2client.AuthorizationDecision{
							{ActionId: "Microsoft.Network/virtualNetworks/join/action", AccessDecision: azurecheckaccessv2client.NotAllowed},
						},
					}, nil)
			},
			wantResult: []*checkaccessv2AuthorizationDecisionData{
				{ActionID: "Microsoft.Network/virtualNetworks/join/action", IsDataAction: false, AccessDecision: azurecheckaccessv2client.NotAllowed},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockClient := azureclient.NewMockCheckAccessV2Client(ctrl)
			if tt.setupMock != nil {
				tt.setupMock(mockClient)
			}

			v := &ControlPlaneIdentitiesPermissionsClusterValidation{}
			result, err := v.checkNotAllowedAndDeniedActionsForVNet(context.Background(), mockClient, vnetResourceID, tt.roleDefinitionActions, tt.roleDefinitionDataActions, fakeToken)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.wantResult, result)
		})
	}
}

func TestCheckNotAllowedAndDeniedActionsForRouteTable(t *testing.T) {
	routeTableID := "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.Network/routeTables/test-rt"
	routeTableResourceID, err := azcorearm.ParseResourceID(routeTableID)
	require.NoError(t, err)

	fakeToken := azcore.AccessToken{Token: "fake-jwt-token"}

	tests := []struct {
		name                      string
		routeTable                *armnetwork.RouteTable
		roleDefinitionActions     []string
		roleDefinitionDataActions []string
		setupMock                 func(*azureclient.MockCheckAccessV2Client)
		wantResult                []*checkaccessv2AuthorizationDecisionData
		wantErr                   bool
	}{
		{
			name:                      "no overlap with route table actions returns nil without API call",
			routeTable:                &armnetwork.RouteTable{ID: ptr.To(routeTableID)},
			roleDefinitionActions:     []string{"Microsoft.Compute/virtualMachines/read"},
			roleDefinitionDataActions: nil,
			wantResult:                nil,
			wantErr:                   false,
		},
		{
			name:                      "overlap with route table actions returns allowed",
			routeTable:                &armnetwork.RouteTable{ID: ptr.To(routeTableID)},
			roleDefinitionActions:     []string{"Microsoft.Network/routeTables/join/action"},
			roleDefinitionDataActions: nil,
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
				m.EXPECT().CreateAuthorizationRequest(routeTableResourceID.String(), gomock.Any(), fakeToken.Token).
					Return(&azurecheckaccessv2client.AuthorizationRequest{}, nil)
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationDecisionResponse{
						Value: []azurecheckaccessv2client.AuthorizationDecision{
							{ActionId: "Microsoft.Network/routeTables/join/action", AccessDecision: azurecheckaccessv2client.Allowed},
						},
					}, nil)
			},
			wantResult: nil,
			wantErr:    false,
		},
		{
			name:                      "CheckAccessV2 returns not allowed produces decisions",
			routeTable:                &armnetwork.RouteTable{ID: ptr.To(routeTableID)},
			roleDefinitionActions:     []string{"Microsoft.Network/routeTables/join/action"},
			roleDefinitionDataActions: nil,
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
				m.EXPECT().CreateAuthorizationRequest(routeTableResourceID.String(), gomock.Any(), fakeToken.Token).
					Return(&azurecheckaccessv2client.AuthorizationRequest{}, nil)
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationDecisionResponse{
						Value: []azurecheckaccessv2client.AuthorizationDecision{
							{ActionId: "Microsoft.Network/routeTables/join/action", AccessDecision: azurecheckaccessv2client.NotAllowed},
						},
					}, nil)
			},
			wantResult: []*checkaccessv2AuthorizationDecisionData{
				{ActionID: "Microsoft.Network/routeTables/join/action", IsDataAction: false, AccessDecision: azurecheckaccessv2client.NotAllowed},
			},
			wantErr: false,
		},
		{
			name:                      "invalid route table ID returns error",
			routeTable:                &armnetwork.RouteTable{ID: ptr.To("not-a-valid-resource-id")},
			roleDefinitionActions:     []string{"Microsoft.Network/routeTables/join/action"},
			roleDefinitionDataActions: nil,
			wantResult:                nil,
			wantErr:                   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockClient := azureclient.NewMockCheckAccessV2Client(ctrl)
			if tt.setupMock != nil {
				tt.setupMock(mockClient)
			}

			v := &ControlPlaneIdentitiesPermissionsClusterValidation{}
			result, err := v.checkNotAllowedAndDeniedActionsForRouteTable(context.Background(), mockClient, tt.routeTable, tt.roleDefinitionActions, tt.roleDefinitionDataActions, fakeToken)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.wantResult, result)
		})
	}
}

func TestCheckMissingPermissionsForNetworkSecurityGroup(t *testing.T) {
	nsgResourceID, err := azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.Network/networkSecurityGroups/test-nsg")
	require.NoError(t, err)

	identityResourceID, err := azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-identity")
	require.NoError(t, err)

	fakeToken := azcore.AccessToken{Token: "fake-jwt-token"}

	tests := []struct {
		name       string
		actions    []string
		setupMock  func(*azureclient.MockCheckAccessV2Client)
		wantResult *identityResourceMissingPermissions
		wantErr    bool
	}{
		{
			name:    "no missing permissions returns nil",
			actions: []string{"Microsoft.Network/networkSecurityGroups/read"},
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
				m.EXPECT().CreateAuthorizationRequest(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationRequest{}, nil)
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationDecisionResponse{
						Value: []azurecheckaccessv2client.AuthorizationDecision{
							{ActionId: "Microsoft.Network/networkSecurityGroups/read", AccessDecision: azurecheckaccessv2client.Allowed},
						},
					}, nil)
			},
			wantResult: nil,
			wantErr:    false,
		},
		{
			name:    "missing permissions returns result with correct fields",
			actions: []string{"Microsoft.Network/networkSecurityGroups/read", "Microsoft.Network/networkSecurityGroups/write"},
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
				m.EXPECT().CreateAuthorizationRequest(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationRequest{}, nil)
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationDecisionResponse{
						Value: []azurecheckaccessv2client.AuthorizationDecision{
							{ActionId: "Microsoft.Network/networkSecurityGroups/read", AccessDecision: azurecheckaccessv2client.Allowed},
							{ActionId: "Microsoft.Network/networkSecurityGroups/write", AccessDecision: azurecheckaccessv2client.NotAllowed},
						},
					}, nil)
			},
			wantResult: &identityResourceMissingPermissions{
				Resource: nsgResourceID,
				Identity: identityResourceID,
				Decisions: []*checkaccessv2AuthorizationDecisionData{
					{ActionID: "Microsoft.Network/networkSecurityGroups/write", IsDataAction: false, AccessDecision: azurecheckaccessv2client.NotAllowed},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockClient := azureclient.NewMockCheckAccessV2Client(ctrl)
			if tt.setupMock != nil {
				tt.setupMock(mockClient)
			}

			v := &ControlPlaneIdentitiesPermissionsClusterValidation{}
			result, err := v.checkMissingPermissionsForNetworkSecurityGroup(context.Background(), mockClient, nsgResourceID, identityResourceID, tt.actions, nil, fakeToken)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.wantResult, result)
		})
	}
}

func TestCheckMissingPermissionsForVNet(t *testing.T) {
	subnetResourceID, err := azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet")
	require.NoError(t, err)

	vnetResourceID := subnetResourceID.Parent

	identityResourceID, err := azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-identity")
	require.NoError(t, err)

	fakeToken := azcore.AccessToken{Token: "fake-jwt-token"}

	tests := []struct {
		name       string
		actions    []string
		setupMock  func(*azureclient.MockCheckAccessV2Client)
		wantResult *identityResourceMissingPermissions
		wantErr    bool
	}{
		{
			name:    "no missing permissions returns nil",
			actions: []string{"Microsoft.Network/virtualNetworks/read"},
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
				m.EXPECT().CreateAuthorizationRequest(vnetResourceID.String(), gomock.Any(), fakeToken.Token).
					Return(&azurecheckaccessv2client.AuthorizationRequest{}, nil)
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationDecisionResponse{
						Value: []azurecheckaccessv2client.AuthorizationDecision{
							{ActionId: "Microsoft.Network/virtualNetworks/read", AccessDecision: azurecheckaccessv2client.Allowed},
						},
					}, nil)
			},
			wantResult: nil,
			wantErr:    false,
		},
		{
			name:    "missing permissions returns result with VNet parent as resource",
			actions: []string{"Microsoft.Network/virtualNetworks/subnets/join/action"},
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
				m.EXPECT().CreateAuthorizationRequest(vnetResourceID.String(), gomock.Any(), fakeToken.Token).
					Return(&azurecheckaccessv2client.AuthorizationRequest{}, nil)
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationDecisionResponse{
						Value: []azurecheckaccessv2client.AuthorizationDecision{
							{ActionId: "Microsoft.Network/virtualNetworks/subnets/join/action", AccessDecision: azurecheckaccessv2client.Denied},
						},
					}, nil)
			},
			wantResult: &identityResourceMissingPermissions{
				Resource: vnetResourceID,
				Identity: identityResourceID,
				Decisions: []*checkaccessv2AuthorizationDecisionData{
					{ActionID: "Microsoft.Network/virtualNetworks/subnets/join/action", IsDataAction: false, AccessDecision: azurecheckaccessv2client.Denied},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockClient := azureclient.NewMockCheckAccessV2Client(ctrl)
			if tt.setupMock != nil {
				tt.setupMock(mockClient)
			}

			v := &ControlPlaneIdentitiesPermissionsClusterValidation{}
			result, err := v.checkMissingPermissionsForVNet(context.Background(), mockClient, subnetResourceID, identityResourceID, tt.actions, nil, fakeToken)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.wantResult, result)
		})
	}
}

func TestCheckMissingPermissionsForRouteTable(t *testing.T) {
	routeTableID := "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.Network/routeTables/test-rt"
	routeTableResourceID, err := azcorearm.ParseResourceID(routeTableID)
	require.NoError(t, err)

	identityResourceID, err := azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-identity")
	require.NoError(t, err)

	fakeToken := azcore.AccessToken{Token: "fake-jwt-token"}

	tests := []struct {
		name       string
		subnet     *armnetwork.Subnet
		actions    []string
		setupMock  func(*azureclient.MockCheckAccessV2Client)
		wantResult *identityResourceMissingPermissions
		wantErr    bool
	}{
		{
			name: "nil route table returns nil",
			subnet: &armnetwork.Subnet{
				Properties: &armnetwork.SubnetPropertiesFormat{
					RouteTable: nil,
				},
			},
			actions:    []string{"Microsoft.Network/routeTables/join/action"},
			wantResult: nil,
			wantErr:    false,
		},
		{
			name: "no missing permissions returns nil",
			subnet: &armnetwork.Subnet{
				Properties: &armnetwork.SubnetPropertiesFormat{
					RouteTable: &armnetwork.RouteTable{ID: ptr.To(routeTableID)},
				},
			},
			actions: []string{"Microsoft.Network/routeTables/join/action"},
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
				m.EXPECT().CreateAuthorizationRequest(routeTableResourceID.String(), gomock.Any(), fakeToken.Token).
					Return(&azurecheckaccessv2client.AuthorizationRequest{}, nil)
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationDecisionResponse{
						Value: []azurecheckaccessv2client.AuthorizationDecision{
							{ActionId: "Microsoft.Network/routeTables/join/action", AccessDecision: azurecheckaccessv2client.Allowed},
						},
					}, nil)
			},
			wantResult: nil,
			wantErr:    false,
		},
		{
			name: "missing permissions returns result with route table resource ID",
			subnet: &armnetwork.Subnet{
				Properties: &armnetwork.SubnetPropertiesFormat{
					RouteTable: &armnetwork.RouteTable{ID: ptr.To(routeTableID)},
				},
			},
			actions: []string{"Microsoft.Network/routeTables/join/action"},
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
				m.EXPECT().CreateAuthorizationRequest(routeTableResourceID.String(), gomock.Any(), fakeToken.Token).
					Return(&azurecheckaccessv2client.AuthorizationRequest{}, nil)
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationDecisionResponse{
						Value: []azurecheckaccessv2client.AuthorizationDecision{
							{ActionId: "Microsoft.Network/routeTables/join/action", AccessDecision: azurecheckaccessv2client.NotAllowed},
						},
					}, nil)
			},
			wantResult: &identityResourceMissingPermissions{
				Resource: routeTableResourceID,
				Identity: identityResourceID,
				Decisions: []*checkaccessv2AuthorizationDecisionData{
					{ActionID: "Microsoft.Network/routeTables/join/action", IsDataAction: false, AccessDecision: azurecheckaccessv2client.NotAllowed},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid route table ID returns error",
			subnet: &armnetwork.Subnet{
				Properties: &armnetwork.SubnetPropertiesFormat{
					RouteTable: &armnetwork.RouteTable{ID: ptr.To("not-a-valid-resource-id")},
				},
			},
			actions:    []string{"Microsoft.Network/routeTables/join/action"},
			wantResult: nil,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockClient := azureclient.NewMockCheckAccessV2Client(ctrl)
			if tt.setupMock != nil {
				tt.setupMock(mockClient)
			}

			v := &ControlPlaneIdentitiesPermissionsClusterValidation{}
			result, err := v.checkMissingPermissionsForRouteTable(context.Background(), mockClient, tt.subnet, identityResourceID, tt.actions, nil, fakeToken)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.wantResult, result)
		})
	}
}

func TestFetchRoleDefinitions(t *testing.T) {
	roleDefID1, err := azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/providers/Microsoft.Authorization/roleDefinitions/11111111-1111-1111-1111-111111111111")
	require.NoError(t, err)
	roleDefID2, err := azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/providers/Microsoft.Authorization/roleDefinitions/22222222-2222-2222-2222-222222222222")
	require.NoError(t, err)

	roleDef1 := armauthorization.RoleDefinition{
		ID:   ptr.To(roleDefID1.String()),
		Name: ptr.To("Role1"),
		Properties: &armauthorization.RoleDefinitionProperties{
			Permissions: []*armauthorization.Permission{
				{Actions: []*string{ptr.To("Microsoft.Network/networkSecurityGroups/read")}},
			},
		},
	}
	roleDef2 := armauthorization.RoleDefinition{
		ID:   ptr.To(roleDefID2.String()),
		Name: ptr.To("Role2"),
		Properties: &armauthorization.RoleDefinitionProperties{
			Permissions: []*armauthorization.Permission{
				{Actions: []*string{ptr.To("Microsoft.Network/virtualNetworks/read")}},
			},
		},
	}

	tests := []struct {
		name        string
		resourceIDs []*azcorearm.ResourceID
		setupMock   func(*cachedreader.MockRoleDefinitionsCachedReader)
		wantResult  []armauthorization.RoleDefinition
		wantErr     bool
	}{
		{
			name:        "single role definition returns one definition",
			resourceIDs: []*azcorearm.ResourceID{roleDefID1},
			setupMock: func(m *cachedreader.MockRoleDefinitionsCachedReader) {
				m.EXPECT().GetCachedByID(gomock.Any(), roleDefID1.String(), nil).
					Return(armauthorization.RoleDefinitionsClientGetByIDResponse{RoleDefinition: roleDef1}, nil)
			},
			wantResult: []armauthorization.RoleDefinition{roleDef1},
			wantErr:    false,
		},
		{
			name:        "multiple role definition IDs returns all",
			resourceIDs: []*azcorearm.ResourceID{roleDefID1, roleDefID2},
			setupMock: func(m *cachedreader.MockRoleDefinitionsCachedReader) {
				m.EXPECT().GetCachedByID(gomock.Any(), roleDefID1.String(), nil).
					Return(armauthorization.RoleDefinitionsClientGetByIDResponse{RoleDefinition: roleDef1}, nil)
				m.EXPECT().GetCachedByID(gomock.Any(), roleDefID2.String(), nil).
					Return(armauthorization.RoleDefinitionsClientGetByIDResponse{RoleDefinition: roleDef2}, nil)
			},
			wantResult: []armauthorization.RoleDefinition{roleDef1, roleDef2},
			wantErr:    false,
		},
		{
			name:        "cached reader returns error",
			resourceIDs: []*azcorearm.ResourceID{roleDefID1},
			setupMock: func(m *cachedreader.MockRoleDefinitionsCachedReader) {
				m.EXPECT().GetCachedByID(gomock.Any(), roleDefID1.String(), nil).
					Return(armauthorization.RoleDefinitionsClientGetByIDResponse{}, fmt.Errorf("cache miss and fetch failed"))
			},
			wantResult: nil,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockCachedReader := cachedreader.NewMockRoleDefinitionsCachedReader(ctrl)
			if tt.setupMock != nil {
				tt.setupMock(mockCachedReader)
			}

			v := &ControlPlaneIdentitiesPermissionsClusterValidation{
				backendIdentityAzureCachedReaders: &cachedreader.BackendIdentityAzureCachedReaders{
					RoleDefinitionsCachedReader: mockCachedReader,
				},
			}
			result, err := v.fetchRoleDefinitions(context.Background(), tt.resourceIDs)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.wantResult, result)
			}
		})
	}
}

func TestRoleActionsForOperator(t *testing.T) {
	roleDefID1, err := azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/providers/Microsoft.Authorization/roleDefinitions/11111111-1111-1111-1111-111111111111")
	require.NoError(t, err)
	roleDefID2, err := azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/providers/Microsoft.Authorization/roleDefinitions/22222222-2222-2222-2222-222222222222")
	require.NoError(t, err)

	tests := []struct {
		name         string
		operatorName string
		config       *azure.ClusterScopedIdentitiesConfig
		setupMock    func(*cachedreader.MockRoleDefinitionsCachedReader)
		wantActions  []string
		wantErr      bool
	}{
		{
			name:         "operator with no role definitions returns error",
			operatorName: "empty-operator",
			config: &azure.ClusterScopedIdentitiesConfig{
				ControlPlaneOperatorsIdentities: azure.ControlPlaneOperatorsIdentities{
					azure.ClusterOperatorIdentifier("empty-operator"): {
						BaseClusterScopedOperatorIdentity: azure.BaseClusterScopedOperatorIdentity{
							BaseClusterScopedIdentity: azure.BaseClusterScopedIdentity{
								RoleDefinitions: []*azure.ClusterScopedIdentityRoleDefinition{},
							},
						},
					},
				},
			},
			wantActions: nil,
			wantErr:     true,
		},
		{
			name:         "operator with role definitions returns union of actions excluding data actions",
			operatorName: "test-operator",
			config: &azure.ClusterScopedIdentitiesConfig{
				ControlPlaneOperatorsIdentities: azure.ControlPlaneOperatorsIdentities{
					azure.ClusterOperatorIdentifier("test-operator"): {
						BaseClusterScopedOperatorIdentity: azure.BaseClusterScopedOperatorIdentity{
							BaseClusterScopedIdentity: azure.BaseClusterScopedIdentity{
								RoleDefinitions: []*azure.ClusterScopedIdentityRoleDefinition{
									{DescriptiveName: "NetworkRole", ResourceID: roleDefID1},
									{DescriptiveName: "StorageRole", ResourceID: roleDefID2},
								},
							},
						},
					},
				},
			},
			setupMock: func(m *cachedreader.MockRoleDefinitionsCachedReader) {
				m.EXPECT().GetCachedByID(gomock.Any(), roleDefID1.String(), nil).
					Return(armauthorization.RoleDefinitionsClientGetByIDResponse{
						RoleDefinition: armauthorization.RoleDefinition{
							ID: ptr.To(roleDefID1.String()),
							Properties: &armauthorization.RoleDefinitionProperties{
								Permissions: []*armauthorization.Permission{
									{
										Actions: []*string{
											ptr.To("Microsoft.Network/networkSecurityGroups/read"),
											ptr.To("Microsoft.Network/networkSecurityGroups/write"),
										},
										DataActions: []*string{
											ptr.To("Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read"),
										},
									},
								},
							},
						},
					}, nil)
				m.EXPECT().GetCachedByID(gomock.Any(), roleDefID2.String(), nil).
					Return(armauthorization.RoleDefinitionsClientGetByIDResponse{
						RoleDefinition: armauthorization.RoleDefinition{
							ID: ptr.To(roleDefID2.String()),
							Properties: &armauthorization.RoleDefinitionProperties{
								Permissions: []*armauthorization.Permission{
									{
										Actions: []*string{
											ptr.To("Microsoft.Network/networkSecurityGroups/read"),
											ptr.To("Microsoft.Network/virtualNetworks/subnets/join/action"),
										},
										DataActions: []*string{
											ptr.To("Microsoft.Storage/storageAccounts/blobServices/containers/blobs/write"),
										},
									},
								},
							},
						},
					}, nil)
			},
			wantActions: []string{
				"Microsoft.Network/networkSecurityGroups/read",
				"Microsoft.Network/networkSecurityGroups/write",
				"Microsoft.Network/virtualNetworks/subnets/join/action",
			},
			wantErr: false,
		},
		{
			name:         "cached reader error returns error",
			operatorName: "test-operator",
			config: &azure.ClusterScopedIdentitiesConfig{
				ControlPlaneOperatorsIdentities: azure.ControlPlaneOperatorsIdentities{
					azure.ClusterOperatorIdentifier("test-operator"): {
						BaseClusterScopedOperatorIdentity: azure.BaseClusterScopedOperatorIdentity{
							BaseClusterScopedIdentity: azure.BaseClusterScopedIdentity{
								RoleDefinitions: []*azure.ClusterScopedIdentityRoleDefinition{
									{DescriptiveName: "TestRole", ResourceID: roleDefID1},
								},
							},
						},
					},
				},
			},
			setupMock: func(m *cachedreader.MockRoleDefinitionsCachedReader) {
				m.EXPECT().GetCachedByID(gomock.Any(), roleDefID1.String(), nil).
					Return(armauthorization.RoleDefinitionsClientGetByIDResponse{}, fmt.Errorf("fetch failed"))
			},
			wantActions: nil,
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockCachedReader := cachedreader.NewMockRoleDefinitionsCachedReader(ctrl)
			if tt.setupMock != nil {
				tt.setupMock(mockCachedReader)
			}

			v := &ControlPlaneIdentitiesPermissionsClusterValidation{
				clusterScopedIdentitiesConfig: tt.config,
				backendIdentityAzureCachedReaders: &cachedreader.BackendIdentityAzureCachedReaders{
					RoleDefinitionsCachedReader: mockCachedReader,
				},
			}
			result, err := v.roleActionsForOperator(context.Background(), tt.operatorName)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.ElementsMatch(t, tt.wantActions, result)
			}
		})
	}
}

func TestRoleDataActionsForOperator(t *testing.T) {
	roleDefID1, err := azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/providers/Microsoft.Authorization/roleDefinitions/11111111-1111-1111-1111-111111111111")
	require.NoError(t, err)
	roleDefID2, err := azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/providers/Microsoft.Authorization/roleDefinitions/22222222-2222-2222-2222-222222222222")
	require.NoError(t, err)

	tests := []struct {
		name            string
		operatorName    string
		config          *azure.ClusterScopedIdentitiesConfig
		setupMock       func(*cachedreader.MockRoleDefinitionsCachedReader)
		wantDataActions []string
		wantErr         bool
	}{
		{
			name:         "operator with no role definitions returns nil without error",
			operatorName: "empty-operator",
			config: &azure.ClusterScopedIdentitiesConfig{
				ControlPlaneOperatorsIdentities: azure.ControlPlaneOperatorsIdentities{
					azure.ClusterOperatorIdentifier("empty-operator"): {
						BaseClusterScopedOperatorIdentity: azure.BaseClusterScopedOperatorIdentity{
							BaseClusterScopedIdentity: azure.BaseClusterScopedIdentity{
								RoleDefinitions: []*azure.ClusterScopedIdentityRoleDefinition{},
							},
						},
					},
				},
			},
			wantDataActions: nil,
			wantErr:         false,
		},
		{
			name:         "operator with role definitions returns union of data actions excluding actions",
			operatorName: "test-operator",
			config: &azure.ClusterScopedIdentitiesConfig{
				ControlPlaneOperatorsIdentities: azure.ControlPlaneOperatorsIdentities{
					azure.ClusterOperatorIdentifier("test-operator"): {
						BaseClusterScopedOperatorIdentity: azure.BaseClusterScopedOperatorIdentity{
							BaseClusterScopedIdentity: azure.BaseClusterScopedIdentity{
								RoleDefinitions: []*azure.ClusterScopedIdentityRoleDefinition{
									{DescriptiveName: "StorageRole", ResourceID: roleDefID1},
									{DescriptiveName: "KeyVaultRole", ResourceID: roleDefID2},
								},
							},
						},
					},
				},
			},
			setupMock: func(m *cachedreader.MockRoleDefinitionsCachedReader) {
				m.EXPECT().GetCachedByID(gomock.Any(), roleDefID1.String(), nil).
					Return(armauthorization.RoleDefinitionsClientGetByIDResponse{
						RoleDefinition: armauthorization.RoleDefinition{
							ID: ptr.To(roleDefID1.String()),
							Properties: &armauthorization.RoleDefinitionProperties{
								Permissions: []*armauthorization.Permission{
									{
										DataActions: []*string{
											ptr.To("Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read"),
											ptr.To("Microsoft.Storage/storageAccounts/blobServices/containers/blobs/write"),
										},
										Actions: []*string{
											ptr.To("Microsoft.Network/networkSecurityGroups/read"),
										},
									},
								},
							},
						},
					}, nil)
				m.EXPECT().GetCachedByID(gomock.Any(), roleDefID2.String(), nil).
					Return(armauthorization.RoleDefinitionsClientGetByIDResponse{
						RoleDefinition: armauthorization.RoleDefinition{
							ID: ptr.To(roleDefID2.String()),
							Properties: &armauthorization.RoleDefinitionProperties{
								Permissions: []*armauthorization.Permission{
									{
										DataActions: []*string{
											ptr.To("Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read"),
											ptr.To("Microsoft.KeyVault/vaults/secrets/readMetadata/action"),
										},
										Actions: []*string{
											ptr.To("Microsoft.Network/virtualNetworks/read"),
										},
									},
								},
							},
						},
					}, nil)
			},
			wantDataActions: []string{
				"Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read",
				"Microsoft.Storage/storageAccounts/blobServices/containers/blobs/write",
				"Microsoft.KeyVault/vaults/secrets/readMetadata/action",
			},
			wantErr: false,
		},
		{
			name:         "cached reader error returns error",
			operatorName: "test-operator",
			config: &azure.ClusterScopedIdentitiesConfig{
				ControlPlaneOperatorsIdentities: azure.ControlPlaneOperatorsIdentities{
					azure.ClusterOperatorIdentifier("test-operator"): {
						BaseClusterScopedOperatorIdentity: azure.BaseClusterScopedOperatorIdentity{
							BaseClusterScopedIdentity: azure.BaseClusterScopedIdentity{
								RoleDefinitions: []*azure.ClusterScopedIdentityRoleDefinition{
									{DescriptiveName: "TestRole", ResourceID: roleDefID1},
								},
							},
						},
					},
				},
			},
			setupMock: func(m *cachedreader.MockRoleDefinitionsCachedReader) {
				m.EXPECT().GetCachedByID(gomock.Any(), roleDefID1.String(), nil).
					Return(armauthorization.RoleDefinitionsClientGetByIDResponse{}, fmt.Errorf("fetch failed"))
			},
			wantDataActions: nil,
			wantErr:         true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockCachedReader := cachedreader.NewMockRoleDefinitionsCachedReader(ctrl)
			if tt.setupMock != nil {
				tt.setupMock(mockCachedReader)
			}

			v := &ControlPlaneIdentitiesPermissionsClusterValidation{
				clusterScopedIdentitiesConfig: tt.config,
				backendIdentityAzureCachedReaders: &cachedreader.BackendIdentityAzureCachedReaders{
					RoleDefinitionsCachedReader: mockCachedReader,
				},
			}
			result, err := v.roleDataActionsForOperator(context.Background(), tt.operatorName)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.ElementsMatch(t, tt.wantDataActions, result)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	const (
		testTenantID         = "11111111-1111-1111-1111-111111111111"
		testSubscriptionID   = "00000000-0000-0000-0000-000000000000"
		testIdentityURL      = "https://identity.example.com"
		testCheckAccessScope = "https://management.azure.com/.default"
	)

	clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster"))
	subnetResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet"))
	nsgResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/test-rg/providers/Microsoft.Network/networkSecurityGroups/test-nsg"))
	operatorIdentityResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-operator-identity"))
	smiResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-smi"))
	roleDefID := api.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/providers/Microsoft.Authorization/roleDefinitions/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"))

	clusterSubscription := &arm.Subscription{
		Properties: &arm.SubscriptionProperties{
			TenantId: ptr.To(testTenantID),
		},
	}

	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{ID: clusterResourceID},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ManagedIdentitiesDataPlaneIdentityURL: testIdentityURL,
		},
		CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
			Platform: api.CustomerPlatformProfile{
				SubnetID:               subnetResourceID,
				NetworkSecurityGroupID: nsgResourceID,
				OperatorsAuthentication: api.OperatorsAuthenticationProfile{
					UserAssignedIdentities: api.UserAssignedIdentitiesProfile{
						ControlPlaneOperators: map[string]*azcorearm.ResourceID{
							string(azure.ClusterOperatorIdentifierCloudControllerManager): operatorIdentityResourceID,
						},
						ServiceManagedIdentity: smiResourceID,
					},
				},
			},
		},
	}

	config := &azure.ClusterScopedIdentitiesConfig{
		ControlPlaneOperatorsIdentities: azure.ControlPlaneOperatorsIdentities{
			azure.ClusterOperatorIdentifierCloudControllerManager: {
				BaseClusterScopedOperatorIdentity: azure.BaseClusterScopedOperatorIdentity{
					BaseClusterScopedIdentity: azure.BaseClusterScopedIdentity{
						RoleDefinitions: []*azure.ClusterScopedIdentityRoleDefinition{
							{DescriptiveName: "TestRole", ResourceID: roleDefID},
						},
					},
				},
			},
		},
	}

	roleDefResponse := armauthorization.RoleDefinitionsClientGetByIDResponse{
		RoleDefinition: armauthorization.RoleDefinition{
			ID: ptr.To(roleDefID.String()),
			Properties: &armauthorization.RoleDefinitionProperties{
				Permissions: []*armauthorization.Permission{
					{
						Actions: []*string{
							ptr.To("Microsoft.Network/networkSecurityGroups/read"),
							ptr.To("Microsoft.Network/virtualNetworks/subnets/join/action"),
						},
					},
				},
			},
		},
	}

	subnetGetResponse := armnetwork.SubnetsClientGetResponse{
		Subnet: armnetwork.Subnet{
			Properties: &armnetwork.SubnetPropertiesFormat{
				RouteTable: nil,
			},
		},
	}

	tests := []struct {
		name             string
		setupCheckAccess func(*azureclient.MockCheckAccessV2Client)
		wantErr          bool
		expectedError    string
	}{
		{
			name: "all permissions granted returns nil",
			setupCheckAccess: func(m *azureclient.MockCheckAccessV2Client) {
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationDecisionResponse{
						Value: []azurecheckaccessv2client.AuthorizationDecision{
							{ActionId: "placeholder", AccessDecision: azurecheckaccessv2client.Allowed},
						},
					}, nil).Times(2)
			},
			wantErr: false,
		},
		{
			name: "missing permissions returns error with details",
			setupCheckAccess: func(m *azureclient.MockCheckAccessV2Client) {
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationDecisionResponse{
						Value: []azurecheckaccessv2client.AuthorizationDecision{
							{ActionId: "placeholder", AccessDecision: azurecheckaccessv2client.NotAllowed},
						},
					}, nil)
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationDecisionResponse{
						Value: []azurecheckaccessv2client.AuthorizationDecision{
							{ActionId: "placeholder", AccessDecision: azurecheckaccessv2client.Allowed},
						},
					}, nil)
			},
			wantErr:       true,
			expectedError: `control plane operators missing required permissions: [{"resource":"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.Network/networkSecurityGroups/test-nsg","identity":"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-operator-identity","decisions":[{"actionId":"placeholder","isDataAction":false,"accessDecision":"NotAllowed"}]}]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)

			mockCheckAccessBuilder := azureclient.NewMockCheckAccessV2ClientBuilder(ctrl)
			mockCheckAccessClient := azureclient.NewMockCheckAccessV2Client(ctrl)
			mockSMIBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
			mockSubnetsClient := azureclient.NewMockSubnetsClient(ctrl)
			mockTokenBuilder := azureclient.NewMockMIDataplaneBasedIdentityAccessTokenRetrieverBuilder(ctrl)
			mockTokenRetriever := azureclient.NewMockMIDataplaneBasedIdentityAccessTokenRetriever(ctrl)
			mockCachedReader := cachedreader.NewMockRoleDefinitionsCachedReader(ctrl)

			mockCheckAccessBuilder.EXPECT().Build(testTenantID).Return(mockCheckAccessClient, nil)
			mockSMIBuilder.EXPECT().SubnetsClient(gomock.Any(), testIdentityURL, smiResourceID, testSubscriptionID).Return(mockSubnetsClient, nil)
			mockSubnetsClient.EXPECT().Get(gomock.Any(), subnetResourceID.ResourceGroupName, subnetResourceID.Parent.Name, subnetResourceID.Name, nil).Return(subnetGetResponse, nil)

			mockCachedReader.EXPECT().GetCachedByID(gomock.Any(), roleDefID.String(), nil).Return(roleDefResponse, nil).Times(2)

			mockTokenBuilder.EXPECT().Build(testIdentityURL, operatorIdentityResourceID).Return(mockTokenRetriever, nil)
			mockTokenRetriever.EXPECT().GetToken(gomock.Any(), gomock.Any()).Return(azcore.AccessToken{Token: "fake-token"}, nil)

			mockCheckAccessClient.EXPECT().CreateAuthorizationRequest(gomock.Any(), gomock.Any(), "fake-token").
				Return(&azurecheckaccessv2client.AuthorizationRequest{
					Actions: []azurecheckaccessv2client.ActionInfo{{Id: "placeholder"}},
				}, nil).Times(2)

			tt.setupCheckAccess(mockCheckAccessClient)

			v := NewControlPlaneIdentitiesPermissionsClusterValidation(
				mockSMIBuilder,
				config,
				&cachedreader.BackendIdentityAzureCachedReaders{RoleDefinitionsCachedReader: mockCachedReader},
				mockCheckAccessBuilder,
				mockTokenBuilder,
				testCheckAccessScope,
			)

			err := v.Validate(context.Background(), clusterSubscription, cluster)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
