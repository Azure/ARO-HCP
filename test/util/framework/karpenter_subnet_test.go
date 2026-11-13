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

package framework

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/utils/ptr"

	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
)

func TestCreateKarpenterSubnet(t *testing.T) {
	const vnetID = "/subscriptions/" + fakeSubscriptionID + "/resourceGroups/customer-rg/providers/Microsoft.Network/virtualNetworks/deployment-vnet"
	const clusterSubnetID = vnetID + "/subnets/cluster-default"
	const customSubnetID = vnetID + "/subnets/karpenter-custom"
	const nsgID = "/subscriptions/" + fakeSubscriptionID + "/resourceGroups/customer-rg/providers/Microsoft.Network/networkSecurityGroups/cluster-nsg"

	for _, scenario := range []struct {
		name, clusterID, subnetName, returnedID, wantError string
		status                                             int
	}{
		{name: "same VNet", clusterID: clusterSubnetID, subnetName: "karpenter-custom", returnedID: customSubnetID, status: http.StatusOK},
		{name: "wrong returned subnet", clusterID: clusterSubnetID, subnetName: "karpenter-custom", returnedID: clusterSubnetID, status: http.StatusOK, wantError: "expected custom subnet"},
		{name: "Azure rejects subnet", clusterID: clusterSubnetID, subnetName: "karpenter-custom", status: http.StatusForbidden, wantError: "failed to create custom subnet"},
		{name: "default name", clusterID: clusterSubnetID, subnetName: "CLUSTER-DEFAULT", wantError: "must be a separate subnet"},
		{name: "empty name", clusterID: clusterSubnetID, wantError: "must be a separate subnet"},
		{name: "nested name", clusterID: clusterSubnetID, subnetName: "nested/subnet", wantError: "must be a separate subnet"},
		{name: "VNet instead of subnet", clusterID: vnetID, subnetName: "karpenter-custom", wantError: "must identify a VNet subnet"},
		{name: "invalid ID", clusterID: "invalid", subnetName: "karpenter-custom", wantError: "failed to parse cluster subnet ID"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			calls := 0
			transport := &fakeTransport{do: func(req *http.Request) (*http.Response, error) {
				calls++
				assert.Equal(t, http.MethodPut, req.Method, "the helper must create only the new subnet")
				assert.Equal(t, customSubnetID, req.URL.Path, "the subnet must use the VNet from the deployment ID")
				var payload armnetwork.Subnet
				require.NoError(t, json.NewDecoder(req.Body).Decode(&payload), "the subnet request must contain valid JSON")
				require.NotNil(t, payload.Properties, "the subnet request must contain network properties")
				assert.Equal(t, "10.0.2.0/24", ptr.Deref(payload.Properties.AddressPrefix, ""), "the subnet must use the free test prefix")
				require.NotNil(t, payload.Properties.NetworkSecurityGroup, "the custom subnet must use the cluster NSG")
				assert.Equal(t, nsgID, ptr.Deref(payload.Properties.NetworkSecurityGroup.ID, ""), "the custom subnet must preserve the cluster NSG ID")
				assert.Empty(t, payload.Properties.Delegations, "the worker subnet must not inherit the integration subnet delegation")
				body := fmt.Sprintf(`{"id":%q,"properties":{"provisioningState":"Succeeded"}}`, scenario.returnedID)
				if scenario.status != http.StatusOK {
					body = `{"error":{"code":"AuthorizationFailed","message":"subnet create denied"}}`
				}
				return &http.Response{
					StatusCode: scenario.status,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(body)),
					Request:    req,
				}, nil
			}}
			networkFactory, err := armnetwork.NewClientFactory(fakeSubscriptionID, &azfake.TokenCredential{}, fakeClientOptions(transport))
			require.NoError(t, err, "the fake Azure network client must initialize")
			subnetID, err := CreateKarpenterSubnet(t.Context(), networkFactory, scenario.clusterID, scenario.subnetName, "10.0.2.0/24", nsgID)
			if scenario.wantError != "" {
				require.ErrorContains(t, err, scenario.wantError, "the helper must reject an invalid subnet or Azure response")
				assert.Empty(t, subnetID, "a failed create must not return a usable subnet ID")
			} else {
				require.NoError(t, err, "the helper must create a custom subnet in the deployment VNet")
				assert.Equal(t, customSubnetID, subnetID, "the helper must return the Azure subnet ID")
			}
			if scenario.status == 0 {
				assert.Zero(t, calls, "invalid input must not change Azure resources")
			} else {
				assert.Equal(t, 1, calls, "the helper must not change the VNet or default subnet")
			}
		})
	}
}

func TestCheckKarpenterNICSubnet(t *testing.T) {
	const vnetID = "/subscriptions/" + fakeSubscriptionID + "/resourceGroups/customer-rg/providers/Microsoft.Network/virtualNetworks/deployment-vnet"
	const customSubnetID = vnetID + "/subnets/karpenter-custom"
	const defaultSubnetID = vnetID + "/subnets/cluster-default"
	ipConfig := func(subnetID string, primary bool) *armnetwork.InterfaceIPConfiguration {
		return &armnetwork.InterfaceIPConfiguration{Properties: &armnetwork.InterfaceIPConfigurationPropertiesFormat{
			Primary: to.Ptr(primary), Subnet: &armnetwork.Subnet{ID: to.Ptr(subnetID)},
		}}
	}
	for _, scenario := range []struct {
		name      string
		configs   []*armnetwork.InterfaceIPConfiguration
		wantError string
	}{
		{name: "custom subnet", configs: []*armnetwork.InterfaceIPConfiguration{ipConfig(customSubnetID, true)}},
		{name: "case insensitive ID", configs: []*armnetwork.InterfaceIPConfiguration{ipConfig(strings.ToUpper(customSubnetID), true)}},
		{name: "default subnet fails", configs: []*armnetwork.InterfaceIPConfiguration{ipConfig(defaultSubnetID, true)}, wantError: defaultSubnetID},
		{name: "secondary custom subnet cannot hide default primary", configs: []*armnetwork.InterfaceIPConfiguration{ipConfig(customSubnetID, false), ipConfig(defaultSubnetID, true)}, wantError: defaultSubnetID},
		{name: "no IP configurations", wantError: "no primary IP configuration"},
		{name: "missing properties", configs: []*armnetwork.InterfaceIPConfiguration{{}}, wantError: "no primary IP configuration"},
		{name: "missing subnet", configs: []*armnetwork.InterfaceIPConfiguration{{Properties: &armnetwork.InterfaceIPConfigurationPropertiesFormat{}}}, wantError: "no primary IP configuration"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			nic := &armnetwork.Interface{ID: to.Ptr("node-nic"), Properties: &armnetwork.InterfacePropertiesFormat{IPConfigurations: scenario.configs}}
			err := CheckKarpenterNICSubnet(nic, customSubnetID)
			if scenario.wantError != "" {
				require.ErrorContains(t, err, scenario.wantError, "the check must reject a NIC without the custom primary subnet")
				assert.ErrorContains(t, err, customSubnetID, "the failure must state the expected custom subnet")
			} else {
				require.NoError(t, err, "the check must accept the custom subnet ID from Azure")
			}
		})
	}
	require.ErrorContains(t, CheckKarpenterNICSubnet(nil, customSubnetID), "no primary IP configuration", "a missing NIC must fail the subnet check")
	require.ErrorContains(t, CheckKarpenterNICSubnet(nil, ""), "must not be empty", "an empty expected subnet must fail the check")
}
