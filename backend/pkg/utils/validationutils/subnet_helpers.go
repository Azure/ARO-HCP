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

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// getSubnet fetches a single subnet from Azure using the SubnetsClient.
func getSubnet(ctx context.Context, subnetsClient azureclient.SubnetsClient, subnetID *azcorearm.ResourceID) (*armnetwork.Subnet, error) {
	if subnetID == nil {
		return nil, utils.TrackError(fmt.Errorf("subnet ID is nil"))
	}
	if subnetID.Parent == nil {
		return nil, utils.TrackError(fmt.Errorf("subnet %q has no parent virtual network", subnetID.String()))
	}
	resp, err := subnetsClient.Get(ctx, subnetID.ResourceGroupName, subnetID.Parent.Name, subnetID.Name, nil)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get subnet %q: %w", subnetID.String(), err))
	}
	if resp.Properties == nil {
		return nil, utils.TrackError(fmt.Errorf("subnet %q has no properties", subnetID.String()))
	}
	return &resp.Subnet, nil
}

// ReasonSubnetReadPermissionDenied is the ValidationResult reason used when the
// cluster's service-managed identity (SMI) is denied
// Microsoft.Network/virtualNetworks/subnets/read on the cluster subnet. It is a
// terminal, customer-fixable misconfiguration (not a transient error).
const ReasonSubnetReadPermissionDenied = "SubnetReadPermissionDenied"

// isSubnetReadPermissionDenied reports whether err is a definite Azure authorization
// failure (403 / AuthorizationFailed) from reading the subnet as the SMI. Only this
// case is treated specially; all other errors are handled by the caller as before.
func isSubnetReadPermissionDenied(err error) bool {
	return azureclient.IsAzureAuthorizationFailedError(err)
}

// subnetReadPermissionDeniedResult builds the terminal Failed result for a denied SMI
// subnet read, with a specific, user-actionable message. It is returned instead of
// the generic retryable UnknownValidation so the create fails with an actionable
// error and the validation controller stops reporting the failure as a controller
// error (which otherwise floods the logs; see cluster_validation_controller.go).
func subnetReadPermissionDeniedResult(subnetID, smiResourceID *azcorearm.ResourceID) ValidationResult {
	msg := fmt.Sprintf(
		"The cluster's service-managed identity %q cannot read subnet %q "+
			"(missing Microsoft.Network/virtualNetworks/subnets/read). Grant the "+
			"identity a role that includes subnets/read on the subnet or its "+
			"virtual network, then retry.",
		resourceIDString(smiResourceID), resourceIDString(subnetID))
	return FailedValidation(ReasonSubnetReadPermissionDenied, msg, msg)
}

// resourceIDString renders a resource ID defensively (nil-safe).
func resourceIDString(id *azcorearm.ResourceID) string {
	if id == nil {
		return "<unknown>"
	}
	return id.String()
}

// subnetAddressPrefixes returns parsed subnet address prefixes from Azure.
// Azure sets either AddressPrefixes (plural, for dual-stack) or AddressPrefix (singular).
func subnetAddressPrefixes(subnet *armnetwork.Subnet) ([]netip.Prefix, error) {
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
		rawPrefixes = []string{*subnet.Properties.AddressPrefix}
	}

	if len(rawPrefixes) == 0 {
		return nil, utils.TrackError(fmt.Errorf("subnet %q has no address prefix", ptr.Deref(subnet.ID, "")))
	}

	prefixes := make([]netip.Prefix, 0, len(rawPrefixes))
	for _, raw := range rawPrefixes {
		p, err := netip.ParsePrefix(raw)
		if err != nil {
			return nil, utils.TrackError(fmt.Errorf("failed to parse address prefix %q for subnet %q: %w",
				raw, ptr.Deref(subnet.ID, ""), err))
		}
		prefixes = append(prefixes, p)
	}
	return prefixes, nil
}
