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
	"strings"

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// outboundNSGPorts are TCP ports that must remain allowed on the worker-subnet NSG.
//
// Evaluated as: outbound rules on the NSG attached to the worker (node-pool or parentcluster) subnet.
// Required path: worker subnet → parent cluster machine subnet.
// Required Direction: outbound.
// A Deny on destination port "*" also blocks these ports.
//
//   - 443:  origin worker subnet → destination cluster subnet (HTTPS to private router; Konnectivity / Ignition via SWIFT)
//   - 6443: origin worker subnet → destination cluster subnet (kube-apiserver; kubelet / API clients)
var outboundNSGPorts = []int32{443, 6443}

// inboundNSGPorts are TCP ports that must remain allowed on the vnet-integration-subnet NSG.
//
// Evaluated as: inbound rules on the NSG attached to the vnet-integration subnet.
// Required path: worker subnet → vnet-integration subnet.
// Required Direction: inbound.
// A Deny on destination port "*" also blocks these ports.
//
//   - 8443: origin worker subnet → destination vnet-integration subnet (HTTPS hop on the private router)
//   - 443:  origin worker subnet → destination vnet-integration subnet (HTTPS to private router; Konnectivity / Ignition via SWIFT)
var inboundNSGPorts = []int32{8443, 443}

// AzureNodePoolNSGBasedRequiredConnectivityValidation checks that customer-attached NSGs do not block
// worker-node access to the hosted kube-apiserver (KAS) and vnet-integration subnet.
//
// An NSG Deny on the worker→control-plane path fails node bootstrap, so controller validates the NSG rules.
// the nodepool will still attempt to create in the background, but will never be successful.
//
// Outbound checks run on the worker-subnet NSG.
// On outboundNSGPorts (including port"*"), Deny Any→Any, Deny Any→parent cluster's subnet,
// and Deny worker→Any are rejected.
//
// Inbound checks run on the vnet-integration-subnet NSG: rules must not block
// worker → vnet-integration traffic on inboundNSGPorts. A higher-priority
// Allow compensates a Deny.
type AzureNodePoolNSGBasedRequiredConnectivityValidation struct {
	smiClientBuilder azureclient.ServiceManagedIdentityClientBuilder
}

func NewAzureNodePoolNSGBasedRequiredConnectivityValidation(smiClientBuilder azureclient.ServiceManagedIdentityClientBuilder) *AzureNodePoolNSGBasedRequiredConnectivityValidation {
	return &AzureNodePoolNSGBasedRequiredConnectivityValidation{
		smiClientBuilder: smiClientBuilder,
	}
}

var _ NodePoolValidation = (*AzureNodePoolNSGBasedRequiredConnectivityValidation)(nil)

func (v *AzureNodePoolNSGBasedRequiredConnectivityValidation) Name() string {
	return "AzureNodePoolNSGBasedRequiredConnectivityValidation"
}

// Validate checks outbound rules on the worker-subnet NSG and inbound rules
// on the vnet-integration-subnet NSG. Each subnet is skipped when it has no NSG.
func (v *AzureNodePoolNSGBasedRequiredConnectivityValidation) Validate(ctx context.Context, cluster *coreapi.HCPOpenShiftCluster, _ *coreapi.Subscription, nodePool *coreapi.HCPOpenShiftClusterNodePool) ValidationResult {
	smiResourceID := cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity
	clusterIdentityURL := cluster.ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL
	subscriptionID := cluster.ID.SubscriptionID

	subnetsClient, err := v.smiClientBuilder.SubnetsClient(ctx, clusterIdentityURL, smiResourceID, subscriptionID)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify azure network security group rules required for node pool connectivity.",
			fmt.Sprintf("failed to get subnets client as service managed identity: %v", err),
			ControllerReportingPolicyTypeError,
		)
	}

	// Lazily create the NSG client only when a subnet has an NSG attached,
	// so we avoid a wasted SMI credential exchange when neither does.
	getNSGClient := func() func() (azureclient.NetworkSecurityGroupsClient, error) {
		var nsgClient azureclient.NetworkSecurityGroupsClient
		return func() (azureclient.NetworkSecurityGroupsClient, error) {
			if nsgClient != nil {
				return nsgClient, nil
			}
			client, err := v.smiClientBuilder.NetworkSecurityGroupsClient(ctx, clusterIdentityURL, smiResourceID, subscriptionID)
			if err != nil {
				return nil, utils.TrackError(fmt.Errorf("failed to get network security groups client as service managed identity: %w", err))
			}
			nsgClient = client
			return nsgClient, nil
		}
	}()

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
			"Unable to verify azure network security group rules required for node pool connectivity.",
			fmt.Sprintf("failed to get worker subnet for subnet ID %q: %v", workerSubnetID.String(), err),
			ControllerReportingPolicyTypeError,
		)
	}
	workerNSGID, err := v.nsgIDFromSubnet(workerSubnet)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify azure network security group rules required for node pool connectivity.",
			fmt.Sprintf("failed to get worker subnet NSG for subnet ID %q: %v", workerSubnetID.String(), err),
			ControllerReportingPolicyTypeError,
		)
	}

	workerPrefixes, err := v.subnetAddressPrefixes(workerSubnet)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify azure network security group rules required for node pool connectivity.",
			fmt.Sprintf("failed to get worker subnet address prefixes for subnet ID %q: %v", workerSubnetID.String(), err),
			ControllerReportingPolicyTypeError,
		)
	}

	// If the worker subnet has an nsg attached, then we need to validate outbound rules on the worker-subnet NSG.
	// If the worker subnet has no nsg attached, then there will be no outbound rules to validate.
	var allViolations []*nsgRequiredConnectivityViolationDetails
	if workerNSGID != nil {
		// Check outbound rule destination on parent cluster's subnet
		// if worker subnet and cluster subnet are same then workerPrefixes are prefixes of parent cluster's subnet and use as destination prefix.
		// if worker subnet and cluster subnet are different then get clusterPrefixes from parent cluster's subnet and use as destination prefix.
		clusterPrefixes := workerPrefixes
		if !strings.EqualFold(workerSubnetID.String(), clusterSubnetID.String()) {
			clusterSubnet, err := v.getSubnet(ctx, subnetsClient, clusterSubnetID)
			if err != nil {
				return UnknownValidation(
					"InternalError",
					"Unable to verify azure network security group rules required for node pool connectivity.",
					fmt.Sprintf("failed to get cluster subnet for subnet ID %q: %v", clusterSubnetID.String(), err),
					ControllerReportingPolicyTypeError,
				)
			}
			clusterPrefixes, err = v.subnetAddressPrefixes(clusterSubnet)
			if err != nil {
				return UnknownValidation(
					"InternalError",
					"Unable to verify azure network security group rules required for node pool connectivity.",
					fmt.Sprintf("failed to get cluster subnet address prefixes for subnet ID %q: %v", clusterSubnetID.String(), err),
					ControllerReportingPolicyTypeError,
				)
			}
		}

		var outboundDestinationPrefixes []netip.Prefix
		outboundDestinationPrefixes = append(outboundDestinationPrefixes, clusterPrefixes...)

		nsgClient, err := getNSGClient()
		if err != nil {
			return UnknownValidation(
				"InternalError",
				"Unable to verify azure network security group rules required for node pool connectivity.",
				fmt.Sprintf("failed to get network security groups client for worker subnet NSG ID %q: %v", workerNSGID.String(), err),
				ControllerReportingPolicyTypeError,
			)
		}
		outbound, _, err := v.listNSGSecurityRules(ctx, nsgClient, workerNSGID)
		if err != nil {
			return UnknownValidation(
				"InternalError",
				"Unable to verify azure network security group rules required for node pool connectivity.",
				fmt.Sprintf("failed to list network security group rules for worker subnet NSG ID %q: %v", workerNSGID.String(), err),
				ControllerReportingPolicyTypeError,
			)
		}
		outboundViolations, err := v.validateOutboundNSGRules(outbound, workerPrefixes, outboundDestinationPrefixes, outboundNSGPorts)
		if err != nil {
			return UnknownValidation(
				"InternalError",
				"Unable to verify azure network security group rules required for node pool connectivity.",
				fmt.Sprintf("failed to validate outbound network security group rules: %v", err),
				ControllerReportingPolicyTypeError,
			)
		}
		allViolations = append(allViolations, outboundViolations...)
	}

	// inbound validation on the vnet-integration subnet
	vnetIntegrationSubnetID := cluster.CustomerProperties.Platform.VnetIntegrationSubnetID

	// Inbound validation runs on the vnet-integration subnet. When that subnet is
	// absent the cluster is legacy/non-SWIFT and inbound NSG checks do not apply.
	// TODO: When 2024-06-10-preview API support is removed, delete vnetIntegrationSubnetID
	// non-nil check and early-return passed validation block.
	if vnetIntegrationSubnetID == nil {
		if len(allViolations) > 0 {
			msg := formatNSGRequiredConnectivityViolationsMessage(allViolations)
			return FailedValidation("AzureNodePoolNSGBlocksRequiredConnectivity", msg, msg)
		}
		return PassedValidation(
			coreapi.ControllerConditionReasonAsExpected,
			"Azure network security group rules required for node pool connectivity are valid.",
			"Azure network security group rules required for node pool connectivity are valid.")
	}
	vnetIntegrationSubnet, err := v.getSubnet(ctx, subnetsClient, vnetIntegrationSubnetID)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify azure network security group rules required for node pool connectivity.",
			fmt.Sprintf("failed to get vnet-integration subnet for subnet ID %q: %v", vnetIntegrationSubnetID.String(), err),
			ControllerReportingPolicyTypeError,
		)
	}
	vnetIntegrationPrefixes, err := v.subnetAddressPrefixes(vnetIntegrationSubnet)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify azure network security group rules required for node pool connectivity.",
			fmt.Sprintf("failed to get vnet-integration subnet address prefixes for subnet ID %q: %v", vnetIntegrationSubnetID.String(), err),
			ControllerReportingPolicyTypeError,
		)
	}
	vnetIntegrationNSGID, err := v.nsgIDFromSubnet(vnetIntegrationSubnet)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify azure network security group rules required for node pool connectivity.",
			fmt.Sprintf("failed to get vnet-integration subnet NSG for subnet ID %q: %v", vnetIntegrationSubnetID.String(), err),
			ControllerReportingPolicyTypeError,
		)
	}

	// If the vnet-integration subnet has no NSG attached, then there will be no
	// inbound rules to validate.
	if vnetIntegrationNSGID == nil {
		if len(allViolations) > 0 {
			msg := formatNSGRequiredConnectivityViolationsMessage(allViolations)
			return FailedValidation("AzureNodePoolNSGBlocksRequiredConnectivity", msg, msg)
		}
		return PassedValidation(
			coreapi.ControllerConditionReasonAsExpected,
			"Azure network security group rules required for node pool connectivity are valid.",
			"Azure network security group rules required for node pool connectivity are valid.",
		)
	}

	nsgClient, err := getNSGClient()
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify azure network security group rules required for node pool connectivity.",
			fmt.Sprintf("failed to get network security groups client for vnet-integration subnet NSG ID %q: %v", vnetIntegrationNSGID.String(), err),
			ControllerReportingPolicyTypeError,
		)
	}

	_, inbound, err := v.listNSGSecurityRules(ctx, nsgClient, vnetIntegrationNSGID)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify azure network security group rules required for node pool connectivity.",
			fmt.Sprintf("failed to list network security group rules for vnet-integration subnet NSG ID %q: %v", vnetIntegrationNSGID.String(), err),
			ControllerReportingPolicyTypeError,
		)
	}
	inboundViolations, err := v.validateInboundNSGRules(inbound, workerPrefixes, vnetIntegrationPrefixes, inboundNSGPorts)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify azure network security group rules required for node pool connectivity.",
			fmt.Sprintf("failed to validate inbound network security group rules: %v", err),
			ControllerReportingPolicyTypeError,
		)
	}
	allViolations = append(allViolations, inboundViolations...)

	if len(allViolations) > 0 {
		msg := formatNSGRequiredConnectivityViolationsMessage(allViolations)
		return FailedValidation("AzureNodePoolNSGBlocksRequiredConnectivity", msg, msg)
	}
	return PassedValidation(
		coreapi.ControllerConditionReasonAsExpected,
		"Azure network security group rules required for node pool connectivity are valid.",
		"Azure network security group rules required for node pool connectivity are valid.",
	)
}

func (v *AzureNodePoolNSGBasedRequiredConnectivityValidation) getSubnet(ctx context.Context, subnetsClient azureclient.SubnetsClient, subnetID *azcorearm.ResourceID) (*armnetwork.Subnet, error) {
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

// subnetAddressPrefixes returns parsed subnet address prefixes from Azure.
// Azure sets either AddressPrefixes or AddressPrefix.
func (v *AzureNodePoolNSGBasedRequiredConnectivityValidation) subnetAddressPrefixes(subnet *armnetwork.Subnet) ([]netip.Prefix, error) {
	if subnet.Properties == nil {
		return nil, utils.TrackError(fmt.Errorf("subnet %q has no properties", ptr.Deref(subnet.ID, "")))
	}
	rawPrefixes := v.singularOrPluralStrings(subnet.Properties.AddressPrefix, subnet.Properties.AddressPrefixes)
	// subnet from Azure should always have at least one address prefix
	// treat missing prefixes as an internal error.
	if len(rawPrefixes) == 0 {
		return nil, utils.TrackError(fmt.Errorf("subnet %q has no address prefix", ptr.Deref(subnet.ID, "")))
	}
	prefixes, err := parsePrefixes(rawPrefixes, "subnet")
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to parse subnet address prefixes for subnet %q: %w", ptr.Deref(subnet.ID, ""), err))
	}
	return prefixes, nil
}

// nsgIDFromSubnet returns the NSG resource ID attached to the subnet, or nil if none.
func (v *AzureNodePoolNSGBasedRequiredConnectivityValidation) nsgIDFromSubnet(subnet *armnetwork.Subnet) (*azcorearm.ResourceID, error) {
	if subnet.Properties == nil {
		return nil, utils.TrackError(fmt.Errorf("subnet %q has no properties", ptr.Deref(subnet.ID, "")))
	}
	props := subnet.Properties
	if props.NetworkSecurityGroup == nil || props.NetworkSecurityGroup.ID == nil || *props.NetworkSecurityGroup.ID == "" {
		return nil, nil
	}
	nsgID, err := azcorearm.ParseResourceID(*props.NetworkSecurityGroup.ID)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("invalid network security group ID %q: %w", *props.NetworkSecurityGroup.ID, err))
	}
	return nsgID, nil
}

func (v *AzureNodePoolNSGBasedRequiredConnectivityValidation) listNSGSecurityRules(ctx context.Context, nsgClient azureclient.NetworkSecurityGroupsClient, nsgID *azcorearm.ResourceID) (outbound, inbound []nsgSecurityRule, err error) {
	resp, err := nsgClient.Get(ctx, nsgID.ResourceGroupName, nsgID.Name, nil)
	if err != nil {
		return nil, nil, utils.TrackError(fmt.Errorf("failed to get network security group %q: %w", nsgID.String(), err))
	}
	if resp.Properties == nil {
		return nil, nil, utils.TrackError(fmt.Errorf("network security group %q has no properties", nsgID.String()))
	}
	outbound = append(
		v.convertSecurityRules(resp.Properties.SecurityRules, armnetwork.SecurityRuleDirectionOutbound),
		v.convertSecurityRules(resp.Properties.DefaultSecurityRules, armnetwork.SecurityRuleDirectionOutbound)...,
	)
	inbound = append(
		v.convertSecurityRules(resp.Properties.SecurityRules, armnetwork.SecurityRuleDirectionInbound),
		v.convertSecurityRules(resp.Properties.DefaultSecurityRules, armnetwork.SecurityRuleDirectionInbound)...,
	)
	return outbound, inbound, nil
}

// convertSecurityRules converts Azure's SecurityRule properties to our nsgSecurityRule type.
func (v *AzureNodePoolNSGBasedRequiredConnectivityValidation) convertSecurityRules(rules []*armnetwork.SecurityRule, direction armnetwork.SecurityRuleDirection) []nsgSecurityRule {
	var out []nsgSecurityRule
	for _, rule := range rules {
		if rule == nil || rule.Properties == nil {
			continue
		}
		props := rule.Properties
		if props.Direction == nil || *props.Direction != direction {
			continue
		}
		access := securityGroupAccessDeny
		if props.Access != nil && *props.Access == armnetwork.SecurityRuleAccessAllow {
			access = securityGroupAccessAllow
		}
		protocol := "*"
		if props.Protocol != nil {
			protocol = string(*props.Protocol)
		}
		out = append(out, nsgSecurityRule{
			Name:                       ptr.Deref(rule.Name, ""),
			Priority:                   ptr.Deref(props.Priority, 0),
			Access:                     access,
			Protocol:                   protocol,
			SourceAddressPrefixes:      v.singularOrPluralStrings(props.SourceAddressPrefix, props.SourceAddressPrefixes),
			SourcePortRanges:           v.singularOrPluralStrings(props.SourcePortRange, props.SourcePortRanges),
			DestinationAddressPrefixes: v.singularOrPluralStrings(props.DestinationAddressPrefix, props.DestinationAddressPrefixes),
			DestinationPortRanges:      v.singularOrPluralStrings(props.DestinationPortRange, props.DestinationPortRanges),
		})
	}
	return out
}

// validateOutboundNSGRules validates outbound NSG rules so worker nodes
// can reach the control plane (typically TCP 443/6443; see outboundNSGPorts).
//
// workerSubnetPrefixes are the worker (node-pool or cluster) address spaces that
// originate egress. destinationPrefixes are the address spaces those workers must
// reach for KAS.
//
// Returns all violations when Deny rules block required traffic; err is reserved for
// unexpected input or evaluation failures (invalid CIDRs, etc.).
func (v *AzureNodePoolNSGBasedRequiredConnectivityValidation) validateOutboundNSGRules(rules []nsgSecurityRule, workerSubnetPrefixes, destinationPrefixes []netip.Prefix, ports []int32) ([]*nsgRequiredConnectivityViolationDetails, error) {
	// validate source and destination ports are in the valid range
	err := preValidationPortCheck(rules)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	paths, err := buildRequiredNSGPaths(workerSubnetPrefixes, "worker subnet", destinationPrefixes, "destination subnet", ports)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	return evaluateRequiredNSGPathViolations(rules, paths, nsgDirectionOutbound)
}

// validateInboundNSGRules validates inbound NSG rules so
// traffic from worker nodes toward the vnet integration path used for KAS is
// not blocked on the given ports (typically TCP 443/8443; see inboundNSGPorts).
//
// workerSubnetPrefixes identify worker-sourced traffic.
// vnetIntegrationSubnetPrefixes are the vnet-integration subnet address spaces
// whose NSG is being evaluated; must be non-empty.
//
// Returns all violations when Deny rules block required traffic; err is reserved for
// unexpected input or evaluation failures.
func (v *AzureNodePoolNSGBasedRequiredConnectivityValidation) validateInboundNSGRules(rules []nsgSecurityRule, workerSubnetPrefixes, vnetIntegrationSubnetPrefixes []netip.Prefix, ports []int32) ([]*nsgRequiredConnectivityViolationDetails, error) {
	// validate  source and destination ports are in the valid range
	err := preValidationPortCheck(rules)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	paths, err := buildRequiredNSGPaths(workerSubnetPrefixes, "worker subnet", vnetIntegrationSubnetPrefixes, "vnet-integration subnet", ports)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	return evaluateRequiredNSGPathViolations(rules, paths, nsgDirectionInbound)
}

// singularOrPluralStrings returns Azure's plural string list when present,
// otherwise the singular value. Azure populates one form or the other
// (e.g. DestinationPortRange vs DestinationPortRanges).
func (v *AzureNodePoolNSGBasedRequiredConnectivityValidation) singularOrPluralStrings(single *string, multi []*string) []string {
	var out []string
	// Process plural list if items exist
	for _, p := range multi {
		if p != nil {
			out = append(out, *p)
		}
	}
	// If we found valid elements in the plural list, return them
	if len(out) > 0 {
		return out
	}
	// Fall back to singular value if the plural list was empty or entirely nil
	if single != nil {
		return []string{*single}
	}
	return nil
}
