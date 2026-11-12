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

	corev1 "k8s.io/api/core/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
)

// AzureVMCoordinates holds the Azure resource coordinates of the virtual
// machine backing a Kubernetes Node.
type AzureVMCoordinates struct {
	ResourceGroup string
	VMName        string
}

// ParseNodeProviderID extracts the resource group and VM name from a
// Node's spec.providerID. The expected providerID form is:
//
//	azure:///subscriptions/<sub>/resourceGroups/<rg>/providers/Microsoft.Compute/virtualMachines/<name>
func ParseNodeProviderID(providerID string) (AzureVMCoordinates, error) {
	const prefix = "azure:///"
	if !strings.HasPrefix(providerID, prefix) {
		return AzureVMCoordinates{}, fmt.Errorf("providerID %q does not start with %q", providerID, prefix)
	}

	resourceID, err := azcorearm.ParseResourceID("/" + strings.TrimPrefix(providerID, prefix))
	if err != nil {
		return AzureVMCoordinates{}, fmt.Errorf("failed to parse resource ID from providerID %q: %w", providerID, err)
	}
	if !strings.EqualFold(resourceID.ResourceType.String(), "Microsoft.Compute/virtualMachines") {
		return AzureVMCoordinates{}, fmt.Errorf("providerID %q is not a virtual machine resource, got type %q", providerID, resourceID.ResourceType.String())
	}

	return AzureVMCoordinates{
		ResourceGroup: resourceID.ResourceGroupName,
		VMName:        resourceID.Name,
	}, nil
}

// ResolveNodePrimaryNIC fetches the primary Azure network interface for
// a Node by looking up the backing virtual machine and reading its
// network profile.
func ResolveNodePrimaryNIC(ctx context.Context, computeFactory *armcompute.ClientFactory, networkFactory *armnetwork.ClientFactory, node *corev1.Node) (*armnetwork.Interface, error) {
	coords, err := ParseNodeProviderID(node.Spec.ProviderID)
	if err != nil {
		return nil, fmt.Errorf("failed to parse providerID for node %s: %w", node.Name, err)
	}

	vmResp, err := computeFactory.NewVirtualMachinesClient().Get(ctx, coords.ResourceGroup, coords.VMName, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get virtual machine %s for node %s: %w", coords.VMName, node.Name, err)
	}
	if vmResp.Properties == nil || vmResp.Properties.NetworkProfile == nil || len(vmResp.Properties.NetworkProfile.NetworkInterfaces) == 0 {
		return nil, fmt.Errorf("virtual machine %s for node %s has no network interfaces", coords.VMName, node.Name)
	}

	nicRef := vmResp.Properties.NetworkProfile.NetworkInterfaces[0]
	for _, candidate := range vmResp.Properties.NetworkProfile.NetworkInterfaces {
		if candidate != nil && candidate.Properties != nil && candidate.Properties.Primary != nil && *candidate.Properties.Primary {
			nicRef = candidate
			break
		}
	}
	if nicRef == nil || nicRef.ID == nil {
		return nil, fmt.Errorf("virtual machine %s for node %s has no usable network interface reference", coords.VMName, node.Name)
	}

	nicResourceID, err := azcorearm.ParseResourceID(*nicRef.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to parse network interface resource ID %q for node %s: %w", *nicRef.ID, node.Name, err)
	}

	resp, err := networkFactory.NewInterfacesClient().Get(ctx, nicResourceID.ResourceGroupName, nicResourceID.Name, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get network interface %q for node %s: %w", nicResourceID.Name, node.Name, err)
	}
	return &resp.Interface, nil
}

// PrimaryIPConfig returns the IP configuration marked primary on nic, or its
// first IP configuration if none is marked primary. Returns nil if nic has
// no IP configurations.
func PrimaryIPConfig(nic *armnetwork.Interface) *armnetwork.InterfaceIPConfiguration {
	if nic == nil || nic.Properties == nil || len(nic.Properties.IPConfigurations) == 0 {
		return nil
	}
	for _, cfg := range nic.Properties.IPConfigurations {
		if cfg != nil && cfg.Properties != nil && cfg.Properties.Primary != nil && *cfg.Properties.Primary {
			return cfg
		}
	}
	return nic.Properties.IPConfigurations[0]
}

// NICHasPrivateIP reports whether nic has an IP configuration (of any kind,
// primary or secondary) whose private IP address equals ip.
func NICHasPrivateIP(nic *armnetwork.Interface, ip string) bool {
	if nic == nil || nic.Properties == nil {
		return false
	}
	for _, cfg := range nic.Properties.IPConfigurations {
		if cfg == nil || cfg.Properties == nil || cfg.Properties.PrivateIPAddress == nil {
			continue
		}
		if *cfg.Properties.PrivateIPAddress == ip {
			return true
		}
	}
	return false
}

// PickAvailableIPInNodeSubnet returns a private IP address Azure reports
// as available in the subnet backing node's primary NIC.
func PickAvailableIPInNodeSubnet(ctx context.Context, computeFactory *armcompute.ClientFactory, networkFactory *armnetwork.ClientFactory, node *corev1.Node) (string, error) {
	nic, err := ResolveNodePrimaryNIC(ctx, computeFactory, networkFactory, node)
	if err != nil {
		return "", fmt.Errorf("failed to resolve network interface for node %s: %w", node.Name, err)
	}

	primary := PrimaryIPConfig(nic)
	if primary == nil || primary.Properties == nil || primary.Properties.PrivateIPAddress == nil {
		return "", fmt.Errorf("network interface for node %s has no primary private IP address", node.Name)
	}
	if primary.Properties.Subnet == nil || primary.Properties.Subnet.ID == nil {
		return "", fmt.Errorf("network interface for node %s has no subnet reference", node.Name)
	}

	subnetResourceID, err := azcorearm.ParseResourceID(*primary.Properties.Subnet.ID)
	if err != nil {
		return "", fmt.Errorf("failed to parse subnet resource ID %q for node %s: %w", *primary.Properties.Subnet.ID, node.Name, err)
	}
	if subnetResourceID.Parent == nil {
		return "", fmt.Errorf("subnet resource ID %q for node %s has no parent virtual network", *primary.Properties.Subnet.ID, node.Name)
	}
	vnetName := subnetResourceID.Parent.Name
	vnetResourceGroup := subnetResourceID.ResourceGroupName

	// Probe with the NIC's own assigned IP (guaranteed unavailable) so Azure
	// always returns real suggestions in AvailableIPAddresses.
	probeIP := *primary.Properties.PrivateIPAddress
	availResp, err := networkFactory.NewVirtualNetworksClient().CheckIPAddressAvailability(ctx, vnetResourceGroup, vnetName, probeIP, nil)
	if err != nil {
		return "", fmt.Errorf("failed to check IP address availability for %s in %s/%s: %w", probeIP, vnetResourceGroup, vnetName, err)
	}
	for _, suggestion := range availResp.AvailableIPAddresses {
		if suggestion != nil && *suggestion != "" {
			return *suggestion, nil
		}
	}
	return "", fmt.Errorf("no available IP address suggestions returned for subnet of node %s (probed with %s)", node.Name, probeIP)
}
