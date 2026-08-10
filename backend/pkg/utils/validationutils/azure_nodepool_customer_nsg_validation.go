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

func (v *AzureCustomerNSGValidation) Name() string {
	return "AzureCustomerNSGValidation"
}

// Validate checks outbound rules on the worker-subnet NSG and inbound rules
// on the vnet-integration-subnet NSG. Each subnet is skipped when it has no NSG.
func (v *AzureCustomerNSGValidation) Validate(ctx context.Context, cluster *coreapi.HCPOpenShiftCluster, _ *coreapi.Subscription, nodePool *coreapi.HCPOpenShiftClusterNodePool) error {
	smiResourceID := cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity
	clusterIdentityURL := cluster.ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL
	subscriptionID := cluster.ID.SubscriptionID

	subnetsClient, err := v.smiClientBuilder.SubnetsClient(ctx, clusterIdentityURL, smiResourceID, subscriptionID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get subnets client as service managed identity: %w", err))
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
			return nil, utils.TrackError(fmt.Errorf("failed to get network security groups client as service managed identity: %w", err))
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
	if clusterSubnetID == nil {
		return utils.TrackError(fmt.Errorf("cluster has no subnet ID"))
	}

	workerSubnet, err := v.getSubnet(ctx, subnetsClient, workerSubnetID)
	if err != nil {
		return utils.TrackError(err)
	}
	workerNSGID, err := v.nsgIDFromSubnet(workerSubnet.Properties)
	if err != nil {
		return utils.TrackError(fmt.Errorf("node pool subnet: %w", err))
	}
	workerPrefixes, err := v.subnetAddressPrefixes(workerSubnet.Properties)
	if err != nil {
		return utils.TrackError(fmt.Errorf("node pool subnet: %w", err))
	}

	// Cluster machine subnet CIDRs
	clusterPrefixes := workerPrefixes
	if !strings.EqualFold(workerSubnetID.String(), clusterSubnetID.String()) {
		clusterSubnet, err := v.getSubnet(ctx, subnetsClient, clusterSubnetID)
		if err != nil {
			return utils.TrackError(fmt.Errorf("cluster subnet: %w", err))
		}
		clusterPrefixes, err = v.subnetAddressPrefixes(clusterSubnet.Properties)
		if err != nil {
			return utils.TrackError(fmt.Errorf("cluster subnet: %w", err))
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
			return utils.TrackError(err)
		}
		vnetIntegrationPrefixes, err = v.subnetAddressPrefixes(vnetIntegrationSubnet.Properties)
		if err != nil {
			return utils.TrackError(fmt.Errorf("vnet-integration subnet: %w", err))
		}
		vnetIntegrationNSGID, err = v.nsgIDFromSubnet(vnetIntegrationSubnet.Properties)
		if err != nil {
			return utils.TrackError(fmt.Errorf("vnet-integration subnet: %w", err))
		}
		outboundDestinationPrefixes = append(outboundDestinationPrefixes, vnetIntegrationPrefixes...)
	}

	if workerNSGID != nil {
		client, err := getNSGClient()
		if err != nil {
			return err
		}
		outbound, _, err := v.listNSGSecurityRules(ctx, client, workerNSGID)
		if err != nil {
			return utils.TrackError(err)
		}
		err = v.validateOutboundNSGRules(outbound, workerPrefixes, outboundDestinationPrefixes, outboundNSGPorts)
		if err != nil {
			return utils.TrackError(err)
		}
	}

	if vnetIntegrationNSGID == nil {
		return nil
	}

	client, err := getNSGClient()
	if err != nil {
		return err
	}
	_, inbound, err := v.listNSGSecurityRules(ctx, client, vnetIntegrationNSGID)
	if err != nil {
		return utils.TrackError(err)
	}
	err = v.validateInboundNSGRules(inbound, workerPrefixes, vnetIntegrationPrefixes, inboundNSGPorts)
	if err != nil {
		return utils.TrackError(err)
	}
	return nil
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
