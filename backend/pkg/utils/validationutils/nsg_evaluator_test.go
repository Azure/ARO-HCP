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
	"testing"

	"github.com/stretchr/testify/require"
)

func requireNoNSGRuleViolation(t *testing.T, violation *nsgDenyViolation, err error) {
	t.Helper()
	require.NoError(t, err)
	require.Nil(t, violation)
}

func requireNSGRuleViolation(t *testing.T, violation *nsgDenyViolation, err error) *nsgDenyViolation {
	t.Helper()
	require.NoError(t, err)
	require.NotNil(t, violation)
	return violation
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
		violation, err := v.validateOutboundNSGRules(rules, []string{subnet}, []string{subnet}, ports)
		blocked := requireNSGRuleViolation(t, violation, err)
		require.Contains(t, blocked.Message, "DenyAnyAny")
		require.Contains(t, blocked.Message, "source Any to destination Any")
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
		violation, err := v.validateOutboundNSGRules(rules, []string{subnet}, []string{subnet}, ports)
		requireNoNSGRuleViolation(t, violation, err)
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
		violation, err := v.validateOutboundNSGRules(rules, []string{npSubnet}, []string{clusterSubnet}, ports)
		requireNoNSGRuleViolation(t, violation, err)
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
		violation, err := v.validateOutboundNSGRules(rules, []string{subnet}, []string{subnet}, ports)
		blocked := requireNSGRuleViolation(t, violation, err)
		require.Contains(t, blocked.Message, "DenyAnyAny")
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
		violation, err := v.validateOutboundNSGRules(rules, []string{subnet}, []string{subnet}, ports)
		blocked := requireNSGRuleViolation(t, violation, err)
		require.Contains(t, blocked.Message, "DenyAnyAny")
	})

	t.Run("Deny Any to Any on only 6443 fails", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{{
			Name: "DenyAny6443", Priority: 100, Access: securityGroupAccessDeny, Protocol: "*",
			SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"*"},
			DestinationPortRanges: []string{"6443"},
		}}
		violation, err := v.validateOutboundNSGRules(rules, []string{subnet}, []string{subnet}, ports)
		blocked := requireNSGRuleViolation(t, violation, err)
		require.Contains(t, blocked.Message, "DenyAny6443")
		require.Contains(t, blocked.Message, "6443")
	})

	t.Run("Deny Any to Any on only 443 fails", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{{
			Name: "DenyAny443", Priority: 100, Access: securityGroupAccessDeny, Protocol: "*",
			SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"*"},
			DestinationPortRanges: []string{"443"},
		}}
		violation, err := v.validateOutboundNSGRules(rules, []string{subnet}, []string{subnet}, ports)
		blocked := requireNSGRuleViolation(t, violation, err)
		require.Contains(t, blocked.Message, "DenyAny443")
		require.Contains(t, blocked.Message, "443")
	})

	t.Run("Deny Any to Any on both 443 and 6443 fails", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{{
			Name: "DenyAnyBoth", Priority: 100, Access: securityGroupAccessDeny, Protocol: "*",
			SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"*"},
			DestinationPortRanges: []string{"443", "6443"},
		}}
		violation, err := v.validateOutboundNSGRules(rules, []string{subnet}, []string{subnet}, ports)
		blocked := requireNSGRuleViolation(t, violation, err)
		require.Contains(t, blocked.Message, "DenyAnyBoth")
		require.Contains(t, blocked.Message, "443")
		require.Contains(t, blocked.Message, "6443")
	})

	t.Run("Deny Any to Any on comma-separated 443,6443 fails", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{{
			Name: "DenyAnyComma", Priority: 100, Access: securityGroupAccessDeny, Protocol: "*",
			SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"*"},
			DestinationPortRanges: []string{"443,6443"},
		}}
		violation, err := v.validateOutboundNSGRules(rules, []string{subnet}, []string{subnet}, ports)
		blocked := requireNSGRuleViolation(t, violation, err)
		require.Contains(t, blocked.Message, "DenyAnyComma")
	})

	t.Run("UDP-only Deny Any to Any is ignored", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{{
			Name: "DenyUDP", Priority: 100, Access: securityGroupAccessDeny, Protocol: "Udp",
			SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"*"},
			DestinationPortRanges: []string{"*"},
		}}
		violation, err := v.validateOutboundNSGRules(rules, []string{subnet}, []string{subnet}, ports)
		requireNoNSGRuleViolation(t, violation, err)
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
		violation, err := v.validateOutboundNSGRules(rules, []string{subnet, second}, []string{subnet, second}, ports)
		blocked := requireNSGRuleViolation(t, violation, err)
		require.Contains(t, blocked.Message, "DenyAnyAny")
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
		violation, err := v.validateOutboundNSGRules(rules, []string{subnet}, []string{subnet}, ports)
		blocked := requireNSGRuleViolation(t, violation, err)
		require.Contains(t, blocked.Message, "DenySubnet")
		require.Contains(t, blocked.Message, subnet)
	})

	t.Run("UDP-only Deny to subnet is ignored", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{{
			Name: "DenyUDPSubnet", Priority: 120, Access: securityGroupAccessDeny, Protocol: "Udp",
			SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{subnet},
			DestinationPortRanges: []string{"443", "6443"},
		}}
		violation, err := v.validateOutboundNSGRules(rules, []string{subnet}, []string{subnet}, ports)
		requireNoNSGRuleViolation(t, violation, err)
	})

	t.Run("Deny to IP inside subnet without Allow fails", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{{
			Name: "DenyILB", Priority: 110, Access: securityGroupAccessDeny, Protocol: "Tcp",
			SourceAddressPrefixes: []string{"VirtualNetwork"}, DestinationAddressPrefixes: []string{"10.0.0.4"},
			DestinationPortRanges: []string{"443", "6443"},
		}}
		violation, err := v.validateOutboundNSGRules(rules, []string{subnet}, []string{subnet}, ports)
		blocked := requireNSGRuleViolation(t, violation, err)
		require.Contains(t, blocked.Message, "DenyILB")
	})

	t.Run("higher-priority Allow to IP before Deny to IP passes", func(t *testing.T) {
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
		violation, err := v.validateOutboundNSGRules(rules, []string{subnet}, []string{subnet}, ports)
		requireNoNSGRuleViolation(t, violation, err)
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
		violation, err := v.validateOutboundNSGRules(rules, []string{subnet}, []string{subnet}, ports)
		requireNoNSGRuleViolation(t, violation, err)
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
		violation, err := v.validateOutboundNSGRules(rules, []string{subnet}, []string{subnet}, ports)
		requireNoNSGRuleViolation(t, violation, err)
	})

	t.Run("Deny to IP outside subnet is ignored", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{{
			Name: "DenyOther", Priority: 110, Access: securityGroupAccessDeny, Protocol: "Tcp",
			SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"10.0.1.4"},
			DestinationPortRanges: []string{"443", "6443"},
		}}
		violation, err := v.validateOutboundNSGRules(rules, []string{subnet}, []string{subnet}, ports)
		requireNoNSGRuleViolation(t, violation, err)
	})

	t.Run("Deny with source CIDR outside worker subnet is ignored", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{{
			Name: "DenyUnrelatedSource", Priority: 110, Access: securityGroupAccessDeny, Protocol: "Tcp",
			SourceAddressPrefixes: []string{"192.168.99.0/24"}, DestinationAddressPrefixes: []string{subnet},
			DestinationPortRanges: []string{"443", "6443"},
		}}
		violation, err := v.validateOutboundNSGRules(rules, []string{subnet}, []string{subnet}, ports)
		requireNoNSGRuleViolation(t, violation, err)
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
		violation, err := v.validateOutboundNSGRules(rules, []string{subnet}, []string{subnet}, ports)
		blocked := requireNSGRuleViolation(t, violation, err)
		require.Contains(t, blocked.Message, "DenyILB")
	})

	t.Run("Deny with source overlapping worker subnet fails", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{{
			Name: "DenyWorkerSource", Priority: 110, Access: securityGroupAccessDeny, Protocol: "Tcp",
			SourceAddressPrefixes: []string{subnet}, DestinationAddressPrefixes: []string{"10.0.0.4"},
			DestinationPortRanges: []string{"443", "6443"},
		}}
		violation, err := v.validateOutboundNSGRules(rules, []string{subnet}, []string{subnet}, ports)
		blocked := requireNSGRuleViolation(t, violation, err)
		require.Contains(t, blocked.Message, "DenyWorkerSource")
	})

	t.Run("no custom rules passes", func(t *testing.T) {
		t.Parallel()
		violation, err := v.validateOutboundNSGRules(nil, []string{subnet}, []string{subnet}, ports)
		requireNoNSGRuleViolation(t, violation, err)
	})

	t.Run("Deny targeting second of multiple worker subnet prefixes fails", func(t *testing.T) {
		t.Parallel()
		second := "10.1.0.0/24"
		rules := []nsgSecurityRule{{
			Name: "DenySecondPrefix", Priority: 110, Access: securityGroupAccessDeny, Protocol: "Tcp",
			SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"10.1.0.4"},
			DestinationPortRanges: []string{"443", "6443"},
		}}
		violation, err := v.validateOutboundNSGRules(rules, []string{subnet, second}, []string{subnet, second}, ports)
		blocked := requireNSGRuleViolation(t, violation, err)
		require.Contains(t, blocked.Message, "DenySecondPrefix")
		require.Contains(t, blocked.Message, second)
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
		violation, err := v.validateOutboundNSGRules(rules, []string{npSubnet}, []string{clusterSubnet}, ports)
		blocked := requireNSGRuleViolation(t, violation, err)
		require.Contains(t, blocked.Message, "DenyClusterSubnet")
		require.Contains(t, blocked.Message, clusterSubnet)
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
		violation, err := v.validateOutboundNSGRules(rules, []string{npSubnet}, []string{clusterSubnet}, ports)
		blocked := requireNSGRuleViolation(t, violation, err)
		require.Contains(t, blocked.Message, "DenyClusterIP")
	})

	t.Run("Deny Any to node-pool subnet prefix is ignored when destinations are cluster only", func(t *testing.T) {
		t.Parallel()
		clusterSubnet := "10.0.0.0/24"
		npSubnet := "10.0.2.0/24"
		rules := []nsgSecurityRule{{
			Name: "DenyNPSubnet", Priority: 101, Access: securityGroupAccessDeny, Protocol: "Tcp",
			SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{npSubnet},
			DestinationPortRanges: []string{"443", "6443"},
		}}
		violation, err := v.validateOutboundNSGRules(rules, []string{npSubnet}, []string{clusterSubnet}, ports)
		requireNoNSGRuleViolation(t, violation, err)
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
		violation, err := v.validateOutboundNSGRules(rules, []string{npSubnet}, []string{clusterSubnet}, ports)
		blocked := requireNSGRuleViolation(t, violation, err)
		require.Contains(t, blocked.Message, "DenyWorkerToCluster")
		require.Contains(t, blocked.Message, clusterSubnet)
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
				violation, err := v.validateOutboundNSGRules([]nsgSecurityRule{tc.rule}, []string{npSubnet}, []string{clusterSubnet}, ports)
				requireNoNSGRuleViolation(t, violation, err)
			})
		}
	})

	t.Run("Deny worker subnet to vnet-integration is ignored when destinations are cluster only", func(t *testing.T) {
		t.Parallel()
		npSubnet := "10.0.2.0/24"
		clusterSubnet := "10.0.0.0/24"
		integration := "10.0.1.0/24"
		rules := []nsgSecurityRule{{
			Name: "DenyWorkerToIntegration", Priority: 101, Access: securityGroupAccessDeny, Protocol: "Tcp",
			SourceAddressPrefixes: []string{npSubnet}, DestinationAddressPrefixes: []string{integration},
			DestinationPortRanges: []string{"443", "6443"},
		}}
		violation, err := v.validateOutboundNSGRules(rules, []string{npSubnet}, []string{clusterSubnet}, ports)
		requireNoNSGRuleViolation(t, violation, err)
	})

	t.Run("Deny unrelated source to cluster subnet is ignored", func(t *testing.T) {
		t.Parallel()
		clusterSubnet := "10.0.0.0/24"
		npSubnet := "10.0.2.0/24"
		unrelated := "10.9.0.0/24"
		rules := []nsgSecurityRule{{
			Name: "DenyUnrelated", Priority: 101, Access: securityGroupAccessDeny, Protocol: "Tcp",
			SourceAddressPrefixes: []string{unrelated}, DestinationAddressPrefixes: []string{clusterSubnet},
			DestinationPortRanges: []string{"443", "6443"},
		}}
		violation, err := v.validateOutboundNSGRules(rules, []string{npSubnet}, []string{clusterSubnet}, ports)
		requireNoNSGRuleViolation(t, violation, err)
	})

	t.Run("Deny worker subnet to Any fails", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{{
			Name: "DenyWorkerToAny", Priority: 110, Access: securityGroupAccessDeny, Protocol: "Tcp",
			SourceAddressPrefixes: []string{subnet}, DestinationAddressPrefixes: []string{"*"},
			DestinationPortRanges: []string{"443", "6443"},
		}}
		violation, err := v.validateOutboundNSGRules(rules, []string{subnet}, []string{subnet}, ports)
		blocked := requireNSGRuleViolation(t, violation, err)
		require.Contains(t, blocked.Message, "DenyWorkerToAny")
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
		violation, err := v.validateOutboundNSGRules(rules, []string{npSubnet}, []string{clusterSubnet}, ports)
		blocked := requireNSGRuleViolation(t, violation, err)
		require.Contains(t, blocked.Message, "DenyNPToAny")
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
		violation, err := v.validateOutboundNSGRules(rules, []string{npSubnet}, []string{clusterSubnet}, ports)
		blocked := requireNSGRuleViolation(t, violation, err)
		require.Contains(t, blocked.Message, "DenyAnyAny")
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
		violation, err := v.validateInboundNSGRules(rules, []string{subnet}, []string{subnet}, ports)
		blocked := requireNSGRuleViolation(t, violation, err)
		require.Contains(t, blocked.Message, "DenyAnyAnyIn")
		require.Contains(t, blocked.Message, "source Any to destination Any")
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
		violation, err := v.validateInboundNSGRules(rules, []string{worker}, []string{integration}, ports)
		blocked := requireNSGRuleViolation(t, violation, err)
		require.Contains(t, blocked.Message, "DenyAnyToIntegration")
		require.Contains(t, blocked.Message, "source Any to destination VirtualNetwork/vnet-integration")
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
		violation, err := v.validateInboundNSGRules(rules, []string{worker}, []string{integration}, ports)
		blocked := requireNSGRuleViolation(t, violation, err)
		require.Contains(t, blocked.Message, "DenyAnyToIntegrationStar")
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
		violation, err := v.validateInboundNSGRules(rules, []string{worker}, []string{integration}, ports)
		blocked := requireNSGRuleViolation(t, violation, err)
		require.Contains(t, blocked.Message, "DenyAnyToVNet")
		require.Contains(t, blocked.Message, "source Any to destination VirtualNetwork/vnet-integration")
	})

	t.Run("Deny Any to non-integration subnet is ignored", func(t *testing.T) {
		t.Parallel()
		worker := "10.0.0.0/24"
		integration := "10.0.1.0/24"
		other := "10.0.2.0/24"
		rules := []nsgSecurityRule{{
			Name: "DenyAnyToOther", Priority: 200, Access: securityGroupAccessDeny, Protocol: "Tcp",
			SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{other},
			DestinationPortRanges: []string{"8443", "443"},
		}}
		violation, err := v.validateInboundNSGRules(rules, []string{worker}, []string{integration}, ports)
		requireNoNSGRuleViolation(t, violation, err)
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
		violation, err := v.validateInboundNSGRules(rules, []string{worker}, []string{integration}, ports)
		requireNoNSGRuleViolation(t, violation, err)
	})

	t.Run("Deny worker subnet to Any fails", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{{
			Name: "DenyWorkerToAny", Priority: 200, Access: securityGroupAccessDeny, Protocol: "Tcp",
			SourceAddressPrefixes: []string{subnet}, DestinationAddressPrefixes: []string{"*"},
			DestinationPortRanges: []string{"*"},
		}}
		violation, err := v.validateInboundNSGRules(rules, []string{subnet}, []string{subnet}, ports)
		blocked := requireNSGRuleViolation(t, violation, err)
		require.Contains(t, blocked.Message, "DenyWorkerToAny")
		require.Contains(t, blocked.Message, "destination Any")
	})

	t.Run("Deny worker subnet to VirtualNetwork fails", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{{
			Name: "DenyWorkerToVNet", Priority: 200, Access: securityGroupAccessDeny, Protocol: "Tcp",
			SourceAddressPrefixes: []string{subnet}, DestinationAddressPrefixes: []string{"VirtualNetwork"},
			DestinationPortRanges: []string{"*"},
		}}
		violation, err := v.validateInboundNSGRules(rules, []string{subnet}, []string{subnet}, ports)
		blocked := requireNSGRuleViolation(t, violation, err)
		require.Contains(t, blocked.Message, "DenyWorkerToVNet")
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
		violation, err := v.validateInboundNSGRules(rules, []string{worker}, []string{integration}, ports)
		blocked := requireNSGRuleViolation(t, violation, err)
		require.Contains(t, blocked.Message, "DenyWorkerToIntegration")
		require.Contains(t, blocked.Message, "vnet-integration")
	})

	t.Run("Deny worker to non-integration subnet is ignored when integration differs", func(t *testing.T) {
		t.Parallel()
		worker := "10.0.0.0/24"
		integration := "10.0.1.0/24"
		other := "10.0.2.0/24"
		rules := []nsgSecurityRule{{
			Name: "DenyWorkerToOther", Priority: 200, Access: securityGroupAccessDeny, Protocol: "Tcp",
			SourceAddressPrefixes: []string{worker}, DestinationAddressPrefixes: []string{other},
			DestinationPortRanges: []string{"8443", "443"},
		}}
		violation, err := v.validateInboundNSGRules(rules, []string{worker}, []string{integration}, ports)
		requireNoNSGRuleViolation(t, violation, err)
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
		violation, err := v.validateInboundNSGRules(rules, []string{npSubnet}, []string{integration}, ports)
		blocked := requireNSGRuleViolation(t, violation, err)
		require.Contains(t, blocked.Message, "DenyNPToAny")
		require.Contains(t, blocked.Message, "destination Any")
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
		violation, err := v.validateInboundNSGRules(rules, []string{npSubnet}, []string{integration}, ports)
		blocked := requireNSGRuleViolation(t, violation, err)
		require.Contains(t, blocked.Message, "DenyNPToIntegration")
		require.Contains(t, blocked.Message, "vnet-integration")
	})

	t.Run("Deny worker IP to worker subnet fails", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{{
			Name: "DenyWorkerToSubnet", Priority: 200, Access: securityGroupAccessDeny, Protocol: "Tcp",
			SourceAddressPrefixes: []string{"10.0.0.5"}, DestinationAddressPrefixes: []string{subnet},
			DestinationPortRanges: []string{"*"},
		}}
		violation, err := v.validateInboundNSGRules(rules, []string{subnet}, []string{subnet}, ports)
		blocked := requireNSGRuleViolation(t, violation, err)
		require.Contains(t, blocked.Message, "DenyWorkerToSubnet")
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
		violation, err := v.validateInboundNSGRules(rules, []string{subnet}, []string{subnet}, ports)
		requireNoNSGRuleViolation(t, violation, err)
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
		violation, err := v.validateInboundNSGRules(rules, []string{subnet}, []string{subnet}, ports)
		requireNoNSGRuleViolation(t, violation, err)
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
		violation, err := v.validateInboundNSGRules(rules, []string{subnet}, []string{subnet}, ports)
		blocked := requireNSGRuleViolation(t, violation, err)
		require.Contains(t, blocked.Message, "DenyAnyAnyIn")
	})

	t.Run("Deny from other subnet to Any is ignored", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{{
			Name: "DenyOtherToAny", Priority: 200, Access: securityGroupAccessDeny, Protocol: "Tcp",
			SourceAddressPrefixes: []string{"10.0.1.0/24"}, DestinationAddressPrefixes: []string{"*"},
			DestinationPortRanges: []string{"*"},
		}}
		violation, err := v.validateInboundNSGRules(rules, []string{subnet}, []string{subnet}, ports)
		requireNoNSGRuleViolation(t, violation, err)
	})

	t.Run("UDP Deny Any to Any is ignored", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{{
			Name: "DenyUDP", Priority: 200, Access: securityGroupAccessDeny, Protocol: "Udp",
			SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"*"},
			DestinationPortRanges: []string{"*"},
		}}
		violation, err := v.validateInboundNSGRules(rules, []string{subnet}, []string{subnet}, ports)
		requireNoNSGRuleViolation(t, violation, err)
	})

	t.Run("Deny Any to Any on unrelated port 80 is ignored", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{{
			Name: "DenyPort80", Priority: 200, Access: securityGroupAccessDeny, Protocol: "Tcp",
			SourceAddressPrefixes: []string{"*"}, DestinationAddressPrefixes: []string{"*"},
			DestinationPortRanges: []string{"80"},
		}}
		violation, err := v.validateInboundNSGRules(rules, []string{subnet}, []string{subnet}, ports)
		requireNoNSGRuleViolation(t, violation, err)
	})

	t.Run("Deny worker to VirtualNetwork on only 8443 fails", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{{
			Name: "Deny8443", Priority: 200, Access: securityGroupAccessDeny, Protocol: "Tcp",
			SourceAddressPrefixes: []string{subnet}, DestinationAddressPrefixes: []string{"VirtualNetwork"},
			DestinationPortRanges: []string{"8443"},
		}}
		violation, err := v.validateInboundNSGRules(rules, []string{subnet}, []string{subnet}, ports)
		blocked := requireNSGRuleViolation(t, violation, err)
		require.Contains(t, blocked.Message, "Deny8443")
		require.Contains(t, blocked.Message, "8443")
	})

	t.Run("Deny worker to VirtualNetwork on only 443 fails", func(t *testing.T) {
		t.Parallel()
		rules := []nsgSecurityRule{{
			Name: "Deny443", Priority: 200, Access: securityGroupAccessDeny, Protocol: "Tcp",
			SourceAddressPrefixes: []string{subnet}, DestinationAddressPrefixes: []string{"VirtualNetwork"},
			DestinationPortRanges: []string{"443"},
		}}
		violation, err := v.validateInboundNSGRules(rules, []string{subnet}, []string{subnet}, ports)
		blocked := requireNSGRuleViolation(t, violation, err)
		require.Contains(t, blocked.Message, "Deny443")
		require.Contains(t, blocked.Message, "443")
	})

	t.Run("empty vnet-integration CIDRs returns error", func(t *testing.T) {
		t.Parallel()
		violation, err := v.validateInboundNSGRules(nil, []string{subnet}, nil, ports)
		require.Error(t, err)
		require.Nil(t, violation)
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
		violation, err := v.validateInboundNSGRules(rules, []string{subnet, second}, []string{integration}, ports)
		blocked := requireNSGRuleViolation(t, violation, err)
		require.Contains(t, blocked.Message, "DenySecondToAny")
	})

	t.Run("Allow covering second worker prefix compensates Deny of second", func(t *testing.T) {
		t.Parallel()
		second := "10.1.0.0/24"
		integration := "10.2.0.0/24"
		rules := []nsgSecurityRule{
			{
				Name: "AllowSecond", Priority: 100, Access: securityGroupAccessAllow, Protocol: "Tcp",
				SourceAddressPrefixes: []string{second}, DestinationAddressPrefixes: []string{"*"},
				DestinationPortRanges: []string{"*"},
			},
			{
				Name: "DenySecondToAny", Priority: 200, Access: securityGroupAccessDeny, Protocol: "Tcp",
				SourceAddressPrefixes: []string{second}, DestinationAddressPrefixes: []string{"*"},
				DestinationPortRanges: []string{"*"},
			},
		}
		violation, err := v.validateInboundNSGRules(rules, []string{subnet, second}, []string{integration}, ports)
		requireNoNSGRuleViolation(t, violation, err)
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
		violation, err := v.validateInboundNSGRules(rules, []string{subnet, second}, []string{integration}, ports)
		blocked := requireNSGRuleViolation(t, violation, err)
		require.Contains(t, blocked.Message, "DenyAnyAnyIn")
	})
}
