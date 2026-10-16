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

package metadataapi

import (
	"k8s.io/apimachinery/pkg/util/sets"
)

const (
	OpenShiftVersionPrefix = "openshift-v"
)

// AllowedMajorUpgrades is for OpenShift cross-major upgrades (one major at a time, e.g. 4 to 5). Keys and values
// are major.minor lines ("x.y"): the key is the current version's line, the value is the only allowed line for
// the desired version. Used when validating version on clusters and node pools.
// Add entries when new cross-major paths are supported.
var AllowMajorUpgradePaths = map[string]string{
	"4.22": "5.0",
	"4.23": "5.1",
}

// AllowControlPlaneNodePoolMajorVersionSkew maps node pool OpenShift minor release lines (x.y) to allowed
// control-plane minor release lines when the node pool major differs from the cluster major. Values are
// sorted from strictest to most permissive allowed skew. See the HyperShift control plane version status
// enhancement for node pool skews against cluster version:
// https://github.com/openshift/enhancements/blob/master/enhancements/hypershift/hypershift-control-plane-version-status.md
var AllowControlPlaneNodePoolMajorVersionSkew = map[string][]string{
	"4.21": {"5.0"},
	"4.22": {"5.0", "5.1"},
	"4.23": {"5.1", "5.2"},
}

// OpenShift version update channel groups.
const (
	ChannelGroupStable    = "stable"
	ChannelGroupFast      = "fast"
	ChannelGroupCandidate = "candidate"
	// ChannelGroupNightly builds are published to the CI releasestream API rather
	// than the Cincinnati graph API used for version selection, so a nightly
	// version cannot be resolved from a bare major.minor and must be pinned to a
	// full major.minor.patch (see validateVersionProfile and the control plane
	// desired version controller).
	ChannelGroupNightly = "nightly"
)

// AllowedChannelGroups is the set ARO-HCP allows to use for customer purposes
var AllowedChannelGroups = sets.New(ChannelGroupStable, ChannelGroupFast)

// AllowedChannelGroupsWithExperimentalFlag is the set the service allows to use when using the Experimental Feature AFEC flag
var AllowedChannelGroupsWithExperimentalFlag = sets.New(ChannelGroupStable, ChannelGroupFast, ChannelGroupCandidate, ChannelGroupNightly)
