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
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apitesting/coreapitesting"
)

func testClusterSubnetCluster() *coreapi.Cluster {
	cluster := coreapitesting.MinimumValidClusterTestCase()
	cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity = metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + coreapitesting.TestSubscriptionID + "/resourceGroups/" + coreapitesting.TestResourceGroupName +
			"/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-smi",
	))
	return cluster
}

func testClusterSubnetSubscription() *coreapi.Subscription {
	return &coreapi.Subscription{
		Properties: &coreapi.SubscriptionProperties{
			TenantId: ptr.To(coreapitesting.TestTenantID),
		},
	}
}

func virtualNetworkResponse(location string, subnetNames ...string) armnetwork.VirtualNetworksClientGetResponse {
	vnet := armnetwork.VirtualNetwork{
		Location:   ptr.To(location),
		Properties: &armnetwork.VirtualNetworkPropertiesFormat{},
	}
	for _, name := range subnetNames {
		vnet.Properties.Subnets = append(vnet.Properties.Subnets, &armnetwork.Subnet{
			Name:       ptr.To(name),
			Properties: &armnetwork.SubnetPropertiesFormat{},
		})
	}
	return armnetwork.VirtualNetworksClientGetResponse{VirtualNetwork: vnet}
}

func virtualNetworkResponseWithSubnetNilProperties(location, subnetName string) armnetwork.VirtualNetworksClientGetResponse {
	vnet := armnetwork.VirtualNetwork{
		Location: ptr.To(location),
		Properties: &armnetwork.VirtualNetworkPropertiesFormat{
			Subnets: []*armnetwork.Subnet{{Name: ptr.To(subnetName)}},
		},
	}
	return armnetwork.VirtualNetworksClientGetResponse{VirtualNetwork: vnet}
}

func virtualNetworkResponseWithSubnetNSG(location, subnetName, nsgResourceID string) armnetwork.VirtualNetworksClientGetResponse {
	vnet := armnetwork.VirtualNetwork{
		Location: ptr.To(location),
		Properties: &armnetwork.VirtualNetworkPropertiesFormat{
			Subnets: []*armnetwork.Subnet{
				{
					Name: ptr.To(subnetName),
					Properties: &armnetwork.SubnetPropertiesFormat{
						NetworkSecurityGroup: &armnetwork.SecurityGroup{ID: ptr.To(nsgResourceID)},
					},
				},
			},
		},
	}
	return armnetwork.VirtualNetworksClientGetResponse{VirtualNetwork: vnet}
}

func TestAzureClusterSubnetValidation_Name(t *testing.T) {
	t.Parallel()
	v := NewAzureClusterSubnetValidation(nil)
	assert.Equal(t, "AzureClusterSubnetValidation", v.Name())
}

func TestAzureClusterSubnetValidation(t *testing.T) {
	t.Parallel()

	t.Run("SMI client builder error returns Unknown", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		cluster := testClusterSubnetCluster()

		smiBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
		smiBuilder.EXPECT().VirtualNetworksClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(nil, fmt.Errorf("credential error"))

		v := NewAzureClusterSubnetValidation(smiBuilder)
		result := v.Validate(context.Background(), testClusterSubnetSubscription(), cluster)
		require.Equal(t, OutcomeTypeUnknown, result.Outcome.Type)
		assert.Contains(t, result.InternalMessage(), "credential error")
	})

	t.Run("virtual network Get error returns Unknown", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		cluster := testClusterSubnetCluster()
		vnets := azureclient.NewMockVirtualNetworksClient(ctrl)
		vnets.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(armnetwork.VirtualNetworksClientGetResponse{}, fmt.Errorf("network error"))

		smiBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
		smiBuilder.EXPECT().VirtualNetworksClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(vnets, nil)

		v := NewAzureClusterSubnetValidation(smiBuilder)
		result := v.Validate(context.Background(), testClusterSubnetSubscription(), cluster)
		require.Equal(t, OutcomeTypeUnknown, result.Outcome.Type)
		assert.Contains(t, result.InternalMessage(), "network error")
	})

	t.Run("virtual network NotFound returns Failed", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		cluster := testClusterSubnetCluster()
		vnets := azureclient.NewMockVirtualNetworksClient(ctrl)
		vnets.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(armnetwork.VirtualNetworksClientGetResponse{}, &azcore.ResponseError{StatusCode: http.StatusNotFound})

		smiBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
		smiBuilder.EXPECT().VirtualNetworksClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(vnets, nil)

		v := NewAzureClusterSubnetValidation(smiBuilder)
		result := v.Validate(context.Background(), testClusterSubnetSubscription(), cluster)
		require.Equal(t, OutcomeTypeFailed, result.Outcome.Type)
		assert.Equal(t, "VirtualNetworkNotFound", result.Reason())
		assert.Contains(t, result.Outcome.Failed.UserMessage, "was not found")
	})

	t.Run("virtual network with nil location returns Unknown", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		cluster := testClusterSubnetCluster()
		vnets := azureclient.NewMockVirtualNetworksClient(ctrl)
		vnets.EXPECT().Get(gomock.Any(), coreapitesting.TestResourceGroupName, coreapitesting.TestVirtualNetworkName, nil).
			Return(armnetwork.VirtualNetworksClientGetResponse{
				VirtualNetwork: armnetwork.VirtualNetwork{
					Properties: &armnetwork.VirtualNetworkPropertiesFormat{
						Subnets: []*armnetwork.Subnet{{Name: ptr.To(coreapitesting.TestSubnetName)}},
					},
				},
			}, nil)

		smiBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
		smiBuilder.EXPECT().VirtualNetworksClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(vnets, nil)

		v := NewAzureClusterSubnetValidation(smiBuilder)
		result := v.Validate(context.Background(), testClusterSubnetSubscription(), cluster)
		require.Equal(t, OutcomeTypeUnknown, result.Outcome.Type)
		assert.Contains(t, result.InternalMessage(), "no location")
	})

	t.Run("virtual network location mismatch returns Failed", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		cluster := testClusterSubnetCluster()
		vnets := azureclient.NewMockVirtualNetworksClient(ctrl)
		vnets.EXPECT().Get(gomock.Any(), coreapitesting.TestResourceGroupName, coreapitesting.TestVirtualNetworkName, nil).
			Return(virtualNetworkResponseWithSubnetNSG("eastus", coreapitesting.TestSubnetName, coreapitesting.TestNetworkSecurityGroupResourceID), nil)

		smiBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
		smiBuilder.EXPECT().VirtualNetworksClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(vnets, nil)

		v := NewAzureClusterSubnetValidation(smiBuilder)
		result := v.Validate(context.Background(), testClusterSubnetSubscription(), cluster)
		require.Equal(t, OutcomeTypeFailed, result.Outcome.Type)
		assert.Equal(t, "SubnetValidationFailed", result.Reason())
		assert.Contains(t, result.Outcome.Failed.UserMessage, "is not the same as the cluster's location")
	})

	t.Run("virtual network with nil properties returns Unknown", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		cluster := testClusterSubnetCluster()
		vnets := azureclient.NewMockVirtualNetworksClient(ctrl)
		vnets.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(armnetwork.VirtualNetworksClientGetResponse{
				VirtualNetwork: armnetwork.VirtualNetwork{Location: ptr.To(coreapitesting.TestLocation)},
			}, nil)

		smiBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
		smiBuilder.EXPECT().VirtualNetworksClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(vnets, nil)

		v := NewAzureClusterSubnetValidation(smiBuilder)
		result := v.Validate(context.Background(), testClusterSubnetSubscription(), cluster)
		require.Equal(t, OutcomeTypeUnknown, result.Outcome.Type)
		assert.Contains(t, result.InternalMessage(), "no properties")
	})

	t.Run("subnet not present on virtual network returns Failed", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		cluster := testClusterSubnetCluster()
		vnets := azureclient.NewMockVirtualNetworksClient(ctrl)
		vnets.EXPECT().Get(gomock.Any(), coreapitesting.TestResourceGroupName, coreapitesting.TestVirtualNetworkName, nil).
			Return(virtualNetworkResponse(coreapitesting.TestLocation, "some-other-subnet"), nil)

		smiBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
		smiBuilder.EXPECT().VirtualNetworksClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(vnets, nil)

		v := NewAzureClusterSubnetValidation(smiBuilder)
		result := v.Validate(context.Background(), testClusterSubnetSubscription(), cluster)
		require.Equal(t, OutcomeTypeFailed, result.Outcome.Type)
		assert.Equal(t, "SubnetValidationFailed", result.Reason())
		assert.Contains(t, result.Outcome.Failed.UserMessage, "was not found")
	})

	t.Run("subnet not present and location mismatch are both reported", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		cluster := testClusterSubnetCluster()
		vnets := azureclient.NewMockVirtualNetworksClient(ctrl)
		vnets.EXPECT().Get(gomock.Any(), coreapitesting.TestResourceGroupName, coreapitesting.TestVirtualNetworkName, nil).
			Return(virtualNetworkResponse("eastus", "some-other-subnet"), nil)

		smiBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
		smiBuilder.EXPECT().VirtualNetworksClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(vnets, nil)

		v := NewAzureClusterSubnetValidation(smiBuilder)
		result := v.Validate(context.Background(), testClusterSubnetSubscription(), cluster)
		require.Equal(t, OutcomeTypeFailed, result.Outcome.Type)
		assert.Equal(t, "SubnetValidationFailed", result.Reason())
		assert.Contains(t, result.Outcome.Failed.UserMessage, "was not found")
		assert.Contains(t, result.Outcome.Failed.UserMessage, "is not the same as the cluster's location")
	})

	t.Run("subnet with nil properties returns Unknown", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		cluster := testClusterSubnetCluster()
		vnets := azureclient.NewMockVirtualNetworksClient(ctrl)
		vnets.EXPECT().Get(gomock.Any(), coreapitesting.TestResourceGroupName, coreapitesting.TestVirtualNetworkName, nil).
			Return(virtualNetworkResponseWithSubnetNilProperties(coreapitesting.TestLocation, coreapitesting.TestSubnetName), nil)

		smiBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
		smiBuilder.EXPECT().VirtualNetworksClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(vnets, nil)

		v := NewAzureClusterSubnetValidation(smiBuilder)
		result := v.Validate(context.Background(), testClusterSubnetSubscription(), cluster)
		require.Equal(t, OutcomeTypeUnknown, result.Outcome.Type)
		assert.Contains(t, result.InternalMessage(), "no properties")
	})

	t.Run("subnet without a network security group attached returns Failed", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		cluster := testClusterSubnetCluster()
		vnets := azureclient.NewMockVirtualNetworksClient(ctrl)
		vnets.EXPECT().Get(gomock.Any(), coreapitesting.TestResourceGroupName, coreapitesting.TestVirtualNetworkName, nil).
			Return(virtualNetworkResponse(coreapitesting.TestLocation, coreapitesting.TestSubnetName), nil)

		smiBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
		smiBuilder.EXPECT().VirtualNetworksClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(vnets, nil)

		v := NewAzureClusterSubnetValidation(smiBuilder)
		result := v.Validate(context.Background(), testClusterSubnetSubscription(), cluster)
		require.Equal(t, OutcomeTypeFailed, result.Outcome.Type)
		assert.Equal(t, "SubnetValidationFailed", result.Reason())
		assert.Contains(t, result.Outcome.Failed.UserMessage, "does not have the cluster's network security group")
	})

	t.Run("subnet with a different network security group attached returns Failed", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		cluster := testClusterSubnetCluster()
		vnets := azureclient.NewMockVirtualNetworksClient(ctrl)
		vnets.EXPECT().Get(gomock.Any(), coreapitesting.TestResourceGroupName, coreapitesting.TestVirtualNetworkName, nil).
			Return(virtualNetworkResponseWithSubnetNSG(
				coreapitesting.TestLocation,
				coreapitesting.TestSubnetName,
				"/subscriptions/"+coreapitesting.TestSubscriptionID+"/resourceGroups/"+coreapitesting.TestResourceGroupName+
					"/providers/Microsoft.Network/networkSecurityGroups/some-other-nsg",
			), nil)

		smiBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
		smiBuilder.EXPECT().VirtualNetworksClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(vnets, nil)

		v := NewAzureClusterSubnetValidation(smiBuilder)
		result := v.Validate(context.Background(), testClusterSubnetSubscription(), cluster)
		require.Equal(t, OutcomeTypeFailed, result.Outcome.Type)
		assert.Equal(t, "SubnetValidationFailed", result.Reason())
		assert.Contains(t, result.Outcome.Failed.UserMessage, "some-other-nsg")
		assert.Contains(t, result.Outcome.Failed.UserMessage, "instead of the cluster's network security group")
	})

	t.Run("location mismatch and missing network security group are both reported", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		cluster := testClusterSubnetCluster()
		vnets := azureclient.NewMockVirtualNetworksClient(ctrl)
		vnets.EXPECT().Get(gomock.Any(), coreapitesting.TestResourceGroupName, coreapitesting.TestVirtualNetworkName, nil).
			Return(virtualNetworkResponse("eastus", coreapitesting.TestSubnetName), nil)

		smiBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
		smiBuilder.EXPECT().VirtualNetworksClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(vnets, nil)

		v := NewAzureClusterSubnetValidation(smiBuilder)
		result := v.Validate(context.Background(), testClusterSubnetSubscription(), cluster)
		require.Equal(t, OutcomeTypeFailed, result.Outcome.Type)
		assert.Equal(t, "SubnetValidationFailed", result.Reason())
		assert.Contains(t, result.Outcome.Failed.UserMessage, "is not the same as the cluster's location")
		assert.Contains(t, result.Outcome.Failed.UserMessage, "does not have the cluster's network security group")
	})

	t.Run("subnet present in the same location with the cluster's network security group attached returns Passed", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		cluster := testClusterSubnetCluster()
		vnets := azureclient.NewMockVirtualNetworksClient(ctrl)
		vnets.EXPECT().Get(gomock.Any(), coreapitesting.TestResourceGroupName, coreapitesting.TestVirtualNetworkName, nil).
			Return(virtualNetworkResponseWithSubnetNSG(
				coreapitesting.TestLocation,
				coreapitesting.TestSubnetName,
				coreapitesting.TestNetworkSecurityGroupResourceID,
			), nil)

		smiBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
		smiBuilder.EXPECT().VirtualNetworksClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(vnets, nil)

		v := NewAzureClusterSubnetValidation(smiBuilder)
		result := v.Validate(context.Background(), testClusterSubnetSubscription(), cluster)
		require.Equal(t, OutcomeTypePassed, result.Outcome.Type)
	})
}
