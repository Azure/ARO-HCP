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

// VMFamily is a named string type for Azure VM family identifiers (e.g.
// "standardEDSv6Family"). It disambiguates map keys from raw VM size strings.
type VMFamily string

// VMSpec captures the hardware characteristics of a VM size. Bundling these
// fields into a single struct makes it impossible to construct a Pool
// without specifying family, vCPU count, and NIC count — preventing silent
// zero-value bugs in tests and production code.
type VMSpec struct {
	Size          string   `json:"size"`
	Family        VMFamily `json:"family"`
	VCPUs         int64    `json:"vcpus"`
	MemoryGiB     int64    `json:"memoryGiB"`
	SecondaryNICs int64    `json:"secondaryNICs"`
}

func NewVMSpecFromSKU(meta *skucache.SKUMetadata) VMSpec {
	return VMSpec{
		Size:          meta.Name,
		Family:        VMFamily(meta.Family),
		VCPUs:         meta.VCPUs,
		MemoryGiB:     meta.MemoryGB,
		SecondaryNICs: meta.SecondaryNICs,
	}
}

// NetworkConfig holds per-cluster configuration derived from the system pool at
// runtime. These values are the same for all pools on a cluster.
type NetworkConfig struct {
	VnetSubnetID string
	PodSubnetID  string
}

// Pool describes a single AKS node pool spec. Used for both desired
// state (from computation) and current state (projected from AKS).
type Pool struct {
	Role              PoolRole `json:"role"`
	Name              string   `json:"name"`
	Spec              VMSpec   `json:"spec"`
	AvailabilityZones []string `json:"zones"`
	MaxCount          int32    `json:"maxCount"`
	// InitialMinCount is the autoscaler floor to seed at pool creation, derived
	// from TierConfig.InitialMinNodes clamped to MaxCount. It is only consumed by
	// the create path; the controller preserves the live MinCount thereafter.
	InitialMinCount int32             `json:"initialMinCount"`
	OSDiskSizeGB    int32             `json:"osDiskSizeGB"`
	MaxPods         int32             `json:"maxPods"`
	Labels          map[string]string `json:"labels,omitempty"`
	Taints          []string          `json:"taints,omitempty"`
	EnableSwift     bool              `json:"enableSwift,omitempty"`
}

// ZoneString returns a comma-separated zone list for logging.
func (p Pool) ZoneString() string {
	return strings.Join(p.AvailabilityZones, ",")
}

// eligibleSKU is a single VM size that passed eligibility filtering.
type eligibleSKU struct {
	VMSize string
	Meta   *skucache.SKUMetadata
}

// EligibleSKUIndex is a precomputed lookup from (VM family, vCPU count) to
// the eligible SKU. Only SKUs that support ephemeral OS disks, have non-zero
// disk and vCPU counts, and are available in all required zones are included.
type EligibleSKUIndex map[VMFamily]map[int64]eligibleSKU

// BuildEligibleSKUIndex indexes raw SKU metadata into a lookup table keyed by VM
// family and vCPU count, keeping only SKUs that support an ephemeral OS disk with
// non-zero size and have an unconstrained, non-zero vCPU count. Zone eligibility
// is evaluated later, per tier, at allocation time. When multiple VM sizes in the
// same family share a vCPU count they are interchangeable for allocation, so the
// lexicographically smallest name wins for deterministic selection.
func BuildEligibleSKUIndex(skuMetadata map[string]*skucache.SKUMetadata) EligibleSKUIndex {
	index := make(EligibleSKUIndex)
	for vmSize, meta := range skuMetadata {
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
// order of the given zones. Used for PoolModePerZone to pick which zones a tier's
// pools land in, so a zone-restricted SKU can still serve the tier as long as
// enough of its zones remain.
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

// tierSKUCoversZones reports whether the SKU can serve the tier: PoolModePerZone
// needs it available in at least min(PoolCount, len(zones)) zones; PoolModeRegional
// sets no zones and accepts any SKU. It mirrors the per-mode eligibility in
// allocateTier and only classifies allocation failures.
func tierSKUCoversZones(tier TierConfig, zones []string, meta *skucache.SKUMetadata) bool {
	if tier.PoolMode != PoolModePerZone {
		return true
	}
	return len(intersectZones(zones, meta)) >= min(tier.PoolCount, len(zones))
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
// zone coverage for the PoolModePerZone tiers that actually need it. We only
// target zonal regions, so a family whose SKU is unavailable or offered in zero
// zones (restricted in every zone) is unschedulable; those sort last so a
// regional pool never prefers a broken SKU over a usable one.
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
	var (
		pools          []Pool
		runningPerPool int64
	)

	for _, family := range familyAllocationOrder(tier, zones, skuIndex) {
		vmSize, meta, found := skuIndex.Lookup(family, tier.Cores)
		if !found {
			logger.Info("no eligible SKU found in family, skipping", "family", family, "desiredCores", tier.Cores)
			continue
		}

		if meta.EphemeralDiskSizeGB < int64(tier.OSDiskSizeGB) {
			logger.Info("SKU ephemeral disk too small for configured OS disk size, skipping",
				"family", family, "vmSize", vmSize,
				"ephemeralDiskSizeGB", meta.EphemeralDiskSizeGB,
				"osDiskSizeGB", tier.OSDiskSizeGB)
			continue
		}

		vcpusPerNode := meta.VCPUs

		budget := budgets[family]
		if budget <= 0 {
			continue
		}

		remaining := tier.MaxNodes - runningPerPool

		switch tier.PoolMode {
		case PoolModeRegional:
			// Regional pools set no availability zones, so AKS places nodes
			// anywhere in the region and any SKU is eligible, including
			// zone-restricted ones.
			quotaNodes := budget / vcpusPerNode
			maxCount := minNonNegative(quotaNodes, remaining)
			if maxCount < 1 {
				continue
			}
			pools = append(pools, Pool{
				Role:              tier.Role,
				Name:              poolName(tier.Name, tier.Role, "0", vmSize, tier.OSDiskSizeGB, tier.MaxPods, tier.EnableSwift),
				Spec:              NewVMSpecFromSKU(meta),
				AvailabilityZones: nil,
				MaxCount:          int32(maxCount),
				InitialMinCount:   seedMinCount(tier.InitialMinNodes, maxCount),
				OSDiskSizeGB:      tier.OSDiskSizeGB,
				MaxPods:           tier.MaxPods,
				Labels:            tierLabels(tier),
				Taints:            slices.Clone(tier.Taints),
				EnableSwift:       tier.EnableSwift,
			})
			runningPerPool += maxCount

		case PoolModePerZone:
			// PoolCount is the number of zonal pools, clamped to the number of
			// zones available (a 3-pool tier on 2 zones yields 2).
			targetCount := min(tier.PoolCount, len(zones))
			if targetCount == 0 {
				continue
			}
			// Each pool must land in a zone where the SKU is offered. Take the
			// first targetCount zones in which the SKU is available; if fewer
			// remain, it cannot serve the tier — try the next family in priority
			// order.
			poolZones := intersectZones(zones, meta)
			if len(poolZones) < targetCount {
				continue
			}
			poolZones = poolZones[:targetCount]
			poolCount := int64(len(poolZones))
			// Integer division deliberately floors: splitting the family
			// budget evenly across pools can waste up to
			// (poolCount-1)*vcpusPerNode vCPUs, which is the conservative
			// choice — never provision beyond quota.
			quotaPerZone := budget / (vcpusPerNode * poolCount)
			perZone := minNonNegative(quotaPerZone, remaining)
			if perZone < 1 {
				continue
			}
			for _, zone := range poolZones {
				pools = append(pools, Pool{
					Role:              tier.Role,
					Name:              poolName(tier.Name, tier.Role, zone, vmSize, tier.OSDiskSizeGB, tier.MaxPods, tier.EnableSwift),
					Spec:              NewVMSpecFromSKU(meta),
					AvailabilityZones: []string{zone},
					MaxCount:          int32(perZone),
					InitialMinCount:   seedMinCount(tier.InitialMinNodes, perZone),
					OSDiskSizeGB:      tier.OSDiskSizeGB,
					MaxPods:           tier.MaxPods,
					Labels:            tierLabels(tier),
					Taints:            slices.Clone(tier.Taints),
					EnableSwift:       tier.EnableSwift,
				})
			}
			runningPerPool += perZone

		default:
			logger.Info("unknown pool mode, skipping tier", "poolMode", tier.PoolMode)
			continue
		}

		if runningPerPool >= tier.MaxNodes {
			break
		}
	}

	return pools, len(pools) > 0 && runningPerPool == tier.MaxNodes
}

// tierExhaustedFailure creates an AllocationFailure for a tier that couldn't
// allocate any pools, classifying why: empty family list, no eligible SKU, no
// SKU available in enough zones, or insufficient quota.
func tierExhaustedFailure(tierIndex int, tier TierConfig, zones []string, skuIndex EligibleSKUIndex) AllocationFailure {
	reason := "InsufficientQuota"
	message := fmt.Sprintf("tier %d (%d cores): no family has enough quota for at least one node per zone", tierIndex, tier.Cores)

	if len(tier.FamilyPriority) == 0 {
		reason = "NoEligibleFamily"
		message = fmt.Sprintf("tier %d (%d cores): family priority list is empty", tierIndex, tier.Cores)
	} else {
		hasEligible := false
		hasZoneCoverage := false
		for _, family := range tier.FamilyPriority {
			_, meta, found := skuIndex.Lookup(family, tier.Cores)
			if !found {
				continue
			}
			hasEligible = true
			if tierSKUCoversZones(tier, zones, meta) {
				hasZoneCoverage = true
				break
			}
		}
		switch {
		case !hasEligible:
			reason = "NoEligibleSKU"
			message = fmt.Sprintf("tier %d (%d cores): no family has an eligible SKU (ephemeral OS disk support required)", tierIndex, tier.Cores)
		case !hasZoneCoverage:
			reason = "NoZoneCoverage"
			message = fmt.Sprintf("tier %d (%d cores): no family has a SKU available in the required zones", tierIndex, tier.Cores)
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
