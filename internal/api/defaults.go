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
//   - Version constructors (SetDefaultValues* in v*/methods.go, called from New* constructors)
//   - Internal constructors (NewDefault* in types_*.go)
//
// String enum defaults (NetworkTypeOVNKubernetes, VisibilityPublic,
// OutboundTypeLoadBalancer, DiskStorageAccountTypePremium_LRS,
// EtcdDataEncryptionKeyManagementModeTypePlatformManaged,
// ClusterImageRegistryStateEnabled) already exist as typed
// constants in enums.go and are used directly by canonical defaults
// (EnsureDefaults methods in types_*.go). No new constants are needed
// for those.
//
// See docs/api-version-defaults-and-storage.md for architecture.

// Cluster defaults
const (
	DefaultClusterVersionChannelGroup               = "stable"
	DefaultClusterNetworkPodCIDR                    = "10.128.0.0/14"
	DefaultClusterNetworkServiceCIDR                = "172.30.0.0/16"
	DefaultClusterNetworkMachineCIDR                = "10.0.0.0/16"
	DefaultClusterNetworkHostPrefix           int32 = 23
	DefaultClusterMaxPodGracePeriodSeconds    int32 = 600
	DefaultClusterMaxNodeProvisionTimeSeconds int32 = 900
	DefaultClusterPodPriorityThreshold        int32 = -10
)

// NodePool defaults
//
// DefaultNodePoolVersionChannelGroup is intentionally separate from
// DefaultClusterVersionChannelGroup to allow independent evolution.
const (
	DefaultNodePoolVersionChannelGroup       = "stable"
	DefaultNodePoolOSDiskSizeGiB       int32 = 64
)
