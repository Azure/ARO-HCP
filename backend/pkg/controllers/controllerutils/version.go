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

package controllerutils

import (
	"fmt"

	"github.com/blang/semver/v4"
)

// MinServingCAOCPVersion is the minimum OpenShift version for which the
// control-plane serving CA is mirrored into the service cluster (via a
// per-cluster ReadDesire) and gated on during cluster creation. Clusters
// below this version never populate
// ServiceProviderCluster.Status.ServingCABundle, so both the serving CA
// ReadDesire creation and the create-operation completion check must skip the
// serving CA for them.
const MinServingCAOCPVersion = "4.20"

// ClusterVersionAtLeast reports whether versionID is greater than or equal to
// minVersion. An empty versionID returns false (the cluster version is not
// known yet). Both versions are parsed tolerantly (a leading "v" and missing
// patch components are accepted).
func ClusterVersionAtLeast(versionID, minVersion string) (bool, error) {
	if len(versionID) == 0 {
		return false, nil
	}
	current, err := semver.ParseTolerant(versionID)
	if err != nil {
		return false, fmt.Errorf("failed to parse cluster version %q: %w", versionID, err)
	}
	minimum, err := semver.ParseTolerant(minVersion)
	if err != nil {
		return false, fmt.Errorf("failed to parse minimum version %q: %w", minVersion, err)
	}
	return current.GE(minimum), nil
}
