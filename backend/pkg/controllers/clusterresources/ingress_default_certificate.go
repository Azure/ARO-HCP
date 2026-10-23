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

// minVersionWithHCCOIngressCertConsumer is the minimum control plane version
// whose control-plane-operator natively consumes the service-provider ingress
// default certificate synced into the HostedCluster namespace
// (https://github.com/openshift/hypershift/pull/9132, first released in 5.1.0).
var minVersionWithHCCOIngressCertConsumer = semver.Version{Major: 5, Minor: 1}

// overriddenVersionsWithHCCOIngressCertConsumer lists older major.minor
// versions for which every payload in service is covered by the CPO image
// overrides pinned in the hypershift-operator
// (controlplaneoperator-overrides/assets/overrides.yaml), which carry the
// ingress default certificate consumer.
var overriddenVersionsWithHCCOIngressCertConsumer = map[uint64]map[uint64]bool{
	4: {20: true, 21: true, 22: true},
}

// hccoIngressCertConsumerCovered reports whether the control-plane-operator
// for the given version consumes the service-provider ingress default
// certificate, either natively or via a CPO override.
func hccoIngressCertConsumerCovered(version semver.Version) bool {
	majorMinor := semver.Version{Major: version.Major, Minor: version.Minor}
	if majorMinor.GTE(minVersionWithHCCOIngressCertConsumer) {
		return true
	}
	return overriddenVersionsWithHCCOIngressCertConsumer[version.Major][version.Minor]
}

// suppressLegacyIngressDesire reports whether the legacy ACM-based ingress
// certificate wiring desire should be suppressed for the given control plane
// version. Suppression is only safe when the version's HCCO is known to
// consume the service-provider ingress certificate delivered by the
// hypershift-native path; otherwise the guest cluster is left serving the
// CPO-generated root-CA ingress certificate (AROSLSRE-2529). Versions without
// coverage - for example 4.23.0-ec.x payloads, which predate the backport of
// the consumer and have no CPO override yet - keep the legacy ACM wiring.
func suppressLegacyIngressDesire(desireName string, version *semver.Version) bool {
	if version == nil || !hccoIngressCertConsumerCovered(*version) {
		return false
	}

	return legacyACMBasedIngressCertificateWiringDesireName(desireName)
}
