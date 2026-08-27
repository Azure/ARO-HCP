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

package versionrollout

import (
	"fmt"
	"strings"

	"github.com/blang/semver/v4"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

// minorString returns the "major.minor" string for a version (e.g. "4.21").
func minorString(v semver.Version) string {
	return fmt.Sprintf("%d.%d", v.Major, v.Minor)
}

// yStreamChannel builds a y-stream channel name from a channel group and a minor
// version, e.g. ("stable", "4.21") -> "stable-4.21".
func yStreamChannel(channelGroup, minor string) string {
	return channelGroup + "-" + minor
}

// parseYStreamChannel splits a y-stream channel name into its channel group and
// minor version, e.g. "stable-4.21" -> ("stable", "4.21"). It splits on the
// first "-"; channel groups (stable/fast/candidate/nightly) contain no "-".
func parseYStreamChannel(channel string) (channelGroup, minor string, ok bool) {
	parts := strings.SplitN(channel, "-", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// earliestActiveVersionEntry returns the earliest (oldest) active-version entry.
// The active-version list is ordered newest-first and truncated at the first
// completed update, so the last element is the completed base the control plane
// is currently running. A cluster has "achieved" version v when its earliest
// active version equals v (i.e. the list has collapsed to just the completed
// target). Returns nil when there are no active versions.
func earliestActiveVersionEntry(activeVersions []coreapi.HCPClusterActiveVersion) *coreapi.HCPClusterActiveVersion {
	for i := len(activeVersions) - 1; i >= 0; i-- {
		if activeVersions[i].Version != nil {
			return &activeVersions[i]
		}
	}
	return nil
}

// earliestActiveVersion returns the earliest (oldest) version present in a
// cluster's active versions, or nil when there are none.
func earliestActiveVersion(activeVersions []coreapi.HCPClusterActiveVersion) *semver.Version {
	if entry := earliestActiveVersionEntry(activeVersions); entry != nil {
		return entry.Version
	}
	return nil
}

// maxVersion returns the greater of two versions. nil is treated as "no bound",
// so maxVersion(nil, x) == x and maxVersion(x, nil) == x.
func maxVersion(a, b *semver.Version) *semver.Version {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	case a.GTE(*b):
		return a
	default:
		return b
	}
}

// setDesiredVersion sets the cluster's desired control-plane version and, when
// the version actually changes, records the transition time. This transition
// time feeds the rollout's mismatch/failure accounting.
func setDesiredVersion(serviceProviderCluster *coreapi.ServiceProviderCluster, newDesired *semver.Version, now metav1.Time) {
	current := serviceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion
	if current != nil && newDesired != nil && current.EQ(*newDesired) {
		return
	}
	serviceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion = newDesired
	serviceProviderCluster.Spec.ControlPlaneVersion.DesiredVersionLastTransitionTime = &now
}

// versionString renders a possibly-nil version for logging.
func versionString(v *semver.Version) string {
	if v == nil {
		return "<none>"
	}
	return v.String()
}

// percentOfCeil returns ceil(pct/100 * total), so a small non-zero percentage of
// a small fleet still rounds up to at least one cluster.
func percentOfCeil(pct int, total int64) int64 {
	if pct <= 0 || total <= 0 {
		return 0
	}
	return (int64(pct)*total + 99) / 100
}
