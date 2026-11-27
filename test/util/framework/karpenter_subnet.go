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
	"context"
	"fmt"
	"strings"
	"time"

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
)

// CreateKarpenterSubnet adds a subnet to the VNet from the cluster subnet ID.
// The caller must select a free prefix and use a TestContext resource group.
// Resource group cleanup also removes this subnet.
func CreateKarpenterSubnet(ctx context.Context, networkFactory *armnetwork.ClientFactory, clusterSubnetID, subnetName, addressPrefix, nsgID string) (string, error) {
	clusterSubnet, err := azcorearm.ParseResourceID(clusterSubnetID)
	if err != nil {
		return "", fmt.Errorf("failed to parse cluster subnet ID %q: %w", clusterSubnetID, err)
	}
	if !strings.EqualFold(clusterSubnet.ResourceType.String(), "Microsoft.Network/virtualNetworks/subnets") || clusterSubnet.Parent == nil {
		return "", fmt.Errorf("cluster subnet ID %q must identify a VNet subnet", clusterSubnetID)
	}
	if subnetName == "" || strings.Contains(subnetName, "/") || strings.EqualFold(subnetName, clusterSubnet.Name) {
		return "", fmt.Errorf("custom subnet name %q must be a separate subnet from %q", subnetName, clusterSubnet.Name)
	}
	if addressPrefix == "" || nsgID == "" {
		return "", fmt.Errorf("custom subnet %q needs an address prefix and the cluster NSG ID", subnetName)
	}
	subnetID := clusterSubnet.Parent.String() + "/subnets/" + subnetName
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	// Keep the cluster NSG. Do not copy delegations or assigned IP addresses.
	poller, err := networkFactory.NewSubnetsClient().BeginCreateOrUpdate(ctx,
		clusterSubnet.ResourceGroupName, clusterSubnet.Parent.Name, subnetName,
		armnetwork.Subnet{Properties: &armnetwork.SubnetPropertiesFormat{
			AddressPrefix:        to.Ptr(addressPrefix),
			NetworkSecurityGroup: &armnetwork.SecurityGroup{ID: to.Ptr(nsgID)},
		}}, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create custom subnet %q: %w", subnetID, err)
	}
	result, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: StandardPollInterval})
	if err != nil {
		return "", fmt.Errorf("failed to wait for custom subnet %q: %w", subnetID, err)
	}
	if result.ID == nil || !strings.EqualFold(*result.ID, subnetID) {
		return "", fmt.Errorf("Azure returned subnet ID %q; expected custom subnet %q", ptr.Deref(result.ID, ""), subnetID)
	}
	return *result.ID, nil
}

// CheckKarpenterNICSubnet checks the primary IP configuration from Azure.
// A secondary IP configuration cannot hide a primary one on the wrong subnet.
func CheckKarpenterNICSubnet(nic *armnetwork.Interface, expectedSubnetID string) error {
	if expectedSubnetID == "" {
		return fmt.Errorf("expected custom subnet ID must not be empty")
	}
	primary := PrimaryIPConfig(nic)
	if primary == nil || primary.Properties == nil || primary.Properties.Subnet == nil || primary.Properties.Subnet.ID == nil {
		return fmt.Errorf("Azure NIC has no primary IP configuration with a subnet ID; expected %q", expectedSubnetID)
	}
	actualSubnetID := *primary.Properties.Subnet.ID
	if !strings.EqualFold(actualSubnetID, expectedSubnetID) {
		return fmt.Errorf("Azure NIC %q uses subnet %q; expected custom subnet %q", ptr.Deref(nic.ID, ""), actualSubnetID, expectedSubnetID)
	}
	return nil
}
