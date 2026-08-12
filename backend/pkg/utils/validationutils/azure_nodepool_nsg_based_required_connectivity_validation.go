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
	"github.com/Azure/ARO-HCP/internal/utils"
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

// AzureNodePoolNSGBasedRequiredConnectivityValidation checks that customer-attached NSGs do not block
// worker-node access to the hosted kube-apiserver (KAS) and vnet-integration subnet.
//
// An NSG Deny on the worker→control-plane path fails node bootstrap, so we
// reject the node pool before create proceeds.
//
// Outbound checks run on the worker-subnet NSG.
// On outboundNSGPorts (including port"*"), Deny Any→Any, Deny Any→cluster machine subnet,
// and Deny worker→Any are rejected.
//
// Inbound checks run on the vnet-integration-subnet NSG: rules must not block
// worker → vnet-integration traffic on inboundNSGPorts. A higher-priority
// Allow compensates a Deny.
type AzureNodePoolNSGBasedRequiredConnectivityValidation struct {
	smiClientBuilder azureclient.ServiceManagedIdentityClientBuilder
}

func UserProvidedNodePoolNSGBasedRequiredConnectivityValidation(smiClientBuilder azureclient.ServiceManagedIdentityClientBuilder) *AzureNodePoolNSGBasedRequiredConnectivityValidation {
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
			"Unable to verify customer network security group rules.",
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
			"Unable to verify customer network security group rules.",
			fmt.Sprintf("failed to get worker subnet for subnet ID %q: %v", workerSubnetID.String(), err),
			ControllerReportingPolicyTypeError,
		)
	}
	workerNSGID, err := v.nsgIDFromSubnet(workerSubnet)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify customer network security group rules.",
			fmt.Sprintf("failed to get worker subnet NSG for subnet ID %q: %v", workerSubnetID.String(), err),
			ControllerReportingPolicyTypeError,
		)
	}

	workerPrefixes, err := v.subnetAddressPrefixes(workerSubnet)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify customer network security group rules.",
			fmt.Sprintf("failed to get worker subnet address prefixes for subnet ID %q: %v", workerSubnetID.String(), err),
			ControllerReportingPolicyTypeError,
		)
	}

	// No subnet-level NSG on the worker subnet: Azure applies no NSG egress rules
	// at this hop, so there is nothing to inspect for outbound Denys.
	if workerNSGID != nil {
		// Check outbound rule destination on default cluster subnet
		// if worker subnet and cluster subnet are same then workerPrefixes are prefixes of default cluster subnet and use as destination prefix.
		// if worker subnet and cluster subnet are different then get clusterPrefixes from default cluster subnet and use as destination prefix.
		clusterPrefixes := workerPrefixes
		if !strings.EqualFold(workerSubnetID.String(), clusterSubnetID.String()) {
			clusterSubnet, err := v.getSubnet(ctx, subnetsClient, clusterSubnetID)
			if err != nil {
				return UnknownValidation(
					"InternalError",
					"Unable to verify customer network security group rules.",
					fmt.Sprintf("failed to get cluster subnet for subnet ID %q: %v", clusterSubnetID.String(), err),
					ControllerReportingPolicyTypeError,
				)
			}
			clusterPrefixes, err = v.subnetAddressPrefixes(clusterSubnet)
			if err != nil {
				return UnknownValidation(
					"InternalError",
					"Unable to verify customer network security group rules.",
					fmt.Sprintf("failed to get cluster subnet address prefixes for subnet ID %q: %v", clusterSubnetID.String(), err),
					ControllerReportingPolicyTypeError,
				)
			}
		}

		outboundDestinationPrefixes := append([]string(nil), clusterPrefixes...)

		nsgClient, err := getNSGClient()
		if err != nil {
			return UnknownValidation(
				"InternalError",
				"Unable to verify customer network security group rules.",
				fmt.Sprintf("failed to get network security groups client for worker subnet NSG ID %q: %v", workerNSGID.String(), err),
				ControllerReportingPolicyTypeError,
			)
		}
		outbound, _, err := v.listNSGSecurityRules(ctx, nsgClient, workerNSGID)
		if err != nil {
			return UnknownValidation(
				"InternalError",
				"Unable to verify customer network security group rules.",
				fmt.Sprintf("failed to list network security group rules for worker subnet NSG ID %q: %v", workerNSGID.String(), err),
				ControllerReportingPolicyTypeError,
			)
		}
		violation, err := v.validateOutboundNSGRules(outbound, workerPrefixes, outboundDestinationPrefixes, outboundNSGPorts)
		if err != nil {
			return UnknownValidation(
				"InternalError",
				"Unable to verify customer network security group rules.",
				fmt.Sprintf("failed to validate outbound network security group rules: %v", err),
				ControllerReportingPolicyTypeError,
			)
		}
		if violation != nil {
			return FailedValidation("CustomerNSGBlocksControlPlane", violation.Message, violation.Message)
		}
	}

	// inbound validation on the vnet-integration subnet
	vnetIntegrationSubnetID := cluster.CustomerProperties.Platform.VnetIntegrationSubnetID

	// we check inbound validation on vnet-integration subnet
	// If the vnet-integration subnet is not present, it means it is
	// a legacy/non-swift cluster and does not need to check inbound validation.
	if vnetIntegrationSubnetID == nil {
		return PassedValidation(
			coreapi.ControllerConditionReasonAsExpected,
			"Customer network security group rules are valid.",
			"Customer network security group rules are valid.",
		)
	}
	vnetIntegrationSubnet, err := v.getSubnet(ctx, subnetsClient, vnetIntegrationSubnetID)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify customer network security group rules.",
			fmt.Sprintf("failed to get vnet-integration subnet for subnet ID %q: %v", vnetIntegrationSubnetID.String(), err),
			ControllerReportingPolicyTypeError,
		)
	}
	vnetIntegrationPrefixes, err := v.subnetAddressPrefixes(vnetIntegrationSubnet)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify customer network security group rules.",
			fmt.Sprintf("failed to get vnet-integration subnet address prefixes for subnet ID %q: %v", vnetIntegrationSubnetID.String(), err),
			ControllerReportingPolicyTypeError,
		)
	}
	vnetIntegrationNSGID, err := v.nsgIDFromSubnet(vnetIntegrationSubnet)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify customer network security group rules.",
			fmt.Sprintf("failed to get vnet-integration subnet NSG for subnet ID %q: %v", vnetIntegrationSubnetID.String(), err),
			ControllerReportingPolicyTypeError,
		)
	}

	// If the vnet-integration subnet has no NSG attached, then there will be no
	// inbound rules to validate.
	if vnetIntegrationNSGID == nil {
		return PassedValidation(
			coreapi.ControllerConditionReasonAsExpected,
			"Customer network security group rules are valid.",
			"Customer network security group rules are valid.",
		)
	}

	nsgClient, err := getNSGClient()
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify customer network security group rules.",
			fmt.Sprintf("failed to get network security groups client for vnet-integration subnet NSG ID %q: %v", vnetIntegrationNSGID.String(), err),
			ControllerReportingPolicyTypeError,
		)
	}

	_, inbound, err := v.listNSGSecurityRules(ctx, nsgClient, vnetIntegrationNSGID)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify customer network security group rules.",
			fmt.Sprintf("failed to list network security group rules for vnet-integration subnet NSG ID %q: %v", vnetIntegrationNSGID.String(), err),
			ControllerReportingPolicyTypeError,
		)
	}
	violation, err := v.validateInboundNSGRules(inbound, workerPrefixes, vnetIntegrationPrefixes, inboundNSGPorts)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify customer network security group rules.",
			fmt.Sprintf("failed to validate inbound network security group rules: %v", err),
			ControllerReportingPolicyTypeError,
		)
	}
	if violation != nil {
		return FailedValidation("CustomerNSGBlocksControlPlane", violation.Message, violation.Message)
	}
	return PassedValidation(
		coreapi.ControllerConditionReasonAsExpected,
		"Customer network security group rules are valid.",
		"Customer network security group rules are valid.",
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

// subnetAddressPrefixes returns the subnet address prefixes from Azure.
// Azure sets either AddressPrefixes or AddressPrefix (not both).
func (v *AzureNodePoolNSGBasedRequiredConnectivityValidation) subnetAddressPrefixes(subnet *armnetwork.Subnet) ([]string, error) {
	if subnet.Properties == nil {
		return nil, utils.TrackError(fmt.Errorf("subnet %q has no properties", ptr.Deref(subnet.ID, "")))
	}
	prefixes := v.singularOrPluralStrings(subnet.Properties.AddressPrefix, subnet.Properties.AddressPrefixes)
	// subnet from Azure should always have at least one address prefix
	// treat missing prefixes as an internal error.
	if len(prefixes) == 0 {
		return nil, utils.TrackError(fmt.Errorf("subnet %q has no address prefix", ptr.Deref(subnet.ID, "")))
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

func (v *AzureNodePoolNSGBasedRequiredConnectivityValidation) listNSGSecurityRules(ctx context.Context, nsgClient azureclient.NetworkSecurityGroupsClient, nsgID *azcorearm.ResourceID) (outbound, inbound []NSGSecurityRule, err error) {
	resp, err := nsgClient.Get(ctx, nsgID.ResourceGroupName, nsgID.Name, nil)
	if err != nil {
		return nil, nil, utils.TrackError(fmt.Errorf("failed to get network security group %q: %w", nsgID.String(), err))
	}
	if resp.Properties == nil {
		return nil, nil, utils.TrackError(fmt.Errorf("network security group %q has no properties", nsgID.String()))
	}
	outbound = v.convertSecurityRules(resp.Properties.SecurityRules, armnetwork.SecurityRuleDirectionOutbound)
	inbound = v.convertSecurityRules(resp.Properties.SecurityRules, armnetwork.SecurityRuleDirectionInbound)
	return outbound, inbound, nil
}

// convertSecurityRules converts Azure's SecurityRule properties to our NSGSecurityRule type.
func (v *AzureNodePoolNSGBasedRequiredConnectivityValidation) convertSecurityRules(rules []*armnetwork.SecurityRule, direction armnetwork.SecurityRuleDirection) []NSGSecurityRule {
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
			SourceAddressPrefixes:      v.singularOrPluralStrings(props.SourceAddressPrefix, props.SourceAddressPrefixes),
			DestinationAddressPrefixes: v.singularOrPluralStrings(props.DestinationAddressPrefix, props.DestinationAddressPrefixes),
			DestinationPortRanges:      v.singularOrPluralStrings(props.DestinationPortRange, props.DestinationPortRanges),
		})
	}
	return out
}

// validateOutboundNSGRules validates outbound NSG rules so worker nodes
// can reach the control plane (typically TCP 443/6443; see outboundNSGPorts).
//
// workerSubnetCIDRs are the worker (node-pool or cluster) address spaces that
// originate egress. destinationCIDRs are the address spaces those workers must
// reach for KAS (cluster machine subnet).
//
// Returns a violation when a Deny blocks required traffic; err is reserved for
// unexpected input or evaluation failures (invalid CIDRs, etc.).
func (v *AzureNodePoolNSGBasedRequiredConnectivityValidation) validateOutboundNSGRules(rules []NSGSecurityRule, workerSubnetCIDRs, destinationCIDRs []string, ports []int32) (*nsgDenyViolation, error) {
	paths, err := buildRequiredNSGPaths(workerSubnetCIDRs, "worker subnet", destinationCIDRs, "destination subnet", ports)
	if err != nil {
		return nil, err
	}
	return findBlockingNSGDeny(rules, paths, nsgDirectionOutbound)
}

// validateInboundNSGRules validates inbound NSG rules so
// traffic from worker nodes toward the vnet integration path used for KAS is
// not blocked on the given ports (typically TCP 443/8443; see inboundNSGPorts).
//
// workerSubnetCIDRs identify worker-sourced traffic.
// vnetIntegrationSubnetCIDRs are the vnet-integration subnet address spaces
// whose NSG is being evaluated; must be non-empty.
//
// Returns a violation when a Deny blocks required traffic; err is reserved for
// unexpected input or evaluation failures.
func (v *AzureNodePoolNSGBasedRequiredConnectivityValidation) validateInboundNSGRules(rules []NSGSecurityRule, workerSubnetCIDRs, vnetIntegrationSubnetCIDRs []string, ports []int32) (*nsgDenyViolation, error) {
	paths, err := buildRequiredNSGPaths(workerSubnetCIDRs, "worker subnet", vnetIntegrationSubnetCIDRs, "vnet-integration subnet", ports)
	if err != nil {
		return nil, err
	}
	return findBlockingNSGDeny(rules, paths, nsgDirectionInbound)
}

// singularOrPluralStrings returns Azure's plural string list when present,
// otherwise the singular value. Azure populates one form or the other
// (e.g. DestinationPortRange vs DestinationPortRanges).
func (v *AzureNodePoolNSGBasedRequiredConnectivityValidation) singularOrPluralStrings(single *string, multi []*string) []string {
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
