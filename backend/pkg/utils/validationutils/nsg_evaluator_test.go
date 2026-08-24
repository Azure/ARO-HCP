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
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
)

func mustPrefixes(ss ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(ss))
	for i, s := range ss {
		out[i] = netip.MustParsePrefix(s)
	}
	return out
}

func requireNoNSGRuleViolations(t *testing.T, violations []*nsgRequiredConnectivityViolationDetails, err error) {
	t.Helper()
	require.NoError(t, err)
	require.Empty(t, violations)
}

func requireNSGRuleViolations(t *testing.T, violations []*nsgRequiredConnectivityViolationDetails, err error) []*nsgRequiredConnectivityViolationDetails {
	t.Helper()
	require.NoError(t, err)
	require.NotEmpty(t, violations)
	return violations
}

func nsgBlockingViolationsMessage(violations []*nsgRequiredConnectivityViolationDetails) string {
	return formatNSGRequiredConnectivityViolationsMessage(violations)
}

func azureDefaultOutboundNSGRules() []nsgSecurityRule {
	return []nsgSecurityRule{
		{
			Name: "AllowVnetOutBound", Priority: 65000, Access: securityGroupAccessAllow, Protocol: "*",
			SourceAddressPrefixes: []string{"VirtualNetwork"}, DestinationAddressPrefixes: []string{"VirtualNetwork"},
			SourcePortRanges: []string{"*"}, DestinationPortRanges: []string{"*"},
		},
		{
			Name: "AllowInternetOutBound", Priority: 65001, Access: securityGroupAccessAllow, Protocol: "*",
			SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"Internet"},
			SourcePortRanges: []string{"*"}, DestinationPortRanges: []string{"*"},
		},
		{
			Name: "DenyAllOutBound", Priority: 65500, Access: securityGroupAccessDeny, Protocol: "*",
			SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"*"},
			SourcePortRanges: []string{"*"}, DestinationPortRanges: []string{"*"},
		},
	}
}

func azureDefaultInboundNSGRules() []nsgSecurityRule {
	return []nsgSecurityRule{
		{
			Name: "AllowVnetInBound", Priority: 65000, Access: securityGroupAccessAllow, Protocol: "*",
			SourceAddressPrefixes: []string{"VirtualNetwork"}, DestinationAddressPrefixes: []string{"VirtualNetwork"},
			SourcePortRanges: []string{"*"}, DestinationPortRanges: []string{"*"},
		},
		{
			Name: "AllowAzureLoadBalancerInBound", Priority: 65001, Access: securityGroupAccessAllow, Protocol: "*",
			SourceAddressPrefixes: []string{"AzureLoadBalancer"}, DestinationAddressPrefixes: []string{"*"},
			SourcePortRanges: []string{"*"}, DestinationPortRanges: []string{"*"},
		},
		{
			Name: "DenyAllInBound", Priority: 65500, Access: securityGroupAccessDeny, Protocol: "*",
			SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"*"},
			SourcePortRanges: []string{"*"}, DestinationPortRanges: []string{"*"},
		},
	}
}

func TestValidateOutboundNSGRules_BroadDeny(t *testing.T) {
	t.Parallel()
	ports := []int32{443, 6443}
	subnet := "10.0.0.0/24"

	v := &AzureNodePoolNSGBasedRequiredConnectivityValidation{}

	t.Run("Deny Any to Any fails", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{{
			Name: "DenyAnyAny", Priority: 200, Access: securityGroupAccessDeny, Protocol: "*",
			SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"*"},
			DestinationPortRanges: []string{"*"},
		}}
		violations, err := v.validateOutboundNSGRules(rules, mustPrefixes(subnet), mustPrefixes(subnet), ports)
		blocked := requireNSGRuleViolations(t, violations, err)
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "DenyAnyAny")
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "source Any to destination Any")
	})

	t.Run("higher-priority Allow Any to Any before Deny passes", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{
			{
				Name: "AllowAnyAnyKAS", Priority: 100, Access: securityGroupAccessAllow, Protocol: "Tcp",
				SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"*"},
				DestinationPortRanges: []string{"443", "6443"},
			},
			{
				Name: "DenyAnyAny", Priority: 200, Access: securityGroupAccessDeny, Protocol: "*",
				SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"*"},
				DestinationPortRanges: []string{"*"},
			},
		}
		violations, err := v.validateOutboundNSGRules(rules, mustPrefixes(subnet), mustPrefixes(subnet), ports)
		requireNoNSGRuleViolations(t, violations, err)
	})

	t.Run("higher-priority Allow from worker subnet compensates Deny Any to Any when destinations differ", func(t *testing.T) {
		t.Parallel()
		npSubnet := "10.0.2.0/24"
		clusterSubnet := "10.0.0.0/24"
		rules := []nsgSecurityRule{
			{
				Name: "AllowWorkerAny", Priority: 100, Access: securityGroupAccessAllow, Protocol: "Tcp",
				SourceAddressPrefixes: []string{npSubnet}, DestinationAddressPrefixes: []string{"*"},
				DestinationPortRanges: []string{"443", "6443"},
			},
			{
				Name: "DenyAnyAny", Priority: 200, Access: securityGroupAccessDeny, Protocol: "*",
				SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"*"},
				DestinationPortRanges: []string{"*"},
			},
		}
		// Allow compensation must use worker CIDRs, not destination (cluster) CIDRs.
		violations, err := v.validateOutboundNSGRules(rules, mustPrefixes(npSubnet), mustPrefixes(clusterSubnet), ports)
		requireNoNSGRuleViolations(t, violations, err)
	})

	t.Run("lower-priority Allow Any to Any after Deny does not compensate", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{
			{
				Name: "DenyAnyAny", Priority: 100, Access: securityGroupAccessDeny, Protocol: "*",
				SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"*"},
				DestinationPortRanges: []string{"*"},
			},
			{
				Name: "AllowAnyAnyKAS", Priority: 200, Access: securityGroupAccessAllow, Protocol: "Tcp",
				SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"*"},
				DestinationPortRanges: []string{"443", "6443"},
			},
		}
		violations, err := v.validateOutboundNSGRules(rules, mustPrefixes(subnet), mustPrefixes(subnet), ports)
		blocked := requireNSGRuleViolations(t, violations, err)
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "DenyAnyAny")
	})

	t.Run("Allow to specific IP does not satisfy broad Deny Any to Any", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{
			{
				Name: "AllowSpecific", Priority: 100, Access: securityGroupAccessAllow, Protocol: "Tcp",
				SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"10.0.0.4"},
				DestinationPortRanges: []string{"443", "6443"},
			},
			{
				Name: "DenyAnyAny", Priority: 200, Access: securityGroupAccessDeny, Protocol: "*",
				SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"*"},
				DestinationPortRanges: []string{"*"},
			},
		}
		violations, err := v.validateOutboundNSGRules(rules, mustPrefixes(subnet), mustPrefixes(subnet), ports)
		blocked := requireNSGRuleViolations(t, violations, err)
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "DenyAnyAny")
	})

	t.Run("Deny Any to Any on only 6443 fails", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{{
			Name: "DenyAny6443", Priority: 100, Access: securityGroupAccessDeny, Protocol: "*",
			SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"*"},
			DestinationPortRanges: []string{"6443"},
		}}
		violations, err := v.validateOutboundNSGRules(rules, mustPrefixes(subnet), mustPrefixes(subnet), ports)
		blocked := requireNSGRuleViolations(t, violations, err)
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "DenyAny6443")
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "6443")
	})

	t.Run("Deny Any to Any on only 443 fails", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{{
			Name: "DenyAny443", Priority: 100, Access: securityGroupAccessDeny, Protocol: "*",
			SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"*"},
			DestinationPortRanges: []string{"443"},
		}}
		violations, err := v.validateOutboundNSGRules(rules, mustPrefixes(subnet), mustPrefixes(subnet), ports)
		blocked := requireNSGRuleViolations(t, violations, err)
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "DenyAny443")
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "443")
	})

	t.Run("Deny Any to Any on both 443 and 6443 fails", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{{
			Name: "DenyAnyBoth", Priority: 100, Access: securityGroupAccessDeny, Protocol: "*",
			SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"*"},
			DestinationPortRanges: []string{"443", "6443"},
		}}
		violations, err := v.validateOutboundNSGRules(rules, mustPrefixes(subnet), mustPrefixes(subnet), ports)
		blocked := requireNSGRuleViolations(t, violations, err)
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "DenyAnyBoth")
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "443")
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "6443")
	})

	t.Run("Deny Any to Any on comma-separated 443,6443 fails", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{{
			Name: "DenyAnyComma", Priority: 100, Access: securityGroupAccessDeny, Protocol: "*",
			SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"*"},
			DestinationPortRanges: []string{"443,6443"},
		}}
		violations, err := v.validateOutboundNSGRules(rules, mustPrefixes(subnet), mustPrefixes(subnet), ports)
		blocked := requireNSGRuleViolations(t, violations, err)
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "DenyAnyComma")
	})

	t.Run("UDP-only Deny Any to Any is ignored", func(t *testing.T) {
		t.Parallel()
		rules := append([]nsgSecurityRule{{
			Name: "DenyUDP", Priority: 100, Access: securityGroupAccessDeny, Protocol: "Udp",
			SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"*"},
			DestinationPortRanges: []string{"*"},
		}}, azureDefaultOutboundNSGRules()...)
		violations, err := v.validateOutboundNSGRules(rules, mustPrefixes(subnet), mustPrefixes(subnet), ports)
		requireNoNSGRuleViolations(t, violations, err)
	})

	t.Run("Allow covering only first of multiple worker prefixes does not compensate Deny Any to Any", func(t *testing.T) {
		t.Parallel()
		second := "10.1.0.0/24"
		rules := []nsgSecurityRule{
			{
				Name: "AllowFirstOnly", Priority: 100, Access: securityGroupAccessAllow, Protocol: "Tcp",
				SourceAddressPrefixes: []string{subnet}, DestinationAddressPrefixes: []string{"*"},
				DestinationPortRanges: []string{"443", "6443"},
			},
			{
				Name: "DenyAnyAny", Priority: 200, Access: securityGroupAccessDeny, Protocol: "*",
				SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"*"},
				DestinationPortRanges: []string{"*"},
			},
		}
		violations, err := v.validateOutboundNSGRules(rules, mustPrefixes(subnet, second), mustPrefixes(subnet, second), ports)
		blocked := requireNSGRuleViolations(t, violations, err)
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "DenyAnyAny")
	})
}

func TestValidateOutboundNSGRules_SubnetDeny(t *testing.T) {
	t.Parallel()
	ports := []int32{443, 6443}
	subnet := "10.0.0.0/24"

	v := &AzureNodePoolNSGBasedRequiredConnectivityValidation{}

	t.Run("Deny to subnet CIDR without Allow fails", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{{
			Name: "DenySubnet", Priority: 120, Access: securityGroupAccessDeny, Protocol: "Tcp",
			SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{subnet},
			DestinationPortRanges: []string{"443", "6443"},
		}}
		violations, err := v.validateOutboundNSGRules(rules, mustPrefixes(subnet), mustPrefixes(subnet), ports)
		blocked := requireNSGRuleViolations(t, violations, err)
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "DenySubnet")
		require.Contains(t, nsgBlockingViolationsMessage(blocked), subnet)
	})

	t.Run("UDP-only Deny to subnet is ignored", func(t *testing.T) {
		t.Parallel()
		rules := append([]nsgSecurityRule{{
			Name: "DenyUDPSubnet", Priority: 120, Access: securityGroupAccessDeny, Protocol: "Udp",
			SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{subnet},
			DestinationPortRanges: []string{"443", "6443"},
		}}, azureDefaultOutboundNSGRules()...)
		violations, err := v.validateOutboundNSGRules(rules, mustPrefixes(subnet), mustPrefixes(subnet), ports)
		requireNoNSGRuleViolations(t, violations, err)
	})

	t.Run("Deny to IP inside subnet without Allow fails", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{{
			Name: "DenyILB", Priority: 110, Access: securityGroupAccessDeny, Protocol: "Tcp",
			SourceAddressPrefixes: []string{"VirtualNetwork"}, DestinationAddressPrefixes: []string{"10.0.0.4"},
			DestinationPortRanges: []string{"443", "6443"},
		}}
		violations, err := v.validateOutboundNSGRules(rules, mustPrefixes(subnet), mustPrefixes(subnet), ports)
		blocked := requireNSGRuleViolations(t, violations, err)
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "DenyILB")
	})

	t.Run("higher-priority Allow to IP before Deny to IP fails without full subnet cover", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{
			{
				Name: "AllowILB", Priority: 100, Access: securityGroupAccessAllow, Protocol: "Tcp",
				SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"10.0.0.4"},
				DestinationPortRanges: []string{"443", "6443"},
			},
			{
				Name: "DenyILB", Priority: 110, Access: securityGroupAccessDeny, Protocol: "Tcp",
				SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"10.0.0.4"},
				DestinationPortRanges: []string{"443", "6443"},
			},
		}
		violations, err := v.validateOutboundNSGRules(rules, mustPrefixes(subnet), mustPrefixes(subnet), ports)
		blocked := requireNSGRuleViolations(t, violations, err)
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "DenyILB")
	})

	t.Run("higher-priority Allow Any to Any satisfies Deny to IP", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{
			{
				Name: "AllowAnyAny", Priority: 100, Access: securityGroupAccessAllow, Protocol: "Tcp",
				SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"*"},
				DestinationPortRanges: []string{"443", "6443"},
			},
			{
				Name: "DenyILB", Priority: 110, Access: securityGroupAccessDeny, Protocol: "Tcp",
				SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"10.0.0.4"},
				DestinationPortRanges: []string{"443", "6443"},
			},
		}
		violations, err := v.validateOutboundNSGRules(rules, mustPrefixes(subnet), mustPrefixes(subnet), ports)
		requireNoNSGRuleViolations(t, violations, err)
	})

	t.Run("split subnet Allows compensate Deny to full subnet", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{
			{
				Name: "AllowLowerHalf", Priority: 100, Access: securityGroupAccessAllow, Protocol: "*",
				SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"10.0.0.0/25"},
				DestinationPortRanges: []string{"*"},
			},
			{
				Name: "AllowUpperHalf", Priority: 101, Access: securityGroupAccessAllow, Protocol: "*",
				SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"10.0.0.128/25"},
				DestinationPortRanges: []string{"*"},
			},
			{
				Name: "DenySubnet", Priority: 102, Access: securityGroupAccessDeny, Protocol: "*",
				SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{subnet},
				DestinationPortRanges: []string{"*"},
			},
		}
		violations, err := v.validateOutboundNSGRules(rules, mustPrefixes(subnet), mustPrefixes(subnet), ports)
		requireNoNSGRuleViolations(t, violations, err)
	})

	t.Run("non-partitioning split Allows do not compensate Deny to full subnet", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{
			{
				Name: "AllowLowerHalf", Priority: 100, Access: securityGroupAccessAllow, Protocol: "*",
				SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"10.0.0.0/26"},
				DestinationPortRanges: []string{"*"},
			},
			{
				Name: "AllowUpperHalf", Priority: 101, Access: securityGroupAccessAllow, Protocol: "*",
				SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"10.0.0.128/26"},
				DestinationPortRanges: []string{"*"},
			},
			{
				Name: "DenySubnet", Priority: 102, Access: securityGroupAccessDeny, Protocol: "*",
				SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{subnet},
				DestinationPortRanges: []string{"*"},
			},
		}
		violations, err := v.validateOutboundNSGRules(rules, mustPrefixes(subnet), mustPrefixes(subnet), ports)
		blocked := requireNSGRuleViolations(t, violations, err)
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "DenySubnet")
	})

	t.Run("non-partitioning split Allows does not cover full subnet", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{
			{
				Name: "AllowLowerHalf", Priority: 100, Access: securityGroupAccessAllow, Protocol: "*",
				SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"10.0.0.0/26"},
				DestinationPortRanges: []string{"*"},
			},
			{
				Name: "AllowUpperHalf", Priority: 101, Access: securityGroupAccessAllow, Protocol: "*",
				SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"10.0.0.128/26"},
				DestinationPortRanges: []string{"*"},
			},
		}
		violations, err := v.validateOutboundNSGRules(rules, mustPrefixes(subnet), mustPrefixes(subnet), ports)
		blocked := requireNSGRuleViolations(t, violations, err)
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "No Allow")
	})

	t.Run("higher-priority Allow to subnet CIDR covers Deny to IP", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{
			{
				Name: "AllowSubnet", Priority: 100, Access: securityGroupAccessAllow, Protocol: "Tcp",
				SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{subnet},
				DestinationPortRanges: []string{"443", "6443"},
			},
			{
				Name: "DenyILB", Priority: 110, Access: securityGroupAccessDeny, Protocol: "Tcp",
				SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"10.0.0.4"},
				DestinationPortRanges: []string{"443", "6443"},
			},
		}
		violations, err := v.validateOutboundNSGRules(rules, mustPrefixes(subnet), mustPrefixes(subnet), ports)
		requireNoNSGRuleViolations(t, violations, err)
	})

	t.Run("Deny to IP outside subnet is ignored", func(t *testing.T) {
		t.Parallel()
		rules := append([]nsgSecurityRule{{
			Name: "DenyOther", Priority: 110, Access: securityGroupAccessDeny, Protocol: "Tcp",
			SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"10.0.1.4"},
			DestinationPortRanges: []string{"443", "6443"},
		}}, azureDefaultOutboundNSGRules()...)
		violations, err := v.validateOutboundNSGRules(rules, mustPrefixes(subnet), mustPrefixes(subnet), ports)
		requireNoNSGRuleViolations(t, violations, err)
	})

	t.Run("Deny with source CIDR outside worker subnet is ignored", func(t *testing.T) {
		t.Parallel()
		rules := append([]nsgSecurityRule{{
			Name: "DenyUnrelatedSource", Priority: 110, Access: securityGroupAccessDeny, Protocol: "Tcp",
			SourceAddressPrefixes: []string{"192.168.99.0/24"}, DestinationAddressPrefixes: []string{subnet},
			DestinationPortRanges: []string{"443", "6443"},
		}}, azureDefaultOutboundNSGRules()...)
		violations, err := v.validateOutboundNSGRules(rules, mustPrefixes(subnet), mustPrefixes(subnet), ports)
		requireNoNSGRuleViolations(t, violations, err)
	})

	t.Run("Allow with source CIDR outside worker subnet does not satisfy Deny", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{
			{
				Name: "AllowUnrelatedSource", Priority: 100, Access: securityGroupAccessAllow, Protocol: "Tcp",
				SourceAddressPrefixes: []string{"192.168.99.0/24"}, DestinationAddressPrefixes: []string{"10.0.0.4"},
				DestinationPortRanges: []string{"443", "6443"},
			},
			{
				Name: "DenyILB", Priority: 110, Access: securityGroupAccessDeny, Protocol: "Tcp",
				SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"10.0.0.4"},
				DestinationPortRanges: []string{"443", "6443"},
			},
		}
		violations, err := v.validateOutboundNSGRules(rules, mustPrefixes(subnet), mustPrefixes(subnet), ports)
		blocked := requireNSGRuleViolations(t, violations, err)
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "DenyILB")
	})

	t.Run("single Allow on different subnet does not satisfy Deny/Allow", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{
			{
				Name: "AllowOtherSubnet", Priority: 100, Access: securityGroupAccessAllow, Protocol: "Tcp",
				SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"10.0.5.0/24"},
				DestinationPortRanges: []string{"443", "6443"},
			},
		}
		violations, err := v.validateOutboundNSGRules(rules, mustPrefixes(subnet), mustPrefixes(subnet), ports)
		blocked := requireNSGRuleViolations(t, violations, err)
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "No Allow")
	})

	t.Run("Deny with source overlapping worker subnet fails", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{{
			Name: "DenyWorkerSource", Priority: 110, Access: securityGroupAccessDeny, Protocol: "Tcp",
			SourceAddressPrefixes: []string{subnet}, DestinationAddressPrefixes: []string{"10.0.0.4"},
			DestinationPortRanges: []string{"443", "6443"},
		}}
		violations, err := v.validateOutboundNSGRules(rules, mustPrefixes(subnet), mustPrefixes(subnet), ports)
		blocked := requireNSGRuleViolations(t, violations, err)
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "DenyWorkerSource")
	})

	t.Run("no rule return no allow violation", func(t *testing.T) {
		t.Parallel()
		violations, err := v.validateOutboundNSGRules(nil, mustPrefixes(subnet), mustPrefixes(subnet), ports)
		blocked := requireNSGRuleViolations(t, violations, err)
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "No Allow")
	})

	t.Run("Deny targeting second of multiple worker subnet prefixes fails", func(t *testing.T) {
		t.Parallel()
		second := "10.1.0.0/24"
		rules := []nsgSecurityRule{{
			Name: "DenySecondPrefix", Priority: 110, Access: securityGroupAccessDeny, Protocol: "Tcp",
			SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"10.1.0.4"},
			DestinationPortRanges: []string{"443", "6443"},
		}}
		violations, err := v.validateOutboundNSGRules(rules, mustPrefixes(subnet, second), mustPrefixes(subnet, second), ports)
		blocked := requireNSGRuleViolations(t, violations, err)
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "DenySecondPrefix")
		require.Contains(t, nsgBlockingViolationsMessage(blocked), second)
	})

	t.Run("Deny Any to cluster subnet CIDR fails when destinations are cluster only", func(t *testing.T) {
		t.Parallel()
		clusterSubnet := "10.0.0.0/24"
		npSubnet := "10.0.2.0/24"
		rules := []nsgSecurityRule{{
			Name: "DenyClusterSubnet", Priority: 101, Access: securityGroupAccessDeny, Protocol: "Tcp",
			SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{clusterSubnet},
			DestinationPortRanges: []string{"443", "6443"},
		}}
		violations, err := v.validateOutboundNSGRules(rules, mustPrefixes(npSubnet), mustPrefixes(clusterSubnet), ports)
		blocked := requireNSGRuleViolations(t, violations, err)
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "DenyClusterSubnet")
		require.Contains(t, nsgBlockingViolationsMessage(blocked), clusterSubnet)
	})

	t.Run("Deny Any to cluster subnet IP fails when destinations are cluster only", func(t *testing.T) {
		t.Parallel()
		clusterSubnet := "10.0.0.0/24"
		npSubnet := "10.0.2.0/24"
		rules := []nsgSecurityRule{{
			Name: "DenyClusterIP", Priority: 101, Access: securityGroupAccessDeny, Protocol: "Tcp",
			SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"10.0.0.10"},
			DestinationPortRanges: []string{"443", "6443"},
		}}
		violations, err := v.validateOutboundNSGRules(rules, mustPrefixes(npSubnet), mustPrefixes(clusterSubnet), ports)
		blocked := requireNSGRuleViolations(t, violations, err)
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "DenyClusterIP")
	})

	t.Run("Deny Any to node-pool subnet prefix is ignored when destinations are nodepool subnet only", func(t *testing.T) {
		t.Parallel()
		clusterSubnet := "10.0.0.0/24"
		npSubnet := "10.0.2.0/24"
		rules := append([]nsgSecurityRule{{
			Name: "DenyNPSubnet", Priority: 101, Access: securityGroupAccessDeny, Protocol: "Tcp",
			SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{npSubnet},
			DestinationPortRanges: []string{"443", "6443"},
		}}, azureDefaultOutboundNSGRules()...)
		violations, err := v.validateOutboundNSGRules(rules, mustPrefixes(npSubnet), mustPrefixes(clusterSubnet), ports)
		requireNoNSGRuleViolations(t, violations, err)
	})

	t.Run("Deny worker subnet to distinct cluster subnet fails", func(t *testing.T) {
		t.Parallel()
		clusterSubnet := "10.0.0.0/24"
		npSubnet := "10.0.2.0/24"
		rules := []nsgSecurityRule{{
			Name: "DenyWorkerToCluster", Priority: 101, Access: securityGroupAccessDeny, Protocol: "Tcp",
			SourceAddressPrefixes: []string{npSubnet}, DestinationAddressPrefixes: []string{clusterSubnet},
			DestinationPortRanges: []string{"443", "6443"},
		}}
		violations, err := v.validateOutboundNSGRules(rules, mustPrefixes(npSubnet), mustPrefixes(clusterSubnet), ports)
		blocked := requireNSGRuleViolations(t, violations, err)
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "DenyWorkerToCluster")
		require.Contains(t, nsgBlockingViolationsMessage(blocked), clusterSubnet)
	})

	t.Run("outbound Denys to SWIFT are ignored when destinations are cluster only", func(t *testing.T) {
		t.Parallel()
		npSubnet := "10.0.2.0/24"
		clusterSubnet := "10.0.0.0/24"
		swiftSubnet := "10.0.1.0/24"
		swiftIP := "10.0.1.4"

		cases := []struct {
			name string
			rule nsgSecurityRule
		}{
			{
				name: "Any to SWIFT subnet",
				rule: nsgSecurityRule{
					Name: "DenyAnyToSwift", Priority: 101, Access: securityGroupAccessDeny, Protocol: "Tcp",
					SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{swiftSubnet},
					DestinationPortRanges: []string{"443", "6443"},
				},
			},
			{
				name: "worker to SWIFT subnet",
				rule: nsgSecurityRule{
					Name: "DenyWorkerToSwift", Priority: 101, Access: securityGroupAccessDeny, Protocol: "Tcp",
					SourceAddressPrefixes: []string{npSubnet}, DestinationAddressPrefixes: []string{swiftSubnet},
					DestinationPortRanges: []string{"443", "6443"},
				},
			},
			{
				name: "Any to SWIFT IP",
				rule: nsgSecurityRule{
					Name: "DenyAnyToSwiftIP", Priority: 101, Access: securityGroupAccessDeny, Protocol: "Tcp",
					SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{swiftIP},
					DestinationPortRanges: []string{"443", "6443"},
				},
			},
			{
				name: "worker to SWIFT IP",
				rule: nsgSecurityRule{
					Name: "DenyWorkerToSwiftIP", Priority: 101, Access: securityGroupAccessDeny, Protocol: "Tcp",
					SourceAddressPrefixes: []string{npSubnet}, DestinationAddressPrefixes: []string{swiftIP},
					DestinationPortRanges: []string{"443", "6443"},
				},
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				violations, err := v.validateOutboundNSGRules(append([]nsgSecurityRule{tc.rule}, azureDefaultOutboundNSGRules()...), mustPrefixes(npSubnet), mustPrefixes(clusterSubnet), ports)
				requireNoNSGRuleViolations(t, violations, err)
			})
		}
	})

	t.Run("Deny worker subnet to vnet-integration is ignored when destinations are cluster only", func(t *testing.T) {
		t.Parallel()
		npSubnet := "10.0.2.0/24"
		clusterSubnet := "10.0.0.0/24"
		integration := "10.0.1.0/24"
		rules := append([]nsgSecurityRule{{
			Name: "DenyWorkerToIntegration", Priority: 101, Access: securityGroupAccessDeny, Protocol: "Tcp",
			SourceAddressPrefixes: []string{npSubnet}, DestinationAddressPrefixes: []string{integration},
			DestinationPortRanges: []string{"443", "6443"},
		}}, azureDefaultOutboundNSGRules()...)
		violations, err := v.validateOutboundNSGRules(rules, mustPrefixes(npSubnet), mustPrefixes(clusterSubnet), ports)
		requireNoNSGRuleViolations(t, violations, err)
	})

	t.Run("Deny unrelated source to cluster subnet is ignored", func(t *testing.T) {
		t.Parallel()
		clusterSubnet := "10.0.0.0/24"
		npSubnet := "10.0.2.0/24"
		unrelated := "10.9.0.0/24"
		rules := append([]nsgSecurityRule{{
			Name: "DenyUnrelated", Priority: 101, Access: securityGroupAccessDeny, Protocol: "Tcp",
			SourceAddressPrefixes: []string{unrelated}, DestinationAddressPrefixes: []string{clusterSubnet},
			DestinationPortRanges: []string{"443", "6443"},
		}}, azureDefaultOutboundNSGRules()...)
		violations, err := v.validateOutboundNSGRules(rules, mustPrefixes(npSubnet), mustPrefixes(clusterSubnet), ports)
		requireNoNSGRuleViolations(t, violations, err)
	})

	t.Run("Deny worker subnet to Any fails", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{{
			Name: "DenyWorkerToAny", Priority: 110, Access: securityGroupAccessDeny, Protocol: "Tcp",
			SourceAddressPrefixes: []string{subnet}, DestinationAddressPrefixes: []string{"*"},
			DestinationPortRanges: []string{"443", "6443"},
		}}
		violations, err := v.validateOutboundNSGRules(rules, mustPrefixes(subnet), mustPrefixes(subnet), ports)
		blocked := requireNSGRuleViolations(t, violations, err)
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "DenyWorkerToAny")
	})

	t.Run("Deny node-pool subnet to Any fails when destinations are cluster only", func(t *testing.T) {
		t.Parallel()
		clusterSubnet := "10.0.0.0/24"
		npSubnet := "10.0.2.0/24"
		rules := []nsgSecurityRule{{
			Name: "DenyNPToAny", Priority: 101, Access: securityGroupAccessDeny, Protocol: "Tcp",
			SourceAddressPrefixes: []string{npSubnet}, DestinationAddressPrefixes: []string{"*"},
			DestinationPortRanges: []string{"443", "6443"},
		}}
		violations, err := v.validateOutboundNSGRules(rules, mustPrefixes(npSubnet), mustPrefixes(clusterSubnet), ports)
		blocked := requireNSGRuleViolations(t, violations, err)
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "DenyNPToAny")
	})
}

func TestValidateOutboundNSGRules_NodePoolSubnet(t *testing.T) {
	t.Parallel()
	ports := []int32{443, 6443}
	npSubnet := "10.0.2.0/24"
	clusterSubnet := "10.0.0.0/24"

	v := &AzureNodePoolNSGBasedRequiredConnectivityValidation{}

	t.Run("Deny Any to Any fails", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{{
			Name: "DenyAnyAny", Priority: 200, Access: securityGroupAccessDeny, Protocol: "*",
			SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"*"},
			DestinationPortRanges: []string{"*"},
		}}
		violations, err := v.validateOutboundNSGRules(rules, mustPrefixes(npSubnet), mustPrefixes(clusterSubnet), ports)
		blocked := requireNSGRuleViolations(t, violations, err)
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "DenyAnyAny")
	})
}

func TestValidateInboundNSGRules(t *testing.T) {
	t.Parallel()
	subnet := "10.0.0.0/24"
	ports := inboundNSGPorts

	v := &AzureNodePoolNSGBasedRequiredConnectivityValidation{}

	t.Run("Deny Any to Any fails", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{{
			Name: "DenyAnyAnyIn", Priority: 200, Access: securityGroupAccessDeny, Protocol: "*",
			SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"*"},
			DestinationPortRanges: []string{"*"},
		}}
		violations, err := v.validateInboundNSGRules(rules, mustPrefixes(subnet), mustPrefixes(subnet), ports)
		blocked := requireNSGRuleViolations(t, violations, err)
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "DenyAnyAnyIn")
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "source Any to destination Any")
	})

	t.Run("Deny Any to vnet-integration subnet fails", func(t *testing.T) {
		t.Parallel()
		worker := "10.0.0.0/24"
		integration := "10.0.1.0/24"
		rules := []nsgSecurityRule{{
			Name: "DenyAnyToIntegration", Priority: 200, Access: securityGroupAccessDeny, Protocol: "Tcp",
			SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{integration},
			DestinationPortRanges: []string{"8443", "443"},
		}}
		violations, err := v.validateInboundNSGRules(rules, mustPrefixes(worker), mustPrefixes(integration), ports)
		blocked := requireNSGRuleViolations(t, violations, err)
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "DenyAnyToIntegration")
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "source Any to destination VirtualNetwork/vnet-integration")
	})

	t.Run("Deny Any to vnet-integration subnet on star ports fails", func(t *testing.T) {
		t.Parallel()
		worker := "10.0.0.0/24"
		integration := "10.0.1.0/24"
		rules := []nsgSecurityRule{{
			Name: "DenyAnyToIntegrationStar", Priority: 200, Access: securityGroupAccessDeny, Protocol: "*",
			SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{integration},
			DestinationPortRanges: []string{"*"},
		}}
		violations, err := v.validateInboundNSGRules(rules, mustPrefixes(worker), mustPrefixes(integration), ports)
		blocked := requireNSGRuleViolations(t, violations, err)
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "DenyAnyToIntegrationStar")
	})

	t.Run("Deny Any to VirtualNetwork fails", func(t *testing.T) {
		t.Parallel()
		worker := "10.0.0.0/24"
		integration := "10.0.1.0/24"
		rules := []nsgSecurityRule{{
			Name: "DenyAnyToVNet", Priority: 200, Access: securityGroupAccessDeny, Protocol: "Tcp",
			SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"VirtualNetwork"},
			DestinationPortRanges: []string{"8443", "443"},
		}}
		violations, err := v.validateInboundNSGRules(rules, mustPrefixes(worker), mustPrefixes(integration), ports)
		blocked := requireNSGRuleViolations(t, violations, err)
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "DenyAnyToVNet")
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "source Any to destination VirtualNetwork/vnet-integration")
	})

	t.Run("Deny Any to non-integration subnet is ignored", func(t *testing.T) {
		t.Parallel()
		worker := "10.0.0.0/24"
		integration := "10.0.1.0/24"
		other := "10.0.2.0/24"
		rules := append([]nsgSecurityRule{{
			Name: "DenyAnyToOther", Priority: 200, Access: securityGroupAccessDeny, Protocol: "Tcp",
			SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{other},
			DestinationPortRanges: []string{"8443", "443"},
		}}, azureDefaultInboundNSGRules()...)
		violations, err := v.validateInboundNSGRules(rules, mustPrefixes(worker), mustPrefixes(integration), ports)
		requireNoNSGRuleViolations(t, violations, err)
	})

	t.Run("non-partitioning split Allows does not cover full vnet-integration subnet", func(t *testing.T) {
		t.Parallel()
		worker := "10.0.0.0/24"
		integration := "10.0.1.0/24"
		rules := []nsgSecurityRule{
			{
				Name: "AllowLowerHalf", Priority: 100, Access: securityGroupAccessAllow, Protocol: "*",
				SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"10.0.1.0/26"},
				DestinationPortRanges: []string{"*"},
			},
			{
				Name: "AllowUpperHalf", Priority: 101, Access: securityGroupAccessAllow, Protocol: "*",
				SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"10.0.1.128/26"},
				DestinationPortRanges: []string{"*"},
			},
		}
		violations, err := v.validateInboundNSGRules(rules, mustPrefixes(worker), mustPrefixes(integration), ports)
		blocked := requireNSGRuleViolations(t, violations, err)
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "No Allow")
	})

	t.Run("higher-priority Allow Any to Any compensates Deny Any to vnet-integration", func(t *testing.T) {
		t.Parallel()
		worker := "10.0.0.0/24"
		integration := "10.0.1.0/24"
		rules := []nsgSecurityRule{
			{
				Name: "AllowAnyAny", Priority: 100, Access: securityGroupAccessAllow, Protocol: "*",
				SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"*"},
				DestinationPortRanges: []string{"*"},
			},
			{
				Name: "DenyAnyToIntegration", Priority: 200, Access: securityGroupAccessDeny, Protocol: "Tcp",
				SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{integration},
				DestinationPortRanges: []string{"8443", "443"},
			},
		}
		violations, err := v.validateInboundNSGRules(rules, mustPrefixes(worker), mustPrefixes(integration), ports)
		requireNoNSGRuleViolations(t, violations, err)
	})

	t.Run("Deny worker subnet to Any fails", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{{
			Name: "DenyWorkerToAny", Priority: 200, Access: securityGroupAccessDeny, Protocol: "Tcp",
			SourceAddressPrefixes: []string{subnet}, DestinationAddressPrefixes: []string{"*"},
			DestinationPortRanges: []string{"*"},
		}}
		violations, err := v.validateInboundNSGRules(rules, mustPrefixes(subnet), mustPrefixes(subnet), ports)
		blocked := requireNSGRuleViolations(t, violations, err)
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "DenyWorkerToAny")
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "destination Any")
	})

	t.Run("Deny worker subnet to VirtualNetwork fails", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{{
			Name: "DenyWorkerToVNet", Priority: 200, Access: securityGroupAccessDeny, Protocol: "Tcp",
			SourceAddressPrefixes: []string{subnet}, DestinationAddressPrefixes: []string{"VirtualNetwork"},
			DestinationPortRanges: []string{"*"},
		}}
		violations, err := v.validateInboundNSGRules(rules, mustPrefixes(subnet), mustPrefixes(subnet), ports)
		blocked := requireNSGRuleViolations(t, violations, err)
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "DenyWorkerToVNet")
	})

	t.Run("Deny worker to distinct vnet-integration subnet fails", func(t *testing.T) {
		t.Parallel()
		worker := "10.0.0.0/24"
		integration := "10.0.1.0/24"
		rules := []nsgSecurityRule{{
			Name: "DenyWorkerToIntegration", Priority: 200, Access: securityGroupAccessDeny, Protocol: "Tcp",
			SourceAddressPrefixes: []string{worker}, DestinationAddressPrefixes: []string{integration},
			DestinationPortRanges: []string{"8443", "443"},
		}}
		violations, err := v.validateInboundNSGRules(rules, mustPrefixes(worker), mustPrefixes(integration), ports)
		blocked := requireNSGRuleViolations(t, violations, err)
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "DenyWorkerToIntegration")
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "vnet-integration")
	})

	t.Run("Deny worker to non-integration subnet is ignored when integration differs", func(t *testing.T) {
		t.Parallel()
		worker := "10.0.0.0/24"
		integration := "10.0.1.0/24"
		other := "10.0.2.0/24"
		rules := append([]nsgSecurityRule{{
			Name: "DenyWorkerToOther", Priority: 200, Access: securityGroupAccessDeny, Protocol: "Tcp",
			SourceAddressPrefixes: []string{worker}, DestinationAddressPrefixes: []string{other},
			DestinationPortRanges: []string{"8443", "443"},
		}}, azureDefaultInboundNSGRules()...)
		violations, err := v.validateInboundNSGRules(rules, mustPrefixes(worker), mustPrefixes(integration), ports)
		requireNoNSGRuleViolations(t, violations, err)
	})

	t.Run("single Allow on different subnet does not satisfy Deny", func(t *testing.T) {
		t.Parallel()
		worker := "10.0.0.0/24"
		integration := "10.0.1.0/24"
		rules := []nsgSecurityRule{
			{
				Name: "AllowOtherSubnet", Priority: 100, Access: securityGroupAccessAllow, Protocol: "Tcp",
				SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"10.0.2.0/24"},
				DestinationPortRanges: []string{"8443", "443"},
			},
			{
				Name: "DenyIntegration", Priority: 200, Access: securityGroupAccessDeny, Protocol: "Tcp",
				SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{integration},
				DestinationPortRanges: []string{"8443", "443"},
			},
		}
		violations, err := v.validateInboundNSGRules(rules, mustPrefixes(worker), mustPrefixes(integration), ports)
		blocked := requireNSGRuleViolations(t, violations, err)
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "DenyIntegration")
	})

	t.Run("Deny node-pool subnet to Any fails", func(t *testing.T) {
		t.Parallel()
		npSubnet := "10.0.2.0/24"
		integration := "10.0.1.0/24"
		rules := []nsgSecurityRule{{
			Name: "DenyNPToAny", Priority: 200, Access: securityGroupAccessDeny, Protocol: "Tcp",
			SourceAddressPrefixes: []string{npSubnet}, DestinationAddressPrefixes: []string{"*"},
			DestinationPortRanges: []string{"8443", "443"},
		}}
		violations, err := v.validateInboundNSGRules(rules, mustPrefixes(npSubnet), mustPrefixes(integration), ports)
		blocked := requireNSGRuleViolations(t, violations, err)
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "DenyNPToAny")
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "destination Any")
	})

	t.Run("Deny node-pool subnet to vnet-integration subnet fails", func(t *testing.T) {
		t.Parallel()
		npSubnet := "10.0.2.0/24"
		integration := "10.0.1.0/24"
		rules := []nsgSecurityRule{{
			Name: "DenyNPToIntegration", Priority: 200, Access: securityGroupAccessDeny, Protocol: "Tcp",
			SourceAddressPrefixes: []string{npSubnet}, DestinationAddressPrefixes: []string{integration},
			DestinationPortRanges: []string{"8443", "443"},
		}}
		violations, err := v.validateInboundNSGRules(rules, mustPrefixes(npSubnet), mustPrefixes(integration), ports)
		blocked := requireNSGRuleViolations(t, violations, err)
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "DenyNPToIntegration")
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "vnet-integration")
	})

	t.Run("Deny worker IP to worker subnet fails", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{{
			Name: "DenyWorkerToSubnet", Priority: 200, Access: securityGroupAccessDeny, Protocol: "Tcp",
			SourceAddressPrefixes: []string{"10.0.0.5"}, DestinationAddressPrefixes: []string{subnet},
			DestinationPortRanges: []string{"*"},
		}}
		violations, err := v.validateInboundNSGRules(rules, mustPrefixes(subnet), mustPrefixes(subnet), ports)
		blocked := requireNSGRuleViolations(t, violations, err)
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "DenyWorkerToSubnet")
	})

	t.Run("higher-priority Allow Any to Any compensates Deny Any to Any", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{
			{
				Name: "AllowAnyAny", Priority: 100, Access: securityGroupAccessAllow, Protocol: "*",
				SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"*"},
				DestinationPortRanges: []string{"*"},
			},
			{
				Name: "DenyAnyAnyIn", Priority: 200, Access: securityGroupAccessDeny, Protocol: "*",
				SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"*"},
				DestinationPortRanges: []string{"*"},
			},
		}
		violations, err := v.validateInboundNSGRules(rules, mustPrefixes(subnet), mustPrefixes(subnet), ports)
		requireNoNSGRuleViolations(t, violations, err)
	})

	t.Run("higher-priority Allow Any to Any compensates Deny worker to Any", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{
			{
				Name: "AllowAnyAny", Priority: 100, Access: securityGroupAccessAllow, Protocol: "Tcp",
				SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"*"},
				DestinationPortRanges: []string{"*"},
			},
			{
				Name: "DenyWorkerToAny", Priority: 200, Access: securityGroupAccessDeny, Protocol: "Tcp",
				SourceAddressPrefixes: []string{subnet}, DestinationAddressPrefixes: []string{"*"},
				DestinationPortRanges: []string{"*"},
			},
		}
		violations, err := v.validateInboundNSGRules(rules, mustPrefixes(subnet), mustPrefixes(subnet), ports)
		requireNoNSGRuleViolations(t, violations, err)
	})

	t.Run("lower-priority Allow does not compensate Deny", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{
			{
				Name: "DenyAnyAnyIn", Priority: 100, Access: securityGroupAccessDeny, Protocol: "*",
				SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"*"},
				DestinationPortRanges: []string{"*"},
			},
			{
				Name: "AllowAnyAny", Priority: 200, Access: securityGroupAccessAllow, Protocol: "*",
				SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"*"},
				DestinationPortRanges: []string{"*"},
			},
		}
		violations, err := v.validateInboundNSGRules(rules, mustPrefixes(subnet), mustPrefixes(subnet), ports)
		blocked := requireNSGRuleViolations(t, violations, err)
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "DenyAnyAnyIn")
	})

	t.Run("Deny from other subnet to Any is ignored", func(t *testing.T) {
		t.Parallel()
		rules := append([]nsgSecurityRule{{
			Name: "DenyOtherToAny", Priority: 200, Access: securityGroupAccessDeny, Protocol: "Tcp",
			SourceAddressPrefixes: []string{"10.0.1.0/24"}, DestinationAddressPrefixes: []string{"*"},
			DestinationPortRanges: []string{"*"},
		}}, azureDefaultInboundNSGRules()...)
		violations, err := v.validateInboundNSGRules(rules, mustPrefixes(subnet), mustPrefixes(subnet), ports)
		requireNoNSGRuleViolations(t, violations, err)
	})

	t.Run("UDP Deny Any to Any is ignored", func(t *testing.T) {
		t.Parallel()
		rules := append([]nsgSecurityRule{{
			Name: "DenyUDP", Priority: 200, Access: securityGroupAccessDeny, Protocol: "Udp",
			SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"*"},
			DestinationPortRanges: []string{"*"},
		}}, azureDefaultInboundNSGRules()...)
		violations, err := v.validateInboundNSGRules(rules, mustPrefixes(subnet), mustPrefixes(subnet), ports)
		requireNoNSGRuleViolations(t, violations, err)
	})

	t.Run("Deny Any to Any on unrelated port 80 is ignored", func(t *testing.T) {
		t.Parallel()
		rules := append([]nsgSecurityRule{{
			Name: "DenyPort80", Priority: 200, Access: securityGroupAccessDeny, Protocol: "Tcp",
			SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"*"},
			DestinationPortRanges: []string{"80"},
		}}, azureDefaultInboundNSGRules()...)
		violations, err := v.validateInboundNSGRules(rules, mustPrefixes(subnet), mustPrefixes(subnet), ports)
		requireNoNSGRuleViolations(t, violations, err)
	})

	t.Run("Deny worker to VirtualNetwork on only 8443 fails", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{{
			Name: "Deny8443", Priority: 200, Access: securityGroupAccessDeny, Protocol: "Tcp",
			SourceAddressPrefixes: []string{subnet}, DestinationAddressPrefixes: []string{"VirtualNetwork"},
			DestinationPortRanges: []string{"8443"},
		}}
		violations, err := v.validateInboundNSGRules(rules, mustPrefixes(subnet), mustPrefixes(subnet), ports)
		blocked := requireNSGRuleViolations(t, violations, err)
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "Deny8443")
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "8443")
	})

	t.Run("Deny worker to VirtualNetwork on only 443 fails", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{{
			Name: "Deny443", Priority: 200, Access: securityGroupAccessDeny, Protocol: "Tcp",
			SourceAddressPrefixes: []string{subnet}, DestinationAddressPrefixes: []string{"VirtualNetwork"},
			DestinationPortRanges: []string{"443"},
		}}
		violations, err := v.validateInboundNSGRules(rules, mustPrefixes(subnet), mustPrefixes(subnet), ports)
		blocked := requireNSGRuleViolations(t, violations, err)
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "Deny443")
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "443")
	})

	t.Run("empty vnet-integration CIDRs returns error", func(t *testing.T) {
		t.Parallel()
		violations, err := v.validateInboundNSGRules(nil, mustPrefixes(subnet), nil, ports)
		require.Error(t, err)
		require.Nil(t, violations)
		require.Contains(t, err.Error(), "vnet-integration subnet")
	})

	t.Run("Allow covering only first worker prefix does not compensate Deny of second", func(t *testing.T) {
		t.Parallel()
		second := "10.1.0.0/24"
		integration := "10.2.0.0/24"
		rules := []nsgSecurityRule{
			{
				Name: "AllowFirstOnly", Priority: 100, Access: securityGroupAccessAllow, Protocol: "Tcp",
				SourceAddressPrefixes: []string{subnet}, DestinationAddressPrefixes: []string{"*"},
				DestinationPortRanges: []string{"*"},
			},
			{
				Name: "DenySecondToAny", Priority: 200, Access: securityGroupAccessDeny, Protocol: "Tcp",
				SourceAddressPrefixes: []string{second}, DestinationAddressPrefixes: []string{"*"},
				DestinationPortRanges: []string{"*"},
			},
		}
		violations, err := v.validateInboundNSGRules(rules, mustPrefixes(subnet, second), mustPrefixes(integration), ports)
		blocked := requireNSGRuleViolations(t, violations, err)
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "DenySecondToAny")
	})

	t.Run("Allow covering second worker prefix compensates Deny of second", func(t *testing.T) {
		t.Parallel()
		worker := "10.0.0.0/24"
		integration := "10.2.0.0/24"
		rules := []nsgSecurityRule{
			{
				Name: "AllowWorker", Priority: 100, Access: securityGroupAccessAllow, Protocol: "Tcp",
				SourceAddressPrefixes: []string{worker}, DestinationAddressPrefixes: []string{"*"},
				DestinationPortRanges: []string{"*"},
			},
			{
				Name: "DenyWorkerToAny", Priority: 200, Access: securityGroupAccessDeny, Protocol: "Tcp",
				SourceAddressPrefixes: []string{worker}, DestinationAddressPrefixes: []string{"*"},
				DestinationPortRanges: []string{"*"},
			},
		}
		violations, err := v.validateInboundNSGRules(rules, mustPrefixes(subnet, worker), mustPrefixes(integration), ports)
		requireNoNSGRuleViolations(t, violations, err)
	})

	t.Run("Allow covering only first worker prefix does not compensate Deny Any to Any", func(t *testing.T) {
		t.Parallel()
		second := "10.1.0.0/24"
		integration := "10.2.0.0/24"
		rules := []nsgSecurityRule{
			{
				Name: "AllowFirstOnly", Priority: 100, Access: securityGroupAccessAllow, Protocol: "Tcp",
				SourceAddressPrefixes: []string{subnet}, DestinationAddressPrefixes: []string{"*"},
				DestinationPortRanges: []string{"*"},
			},
			{
				Name: "DenyAnyAnyIn", Priority: 200, Access: securityGroupAccessDeny, Protocol: "*",
				SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"*"},
				DestinationPortRanges: []string{"*"},
			},
		}
		violations, err := v.validateInboundNSGRules(rules, mustPrefixes(subnet, second), mustPrefixes(integration), ports)
		blocked := requireNSGRuleViolations(t, violations, err)
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "DenyAnyAnyIn")
	})
}

func TestValidateNSGRules_VirtualNetworkNonRFC1918(t *testing.T) {
	t.Parallel()
	v := &AzureNodePoolNSGBasedRequiredConnectivityValidation{}
	worker := "100.64.1.0/24"
	integration := "100.64.2.0/24"
	outboundPorts := []int32{443, 6443}
	inboundPorts := []int32{8443, 443}

	t.Run("inbound Deny Any to VirtualNetwork fails for CGNAT subnets", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{{
			Name: "DenyAnyToVNetCGNAT", Priority: 200, Access: securityGroupAccessDeny, Protocol: "Tcp",
			SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"VirtualNetwork"},
			DestinationPortRanges: []string{"8443", "443"},
		}}
		violations, err := v.validateInboundNSGRules(rules, mustPrefixes(worker), mustPrefixes(integration), inboundPorts)
		blocked := requireNSGRuleViolations(t, violations, err)
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "DenyAnyToVNetCGNAT")
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "source Any to destination VirtualNetwork/vnet-integration")
	})

	t.Run("inbound Allow VirtualNetwork compensates Deny VirtualNetwork for CGNAT subnets", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{
			{
				Name: "AllowVNetCGNAT", Priority: 100, Access: securityGroupAccessAllow, Protocol: "Tcp",
				SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"VirtualNetwork"},
				DestinationPortRanges: []string{"8443", "443"},
			},
			{
				Name: "DenyAnyToVNetCGNAT", Priority: 200, Access: securityGroupAccessDeny, Protocol: "Tcp",
				SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"VirtualNetwork"},
				DestinationPortRanges: []string{"8443", "443"},
			},
		}
		violations, err := v.validateInboundNSGRules(rules, mustPrefixes(worker), mustPrefixes(integration), inboundPorts)
		requireNoNSGRuleViolations(t, violations, err)
	})

	t.Run("outbound Deny Any to VirtualNetwork fails for CGNAT cluster subnet", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{{
			Name: "DenyAnyToVNetCGNAT", Priority: 200, Access: securityGroupAccessDeny, Protocol: "Tcp",
			SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"VirtualNetwork"},
			DestinationPortRanges: []string{"443", "6443"},
		}}
		violations, err := v.validateOutboundNSGRules(rules, mustPrefixes(worker), mustPrefixes(integration), outboundPorts)
		blocked := requireNSGRuleViolations(t, violations, err)
		require.Contains(t, nsgBlockingViolationsMessage(blocked), "DenyAnyToVNetCGNAT")
	})

	t.Run("outbound Allow VirtualNetwork covers CGNAT destination subnet", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{
			{
				Name: "AllowVNetCGNAT", Priority: 100, Access: securityGroupAccessAllow, Protocol: "Tcp",
				SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"VirtualNetwork"},
				DestinationPortRanges: []string{"443", "6443"},
			},
			{
				Name: "DenyAnyAnyCGNAT", Priority: 200, Access: securityGroupAccessDeny, Protocol: "*",
				SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"*"},
				DestinationPortRanges: []string{"*"},
			},
		}
		violations, err := v.validateOutboundNSGRules(rules, mustPrefixes(worker), mustPrefixes(integration), outboundPorts)
		requireNoNSGRuleViolations(t, violations, err)
	})
}

func TestValidateNSGRules_azureDefaultRulesOnlyPass(t *testing.T) {
	t.Parallel()

	v := &AzureNodePoolNSGBasedRequiredConnectivityValidation{}
	worker := "10.0.0.0/24"
	integration := "10.0.1.0/24"
	cluster := "10.0.2.0/24"

	t.Run("outbound with default rules only", func(t *testing.T) {
		t.Parallel()
		violations, err := v.validateOutboundNSGRules(
			azureDefaultOutboundNSGRules(),
			mustPrefixes(worker),
			mustPrefixes(cluster),
			outboundNSGPorts,
		)
		requireNoNSGRuleViolations(t, violations, err)
	})

	t.Run("inbound with default rules only", func(t *testing.T) {
		t.Parallel()
		violations, err := v.validateInboundNSGRules(
			azureDefaultInboundNSGRules(),
			mustPrefixes(worker),
			mustPrefixes(integration),
			inboundNSGPorts,
		)
		requireNoNSGRuleViolations(t, violations, err)
	})
}

func TestPreValidationPortCheck(t *testing.T) {
	t.Parallel()

	t.Run("nil rules passes", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, preValidationPortCheck(nil))
	})

	t.Run("empty port ranges pass", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, preValidationPortCheck([]nsgSecurityRule{{Name: "NoPorts"}}))
	})

	t.Run("valid port formats pass", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{{
			Name: "ValidPorts",
			SourcePortRanges: []string{
				"*",
				"443",
				"443,6443",
				" 443 , 6443 ",
				"440-550",
				"0",
				"65535",
				"0-65535",
				"1024+100", // utilnet plus notation: 1024-1124
			},
			DestinationPortRanges: []string{
				"*",
				"8443",
				"443,6443",
				"8400-8500",
			},
		}}
		require.NoError(t, preValidationPortCheck(rules))
	})

	t.Run("invalid destination port fails", func(t *testing.T) {
		t.Parallel()
		testCases := []struct {
			name    string
			ports   []string
			contain string
		}{
			{
				name:    "port above max",
				ports:   []string{"70000"},
				contain: "invalid destination port range \"70000\"",
			},
			{
				name:    "port at 65536",
				ports:   []string{"65536"},
				contain: "invalid destination port range \"65536\"",
			},
			{
				name:    "comma separated with invalid token",
				ports:   []string{"443,70000"},
				contain: "invalid destination port range \"70000\"",
			},
			{
				name:    "non numeric",
				ports:   []string{"https"},
				contain: "invalid destination port range \"https\"",
			},
			{
				name:    "malformed range",
				ports:   []string{"100 - 200"},
				contain: "invalid destination port range",
			},
			{
				name:    "inverted range",
				ports:   []string{"6443-443"},
				contain: "invalid destination port range \"6443-443\"",
			},
		}
		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				err := preValidationPortCheck([]nsgSecurityRule{{
					Name:                  "BadDest",
					DestinationPortRanges: tc.ports,
				}})
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.contain)
			})
		}
	})

	t.Run("invalid source port fails", func(t *testing.T) {
		t.Parallel()
		err := preValidationPortCheck([]nsgSecurityRule{{
			Name:             "BadSource",
			SourcePortRanges: []string{"443,70000"},
		}})
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid source port range \"70000\"")
	})

	t.Run("stops at first invalid rule", func(t *testing.T) {
		t.Parallel()
		err := preValidationPortCheck([]nsgSecurityRule{
			{Name: "Good", DestinationPortRanges: []string{"443"}},
			{Name: "Bad", DestinationPortRanges: []string{"70000"}},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid destination port range \"70000\"")
	})
}

func TestAdjacentAddrsAndMergeAddrRanges_IPv6(t *testing.T) {
	t.Parallel()

	t.Run("adjacentAddrs supports IPv6 adjacency", func(t *testing.T) {
		t.Parallel()
		a := netip.MustParseAddr("2001:db8::ffff")
		b := netip.MustParseAddr("2001:db8::1:0")
		require.True(t, adjacentAddrs(a, b))
	})

	t.Run("adjacentAddrs handles max address boundary", func(t *testing.T) {
		t.Parallel()
		maxIPv6 := netip.MustParseAddr("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff")
		prevIPv6 := netip.MustParseAddr("ffff:ffff:ffff:ffff:ffff:ffff:ffff:fffe")
		require.True(t, adjacentAddrs(maxIPv6, prevIPv6))
	})

	t.Run("mergeAddrRanges merges contiguous IPv6 ranges", func(t *testing.T) {
		t.Parallel()
		ranges := []ipAddrRange{
			{
				first: netip.MustParseAddr("2001:db8::"),
				last:  netip.MustParseAddr("2001:db8::ffff"),
			},
			{
				first: netip.MustParseAddr("2001:db8::1:0"),
				last:  netip.MustParseAddr("2001:db8::1:ffff"),
			},
		}

		merged := mergeAddrRanges(ranges)
		require.Len(t, merged, 1)
		require.Equal(t, netip.MustParseAddr("2001:db8::"), merged[0].first)
		require.Equal(t, netip.MustParseAddr("2001:db8::1:ffff"), merged[0].last)
	})
}
