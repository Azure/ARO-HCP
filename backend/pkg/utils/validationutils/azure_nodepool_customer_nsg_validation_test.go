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
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

type fakeSMIClientBuilder struct {
	nsgClient     azureclient.NetworkSecurityGroupsClient
	subnetsClient azureclient.SubnetsClient
	err           error

	nsgClientRequests int
}

func (f *fakeSMIClientBuilder) BuilderType() azureclient.ServiceManagedIdentityClientBuilderType {
	return azureclient.ServiceManagedIdentityClientBuilderTypeValue
}

func (f *fakeSMIClientBuilder) UserAssignedIdentitiesClient(context.Context, string, *azcorearm.ResourceID, string) (azureclient.UserAssignedIdentitiesClient, error) {
	return nil, nil
}

func (f *fakeSMIClientBuilder) NetworkSecurityGroupsClient(context.Context, string, *azcorearm.ResourceID, string) (azureclient.NetworkSecurityGroupsClient, error) {
	f.nsgClientRequests++
	if f.err != nil {
		return nil, f.err
	}
	return f.nsgClient, nil
}

func (f *fakeSMIClientBuilder) SubnetsClient(context.Context, string, *azcorearm.ResourceID, string) (azureclient.SubnetsClient, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.subnetsClient, nil
}

func testCluster() *api.HCPOpenShiftCluster {
	cluster := api.MinimumValidClusterTestCase()
	cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity = api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + api.TestSubscriptionID + "/resourceGroups/" + api.TestResourceGroupName +
			"/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-smi",
	))
	return cluster
}

func testNodePool() *api.HCPOpenShiftClusterNodePool {
	nodePool := api.NewDefaultHCPOpenShiftClusterNodePool(api.Must(azcorearm.ParseResourceID(api.TestNodePoolResourceID)), api.TestLocation)
	nodePool.Properties.Platform.SubnetID = api.Must(azcorearm.ParseResourceID(api.TestSubnetResourceID))
	return nodePool
}

func testSubscription() *arm.Subscription {
	return &arm.Subscription{
		Properties: &arm.SubscriptionProperties{
			TenantId: ptr.To(api.TestTenantID),
		},
	}
}

func subnetWithNSG(addressPrefix, nsgResourceID string) armnetwork.SubnetsClientGetResponse {
	return armnetwork.SubnetsClientGetResponse{
		Subnet: armnetwork.Subnet{
			Properties: &armnetwork.SubnetPropertiesFormat{
				AddressPrefix: ptr.To(addressPrefix),
				NetworkSecurityGroup: &armnetwork.SecurityGroup{
					ID: ptr.To(nsgResourceID),
				},
			},
		},
	}
}

func subnetWithoutNSG(addressPrefix string) armnetwork.SubnetsClientGetResponse {
	return armnetwork.SubnetsClientGetResponse{
		Subnet: armnetwork.Subnet{
			Properties: &armnetwork.SubnetPropertiesFormat{
				AddressPrefix: ptr.To(addressPrefix),
			},
		},
	}
}

func emptyNSG() armnetwork.SecurityGroupsClientGetResponse {
	return armnetwork.SecurityGroupsClientGetResponse{
		SecurityGroup: armnetwork.SecurityGroup{
			Properties: &armnetwork.SecurityGroupPropertiesFormat{
				SecurityRules: []*armnetwork.SecurityRule{},
			},
		},
	}
}

func expectWorkerAndIntegrationSubnets(subnets *azureclient.MockSubnetsClient, workerResp, integrationResp armnetwork.SubnetsClientGetResponse) {
	subnets.EXPECT().Get(gomock.Any(), api.TestResourceGroupName, api.TestVirtualNetworkName, api.TestSubnetName, nil).Return(workerResp, nil)
	subnets.EXPECT().Get(gomock.Any(), api.TestResourceGroupName, api.TestVirtualNetworkName, api.TestVnetIntegrationSubnetName, nil).Return(integrationResp, nil)
}

func TestAzureCustomerNSGValidation(t *testing.T) {
	t.Parallel()
	const workerSubnet = "10.0.0.0/24"
	const integrationSubnet = "10.0.1.0/24"
	const integrationNSGName = "test-integration-nsg"
	integrationNSGID := "/subscriptions/" + api.TestSubscriptionID + "/resourceGroups/" + api.TestResourceGroupName +
		"/providers/Microsoft.Network/networkSecurityGroups/" + integrationNSGName

	t.Run("Deny Any to Any fails", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		subnets := azureclient.NewMockSubnetsClient(ctrl)
		subnets.EXPECT().Get(gomock.Any(), api.TestResourceGroupName, api.TestVirtualNetworkName, api.TestSubnetName, nil).Return(
			subnetWithNSG(workerSubnet, api.TestNetworkSecurityGroupResourceID), nil,
		)
		nsg := azureclient.NewMockNetworkSecurityGroupsClient(ctrl)
		nsg.EXPECT().Get(gomock.Any(), api.TestResourceGroupName, api.TestNetworkSecurityGroupName, nil).Return(
			armnetwork.SecurityGroupsClientGetResponse{
				SecurityGroup: armnetwork.SecurityGroup{
					Properties: &armnetwork.SecurityGroupPropertiesFormat{
						SecurityRules: []*armnetwork.SecurityRule{{
							Name: ptr.To("DenyAnyAny"),
							Properties: &armnetwork.SecurityRulePropertiesFormat{
								Priority:                 ptr.To(int32(200)),
								Access:                   ptr.To(armnetwork.SecurityRuleAccessDeny),
								Direction:                ptr.To(armnetwork.SecurityRuleDirectionOutbound),
								Protocol:                 ptr.To(armnetwork.SecurityRuleProtocolAsterisk),
								SourceAddressPrefix:      ptr.To("*"),
								DestinationAddressPrefix: ptr.To("*"),
								DestinationPortRange:     ptr.To("*"),
							},
						}},
					},
				},
			}, nil,
		)

		v := UserProvidedNodePoolNetworkSecurityGroupValidation(&fakeSMIClientBuilder{nsgClient: nsg, subnetsClient: subnets})
		err := v.Validate(context.Background(), testCluster(), testSubscription(), testNodePool())
		require.Error(t, err)
		require.Contains(t, err.Error(), "DenyAnyAny")
	})

	t.Run("Deny to IP in subnet fails without Allow", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		subnets := azureclient.NewMockSubnetsClient(ctrl)
		subnets.EXPECT().Get(gomock.Any(), api.TestResourceGroupName, api.TestVirtualNetworkName, api.TestSubnetName, nil).Return(
			subnetWithNSG(workerSubnet, api.TestNetworkSecurityGroupResourceID), nil,
		)
		nsg := azureclient.NewMockNetworkSecurityGroupsClient(ctrl)
		nsg.EXPECT().Get(gomock.Any(), api.TestResourceGroupName, api.TestNetworkSecurityGroupName, nil).Return(
			armnetwork.SecurityGroupsClientGetResponse{
				SecurityGroup: armnetwork.SecurityGroup{
					Properties: &armnetwork.SecurityGroupPropertiesFormat{
						SecurityRules: []*armnetwork.SecurityRule{{
							Name: ptr.To("DenyILB"),
							Properties: &armnetwork.SecurityRulePropertiesFormat{
								Priority:                 ptr.To(int32(110)),
								Access:                   ptr.To(armnetwork.SecurityRuleAccessDeny),
								Direction:                ptr.To(armnetwork.SecurityRuleDirectionOutbound),
								Protocol:                 ptr.To(armnetwork.SecurityRuleProtocolTCP),
								SourceAddressPrefix:      ptr.To("*"),
								DestinationAddressPrefix: ptr.To("10.0.0.4"),
								DestinationPortRanges:    []*string{ptr.To("443"), ptr.To("6443")},
							},
						}},
					},
				},
			}, nil,
		)

		v := UserProvidedNodePoolNetworkSecurityGroupValidation(&fakeSMIClientBuilder{nsgClient: nsg, subnetsClient: subnets})
		err := v.Validate(context.Background(), testCluster(), testSubscription(), testNodePool())
		require.Error(t, err)
		require.Contains(t, err.Error(), "DenyILB")
	})

	t.Run("empty security rules on attached NSGs", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		subnets := azureclient.NewMockSubnetsClient(ctrl)
		expectWorkerAndIntegrationSubnets(subnets,
			subnetWithNSG(workerSubnet, api.TestNetworkSecurityGroupResourceID),
			subnetWithNSG(integrationSubnet, api.TestNetworkSecurityGroupResourceID),
		)
		nsg := azureclient.NewMockNetworkSecurityGroupsClient(ctrl)
		nsg.EXPECT().Get(gomock.Any(), api.TestResourceGroupName, api.TestNetworkSecurityGroupName, nil).Return(emptyNSG(), nil).Times(2)

		v := UserProvidedNodePoolNetworkSecurityGroupValidation(&fakeSMIClientBuilder{nsgClient: nsg, subnetsClient: subnets})
		require.NoError(t, v.Validate(context.Background(), testCluster(), testSubscription(), testNodePool()))
	})

	t.Run("skips validation when vnet-integration subnet has no NSG attached", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		subnets := azureclient.NewMockSubnetsClient(ctrl)
		expectWorkerAndIntegrationSubnets(subnets,
			subnetWithNSG(workerSubnet, api.TestNetworkSecurityGroupResourceID),
			subnetWithoutNSG(integrationSubnet),
		)
		nsg := azureclient.NewMockNetworkSecurityGroupsClient(ctrl)
		nsg.EXPECT().Get(gomock.Any(), api.TestResourceGroupName, api.TestNetworkSecurityGroupName, nil).Return(emptyNSG(), nil)
		builder := &fakeSMIClientBuilder{nsgClient: nsg, subnetsClient: subnets}

		v := UserProvidedNodePoolNetworkSecurityGroupValidation(builder)
		require.NoError(t, v.Validate(context.Background(), testCluster(), testSubscription(), testNodePool()))
		require.Equal(t, 1, builder.nsgClientRequests, "NSG client should be created for worker NSG only")
	})

	t.Run("inbound Deny on integration NSG fails", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		subnets := azureclient.NewMockSubnetsClient(ctrl)
		expectWorkerAndIntegrationSubnets(subnets,
			subnetWithoutNSG(workerSubnet),
			subnetWithNSG(integrationSubnet, integrationNSGID),
		)
		nsg := azureclient.NewMockNetworkSecurityGroupsClient(ctrl)
		nsg.EXPECT().Get(gomock.Any(), api.TestResourceGroupName, integrationNSGName, nil).Return(
			armnetwork.SecurityGroupsClientGetResponse{
				SecurityGroup: armnetwork.SecurityGroup{
					Properties: &armnetwork.SecurityGroupPropertiesFormat{
						SecurityRules: []*armnetwork.SecurityRule{{
							Name: ptr.To("DenyAnyAnyIn"),
							Properties: &armnetwork.SecurityRulePropertiesFormat{
								Priority:                 ptr.To(int32(200)),
								Access:                   ptr.To(armnetwork.SecurityRuleAccessDeny),
								Direction:                ptr.To(armnetwork.SecurityRuleDirectionInbound),
								Protocol:                 ptr.To(armnetwork.SecurityRuleProtocolAsterisk),
								SourceAddressPrefix:      ptr.To("*"),
								DestinationAddressPrefix: ptr.To("*"),
								DestinationPortRange:     ptr.To("*"),
							},
						}},
					},
				},
			}, nil,
		)

		v := UserProvidedNodePoolNetworkSecurityGroupValidation(&fakeSMIClientBuilder{nsgClient: nsg, subnetsClient: subnets})
		err := v.Validate(context.Background(), testCluster(), testSubscription(), testNodePool())
		require.Error(t, err)
		require.Contains(t, err.Error(), "DenyAnyAnyIn")
		require.Contains(t, err.Error(), "inbound")
	})

	t.Run("inbound Deny Any to vnet-integration subnet fails", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		subnets := azureclient.NewMockSubnetsClient(ctrl)
		expectWorkerAndIntegrationSubnets(subnets,
			subnetWithoutNSG(workerSubnet),
			subnetWithNSG(integrationSubnet, integrationNSGID),
		)
		nsg := azureclient.NewMockNetworkSecurityGroupsClient(ctrl)
		nsg.EXPECT().Get(gomock.Any(), api.TestResourceGroupName, integrationNSGName, nil).Return(
			armnetwork.SecurityGroupsClientGetResponse{
				SecurityGroup: armnetwork.SecurityGroup{
					Properties: &armnetwork.SecurityGroupPropertiesFormat{
						SecurityRules: []*armnetwork.SecurityRule{{
							Name: ptr.To("DenyAnyToIntegration"),
							Properties: &armnetwork.SecurityRulePropertiesFormat{
								Priority:                 ptr.To(int32(200)),
								Access:                   ptr.To(armnetwork.SecurityRuleAccessDeny),
								Direction:                ptr.To(armnetwork.SecurityRuleDirectionInbound),
								Protocol:                 ptr.To(armnetwork.SecurityRuleProtocolTCP),
								SourceAddressPrefix:      ptr.To("*"),
								DestinationAddressPrefix: ptr.To(integrationSubnet),
								DestinationPortRanges:    []*string{ptr.To("443"), ptr.To("6443")},
							},
						}},
					},
				},
			}, nil,
		)

		v := UserProvidedNodePoolNetworkSecurityGroupValidation(&fakeSMIClientBuilder{nsgClient: nsg, subnetsClient: subnets})
		err := v.Validate(context.Background(), testCluster(), testSubscription(), testNodePool())
		require.Error(t, err)
		require.Contains(t, err.Error(), "DenyAnyToIntegration")
		require.Contains(t, err.Error(), "inbound")
	})

	t.Run("different NSGs: outbound on worker NSG and inbound on integration NSG", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		subnets := azureclient.NewMockSubnetsClient(ctrl)
		expectWorkerAndIntegrationSubnets(subnets,
			subnetWithNSG(workerSubnet, api.TestNetworkSecurityGroupResourceID),
			subnetWithNSG(integrationSubnet, integrationNSGID),
		)
		nsg := azureclient.NewMockNetworkSecurityGroupsClient(ctrl)
		nsg.EXPECT().Get(gomock.Any(), api.TestResourceGroupName, api.TestNetworkSecurityGroupName, nil).Return(emptyNSG(), nil)
		nsg.EXPECT().Get(gomock.Any(), api.TestResourceGroupName, integrationNSGName, nil).Return(
			armnetwork.SecurityGroupsClientGetResponse{
				SecurityGroup: armnetwork.SecurityGroup{
					Properties: &armnetwork.SecurityGroupPropertiesFormat{
						SecurityRules: []*armnetwork.SecurityRule{
							{
								Name: ptr.To("AllowAnyAny"),
								Properties: &armnetwork.SecurityRulePropertiesFormat{
									Priority:                 ptr.To(int32(100)),
									Access:                   ptr.To(armnetwork.SecurityRuleAccessAllow),
									Direction:                ptr.To(armnetwork.SecurityRuleDirectionInbound),
									Protocol:                 ptr.To(armnetwork.SecurityRuleProtocolAsterisk),
									SourceAddressPrefix:      ptr.To("*"),
									DestinationAddressPrefix: ptr.To("*"),
									DestinationPortRange:     ptr.To("*"),
								},
							},
							{
								Name: ptr.To("DenyWorkerToAny"),
								Properties: &armnetwork.SecurityRulePropertiesFormat{
									Priority:                 ptr.To(int32(200)),
									Access:                   ptr.To(armnetwork.SecurityRuleAccessDeny),
									Direction:                ptr.To(armnetwork.SecurityRuleDirectionInbound),
									Protocol:                 ptr.To(armnetwork.SecurityRuleProtocolTCP),
									SourceAddressPrefix:      ptr.To(workerSubnet),
									DestinationAddressPrefix: ptr.To("*"),
									DestinationPortRange:     ptr.To("*"),
								},
							},
						},
					},
				},
			}, nil,
		)

		v := UserProvidedNodePoolNetworkSecurityGroupValidation(&fakeSMIClientBuilder{nsgClient: nsg, subnetsClient: subnets})
		require.NoError(t, v.Validate(context.Background(), testCluster(), testSubscription(), testNodePool()))
	})
}
