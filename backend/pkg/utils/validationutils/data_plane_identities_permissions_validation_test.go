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

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
	azurecheckaccessv2client "github.com/Azure/checkaccess-v2-go-sdk/client"

	"github.com/Azure/ARO-HCP/backend/pkg/azure/cachedreader"
	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/azure"
)

func TestDataPlaneIdentitiesPermissionsValidation_createAuthorizationRequestForDataPlaneIdentity(t *testing.T) {
	testResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.Network/networkSecurityGroups/test-nsg"))

	tests := []struct {
		name            string
		subject         string
		actions         []string
		dataActions     []string
		wantSubject     string
		wantActions     []string
		wantDataActions []string
	}{
		{
			name:            "actions only",
			subject:         "object-id-1",
			actions:         []string{"Microsoft.Network/networkSecurityGroups/read", "Microsoft.Network/networkSecurityGroups/write"},
			dataActions:     nil,
			wantSubject:     "object-id-1",
			wantActions:     []string{"Microsoft.Network/networkSecurityGroups/read", "Microsoft.Network/networkSecurityGroups/write"},
			wantDataActions: nil,
		},
		{
			name:            "data actions only",
			subject:         "object-id-2",
			actions:         nil,
			dataActions:     []string{"Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read"},
			wantSubject:     "object-id-2",
			wantActions:     nil,
			wantDataActions: []string{"Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read"},
		},
		{
			name:            "both actions and data actions",
			subject:         "object-id-3",
			actions:         []string{"Microsoft.Network/networkSecurityGroups/read"},
			dataActions:     []string{"Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read"},
			wantSubject:     "object-id-3",
			wantActions:     []string{"Microsoft.Network/networkSecurityGroups/read"},
			wantDataActions: []string{"Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read"},
		},
		{
			name:            "empty actions and data actions",
			subject:         "object-id-4",
			actions:         nil,
			dataActions:     nil,
			wantSubject:     "object-id-4",
			wantActions:     nil,
			wantDataActions: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &DataPlaneIdentitiesPermissionsValidation{}
			result := v.createAuthorizationRequestForDataPlaneIdentity(tt.subject, testResourceID, tt.actions, tt.dataActions)

			assert.Equal(t, tt.wantSubject, result.Subject.Attributes.ObjectId)
			assert.Equal(t, testResourceID.String(), result.Resource.Id)

			var gotActions, gotDataActions []string
			for _, a := range result.Actions {
				if a.IsDataAction {
					gotDataActions = append(gotDataActions, a.Id)
				} else {
					gotActions = append(gotActions, a.Id)
				}
			}
			assert.Equal(t, tt.wantActions, gotActions)
			assert.Equal(t, tt.wantDataActions, gotDataActions)
		})
	}
}

func TestDataPlaneIdentitiesPermissionsValidation_checkNotAllowedAndDeniedActionsForResourceID(t *testing.T) {
	testResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.Network/networkSecurityGroups/test-nsg"))

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
			name:    "all actions allowed returns nil",
			actions: []string{"Microsoft.Network/networkSecurityGroups/read"},
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
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
			name:    "some actions denied returns denied decisions",
			actions: []string{"Microsoft.Network/networkSecurityGroups/read", "Microsoft.Network/networkSecurityGroups/write"},
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
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
			name:    "CheckAccess error returns error",
			actions: []string{"Microsoft.Network/networkSecurityGroups/read"},
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(nil, fmt.Errorf("check access failed"))
			},
			wantResult: nil,
			wantErr:    true,
		},
		{
			name:    "nil AuthorizationDecisionResponse returns error",
			actions: []string{"Microsoft.Network/networkSecurityGroups/read"},
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(nil, nil)
			},
			wantResult: nil,
			wantErr:    true,
		},
		{
			name:    "mismatch in expected vs returned action count returns error",
			actions: []string{"Microsoft.Network/networkSecurityGroups/read", "Microsoft.Network/networkSecurityGroups/write"},
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
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
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					DoAndReturn(func(_ context.Context, req azurecheckaccessv2client.AuthorizationRequest) (*azurecheckaccessv2client.AuthorizationDecisionResponse, error) {
						require.Len(t, req.Actions, 2)
						assert.False(t, req.Actions[0].IsDataAction)
						assert.True(t, req.Actions[1].IsDataAction)
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

			v := &DataPlaneIdentitiesPermissionsValidation{}
			result, err := v.checkNotAllowedAndDeniedActionsForResourceID(context.Background(), mockClient, testResourceID, tt.actions, tt.dataActions, "obj-1")

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.wantResult, result)
		})
	}
}

func TestDataPlaneIdentitiesPermissionsValidation_checkNotAllowedAndDeniedActionsForNetworkSecurityGroup(t *testing.T) {
	nsgResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.Network/networkSecurityGroups/test-nsg"))

	tests := []struct {
		name                      string
		roleDefinitionActions     []string
		roleDefinitionDataActions []string
		setupMock                 func(*azureclient.MockCheckAccessV2Client)
		wantResult                []*checkaccessv2AuthorizationDecisionData
		wantErr                   bool
	}{
		{
			name:                  "no overlap with NSG actions returns nil without API call",
			roleDefinitionActions: []string{"Microsoft.Compute/virtualMachines/read"},
			wantResult:            nil,
			wantErr:               false,
		},
		{
			name:                  "overlap with NSG actions checks intersection and returns allowed",
			roleDefinitionActions: []string{"Microsoft.Network/networkSecurityGroups/read", "Microsoft.Compute/virtualMachines/read"},
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
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
			name:                  "CheckAccessV2 returns denied produces decisions",
			roleDefinitionActions: []string{"Microsoft.Network/networkSecurityGroups/read", "Microsoft.Network/networkSecurityGroups/write"},
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
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

			v := &DataPlaneIdentitiesPermissionsValidation{}
			result, err := v.checkNotAllowedAndDeniedActionsForNetworkSecurityGroup(context.Background(), mockClient, nsgResourceID, tt.roleDefinitionActions, tt.roleDefinitionDataActions, "obj-1")

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.wantResult, result)
		})
	}
}

func TestDataPlaneIdentitiesPermissionsValidation_checkNotAllowedAndDeniedActionsForVNet(t *testing.T) {
	vnetResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet"))

	tests := []struct {
		name                      string
		roleDefinitionActions     []string
		roleDefinitionDataActions []string
		setupMock                 func(*azureclient.MockCheckAccessV2Client)
		wantResult                []*checkaccessv2AuthorizationDecisionData
		wantErr                   bool
	}{
		{
			name:                  "no overlap with VNet actions returns nil without API call",
			roleDefinitionActions: []string{"Microsoft.Compute/virtualMachines/read"},
			wantResult:            nil,
			wantErr:               false,
		},
		{
			name:                  "overlap with VNet actions checks intersection and returns allowed",
			roleDefinitionActions: []string{"Microsoft.Network/virtualNetworks/read"},
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
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
			name:                  "CheckAccessV2 returns not allowed produces decisions",
			roleDefinitionActions: []string{"Microsoft.Network/virtualNetworks/join/action"},
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
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

			v := &DataPlaneIdentitiesPermissionsValidation{}
			result, err := v.checkNotAllowedAndDeniedActionsForVNet(context.Background(), mockClient, vnetResourceID, tt.roleDefinitionActions, tt.roleDefinitionDataActions, "obj-1")

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.wantResult, result)
		})
	}
}

func TestDataPlaneIdentitiesPermissionsValidation_checkNotAllowedAndDeniedActionsForSubnet(t *testing.T) {
	subnetResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet"))

	tests := []struct {
		name                      string
		roleDefinitionActions     []string
		roleDefinitionDataActions []string
		setupMock                 func(*azureclient.MockCheckAccessV2Client)
		wantResult                []*checkaccessv2AuthorizationDecisionData
		wantErr                   bool
	}{
		{
			name:                  "no overlap with subnet actions returns nil without API call",
			roleDefinitionActions: []string{"Microsoft.Compute/virtualMachines/read"},
			wantResult:            nil,
			wantErr:               false,
		},
		{
			name:                  "overlap with subnet actions checks intersection and returns allowed",
			roleDefinitionActions: []string{"Microsoft.Network/virtualNetworks/subnets/read"},
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationDecisionResponse{
						Value: []azurecheckaccessv2client.AuthorizationDecision{
							{ActionId: "Microsoft.Network/virtualNetworks/subnets/read", AccessDecision: azurecheckaccessv2client.Allowed},
						},
					}, nil)
			},
			wantResult: nil,
			wantErr:    false,
		},
		{
			name:                  "CheckAccessV2 returns not allowed produces decisions",
			roleDefinitionActions: []string{"Microsoft.Network/virtualNetworks/subnets/join/action"},
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationDecisionResponse{
						Value: []azurecheckaccessv2client.AuthorizationDecision{
							{ActionId: "Microsoft.Network/virtualNetworks/subnets/join/action", AccessDecision: azurecheckaccessv2client.NotAllowed},
						},
					}, nil)
			},
			wantResult: []*checkaccessv2AuthorizationDecisionData{
				{ActionID: "Microsoft.Network/virtualNetworks/subnets/join/action", IsDataAction: false, AccessDecision: azurecheckaccessv2client.NotAllowed},
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

			v := &DataPlaneIdentitiesPermissionsValidation{}
			result, err := v.checkNotAllowedAndDeniedActionsForSubnet(context.Background(), mockClient, subnetResourceID, tt.roleDefinitionActions, tt.roleDefinitionDataActions, "obj-1")

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.wantResult, result)
		})
	}
}

func TestDataPlaneIdentitiesPermissionsValidation_checkNotAllowedAndDeniedActionsForNatGateway(t *testing.T) {
	natGatewayID := "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.Network/natGateways/test-natgw"

	tests := []struct {
		name                      string
		natGateway                *armnetwork.SubResource
		roleDefinitionActions     []string
		roleDefinitionDataActions []string
		setupMock                 func(*azureclient.MockCheckAccessV2Client)
		wantResult                []*checkaccessv2AuthorizationDecisionData
		wantErr                   bool
	}{
		{
			name:                  "no overlap with NAT gateway actions returns nil without API call",
			natGateway:            &armnetwork.SubResource{ID: ptr.To(natGatewayID)},
			roleDefinitionActions: []string{"Microsoft.Compute/virtualMachines/read"},
			wantResult:            nil,
			wantErr:               false,
		},
		{
			name:                  "overlap with NAT gateway actions returns allowed",
			natGateway:            &armnetwork.SubResource{ID: ptr.To(natGatewayID)},
			roleDefinitionActions: []string{"Microsoft.Network/natGateways/join/action"},
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationDecisionResponse{
						Value: []azurecheckaccessv2client.AuthorizationDecision{
							{ActionId: "Microsoft.Network/natGateways/join/action", AccessDecision: azurecheckaccessv2client.Allowed},
						},
					}, nil)
			},
			wantResult: nil,
			wantErr:    false,
		},
		{
			name:                  "CheckAccessV2 returns not allowed produces decisions",
			natGateway:            &armnetwork.SubResource{ID: ptr.To(natGatewayID)},
			roleDefinitionActions: []string{"Microsoft.Network/natGateways/join/action"},
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationDecisionResponse{
						Value: []azurecheckaccessv2client.AuthorizationDecision{
							{ActionId: "Microsoft.Network/natGateways/join/action", AccessDecision: azurecheckaccessv2client.NotAllowed},
						},
					}, nil)
			},
			wantResult: []*checkaccessv2AuthorizationDecisionData{
				{ActionID: "Microsoft.Network/natGateways/join/action", IsDataAction: false, AccessDecision: azurecheckaccessv2client.NotAllowed},
			},
			wantErr: false,
		},
		{
			name:                  "invalid NAT gateway ID returns error",
			natGateway:            &armnetwork.SubResource{ID: ptr.To("not-a-valid-resource-id")},
			roleDefinitionActions: []string{"Microsoft.Network/natGateways/join/action"},
			wantResult:            nil,
			wantErr:               true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockClient := azureclient.NewMockCheckAccessV2Client(ctrl)
			if tt.setupMock != nil {
				tt.setupMock(mockClient)
			}

			v := &DataPlaneIdentitiesPermissionsValidation{}
			result, err := v.checkNotAllowedAndDeniedActionsForNatGateway(context.Background(), mockClient, tt.natGateway, tt.roleDefinitionActions, tt.roleDefinitionDataActions, "obj-1")

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.wantResult, result)
		})
	}
}

func TestDataPlaneIdentitiesPermissionsValidation_checkNotAllowedAndDeniedActionsForRouteTable(t *testing.T) {
	routeTableID := "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.Network/routeTables/test-rt"

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
			name:                  "no overlap with route table actions returns nil without API call",
			routeTable:            &armnetwork.RouteTable{ID: ptr.To(routeTableID)},
			roleDefinitionActions: []string{"Microsoft.Compute/virtualMachines/read"},
			wantResult:            nil,
			wantErr:               false,
		},
		{
			name:                  "overlap with route table actions returns allowed",
			routeTable:            &armnetwork.RouteTable{ID: ptr.To(routeTableID)},
			roleDefinitionActions: []string{"Microsoft.Network/routeTables/join/action"},
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
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
			name:                  "CheckAccessV2 returns not allowed produces decisions",
			routeTable:            &armnetwork.RouteTable{ID: ptr.To(routeTableID)},
			roleDefinitionActions: []string{"Microsoft.Network/routeTables/join/action"},
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
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
			name:                  "invalid route table ID returns error",
			routeTable:            &armnetwork.RouteTable{ID: ptr.To("not-a-valid-resource-id")},
			roleDefinitionActions: []string{"Microsoft.Network/routeTables/join/action"},
			wantResult:            nil,
			wantErr:               true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockClient := azureclient.NewMockCheckAccessV2Client(ctrl)
			if tt.setupMock != nil {
				tt.setupMock(mockClient)
			}

			v := &DataPlaneIdentitiesPermissionsValidation{}
			result, err := v.checkNotAllowedAndDeniedActionsForRouteTable(context.Background(), mockClient, tt.routeTable, tt.roleDefinitionActions, tt.roleDefinitionDataActions, "obj-1")

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.wantResult, result)
		})
	}
}

func TestDataPlaneIdentitiesPermissionsValidation_checkMissingPermissionsForNetworkSecurityGroup(t *testing.T) {
	nsgResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.Network/networkSecurityGroups/test-nsg"))
	identityResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-identity"))

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

			v := &DataPlaneIdentitiesPermissionsValidation{}
			result, err := v.checkMissingPermissionsForNetworkSecurityGroup(context.Background(), mockClient, nsgResourceID, identityResourceID, "obj-1", tt.actions, nil)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.wantResult, result)
		})
	}
}

func TestDataPlaneIdentitiesPermissionsValidation_checkMissingPermissionsForVNet(t *testing.T) {
	subnetResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet"))
	vnetResourceID := subnetResourceID.Parent
	identityResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-identity"))

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
			actions: []string{"Microsoft.Network/virtualNetworks/join/action"},
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationDecisionResponse{
						Value: []azurecheckaccessv2client.AuthorizationDecision{
							{ActionId: "Microsoft.Network/virtualNetworks/join/action", AccessDecision: azurecheckaccessv2client.Denied},
						},
					}, nil)
			},
			wantResult: &identityResourceMissingPermissions{
				Resource: vnetResourceID,
				Identity: identityResourceID,
				Decisions: []*checkaccessv2AuthorizationDecisionData{
					{ActionID: "Microsoft.Network/virtualNetworks/join/action", IsDataAction: false, AccessDecision: azurecheckaccessv2client.Denied},
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

			v := &DataPlaneIdentitiesPermissionsValidation{}
			result, err := v.checkMissingPermissionsForVNet(context.Background(), mockClient, subnetResourceID, identityResourceID, "obj-1", tt.actions, nil)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.wantResult, result)
		})
	}
}

func TestDataPlaneIdentitiesPermissionsValidation_checkMissingPermissionsForSubnet(t *testing.T) {
	subnetResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet"))
	identityResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-identity"))

	tests := []struct {
		name       string
		actions    []string
		setupMock  func(*azureclient.MockCheckAccessV2Client)
		wantResult *identityResourceMissingPermissions
		wantErr    bool
	}{
		{
			name:    "no missing permissions returns nil",
			actions: []string{"Microsoft.Network/virtualNetworks/subnets/read"},
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationDecisionResponse{
						Value: []azurecheckaccessv2client.AuthorizationDecision{
							{ActionId: "Microsoft.Network/virtualNetworks/subnets/read", AccessDecision: azurecheckaccessv2client.Allowed},
						},
					}, nil)
			},
			wantResult: nil,
			wantErr:    false,
		},
		{
			name:    "missing permissions returns result with subnet as resource",
			actions: []string{"Microsoft.Network/virtualNetworks/subnets/join/action"},
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationDecisionResponse{
						Value: []azurecheckaccessv2client.AuthorizationDecision{
							{ActionId: "Microsoft.Network/virtualNetworks/subnets/join/action", AccessDecision: azurecheckaccessv2client.NotAllowed},
						},
					}, nil)
			},
			wantResult: &identityResourceMissingPermissions{
				Resource: subnetResourceID,
				Identity: identityResourceID,
				Decisions: []*checkaccessv2AuthorizationDecisionData{
					{ActionID: "Microsoft.Network/virtualNetworks/subnets/join/action", IsDataAction: false, AccessDecision: azurecheckaccessv2client.NotAllowed},
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

			v := &DataPlaneIdentitiesPermissionsValidation{}
			result, err := v.checkMissingPermissionsForSubnet(context.Background(), mockClient, subnetResourceID, identityResourceID, "obj-1", tt.actions, nil)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.wantResult, result)
		})
	}
}

func TestDataPlaneIdentitiesPermissionsValidation_checkMissingPermissionsForNatGateway(t *testing.T) {
	natGatewayID := "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.Network/natGateways/test-natgw"
	natGatewayResourceID := metadataapi.Must(azcorearm.ParseResourceID(natGatewayID))
	identityResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-identity"))

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
				Properties: &armnetwork.SubnetPropertiesFormat{
					NatGateway: nil,
				},
			},
			actions:    []string{"Microsoft.Network/natGateways/join/action"},
			wantResult: nil,
			wantErr:    false,
		},
		{
			name: "nil NAT gateway ID returns nil",
			subnet: &armnetwork.Subnet{
				Properties: &armnetwork.SubnetPropertiesFormat{
					NatGateway: &armnetwork.SubResource{ID: nil},
				},
			},
			actions:    []string{"Microsoft.Network/natGateways/join/action"},
			wantResult: nil,
			wantErr:    false,
		},
		{
			name: "no missing permissions returns nil",
			subnet: &armnetwork.Subnet{
				Properties: &armnetwork.SubnetPropertiesFormat{
					NatGateway: &armnetwork.SubResource{ID: ptr.To(natGatewayID)},
				},
			},
			actions: []string{"Microsoft.Network/natGateways/join/action"},
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationDecisionResponse{
						Value: []azurecheckaccessv2client.AuthorizationDecision{
							{ActionId: "Microsoft.Network/natGateways/join/action", AccessDecision: azurecheckaccessv2client.Allowed},
						},
					}, nil)
			},
			wantResult: nil,
			wantErr:    false,
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
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationDecisionResponse{
						Value: []azurecheckaccessv2client.AuthorizationDecision{
							{ActionId: "Microsoft.Network/natGateways/join/action", AccessDecision: azurecheckaccessv2client.NotAllowed},
						},
					}, nil)
			},
			wantResult: &identityResourceMissingPermissions{
				Resource: natGatewayResourceID,
				Identity: identityResourceID,
				Decisions: []*checkaccessv2AuthorizationDecisionData{
					{ActionID: "Microsoft.Network/natGateways/join/action", IsDataAction: false, AccessDecision: azurecheckaccessv2client.NotAllowed},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid NAT gateway ID returns error",
			subnet: &armnetwork.Subnet{
				Properties: &armnetwork.SubnetPropertiesFormat{
					NatGateway: &armnetwork.SubResource{ID: ptr.To("not-a-valid-resource-id")},
				},
			},
			actions:    []string{"Microsoft.Network/natGateways/join/action"},
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

			v := &DataPlaneIdentitiesPermissionsValidation{}
			result, err := v.checkMissingPermissionsForNatGateway(context.Background(), mockClient, tt.subnet, identityResourceID, "obj-1", tt.actions, nil)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.wantResult, result)
		})
	}
}

func TestDataPlaneIdentitiesPermissionsValidation_checkMissingPermissionsForRouteTable(t *testing.T) {
	routeTableID := "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.Network/routeTables/test-rt"
	routeTableResourceID := metadataapi.Must(azcorearm.ParseResourceID(routeTableID))
	identityResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-identity"))

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
				Properties: &armnetwork.SubnetPropertiesFormat{
					RouteTable: nil,
				},
			},
			actions:    []string{"Microsoft.Network/routeTables/join/action"},
			wantResult: nil,
			wantErr:    false,
		},
		{
			name: "nil route table ID returns nil",
			subnet: &armnetwork.Subnet{
				Properties: &armnetwork.SubnetPropertiesFormat{
					RouteTable: &armnetwork.RouteTable{ID: nil},
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

			v := &DataPlaneIdentitiesPermissionsValidation{}
			result, err := v.checkMissingPermissionsForRouteTable(context.Background(), mockClient, tt.subnet, identityResourceID, "obj-1", tt.actions, nil)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.wantResult, result)
		})
	}
}

func TestDataPlaneIdentitiesPermissionsValidation_retrieveIdentityObjectID(t *testing.T) {
	identityResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-operator-identity"))

	tests := []struct {
		name       string
		setupMock  func(*azureclient.MockUserAssignedIdentitiesClient)
		wantResult string
		wantErr    bool
	}{
		{
			name: "identity with principal ID returns object ID",
			setupMock: func(m *azureclient.MockUserAssignedIdentitiesClient) {
				m.EXPECT().Get(gomock.Any(), identityResourceID.ResourceGroupName, identityResourceID.Name, nil).
					Return(armmsi.UserAssignedIdentitiesClientGetResponse{
						Identity: armmsi.Identity{
							Properties: &armmsi.UserAssignedIdentityProperties{
								PrincipalID: ptr.To("principal-object-id"),
							},
						},
					}, nil)
			},
			wantResult: "principal-object-id",
			wantErr:    false,
		},
		{
			name: "identity Get error returns error",
			setupMock: func(m *azureclient.MockUserAssignedIdentitiesClient) {
				m.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), nil).
					Return(armmsi.UserAssignedIdentitiesClientGetResponse{}, fmt.Errorf("identity not found"))
			},
			wantErr: true,
		},
		{
			name: "identity with nil Properties returns error",
			setupMock: func(m *azureclient.MockUserAssignedIdentitiesClient) {
				m.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), nil).
					Return(armmsi.UserAssignedIdentitiesClientGetResponse{
						Identity: armmsi.Identity{Properties: nil},
					}, nil)
			},
			wantErr: true,
		},
		{
			name: "identity with nil PrincipalID returns error",
			setupMock: func(m *azureclient.MockUserAssignedIdentitiesClient) {
				m.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), nil).
					Return(armmsi.UserAssignedIdentitiesClientGetResponse{
						Identity: armmsi.Identity{
							Properties: &armmsi.UserAssignedIdentityProperties{
								PrincipalID: nil,
							},
						},
					}, nil)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockUAISClient := azureclient.NewMockUserAssignedIdentitiesClient(ctrl)
			tt.setupMock(mockUAISClient)

			v := &DataPlaneIdentitiesPermissionsValidation{}
			result, err := v.retrieveIdentityObjectID(context.Background(), mockUAISClient, identityResourceID)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Empty(t, result)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.wantResult, result)
		})
	}
}

func TestDataPlaneIdentitiesPermissionsValidation_roleActionsForOperator(t *testing.T) {
	roleDefID1 := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/providers/Microsoft.Authorization/roleDefinitions/11111111-1111-1111-1111-111111111111"))
	roleDefID2 := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/providers/Microsoft.Authorization/roleDefinitions/22222222-2222-2222-2222-222222222222"))

	tests := []struct {
		name         string
		operatorName string
		config       *azure.ClusterScopedIdentitiesConfig
		setupMock    func(*cachedreader.MockRoleDefinitionsCachedReader)
		wantActions  []string
		wantErr      bool
	}{
		{
			name:         "operator not in config returns error",
			operatorName: "unknown-operator",
			config: &azure.ClusterScopedIdentitiesConfig{
				DataPlaneOperatorsIdentities: azure.DataPlaneOperatorsIdentities{},
			},
			wantErr: true,
		},
		{
			name:         "operator with no role definitions returns error",
			operatorName: "empty-operator",
			config: &azure.ClusterScopedIdentitiesConfig{
				DataPlaneOperatorsIdentities: azure.DataPlaneOperatorsIdentities{
					azure.ClusterOperatorIdentifier("empty-operator"): &azure.DataPlaneOperatorIdentity{
						BaseClusterScopedOperatorIdentity: azure.BaseClusterScopedOperatorIdentity{
							BaseClusterScopedIdentity: azure.BaseClusterScopedIdentity{
								RoleDefinitions: []*azure.ClusterScopedIdentityRoleDefinition{},
							},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name:         "operator with role definitions returns union of actions",
			operatorName: "test-operator",
			config: &azure.ClusterScopedIdentitiesConfig{
				DataPlaneOperatorsIdentities: azure.DataPlaneOperatorsIdentities{
					azure.ClusterOperatorIdentifier("test-operator"): &azure.DataPlaneOperatorIdentity{
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
				DataPlaneOperatorsIdentities: azure.DataPlaneOperatorsIdentities{
					azure.ClusterOperatorIdentifier("test-operator"): &azure.DataPlaneOperatorIdentity{
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
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockCachedReader := cachedreader.NewMockRoleDefinitionsCachedReader(ctrl)
			if tt.setupMock != nil {
				tt.setupMock(mockCachedReader)
			}

			v := &DataPlaneIdentitiesPermissionsValidation{
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

func TestDataPlaneIdentitiesPermissionsValidation_roleDataActionsForOperator(t *testing.T) {
	roleDefID1 := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/providers/Microsoft.Authorization/roleDefinitions/11111111-1111-1111-1111-111111111111"))

	tests := []struct {
		name            string
		operatorName    string
		config          *azure.ClusterScopedIdentitiesConfig
		setupMock       func(*cachedreader.MockRoleDefinitionsCachedReader)
		wantDataActions []string
		wantErr         bool
	}{
		{
			name:         "operator not in config returns error",
			operatorName: "unknown-operator",
			config: &azure.ClusterScopedIdentitiesConfig{
				DataPlaneOperatorsIdentities: azure.DataPlaneOperatorsIdentities{},
			},
			wantErr: true,
		},
		{
			name:         "operator with no role definitions returns nil without error",
			operatorName: "empty-operator",
			config: &azure.ClusterScopedIdentitiesConfig{
				DataPlaneOperatorsIdentities: azure.DataPlaneOperatorsIdentities{
					azure.ClusterOperatorIdentifier("empty-operator"): &azure.DataPlaneOperatorIdentity{
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
			name:         "operator with role definitions returns union of data actions",
			operatorName: "test-operator",
			config: &azure.ClusterScopedIdentitiesConfig{
				DataPlaneOperatorsIdentities: azure.DataPlaneOperatorsIdentities{
					azure.ClusterOperatorIdentifier("test-operator"): &azure.DataPlaneOperatorIdentity{
						BaseClusterScopedOperatorIdentity: azure.BaseClusterScopedOperatorIdentity{
							BaseClusterScopedIdentity: azure.BaseClusterScopedIdentity{
								RoleDefinitions: []*azure.ClusterScopedIdentityRoleDefinition{
									{DescriptiveName: "StorageRole", ResourceID: roleDefID1},
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
			},
			wantDataActions: []string{
				"Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read",
				"Microsoft.Storage/storageAccounts/blobServices/containers/blobs/write",
			},
			wantErr: false,
		},
		{
			name:         "cached reader error returns error",
			operatorName: "test-operator",
			config: &azure.ClusterScopedIdentitiesConfig{
				DataPlaneOperatorsIdentities: azure.DataPlaneOperatorsIdentities{
					azure.ClusterOperatorIdentifier("test-operator"): &azure.DataPlaneOperatorIdentity{
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
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockCachedReader := cachedreader.NewMockRoleDefinitionsCachedReader(ctrl)
			if tt.setupMock != nil {
				tt.setupMock(mockCachedReader)
			}

			v := &DataPlaneIdentitiesPermissionsValidation{
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

func TestDataPlaneIdentitiesPermissionsValidation_Validate(t *testing.T) {
	const (
		testTenantID       = "11111111-1111-1111-1111-111111111111"
		testSubscriptionID = "00000000-0000-0000-0000-000000000000"
		testIdentityURL    = "https://identity.example.com"
		testPrincipalID    = "principal-00000000-0000-0000-0000-000000000000"
	)

	clusterResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster"))
	subnetResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet"))
	nsgResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/test-rg/providers/Microsoft.Network/networkSecurityGroups/test-nsg"))
	operatorIdentityResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-operator-identity"))
	smiResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-smi"))
	roleDefID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/providers/Microsoft.Authorization/roleDefinitions/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"))

	clusterSubscription := &coreapi.Subscription{
		Properties: &coreapi.SubscriptionProperties{
			TenantId: ptr.To(testTenantID),
		},
	}

	cluster := &coreapi.HCPOpenShiftCluster{
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{ID: clusterResourceID},
		},
		ServiceProviderProperties: coreapi.HCPOpenShiftClusterServiceProviderProperties{
			ManagedIdentitiesDataPlaneIdentityURL: testIdentityURL,
		},
		CustomerProperties: coreapi.HCPOpenShiftClusterCustomerProperties{
			Platform: coreapi.CustomerPlatformProfile{
				SubnetID:               subnetResourceID,
				NetworkSecurityGroupID: nsgResourceID,
				OperatorsAuthentication: coreapi.OperatorsAuthenticationProfile{
					UserAssignedIdentities: coreapi.UserAssignedIdentitiesProfile{
						DataPlaneOperators: map[string]*azcorearm.ResourceID{
							"test-dp-operator": operatorIdentityResourceID,
						},
						ServiceManagedIdentity: smiResourceID,
					},
				},
			},
		},
	}

	config := &azure.ClusterScopedIdentitiesConfig{
		DataPlaneOperatorsIdentities: azure.DataPlaneOperatorsIdentities{
			azure.ClusterOperatorIdentifier("test-dp-operator"): &azure.DataPlaneOperatorIdentity{
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

	uaisGetResponse := armmsi.UserAssignedIdentitiesClientGetResponse{
		Identity: armmsi.Identity{
			Properties: &armmsi.UserAssignedIdentityProperties{
				PrincipalID: ptr.To(testPrincipalID),
			},
		},
	}

	// Role actions [networkSecurityGroups/read, virtualNetworks/subnets/join/action] intersect with the NSG check
	// (1 action) and the Subnet check (1 action); the VNet, NAT gateway, and route table checks have an empty
	// intersection or no attached resource, so exactly 2 CheckAccess calls are made per operator.
	subnetGetResponse := armnetwork.SubnetsClientGetResponse{
		Subnet: armnetwork.Subnet{
			Properties: &armnetwork.SubnetPropertiesFormat{},
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
							{ActionId: "placeholder", AccessDecision: azurecheckaccessv2client.Allowed},
						},
					}, nil).Times(2)
			},
			wantOutcome: OutcomeTypePassed,
		},
		{
			name: "missing permissions returns failed with details",
			setupCheckAccess: func(m *azureclient.MockCheckAccessV2Client) {
				// First call (NSG): denied
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationDecisionResponse{
						Value: []azurecheckaccessv2client.AuthorizationDecision{
							{ActionId: "placeholder", AccessDecision: azurecheckaccessv2client.NotAllowed},
						},
					}, nil)
				// Second call (Subnet): allowed
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(&azurecheckaccessv2client.AuthorizationDecisionResponse{
						Value: []azurecheckaccessv2client.AuthorizationDecision{
							{ActionId: "placeholder", AccessDecision: azurecheckaccessv2client.Allowed},
						},
					}, nil)
			},
			wantOutcome:         OutcomeTypeFailed,
			wantInternalMessage: "Data plane operators missing required permissions:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)

			mockCheckAccessBuilder := azureclient.NewMockCheckAccessV2ClientBuilder(ctrl)
			mockCheckAccessClient := azureclient.NewMockCheckAccessV2Client(ctrl)
			mockSMIBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
			mockSubnetsClient := azureclient.NewMockSubnetsClient(ctrl)
			mockUAISClient := azureclient.NewMockUserAssignedIdentitiesClient(ctrl)
			mockCachedReader := cachedreader.NewMockRoleDefinitionsCachedReader(ctrl)

			mockCheckAccessBuilder.EXPECT().Build(testTenantID).Return(mockCheckAccessClient, nil)
			mockSMIBuilder.EXPECT().UserAssignedIdentitiesClient(gomock.Any(), testIdentityURL, smiResourceID, testSubscriptionID).Return(mockUAISClient, nil)
			mockSMIBuilder.EXPECT().SubnetsClient(gomock.Any(), testIdentityURL, smiResourceID, testSubscriptionID).Return(mockSubnetsClient, nil)
			mockSubnetsClient.EXPECT().Get(gomock.Any(), subnetResourceID.ResourceGroupName, subnetResourceID.Parent.Name, subnetResourceID.Name, nil).Return(subnetGetResponse, nil)
			mockUAISClient.EXPECT().Get(gomock.Any(), operatorIdentityResourceID.ResourceGroupName, operatorIdentityResourceID.Name, nil).Return(uaisGetResponse, nil)
			mockCachedReader.EXPECT().GetCachedByID(gomock.Any(), roleDefID.String(), nil).Return(roleDefResponse, nil).Times(2)

			tt.setupCheckAccess(mockCheckAccessClient)

			v := NewDataPlaneIdentitiesPermissionsValidation(
				mockSMIBuilder,
				config,
				&cachedreader.BackendIdentityAzureCachedReaders{RoleDefinitionsCachedReader: mockCachedReader},
				mockCheckAccessBuilder,
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
