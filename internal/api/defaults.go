// Copyright 2025 Microsoft Corporation
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

package api

// Default values for non-enum fields in HCPOpenShiftCluster and
// HCPOpenShiftClusterNodePool.
//
// These constants are the canonical source of truth for bare literal
// defaults referenced by:
//   - API-version defaults (SetDefaultValues* in v*/methods.go)
//   - Internal constructors (NewDefault* in types_*.go)
//
// String enum defaults (NetworkTypeOVNKubernetes, VisibilityPublic,
// OutboundTypeLoadBalancer, DiskStorageAccountTypePremium_LRS,
// EtcdDataEncryptionKeyManagementModeTypePlatformManaged,
// ClusterImageRegistryProfileStateEnabled) already exist as typed
// constants in enums.go and are used directly by storage defaults
// in internal/database/convert_*.go. No new constants are needed
// for those.
//
// See docs/api-version-defaults-and-storage.md for architecture.

// Shared defaults (used by both Cluster and NodePool)
const (
	DefaultVersionChannelGroup = "stable"
)

// Cluster defaults
const (
	DefaultNetworkPodCIDR                  = "10.128.0.0/14"
	DefaultNetworkServiceCIDR              = "172.30.0.0/16"
	DefaultNetworkMachineCIDR              = "10.0.0.0/16"
	DefaultNetworkHostPrefix         int32 = 23
	DefaultMaxPodGracePeriodSeconds  int32 = 600
	DefaultMaxNodeProvisionTimeSeconds  int32 = 900
	DefaultPodPriorityThreshold      int32 = -10
)

// NodePool defaults
//
// DefaultNodePoolAutoRepair MUST NOT be used in storage defaults.
// AutoRepair uses bool+omitempty in the internal type, making it
// unsafe for storage defaulting (false is indistinguishable from
// "never set"). See DDR for details.
const (
	DefaultNodePoolAutoRepair          = true
	DefaultNodePoolOSDiskSizeGiB int32 = 64
)
