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

// requiredNSGPath is traffic that must not be blocked by an uncompensated Deny rule.
type requiredNSGPath struct {
	worker      netip.Prefix
	workerCIDR  string
	destination netip.Prefix
	destCIDR    string
	port        int32
}

// validateOutboundNSGRules validates outbound NSG rules so worker nodes
// can reach the control plane (typically TCP 443/6443; see outboundNSGPorts).
//
// workerSubnetCIDRs are the worker (node-pool or cluster) address spaces that
// originate egress. destinationCIDRs are the address spaces those workers must
// reach for KAS (cluster machine subnet).
func (v *AzureCustomerNSGValidation) validateOutboundNSGRules(rules []NSGSecurityRule, workerSubnetCIDRs, destinationCIDRs []string, ports []int32) error {
	paths, err := buildRequiredNSGPaths(workerSubnetCIDRs, "worker subnet", destinationCIDRs, "destination subnet", ports)
	if err != nil {
		return err
	}
	return validateRequiredNSGPaths(rules, paths, nsgDirectionOutbound)
}

// validateInboundNSGRules validates inbound NSG rules so
// traffic from worker nodes toward the vnet integration path used for KAS is
// not blocked on the given ports (typically TCP 443/8443; see inboundNSGPorts).
//
// workerSubnetCIDRs identify worker-sourced traffic.
// vnetIntegrationSubnetCIDRs are the vnet-integration subnet address spaces
// whose NSG is being evaluated; must be non-empty.
func (v *AzureCustomerNSGValidation) validateInboundNSGRules(rules []NSGSecurityRule, workerSubnetCIDRs, vnetIntegrationSubnetCIDRs []string, ports []int32) error {
	paths, err := buildRequiredNSGPaths(workerSubnetCIDRs, "worker subnet", vnetIntegrationSubnetCIDRs, "vnet-integration subnet", ports)
	if err != nil {
		return err
	}
	return validateRequiredNSGPaths(rules, paths, nsgDirectionInbound)
}

type nsgDirection int

const (
	nsgDirectionOutbound nsgDirection = iota
	nsgDirectionInbound
)

func buildRequiredNSGPaths(workerCIDRs []string, workerLabel string, destCIDRs []string, destLabel string, ports []int32) ([]requiredNSGPath, error) {
	workers, err := parsePrefixes(workerCIDRs, workerLabel)
	if err != nil {
		return nil, err
	}
	destinations, err := parsePrefixes(destCIDRs, destLabel)
	if err != nil {
		return nil, err
	}
	paths := make([]requiredNSGPath, 0, len(workers)*len(destinations)*len(ports))
	for i, worker := range workers {
		for j, dest := range destinations {
			for _, port := range ports {
				paths = append(paths, requiredNSGPath{
					worker:      worker,
					workerCIDR:  workerCIDRs[i],
					destination: dest,
					destCIDR:    destCIDRs[j],
					port:        port,
				})
			}
		}
	}
	return paths, nil
}

// validateRequiredNSGPaths checks that every required path is not blocked by an
// uncompensated Deny rule. Rules are evaluated in Azure priority order; a
// higher-priority Allow that covers the Deny compensates it.
func validateRequiredNSGPaths(rules []NSGSecurityRule, paths []requiredNSGPath, direction nsgDirection) error {
	sorted := sortByPriority(rules)
	requiredPorts := requiredPortsFromPaths(paths)
	for _, path := range paths {
		for i := range sorted {
			deny := &sorted[i]
			if !isDeny(deny) || !protocolMatchesTCP(deny.Protocol) {
				continue
			}
			if !denyMatchesPath(deny, path) {
				continue
			}
			if hasHigherPriorityAllowForDeny(sorted[:i], deny, []int32{path.port}, path.worker) {
				continue
			}
			return pathBlockedError(deny, path, direction, requiredPorts)
		}
	}
	return nil
}

func requiredPortsFromPaths(paths []requiredNSGPath) []int32 {
	if len(paths) == 0 {
		return nil
	}
	seen := make(map[int32]struct{}, len(paths))
	var ports []int32
	for _, path := range paths {
		if _, ok := seen[path.port]; ok {
			continue
		}
		seen[path.port] = struct{}{}
		ports = append(ports, path.port)
	}
	return ports
}

func denyMatchesPath(deny *NSGSecurityRule, path requiredNSGPath) bool {
	if !sourceMatchesSubnet(deny.SourceAddressPrefixes, path.worker) {
		return false
	}
	if !destinationOverlapsSubnet(deny.DestinationAddressPrefixes, path.destination) {
		return false
	}
	return portRangesMatch(deny.DestinationPortRanges, path.port)
}

func pathBlockedError(deny *NSGSecurityRule, path requiredNSGPath, direction nsgDirection, requiredPorts []int32) error {
	blockedPorts := matchingPorts(deny.DestinationPortRanges, requiredPorts)
	if len(blockedPorts) == 0 {
		blockedPorts = []int32{path.port}
	}
	portStr := formatPorts(blockedPorts)
	switch direction {
	case nsgDirectionOutbound:
		if isAnyAddressPrefixes(deny.SourceAddressPrefixes) && isAnyAddressPrefixes(deny.DestinationAddressPrefixes) {
			return fmt.Errorf(
				"outbound NSG rule %q (priority %d) denies source Any to destination Any covering ports %s; remove/narrow the Deny",
				deny.Name, deny.Priority, portStr,
			)
		}
		return fmt.Errorf(
			"outbound NSG rule %q (priority %d) denies egress to a destination overlapping subnet %s covering ports %s; remove/narrow the Deny",
			deny.Name, deny.Priority, path.destCIDR, portStr,
		)
	case nsgDirectionInbound:
		reason := inboundDenyReason(deny, path)
		return fmt.Errorf(
			"inbound NSG rule %q (priority %d) %s covering ports %s; remove/narrow the Deny",
			deny.Name, deny.Priority, reason, portStr,
		)
	default:
		return fmt.Errorf("NSG rule %q (priority %d) blocks required path on port %s", deny.Name, deny.Priority, portStr)
	}
}

func inboundDenyReason(deny *NSGSecurityRule, path requiredNSGPath) string {
	srcAny := isAnyAddressPrefixes(deny.SourceAddressPrefixes)
	dstAny := isAnyAddressPrefixes(deny.DestinationAddressPrefixes)
	srcWorker := sourceSpecificallyFromWorker(deny.SourceAddressPrefixes, path.worker)
	dstVNetOrIntegration := destinationIsVNetOrSubnet(deny.DestinationAddressPrefixes, path.destination)

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
		return "denies required worker to vnet-integration traffic"
	}
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

// isAnyProtocol reports whether the NSG protocol field is a wildcard.
// Azure uses "" or "*" for any protocol — not the address service tag "Any".
func isAnyProtocol(protocol string) bool {
	p := strings.TrimSpace(protocol)
	return p == "" || p == "*"
}

func protocolMatchesTCP(protocol string) bool {
	return isAnyProtocol(protocol) || strings.EqualFold(strings.TrimSpace(protocol), "Tcp")
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
	return destinationMatchesSubnet(prefixes, subnet, false)
}

// destinationOverlapsSubnet reports whether destination could apply to traffic
// toward subnet, treating empty and Any/*/wildcard as a match.
func destinationOverlapsSubnet(prefixes []string, subnet netip.Prefix) bool {
	return destinationMatchesSubnet(prefixes, subnet, true)
}

// destinationMatchesSubnet reports whether destination prefixes apply to subnet.
// When matchAny is true, an empty prefix list or Any/*/wildcard counts as a match
// (outbound overlap semantics). When false, those are ignored and only
// VirtualNetwork (for private subnets) or concrete CIDR/IP overlap counts
// (inbound specific-destination semantics).
func destinationMatchesSubnet(prefixes []string, subnet netip.Prefix, matchAny bool) bool {
	if len(prefixes) == 0 {
		return matchAny
	}
	for _, p := range prefixes {
		p = strings.TrimSpace(p)
		if isAnyAddressPrefix(p) {
			if matchAny {
				return true
			}
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
