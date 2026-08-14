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

	"github.com/Azure/ARO-HCP/internal/utils"
)

// SecurityGroupAccess is the Allow/Deny result of an NSG security rule.
type securityGroupAccess string

const (
	securityGroupAccessAllow securityGroupAccess = "Allow"
	securityGroupAccessDeny  securityGroupAccess = "Deny"

	azureTagAny            = "Any"
	azureTagVirtualNetwork = "VirtualNetwork"
	azureTagInternet       = "Internet"
)

// nsgSecurityRule is a simplified NSG security rule used by outbound and inbound evaluators.
type nsgSecurityRule struct {
	Name                       string
	Priority                   int32
	Access                     securityGroupAccess
	Protocol                   string
	SourceAddressPrefixes      []string
	DestinationAddressPrefixes []string
	DestinationPortRanges      []string
}

// requiredNSGPath is traffic that must not be blocked by an uncompensated Deny rule.
type requiredNSGPath struct {
	worker      netip.Prefix
	destination netip.Prefix
	port        int32
}

// nsgRequiredConnectivityBlockingViolation describes a customer NSG rules that blocks required traffic.
type nsgRequiredConnectivityBlockingViolation struct {
	Message string
}

type nsgDirection string

const (
	nsgDirectionOutbound nsgDirection = "Outbound"
	nsgDirectionInbound  nsgDirection = "Inbound"
)

func buildRequiredNSGPaths(workerPrefixes []netip.Prefix, workerLabel string, destinationPrefixes []netip.Prefix, destLabel string, ports []int32) ([]requiredNSGPath, error) {
	if len(workerPrefixes) == 0 {
		return nil, utils.TrackError(fmt.Errorf("no %s prefixes provided", workerLabel))
	}
	if len(destinationPrefixes) == 0 {
		return nil, utils.TrackError(fmt.Errorf("no %s prefixes provided", destLabel))
	}
	paths := make([]requiredNSGPath, 0, len(workerPrefixes)*len(destinationPrefixes)*len(ports))
	for _, worker := range workerPrefixes {
		for _, dest := range destinationPrefixes {
			for _, port := range ports {
				paths = append(paths, requiredNSGPath{
					worker:      worker,
					destination: dest,
					port:        port,
				})
			}
		}
	}
	return paths, nil
}

// findBlockingNSGDeny checks that every required path is allowed by the NSG.
// Rules are scanned in Azure priority order. A covering Allow ends evaluation for
// the path. A matching Deny fails unless a higher-priority Allow compensates it.
func findBlockingNSGDeny(rules []nsgSecurityRule, paths []requiredNSGPath, direction nsgDirection) (*nsgRequiredConnectivityBlockingViolation, error) {
	sorted := sortByPriority(rules)
	requiredPorts := requiredPortsFromPaths(paths)
	for _, path := range paths {
		for i := range sorted {
			rule := &sorted[i]
			if !protocolMatchesTCP(rule.Protocol) {
				continue
			}
			if !ruleMatchesPath(rule, path) {
				continue
			}
			if isAllow(rule) {
				if allowCoversPrefix(rule.DestinationAddressPrefixes, path.destination) {
					break
				}
				continue
			}
			if isDeny(rule) {
				if hasHigherPriorityAllowForDeny(sorted[:i], rule, []int32{path.port}, path.worker) {
					continue
				}
				return pathBlockedViolation(rule, path, direction, requiredPorts), nil
			}
		}
	}
	return nil, nil
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

func ruleMatchesPath(rule *nsgSecurityRule, path requiredNSGPath) bool {
	if !sourceMatchesSubnet(rule.SourceAddressPrefixes, path.worker) {
		return false
	}
	if !destinationOverlapsSubnet(rule.DestinationAddressPrefixes, path.destination) {
		return false
	}
	return portRangesMatch(rule.DestinationPortRanges, path.port)
}

func pathBlockedViolation(deny *nsgSecurityRule, path requiredNSGPath, direction nsgDirection, requiredPorts []int32) *nsgRequiredConnectivityBlockingViolation {
	blockedPorts := matchingPorts(deny.DestinationPortRanges, requiredPorts)
	if len(blockedPorts) == 0 {
		blockedPorts = []int32{path.port}
	}
	portStr := formatPorts(blockedPorts)
	switch direction {
	case nsgDirectionOutbound:
		if isAnyAddressPrefixes(deny.SourceAddressPrefixes) && isAnyAddressPrefixes(deny.DestinationAddressPrefixes) {
			return &nsgRequiredConnectivityBlockingViolation{Message: fmt.Sprintf(
				"outbound NSG rule %q (priority %d) denies source Any to destination Any covering ports %s; remove/narrow the Deny",
				deny.Name, deny.Priority, portStr,
			)}
		}
		return &nsgRequiredConnectivityBlockingViolation{Message: fmt.Sprintf(
			"outbound NSG rule %q (priority %d) denies egress to a destination overlapping subnet %s covering ports %s; remove/narrow the Deny",
			deny.Name, deny.Priority, path.destination.String(), portStr,
		)}
	case nsgDirectionInbound:
		reason := inboundDenyReason(deny, path)
		return &nsgRequiredConnectivityBlockingViolation{Message: fmt.Sprintf(
			"inbound NSG rule %q (priority %d) %s covering ports %s; remove/narrow the Deny",
			deny.Name, deny.Priority, reason, portStr,
		)}
	default:
		return &nsgRequiredConnectivityBlockingViolation{Message: fmt.Sprintf("NSG rule %q (priority %d) blocks required path on port %s", deny.Name, deny.Priority, portStr)}
	}
}

func inboundDenyReason(deny *nsgSecurityRule, path requiredNSGPath) string {
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
	// cidrs from Azure should always have at least one CIDR
	// treat missing CIDRs as an internal error.
	if len(cidrs) == 0 {
		return nil, utils.TrackError(fmt.Errorf("no %s CIDRs provided", label))
	}
	out := []netip.Prefix{}
	for _, cidr := range cidrs {
		prefix, err := netip.ParsePrefix(cidr)
		if err != nil {
			return nil, utils.TrackError(fmt.Errorf("invalid CIDR %q: %w", cidr, err))
		}
		out = append(out, prefix)
	}
	return out, nil
}

// hasHigherPriorityAllowForDeny reports whether a higher-priority Allow covers
// the Deny for worker-relevant traffic on the ports. Allow Any→Any is accepted.
func hasHigherPriorityAllowForDeny(higherPriorityRules []nsgSecurityRule, deny *nsgSecurityRule, ports []int32, workerSubnet netip.Prefix) bool {
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

// sortByPriority sorts NSG rules by priority in ascending order.
// This is used to evaluate rules in Azure priority order.
func sortByPriority(rules []nsgSecurityRule) []nsgSecurityRule {
	sorted := append([]nsgSecurityRule(nil), rules...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Priority < sorted[j].Priority
	})
	return sorted
}

func isDeny(rule *nsgSecurityRule) bool {
	return strings.EqualFold(string(rule.Access), string(securityGroupAccessDeny))
}

func isAllow(rule *nsgSecurityRule) bool {
	return strings.EqualFold(string(rule.Access), string(securityGroupAccessAllow))
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
// VirtualNetwork or concrete CIDR/IP overlap counts (inbound specific-destination semantics).
// VirtualNetwork matches any subnet prefix supplied here; those prefixes come from the
// cluster VNet and align with Azure's VirtualNetwork service tag semantics.
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
func allowDestinationCoversDeny(allowPrefixes, denyPrefixes []string) bool {
	if isAnyAddressPrefixes(allowPrefixes) {
		return true
	}
	if len(denyPrefixes) == 0 || isAnyAddressPrefixes(denyPrefixes) {
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

func portsCovered(ranges []string, ports []int32) bool {
	for _, port := range ports {
		if !portRangesMatch(ranges, port) {
			return false
		}
	}
	return true
}

// allowCoversPrefix reports whether Allow destinations fully cover
// every Deny destination (so the Allow wins for the denied traffic).
func allowCoversPrefix(allowPrefixes []string, destPrefix netip.Prefix) bool {
	for _, p := range allowPrefixes {
		p = strings.TrimSpace(p)
		if isAnyAddressPrefix(p) {
			return true
		}
		if isVirtualNetworkTag(p) {
			return true
		}
		allowPrefix, err := parsePrefixOrAddr(p)
		if err != nil {
			continue
		}
		if allowPrefix.Contains(destPrefix.Addr()) && allowPrefix.Bits() <= destPrefix.Bits() {
			return true
		}
	}
	return false
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

// parsePrefixOrAddr parses Azure NSG/subnet address strings as either a CIDR
// (e.g. "10.0.0.0/24") or a single IP (e.g. "10.0.0.4", treated as a /32 or /128).
func parsePrefixOrAddr(s string) (netip.Prefix, error) {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "/") {
		prefix, err := netip.ParsePrefix(s)
		if err != nil {
			return netip.Prefix{}, utils.TrackError(fmt.Errorf("invalid CIDR %q: %w", s, err))
		}
		return prefix, nil
	}

	addr, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, utils.TrackError(fmt.Errorf("invalid IP address %q: %w", s, err))
	}
	return netip.PrefixFrom(addr, addr.BitLen()), nil
}

func formatPorts(ports []int32) string {
	parts := make([]string, 0, len(ports))
	for _, p := range ports {
		parts = append(parts, strconv.FormatInt(int64(p), 10))
	}
	return strings.Join(parts, "/")
}
