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

package framework

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/onsi/ginkgo/v2"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
)

// ErrNoUsableVMSize is returned by SelectVMSize when no VM size in the target
// location satisfies the selector after restrictions and capability filtering
// are applied. Callers decide whether this is a hard failure (general-purpose
// SKUs are expected to exist everywhere) or a reason to Skip the test (e.g. GPU
// capacity, which is genuinely scarce and often restricted per subscription).
//
// Encountering this error for a general-purpose selector almost always points
// at a configuration/quota issue with the test subscription/region (the chosen
// SKUs are restricted or unavailable there), not at a product defect.
var ErrNoUsableVMSize = errors.New("no unrestricted VM size satisfied the selector in the test location; " +
	"this typically indicates a VM SKU restriction or quota issue with the test subscription/region, not a product defect")

// Historical default SKUs. They are kept as the first preference in their
// selectors so behaviour is unchanged whenever they are available and
// unrestricted in the target subscription/region; selection only diverges when
// the preferred SKU is genuinely unusable.
const (
	DefaultWorkerVMSize = "Standard_D8s_v3"
	SmallWorkerVMSize   = "Standard_D4s_v3"
	JumpboxVMSize       = "Standard_D2s_v3"
)

// DefaultDiskStorageAccountType is the OS disk storage account type used by the
// default node pool params. It is centralized here so the node pool param
// constructors and tests share a single source of truth.
const DefaultDiskStorageAccountType = "StandardSSD_LRS"

const (
	capabilityVCPUs                    = "vCPUs"
	capabilityGPUs                     = "GPUs"
	capabilityCPUArchitecture          = "CPUArchitectureType"
	capabilityEphemeralOSDiskSupported = "EphemeralOSDiskSupported"
)

// VMSizeSelector describes the requirements a VM size must satisfy. SelectVMSize
// tries Preferred entries in order first, then falls back to a deterministic
// (sorted) pick among the remaining SKUs that match NamePattern and the
// capability constraints.
type VMSizeSelector struct {
	// Name identifies the selector in logs and errors, e.g. "default-worker".
	Name string
	// Preferred is an ordered list of SKU names to try before any discovery.
	Preferred []string
	// NamePattern, when set, restricts fallback discovery candidates to SKU
	// names matching the pattern. It does not constrain Preferred entries.
	NamePattern *regexp.Regexp
	// MinVCPUs, when > 0, requires the SKU to advertise at least this many vCPUs.
	MinVCPUs int
	// CPUArchitecture, when set (e.g. "x64" or "Arm64"), requires the SKU's
	// CPUArchitectureType capability to match (case-insensitive).
	CPUArchitecture string
	// RequireGPU, when true, requires the SKU to advertise at least one GPU.
	RequireGPU bool
	// RequireZones, when true, requires the SKU to expose at least one
	// non-restricted availability zone in the target location.
	RequireZones bool
	// RequireEphemeralOSDisk, when true, requires the SKU to advertise the
	// EphemeralOSDiskSupported capability ("True"). SKUs without local/cache
	// storage (e.g. Dsv5) do not support ephemeral OS disks and are excluded.
	RequireEphemeralOSDisk bool
}

// SelectVMSize queries the Azure Resource SKUs API for the test location and
// returns a VM size satisfying the selector, honouring subscription/location/
// zone restrictions. It logs the full decision trace (which preferred
// candidates were tried, which were skipped, and the final pick) for
// debuggability.
func (tc *perItOrDescribeTestContext) SelectVMSize(ctx context.Context, selector VMSizeSelector) (string, error) {
	location := tc.Location()

	skus, err := tc.listVirtualMachineResourceSKUs(ctx, location)
	if err != nil {
		return "", err
	}

	selected, trace, err := selectVMSize(skus, location, selector)
	logVMSizeSelection(selector, location, trace, err)
	if err != nil {
		return "", err
	}

	return selected, nil
}

// vmSizeSelectionTrace records how SelectVMSize reached its decision so it can
// be logged. It makes it obvious in CI output when a preferred candidate was
// unavailable and selection fell through to the next entry (or the fallback).
type vmSizeSelectionTrace struct {
	// preferredAttempts lists each preferred candidate in order and whether it
	// was usable (available + unrestricted + passed capability filters).
	preferredAttempts []vmSizePreferredAttempt
	// fallbackCandidates is the sorted set of NamePattern-matching usable SKUs
	// considered only when no preferred candidate was usable.
	fallbackCandidates []string
	// selected is the chosen SKU ("" when none was found).
	selected string
	// viaFallback is true when selection used the deterministic fallback rather
	// than a preferred candidate.
	viaFallback bool
}

type vmSizePreferredAttempt struct {
	name   string
	usable bool
}

// logVMSizeSelection writes a human-readable decision trace to the Ginkgo
// writer so CI logs clearly show the selection path.
func logVMSizeSelection(selector VMSizeSelector, location string, trace vmSizeSelectionTrace, selErr error) {
	fmt.Fprintf(ginkgo.GinkgoWriter, "VM size selection for selector %q in %s:\n", selector.Name, location)
	for _, attempt := range trace.preferredAttempts {
		if attempt.usable {
			fmt.Fprintf(ginkgo.GinkgoWriter, "  preferred candidate %q: available/usable\n", attempt.name)
		} else {
			fmt.Fprintf(ginkgo.GinkgoWriter, "  preferred candidate %q: NOT available/usable in this subscription/region (skipping)\n", attempt.name)
		}
	}
	switch {
	case selErr != nil:
		fmt.Fprintf(ginkgo.GinkgoWriter, "  -> no usable VM size found (fallback candidates: %v)\n", trace.fallbackCandidates)
	case trace.viaFallback:
		fmt.Fprintf(ginkgo.GinkgoWriter, "  no preferred candidate was usable; deterministic fallback among %v\n", trace.fallbackCandidates)
		fmt.Fprintf(ginkgo.GinkgoWriter, "  -> selected VM size %q (via fallback)\n", trace.selected)
	default:
		fmt.Fprintf(ginkgo.GinkgoWriter, "  -> selected VM size %q (preferred)\n", trace.selected)
	}
}

// listVirtualMachineResourceSKUs returns the Resource SKUs of type
// "virtualMachines" advertised in the given location.
func (tc *perItOrDescribeTestContext) listVirtualMachineResourceSKUs(ctx context.Context, location string) ([]*armcompute.ResourceSKU, error) {
	tc.perBinaryInvocationTestContext.contextLock.RLock()
	if cached, ok := tc.perBinaryInvocationTestContext.virtualMachineResourceSKUsByLocation[location]; ok {
		defer tc.perBinaryInvocationTestContext.contextLock.RUnlock()
		return cloneResourceSKUSlice(cached), nil
	}
	tc.perBinaryInvocationTestContext.contextLock.RUnlock()

	clientFactory, err := tc.GetARMComputeClientFactory(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get ARM compute client factory: %w", err)
	}

	filter := fmt.Sprintf("location eq '%s'", location)
	pager := clientFactory.NewResourceSKUsClient().NewListPager(&armcompute.ResourceSKUsClientListOptions{
		Filter: &filter,
	})

	var skus []*armcompute.ResourceSKU
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list resource SKUs in %s: %w", location, err)
		}
		for _, sku := range page.Value {
			if sku == nil || sku.ResourceType == nil || *sku.ResourceType != "virtualMachines" {
				continue
			}
			skus = append(skus, sku)
		}
	}

	tc.perBinaryInvocationTestContext.contextLock.Lock()
	if cached, ok := tc.perBinaryInvocationTestContext.virtualMachineResourceSKUsByLocation[location]; ok {
		tc.perBinaryInvocationTestContext.contextLock.Unlock()
		return cloneResourceSKUSlice(cached), nil
	}
	tc.perBinaryInvocationTestContext.virtualMachineResourceSKUsByLocation[location] = cloneResourceSKUSlice(skus)
	tc.perBinaryInvocationTestContext.contextLock.Unlock()

	return cloneResourceSKUSlice(skus), nil
}

func cloneResourceSKUSlice(skus []*armcompute.ResourceSKU) []*armcompute.ResourceSKU {
	return append([]*armcompute.ResourceSKU(nil), skus...)
}

// selectVMSize is the pure selection logic over a list of Resource SKUs. It returns
// the selected SKU together with a trace describing how the decision was reached.
func selectVMSize(skus []*armcompute.ResourceSKU, location string, selector VMSizeSelector) (string, vmSizeSelectionTrace, error) {
	usable := map[string]struct{}{}
	for _, sku := range skus {
		if sku == nil || sku.Name == nil {
			continue
		}
		if !skuUsable(sku, location, selector) {
			continue
		}
		usable[*sku.Name] = struct{}{}
	}

	var trace vmSizeSelectionTrace

	// Preferred entries win when they survive restriction/capability filtering.
	// Record every attempt (including misses) so the caller can log the path.
	for _, name := range selector.Preferred {
		_, ok := usable[name]
		trace.preferredAttempts = append(trace.preferredAttempts, vmSizePreferredAttempt{name: name, usable: ok})
	}
	for _, attempt := range trace.preferredAttempts {
		if attempt.usable {
			trace.selected = attempt.name
			return attempt.name, trace, nil
		}
	}

	// Deterministic fallback: sorted pick among matching usable SKUs.
	fallback := make([]string, 0, len(usable))
	for name := range usable {
		if selector.NamePattern != nil && !selector.NamePattern.MatchString(name) {
			continue
		}
		fallback = append(fallback, name)
	}
	sort.Strings(fallback)
	trace.fallbackCandidates = fallback
	if len(fallback) > 0 {
		trace.selected = fallback[0]
		trace.viaFallback = true
		return fallback[0], trace, nil
	}

	return "", trace, fmt.Errorf("selector %q matched no usable VM size in %s: %w", selector.Name, location, ErrNoUsableVMSize)
}

// skuUsable reports whether a single SKU satisfies the location, restriction and
// capability constraints (it intentionally ignores NamePattern, which only
// narrows fallback discovery).
func skuUsable(sku *armcompute.ResourceSKU, location string, selector VMSizeSelector) bool {
	if !skuAdvertisedInLocation(sku, location) {
		return false
	}
	if skuRestrictedInLocation(sku, location) {
		return false
	}
	if selector.MinVCPUs > 0 {
		vcpus, ok := skuCapabilityInt(sku, capabilityVCPUs)
		if !ok || vcpus < selector.MinVCPUs {
			return false
		}
	}
	if selector.CPUArchitecture != "" {
		arch, ok := skuCapabilityString(sku, capabilityCPUArchitecture)
		if !ok || !strings.EqualFold(arch, selector.CPUArchitecture) {
			return false
		}
	}
	if selector.RequireGPU {
		gpus, ok := skuCapabilityInt(sku, capabilityGPUs)
		if !ok || gpus < 1 {
			return false
		}
	}
	if selector.RequireZones {
		if _, available := zonesInLocation(sku, location); len(available) == 0 {
			return false
		}
	}
	if selector.RequireEphemeralOSDisk {
		val, ok := skuCapabilityString(sku, capabilityEphemeralOSDiskSupported)
		if !ok || !strings.EqualFold(val, "True") {
			return false
		}
	}
	return true
}

func skuAdvertisedInLocation(sku *armcompute.ResourceSKU, location string) bool {
	for _, info := range sku.LocationInfo {
		if info != nil && info.Location != nil && strings.EqualFold(*info.Location, location) {
			return true
		}
	}
	// Some SKUs only populate the top-level Locations list.
	for _, loc := range sku.Locations {
		if loc != nil && strings.EqualFold(*loc, location) {
			return true
		}
	}
	return false
}

// skuRestrictedInLocation reports whether the SKU is entirely unavailable in the
// location. This covers both a Location-type restriction (SKU banned from the
// whole region, e.g. NotAvailableForSubscription) and the case where the SKU
// advertises availability zones in the location but Zone-type restrictions
// remove all of them.
//
// The second case is not merely theoretical: Azure commonly expresses a full
// subscription/region ban as a Zone-type restriction listing every zone (reason
// NotAvailableForSubscription) rather than a Location-type restriction. A SKU in
// that state is effectively unusable in the region, so it must be filtered out
// even for selectors that do not set RequireZones.
func skuRestrictedInLocation(sku *armcompute.ResourceSKU, location string) bool {
	for _, restriction := range sku.Restrictions {
		if restriction == nil || restriction.Type == nil {
			continue
		}
		if *restriction.Type != armcompute.ResourceSKURestrictionsTypeLocation {
			continue
		}
		if restrictionCoversLocation(restriction, location) {
			return true
		}
	}

	// A zonal SKU whose every advertised zone is removed by Zone-type
	// restrictions is unusable. Comparing the advertised count against the
	// remaining zones lets us tell that apart from a genuinely non-zonal SKU
	// (advertised == 0), which must not be treated as restricted. A restriction
	// listing zones beyond those advertised (Azure sometimes lists zones the SKU
	// does not even offer) still empties the available set.
	advertised, available := zonesInLocation(sku, location)
	if advertised > 0 && len(available) == 0 {
		return true
	}

	return false
}

// zonesInLocation returns, in a single pass, how many availability zones the SKU
// advertises in the location and the sorted subset that remains usable after
// Zone-type restrictions are applied. Returning both lets callers distinguish a
// non-zonal SKU (advertised == 0) from a zonal SKU whose every zone is
// restricted (advertised > 0, available empty).
func zonesInLocation(sku *armcompute.ResourceSKU, location string) (advertised int, available []string) {
	advertisedZones := sets.New[string]()
	for _, info := range sku.LocationInfo {
		if info == nil || info.Location == nil || !strings.EqualFold(*info.Location, location) {
			continue
		}
		for _, zone := range info.Zones {
			if zone != nil {
				advertisedZones.Insert(*zone)
			}
		}
	}

	restrictedZones := sets.New[string]()
	for _, restriction := range sku.Restrictions {
		if restriction == nil || restriction.Type == nil || *restriction.Type != armcompute.ResourceSKURestrictionsTypeZone {
			continue
		}
		if restriction.RestrictionInfo == nil || !restrictionCoversLocation(restriction, location) {
			continue
		}
		for _, zone := range restriction.RestrictionInfo.Zones {
			if zone != nil {
				restrictedZones.Insert(*zone)
			}
		}
	}

	return advertisedZones.Len(), sets.List(advertisedZones.Difference(restrictedZones))
}

func restrictionCoversLocation(restriction *armcompute.ResourceSKURestrictions, location string) bool {
	if restriction.RestrictionInfo != nil {
		for _, loc := range restriction.RestrictionInfo.Locations {
			if loc != nil && strings.EqualFold(*loc, location) {
				return true
			}
		}
	}
	for _, value := range restriction.Values {
		if value != nil && strings.EqualFold(*value, location) {
			return true
		}
	}
	return false
}

func skuCapabilityString(sku *armcompute.ResourceSKU, name string) (string, bool) {
	for _, capability := range sku.Capabilities {
		if capability == nil || capability.Name == nil || capability.Value == nil {
			continue
		}
		if strings.EqualFold(*capability.Name, name) {
			return *capability.Value, true
		}
	}
	return "", false
}

func skuCapabilityInt(sku *armcompute.ResourceSKU, name string) (int, bool) {
	value, ok := skuCapabilityString(sku, name)
	if !ok {
		return 0, false
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, false
	}
	return parsed, true
}

// DefaultWorkerVMSizeSelector selects the general-purpose worker SKU used by the
// default node pool. Standard_D8s_v3 is preferred to preserve historical
// behaviour; the fallback keeps the D-series general-purpose, >=8 vCPU shape.
//
// Candidates are restricted to SKU families that are enabled in the ARO-HCP RP
// instance-type allowlist (see cluster-service
// cloud-resource-constraints-config: only the plain "s" and AMD "as" D-series
// variants are enabled; the "ds"/"lds"/"ads" local-disk variants are NOT). A
// SKU that Azure advertises but the RP rejects would otherwise be selected as a
// fallback and fail node pool creation with InvalidRequestContent.
//
// The fallback pattern is capped at D8 (8 vCPUs) to avoid selecting large SKUs
// (D32, D64, D96) that are slow to provision and easily exhaust subscription
// quota in test environments. The alternation matches only the RP-allowlisted
// versions of each family: plain "s" is enabled for v3-v6, but AMD "as" is
// enabled only for v4/v5 (as_v3 and as_v6 are NOT allowlisted). The pattern
// also excludes the non-allowlisted local-disk ("ds", "lds", "ads") and Arm64
// "p" (e.g. Standard_D8ps_v6) variants.
func DefaultWorkerVMSizeSelector() VMSizeSelector {
	return VMSizeSelector{
		Name:        "default-worker",
		Preferred:   []string{DefaultWorkerVMSize, "Standard_D8s_v5", "Standard_D8as_v5", "Standard_D8s_v6"},
		NamePattern: regexp.MustCompile(`^Standard_D[4-8](s_v[3456]|as_v[45])$`),
		MinVCPUs:    8,
	}
}

// SmallWorkerVMSizeSelector selects a smaller general-purpose worker SKU used by
// tests that want faster provisioning.
//
// Candidates are restricted to RP-allowlisted D-series families (plain "s" for
// v3-v6 and AMD "as" for v4/v5 only); see DefaultWorkerVMSizeSelector for the
// rationale. The fallback pattern is capped at D4 to keep provisioning fast and
// quota use low. MinVCPUs=4 additionally excludes the 2-vCPU (D2) sizes the
// [2-4] range would otherwise admit.
func SmallWorkerVMSizeSelector() VMSizeSelector {
	return VMSizeSelector{
		Name:        "small-worker",
		Preferred:   []string{SmallWorkerVMSize, "Standard_D4s_v5", "Standard_D4as_v5", "Standard_D4s_v6"},
		NamePattern: regexp.MustCompile(`^Standard_D[2-4](s_v[3456]|as_v[45])$`),
		MinVCPUs:    4,
	}
}

// JumpboxVMSizeSelector selects a small general-purpose SKU used for throwaway
// helper VMs (e.g. the connectivity test's kubectl client).
//
// The fallback pattern is capped at D4 to keep provisioning fast and quota use
// low. The [^p] exclusion prevents matching Arm64 variants.
func JumpboxVMSizeSelector() VMSizeSelector {
	return VMSizeSelector{
		Name:        "jumpbox",
		Preferred:   []string{JumpboxVMSize, "Standard_D2s_v4", "Standard_D2ds_v5", "Standard_D2lds_v6"},
		NamePattern: regexp.MustCompile(`^Standard_D[2-4][^p]*s_v[3456]$`),
		MinVCPUs:    2,
	}
}

// EphemeralOSDiskWorkerVMSizeSelector selects a general-purpose worker SKU that
// supports ephemeral OS disks (requires local/cache storage) and is enabled in
// the ARO-HCP RP instance-type allowlist.
//
// Ephemeral OS disk support depends on the SKU generation, not just the family:
// among the RP-allowlisted x86 families, only a subset of 8-vCPU SKUs advertise
// EphemeralOSDiskSupported=True. The fallbacks are drawn from distinct families
// (Intel/AMD, D/E series) rather than larger sizes of the same family, because
// SKU restrictions typically apply to an entire family in a region, so a
// larger size of an already-restricted family is no more likely to be usable.
// All candidates are capped at 8 vCPUs to keep provisioning fast and quota use
// low. SKUs without local storage are additionally excluded via the
// RequireEphemeralOSDisk constraint.
func EphemeralOSDiskWorkerVMSizeSelector() VMSizeSelector {
	return VMSizeSelector{
		Name: "ephemeral-osdisk-worker",
		Preferred: []string{
			DefaultWorkerVMSize, // Standard_D8s_v3 - Intel D-series v3
			"Standard_D8as_v4",  // AMD D-series v4
			"Standard_E8s_v3",   // Intel E-series v3
			"Standard_E8as_v4",  // AMD E-series v4
		},
		NamePattern:            regexp.MustCompile(`^Standard_[DE]8(s_v3|as_v4)$`),
		MinVCPUs:               8,
		RequireEphemeralOSDisk: true,
	}
}

// ARM64NodePoolVMSizeSelector selects a small ARM64-capable worker SKU. The
// pattern matches a subset of the smallest (2GiB) ARM64-capable VM sizes listed
// in https://issues.redhat.com/browse/ARO-22443.
func ARM64NodePoolVMSizeSelector() VMSizeSelector {
	return VMSizeSelector{
		Name:            "arm64-worker",
		NamePattern:     regexp.MustCompile(`^Standard_D(?:2|4)pl(?:d)?s_v6$`),
		CPUArchitecture: "Arm64",
	}
}

// GPUNodePoolVMSizeSelector selects a GPU-capable worker SKU. The historical
// GPU sizes are preferred; the fallback accepts any N-series SKU that advertises
// a GPU. Callers should treat ErrNoUsableVMSize as a reason to Skip, since GPU
// capacity is scarce and frequently restricted per subscription/region.
func GPUNodePoolVMSizeSelector() VMSizeSelector {
	return VMSizeSelector{
		Name: "gpu-worker",
		Preferred: []string{
			"Standard_NC4as_T4_v3",
			"Standard_NC8as_T4_v3",
			"Standard_NC16as_T4_v3",
			"Standard_NC64as_T4_v3",
		},
		NamePattern: regexp.MustCompile(`^Standard_N`),
		RequireGPU:  true,
	}
}
