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
	SourcePortRanges           []string
	DestinationPortRanges      []string
}

// requiredNSGPath is traffic that must not be blocked by an uncompensated Deny rule.
type requiredNSGPath struct {
	worker      netip.Prefix
	destination netip.Prefix
	port        int32
}

// nsgRequiredConnectivityViolationDetails describes a customer NSG rule that blocks required traffic.
type nsgRequiredConnectivityViolationDetails struct {
	Direction        nsgDirection
	RuleName         string
	RulePriority     int32
	Protocol         string
	SourcePortRanges []string
	BlockedPorts     []int32
	Source           []string
	Destination      []string
	PathWorker       netip.Prefix
	PathDestination  netip.Prefix
	Reason           string
}

type nsgDirection string

// ipAddrRange represents a range of IP addresses.
type ipAddrRange struct {
	first netip.Addr
	last  netip.Addr
}

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

// evaluateRequiredNSGPathViolations returns every required path that is not permitted by the NSG.
// Rules are walked in Azure priority order. A matching Allow that fully covers the destination
// permits the path immediately. Partial Allows accumulate destination subsets; when a matching Deny
// is reached, the partial Allows must union-cover the full destination or the path is blocked.
func evaluateRequiredNSGPathViolations(rules []nsgSecurityRule, paths []requiredNSGPath, direction nsgDirection) ([]*nsgRequiredConnectivityViolationDetails, error) {
	sorted := sortByPriority(rules)
	requiredPorts := requiredPortsFromPaths(paths)
	var violations []*nsgRequiredConnectivityViolationDetails
	for _, path := range paths {
		if v := evaluateSingleRequiredNSGPath(sorted, path, direction, requiredPorts); v != nil {
			violations = append(violations, v)
		}
	}
	return violations, nil
}

func evaluateSingleRequiredNSGPath(sorted []nsgSecurityRule, path requiredNSGPath, direction nsgDirection, requiredPorts []int32) *nsgRequiredConnectivityViolationDetails {
	var partialSubnets []netip.Prefix
	allowMatched := false
	for i := range sorted {
		rule := &sorted[i]
		if !protocolMatchesTCP(rule.Protocol) || !ruleMatchesPath(rule, path) {
			continue
		}
		if isAllow(rule) {
			allowMatched = true
			if allowCoversPrefix(rule.DestinationAddressPrefixes, path.destination) {
				return nil
			}
			partialSubnets = appendPartialAllowDestinations(partialSubnets, rule, path.destination)
			continue
		}
		if isDeny(rule) {
			if partialSubnetsCoverDestination(partialSubnets, path.destination) {
				return nil
			}
			return pathBlockedViolation(rule, path, direction, requiredPorts)
		}
	}
	if partialSubnetsCoverDestination(partialSubnets, path.destination) {
		return nil
	}
	if !allowMatched {
		return pathMissingAllowViolation(path, direction)
	}
	return pathMissingAllowViolation(path, direction)
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

func appendPartialAllowDestinations(partials []netip.Prefix, rule *nsgSecurityRule, dest netip.Prefix) []netip.Prefix {
	for _, p := range rule.DestinationAddressPrefixes {
		p = strings.TrimSpace(p)
		if isAnyAddressPrefix(p) || isVirtualNetworkTag(p) || isInternetTag(p) {
			continue
		}
		rulePrefix, err := parsePrefixOrAddr(p)
		if err != nil || !rulePrefix.Overlaps(dest) {
			continue
		}
		partials = append(partials, rulePrefix)
	}
	return partials
}

func partialSubnetsCoverDestination(partials []netip.Prefix, dest netip.Prefix) bool {
	if len(partials) == 0 {
		return false
	}
	prefixStrings := make([]string, len(partials))
	for i, p := range partials {
		prefixStrings[i] = p.String()
	}
	if allowCoversPrefix(prefixStrings, dest) {
		return true
	}
	return prefixUnionCoversDest(partials, dest)
}

func prefixUnionCoversDest(partials []netip.Prefix, dest netip.Prefix) bool {
	destRange, ok := prefixToAddrRange(dest)
	if !ok {
		return false
	}
	var ranges []ipAddrRange
	for _, partial := range partials {
		if !partial.Overlaps(dest) {
			continue
		}
		partialRange, ok := prefixToAddrRange(partial)
		if !ok {
			continue
		}
		ranges = append(ranges, intersectAddrRanges(partialRange, destRange))
	}
	if len(ranges) == 0 {
		return false
	}
	merged := mergeAddrRanges(ranges)
	return addrRangeCovers(merged, destRange)
}

func prefixToAddrRange(p netip.Prefix) (ipAddrRange, bool) {
	if !p.IsValid() {
		return ipAddrRange{}, false
	}
	masked := p.Masked()
	first := masked.Addr()
	last, ok := lastAddrOfPrefix(masked)
	if !ok {
		return ipAddrRange{}, false
	}
	return ipAddrRange{first: first, last: last}, true
}

func lastAddrOfPrefix(p netip.Prefix) (netip.Addr, bool) {
	if !p.IsValid() {
		return netip.Addr{}, false
	}
	addr := p.Addr()
	bits := p.Bits()
	if addr.Is4() {
		a := addr.As4()
		n := uint32(a[0])<<24 | uint32(a[1])<<16 | uint32(a[2])<<8 | uint32(a[3])
		if bits < 0 || bits > 32 {
			return netip.Addr{}, false
		}
		hostBits := 32 - bits
		if hostBits > 0 {
			n |= (1 << hostBits) - 1
		}
		return netip.AddrFrom4([4]byte{byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}), true
	}
	a16 := addr.As16()
	var n [16]byte
	copy(n[:], a16[:])
	if bits < 0 || bits > 128 {
		return netip.Addr{}, false
	}
	hostBits := 128 - bits
	for i := 15; i >= 0 && hostBits > 0; i-- {
		if hostBits >= 8 {
			n[i] = 0xff
			hostBits -= 8
			continue
		}
		n[i] |= (1 << hostBits) - 1
		hostBits = 0
	}
	return netip.AddrFrom16(n), true
}

func intersectAddrRanges(a, b ipAddrRange) ipAddrRange {
	first := a.first
	if b.first.Compare(first) > 0 {
		first = b.first
	}
	last := a.last
	if b.last.Compare(last) < 0 {
		last = b.last
	}
	return ipAddrRange{first: first, last: last}
}

func mergeAddrRanges(ranges []ipAddrRange) []ipAddrRange {
	if len(ranges) == 0 {
		return nil
	}
	sort.Slice(ranges, func(i, j int) bool {
		return ranges[i].first.Compare(ranges[j].first) < 0
	})
	merged := []ipAddrRange{ranges[0]}
	for i := 1; i < len(ranges); i++ {
		last := &merged[len(merged)-1]
		if ranges[i].first.Compare(last.last) <= 0 || adjacentAddrs(last.last, ranges[i].first) {
			if ranges[i].last.Compare(last.last) > 0 {
				last.last = ranges[i].last
			}
			continue
		}
		merged = append(merged, ranges[i])
	}
	return merged
}

func adjacentAddrs(a, b netip.Addr) bool {
	if a.Is4() != b.Is4() {
		return false
	}
	aNext := a.Next()
	bNext := b.Next()
	return (aNext.IsValid() && aNext == b) || (bNext.IsValid() && bNext == a)
}

func addrRangeCovers(ranges []ipAddrRange, dest ipAddrRange) bool {
	if len(ranges) == 0 {
		return false
	}
	merged := mergeAddrRanges(ranges)
	for _, r := range merged {
		if r.first.Compare(dest.first) <= 0 && r.last.Compare(dest.last) >= 0 {
			return true
		}
	}
	return false
}

func pathMissingAllowViolation(path requiredNSGPath, direction nsgDirection) *nsgRequiredConnectivityViolationDetails {
	return &nsgRequiredConnectivityViolationDetails{
		Direction:       direction,
		Protocol:        "Tcp",
		BlockedPorts:    []int32{path.port},
		PathWorker:      path.worker,
		PathDestination: path.destination,
		Reason:          "no Allow rule permits this traffic",
	}
}

func pathBlockedViolation(deny *nsgSecurityRule, path requiredNSGPath, direction nsgDirection, requiredPorts []int32) *nsgRequiredConnectivityViolationDetails {
	blockedPorts := matchingPorts(deny.DestinationPortRanges, requiredPorts)
	if len(blockedPorts) == 0 {
		blockedPorts = []int32{path.port}
	}
	var reason string
	switch direction {
	case nsgDirectionOutbound:
		reason = outboundBlockedRuleEndpoints(deny, path)
	case nsgDirectionInbound:
		reason = inboundBlockedRuleEndpoints(deny, path)
	default:
		reason = formatRuleSourceToDestination(deny.SourceAddressPrefixes, deny.DestinationAddressPrefixes)
	}
	return &nsgRequiredConnectivityViolationDetails{
		Direction:        direction,
		RuleName:         deny.Name,
		RulePriority:     deny.Priority,
		Protocol:         deny.Protocol,
		SourcePortRanges: append([]string(nil), deny.SourcePortRanges...),
		BlockedPorts:     append([]int32(nil), blockedPorts...),
		Source:           append([]string(nil), deny.SourceAddressPrefixes...),
		Destination:      append([]string(nil), deny.DestinationAddressPrefixes...),
		PathWorker:       path.worker,
		PathDestination:  path.destination,
		Reason:           reason,
	}
}

func outboundBlockedRuleEndpoints(deny *nsgSecurityRule, path requiredNSGPath) string {
	if isAnyAddressPrefixes(deny.SourceAddressPrefixes) && isAnyAddressPrefixes(deny.DestinationAddressPrefixes) {
		return "source Any to destination Any"
	}
	return fmt.Sprintf("source %s to destination overlapping subnet %s",
		formatNSGAddressPrefixesForMessage(deny.SourceAddressPrefixes), path.destination.String())
}

func inboundBlockedRuleEndpoints(deny *nsgSecurityRule, path requiredNSGPath) string {
	srcAny := isAnyAddressPrefixes(deny.SourceAddressPrefixes)
	dstAny := isAnyAddressPrefixes(deny.DestinationAddressPrefixes)
	srcWorker := sourceSpecificallyFromWorker(deny.SourceAddressPrefixes, path.worker)
	dstVNetOrIntegration := destinationIsVNetOrSubnet(deny.DestinationAddressPrefixes, path.destination)

	switch {
	case srcAny && dstAny:
		return "source Any to destination Any"
	case srcAny && dstVNetOrIntegration:
		return "source Any to destination VirtualNetwork/vnet-integration subnet"
	case srcWorker && dstAny:
		return "source Worker subnet to destination Any"
	case srcWorker && dstVNetOrIntegration:
		return "source Worker subnet to destination VirtualNetwork/vnet-integration subnet"
	default:
		return formatRuleSourceToDestination(deny.SourceAddressPrefixes, deny.DestinationAddressPrefixes)
	}
}

func formatRuleSourceToDestination(sourcePrefixes, destinationPrefixes []string) string {
	return fmt.Sprintf("source %s to destination %s",
		formatNSGAddressPrefixesForMessage(sourcePrefixes),
		formatNSGAddressPrefixesForMessage(destinationPrefixes))
}

func formatNSGAddressPrefixesForMessage(prefixes []string) string {
	if isAnyAddressPrefixes(prefixes) {
		return "Any"
	}
	parts := make([]string, 0, len(prefixes))
	for _, p := range prefixes {
		p = strings.TrimSpace(p)
		if isVirtualNetworkTag(p) {
			parts = append(parts, azureTagVirtualNetwork)
			continue
		}
		if isInternetTag(p) {
			parts = append(parts, azureTagInternet)
			continue
		}
		parts = append(parts, p)
	}
	if len(parts) == 0 {
		return "Any"
	}
	return strings.Join(parts, ", ")
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

func isAnyPort(p string) bool {
	return p == "*"
}

func isVirtualNetworkTag(p string) bool {
	return strings.EqualFold(strings.TrimSpace(p), azureTagVirtualNetwork)
}

func isInternetTag(p string) bool {
	return strings.EqualFold(strings.TrimSpace(p), azureTagInternet)
}

// isAnyProtocol reports whether the NSG protocol field is a wildcard.
func isAnyProtocol(protocol string) bool {
	p := strings.TrimSpace(protocol)
	return p == "" || p == "*" || p == "Any"
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

// allowCoversPrefix reports whether Allow destinations fully cover the destination prefix.
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
		if isAnyPort(r) {
			return true
		}
		// Azure may return comma-separated ports in a single field (e.g. "443,6443").
		for _, part := range strings.Split(r, ",") {
			part = strings.TrimSpace(part)
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

func preValidationPortCheck(rules []nsgSecurityRule) error {
	for _, rule := range rules {
		if err := validateNSGPortRanges(rule.SourcePortRanges, "source"); err != nil {
			return utils.TrackError(err)
		}
		if err := validateNSGPortRanges(rule.DestinationPortRanges, "destination"); err != nil {
			return utils.TrackError(err)
		}
	}
	return nil
}

// validateNSGPortRanges checks Azure NSG port fields: "*", single ports,
// comma-separated ports, or hyphen ranges. Each token is validated with
// utilnet.ParsePortRange (0-65535).
func validateNSGPortRanges(ranges []string, field string) error {
	for _, r := range ranges {
		r = strings.TrimSpace(r)
		if isAnyPort(r) {
			continue
		}
		// Azure may return comma-separated ports in a single field (e.g. "443,6443").
		for _, part := range strings.Split(r, ",") {
			part = strings.TrimSpace(part)
			if isAnyPort(part) {
				continue
			}
			if _, err := utilnet.ParsePortRange(part); err != nil {
				return utils.TrackError(fmt.Errorf("invalid %s port range %q: %w", field, part, err))
			}
		}
	}
	return nil
}

func formatNSGRequiredConnectivityViolationDetailsMessage(v *nsgRequiredConnectivityViolationDetails) string {
	if v == nil {
		return ""
	}
	base := fmt.Sprintf(
		"%s traffic is not allowed from %s (%s) to %s (%s).",
		string(v.Direction),
		v.PathWorker.String(),
		formatNSGSourcePortClause(v.SourcePortRanges, v.Protocol),
		v.PathDestination.String(),
		formatNSGDestinationPortClause(v.BlockedPorts, v.Protocol),
	)
	if v.RuleName == "" {
		return base + " No Allow rule permits this traffic."
	}
	return fmt.Sprintf(
		"%s It is blocked by rule %q (priority %d), action - Deny from %s.",
		base,
		v.RuleName, v.RulePriority,
		v.Reason,
	)
}

func formatNSGRequiredConnectivityViolationsMessage(violations []*nsgRequiredConnectivityViolationDetails) string {
	if len(violations) == 0 {
		return ""
	}
	if len(violations) == 1 {
		return formatNSGRequiredConnectivityViolationDetailsMessage(violations[0])
	}
	parts := make([]string, 0, len(violations)+1)
	for _, v := range violations {
		parts = append(parts, formatNSGRequiredConnectivityViolationDetailsMessage(v))
	}
	return strings.Join(parts, "\n")
}

func formatNSGSourcePortClause(sourcePortRanges []string, protocol string) string {
	ports := formatNSGRulePortRangesForMessage(sourcePortRanges)
	proto := formatNSGProtocolForMessage(protocol)
	if ports == "any" {
		return fmt.Sprintf("any source port, %s protocol", proto)
	}
	if strings.Contains(ports, "/") {
		return fmt.Sprintf("source ports %s, %s protocol", ports, proto)
	}
	return fmt.Sprintf("source port %s, %s protocol", ports, proto)
}

func formatNSGDestinationPortClause(blockedPorts []int32, protocol string) string {
	return fmt.Sprintf("port %s, %s protocol", formatPorts(blockedPorts), formatNSGProtocolForMessage(protocol))
}

func formatNSGRulePortRangesForMessage(ranges []string) string {
	if len(ranges) == 0 {
		return "any"
	}
	parts := make([]string, 0, len(ranges))
	for _, r := range ranges {
		r = strings.TrimSpace(r)
		if r == "" || isAnyPort(r) {
			return "any"
		}
		parts = append(parts, r)
	}
	return strings.Join(parts, "/")
}

func formatNSGProtocolForMessage(protocol string) string {
	protocol = strings.TrimSpace(protocol)
	if isAnyProtocol(protocol) {
		return "Any"
	}
	switch strings.ToLower(protocol) {
	case "tcp":
		return "TCP"
	case "udp":
		return "UDP"
	case "icmp":
		return "ICMP"
	default:
		return strings.ToUpper(protocol)
	}
}
