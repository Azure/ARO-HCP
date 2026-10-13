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

// Package compute holds the shared node pool types (Pool, VMSpec, roles,
// capacity) used by the AKS cluster creation tool and the fleet nodepool
// controller so a freshly-created cluster and a controller-reconciled one
// describe pools identically.
package compute

import (
	"strings"

	"github.com/Azure/ARO-HCP/fleet/pkg/azure/skucache"
)

// PoolRole identifies the operational role of a node pool tier.
type PoolRole string

const (
	PoolRoleSystem PoolRole = "system"
	PoolRoleInfra  PoolRole = "infra"
	PoolRoleWorker PoolRole = "worker"
)

const RoleLabel = "aro-hcp.azure.com/role"

const (
	TaintCriticalAddonsOnly = "CriticalAddonsOnly=true:NoSchedule"
	TaintInfra              = "infra=true:NoSchedule"
)

// PoolMode controls how a tier maps to AKS agent pools.
type PoolMode string

const (
	// PoolModePerZone creates TierConfig.PoolCount pools, each pinned to a single
	// availability zone.
	PoolModePerZone PoolMode = "PerZone"

	// PoolModeRegional creates a single pool with no availability zones set, so
	// AKS places its nodes anywhere in the region without zonal pinning. Because
	// it never requests a specific zone, it is the only mode that can use
	// zone-restricted SKUs.
	PoolModeRegional PoolMode = "Regional"
)

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
	MemoryBytes   int64    `json:"memoryBytes"`
	SecondaryNICs int64    `json:"secondaryNICs"`
}

func NewVMSpecFromSKU(meta *skucache.SKUMetadata) VMSpec {
	return VMSpec{
		Size:          meta.Name,
		Family:        VMFamily(meta.Family),
		VCPUs:         meta.VCPUs,
		MemoryBytes:   meta.MemoryBytes,
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
	// MinCount is the configured autoscaler floor. Proposed creates seed it
	// from TierConfig.InitialMinNodes; subsequent planner actions preserve the
	// observed floor unless a lower ceiling requires clamping it.
	MinCount     int32             `json:"minCount"`
	OSDiskSizeGB int32             `json:"osDiskSizeGB"`
	MaxPods      int32             `json:"maxPods"`
	Labels       map[string]string `json:"labels,omitempty"`
	Taints       []string          `json:"taints,omitempty"`
	EnableSwift  bool              `json:"enableSwift,omitempty"`
}

// ZoneString returns a comma-separated zone list for logging.
func (p Pool) ZoneString() string {
	return strings.Join(p.AvailabilityZones, ",")
}
