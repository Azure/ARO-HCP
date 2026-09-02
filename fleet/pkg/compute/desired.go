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
	"math"
	"slices"
	"strconv"
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
	MemoryGB      int64    `json:"memoryGB"`
	SecondaryNICs int64    `json:"secondaryNICs"`
}

func NewVMSpecFromSKU(meta *skucache.SKUMetadata) VMSpec {
	return VMSpec{
		Size:          meta.Name,
		Family:        VMFamily(meta.Family),
		VCPUs:         meta.VCPUs,
		MemoryGB:      meta.MemoryGB,
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
	Role              PoolRole          `json:"role"`
	Name              string            `json:"name"`
	Spec              VMSpec            `json:"spec"`
	AvailabilityZones []string          `json:"zones"`
	MaxCount          int32             `json:"maxCount"`
	OSDiskSizeGB      int32             `json:"osDiskSizeGB"`
	MaxPods           int32             `json:"maxPods"`
	Labels            map[string]string `json:"labels,omitempty"`
	Taints            []string          `json:"taints,omitempty"`
	EnableSwift       bool              `json:"enableSwift,omitempty"`
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

// BuildEligibleSKUIndex filters and indexes raw SKU metadata into a lookup
// table keyed by VM family and vCPU count. Only SKUs available in all
// requiredZones are included. When multiple VM sizes in the same family
// have the same vCPU count they are interchangeable for allocation, so the
// lexicographically smallest name wins purely for deterministic selection.
func BuildEligibleSKUIndex(skuMetadata map[string]*skucache.SKUMetadata, requiredZones []string) EligibleSKUIndex {
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
		if !skuSupportsAllZones(meta, requiredZones) {
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

func skuSupportsAllZones(meta *skucache.SKUMetadata, requiredZones []string) bool {
	if len(requiredZones) == 0 {
		return true
	}
	zoneSet := make(map[string]struct{}, len(meta.Zones))
	for _, zone := range meta.Zones {
		zoneSet[zone] = struct{}{}
	}
	for _, zone := range requiredZones {
		if _, ok := zoneSet[zone]; !ok {
			return false
		}
	}
	return true
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
// familyBudgets maps VM family names to available vCPU counts (quota limit
// minus non-worker pool reservations). It returns the desired pools and any
// allocation failures explaining why tiers could not be satisfied.
//
// A per-family surge reservation is derived from processed tiers to ensure
// enough headroom for AKS node pool upgrades (one surge node of the largest
// SKU in each family). Each tier receives an available budget computed as
// raw budget minus prior consumption minus surge reservation.
func ComputeDesiredPools(
	logger logr.Logger,
	tiers []TierConfig,
	zones []string,
	familyBudgets map[VMFamily]int64,
	skuIndex EligibleSKUIndex,
) ([]Pool, []AllocationFailure) {
	consumed := make(map[VMFamily]int64)

	var (
		pools    []Pool
		failures []AllocationFailure
	)

	for tierIndex, tier := range tiers {
		surge := maxCoresPerFamily(tiers[:tierIndex+1])
		available := make(map[VMFamily]int64, len(familyBudgets))
		for family, budget := range familyBudgets {
			available[family] = budget - consumed[family] - surge[family]
		}

		tierPools := allocateTier(logger, tier, zones, available, skuIndex)
		if len(tierPools) == 0 {
			failures = append(failures, tierExhaustedFailure(tierIndex, tier, skuIndex))
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

	return pools, failures
}

// allocateTier allocates pools for a single tier from the given per-family budgets.
func allocateTier(
	logger logr.Logger,
	tier TierConfig,
	zones []string,
	budgets map[VMFamily]int64,
	skuIndex EligibleSKUIndex,
) []Pool {
	seen := make(map[VMFamily]struct{})
	zoneCount := int64(len(zones))

	var (
		pools          []Pool
		runningPerPool int64
	)

	for _, family := range tier.FamilyPriority {
		if _, duplicate := seen[family]; duplicate {
			continue
		}
		seen[family] = struct{}{}

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
		case PoolModeSpanZones:
			quotaNodes := budget / vcpusPerNode
			maxCount := minNonNegative(quotaNodes, remaining)
			if maxCount < 1 {
				continue
			}
			pools = append(pools, Pool{
				Role:              tier.Role,
				Name:              poolName(tier.Role, vmSize, 0, tier.OSDiskSizeGB),
				Spec:              NewVMSpecFromSKU(meta),
				AvailabilityZones: zones,
				MaxCount:          int32(maxCount),
				OSDiskSizeGB:      tier.OSDiskSizeGB,
				MaxPods:           tier.MaxPods,
				Labels:            maps.Clone(tier.Labels),
				Taints:            slices.Clone(tier.Taints),
				EnableSwift:       tier.EnableSwift,
			})
			runningPerPool += maxCount

		case PoolModePerZone:
			if zoneCount == 0 {
				continue
			}
			// PoolCount caps the number of zonal pools at the first N zones,
			// matching aks/pool.bicep's poolCount semantics. Zero means one
			// pool per zone.
			poolZones := zones
			if tier.PoolCount > 0 && tier.PoolCount < len(zones) {
				poolZones = zones[:tier.PoolCount]
			}
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
				// Zones are validated as single digits 1-9 at startup
				// (cmd/controller options), so ParseInt cannot fail here.
				zoneInt, _ := strconv.ParseInt(zone, 10, 64)
				pools = append(pools, Pool{
					Role:              tier.Role,
					Name:              poolName(tier.Role, vmSize, zoneInt, tier.OSDiskSizeGB),
					Spec:              NewVMSpecFromSKU(meta),
					AvailabilityZones: []string{zone},
					MaxCount:          int32(perZone),
					OSDiskSizeGB:      tier.OSDiskSizeGB,
					MaxPods:           tier.MaxPods,
					Labels:            maps.Clone(tier.Labels),
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

	return pools
}

// tierExhaustedFailure creates an AllocationFailure for a tier that couldn't
// allocate any pools. The reason distinguishes between no eligible SKUs and
// insufficient quota.
func tierExhaustedFailure(tierIndex int, tier TierConfig, skuIndex EligibleSKUIndex) AllocationFailure {
	reason := "InsufficientQuota"
	message := fmt.Sprintf("tier %d (%d cores): no family has enough quota for at least one node per zone", tierIndex, tier.Cores)

	if len(tier.FamilyPriority) == 0 {
		reason = "NoEligibleFamily"
		message = fmt.Sprintf("tier %d (%d cores): family priority list is empty", tierIndex, tier.Cores)
	} else {
		hasEligible := false
		for _, family := range tier.FamilyPriority {
			if _, _, found := skuIndex.Lookup(family, tier.Cores); found {
				hasEligible = true
				break
			}
		}
		if !hasEligible {
			reason = "NoEligibleSKU"
			message = fmt.Sprintf("tier %d (%d cores): no family has an eligible SKU (ephemeral OS disk support required)", tierIndex, tier.Cores)
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

// poolName generates a deterministic pool name from the immutable pool spec
// fields. Format: <rolePrefix><zone><hash> where rolePrefix is s (system),
// i (infra), or w (worker), and hash is a 10-character hex prefix of the
// SHA-256 of VMSize and OSDiskSizeGB. The zone is part of the prefix for
// readability but excluded from the hash so that pools of the same SKU across
// zones share the same hash suffix. A 40-bit (10 hex char) truncation is
// collision-safe for the small number of distinct SKUs a cluster runs.
func poolName(role PoolRole, vmSize string, zone int64, osDiskSizeGB int32) string {
	prefix := "w"
	switch role {
	case PoolRoleSystem:
		prefix = "s"
	case PoolRoleInfra:
		prefix = "i"
	}
	input := vmSize + "|" + strconv.FormatInt(int64(osDiskSizeGB), 10)
	sum := sha256.Sum256([]byte(input))
	return fmt.Sprintf("%s%d%s", prefix, zone, hex.EncodeToString(sum[:])[:10])
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

// BestFailureReason returns the most actionable reason from a set of
// allocation failures. InsufficientQuota wins because it's the most
// operationally actionable (request quota increase). Returns "" if failures
// is empty.
func BestFailureReason(failures []AllocationFailure) string {
	if len(failures) == 0 {
		return ""
	}

	reasonPriority := map[string]int{
		"InsufficientQuota": 0,
		"NoEligibleSKU":     1,
		"NoEligibleFamily":  2,
		"NoTiersConfigured": 3,
	}

	best := failures[0].Reason
	for _, failure := range failures[1:] {
		if reasonPriority[failure.Reason] < reasonPriority[best] {
			best = failure.Reason
		}
	}
	return best
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

// minNonNegative returns the smallest of the given values, clamped to 0 so a
// negative input (e.g. exhausted quota) yields "no nodes" rather than a
// negative count.
func minNonNegative(values ...int64) int64 {
	result := int64(math.MaxInt64)
	for _, v := range values {
		if v < result {
			result = v
		}
	}
	if result < 0 {
		return 0
	}
	return result
}
