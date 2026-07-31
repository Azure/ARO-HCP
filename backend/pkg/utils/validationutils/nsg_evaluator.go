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
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"

	utilnet "k8s.io/apimachinery/pkg/util/net"
)

// SecurityGroupAccess is the Allow/Deny result of an NSG security rule.
type SecurityGroupAccess string

const (
	SecurityGroupAccessAllow SecurityGroupAccess = "Allow"
	SecurityGroupAccessDeny  SecurityGroupAccess = "Deny"

	azureTagAny            = "Any"
	azureTagVirtualNetwork = "VirtualNetwork"
	azureTagInternet       = "Internet"
)

// NSGSecurityRule is a simplified NSG security rule used by outbound and inbound evaluators.
type NSGSecurityRule struct {
	Name                       string
	Priority                   int32
	Access                     SecurityGroupAccess
	Protocol                   string
	SourceAddressPrefixes      []string
	DestinationAddressPrefixes []string
	DestinationPortRanges      []string
}

// validateOutboundNSGRules validates outbound NSG rules so worker nodes
// can reach the kube-apiserver internal load balancer (typically TCP 443/6443).
// The ILB private IP usually lives in the worker subnet, so Denys to that subnet
// (or Any→Any covering the ports) are treated as blocking KAS access.
//
//  1. Broad Deny (source Any → destination Any covering the ports) fails unless a
//     higher-priority Allow covers the denied traffic (including Allow Any→Any).
//  2. Deny whose destination overlaps any worker subnet CIDR (the subnet itself,
//     any IP inside it, or VirtualNetwork): requires a higher-priority Allow that
//     permits traffic to that destination (specific CIDR/IP/tag, or Allow Any).
func (v *AzureCustomerNSGValidation) validateOutboundNSGRules(rules []NSGSecurityRule, workerSubnetCIDRs []string, ports []int32) error {
	workerSubnets, err := parsePrefixes(workerSubnetCIDRs, "worker subnet")
	if err != nil {
		return err
	}
	if err := validateBroadDenyAnyAny(rules, workerSubnets, ports); err != nil {
		return err
	}
	for i, subnet := range workerSubnets {
		if err := validateDenyOverlappingSubnet(rules, subnet, workerSubnetCIDRs[i], ports); err != nil {
			return err
		}
	}
	return nil
}

// validateInboundNSGRules validates inbound NSG rules so
// traffic from worker nodes toward the vnet integration path used for KAS is
// not blocked on the given ports (typically TCP 8443). Fails when an
// inbound Deny covers any of those ports and matches any of:
//
//  1. source Any → destination Any
//  2. source Any → destination VirtualNetwork / vnet-integration subnet
//  3. source worker subnet (or VirtualNetwork) → destination Any
//  4. source worker subnet (or VirtualNetwork) → destination VirtualNetwork / vnet-integration subnet
//
// workerSubnetCIDRs identify worker-sourced traffic.
// vnetIntegrationSubnetCIDRs are the vnet-integration subnet address spaces
// whose NSG is being evaluated; must be non-empty.
//
// A higher-priority Allow that covers the denied traffic (including Allow Any→Any)
// compensates and the Deny is accepted. Denys that do not match any of the given
// ports are ignored.
func (v *AzureCustomerNSGValidation) validateInboundNSGRules(rules []NSGSecurityRule, workerSubnetCIDRs, vnetIntegrationSubnetCIDRs []string, ports []int32) error {
	workerSubnets, err := parsePrefixes(workerSubnetCIDRs, "worker subnet")
	if err != nil {
		return err
	}
	vnetIntegrationSubnets, err := parsePrefixes(vnetIntegrationSubnetCIDRs, "vnet-integration subnet")
	if err != nil {
		return err
	}

	sorted := sortByPriority(rules)
	for i := range sorted {
		deny := &sorted[i]
		if !isDeny(deny) || !protocolMatchesTCP(deny.Protocol) {
			continue
		}

		blockedPorts := matchingPorts(deny.DestinationPortRanges, ports)
		if len(blockedPorts) == 0 {
			continue
		}

		reason := inboundDenyBlocksWorkerToVnetIntegrationKASReason(deny, workerSubnets, vnetIntegrationSubnets)
		if reason == "" {
			continue
		}
		// Compensate only when Allow covers every worker prefix this Deny actually hits.
		affected := workerSubnetsAffectedByInboundDeny(deny, workerSubnets, vnetIntegrationSubnets)
		if hasHigherPriorityAllowForAllSubnets(sorted[:i], deny, blockedPorts, affected) {
			continue
		}
		return fmt.Errorf(
			"inbound NSG rule %q (priority %d) %s covering ports %s; remove/narrow the Deny",
			deny.Name, deny.Priority, reason, formatPorts(blockedPorts),
		)
	}
	return nil
}

func parsePrefixes(cidrs []string, label string) ([]netip.Prefix, error) {
	if len(cidrs) == 0 {
		return nil, fmt.Errorf("no %s CIDRs provided", label)
	}
	out := make([]netip.Prefix, 0, len(cidrs))
	for _, cidr := range cidrs {
		prefix, err := parsePrefixOrAddr(cidr)
		if err != nil {
			return nil, fmt.Errorf("invalid %s CIDR %q: %w", label, cidr, err)
		}
		out = append(out, prefix)
	}
	return out, nil
}

// inboundDenyBlocksWorkerToVnetIntegrationKASReason returns the reason why an inbound Deny blocks worker to vnet-integration/KAS path.
// If the Deny does not block the path, an empty string is returned.
func inboundDenyBlocksWorkerToVnetIntegrationKASReason(deny *NSGSecurityRule, workerSubnets, vnetIntegrationSubnets []netip.Prefix) string {
	srcAny := isAnyAddressPrefixes(deny.SourceAddressPrefixes)
	dstAny := isAnyAddressPrefixes(deny.DestinationAddressPrefixes)
	srcWorker := sourceSpecificallyFromAnySubnet(deny.SourceAddressPrefixes, workerSubnets)
	dstVNetOrIntegration := destinationIsVNetOrAnySubnet(deny.DestinationAddressPrefixes, vnetIntegrationSubnets)

	switch {
	case srcAny && dstAny:
		return "denies source Any to destination Any (blocks worker to vnet-integration)"
	case srcAny && dstVNetOrIntegration:
		return "denies source Any to destination VirtualNetwork/vnet-integration subnet (blocks worker to vnet-integration)"
	case srcWorker && dstAny:
		return "denies source worker subnet to destination Any (blocks worker to vnet-integration)"
	case srcWorker && dstVNetOrIntegration:
		return "denies source worker subnet to destination VirtualNetwork/vnet-integration subnet (blocks worker to vnet-integration)"
	default:
		return ""
	}
}

func sourceSpecificallyFromAnySubnet(prefixes []string, subnets []netip.Prefix) bool {
	for i := range subnets {
		if sourceSpecificallyFromWorker(prefixes, subnets[i]) {
			return true
		}
	}
	return false
}

// workerSubnetsAffectedByInboundDeny returns the worker prefixes for which the
// Deny alone would be considered blocking toward the vnet-integration path.
func workerSubnetsAffectedByInboundDeny(deny *NSGSecurityRule, workerSubnets, vnetIntegrationSubnets []netip.Prefix) []netip.Prefix {
	affected := make([]netip.Prefix, 0, len(workerSubnets))
	for _, subnet := range workerSubnets {
		if inboundDenyBlocksWorkerToVnetIntegrationKASReason(deny, []netip.Prefix{subnet}, vnetIntegrationSubnets) != "" {
			affected = append(affected, subnet)
		}
	}
	return affected
}

func destinationIsVNetOrAnySubnet(prefixes []string, subnets []netip.Prefix) bool {
	for i := range subnets {
		if destinationIsVNetOrSubnet(prefixes, subnets[i]) {
			return true
		}
	}
	return false
}

// validateBroadDenyAnyAny validates outbound NSG rules that deny source Any to destination Any covering the ports.
// A higher-priority Allow that covers the denied traffic for every worker subnet
// prefix (including Allow Any→Any) compensates and the Deny is accepted.
func validateBroadDenyAnyAny(rules []NSGSecurityRule, workerSubnets []netip.Prefix, ports []int32) error {
	sorted := sortByPriority(rules)
	for i := range sorted {
		deny := &sorted[i]
		if !isDeny(deny) || !protocolMatchesTCP(deny.Protocol) {
			continue
		}
		if !isAnyAddressPrefixes(deny.SourceAddressPrefixes) || !isAnyAddressPrefixes(deny.DestinationAddressPrefixes) {
			continue
		}
		blockedPorts := matchingPorts(deny.DestinationPortRanges, ports)
		if len(blockedPorts) == 0 {
			continue
		}
		if hasHigherPriorityAllowForAllSubnets(sorted[:i], deny, blockedPorts, workerSubnets) {
			continue
		}
		return fmt.Errorf(
			"outbound NSG rule %q (priority %d) denies source Any to destination Any covering ports %s; remove/narrow the Deny",
			deny.Name, deny.Priority, formatPorts(blockedPorts),
		)
	}
	return nil
}

func validateDenyOverlappingSubnet(rules []NSGSecurityRule, subnet netip.Prefix, workerSubnetCIDR string, ports []int32) error {
	sorted := sortByPriority(rules)
	for i := range sorted {
		deny := &sorted[i]
		if !isDeny(deny) || !protocolMatchesTCP(deny.Protocol) {
			continue
		}
		if !sourceMatchesSubnet(deny.SourceAddressPrefixes, subnet) {
			continue
		}
		if !destinationOverlapsSubnet(deny.DestinationAddressPrefixes, subnet) {
			continue
		}
		blockedPorts := matchingPorts(deny.DestinationPortRanges, ports)
		if len(blockedPorts) == 0 {
			continue
		}
		if !hasHigherPriorityAllowForDeny(sorted[:i], deny, blockedPorts, subnet) {
			return fmt.Errorf(
				"outbound NSG rule %q (priority %d) denies egress to a destination overlapping worker subnet %s covering ports %s; remove/narrow the Deny",
				deny.Name, deny.Priority, workerSubnetCIDR, formatPorts(blockedPorts),
			)
		}
	}
	return nil
}

// hasHigherPriorityAllowForAllSubnets reports whether a higher-priority Allow
// covers the Deny for every given worker subnet prefix.
func hasHigherPriorityAllowForAllSubnets(higherPriorityRules []NSGSecurityRule, deny *NSGSecurityRule, ports []int32, workerSubnets []netip.Prefix) bool {
	if len(workerSubnets) == 0 {
		return false
	}
	for _, subnet := range workerSubnets {
		if !hasHigherPriorityAllowForDeny(higherPriorityRules, deny, ports, subnet) {
			return false
		}
	}
	return true
}

// hasHigherPriorityAllowForDeny reports whether a higher-priority Allow covers
// the Deny for worker-relevant traffic on the ports. Allow Any→Any is accepted.
func hasHigherPriorityAllowForDeny(higherPriorityRules []NSGSecurityRule, deny *NSGSecurityRule, ports []int32, workerSubnet netip.Prefix) bool {
	for i := range higherPriorityRules {
		allow := &higherPriorityRules[i]
		if !isAllow(allow) || !protocolMatchesTCP(allow.Protocol) {
			continue
		}
		if !sourceMatchesSubnet(allow.SourceAddressPrefixes, workerSubnet) &&
			!sourcesOverlap(allow.SourceAddressPrefixes, deny.SourceAddressPrefixes, workerSubnet) {
			continue
		}
		if !allowDestinationCoversDeny(allow.DestinationAddressPrefixes, deny.DestinationAddressPrefixes) {
			continue
		}
		if portsCovered(allow.DestinationPortRanges, ports) {
			return true
		}
	}
	return false
}

func sortByPriority(rules []NSGSecurityRule) []NSGSecurityRule {
	sorted := append([]NSGSecurityRule(nil), rules...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Priority < sorted[j].Priority
	})
	return sorted
}

func isDeny(rule *NSGSecurityRule) bool {
	return strings.EqualFold(string(rule.Access), string(SecurityGroupAccessDeny))
}

func isAllow(rule *NSGSecurityRule) bool {
	return strings.EqualFold(string(rule.Access), string(SecurityGroupAccessAllow))
}

func isAnyAddressPrefixes(prefixes []string) bool {
	if len(prefixes) == 0 {
		return true
	}
	for _, p := range prefixes {
		if isAnyAddressPrefix(p) {
			return true
		}
	}
	return false
}

func isAnyAddressPrefix(p string) bool {
	p = strings.TrimSpace(p)
	return p == "" || p == "*" || strings.EqualFold(p, azureTagAny)
}

func isVirtualNetworkTag(p string) bool {
	return strings.EqualFold(strings.TrimSpace(p), azureTagVirtualNetwork)
}

func isInternetTag(p string) bool {
	return strings.EqualFold(strings.TrimSpace(p), azureTagInternet)
}

func protocolMatchesTCP(protocol string) bool {
	p := strings.TrimSpace(protocol)
	if isAnyAddressPrefix(p) {
		return true
	}
	return strings.EqualFold(p, "Tcp")
}

// sourceMatchesSubnet reports whether a rule's source could apply to traffic
// from the given subnet (Any, VirtualNetwork, or a CIDR/IP overlapping it).
func sourceMatchesSubnet(prefixes []string, subnet netip.Prefix) bool {
	if len(prefixes) == 0 {
		return true
	}
	for _, p := range prefixes {
		p = strings.TrimSpace(p)
		if isAnyAddressPrefix(p) || isVirtualNetworkTag(p) {
			return true
		}
		rulePrefix, err := parsePrefixOrAddr(p)
		if err != nil {
			continue
		}
		if rulePrefix.Overlaps(subnet) {
			return true
		}
	}
	return false
}

// sourceSpecificallyFromWorker reports whether source includes the worker subnet
// or VirtualNetwork as a concrete match (not merely Any).
func sourceSpecificallyFromWorker(prefixes []string, workerSubnet netip.Prefix) bool {
	for _, p := range prefixes {
		p = strings.TrimSpace(p)
		if isAnyAddressPrefix(p) {
			continue
		}
		if isVirtualNetworkTag(p) {
			return true
		}
		rulePrefix, err := parsePrefixOrAddr(p)
		if err != nil {
			continue
		}
		if rulePrefix.Overlaps(workerSubnet) {
			return true
		}
	}
	return false
}

// destinationIsVNetOrSubnet reports whether destination targets VirtualNetwork
// or the given subnet CIDR/IP (not merely Any).
func destinationIsVNetOrSubnet(prefixes []string, subnet netip.Prefix) bool {
	for _, p := range prefixes {
		p = strings.TrimSpace(p)
		if isAnyAddressPrefix(p) {
			continue
		}
		if isInternetTag(p) {
			continue
		}
		if isVirtualNetworkTag(p) {
			if isPrivateAddr(subnet.Addr()) {
				return true
			}
			continue
		}
		rulePrefix, err := parsePrefixOrAddr(p)
		if err != nil {
			continue
		}
		if rulePrefix.Overlaps(subnet) {
			return true
		}
	}
	return false
}

// sourcesOverlap reports whether allow source prefixes could cover deny sources
// for traffic involving the given subnet (used when Allow source is more specific).
func sourcesOverlap(allowPrefixes, denyPrefixes []string, subnet netip.Prefix) bool {
	if isAnyAddressPrefixes(allowPrefixes) {
		return true
	}
	if isAnyAddressPrefixes(denyPrefixes) {
		return sourceMatchesSubnet(allowPrefixes, subnet)
	}
	for _, denyP := range denyPrefixes {
		denyP = strings.TrimSpace(denyP)
		if isAnyAddressPrefix(denyP) {
			if sourceMatchesSubnet(allowPrefixes, subnet) {
				return true
			}
			continue
		}
		if isVirtualNetworkTag(denyP) {
			if sourceSpecificallyFromWorker(allowPrefixes, subnet) || isAnyAddressPrefixes(allowPrefixes) {
				return true
			}
			continue
		}
		denyPrefix, err := parsePrefixOrAddr(denyP)
		if err != nil {
			continue
		}
		if allowCoversPrefix(allowPrefixes, denyPrefix) || (sourceMatchesSubnet(allowPrefixes, subnet) && denyPrefix.Overlaps(subnet)) {
			return true
		}
	}
	return false
}

func destinationOverlapsSubnet(prefixes []string, subnet netip.Prefix) bool {
	if len(prefixes) == 0 {
		return true
	}
	for _, p := range prefixes {
		p = strings.TrimSpace(p)
		if isAnyAddressPrefix(p) {
			return true
		}
		if isInternetTag(p) {
			continue
		}
		if isVirtualNetworkTag(p) {
			if isPrivateAddr(subnet.Addr()) {
				return true
			}
			continue
		}
		rulePrefix, err := parsePrefixOrAddr(p)
		if err != nil {
			continue
		}
		if rulePrefix.Overlaps(subnet) {
			return true
		}
	}
	return false
}

// allowDestinationCoversDeny reports whether Allow destinations fully cover
// every Deny destination (so the Allow wins for the denied traffic).
// Allow Any covers any Deny destination (including Deny Any).
func allowDestinationCoversDeny(allowPrefixes, denyPrefixes []string) bool {
	if isAnyAddressPrefixes(allowPrefixes) {
		return true
	}
	if len(denyPrefixes) == 0 || isAnyAddressPrefixes(denyPrefixes) {
		// Specific Allow cannot cover Deny Any; only Allow Any (handled above) can.
		return false
	}
	for _, denyP := range denyPrefixes {
		denyP = strings.TrimSpace(denyP)
		if isInternetTag(denyP) {
			if !allowHasTag(allowPrefixes, azureTagInternet) {
				return false
			}
			continue
		}
		if isVirtualNetworkTag(denyP) {
			if !allowHasTag(allowPrefixes, azureTagVirtualNetwork) {
				return false
			}
			continue
		}
		denyPrefix, err := parsePrefixOrAddr(denyP)
		if err != nil {
			return false
		}
		if !allowCoversPrefix(allowPrefixes, denyPrefix) {
			return false
		}
	}
	return true
}

func allowHasTag(prefixes []string, tag string) bool {
	for _, p := range prefixes {
		if strings.EqualFold(strings.TrimSpace(p), tag) {
			return true
		}
	}
	return false
}

func allowCoversPrefix(allowPrefixes []string, denyPrefix netip.Prefix) bool {
	for _, p := range allowPrefixes {
		p = strings.TrimSpace(p)
		if isAnyAddressPrefix(p) {
			return true
		}
		if isVirtualNetworkTag(p) && isPrivateAddr(denyPrefix.Addr()) {
			return true
		}
		allowPrefix, err := parsePrefixOrAddr(p)
		if err != nil {
			continue
		}
		// Allow covers Deny when Allow is equal or broader than Deny.
		if allowPrefix.Contains(denyPrefix.Addr()) && allowPrefix.Bits() <= denyPrefix.Bits() {
			return true
		}
	}
	return false
}

func portsCovered(ranges []string, ports []int32) bool {
	for _, port := range ports {
		if !portRangesMatch(ranges, port) {
			return false
		}
	}
	return true
}

// matchingPorts returns the subset of ports that the rule's destination port
// ranges match. A Deny on only 6443 must still fail validation for KAS egress.
func matchingPorts(ranges []string, ports []int32) []int32 {
	var out []int32
	for _, port := range ports {
		if portRangesMatch(ranges, port) {
			out = append(out, port)
		}
	}
	return out
}

func portRangesMatch(ranges []string, port int32) bool {
	if len(ranges) == 0 {
		return true
	}
	for _, r := range ranges {
		r = strings.TrimSpace(r)
		if isAnyAddressPrefix(r) {
			return true
		}
		// Azure may return comma-separated ports in a single field (e.g. "443,6443").
		for _, part := range strings.Split(r, ",") {
			part = strings.TrimSpace(part)
			if isAnyAddressPrefix(part) {
				return true
			}
			pr, err := utilnet.ParsePortRange(part)
			if err != nil {
				continue
			}
			if pr.Contains(int(port)) {
				return true
			}
		}
	}
	return false
}

func parsePrefixOrAddr(s string) (netip.Prefix, error) {
	s = strings.TrimSpace(s)
	if prefix, err := netip.ParsePrefix(s); err == nil {
		return prefix, nil
	}
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("invalid IP or CIDR %q", s)
	}
	return netip.PrefixFrom(addr, addr.BitLen()), nil
}

func isPrivateAddr(addr netip.Addr) bool {
	return addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast()
}

func formatPorts(ports []int32) string {
	parts := make([]string, 0, len(ports))
	for _, p := range ports {
		parts = append(parts, strconv.FormatInt(int64(p), 10))
	}
	return strings.Join(parts, "/")
}
