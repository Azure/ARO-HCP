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
	"net/netip"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// minVnetIntegrationSubnetPrefixLength is the largest IPv4 prefix length (i.e.
// the smallest subnet) permitted for the VNet integration subnet. A /28 yields
// 16 IP addresses; anything smaller (a numerically larger prefix length such as
// /29) does not leave enough addresses for the hosted control plane's private
// router, and the cluster installation fails later with an opaque error.
// Enforcing the minimum here surfaces a clear, actionable failure instead.
const minVnetIntegrationSubnetPrefixLength = 28

// AzureClusterVnetIntegrationSubnetSizeValidation checks that the customer's
// VNet integration subnet meets the minimum size requirement of /28 (16 IP
// addresses).
//
// The subnet's real CIDR is only readable from Azure (via the cluster's
// service-managed identity), which the frontend/admission layer cannot do, so
// this runs as a backend provisioning validation and surfaces the result as a
// cluster status condition rather than a synchronous create-time error.
type AzureClusterVnetIntegrationSubnetSizeValidation struct {
	smiClientBuilder azureclient.ServiceManagedIdentityClientBuilder
}

func NewAzureClusterVnetIntegrationSubnetSizeValidation(smiClientBuilder azureclient.ServiceManagedIdentityClientBuilder) *AzureClusterVnetIntegrationSubnetSizeValidation {
	return &AzureClusterVnetIntegrationSubnetSizeValidation{
		smiClientBuilder: smiClientBuilder,
	}
}

var _ ClusterValidation = (*AzureClusterVnetIntegrationSubnetSizeValidation)(nil)

func (v *AzureClusterVnetIntegrationSubnetSizeValidation) Name() string {
	return "AzureClusterVnetIntegrationSubnetSizeValidation"
}

// Validate fetches the VNet integration subnet from Azure and fails when its
// IPv4 prefix is smaller than the minimum (/28). Clusters without a VNet
// integration subnet (legacy / non-SWIFT) are skipped.
func (v *AzureClusterVnetIntegrationSubnetSizeValidation) Validate(ctx context.Context, _ *coreapi.Subscription, cluster *coreapi.Cluster) ValidationResult {
	vnetIntegrationSubnetID := cluster.CustomerProperties.Platform.VnetIntegrationSubnetID
	// The VNet integration subnet only exists for SWIFT clusters. Legacy /
	// non-SWIFT clusters (e.g. 2024-06-10-preview) have no such subnet, so there
	// is nothing to size-check.
	if vnetIntegrationSubnetID == nil {
		return SkippedValidation(
			"NotApplicable",
			"Cluster has no VNet integration subnet to validate.",
			"Cluster has no VNet integration subnet configured.",
		)
	}
	if vnetIntegrationSubnetID.Parent == nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify VNet integration subnet size.",
			fmt.Sprintf("vnet integration subnet %q has no parent virtual network", vnetIntegrationSubnetID.String()),
			ControllerReportingPolicyTypeError,
		)
	}

	subnetsClient, err := v.smiClientBuilder.SubnetsClient(
		ctx,
		cluster.ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL,
		cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity,
		cluster.ID.SubscriptionID,
	)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify VNet integration subnet size.",
			fmt.Sprintf("failed to get subnets client as service managed identity: %v", err),
			ControllerReportingPolicyTypeError,
		)
	}

	resp, err := subnetsClient.Get(ctx, vnetIntegrationSubnetID.ResourceGroupName, vnetIntegrationSubnetID.Parent.Name, vnetIntegrationSubnetID.Name, nil)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify VNet integration subnet size.",
			fmt.Sprintf("failed to get vnet integration subnet %q: %v", vnetIntegrationSubnetID.String(), err),
			ControllerReportingPolicyTypeError,
		)
	}

	prefixes, err := vnetIntegrationSubnetAddressPrefixes(&resp.Subnet)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify VNet integration subnet size.",
			fmt.Sprintf("failed to read address prefixes for vnet integration subnet %q: %v", vnetIntegrationSubnetID.String(), err),
			ControllerReportingPolicyTypeError,
		)
	}

	// Only the IPv4 prefix governs the usable address count the private router
	// needs, so the size check is applied to the IPv4 prefix.
	for _, prefix := range prefixes {
		if !prefix.Addr().Is4() {
			continue
		}
		if prefix.Bits() > minVnetIntegrationSubnetPrefixLength {
			currentIPs := 1 << (32 - prefix.Bits())
			minIPs := 1 << (32 - minVnetIntegrationSubnetPrefixLength)
			msg := fmt.Sprintf(
				"VNet integration subnet size requirement not met. The subnet %q is /%d (%d IP addresses), but must have a minimum size of /%d (%d IP addresses). Please verify your subnet configuration and ensure it meets the minimum size requirement.",
				vnetIntegrationSubnetID.String(), prefix.Bits(), currentIPs, minVnetIntegrationSubnetPrefixLength, minIPs,
			)
			return FailedValidation("AzureVnetIntegrationSubnetTooSmall", msg, msg)
		}
	}

	return PassedValidation(
		coreapi.ControllerConditionReasonAsExpected,
		"VNet integration subnet meets the minimum size requirement.",
		"VNet integration subnet meets the minimum size requirement.",
	)
}

// vnetIntegrationSubnetAddressPrefixes returns the parsed address prefixes for a
// subnet. Azure populates either AddressPrefix (singular) or AddressPrefixes
// (plural).
func vnetIntegrationSubnetAddressPrefixes(subnet *armnetwork.Subnet) ([]netip.Prefix, error) {
	if subnet.Properties == nil {
		return nil, utils.TrackError(fmt.Errorf("subnet %q has no properties", ptr.Deref(subnet.ID, "")))
	}
	var rawPrefixes []string
	for _, p := range subnet.Properties.AddressPrefixes {
		if p != nil {
			rawPrefixes = append(rawPrefixes, *p)
		}
	}
	if len(rawPrefixes) == 0 && subnet.Properties.AddressPrefix != nil {
		rawPrefixes = append(rawPrefixes, *subnet.Properties.AddressPrefix)
	}
	if len(rawPrefixes) == 0 {
		return nil, utils.TrackError(fmt.Errorf("subnet %q has no address prefix", ptr.Deref(subnet.ID, "")))
	}
	prefixes, err := parsePrefixes(rawPrefixes, "subnet")
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to parse address prefixes for subnet %q: %w", ptr.Deref(subnet.ID, ""), err))
	}
	return prefixes, nil
}
