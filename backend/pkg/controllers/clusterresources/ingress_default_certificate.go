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
	"encoding/json"
	"fmt"

	semver "github.com/blang/semver/v4"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
)

const (
	// defaultIngressConfigCertificateNameKey is the key on the per-cluster
	// default ingress ConfigMap that the ACM default-ingress-config policy
	// (acm/deploy/helm/policies/templates/default-ingress-cert.policy.yaml)
	// reads to pick the guest IngressController default certificate.
	defaultIngressConfigCertificateNameKey = "default-certificate-name"
	// acmIngressCertificateSecretName is the guest openshift-ingress Secret
	// that the ACM policy fills with the service-provider certificate.
	acmIngressCertificateSecretName = "cluster-ingress-cert"
)

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

// selectACMIngressCertificate reports whether the guest IngressController
// must use the certificate the ACM policy delivers in cluster-ingress-cert.
// Versions without HCCO coverage - for example 4.23.0-ec.x payloads, which
// predate the backport of the consumer and have no CPO override yet - fill
// default-ingress-cert with a CPO-generated root-CA certificate
// (AROSLSRE-2529), so they select the ACM certificate.
//
// The selection is sticky. HCCO never rewrites an existing IngressController,
// and the backend cannot observe the guest cluster, so moving a guest back to
// default-ingress-cert after an upgrade has no safe handoff: dropping the
// legacy wiring would strand the guest on a certificate that is no longer
// renewed. A missing version leaves the selection unchanged.
func selectACMIngressCertificate(version *semver.Version, alreadySelected bool) bool {
	if alreadySelected {
		return true
	}
	return version != nil && !hccoIngressCertConsumerCovered(*version)
}

// suppressLegacyIngressDesire reports whether the legacy ACM-based ingress
// certificate wiring desire should be suppressed. Suppression is only safe
// when the version's HCCO is known to consume the service-provider ingress
// certificate delivered by the hypershift-native path and the guest has never
// been pointed at the ACM-delivered certificate.
func suppressLegacyIngressDesire(desireName string, version *semver.Version, acmCertificateSelected bool) bool {
	if acmCertificateSelected || version == nil || !hccoIngressCertConsumerCovered(*version) {
		return false
	}

	return legacyACMBasedIngressCertificateWiringDesireName(desireName)
}

// defaultIngressConfigSelectsACMCertificate reports whether the applied
// default ingress ConfigMap desire already points the ACM policy at the
// ACM-delivered certificate.
func defaultIngressConfigSelectsACMCertificate(desire *kubeapplierapi.ApplyDesire) (bool, error) {
	if desire == nil || desire.Spec.Type != kubeapplierapi.ApplyDesireTypeServerSideApply ||
		desire.Spec.ServerSideApply == nil || desire.Spec.ServerSideApply.KubeContent == nil {
		return false, nil
	}
	var configMap unstructured.Unstructured
	if err := json.Unmarshal(desire.Spec.ServerSideApply.KubeContent.Raw, &configMap); err != nil {
		return false, fmt.Errorf("decoding default ingress ConfigMap desire: %w", err)
	}
	name, _, err := unstructured.NestedString(configMap.Object, "data", defaultIngressConfigCertificateNameKey)
	if err != nil {
		return false, fmt.Errorf("reading data.%s: %w", defaultIngressConfigCertificateNameKey, err)
	}
	return name == acmIngressCertificateSecretName, nil
}

// ensureDefaultIngressConfigCertificateName points the ACM policy at the
// Secret that carries the service-provider certificate. Without the key the
// policy keeps the guest IngressController on default-ingress-cert, which the
// HCCO of covered versions fills with the service-provider certificate.
func ensureDefaultIngressConfigCertificateName(configMap *unstructured.Unstructured, acmCertificateSelected bool) error {
	if !acmCertificateSelected {
		unstructured.RemoveNestedField(configMap.Object, "data", defaultIngressConfigCertificateNameKey)
		return nil
	}
	if err := unstructured.SetNestedField(configMap.Object, acmIngressCertificateSecretName, "data", defaultIngressConfigCertificateNameKey); err != nil {
		return fmt.Errorf("setting data.%s: %w", defaultIngressConfigCertificateNameKey, err)
	}
	return nil
}
