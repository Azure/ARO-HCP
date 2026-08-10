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
	"strings"

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

// outboundNSGPorts are TCP ports checked on the worker-subnet NSG (egress).
// Workers must reach the control plane on these ports (port "*" also blocks):
//
//   - 443: HTTPS to the private router (Konnectivity / Ignition via SWIFT)
//   - 6443: kube-apiserver (kubelet / API clients)
var outboundNSGPorts = []int32{443, 6443}

// inboundNSGPorts are TCP ports checked on the vnet-integration-subnet NSG
// (ingress). Worker → vnet-integration traffic on these ports must not be
// denied (port "*" also blocks):
//
//   - 8443: additional HTTPS path used on the private-router hop
//   - 443: HTTPS into the private router (Konnectivity / Ignition via SWIFT)
var inboundNSGPorts = []int32{8443, 443}

// AzureCustomerNSGValidation checks that customer-attached NSGs do not block
// worker-node access to the hosted kube-apiserver (KAS).
//
// An NSG Deny on the worker→control-plane path fails node bootstrap, so we
// reject the node pool before create proceeds.
//
// Outbound checks run on the worker-subnet NSG (node-pool subnet, or cluster
// subnet when the node pool has none). On outboundNSGPorts (including port
// "*"), Deny Any→Any, Deny Any→cluster machine subnet CIDRs/IPs, and Deny
// Any→vnet-integration CIDRs/IPs are rejected.
//
// Inbound checks run on the vnet-integration-subnet NSG: rules must not block
// worker → vnet-integration traffic on inboundNSGPorts. A higher-priority
// Allow compensates a Deny.
type AzureCustomerNSGValidation struct {
	smiClientBuilder azureclient.ServiceManagedIdentityClientBuilder
}

func UserProvidedNodePoolNetworkSecurityGroupValidation(smiClientBuilder azureclient.ServiceManagedIdentityClientBuilder) *AzureCustomerNSGValidation {
	return &AzureCustomerNSGValidation{
		smiClientBuilder: smiClientBuilder,
	}
}

var _ NodePoolValidation = (*AzureCustomerNSGValidation)(nil)

func (v *AzureCustomerNSGValidation) Name() string {
	return "AzureCustomerNSGValidation"
}

// Validate checks outbound rules on the worker-subnet NSG and inbound rules
// on the vnet-integration-subnet NSG. Each subnet is skipped when it has no NSG.
func (v *AzureCustomerNSGValidation) Validate(ctx context.Context, cluster *coreapi.HCPOpenShiftCluster, _ *coreapi.Subscription, nodePool *coreapi.HCPOpenShiftClusterNodePool) ValidationResult {
	smiResourceID := cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity
	clusterIdentityURL := cluster.ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL
	subscriptionID := cluster.ID.SubscriptionID

	subnetsClient, err := v.smiClientBuilder.SubnetsClient(ctx, clusterIdentityURL, smiResourceID, subscriptionID)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify customer network security group rules.",
			fmt.Sprintf("failed to get subnets client as service managed identity: %v", err),
			ControllerReportingPolicyTypeError,
		)
	}

	// Lazily create the NSG client only when a subnet has an NSG attached,
	// so we avoid a wasted SMI credential exchange when neither does.
	var nsgClient azureclient.NetworkSecurityGroupsClient
	getNSGClient := func() (azureclient.NetworkSecurityGroupsClient, error) {
		if nsgClient != nil {
			return nsgClient, nil
		}
		client, err := v.smiClientBuilder.NetworkSecurityGroupsClient(ctx, clusterIdentityURL, smiResourceID, subscriptionID)
		if err != nil {
			return nil, fmt.Errorf("failed to get network security groups client as service managed identity: %w", err)
		}
		nsgClient = client
		return nsgClient, nil
	}

	// Worker subnet: node-pool subnet if set, otherwise cluster subnet.
	workerSubnetID := nodePool.Properties.Platform.SubnetID
	if workerSubnetID == nil {
		workerSubnetID = cluster.CustomerProperties.Platform.SubnetID
	}

	clusterSubnetID := cluster.CustomerProperties.Platform.SubnetID

	workerSubnet, err := v.getSubnet(ctx, subnetsClient, workerSubnetID)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify customer network security group rules.",
			fmt.Sprintf("failed to get worker subnet: %v", err),
			ControllerReportingPolicyTypeError,
		)
	}
	workerNSGID, err := v.nsgIDFromSubnet(workerSubnet.Properties)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify customer network security group rules.",
			fmt.Sprintf("failed to get worker subnet NSG: %v", err),
			ControllerReportingPolicyTypeError,
		)
	}

	workerPrefixes, err := v.subnetAddressPrefixes(workerSubnet.Properties)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify customer network security group rules.",
			fmt.Sprintf("failed to get worker subnet address prefixes: %v", err),
			ControllerReportingPolicyTypeError,
		)
	}

	// Cluster machine subnet CIDRs
	clusterPrefixes := workerPrefixes
	if !strings.EqualFold(workerSubnetID.String(), clusterSubnetID.String()) {
		clusterSubnet, err := v.getSubnet(ctx, subnetsClient, clusterSubnetID)
		if err != nil {
			return UnknownValidation(
				"InternalError",
				"Unable to verify customer network security group rules.",
				fmt.Sprintf("failed to get cluster subnet: %v", err),
				ControllerReportingPolicyTypeError,
			)
		}
		clusterPrefixes, err = v.subnetAddressPrefixes(clusterSubnet.Properties)
		if err != nil {
			return UnknownValidation(
				"InternalError",
				"Unable to verify customer network security group rules.",
				fmt.Sprintf("failed to get cluster subnet address prefixes: %v", err),
				ControllerReportingPolicyTypeError,
			)
		}
	}

	// Outbound destinations: cluster machine subnet + vnet-integration (when set).
	// Any→Any is always evaluated inside validateOutboundNSGRules.
	outboundDestinationPrefixes := append([]string(nil), clusterPrefixes...)

	vnetIntegrationSubnetID := cluster.CustomerProperties.Platform.VnetIntegrationSubnetID
	var vnetIntegrationPrefixes []string
	var vnetIntegrationNSGID *azcorearm.ResourceID
	if vnetIntegrationSubnetID != nil {
		vnetIntegrationSubnet, err := v.getSubnet(ctx, subnetsClient, vnetIntegrationSubnetID)
		if err != nil {
			return UnknownValidation(
				"InternalError",
				"Unable to verify customer network security group rules.",
				fmt.Sprintf("failed to get vnet-integration subnet: %v", err),
				ControllerReportingPolicyTypeError,
			)
		}
		vnetIntegrationPrefixes, err = v.subnetAddressPrefixes(vnetIntegrationSubnet.Properties)
		if err != nil {
			return UnknownValidation(
				"InternalError",
				"Unable to verify customer network security group rules.",
				fmt.Sprintf("failed to get vnet-integration subnet address prefixes: %v", err),
				ControllerReportingPolicyTypeError,
			)
		}
		vnetIntegrationNSGID, err = v.nsgIDFromSubnet(vnetIntegrationSubnet.Properties)
		if err != nil {
			return UnknownValidation(
				"InternalError",
				"Unable to verify customer network security group rules.",
				fmt.Sprintf("failed to get vnet-integration subnet NSG: %v", err),
				ControllerReportingPolicyTypeError,
			)
		}
		outboundDestinationPrefixes = append(outboundDestinationPrefixes, vnetIntegrationPrefixes...)
	}

	client, err := getNSGClient()
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify customer network security group rules.",
			fmt.Sprintf("failed to get network security groups client: %v", err),
			ControllerReportingPolicyTypeError,
		)
	}
	outbound, _, err := v.listNSGSecurityRules(ctx, client, workerNSGID)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify customer network security group rules.",
			fmt.Sprintf("failed to list network security group rules: %v", err),
			ControllerReportingPolicyTypeError,
		)
	}
	if err := v.validateOutboundNSGRules(outbound, workerPrefixes, outboundDestinationPrefixes, outboundNSGPorts); err != nil {
		msg := err.Error()
		return FailedValidation("CustomerNSGBlocksControlPlane", msg, msg)
	}

	if vnetIntegrationNSGID == nil {
		return PassedValidation(
			coreapi.ControllerConditionReasonAsExpected,
			"Customer network security group rules are valid.",
			"Customer network security group rules are valid.",
		)
	}

	_, inbound, err := v.listNSGSecurityRules(ctx, client, vnetIntegrationNSGID)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify customer network security group rules.",
			fmt.Sprintf("failed to list network security group rules: %v", err),
			ControllerReportingPolicyTypeError,
		)
	}
	if err := v.validateInboundNSGRules(inbound, workerPrefixes, vnetIntegrationPrefixes, inboundNSGPorts); err != nil {
		msg := err.Error()
		return FailedValidation("CustomerNSGBlocksControlPlane", msg, fmt.Sprintf("failed to validate inbound network security group rules: %v", err))
	}
	return PassedValidation(
		coreapi.ControllerConditionReasonAsExpected,
		"Customer network security group rules are valid.",
		"Customer network security group rules are valid.",
	)
}

func (v *AzureCustomerNSGValidation) getSubnet(ctx context.Context, subnetsClient azureclient.SubnetsClient, subnetID *azcorearm.ResourceID) (*armnetwork.Subnet, error) {
	if subnetID.Parent == nil {
		return nil, fmt.Errorf("subnet %q has no parent virtual network", subnetID.String())
	}
	resp, err := subnetsClient.Get(ctx, subnetID.ResourceGroupName, subnetID.Parent.Name, subnetID.Name, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get subnet %q: %w", subnetID.String(), err)
	}
	if resp.Properties == nil {
		return nil, fmt.Errorf("subnet %q has no properties", subnetID.String())
	}
	return &resp.Subnet, nil
}

// subnetAddressPrefixes returns the subnet address prefixes from Azure.
// Azure sets either AddressPrefixes or AddressPrefix (not both).
func (v *AzureCustomerNSGValidation) subnetAddressPrefixes(props *armnetwork.SubnetPropertiesFormat) ([]string, error) {
	prefixes := singularOrPluralStrings(props.AddressPrefix, props.AddressPrefixes)
	if len(prefixes) == 0 {
		return nil, fmt.Errorf("has no address prefix")
	}
	return prefixes, nil
}

// nsgIDFromSubnet returns the NSG resource ID attached to the subnet, or nil if none.
func (v *AzureCustomerNSGValidation) nsgIDFromSubnet(props *armnetwork.SubnetPropertiesFormat) (*azcorearm.ResourceID, error) {
	if props.NetworkSecurityGroup == nil || props.NetworkSecurityGroup.ID == nil || *props.NetworkSecurityGroup.ID == "" {
		return nil, nil
	}
	nsgID, err := azcorearm.ParseResourceID(*props.NetworkSecurityGroup.ID)
	if err != nil {
		return nil, fmt.Errorf("invalid network security group ID %q: %w", *props.NetworkSecurityGroup.ID, err)
	}
	return nsgID, nil
}

func (v *AzureCustomerNSGValidation) listNSGSecurityRules(ctx context.Context, nsgClient azureclient.NetworkSecurityGroupsClient, nsgID *azcorearm.ResourceID) (outbound, inbound []NSGSecurityRule, err error) {
	resp, err := nsgClient.Get(ctx, nsgID.ResourceGroupName, nsgID.Name, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get network security group %q: %w", nsgID.String(), err)
	}
	if resp.Properties == nil {
		return nil, nil, fmt.Errorf("network security group %q has no properties", nsgID.String())
	}
	outbound = v.convertSecurityRules(resp.Properties.SecurityRules, armnetwork.SecurityRuleDirectionOutbound)
	inbound = v.convertSecurityRules(resp.Properties.SecurityRules, armnetwork.SecurityRuleDirectionInbound)
	return outbound, inbound, nil
}

// convertSecurityRules converts Azure's SecurityRule properties to our NSGSecurityRule type.
func (v *AzureCustomerNSGValidation) convertSecurityRules(rules []*armnetwork.SecurityRule, direction armnetwork.SecurityRuleDirection) []NSGSecurityRule {
	var out []NSGSecurityRule
	for _, rule := range rules {
		if rule == nil || rule.Properties == nil {
			continue
		}
		props := rule.Properties
		if props.Direction == nil || *props.Direction != direction {
			continue
		}
		access := SecurityGroupAccessDeny
		if props.Access != nil && *props.Access == armnetwork.SecurityRuleAccessAllow {
			access = SecurityGroupAccessAllow
		}
		protocol := "*"
		if props.Protocol != nil {
			protocol = string(*props.Protocol)
		}
		out = append(out, NSGSecurityRule{
			Name:                       ptr.Deref(rule.Name, ""),
			Priority:                   ptr.Deref(props.Priority, 0),
			Access:                     access,
			Protocol:                   protocol,
			SourceAddressPrefixes:      singularOrPluralStrings(props.SourceAddressPrefix, props.SourceAddressPrefixes),
			DestinationAddressPrefixes: singularOrPluralStrings(props.DestinationAddressPrefix, props.DestinationAddressPrefixes),
			DestinationPortRanges:      singularOrPluralStrings(props.DestinationPortRange, props.DestinationPortRanges),
		})
	}
	return out
}

// singularOrPluralStrings returns Azure's plural string list when present,
// otherwise the singular value. Azure populates one form or the other
// (e.g. DestinationPortRange vs DestinationPortRanges).
func singularOrPluralStrings(single *string, multi []*string) []string {
	if len(multi) > 0 {
		out := make([]string, 0, len(multi))
		for _, p := range multi {
			if p != nil {
				out = append(out, *p)
			}
		}
		return out
	}
	if single != nil {
		return []string{*single}
	}
	return nil
}
