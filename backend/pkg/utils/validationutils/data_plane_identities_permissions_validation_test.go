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

func TestCreateAuthorizationRequestForDataPlaneIdentity(t *testing.T) {
	testResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.Network/networkSecurityGroups/test-nsg"))

	tests := []struct {
		name        string
		subject     string
		actions     []string
		dataActions []string
		wantSubject string
		wantActions int
	}{
		{
			name:        "actions only",
			subject:     "object-id-1",
			actions:     []string{"Microsoft.Network/networkSecurityGroups/read", "Microsoft.Network/networkSecurityGroups/write"},
			dataActions: nil,
			wantSubject: "object-id-1",
			wantActions: 2,
		},
		{
			name:        "data actions only",
			subject:     "object-id-2",
			actions:     nil,
			dataActions: []string{"Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read"},
			wantSubject: "object-id-2",
			wantActions: 1,
		},
		{
			name:        "both actions and data actions",
			subject:     "object-id-3",
			actions:     []string{"Microsoft.Network/networkSecurityGroups/read"},
			dataActions: []string{"Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read"},
			wantSubject: "object-id-3",
			wantActions: 2,
		},
		{
			name:        "empty actions and data actions",
			subject:     "object-id-4",
			actions:     nil,
			dataActions: nil,
			wantSubject: "object-id-4",
			wantActions: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &DataPlaneIdentitiesPermissionsValidation{}
			result := v.createAuthorizationRequestForDataPlaneIdentity(tt.subject, testResourceID, tt.actions, tt.dataActions)

			assert.Equal(t, tt.wantSubject, result.Subject.Attributes.ObjectId)
			assert.Equal(t, testResourceID.String(), result.Resource.Id)
			assert.Len(t, result.Actions, tt.wantActions)

			dataActionCount := 0
			for _, a := range result.Actions {
				if a.IsDataAction {
					dataActionCount++
				}
			}
			assert.Equal(t, len(tt.dataActions), dataActionCount)
		})
	}
}

func TestCheckMissingPermissionsOnResource(t *testing.T) {
	testResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.Network/networkSecurityGroups/test-nsg"))

	tests := []struct {
		name            string
		check           resourcePermissionCheck
		identityObjID   string
		roleActions     []string
		roleDataActions []string
		setupMock       func(*azureclient.MockCheckAccessV2Client)
		wantResult      []*checkaccessv2AuthorizationDecisionData
		wantErr         bool
	}{
		{
			name: "no intersection between required and role actions returns nil without API call",
			check: resourcePermissionCheck{
				resourceID:      testResourceID,
				resourceType:    resourceTypeNetworkSecurityGroup,
				requiredActions: networkSecurityGroupActions,
			},
			identityObjID: "obj-1",
			roleActions:   []string{"Microsoft.Compute/virtualMachines/read"},
			wantResult:    nil,
			wantErr:       false,
		},
		{
			name: "all intersected actions allowed returns nil",
			check: resourcePermissionCheck{
				resourceID:      testResourceID,
				resourceType:    resourceTypeNetworkSecurityGroup,
				requiredActions: networkSecurityGroupActions,
			},
			identityObjID: "obj-1",
			roleActions:   []string{"Microsoft.Network/networkSecurityGroups/read"},
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
			name: "some actions denied returns denied decisions",
			check: resourcePermissionCheck{
				resourceID:      testResourceID,
				resourceType:    resourceTypeNetworkSecurityGroup,
				requiredActions: networkSecurityGroupActions,
			},
			identityObjID: "obj-1",
			roleActions: []string{
				"Microsoft.Network/networkSecurityGroups/read",
				"Microsoft.Network/networkSecurityGroups/write",
			},
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
			name: "CheckAccess error returns error",
			check: resourcePermissionCheck{
				resourceID:      testResourceID,
				resourceType:    resourceTypeNetworkSecurityGroup,
				requiredActions: networkSecurityGroupActions,
			},
			identityObjID: "obj-1",
			roleActions:   []string{"Microsoft.Network/networkSecurityGroups/read"},
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(nil, fmt.Errorf("check access failed"))
			},
			wantResult: nil,
			wantErr:    true,
		},
		{
			name: "nil AuthorizationDecisionResponse returns error",
			check: resourcePermissionCheck{
				resourceID:      testResourceID,
				resourceType:    resourceTypeNetworkSecurityGroup,
				requiredActions: networkSecurityGroupActions,
			},
			identityObjID: "obj-1",
			roleActions:   []string{"Microsoft.Network/networkSecurityGroups/read"},
			setupMock: func(m *azureclient.MockCheckAccessV2Client) {
				m.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).
					Return(nil, nil)
			},
			wantResult: nil,
			wantErr:    true,
		},
		{
			name: "mismatch in expected vs returned action count returns error",
			check: resourcePermissionCheck{
				resourceID:      testResourceID,
				resourceType:    resourceTypeNetworkSecurityGroup,
				requiredActions: networkSecurityGroupActions,
			},
			identityObjID: "obj-1",
			roleActions: []string{
				"Microsoft.Network/networkSecurityGroups/read",
				"Microsoft.Network/networkSecurityGroups/write",
			},
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
			name: "data actions sent with IsDataAction true",
			check: resourcePermissionCheck{
				resourceID:          testResourceID,
				resourceType:        resourceTypeNetworkSecurityGroup,
				requiredActions:     []string{"Microsoft.Network/networkSecurityGroups/read"},
				requiredDataActions: []string{"Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read"},
			},
			identityObjID:   "obj-1",
			roleActions:     []string{"Microsoft.Network/networkSecurityGroups/read"},
			roleDataActions: []string{"Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read"},
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
			result, err := v.checkMissingPermissionsOnResource(context.Background(), mockClient, tt.check, tt.identityObjID, tt.roleActions, tt.roleDataActions)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.wantResult, result)
		})
	}
}

func TestGetSubnetAttachedResources(t *testing.T) {
	subnetResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet"))
	natGatewayID := "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.Network/natGateways/test-natgw"
	routeTableID := "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.Network/routeTables/test-rt"

	tests := []struct {
		name           string
		setupMock      func(*azureclient.MockSubnetsClient)
		wantNatGateway bool
		wantRouteTable bool
		wantErr        bool
	}{
		{
			name: "subnet with no attached resources",
			setupMock: func(m *azureclient.MockSubnetsClient) {
				m.EXPECT().Get(gomock.Any(), subnetResourceID.ResourceGroupName, subnetResourceID.Parent.Name, subnetResourceID.Name, nil).
					Return(armnetwork.SubnetsClientGetResponse{
						Subnet: armnetwork.Subnet{
							Properties: &armnetwork.SubnetPropertiesFormat{},
						},
					}, nil)
			},
			wantNatGateway: false,
			wantRouteTable: false,
			wantErr:        false,
		},
		{
			name: "subnet with NAT gateway and route table",
			setupMock: func(m *azureclient.MockSubnetsClient) {
				m.EXPECT().Get(gomock.Any(), subnetResourceID.ResourceGroupName, subnetResourceID.Parent.Name, subnetResourceID.Name, nil).
					Return(armnetwork.SubnetsClientGetResponse{
						Subnet: armnetwork.Subnet{
							Properties: &armnetwork.SubnetPropertiesFormat{
								NatGateway: &armnetwork.SubResource{ID: ptr.To(natGatewayID)},
								RouteTable: &armnetwork.RouteTable{ID: ptr.To(routeTableID)},
							},
						},
					}, nil)
			},
			wantNatGateway: true,
			wantRouteTable: true,
			wantErr:        false,
		},
		{
			name: "subnet with only NAT gateway",
			setupMock: func(m *azureclient.MockSubnetsClient) {
				m.EXPECT().Get(gomock.Any(), subnetResourceID.ResourceGroupName, subnetResourceID.Parent.Name, subnetResourceID.Name, nil).
					Return(armnetwork.SubnetsClientGetResponse{
						Subnet: armnetwork.Subnet{
							Properties: &armnetwork.SubnetPropertiesFormat{
								NatGateway: &armnetwork.SubResource{ID: ptr.To(natGatewayID)},
							},
						},
					}, nil)
			},
			wantNatGateway: true,
			wantRouteTable: false,
			wantErr:        false,
		},
		{
			name: "subnet with only route table",
			setupMock: func(m *azureclient.MockSubnetsClient) {
				m.EXPECT().Get(gomock.Any(), subnetResourceID.ResourceGroupName, subnetResourceID.Parent.Name, subnetResourceID.Name, nil).
					Return(armnetwork.SubnetsClientGetResponse{
						Subnet: armnetwork.Subnet{
							Properties: &armnetwork.SubnetPropertiesFormat{
								RouteTable: &armnetwork.RouteTable{ID: ptr.To(routeTableID)},
							},
						},
					}, nil)
			},
			wantNatGateway: false,
			wantRouteTable: true,
			wantErr:        false,
		},
		{
			name: "subnet Get error returns error",
			setupMock: func(m *azureclient.MockSubnetsClient) {
				m.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), nil).
					Return(armnetwork.SubnetsClientGetResponse{}, fmt.Errorf("subnet not found"))
			},
			wantErr: true,
		},
		{
			name: "subnet with nil Properties returns empty result",
			setupMock: func(m *azureclient.MockSubnetsClient) {
				m.EXPECT().Get(gomock.Any(), subnetResourceID.ResourceGroupName, subnetResourceID.Parent.Name, subnetResourceID.Name, nil).
					Return(armnetwork.SubnetsClientGetResponse{
						Subnet: armnetwork.Subnet{},
					}, nil)
			},
			wantNatGateway: false,
			wantRouteTable: false,
			wantErr:        false,
		},
		{
			name: "invalid NAT gateway resource ID returns error",
			setupMock: func(m *azureclient.MockSubnetsClient) {
				m.EXPECT().Get(gomock.Any(), subnetResourceID.ResourceGroupName, subnetResourceID.Parent.Name, subnetResourceID.Name, nil).
					Return(armnetwork.SubnetsClientGetResponse{
						Subnet: armnetwork.Subnet{
							Properties: &armnetwork.SubnetPropertiesFormat{
								NatGateway: &armnetwork.SubResource{ID: ptr.To("not-a-valid-id")},
							},
						},
					}, nil)
			},
			wantErr: true,
		},
		{
			name: "invalid route table resource ID returns error",
			setupMock: func(m *azureclient.MockSubnetsClient) {
				m.EXPECT().Get(gomock.Any(), subnetResourceID.ResourceGroupName, subnetResourceID.Parent.Name, subnetResourceID.Name, nil).
					Return(armnetwork.SubnetsClientGetResponse{
						Subnet: armnetwork.Subnet{
							Properties: &armnetwork.SubnetPropertiesFormat{
								RouteTable: &armnetwork.RouteTable{ID: ptr.To("not-a-valid-id")},
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
			mockSubnetsClient := azureclient.NewMockSubnetsClient(ctrl)
			tt.setupMock(mockSubnetsClient)

			v := &DataPlaneIdentitiesPermissionsValidation{}
			result, err := v.getSubnetAttachedResources(context.Background(), subnetResourceID, mockSubnetsClient)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			if tt.wantNatGateway {
				assert.NotNil(t, result.natGatewayResourceID)
			} else {
				assert.Nil(t, result.natGatewayResourceID)
			}
			if tt.wantRouteTable {
				assert.NotNil(t, result.routeTableResourceID)
			} else {
				assert.Nil(t, result.routeTableResourceID)
			}
		})
	}
}

func TestBuildResourceChecks(t *testing.T) {
	subnetResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet"))
	nsgResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.Network/networkSecurityGroups/test-nsg"))
	natGatewayID := "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.Network/natGateways/test-natgw"
	routeTableID := "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.Network/routeTables/test-rt"

	cluster := &coreapi.HCPOpenShiftCluster{
		CustomerProperties: coreapi.HCPOpenShiftClusterCustomerProperties{
			Platform: coreapi.CustomerPlatformProfile{
				SubnetID:               subnetResourceID,
				NetworkSecurityGroupID: nsgResourceID,
			},
		},
	}

	tests := []struct {
		name           string
		setupMock      func(*azureclient.MockSubnetsClient)
		wantCheckCount int
		wantErr        bool
	}{
		{
			name: "subnet with no attached resources produces 3 checks",
			setupMock: func(m *azureclient.MockSubnetsClient) {
				m.EXPECT().Get(gomock.Any(), subnetResourceID.ResourceGroupName, subnetResourceID.Parent.Name, subnetResourceID.Name, nil).
					Return(armnetwork.SubnetsClientGetResponse{
						Subnet: armnetwork.Subnet{
							Properties: &armnetwork.SubnetPropertiesFormat{},
						},
					}, nil)
			},
			wantCheckCount: 3,
			wantErr:        false,
		},
		{
			name: "subnet with NAT gateway and route table produces 5 checks",
			setupMock: func(m *azureclient.MockSubnetsClient) {
				m.EXPECT().Get(gomock.Any(), subnetResourceID.ResourceGroupName, subnetResourceID.Parent.Name, subnetResourceID.Name, nil).
					Return(armnetwork.SubnetsClientGetResponse{
						Subnet: armnetwork.Subnet{
							Properties: &armnetwork.SubnetPropertiesFormat{
								NatGateway: &armnetwork.SubResource{ID: ptr.To(natGatewayID)},
								RouteTable: &armnetwork.RouteTable{ID: ptr.To(routeTableID)},
							},
						},
					}, nil)
			},
			wantCheckCount: 5,
			wantErr:        false,
		},
		{
			name: "subnet Get error returns error",
			setupMock: func(m *azureclient.MockSubnetsClient) {
				m.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), nil).
					Return(armnetwork.SubnetsClientGetResponse{}, fmt.Errorf("subnet not found"))
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockSubnetsClient := azureclient.NewMockSubnetsClient(ctrl)
			tt.setupMock(mockSubnetsClient)

			v := &DataPlaneIdentitiesPermissionsValidation{}
			checks, err := v.buildResourceChecks(context.Background(), cluster, mockSubnetsClient)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Len(t, checks, tt.wantCheckCount)

			assert.Equal(t, resourceTypeNetworkSecurityGroup, checks[0].resourceType)
			assert.Equal(t, nsgResourceID.String(), checks[0].resourceID.String())

			assert.Equal(t, resourceTypeVirtualNetwork, checks[1].resourceType)
			assert.Equal(t, subnetResourceID.Parent.String(), checks[1].resourceID.String())

			assert.Equal(t, resourceTypeSubnet, checks[2].resourceType)
			assert.Equal(t, subnetResourceID.String(), checks[2].resourceID.String())
		})
	}
}

func TestDataPlaneRoleActionsForOperator(t *testing.T) {
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

func TestDataPlaneRoleDataActionsForOperator(t *testing.T) {
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

func TestDataPlaneValidate(t *testing.T) {
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

	subnetGetResponse := armnetwork.SubnetsClientGetResponse{
		Subnet: armnetwork.Subnet{
			Properties: &armnetwork.SubnetPropertiesFormat{},
		},
	}

	tests := []struct {
		name                string
		setupCheckAccess    func(*azureclient.MockCheckAccessV2Client)
		setupUAISOverride   func(*azureclient.MockUserAssignedIdentitiesClient)
		wantOutcome         OutcomeType
		wantInternalMessage string
	}{
		{
			name: "all permissions granted returns passed",
			setupCheckAccess: func(m *azureclient.MockCheckAccessV2Client) {
				// Role actions [networkSecurityGroups/read, virtualNetworks/subnets/join/action]
				// intersect with NSG check (1 action) and Subnet check (1 action); VNet check has empty intersection.
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
		{
			name: "identity Get error returns unknown",
			setupUAISOverride: func(m *azureclient.MockUserAssignedIdentitiesClient) {
				m.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), nil).
					Return(armmsi.UserAssignedIdentitiesClientGetResponse{}, fmt.Errorf("identity not found"))
			},
			wantOutcome: OutcomeTypeUnknown,
		},
		{
			name: "identity with nil PrincipalID returns unknown",
			setupUAISOverride: func(m *azureclient.MockUserAssignedIdentitiesClient) {
				m.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), nil).
					Return(armmsi.UserAssignedIdentitiesClientGetResponse{
						Identity: armmsi.Identity{
							Properties: &armmsi.UserAssignedIdentityProperties{
								PrincipalID: nil,
							},
						},
					}, nil)
			},
			wantOutcome: OutcomeTypeUnknown,
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

			if tt.setupUAISOverride != nil {
				tt.setupUAISOverride(mockUAISClient)
			} else {
				mockUAISClient.EXPECT().Get(gomock.Any(), operatorIdentityResourceID.ResourceGroupName, operatorIdentityResourceID.Name, nil).
					Return(uaisGetResponse, nil)
				mockCachedReader.EXPECT().GetCachedByID(gomock.Any(), roleDefID.String(), nil).Return(roleDefResponse, nil).Times(2)
				tt.setupCheckAccess(mockCheckAccessClient)
			}

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
