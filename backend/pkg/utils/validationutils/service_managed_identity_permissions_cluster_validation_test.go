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

package validationutils

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
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/azure"
)

func TestCheckNotAllowedAndDeniedActionsForDataPlaneOperatorIdentity(t *testing.T) {
	identityResourceID, err := azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-operator-identity")
	require.NoError(t, err)

	fakeToken := azcore.AccessToken{Token: "fake-jwt-token"}

	tests := []struct {
		name       string
		setupMock  func(*azureclient.MockCheckAccessV2Client)
		wantResult []*checkaccessv2AuthorizationDecisionData
		wantErr    bool
	}{
		{
			name: "all hardcoded actions allowed returns nil",
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
				m.EXPECT().CreateAuthorizationRequest(identityResourceID.String(), gomock.Any(), fakeToken.Token).
					Return(&azurecheckaccessv2client.AuthorizationRequest{}, nil)
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationDecisionResponse{
						Value: []azurecheckaccessv2client.AuthorizationDecision{
							{ActionId: "Microsoft.ManagedIdentity/userAssignedIdentities/read", AccessDecision: azurecheckaccessv2client.Allowed},
							{ActionId: "Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/read", AccessDecision: azurecheckaccessv2client.Allowed},
							{ActionId: "Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/write", AccessDecision: azurecheckaccessv2client.Allowed},
							{ActionId: "Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/delete", AccessDecision: azurecheckaccessv2client.Allowed},
						},
					}, nil)
			},
			wantResult: nil,
			wantErr:    false,
		},
		{
			name: "CheckAccessV2 returns not allowed and denied produces decisions",
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
				m.EXPECT().CreateAuthorizationRequest(identityResourceID.String(), gomock.Any(), fakeToken.Token).
					Return(&azurecheckaccessv2client.AuthorizationRequest{}, nil)
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationDecisionResponse{
						Value: []azurecheckaccessv2client.AuthorizationDecision{
							{ActionId: "Microsoft.ManagedIdentity/userAssignedIdentities/read", AccessDecision: azurecheckaccessv2client.Allowed},
							{ActionId: "Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/read", AccessDecision: azurecheckaccessv2client.NotAllowed},
							{ActionId: "Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/write", AccessDecision: azurecheckaccessv2client.Denied},
							{ActionId: "Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/delete", AccessDecision: azurecheckaccessv2client.Allowed},
						},
					}, nil)
			},
			wantResult: []*checkaccessv2AuthorizationDecisionData{
				{ActionID: "Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/read", IsDataAction: false, AccessDecision: azurecheckaccessv2client.NotAllowed},
				{ActionID: "Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/write", IsDataAction: false, AccessDecision: azurecheckaccessv2client.Denied},
			},
			wantErr: false,
		},
		{
			name: "CheckAccess error returns error",
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
				m.EXPECT().CreateAuthorizationRequest(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationRequest{}, nil)
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(nil, fmt.Errorf("check access failed"))
			},
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

			v := &ServiceManagedIdentityPermissionsClusterValidation{}
			result, err := v.checkNotAllowedAndDeniedActionsForDataPlaneOperatorIdentity(context.Background(), mockClient, identityResourceID, fakeToken)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.wantResult, result)
		})
	}
}

func TestCheckMissingPermissionsForDataPlaneOperatorIdentity(t *testing.T) {
	identityResourceID, err := azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-operator-identity")
	require.NoError(t, err)

	smiResourceID, err := azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-smi")
	require.NoError(t, err)

	fakeToken := azcore.AccessToken{Token: "fake-jwt-token"}

	tests := []struct {
		name       string
		setupMock  func(*azureclient.MockCheckAccessV2Client)
		wantResult *identityResourceMissingPermissions
		wantErr    bool
	}{
		{
			name: "no missing permissions returns nil",
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
				m.EXPECT().CreateAuthorizationRequest(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationRequest{}, nil)
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationDecisionResponse{
						Value: []azurecheckaccessv2client.AuthorizationDecision{
							{ActionId: "Microsoft.ManagedIdentity/userAssignedIdentities/read", AccessDecision: azurecheckaccessv2client.Allowed},
							{ActionId: "Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/read", AccessDecision: azurecheckaccessv2client.Allowed},
							{ActionId: "Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/write", AccessDecision: azurecheckaccessv2client.Allowed},
							{ActionId: "Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/delete", AccessDecision: azurecheckaccessv2client.Allowed},
						},
					}, nil)
			},
			wantResult: nil,
			wantErr:    false,
		},
		{
			name: "missing permissions returns result with the data plane operator identity as resource and the SMI as identity",
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
				m.EXPECT().CreateAuthorizationRequest(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationRequest{}, nil)
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationDecisionResponse{
						Value: []azurecheckaccessv2client.AuthorizationDecision{
							{ActionId: "Microsoft.ManagedIdentity/userAssignedIdentities/read", AccessDecision: azurecheckaccessv2client.Allowed},
							{ActionId: "Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/read", AccessDecision: azurecheckaccessv2client.Allowed},
							{ActionId: "Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/write", AccessDecision: azurecheckaccessv2client.NotAllowed},
							{ActionId: "Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/delete", AccessDecision: azurecheckaccessv2client.Allowed},
						},
					}, nil)
			},
			wantResult: &identityResourceMissingPermissions{
				Resource: identityResourceID,
				Identity: smiResourceID,
				Decisions: []*checkaccessv2AuthorizationDecisionData{
					{ActionID: "Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/write", IsDataAction: false, AccessDecision: azurecheckaccessv2client.NotAllowed},
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

			v := &ServiceManagedIdentityPermissionsClusterValidation{}
			result, err := v.checkMissingPermissionsForDataPlaneOperatorIdentity(context.Background(), mockClient, identityResourceID, smiResourceID, fakeToken)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.wantResult, result)
		})
	}
}

func TestCheckNotAllowedAndDeniedActionsForControlPlaneOperatorIdentity(t *testing.T) {
	identityResourceID, err := azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-operator-identity")
	require.NoError(t, err)

	fakeToken := azcore.AccessToken{Token: "fake-jwt-token"}

	tests := []struct {
		name       string
		setupMock  func(*azureclient.MockCheckAccessV2Client)
		wantResult []*checkaccessv2AuthorizationDecisionData
	}{
		{
			name: "managed identity read action allowed returns nil",
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
				m.EXPECT().CreateAuthorizationRequest(identityResourceID.String(), []string{"Microsoft.ManagedIdentity/userAssignedIdentities/read"}, fakeToken.Token).
					Return(&azurecheckaccessv2client.AuthorizationRequest{}, nil)
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationDecisionResponse{
						Value: []azurecheckaccessv2client.AuthorizationDecision{
							{ActionId: "Microsoft.ManagedIdentity/userAssignedIdentities/read", AccessDecision: azurecheckaccessv2client.Allowed},
						},
					}, nil)
			},
			wantResult: nil,
		},
		{
			name: "managed identity read action not allowed is reported",
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
				m.EXPECT().CreateAuthorizationRequest(identityResourceID.String(), []string{"Microsoft.ManagedIdentity/userAssignedIdentities/read"}, fakeToken.Token).
					Return(&azurecheckaccessv2client.AuthorizationRequest{}, nil)
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationDecisionResponse{
						Value: []azurecheckaccessv2client.AuthorizationDecision{
							{ActionId: "Microsoft.ManagedIdentity/userAssignedIdentities/read", AccessDecision: azurecheckaccessv2client.NotAllowed},
						},
					}, nil)
			},
			wantResult: []*checkaccessv2AuthorizationDecisionData{
				{ActionID: "Microsoft.ManagedIdentity/userAssignedIdentities/read", IsDataAction: false, AccessDecision: azurecheckaccessv2client.NotAllowed},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockClient := azureclient.NewMockCheckAccessV2Client(ctrl)
			if tt.setupMock != nil {
				tt.setupMock(mockClient)
			}

			v := &ServiceManagedIdentityPermissionsClusterValidation{}
			result, err := v.checkNotAllowedAndDeniedActionsForControlPlaneOperatorIdentity(context.Background(), mockClient, identityResourceID, fakeToken)

			assert.NoError(t, err)
			assert.Equal(t, tt.wantResult, result)
		})
	}
}

func TestCheckMissingPermissionsForControlPlaneOperatorIdentity(t *testing.T) {
	identityResourceID, err := azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-operator-identity")
	require.NoError(t, err)

	smiResourceID, err := azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-smi")
	require.NoError(t, err)

	fakeToken := azcore.AccessToken{Token: "fake-jwt-token"}

	tests := []struct {
		name       string
		setupMock  func(*azureclient.MockCheckAccessV2Client)
		wantResult *identityResourceMissingPermissions
	}{
		{
			name: "no missing permissions returns nil",
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
				m.EXPECT().CreateAuthorizationRequest(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationRequest{}, nil)
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationDecisionResponse{
						Value: []azurecheckaccessv2client.AuthorizationDecision{
							{ActionId: "Microsoft.ManagedIdentity/userAssignedIdentities/read", AccessDecision: azurecheckaccessv2client.Allowed},
						},
					}, nil)
			},
			wantResult: nil,
		},
		{
			name: "missing permissions returns result with the control plane operator identity as resource and the SMI as identity",
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
				m.EXPECT().CreateAuthorizationRequest(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationRequest{}, nil)
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationDecisionResponse{
						Value: []azurecheckaccessv2client.AuthorizationDecision{
							{ActionId: "Microsoft.ManagedIdentity/userAssignedIdentities/read", AccessDecision: azurecheckaccessv2client.NotAllowed},
						},
					}, nil)
			},
			wantResult: &identityResourceMissingPermissions{
				Resource: identityResourceID,
				Identity: smiResourceID,
				Decisions: []*checkaccessv2AuthorizationDecisionData{
					{ActionID: "Microsoft.ManagedIdentity/userAssignedIdentities/read", IsDataAction: false, AccessDecision: azurecheckaccessv2client.NotAllowed},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockClient := azureclient.NewMockCheckAccessV2Client(ctrl)
			if tt.setupMock != nil {
				tt.setupMock(mockClient)
			}

			v := &ServiceManagedIdentityPermissionsClusterValidation{}
			result, err := v.checkMissingPermissionsForControlPlaneOperatorIdentity(context.Background(), mockClient, identityResourceID, smiResourceID, fakeToken)

			assert.NoError(t, err)
			assert.Equal(t, tt.wantResult, result)
		})
	}
}

func TestCheckMissingPermissionsForNetworkSecurityGroupSMI(t *testing.T) {
	nsgResourceID, err := azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.Network/networkSecurityGroups/test-nsg")
	require.NoError(t, err)

	smiResourceID, err := azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-smi")
	require.NoError(t, err)

	fakeToken := azcore.AccessToken{Token: "fake-jwt-token"}

	tests := []struct {
		name       string
		actions    []string
		setupMock  func(*azureclient.MockCheckAccessV2Client)
		wantResult *identityResourceMissingPermissions
	}{
		{
			name:       "no overlap with NSG actions returns nil without API call",
			actions:    []string{"Microsoft.ManagedIdentity/userAssignedIdentities/read"},
			wantResult: nil,
		},
		{
			name:    "missing permissions returns result with NSG as resource and the SMI as identity",
			actions: []string{"Microsoft.Network/networkSecurityGroups/read", "Microsoft.Network/networkSecurityGroups/join/action"},
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
				m.EXPECT().CreateAuthorizationRequest(nsgResourceID.String(), gomock.Any(), fakeToken.Token).
					Return(&azurecheckaccessv2client.AuthorizationRequest{}, nil)
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationDecisionResponse{
						Value: []azurecheckaccessv2client.AuthorizationDecision{
							{ActionId: "Microsoft.Network/networkSecurityGroups/read", AccessDecision: azurecheckaccessv2client.Allowed},
							{ActionId: "Microsoft.Network/networkSecurityGroups/join/action", AccessDecision: azurecheckaccessv2client.NotAllowed},
						},
					}, nil)
			},
			wantResult: &identityResourceMissingPermissions{
				Resource: nsgResourceID,
				Identity: smiResourceID,
				Decisions: []*checkaccessv2AuthorizationDecisionData{
					{ActionID: "Microsoft.Network/networkSecurityGroups/join/action", IsDataAction: false, AccessDecision: azurecheckaccessv2client.NotAllowed},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockClient := azureclient.NewMockCheckAccessV2Client(ctrl)
			if tt.setupMock != nil {
				tt.setupMock(mockClient)
			}

			v := &ServiceManagedIdentityPermissionsClusterValidation{}
			result, err := v.checkMissingPermissionsForNetworkSecurityGroup(context.Background(), mockClient, nsgResourceID, smiResourceID, tt.actions, nil, fakeToken)

			assert.NoError(t, err)
			assert.Equal(t, tt.wantResult, result)
		})
	}
}

func TestCheckMissingPermissionsForVNetSMI(t *testing.T) {
	vnetResourceID, err := azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet")
	require.NoError(t, err)

	smiResourceID, err := azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-smi")
	require.NoError(t, err)

	fakeToken := azcore.AccessToken{Token: "fake-jwt-token"}

	tests := []struct {
		name       string
		actions    []string
		setupMock  func(*azureclient.MockCheckAccessV2Client)
		wantResult *identityResourceMissingPermissions
	}{
		{
			name:       "no overlap with VNet actions returns nil without API call",
			actions:    []string{"Microsoft.ManagedIdentity/userAssignedIdentities/read"},
			wantResult: nil,
		},
		{
			name:    "missing permissions returns result with VNet as resource",
			actions: []string{"Microsoft.Network/virtualNetworks/join/action"},
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
				m.EXPECT().CreateAuthorizationRequest(vnetResourceID.String(), gomock.Any(), fakeToken.Token).
					Return(&azurecheckaccessv2client.AuthorizationRequest{}, nil)
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationDecisionResponse{
						Value: []azurecheckaccessv2client.AuthorizationDecision{
							{ActionId: "Microsoft.Network/virtualNetworks/join/action", AccessDecision: azurecheckaccessv2client.Denied},
						},
					}, nil)
			},
			wantResult: &identityResourceMissingPermissions{
				Resource: vnetResourceID,
				Identity: smiResourceID,
				Decisions: []*checkaccessv2AuthorizationDecisionData{
					{ActionID: "Microsoft.Network/virtualNetworks/join/action", IsDataAction: false, AccessDecision: azurecheckaccessv2client.Denied},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockClient := azureclient.NewMockCheckAccessV2Client(ctrl)
			if tt.setupMock != nil {
				tt.setupMock(mockClient)
			}

			v := &ServiceManagedIdentityPermissionsClusterValidation{}
			result, err := v.checkMissingPermissionsForVNet(context.Background(), mockClient, vnetResourceID, smiResourceID, tt.actions, nil, fakeToken)

			assert.NoError(t, err)
			assert.Equal(t, tt.wantResult, result)
		})
	}
}

func TestCheckMissingPermissionsForSubnetSMI(t *testing.T) {
	subnetResourceID, err := azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet")
	require.NoError(t, err)

	smiResourceID, err := azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-smi")
	require.NoError(t, err)

	fakeToken := azcore.AccessToken{Token: "fake-jwt-token"}

	tests := []struct {
		name       string
		actions    []string
		setupMock  func(*azureclient.MockCheckAccessV2Client)
		wantResult *identityResourceMissingPermissions
	}{
		{
			name:       "no overlap with subnet actions returns nil without API call",
			actions:    []string{"Microsoft.ManagedIdentity/userAssignedIdentities/read"},
			wantResult: nil,
		},
		{
			name:    "missing permissions returns result with subnet as resource",
			actions: []string{"Microsoft.Network/virtualNetworks/subnets/join/action"},
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
				m.EXPECT().CreateAuthorizationRequest(subnetResourceID.String(), gomock.Any(), fakeToken.Token).
					Return(&azurecheckaccessv2client.AuthorizationRequest{}, nil)
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationDecisionResponse{
						Value: []azurecheckaccessv2client.AuthorizationDecision{
							{ActionId: "Microsoft.Network/virtualNetworks/subnets/join/action", AccessDecision: azurecheckaccessv2client.NotAllowed},
						},
					}, nil)
			},
			wantResult: &identityResourceMissingPermissions{
				Resource: subnetResourceID,
				Identity: smiResourceID,
				Decisions: []*checkaccessv2AuthorizationDecisionData{
					{ActionID: "Microsoft.Network/virtualNetworks/subnets/join/action", IsDataAction: false, AccessDecision: azurecheckaccessv2client.NotAllowed},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockClient := azureclient.NewMockCheckAccessV2Client(ctrl)
			if tt.setupMock != nil {
				tt.setupMock(mockClient)
			}

			v := &ServiceManagedIdentityPermissionsClusterValidation{}
			result, err := v.checkMissingPermissionsForSubnet(context.Background(), mockClient, subnetResourceID, smiResourceID, tt.actions, nil, fakeToken)

			assert.NoError(t, err)
			assert.Equal(t, tt.wantResult, result)
		})
	}
}

func TestCheckMissingPermissionsForNatGatewaySMI(t *testing.T) {
	natGatewayID := "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.Network/natGateways/test-natgw"
	natGatewayResourceID, err := azcorearm.ParseResourceID(natGatewayID)
	require.NoError(t, err)

	smiResourceID, err := azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-smi")
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
			name:       "nil subnet properties returns error",
			subnet:     &armnetwork.Subnet{Properties: nil},
			actions:    []string{"Microsoft.Network/natGateways/join/action"},
			wantResult: nil,
			wantErr:    true,
		},
		{
			name: "nil NAT gateway returns nil",
			subnet: &armnetwork.Subnet{
				Properties: &armnetwork.SubnetPropertiesFormat{NatGateway: nil},
			},
			actions:    []string{"Microsoft.Network/natGateways/join/action"},
			wantResult: nil,
		},
		{
			name: "missing permissions returns result with NAT gateway resource ID",
			subnet: &armnetwork.Subnet{
				Properties: &armnetwork.SubnetPropertiesFormat{
					NatGateway: &armnetwork.SubResource{ID: ptr.To(natGatewayID)},
				},
			},
			actions: []string{"Microsoft.Network/natGateways/join/action"},
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
				m.EXPECT().CreateAuthorizationRequest(natGatewayResourceID.String(), gomock.Any(), fakeToken.Token).
					Return(&azurecheckaccessv2client.AuthorizationRequest{}, nil)
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationDecisionResponse{
						Value: []azurecheckaccessv2client.AuthorizationDecision{
							{ActionId: "Microsoft.Network/natGateways/join/action", AccessDecision: azurecheckaccessv2client.NotAllowed},
						},
					}, nil)
			},
			wantResult: &identityResourceMissingPermissions{
				Resource: natGatewayResourceID,
				Identity: smiResourceID,
				Decisions: []*checkaccessv2AuthorizationDecisionData{
					{ActionID: "Microsoft.Network/natGateways/join/action", IsDataAction: false, AccessDecision: azurecheckaccessv2client.NotAllowed},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockClient := azureclient.NewMockCheckAccessV2Client(ctrl)
			if tt.setupMock != nil {
				tt.setupMock(mockClient)
			}

			v := &ServiceManagedIdentityPermissionsClusterValidation{}
			result, err := v.checkMissingPermissionsForNatGateway(context.Background(), mockClient, tt.subnet, smiResourceID, tt.actions, nil, fakeToken)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.wantResult, result)
		})
	}
}

func TestCheckMissingPermissionsForRouteTableSMI(t *testing.T) {
	routeTableID := "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.Network/routeTables/test-rt"
	routeTableResourceID, err := azcorearm.ParseResourceID(routeTableID)
	require.NoError(t, err)

	smiResourceID, err := azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-smi")
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
			name:       "nil subnet properties returns error",
			subnet:     &armnetwork.Subnet{Properties: nil},
			actions:    []string{"Microsoft.Network/routeTables/join/action"},
			wantResult: nil,
			wantErr:    true,
		},
		{
			name: "nil route table returns nil",
			subnet: &armnetwork.Subnet{
				Properties: &armnetwork.SubnetPropertiesFormat{RouteTable: nil},
			},
			actions:    []string{"Microsoft.Network/routeTables/join/action"},
			wantResult: nil,
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
				Identity: smiResourceID,
				Decisions: []*checkaccessv2AuthorizationDecisionData{
					{ActionID: "Microsoft.Network/routeTables/join/action", IsDataAction: false, AccessDecision: azurecheckaccessv2client.NotAllowed},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockClient := azureclient.NewMockCheckAccessV2Client(ctrl)
			if tt.setupMock != nil {
				tt.setupMock(mockClient)
			}

			v := &ServiceManagedIdentityPermissionsClusterValidation{}
			result, err := v.checkMissingPermissionsForRouteTable(context.Background(), mockClient, tt.subnet, smiResourceID, tt.actions, nil, fakeToken)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.wantResult, result)
		})
	}
}

func TestCheckMissingPermissionsForSubnetNetworkSecurityGroup(t *testing.T) {
	nsgID := "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.Network/networkSecurityGroups/test-subnet-nsg"
	nsgResourceID, err := azcorearm.ParseResourceID(nsgID)
	require.NoError(t, err)

	smiResourceID, err := azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-smi")
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
			name:       "nil subnet properties returns error",
			subnet:     &armnetwork.Subnet{Properties: nil},
			actions:    []string{"Microsoft.Network/networkSecurityGroups/join/action"},
			wantResult: nil,
			wantErr:    true,
		},
		{
			name: "nil NSG returns nil",
			subnet: &armnetwork.Subnet{
				Properties: &armnetwork.SubnetPropertiesFormat{NetworkSecurityGroup: nil},
			},
			actions:    []string{"Microsoft.Network/networkSecurityGroups/join/action"},
			wantResult: nil,
		},
		{
			name: "missing permissions returns result with the subnet's NSG resource ID",
			subnet: &armnetwork.Subnet{
				Properties: &armnetwork.SubnetPropertiesFormat{
					NetworkSecurityGroup: &armnetwork.SecurityGroup{ID: ptr.To(nsgID)},
				},
			},
			actions: []string{"Microsoft.Network/networkSecurityGroups/join/action"},
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
				m.EXPECT().CreateAuthorizationRequest(nsgResourceID.String(), gomock.Any(), fakeToken.Token).
					Return(&azurecheckaccessv2client.AuthorizationRequest{}, nil)
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationDecisionResponse{
						Value: []azurecheckaccessv2client.AuthorizationDecision{
							{ActionId: "Microsoft.Network/networkSecurityGroups/join/action", AccessDecision: azurecheckaccessv2client.NotAllowed},
						},
					}, nil)
			},
			wantResult: &identityResourceMissingPermissions{
				Resource: nsgResourceID,
				Identity: smiResourceID,
				Decisions: []*checkaccessv2AuthorizationDecisionData{
					{ActionID: "Microsoft.Network/networkSecurityGroups/join/action", IsDataAction: false, AccessDecision: azurecheckaccessv2client.NotAllowed},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockClient := azureclient.NewMockCheckAccessV2Client(ctrl)
			if tt.setupMock != nil {
				tt.setupMock(mockClient)
			}

			v := &ServiceManagedIdentityPermissionsClusterValidation{}
			result, err := v.checkMissingPermissionsForSubnetNetworkSecurityGroup(context.Background(), mockClient, tt.subnet, smiResourceID, tt.actions, nil, fakeToken)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.wantResult, result)
		})
	}
}

func TestCheckMissingPermissionsForSubnetAttachedResources(t *testing.T) {
	smiResourceID, err := azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-smi")
	require.NoError(t, err)

	fakeToken := azcore.AccessToken{Token: "fake-jwt-token"}

	t.Run("subnet with no attached resources returns no results", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockClient := azureclient.NewMockCheckAccessV2Client(ctrl)

		subnet := &armnetwork.Subnet{Properties: &armnetwork.SubnetPropertiesFormat{}}

		v := &ServiceManagedIdentityPermissionsClusterValidation{}
		results, err := v.checkMissingPermissionsForSubnetAttachedResources(context.Background(), mockClient, subnet, smiResourceID, []string{"Microsoft.Network/natGateways/join/action", "Microsoft.Network/routeTables/join/action", "Microsoft.Network/networkSecurityGroups/join/action"}, nil, fakeToken)

		require.NoError(t, err)
		assert.Empty(t, results)
	})

	t.Run("subnet with all three attached resources missing permissions returns all three results", func(t *testing.T) {
		natGatewayID := "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.Network/natGateways/test-natgw"
		routeTableID := "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.Network/routeTables/test-rt"
		nsgID := "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.Network/networkSecurityGroups/test-nsg"

		subnet := &armnetwork.Subnet{
			Properties: &armnetwork.SubnetPropertiesFormat{
				NatGateway:           &armnetwork.SubResource{ID: ptr.To(natGatewayID)},
				RouteTable:           &armnetwork.RouteTable{ID: ptr.To(routeTableID)},
				NetworkSecurityGroup: &armnetwork.SecurityGroup{ID: ptr.To(nsgID)},
			},
		}

		ctrl := gomock.NewController(t)
		mockClient := azureclient.NewMockCheckAccessV2Client(ctrl)
		mockClient.EXPECT().CreateAuthorizationRequest(gomock.Any(), gomock.Any(), fakeToken.Token).
			Return(&azurecheckaccessv2client.AuthorizationRequest{}, nil).Times(3)
		mockClient.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
			Return(&azurecheckaccessv2client.AuthorizationDecisionResponse{
				Value: []azurecheckaccessv2client.AuthorizationDecision{
					{ActionId: "join/action", AccessDecision: azurecheckaccessv2client.NotAllowed},
				},
			}, nil).Times(3)

		v := &ServiceManagedIdentityPermissionsClusterValidation{}
		results, err := v.checkMissingPermissionsForSubnetAttachedResources(context.Background(), mockClient, subnet,
			smiResourceID,
			[]string{"Microsoft.Network/natGateways/join/action", "Microsoft.Network/routeTables/join/action", "Microsoft.Network/networkSecurityGroups/join/action"},
			nil, fakeToken)

		require.NoError(t, err)
		require.Len(t, results, 3)
		var resources []string
		for _, r := range results {
			resources = append(resources, r.Resource.String())
		}
		assert.ElementsMatch(t, []string{natGatewayID, routeTableID, nsgID}, resources)
	})
}

func TestRoleActionsForServiceManagedIdentity(t *testing.T) {
	roleDefID1, err := azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/providers/Microsoft.Authorization/roleDefinitions/11111111-1111-1111-1111-111111111111")
	require.NoError(t, err)

	tests := []struct {
		name        string
		config      *azure.ClusterScopedIdentitiesConfig
		setupMock   func(*cachedreader.MockRoleDefinitionsCachedReader)
		wantActions []string
		wantErr     bool
	}{
		{
			name: "no role definitions configured returns error",
			config: &azure.ClusterScopedIdentitiesConfig{
				ServiceManagedIdentity: &azure.ServiceManagedIdentity{
					BaseClusterScopedIdentity: azure.BaseClusterScopedIdentity{
						RoleDefinitions: []*azure.ClusterScopedIdentityRoleDefinition{},
					},
				},
			},
			wantActions: nil,
			wantErr:     true,
		},
		{
			name: "role definitions returns union of actions excluding data actions",
			config: &azure.ClusterScopedIdentitiesConfig{
				ServiceManagedIdentity: &azure.ServiceManagedIdentity{
					BaseClusterScopedIdentity: azure.BaseClusterScopedIdentity{
						RoleDefinitions: []*azure.ClusterScopedIdentityRoleDefinition{
							{DescriptiveName: "SMIRole", ResourceID: roleDefID1},
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
											ptr.To("Microsoft.ManagedIdentity/userAssignedIdentities/read"),
											ptr.To("Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/read"),
										},
										DataActions: []*string{
											ptr.To("Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read"),
										},
									},
								},
							},
						},
					}, nil)
			},
			wantActions: []string{
				"Microsoft.ManagedIdentity/userAssignedIdentities/read",
				"Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/read",
			},
			wantErr: false,
		},
		{
			name: "cached reader error returns error",
			config: &azure.ClusterScopedIdentitiesConfig{
				ServiceManagedIdentity: &azure.ServiceManagedIdentity{
					BaseClusterScopedIdentity: azure.BaseClusterScopedIdentity{
						RoleDefinitions: []*azure.ClusterScopedIdentityRoleDefinition{
							{DescriptiveName: "SMIRole", ResourceID: roleDefID1},
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

			v := &ServiceManagedIdentityPermissionsClusterValidation{
				clusterScopedIdentitiesConfig: tt.config,
				backendIdentityAzureCachedReaders: &cachedreader.BackendIdentityAzureCachedReaders{
					RoleDefinitionsCachedReader: mockCachedReader,
				},
			}
			result, err := v.roleActionsForServiceManagedIdentity(context.Background())

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

func TestValidateServiceManagedIdentityPermissions(t *testing.T) {
	const (
		testTenantID         = "11111111-1111-1111-1111-111111111111"
		testSubscriptionID   = "00000000-0000-0000-0000-000000000000"
		testIdentityURL      = "https://identity.example.com"
		testCheckAccessScope = "https://management.azure.com/.default"
	)

	clusterResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster"))
	subnetResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet"))
	nsgResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/test-rg/providers/Microsoft.Network/networkSecurityGroups/test-nsg"))
	dataPlaneOperatorIdentityResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-dataplane-identity"))
	smiResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-smi"))
	roleDefID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/providers/Microsoft.Authorization/roleDefinitions/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"))

	clusterSubscription := &coreapi.Subscription{
		Properties: &coreapi.SubscriptionProperties{
			TenantId: ptr.To(testTenantID),
		},
	}

	cluster := &coreapi.Cluster{
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{ID: clusterResourceID},
		},
		ServiceProviderProperties: coreapi.ClusterServiceProviderProperties{
			ManagedIdentitiesDataPlaneIdentityURL: testIdentityURL,
		},
		CustomerProperties: coreapi.ClusterCustomerProperties{
			Platform: coreapi.CustomerPlatformProfile{
				SubnetID:               subnetResourceID,
				NetworkSecurityGroupID: nsgResourceID,
				OperatorsAuthentication: coreapi.OperatorsAuthenticationProfile{
					UserAssignedIdentities: coreapi.UserAssignedIdentitiesProfile{
						DataPlaneOperators: map[string]*azcorearm.ResourceID{
							string(azure.ClusterOperatorIdentifierDiskCSIDriver): dataPlaneOperatorIdentityResourceID,
						},
						ServiceManagedIdentity: smiResourceID,
					},
				},
			},
		},
	}

	config := &azure.ClusterScopedIdentitiesConfig{
		ServiceManagedIdentity: &azure.ServiceManagedIdentity{
			BaseClusterScopedIdentity: azure.BaseClusterScopedIdentity{
				RoleDefinitions: []*azure.ClusterScopedIdentityRoleDefinition{
					{DescriptiveName: "SMIRole", ResourceID: roleDefID},
				},
			},
		},
	}

	roleDefResponse := armauthorization.RoleDefinitionsClientGetByIDResponse{
		RoleDefinition: armauthorization.RoleDefinition{
			ID:         ptr.To(roleDefID.String()),
			Properties: &armauthorization.RoleDefinitionProperties{Permissions: []*armauthorization.Permission{{}}},
		},
	}

	subnetGetResponse := armnetwork.SubnetsClientGetResponse{
		Subnet: armnetwork.Subnet{
			Properties: &armnetwork.SubnetPropertiesFormat{
				RouteTable: nil,
				NatGateway: nil,
			},
		},
	}

	tests := []struct {
		name                string
		setupCheckAccess    func(*azureclient.MockCheckAccessV2Client)
		wantOutcome         OutcomeType
		wantInternalMessage string
	}{
		{
			name: "all permissions granted returns passed",
			setupCheckAccess: func(m *azureclient.MockCheckAccessV2Client) {
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationDecisionResponse{
						Value: []azurecheckaccessv2client.AuthorizationDecision{
							{ActionId: "Microsoft.ManagedIdentity/userAssignedIdentities/read", AccessDecision: azurecheckaccessv2client.Allowed},
							{ActionId: "Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/read", AccessDecision: azurecheckaccessv2client.Allowed},
							{ActionId: "Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/write", AccessDecision: azurecheckaccessv2client.Allowed},
							{ActionId: "Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/delete", AccessDecision: azurecheckaccessv2client.Allowed},
						},
					}, nil)
			},
			wantOutcome: OutcomeTypePassed,
		},
		{
			name: "missing permissions returns failed with details",
			setupCheckAccess: func(m *azureclient.MockCheckAccessV2Client) {
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationDecisionResponse{
						Value: []azurecheckaccessv2client.AuthorizationDecision{
							{ActionId: "Microsoft.ManagedIdentity/userAssignedIdentities/read", AccessDecision: azurecheckaccessv2client.Allowed},
							{ActionId: "Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/read", AccessDecision: azurecheckaccessv2client.Allowed},
							{ActionId: "Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/write", AccessDecision: azurecheckaccessv2client.NotAllowed},
							{ActionId: "Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/delete", AccessDecision: azurecheckaccessv2client.Allowed},
						},
					}, nil)
			},
			wantOutcome:         OutcomeTypeFailed,
			wantInternalMessage: `Cluster's service managed identity missing required permissions over expected resources: [{"resource":"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-dataplane-identity","identity":"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-smi","decisions":[{"actionId":"Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/write","isDataAction":false,"accessDecision":"NotAllowed"}]}]`,
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

			mockTokenBuilder.EXPECT().Build(testIdentityURL, smiResourceID).Return(mockTokenRetriever, nil)
			mockTokenRetriever.EXPECT().GetToken(gomock.Any(), gomock.Any()).Return(azcore.AccessToken{Token: "fake-token"}, nil)

			mockCheckAccessClient.EXPECT().CreateAuthorizationRequest(dataPlaneOperatorIdentityResourceID.String(), gomock.Any(), "fake-token").
				Return(&azurecheckaccessv2client.AuthorizationRequest{
					Actions: []azurecheckaccessv2client.ActionInfo{
						{Id: "Microsoft.ManagedIdentity/userAssignedIdentities/read"},
						{Id: "Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/read"},
						{Id: "Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/write"},
						{Id: "Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/delete"},
					},
				}, nil)

			tt.setupCheckAccess(mockCheckAccessClient)

			v := NewServiceManagedIdentityPermissionsClusterValidation(
				mockSMIBuilder,
				config,
				&cachedreader.BackendIdentityAzureCachedReaders{RoleDefinitionsCachedReader: mockCachedReader},
				mockCheckAccessBuilder,
				mockTokenBuilder,
				testCheckAccessScope,
			)

			result := v.Validate(context.Background(), clusterSubscription, cluster)
			require.NoError(t, result.Validate())
			assert.Equal(t, tt.wantOutcome, result.Outcome.Type)
			if tt.wantInternalMessage != "" {
				assert.Contains(t, result.InternalMessage(), tt.wantInternalMessage)
			}
		})
	}
}

// TestValidateServiceManagedIdentityPermissionsSwift covers the SWIFT-networked path: when the cluster has a VNet
// integration subnet, Validate must also fetch it and check permissions over its attached NAT gateway, route table,
// and NSG, plus the VNet containing it and the subnet itself.
func TestValidateServiceManagedIdentityPermissionsSwift(t *testing.T) {
	const (
		testTenantID         = "11111111-1111-1111-1111-111111111111"
		testSubscriptionID   = "00000000-0000-0000-0000-000000000000"
		testIdentityURL      = "https://identity.example.com"
		testCheckAccessScope = "https://management.azure.com/.default"
	)

	clusterResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster"))
	subnetResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet"))
	vnetIntegrationSubnetResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet-integration-vnet/subnets/test-vnet-integration-subnet"))
	nsgResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/test-rg/providers/Microsoft.Network/networkSecurityGroups/test-nsg"))
	vnetIntegrationSubnetNsgID := "/subscriptions/" + testSubscriptionID + "/resourceGroups/test-rg/providers/Microsoft.Network/networkSecurityGroups/test-vnet-integration-subnet-nsg"
	vnetIntegrationSubnetNsgResourceID := metadataapi.Must(azcorearm.ParseResourceID(vnetIntegrationSubnetNsgID))
	smiResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-smi"))
	roleDefID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/providers/Microsoft.Authorization/roleDefinitions/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"))

	clusterSubscription := &coreapi.Subscription{
		Properties: &coreapi.SubscriptionProperties{
			TenantId: ptr.To(testTenantID),
		},
	}

	cluster := &coreapi.Cluster{
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{ID: clusterResourceID},
		},
		ServiceProviderProperties: coreapi.ClusterServiceProviderProperties{
			ManagedIdentitiesDataPlaneIdentityURL: testIdentityURL,
		},
		CustomerProperties: coreapi.ClusterCustomerProperties{
			Platform: coreapi.CustomerPlatformProfile{
				SubnetID:                subnetResourceID,
				VnetIntegrationSubnetID: vnetIntegrationSubnetResourceID,
				NetworkSecurityGroupID:  nsgResourceID,
				OperatorsAuthentication: coreapi.OperatorsAuthenticationProfile{
					UserAssignedIdentities: coreapi.UserAssignedIdentitiesProfile{
						ServiceManagedIdentity: smiResourceID,
					},
				},
			},
		},
	}

	// Grant only the NSG join action and the subnet read action, so CheckAccess is exercised exactly for the three
	// relevant resources (the declared cluster NSG, the VNet integration subnet's attached NSG, and the VNet
	// integration subnet itself), keeping the mock setup focused.
	config := &azure.ClusterScopedIdentitiesConfig{
		ServiceManagedIdentity: &azure.ServiceManagedIdentity{
			BaseClusterScopedIdentity: azure.BaseClusterScopedIdentity{
				RoleDefinitions: []*azure.ClusterScopedIdentityRoleDefinition{
					{DescriptiveName: "SMIRole", ResourceID: roleDefID},
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
							ptr.To("Microsoft.Network/networkSecurityGroups/join/action"),
							ptr.To("Microsoft.Network/virtualNetworks/subnets/read"),
						},
					},
				},
			},
		},
	}

	mainSubnetGetResponse := armnetwork.SubnetsClientGetResponse{
		Subnet: armnetwork.Subnet{
			Properties: &armnetwork.SubnetPropertiesFormat{},
		},
	}

	vnetIntegrationSubnetGetResponse := armnetwork.SubnetsClientGetResponse{
		Subnet: armnetwork.Subnet{
			Properties: &armnetwork.SubnetPropertiesFormat{
				NetworkSecurityGroup: &armnetwork.SecurityGroup{ID: ptr.To(vnetIntegrationSubnetNsgID)},
			},
		},
	}

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
	mockSubnetsClient.EXPECT().Get(gomock.Any(), subnetResourceID.ResourceGroupName, subnetResourceID.Parent.Name, subnetResourceID.Name, nil).Return(mainSubnetGetResponse, nil)
	mockSubnetsClient.EXPECT().Get(gomock.Any(), vnetIntegrationSubnetResourceID.ResourceGroupName, vnetIntegrationSubnetResourceID.Parent.Name, vnetIntegrationSubnetResourceID.Name, nil).Return(vnetIntegrationSubnetGetResponse, nil)

	mockCachedReader.EXPECT().GetCachedByID(gomock.Any(), roleDefID.String(), nil).Return(roleDefResponse, nil).Times(2)

	mockTokenBuilder.EXPECT().Build(testIdentityURL, smiResourceID).Return(mockTokenRetriever, nil)
	mockTokenRetriever.EXPECT().GetToken(gomock.Any(), gomock.Any()).Return(azcore.AccessToken{Token: "fake-token"}, nil)

	mockCheckAccessClient.EXPECT().CreateAuthorizationRequest(nsgResourceID.String(), gomock.Any(), "fake-token").
		Return(&azurecheckaccessv2client.AuthorizationRequest{
			Resource: azurecheckaccessv2client.ResourceInfo{Id: nsgResourceID.String()},
			Actions:  []azurecheckaccessv2client.ActionInfo{{Id: "Microsoft.Network/networkSecurityGroups/join/action"}},
		}, nil)
	mockCheckAccessClient.EXPECT().CreateAuthorizationRequest(vnetIntegrationSubnetNsgResourceID.String(), gomock.Any(), "fake-token").
		Return(&azurecheckaccessv2client.AuthorizationRequest{
			Resource: azurecheckaccessv2client.ResourceInfo{Id: vnetIntegrationSubnetNsgResourceID.String()},
			Actions:  []azurecheckaccessv2client.ActionInfo{{Id: "Microsoft.Network/networkSecurityGroups/join/action"}},
		}, nil)
	mockCheckAccessClient.EXPECT().CreateAuthorizationRequest(subnetResourceID.String(), gomock.Any(), "fake-token").
		Return(&azurecheckaccessv2client.AuthorizationRequest{
			Resource: azurecheckaccessv2client.ResourceInfo{Id: subnetResourceID.String()},
			Actions:  []azurecheckaccessv2client.ActionInfo{{Id: "Microsoft.Network/virtualNetworks/subnets/read"}},
		}, nil)
	mockCheckAccessClient.EXPECT().CreateAuthorizationRequest(vnetIntegrationSubnetResourceID.String(), gomock.Any(), "fake-token").
		Return(&azurecheckaccessv2client.AuthorizationRequest{
			Resource: azurecheckaccessv2client.ResourceInfo{Id: vnetIntegrationSubnetResourceID.String()},
			Actions:  []azurecheckaccessv2client.ActionInfo{{Id: "Microsoft.Network/virtualNetworks/subnets/read"}},
		}, nil)

	mockCheckAccessClient.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req azurecheckaccessv2client.AuthorizationRequest) (*azurecheckaccessv2client.AuthorizationDecisionResponse, error) {
			decision := azurecheckaccessv2client.Allowed
			if req.Resource.Id == vnetIntegrationSubnetNsgResourceID.String() {
				decision = azurecheckaccessv2client.NotAllowed
			}
			return &azurecheckaccessv2client.AuthorizationDecisionResponse{
				Value: []azurecheckaccessv2client.AuthorizationDecision{
					{ActionId: req.Actions[0].Id, AccessDecision: decision},
				},
			}, nil
		}).Times(4)

	v := NewServiceManagedIdentityPermissionsClusterValidation(
		mockSMIBuilder,
		config,
		&cachedreader.BackendIdentityAzureCachedReaders{RoleDefinitionsCachedReader: mockCachedReader},
		mockCheckAccessBuilder,
		mockTokenBuilder,
		testCheckAccessScope,
	)

	result := v.Validate(context.Background(), clusterSubscription, cluster)
	require.NoError(t, result.Validate())
	assert.Equal(t, OutcomeTypeFailed, result.Outcome.Type)
	assert.Contains(t, result.InternalMessage(), vnetIntegrationSubnetNsgResourceID.String())
}

// TestValidateServiceManagedIdentityPermissionsSubnetFetchFailure covers the case where the service managed identity
// itself cannot read the cluster's subnet (e.g. it genuinely lacks subnet read permission). That Get is only used to
// discover subnet-attached resources (NAT gateway, route table, NSG); it must not prevent the CheckAccessV2-based
// checks (which don't require it) from running, nor hide a missing permission already found elsewhere.
func TestValidateServiceManagedIdentityPermissionsSubnetFetchFailure(t *testing.T) {
	const (
		testTenantID         = "11111111-1111-1111-1111-111111111111"
		testSubscriptionID   = "00000000-0000-0000-0000-000000000000"
		testIdentityURL      = "https://identity.example.com"
		testCheckAccessScope = "https://management.azure.com/.default"
	)

	clusterResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster"))
	subnetResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet"))
	nsgResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/test-rg/providers/Microsoft.Network/networkSecurityGroups/test-nsg"))
	dataPlaneOperatorIdentityResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-dataplane-identity"))
	smiResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-smi"))
	roleDefID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/providers/Microsoft.Authorization/roleDefinitions/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"))

	clusterSubscription := &coreapi.Subscription{
		Properties: &coreapi.SubscriptionProperties{
			TenantId: ptr.To(testTenantID),
		},
	}

	cluster := &coreapi.Cluster{
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{ID: clusterResourceID},
		},
		ServiceProviderProperties: coreapi.ClusterServiceProviderProperties{
			ManagedIdentitiesDataPlaneIdentityURL: testIdentityURL,
		},
		CustomerProperties: coreapi.ClusterCustomerProperties{
			Platform: coreapi.CustomerPlatformProfile{
				SubnetID:               subnetResourceID,
				NetworkSecurityGroupID: nsgResourceID,
				OperatorsAuthentication: coreapi.OperatorsAuthenticationProfile{
					UserAssignedIdentities: coreapi.UserAssignedIdentitiesProfile{
						DataPlaneOperators: map[string]*azcorearm.ResourceID{
							string(azure.ClusterOperatorIdentifierDiskCSIDriver): dataPlaneOperatorIdentityResourceID,
						},
						ServiceManagedIdentity: smiResourceID,
					},
				},
			},
		},
	}

	config := &azure.ClusterScopedIdentitiesConfig{
		ServiceManagedIdentity: &azure.ServiceManagedIdentity{
			BaseClusterScopedIdentity: azure.BaseClusterScopedIdentity{
				RoleDefinitions: []*azure.ClusterScopedIdentityRoleDefinition{
					{DescriptiveName: "SMIRole", ResourceID: roleDefID},
				},
			},
		},
	}

	roleDefResponse := armauthorization.RoleDefinitionsClientGetByIDResponse{
		RoleDefinition: armauthorization.RoleDefinition{
			ID:         ptr.To(roleDefID.String()),
			Properties: &armauthorization.RoleDefinitionProperties{Permissions: []*armauthorization.Permission{{}}},
		},
	}

	// setupMocksAndValidate wires up a fresh set of mocks (the subnet Get always fails), lets the caller configure the
	// CheckAccess decisions for the data plane operator's managed-identity check, and runs Validate.
	setupMocksAndValidate := func(t *testing.T, checkAccessDecisions []azurecheckaccessv2client.AuthorizationDecision) ValidationResult {
		t.Helper()
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
		mockSubnetsClient.EXPECT().Get(gomock.Any(), subnetResourceID.ResourceGroupName, subnetResourceID.Parent.Name, subnetResourceID.Name, nil).
			Return(armnetwork.SubnetsClientGetResponse{}, fmt.Errorf("authorization failed"))

		mockCachedReader.EXPECT().GetCachedByID(gomock.Any(), roleDefID.String(), nil).Return(roleDefResponse, nil).Times(2)

		mockTokenBuilder.EXPECT().Build(testIdentityURL, smiResourceID).Return(mockTokenRetriever, nil)
		mockTokenRetriever.EXPECT().GetToken(gomock.Any(), gomock.Any()).Return(azcore.AccessToken{Token: "fake-token"}, nil)

		mockCheckAccessClient.EXPECT().CreateAuthorizationRequest(dataPlaneOperatorIdentityResourceID.String(), gomock.Any(), "fake-token").
			Return(&azurecheckaccessv2client.AuthorizationRequest{
				Actions: []azurecheckaccessv2client.ActionInfo{
					{Id: "Microsoft.ManagedIdentity/userAssignedIdentities/read"},
					{Id: "Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/read"},
					{Id: "Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/write"},
					{Id: "Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/delete"},
				},
			}, nil)
		mockCheckAccessClient.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
			Return(&azurecheckaccessv2client.AuthorizationDecisionResponse{Value: checkAccessDecisions}, nil)

		v := NewServiceManagedIdentityPermissionsClusterValidation(
			mockSMIBuilder,
			config,
			&cachedreader.BackendIdentityAzureCachedReaders{RoleDefinitionsCachedReader: mockCachedReader},
			mockCheckAccessBuilder,
			mockTokenBuilder,
			testCheckAccessScope,
		)

		result := v.Validate(context.Background(), clusterSubscription, cluster)
		require.NoError(t, result.Validate())
		return result
	}

	t.Run("subnet fetch fails but a real missing permission is still found elsewhere returns failed, not unknown, and still mentions the fetch failure", func(t *testing.T) {
		result := setupMocksAndValidate(t, []azurecheckaccessv2client.AuthorizationDecision{
			{ActionId: "Microsoft.ManagedIdentity/userAssignedIdentities/read", AccessDecision: azurecheckaccessv2client.Allowed},
			{ActionId: "Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/read", AccessDecision: azurecheckaccessv2client.Allowed},
			{ActionId: "Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/write", AccessDecision: azurecheckaccessv2client.NotAllowed},
			{ActionId: "Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/delete", AccessDecision: azurecheckaccessv2client.Allowed},
		})

		assert.Equal(t, OutcomeTypeFailed, result.Outcome.Type)
		assert.Contains(t, result.InternalMessage(), dataPlaneOperatorIdentityResourceID.String(), "the real missing permission must still be reported")
		assert.Contains(t, result.InternalMessage(), "failed to get subnet", "the subnet fetch failure must not be silently dropped just because something else also failed")
		assert.Contains(t, result.InternalMessage(), "authorization failed", "the raw fetch error detail belongs in the internal message")

		userMessage := result.Outcome.Failed.UserMessage
		assert.Contains(t, userMessage, dataPlaneOperatorIdentityResourceID.String(), "the user-facing message must still report the real missing permission")
		assert.Contains(t, userMessage, subnetResourceID.String(), "the user-facing message should name the specific subnet that couldn't be fully checked")
		assert.Contains(t, userMessage, "subnet-attached resources", "the user-facing message should explain what couldn't be fully checked")
		assert.NotContains(t, userMessage, "authorization failed", "the raw internal fetch error must not leak into the user-facing message")
		assert.NotContains(t, userMessage, "failed to get subnet", "internal fetch-failure detail must not leak into the user-facing message")
	})

	t.Run("subnet fetch fails and nothing else is missing returns unknown, not passed", func(t *testing.T) {
		result := setupMocksAndValidate(t, []azurecheckaccessv2client.AuthorizationDecision{
			{ActionId: "Microsoft.ManagedIdentity/userAssignedIdentities/read", AccessDecision: azurecheckaccessv2client.Allowed},
			{ActionId: "Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/read", AccessDecision: azurecheckaccessv2client.Allowed},
			{ActionId: "Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/write", AccessDecision: azurecheckaccessv2client.Allowed},
			{ActionId: "Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/delete", AccessDecision: azurecheckaccessv2client.Allowed},
		})

		assert.Equal(t, OutcomeTypeUnknown, result.Outcome.Type)
		assert.Contains(t, result.InternalMessage(), "failed to get subnet")
		assert.Contains(t, result.InternalMessage(), "authorization failed", "the raw fetch error detail belongs in the internal message")

		userMessage := result.Outcome.Unknown.UserMessage
		assert.Contains(t, userMessage, subnetResourceID.String(), "the user-facing message should name the specific subnet that couldn't be fully checked")
		assert.Contains(t, userMessage, "subnet-attached resources", "the user-facing message should explain what couldn't be fully checked")
		assert.NotContains(t, userMessage, "authorization failed", "the raw internal fetch error must not leak into the user-facing message")
		assert.NotContains(t, userMessage, "failed to get subnet", "internal fetch-failure detail must not leak into the user-facing message")
	})
}

// TestValidateServiceManagedIdentityPermissionsSubnetPropertiesNil covers the case where a subnet Get
// succeeds (no error) but returns nil Properties. Unlike an ordinary Get error (which is tolerated and
// folded into a best-effort Unknown/Failed result, see TestValidateServiceManagedIdentityPermissionsSubnetFetchFailure),
// this is treated as unexpected and short-circuits the whole validation immediately: none of the
// CheckAccessV2-based checks should ever run.
func TestValidateServiceManagedIdentityPermissionsSubnetPropertiesNil(t *testing.T) {
	const (
		testTenantID         = "11111111-1111-1111-1111-111111111111"
		testSubscriptionID   = "00000000-0000-0000-0000-000000000000"
		testIdentityURL      = "https://identity.example.com"
		testCheckAccessScope = "https://management.azure.com/.default"
	)

	clusterResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster"))
	subnetResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet"))
	vnetIntegrationSubnetResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-vnet-integration-subnet"))
	smiResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-smi"))
	roleDefID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/providers/Microsoft.Authorization/roleDefinitions/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"))

	clusterSubscription := &coreapi.Subscription{
		Properties: &coreapi.SubscriptionProperties{
			TenantId: ptr.To(testTenantID),
		},
	}

	newCluster := func(withVnetIntegrationSubnet bool) *coreapi.Cluster {
		cluster := &coreapi.Cluster{
			TrackedResource: coreapi.TrackedResource{
				Resource: coreapi.Resource{ID: clusterResourceID},
			},
			ServiceProviderProperties: coreapi.ClusterServiceProviderProperties{
				ManagedIdentitiesDataPlaneIdentityURL: testIdentityURL,
			},
			CustomerProperties: coreapi.ClusterCustomerProperties{
				Platform: coreapi.CustomerPlatformProfile{
					SubnetID: subnetResourceID,
					OperatorsAuthentication: coreapi.OperatorsAuthenticationProfile{
						UserAssignedIdentities: coreapi.UserAssignedIdentitiesProfile{
							ServiceManagedIdentity: smiResourceID,
						},
					},
				},
			},
		}
		if withVnetIntegrationSubnet {
			cluster.CustomerProperties.Platform.VnetIntegrationSubnetID = vnetIntegrationSubnetResourceID
		}
		return cluster
	}

	config := &azure.ClusterScopedIdentitiesConfig{
		ServiceManagedIdentity: &azure.ServiceManagedIdentity{
			BaseClusterScopedIdentity: azure.BaseClusterScopedIdentity{
				RoleDefinitions: []*azure.ClusterScopedIdentityRoleDefinition{
					{DescriptiveName: "SMIRole", ResourceID: roleDefID},
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
							ptr.To("Microsoft.Network/virtualNetworks/subnets/read"),
						},
					},
				},
			},
		},
	}

	setupCommonMocks := func(t *testing.T, mockSubnetsClient *azureclient.MockSubnetsClient) *ServiceManagedIdentityPermissionsClusterValidation {
		t.Helper()
		ctrl := gomock.NewController(t)

		mockCheckAccessBuilder := azureclient.NewMockCheckAccessV2ClientBuilder(ctrl)
		mockCheckAccessClient := azureclient.NewMockCheckAccessV2Client(ctrl)
		mockSMIBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
		mockTokenBuilder := azureclient.NewMockMIDataplaneBasedIdentityAccessTokenRetrieverBuilder(ctrl)
		mockTokenRetriever := azureclient.NewMockMIDataplaneBasedIdentityAccessTokenRetriever(ctrl)
		mockCachedReader := cachedreader.NewMockRoleDefinitionsCachedReader(ctrl)

		// No CheckAccessV2 authorization request/decision mocks are set up: the validation must
		// short-circuit before ever reaching findMissingActionsForServiceManagedIdentity, so if it
		// mistakenly called CreateAuthorizationRequest/CheckAccess, gomock would fail the test for an
		// unexpected call.
		mockCheckAccessBuilder.EXPECT().Build(testTenantID).Return(mockCheckAccessClient, nil)
		mockSMIBuilder.EXPECT().SubnetsClient(gomock.Any(), testIdentityURL, smiResourceID, testSubscriptionID).Return(mockSubnetsClient, nil)
		mockCachedReader.EXPECT().GetCachedByID(gomock.Any(), roleDefID.String(), nil).Return(roleDefResponse, nil).Times(2)
		mockTokenBuilder.EXPECT().Build(testIdentityURL, smiResourceID).Return(mockTokenRetriever, nil)
		mockTokenRetriever.EXPECT().GetToken(gomock.Any(), gomock.Any()).Return(azcore.AccessToken{Token: "fake-token"}, nil)

		return NewServiceManagedIdentityPermissionsClusterValidation(
			mockSMIBuilder,
			config,
			&cachedreader.BackendIdentityAzureCachedReaders{RoleDefinitionsCachedReader: mockCachedReader},
			mockCheckAccessBuilder,
			mockTokenBuilder,
			testCheckAccessScope,
		)
	}

	t.Run("cluster subnet Get succeeds with nil properties returns unknown immediately", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockSubnetsClient := azureclient.NewMockSubnetsClient(ctrl)
		mockSubnetsClient.EXPECT().Get(gomock.Any(), subnetResourceID.ResourceGroupName, subnetResourceID.Parent.Name, subnetResourceID.Name, nil).
			Return(armnetwork.SubnetsClientGetResponse{}, nil)

		v := setupCommonMocks(t, mockSubnetsClient)
		result := v.Validate(context.Background(), clusterSubscription, newCluster(false))
		require.NoError(t, result.Validate())

		assert.Equal(t, OutcomeTypeUnknown, result.Outcome.Type)
		assert.Contains(t, result.InternalMessage(), subnetResourceID.String())
		assert.Contains(t, result.InternalMessage(), "no properties")
	})

	t.Run("vnet integration subnet Get succeeds with nil properties returns unknown immediately", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockSubnetsClient := azureclient.NewMockSubnetsClient(ctrl)
		mockSubnetsClient.EXPECT().Get(gomock.Any(), subnetResourceID.ResourceGroupName, subnetResourceID.Parent.Name, subnetResourceID.Name, nil).
			Return(armnetwork.SubnetsClientGetResponse{Subnet: armnetwork.Subnet{Properties: &armnetwork.SubnetPropertiesFormat{}}}, nil)
		mockSubnetsClient.EXPECT().Get(gomock.Any(), vnetIntegrationSubnetResourceID.ResourceGroupName, vnetIntegrationSubnetResourceID.Parent.Name, vnetIntegrationSubnetResourceID.Name, nil).
			Return(armnetwork.SubnetsClientGetResponse{}, nil)

		v := setupCommonMocks(t, mockSubnetsClient)
		result := v.Validate(context.Background(), clusterSubscription, newCluster(true))
		require.NoError(t, result.Validate())

		assert.Equal(t, OutcomeTypeUnknown, result.Outcome.Type)
		assert.Contains(t, result.InternalMessage(), vnetIntegrationSubnetResourceID.String())
		assert.Contains(t, result.InternalMessage(), "no properties")
	})
}
