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

import (
	"github.com/blang/semver/v4"
)

func ingressDefaultCertSupported(rawVersion string) bool {
	parsed, err := semver.Parse(rawVersion)
	if err != nil {
		return false
	}
	parsed.Pre = nil
	if parsed.GTE(semver.Version{Major: 5, Minor: 1}) {
		return true
	}
	// These per-release floors select payloads with HyperShift's ingress certificate
	// reconciliation: 4.20.43 (https://github.com/openshift/hypershift/pull/9780),
	// 4.21.38 (https://github.com/openshift/hypershift/pull/9744), and
	// 4.22.19 (https://github.com/openshift/hypershift/pull/9607).
	// The feature is included from the start of the 4.23 and 5.0 lines
	// (5.0 backport: https://github.com/openshift/hypershift/pull/9604),
	// and in 5.1+ via https://github.com/openshift/hypershift/pull/9132.
	thresholds := []semver.Version{
		{Major: 4, Minor: 20, Patch: 43},
		{Major: 4, Minor: 21, Patch: 38},
		{Major: 4, Minor: 22, Patch: 19},
		{Major: 4, Minor: 23},
		{Major: 5, Minor: 0},
	}
	for _, threshold := range thresholds {
		if parsed.Major == threshold.Major && parsed.Minor == threshold.Minor {
			return parsed.GTE(threshold)
		}
	}
	return false
}

func suppressLegacyIngressDesire(desireName string, suppress bool) bool {
	if !suppress {
		return false
	}
	switch desireName {
	case "ManagedCluster", "DefaultIngressConfigMap",
		"DefaultIngressWildcardCertSecretSync", "DefaultIngressWildcardCertSecretProviderClass":
		return true
	default:
		return false
	}
}
