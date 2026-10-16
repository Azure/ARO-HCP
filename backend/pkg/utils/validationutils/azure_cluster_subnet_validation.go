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
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

// AzureClusterSubnetValidation validates that the cluster's subnet exists, that it is in the same
// Azure Location as the cluster, and that it has the cluster's network security group attached.
type AzureClusterSubnetValidation struct {
	smiClientBuilder azureclient.ServiceManagedIdentityClientBuilder
}

var _ ClusterValidation = (*AzureClusterSubnetValidation)(nil)

func NewAzureClusterSubnetValidation(
	smiClientBuilder azureclient.ServiceManagedIdentityClientBuilder,
) *AzureClusterSubnetValidation {
	return &AzureClusterSubnetValidation{
		smiClientBuilder: smiClientBuilder,
	}
}

func (v *AzureClusterSubnetValidation) Name() string {
	return "AzureClusterSubnetValidation"
}

func (v *AzureClusterSubnetValidation) Validate(ctx context.Context, clusterSubscription *coreapi.Subscription, cluster *coreapi.Cluster) ValidationResult {
	subnetID := cluster.CustomerProperties.Platform.SubnetID
	smiResourceID := cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity
	clusterIdentityURL := cluster.ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL

	virtualNetworksClient, err := v.smiClientBuilder.VirtualNetworksClient(ctx, clusterIdentityURL, smiResourceID, cluster.ID.SubscriptionID)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			fmt.Sprintf("Unable to verify the cluster's subnet %q.", subnetID.String()),
			fmt.Sprintf("failed to get virtual networks client as service managed identity: %v", err),
			ControllerReportingPolicyTypeError,
		)
	}

	// It's not possible to get the subnet's location directly from the Azure Subnet resource. That information
	// is available on the virtual network that the subnet belongs to.
	resp, err := virtualNetworksClient.Get(ctx, subnetID.ResourceGroupName, subnetID.Parent.Name, nil)
	if err != nil {
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound {
			msg := fmt.Sprintf("Virtual network %q of cluster's subnet %q was not found.", subnetID.Parent.String(), subnetID.String())
			return FailedValidation("VirtualNetworkNotFound", msg, msg)
		}
		return UnknownValidation(
			"InternalError",
			fmt.Sprintf("Unable to verify the cluster's subnet %q.", subnetID.String()),
			fmt.Sprintf("failed to get virtual network %q: %v", subnetID.Parent.String(), err),
			ControllerReportingPolicyTypeError,
		)
	}

	if resp.Properties == nil {
		return UnknownValidation(
			"InternalError",
			fmt.Sprintf("Unable to verify the cluster's subnet %q.", subnetID.String()),
			fmt.Sprintf("virtual network %q has no properties", subnetID.Parent.String()),
			ControllerReportingPolicyTypeError,
		)
	}

	// We can check if the subnet itself exists by iterating over the virtual network's subnets. This allows us to
	// avoid making an additional API call to the subnet resource. If the subnet is deleted it stops appearing
	// in the virtual network's subnets list.
	var clusterSubnet *armnetwork.Subnet
	for _, subnet := range resp.Properties.Subnets {
		if subnet != nil && subnet.Name != nil && *subnet.Name == subnetID.Name {
			clusterSubnet = subnet
			break
		}
	}

	if resp.Location == nil {
		return UnknownValidation(
			"InternalError",
			fmt.Sprintf("Unable to verify the cluster's subnet %q.", subnetID.String()),
			fmt.Sprintf("virtual network %q has no location", subnetID.Parent.String()),
			ControllerReportingPolicyTypeError,
		)
	}

	// Collect every deterministic failure instead of stopping at the first one, so the condition
	// message reports all problems with the subnet at once.
	var failureMessages []string

	if clusterSubnet == nil {
		failureMessages = append(failureMessages, fmt.Sprintf("Cluster's subnet %q was not found.", subnetID.String()))
	}

	if !strings.EqualFold(*resp.Location, cluster.Location) {
		failureMessages = append(failureMessages, fmt.Sprintf(
			"Location of the cluster's subnet %q (%q) is not the same as the cluster's location (%q).",
			subnetID.String(), *resp.Location, cluster.Location))
	}

	if clusterSubnet != nil {
		if clusterSubnet.Properties == nil {
			return UnknownValidation(
				"InternalError",
				fmt.Sprintf("Unable to verify the cluster's subnet %q.", subnetID.String()),
				fmt.Sprintf("subnet %q has no properties", subnetID.String()),
				ControllerReportingPolicyTypeError,
			)
		}

		nsgID := cluster.CustomerProperties.Platform.NetworkSecurityGroupID
		var subnetNSGID string
		if clusterSubnet.Properties.NetworkSecurityGroup != nil && clusterSubnet.Properties.NetworkSecurityGroup.ID != nil {
			subnetNSGID = *clusterSubnet.Properties.NetworkSecurityGroup.ID
		}
		switch {
		case subnetNSGID == "":
			failureMessages = append(failureMessages, fmt.Sprintf(
				"Cluster's subnet %q does not have the cluster's network security group %q attached.",
				subnetID.String(), nsgID.String()))
		case !strings.EqualFold(subnetNSGID, nsgID.String()):
			failureMessages = append(failureMessages, fmt.Sprintf(
				"Cluster's subnet %q has network security group %q attached instead of the cluster's network security group %q.",
				subnetID.String(), subnetNSGID, nsgID.String()))
		}
	}

	if len(failureMessages) > 0 {
		msg := strings.Join(failureMessages, " ")
		return FailedValidation("SubnetValidationFailed", msg, msg)
	}

	internalAndUserMsg := fmt.Sprintf(
		"Cluster's subnet %q exists in the same location as the cluster and has the cluster's network security group attached.",
		subnetID.String())
	return PassedValidation(coreapi.ControllerConditionReasonAsExpected, internalAndUserMsg, internalAndUserMsg)
}
