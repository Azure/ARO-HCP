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

package clusterresources

import semver "github.com/blang/semver/v4"

// minVersionForLegacyIngressDesire is the minimum version for which the legacy ingress desire is suppressed
// until the CPO override with https://github.com/openshift/hypershift/pull/9132 for all versions
// up to 4.20 is released. We set it to 5.1 because the PR 9132 was merged in 5.1.0.
var minVersionForLegacyIngressDesire = semver.Version{Major: 5, Minor: 1}

func suppressLegacyIngressDesire(desireName string, version semver.Version) bool {
	var versionMajorMinor = semver.Version{Major: version.Major, Minor: version.Minor}
	if versionMajorMinor.LT(minVersionForLegacyIngressDesire) {
		return false
	}

	switch desireName {
	case DesireNameManagedCluster, DesireNameDefaultIngressConfigMap,
		DesireNameDefaultIngressWildcardCertSecretSync, DesireNameDefaultIngressWildcardCertSecretProviderClass:
		return true
	default:
		return false
	}
}
