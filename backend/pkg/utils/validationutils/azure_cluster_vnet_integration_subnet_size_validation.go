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
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/netip"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

const (
	// vnetIntegrationSubnetMinIPs is the minimum number of usable IPs required
	// for the vnet-integration subnet.
	vnetIntegrationSubnetMinIPs = 6

	// azureReservedIPAddressesCountPerSubnet is the number of IP addresses Azure
	// reserves per subnet (network address, default gateway, 2 DNS, broadcast).
	// See https://learn.microsoft.com/en-us/azure/virtual-network/virtual-networks-faq
	azureReservedIPAddressesCountPerSubnet = 5
)

// AzureClusterVnetIntegrationSubnetSizeValidation validates that the vnet-integration
// subnet has enough usable IP addresses. Azure reserves 5 IPs per subnet, so the
// subnet must be large enough to provide at least vnetIntegrationSubnetMinIPs
// usable IPs after the reserved addresses are subtracted.
type AzureClusterVnetIntegrationSubnetSizeValidation struct {
	smiClientBuilder azureclient.ServiceManagedIdentityClientBuilder
}

var _ ClusterValidation = (*AzureClusterVnetIntegrationSubnetSizeValidation)(nil)

func NewAzureClusterVnetIntegrationSubnetSizeValidation(
	smiClientBuilder azureclient.ServiceManagedIdentityClientBuilder,
) *AzureClusterVnetIntegrationSubnetSizeValidation {
	return &AzureClusterVnetIntegrationSubnetSizeValidation{
		smiClientBuilder: smiClientBuilder,
	}
}

func (v *AzureClusterVnetIntegrationSubnetSizeValidation) Name() string {
	return "AzureClusterVnetIntegrationSubnetSizeValidation"
}

func (v *AzureClusterVnetIntegrationSubnetSizeValidation) Validate(
	ctx context.Context, clusterSubscription *coreapi.Subscription, cluster *coreapi.Cluster,
) ValidationResult {
	vnetIntegrationSubnetID := cluster.CustomerProperties.Platform.VnetIntegrationSubnetID
	if vnetIntegrationSubnetID == nil {
		return SkippedValidation(
			"NotApplicable",
			"Cluster does not have a vnet-integration subnet configured.",
			"VnetIntegrationSubnetID is nil; cluster does not use vnet-integration.",
		)
	}

	smiResourceID := cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity
	clusterIdentityURL := cluster.ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL
	subnetsClient, err := v.smiClientBuilder.SubnetsClient(ctx, clusterIdentityURL, smiResourceID, cluster.ID.SubscriptionID)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify vnet-integration subnet size.",
			fmt.Sprintf("failed to get subnets client as service managed identity: %v", err),
			ControllerReportingPolicyTypeError,
		)
	}

	subnet, err := getSubnet(ctx, subnetsClient, vnetIntegrationSubnetID)
	if err != nil {
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound {
			msg := fmt.Sprintf(
				"Vnet-integration subnet %q was not found.",
				vnetIntegrationSubnetID.String())
			return FailedValidation("SubnetNotFound", msg, msg)
		}
		return UnknownValidation(
			"InternalError",
			"Unable to verify vnet-integration subnet size.",
			fmt.Sprintf("failed to get vnet-integration subnet %q: %v", vnetIntegrationSubnetID.String(), err),
			ControllerReportingPolicyTypeError,
		)
	}

	prefixes, err := subnetAddressPrefixes(subnet)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify vnet-integration subnet size.",
			fmt.Sprintf("failed to extract address prefixes for vnet-integration subnet %q: %v", vnetIntegrationSubnetID.String(), err),
			ControllerReportingPolicyTypeError,
		)
	}

	totalUsable := totalUsableIPs(prefixes)
	if totalUsable < uint64(vnetIntegrationSubnetMinIPs) {
		internalAndUserMsg := fmt.Sprintf(
			"Vnet-integration subnet %q has %d usable IPs, which is below the minimum of %d."+
				" The subnet must have at least %d usable IPs.",
			vnetIntegrationSubnetID.String(), totalUsable, vnetIntegrationSubnetMinIPs, vnetIntegrationSubnetMinIPs)
		return FailedValidation("VnetIntegrationSubnetTooSmall", internalAndUserMsg, internalAndUserMsg)
	}

	internalAndUserMsg := fmt.Sprintf(
		"Vnet-integration subnet %q has %d usable IPs (minimum %d).",
		vnetIntegrationSubnetID.String(), totalUsable, vnetIntegrationSubnetMinIPs)
	return PassedValidation(coreapi.ControllerConditionReasonAsExpected, internalAndUserMsg, internalAndUserMsg)
}

// totalUsableIPs calculates the total usable IPs across all address prefixes.
// Azure reserves 5 IPs per subnet (network address, default gateway, 2 DNS, broadcast).
// See https://learn.microsoft.com/en-us/azure/virtual-network/virtual-networks-faq
// For each prefix, usable = max(0, 2^hostBits - 5).
func totalUsableIPs(prefixes []netip.Prefix) uint64 {
	var total uint64
	for _, p := range prefixes {
		hostBits := p.Addr().BitLen() - p.Bits()
		if hostBits <= 0 {
			continue
		}
		var usable uint64
		switch {
		case hostBits == 64:
			usable = math.MaxUint64 - (azureReservedIPAddressesCountPerSubnet - 1)
		case hostBits > 64:
			usable = math.MaxUint64 // exact count cannot fit in uint64
		default:
			totalIPs := uint64(1) << uint(hostBits)
			if totalIPs <= azureReservedIPAddressesCountPerSubnet {
				continue
			}
			usable = totalIPs - azureReservedIPAddressesCountPerSubnet
		}
		if math.MaxUint64-total < usable {
			return math.MaxUint64
		}
		total += usable
	}
	return total
}
