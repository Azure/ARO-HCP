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

package compute

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-HCP/fleet/pkg/azure/skucache"
)

// AllocationFailure captures why a tier could not allocate any pools.
type AllocationFailure struct {
	TierIndex int    `json:"tierIndex"`
	Cores     int64  `json:"cores"`
	Reason    string `json:"reason"`
	Message   string `json:"message"`
	Required  bool   `json:"required"`
}

// eligibleSKU is a single VM size that passed eligibility filtering.
type eligibleSKU struct {
	VMSize string
	Meta   *skucache.SKUMetadata
}

// EligibleSKUIndex is a precomputed lookup from (VM family, vCPU count) to
// the eligible SKU. Region-restricted SKUs are excluded along with SKUs lacking
// the required disk and vCPU capabilities. Zone eligibility is checked per tier.
type EligibleSKUIndex map[VMFamily]map[int64]eligibleSKU

// BuildEligibleSKUIndex indexes raw SKU metadata into a lookup table keyed by VM
// family and vCPU count, keeping only SKUs available in the cache's region that
// support an ephemeral OS disk with non-zero size and have an unconstrained,
// non-zero vCPU count. Zone eligibility is evaluated later, per tier, at allocation
// time. When multiple VM sizes in the same family share a vCPU count they are
// interchangeable for allocation. The lexicographically smallest name wins
// for deterministic selection.
func BuildEligibleSKUIndex(skuMetadata map[string]*skucache.SKUMetadata) EligibleSKUIndex {
	index := make(EligibleSKUIndex)
	for vmSize, meta := range skuMetadata {
		if meta.LocationRestricted {
			continue
		}
		if !meta.EphemeralOSDiskSupported || meta.EphemeralDiskSizeGB <= 0 {
			continue
		}
		if meta.ConstrainedVCPUs {
			continue
		}
		vcpus := meta.VCPUs
		if vcpus == 0 {
			continue
		}
		family := VMFamily(meta.Family)
		if index[family] == nil {
			index[family] = make(map[int64]eligibleSKU)
		}
		if existing, exists := index[family][vcpus]; exists && existing.VMSize < vmSize {
			continue
		}
		index[family][vcpus] = eligibleSKU{
			VMSize: vmSize,
			Meta:   meta,
		}
	}
	return index
}

// intersectZones returns the zones in which the SKU is available, preserving the
// order of the given zones.
func intersectZones(zones []string, meta *skucache.SKUMetadata) []string {
	available := make(map[string]struct{}, len(meta.Zones))
	for _, zone := range meta.Zones {
		available[zone] = struct{}{}
	}
	var out []string
	for _, zone := range zones {
		if _, ok := available[zone]; ok {
			out = append(out, zone)
		}
	}
	return out
}

// Lookup finds the SKU with exactly desiredCores vCPUs within a family.
// Returns ("", nil, false) when no exact match exists.
func (idx EligibleSKUIndex) Lookup(family VMFamily, desiredCores int64) (string, *skucache.SKUMetadata, bool) {
	sku, ok := idx[family][desiredCores]
	if !ok {
		return "", nil, false
	}
	return sku.VMSize, sku.Meta, true
}

// ComputeDesiredPools computes the desired set of node pools given
// configuration, per-family vCPU budgets, and a precomputed SKU index.
// familyLimits maps VM family names to total vCPU limits, independent of
// current usage. It returns the desired pools, failures for unallocated tiers,
// and whether every tier reached its configured node target.
//
// A per-family surge reservation is derived from processed tiers to ensure
// enough headroom for AKS node pool upgrades (one surge node of the largest
// SKU in each family). Each tier receives an available budget computed as
// raw budget minus prior consumption minus surge reservation.
func ComputeDesiredPools(
	logger logr.Logger,
	tiers []TierConfig,
	zones []string,
	familyLimits map[VMFamily]int64,
	skuIndex EligibleSKUIndex,
) ([]Pool, []AllocationFailure, bool) {
	consumed := make(map[VMFamily]int64)
	fullyAllocated := len(tiers) > 0

	var (
		pools    []Pool
		failures []AllocationFailure
	)

	for tierIndex, tier := range tiers {
		surge := maxCoresPerFamily(tiers[:tierIndex+1])
		available := make(map[VMFamily]int64, len(familyLimits))
		for family, budget := range familyLimits {
			available[family] = budget - consumed[family] - surge[family]
		}

		tierPools, tierFullyAllocated := allocateTier(logger, tier, zones, available, skuIndex)
		fullyAllocated = fullyAllocated && tierFullyAllocated
		if len(tierPools) == 0 {
			failures = append(failures, tierExhaustedFailure(tierIndex, tier, zones, skuIndex))
			continue
		}
		for _, pool := range tierPools {
			consumed[pool.Spec.Family] += int64(pool.MaxCount) * pool.Spec.VCPUs
		}
		pools = append(pools, tierPools...)
	}

	if len(pools) == 0 && len(failures) == 0 {
		failures = append(failures, AllocationFailure{
			Reason:  "NoTiersConfigured",
			Message: "no worker pool tiers are configured",
		})
	}

	return pools, failures, fullyAllocated
}

// tierLabels returns the node labels for a tier's pools: the role label derived
// from tier.Role, plus any extra labels the tier declares.
func tierLabels(tier TierConfig) map[string]string {
	labels := map[string]string{RoleLabel: string(tier.Role)}
	maps.Copy(labels, tier.Labels)
	return labels
}

// familyAllocationOrder returns the families to try for a tier, in the order
// they should be considered. PoolModePerZone keeps the operator-declared
// FamilyPriority. PoolModeRegional reorders it to spend the most zone-restricted
// families first: a regional pool sets no zones, so it runs fine on a family
// whose SKU is offered in only some zones, which preserves families with broader
// zone coverage for the PoolModePerZone tiers that actually need it. A family
// whose SKU is missing or restricted in every zone sorts last. Zone restrictions
// only block zonal deployments, so a regional pool can still use such a SKU,
// but it stays a last resort behind families offered in at least one zone.
func familyAllocationOrder(tier TierConfig, zones []string, skuIndex EligibleSKUIndex) []VMFamily {
	if tier.PoolMode != PoolModeRegional {
		return tier.FamilyPriority
	}
	unschedulable := len(zones) + 1
	rank := make(map[VMFamily]int, len(tier.FamilyPriority))
	for _, family := range tier.FamilyPriority {
		_, meta, found := skuIndex.Lookup(family, tier.Cores)
		coverage := 0
		if found {
			coverage = len(intersectZones(zones, meta))
		}
		if coverage == 0 {
			coverage = unschedulable
		}
		rank[family] = coverage
	}
	order := slices.Clone(tier.FamilyPriority)
	slices.SortStableFunc(order, func(a, b VMFamily) int {
		return rank[a] - rank[b]
	})
	return order
}

// allocateTier allocates pools for a single tier from the given per-family budgets.
func allocateTier(
	logger logr.Logger,
	tier TierConfig,
	zones []string,
	budgets map[VMFamily]int64,
	skuIndex EligibleSKUIndex,
) ([]Pool, bool) {
	switch tier.PoolMode {
	case PoolModeRegional:
		return allocateRegionalTier(logger, tier, zones, budgets, skuIndex)
	case PoolModePerZone:
		return allocatePerZoneTier(logger, tier, zones, budgets, skuIndex)
	default:
		logger.Info("unknown pool mode, skipping tier", "poolMode", tier.PoolMode)
		return nil, false
	}
}

// allocateRegionalTier allocates one zoneless pool per family, in allocation
// order, until the tier reaches MaxNodes.
func allocateRegionalTier(
	logger logr.Logger,
	tier TierConfig,
	zones []string,
	budgets map[VMFamily]int64,
	skuIndex EligibleSKUIndex,
) ([]Pool, bool) {
	var (
		pools          []Pool
		allocatedNodes int64
	)
	for _, family := range familyAllocationOrder(tier, zones, skuIndex) {
		meta, ok := tierSKU(logger, tier, family, skuIndex)
		if !ok {
			continue
		}
		// Regional pools set no availability zones, so AKS places nodes
		// anywhere in the region and any SKU is eligible, including
		// zone-restricted ones.
		maxCount := minNonNegative(budgets[family]/meta.VCPUs, tier.MaxNodes-allocatedNodes)
		if maxCount < 1 {
			continue
		}
		pools = append(pools, Pool{
			Role:              tier.Role,
			Name:              poolName(tier.Name, tier.Role, "0", meta.Name, tier.OSDiskSizeGB, tier.MaxPods, tier.EnableSwift),
			Spec:              NewVMSpecFromSKU(meta),
			AvailabilityZones: nil,
			MaxCount:          int32(maxCount),
			MinCount:          seedMinCount(tier.InitialMinNodes, maxCount),
			OSDiskSizeGB:      tier.OSDiskSizeGB,
			MaxPods:           tier.MaxPods,
			Labels:            tierLabels(tier),
			Taints:            slices.Clone(tier.Taints),
			EnableSwift:       tier.EnableSwift,
		})
		allocatedNodes += maxCount
		if allocatedNodes >= tier.MaxNodes {
			break
		}
	}
	return pools, len(pools) > 0 && allocatedNodes == tier.MaxNodes
}

// allocatePerZoneTier allocates zonal pools with the same node count in every
// tier zone: MaxNodes, or fewer when quota runs short. Each family only serves
// the tier zones its SKU is offered in, so a zone-restricted family still
// fills the zones it can. The tier zones are the ones allowing the most nodes
// per zone (see bestZones). Zones are filled one after another in family
// priority order, so each family spans as few pools as possible.
func allocatePerZoneTier(
	logger logr.Logger,
	tier TierConfig,
	zones []string,
	budgets map[VMFamily]int64,
	skuIndex EligibleSKUIndex,
) ([]Pool, bool) {
	// PoolCount is the number of zones, clamped to the number of zones
	// available (a 3-pool tier on 2 zones yields 2).
	zoneCount := min(tier.PoolCount, len(zones))
	if zoneCount == 0 {
		return nil, false
	}

	var familiesWithQuota []familyQuota
	for _, family := range familyAllocationOrder(tier, zones, skuIndex) {
		meta, ok := tierSKU(logger, tier, family, skuIndex)
		if !ok {
			continue
		}
		quotaNodes := budgets[family] / meta.VCPUs
		if quotaNodes < 1 {
			continue
		}
		familiesWithQuota = append(familiesWithQuota, familyQuota{meta: meta, quotaNodes: quotaNodes})
	}

	tierZones, nodesPerZone := bestZones(familiesWithQuota, zones, zoneCount, tier.MaxNodes)

	zoneNodesNeeded := make(map[string]int64, len(tierZones))
	for _, zone := range tierZones {
		zoneNodesNeeded[zone] = nodesPerZone
	}
	var pools []Pool
	for _, zone := range tierZones {
		for i := range familiesWithQuota {
			family := &familiesWithQuota[i]
			if !slices.Contains(family.meta.Zones, zone) {
				continue
			}
			poolNodes := mostFillableNodes(familiesWithQuota, zoneNodesNeeded, family, zone)
			if poolNodes == 0 {
				continue
			}
			family.quotaNodes -= poolNodes
			zoneNodesNeeded[zone] -= poolNodes
			pools = append(pools, Pool{
				Role:              tier.Role,
				Name:              poolName(tier.Name, tier.Role, zone, family.meta.Name, tier.OSDiskSizeGB, tier.MaxPods, tier.EnableSwift),
				Spec:              NewVMSpecFromSKU(family.meta),
				AvailabilityZones: []string{zone},
				MaxCount:          int32(poolNodes),
				MinCount:          seedMinCount(tier.InitialMinNodes, poolNodes),
				OSDiskSizeGB:      tier.OSDiskSizeGB,
				MaxPods:           tier.MaxPods,
				Labels:            tierLabels(tier),
				Taints:            slices.Clone(tier.Taints),
				EnableSwift:       tier.EnableSwift,
			})
		}
	}
	return pools, len(pools) > 0 && nodesPerZone == tier.MaxNodes
}

// familyQuota is a family's SKU for a tier and the number of nodes of that SKU
// the family's quota covers.
type familyQuota struct {
	meta       *skucache.SKUMetadata
	quotaNodes int64
}

// bestZones returns the zoneCount zones that allow the most nodes per zone,
// and that node count. Ties keep the earliest zones in the given order, so
// equally good choices never move the tier.
func bestZones(familiesWithQuota []familyQuota, zones []string, zoneCount int, maxNodes int64) ([]string, int64) {
	var (
		bestTierZones    []string
		bestNodesPerZone int64 = -1
	)
	for _, candidateZones := range combinations(zones, zoneCount) {
		nodesPerZone := maxNodesPerZone(familiesWithQuota, candidateZones, maxNodes)
		if nodesPerZone > bestNodesPerZone {
			bestTierZones, bestNodesPerZone = candidateZones, nodesPerZone
		}
	}
	return bestTierZones, bestNodesPerZone
}

// maxNodesPerZone returns the largest node count, up to maxNodes, that every
// given zone can get.
func maxNodesPerZone(familiesWithQuota []familyQuota, zones []string, maxNodes int64) int64 {
	for nodesPerZone := maxNodes; nodesPerZone > 0; nodesPerZone-- {
		zoneNodesNeeded := make(map[string]int64, len(zones))
		for _, zone := range zones {
			zoneNodesNeeded[zone] = nodesPerZone
		}
		if canFill(familiesWithQuota, zoneNodesNeeded) {
			return nodesPerZone
		}
	}
	return 0
}

// mostFillableNodes returns the most nodes, up to what the zone still needs,
// the family can give the zone while every zone can still get the nodes it
// needs afterwards. family must point into familiesWithQuota.
func mostFillableNodes(familiesWithQuota []familyQuota, zoneNodesNeeded map[string]int64, family *familyQuota, zone string) int64 {
	for poolNodes := min(zoneNodesNeeded[zone], family.quotaNodes); poolNodes > 0; poolNodes-- {
		family.quotaNodes -= poolNodes
		zoneNodesNeeded[zone] -= poolNodes
		fillable := canFill(familiesWithQuota, zoneNodesNeeded)
		family.quotaNodes += poolNodes
		zoneNodesNeeded[zone] += poolNodes
		if fillable {
			return poolNodes
		}
	}
	return 0
}

// canFill reports whether every zone can get the nodes it needs when each
// family only serves the zones its SKU is offered in. That holds exactly when
// every group of zones is offered enough quota: the families offered in at
// least one zone of the group cover the nodes the whole group needs (Hall's
// theorem).
func canFill(familiesWithQuota []familyQuota, zoneNodesNeeded map[string]int64) bool {
	zones := slices.Sorted(maps.Keys(zoneNodesNeeded))
	for groupSize := 1; groupSize <= len(zones); groupSize++ {
		for _, group := range combinations(zones, groupSize) {
			var groupNodesNeeded, groupQuotaNodes int64
			for _, zone := range group {
				groupNodesNeeded += zoneNodesNeeded[zone]
			}
			for _, family := range familiesWithQuota {
				if len(intersectZones(group, family.meta)) > 0 {
					groupQuotaNodes += family.quotaNodes
				}
			}
			if groupQuotaNodes < groupNodesNeeded {
				return false
			}
		}
	}
	return true
}

// combinations returns every choice of k of the given zones, each in the
// zones' order, starting with the earliest zones.
func combinations(zones []string, k int) [][]string {
	if k == 0 {
		return [][]string{nil}
	}
	var result [][]string
	for i := 0; i <= len(zones)-k; i++ {
		for _, rest := range combinations(zones[i+1:], k-1) {
			result = append(result, append([]string{zones[i]}, rest...))
		}
	}
	return result
}

// tierSKU returns the family's SKU for the tier, or false when the family has
// no eligible SKU with the tier's core count or its ephemeral disk is too
// small for the tier's OS disk.
func tierSKU(logger logr.Logger, tier TierConfig, family VMFamily, skuIndex EligibleSKUIndex) (*skucache.SKUMetadata, bool) {
	vmSize, meta, found := skuIndex.Lookup(family, tier.Cores)
	if !found {
		logger.Info("no eligible SKU found in family, skipping", "family", family, "desiredCores", tier.Cores)
		return nil, false
	}
	if meta.EphemeralDiskSizeGB < int64(tier.OSDiskSizeGB) {
		logger.Info("SKU ephemeral disk too small for configured OS disk size, skipping",
			"family", family, "vmSize", vmSize,
			"ephemeralDiskSizeGB", meta.EphemeralDiskSizeGB,
			"osDiskSizeGB", tier.OSDiskSizeGB)
		return nil, false
	}
	return meta, true
}

// tierExhaustedFailure creates an AllocationFailure for a tier that couldn't
// allocate any pools, classifying why: empty family list, no eligible SKU, no
// SKU available in enough zones, or insufficient quota.
func tierExhaustedFailure(tierIndex int, tier TierConfig, zones []string, skuIndex EligibleSKUIndex) AllocationFailure {
	reason := "InsufficientQuota"
	message := fmt.Sprintf("tier %d (%d cores): the families' quota does not cover at least one node per zone", tierIndex, tier.Cores)
	if tier.PoolMode == PoolModeRegional {
		message = fmt.Sprintf("tier %d (%d cores): the families' quota does not cover at least one node", tierIndex, tier.Cores)
	}

	if len(tier.FamilyPriority) == 0 {
		reason = "NoEligibleFamily"
		message = fmt.Sprintf("tier %d (%d cores): family priority list is empty", tierIndex, tier.Cores)
	} else {
		hasEligible := false
		offeredZones := make(map[string]bool)
		for _, family := range tier.FamilyPriority {
			_, meta, found := skuIndex.Lookup(family, tier.Cores)
			if !found || meta.EphemeralDiskSizeGB < int64(tier.OSDiskSizeGB) {
				continue
			}
			hasEligible = true
			for _, zone := range intersectZones(zones, meta) {
				offeredZones[zone] = true
			}
		}
		// Regional pools set no zones. A zonal tier needs its zones covered by
		// the eligible families together.
		hasZoneCoverage := tier.PoolMode != PoolModePerZone || len(offeredZones) >= min(tier.PoolCount, len(zones))
		switch {
		case !hasEligible:
			reason = "NoEligibleSKU"
			message = fmt.Sprintf("tier %d (%d cores): no family has an eligible SKU with exactly %d vCPUs (unrestricted in region, unconstrained vCPUs, ephemeral OS disk of at least %d GB)", tierIndex, tier.Cores, tier.Cores, tier.OSDiskSizeGB)
		case !hasZoneCoverage:
			reason = "NoZoneCoverage"
			message = fmt.Sprintf("tier %d (%d cores): the eligible families' SKUs are not offered in enough zones", tierIndex, tier.Cores)
		}
	}

	return AllocationFailure{
		TierIndex: tierIndex,
		Cores:     tier.Cores,
		Reason:    reason,
		Message:   message,
		Required:  tier.Required,
	}
}

// poolName generates a deterministic pool name. Format:
// <symbolicName><zone><hash> where symbolicName is the tier's stable identifier
// (1-5 chars), zone is the availability zone digit, and hash is a 6-character
// hex prefix of the SHA-256 of the pool's identity fields (Role, VMSize,
// OSDiskSizeGB, MaxPods, EnableSwift). Role changes require replacement rather
// than relabeling an existing pool. Changing any of those fields changes the
// hash, renaming the pool so the reconciler replaces it — the only correct
// response to an immutable-field change. Zone is excluded from the hash so
// per-zone pools of the same spec share the same hash suffix. Name uniqueness
// within a cluster is guaranteed structurally by (symbolicName, zone), not by
// the hash, so a 24-bit truncation is safe; it only guards change detection.
func poolName(symbolicName string, role PoolRole, zone string, vmSize string, osDiskSizeGB int32, maxPods int32, enableSwift bool) string {
	input := fmt.Sprintf("%s|%s|%d|%d|%t", role, vmSize, osDiskSizeGB, maxPods, enableSwift)
	sum := sha256.Sum256([]byte(input))
	return fmt.Sprintf("%s%s%s", symbolicName, zone, hex.EncodeToString(sum[:])[:6])
}

// RequiredTierFailed returns true if any allocation failure is for a required tier.
func RequiredTierFailed(failures []AllocationFailure) bool {
	for _, failure := range failures {
		if failure.Required {
			return true
		}
	}
	return false
}

// FailureSummary joins every allocation failure's message into a single
// operator-facing string. Returns "" if failures is empty.
func FailureSummary(failures []AllocationFailure) string {
	messages := make([]string, len(failures))
	for i, failure := range failures {
		messages[i] = failure.Message
	}
	return strings.Join(messages, "; ")
}

// maxCoresPerFamily returns the largest core count per family across the
// given tiers. Used as the surge reservation: one upgrade surge node of the
// biggest SKU in each family.
func maxCoresPerFamily(tiers []TierConfig) map[VMFamily]int64 {
	result := make(map[VMFamily]int64)
	for _, tier := range tiers {
		for _, family := range tier.FamilyPriority {
			if tier.Cores > result[family] {
				result[family] = tier.Cores
			}
		}
	}
	return result
}

// seedMinCount derives the create-time autoscaler floor from a tier's
// InitialMinNodes: zero means 1, and the result is clamped to maxCount so a
// budget-constrained pool never gets MinCount > MaxCount.
func seedMinCount(initialMinNodes, maxCount int64) int32 {
	if initialMinNodes < 1 {
		initialMinNodes = 1
	}
	if initialMinNodes > maxCount {
		initialMinNodes = maxCount
	}
	return int32(initialMinNodes)
}

// minNonNegative returns the smaller of a and b, clamped to 0 so a negative
// input (e.g. exhausted quota) yields "no nodes" rather than a negative count.
func minNonNegative(a, b int64) int64 {
	return max(min(a, b), 0)
}
