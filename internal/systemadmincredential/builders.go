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

package systemadmincredential

import (
	"fmt"
	"strings"

	certificatesv1 "k8s.io/api/certificates/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	certificatesv1alpha1 "github.com/openshift/hypershift/api/certificates/v1alpha1"
)

// KubeObject is the subset of a typed Kubernetes object that our builders
// produce and our controllers serialize. It combines object metadata access
// (metav1.Object) with runtime.Object so callers can read name/namespace/GVK and
// JSON-marshal the object without depending on sigs.k8s.io/controller-runtime.
type KubeObject interface {
	metav1.Object
	runtime.Object
}

const (
	// ownerAnnotationKey is the annotation applied to every k8s object we land
	// on a management cluster via ApplyDesire.
	ownerAnnotationKey = "aro-hcp.openshift.io/owner"

	// defaultExpirationSeconds is the CSR expiration (approximately 24 hours).
	defaultExpirationSeconds = int32(86400)

	// customerBreakGlassSignerSuffix is the HyperShift signer name suffix.
	// Defined by HyperShift in pkg/controllers/certificates/signers.go
	// (SignerNameForHC builds "<namespace>.<suffix>").
	customerBreakGlassSignerSuffix = "customer-break-glass"

	// customerBreakGlassRevocationSignerClass is the signer class for CRR.
	customerBreakGlassRevocationSignerClass = "customer-break-glass"
)

// requireOwner panics if owner is nil. All Build* helpers call this to guarantee
// the owner annotation is never omitted.
func requireOwner(owner *azcorearm.ResourceID) {
	if owner == nil {
		panic("systemadmincredential: owner resource ID must not be nil")
	}
}

// ownerAnnotation returns the standard owner annotation map.
func ownerAnnotation(owner *azcorearm.ResourceID) map[string]string {
	return map[string]string{
		ownerAnnotationKey: strings.ToLower(owner.String()),
	}
}

// BuildCSR builds a CertificateSigningRequest for a system admin credential.
// The hcpNamespace is the HyperShift HCP namespace on the management cluster
// (e.g. "ocm-<env>-<csClusterID>").
// csrPEM is the PEM-encoded PKCS#10 certificate request provided by the caller.
func BuildCSR(owner *azcorearm.ResourceID, credName, hcpNamespace string, csrPEM []byte) *certificatesv1.CertificateSigningRequest {
	requireOwner(owner)

	signerName := fmt.Sprintf("hypershift.openshift.io/%s.%s", hcpNamespace, customerBreakGlassSignerSuffix)

	return &certificatesv1.CertificateSigningRequest{
		TypeMeta: metav1.TypeMeta{
			APIVersion: certificatesv1.SchemeGroupVersion.String(),
			Kind:       "CertificateSigningRequest",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        fmt.Sprintf("system-admin-credential-%s", credName),
			Annotations: ownerAnnotation(owner),
		},
		Spec: certificatesv1.CertificateSigningRequestSpec{
			Request:           csrPEM,
			SignerName:        signerName,
			ExpirationSeconds: func() *int32 { v := defaultExpirationSeconds; return &v }(),
			Usages: []certificatesv1.KeyUsage{
				certificatesv1.UsageClientAuth,
				certificatesv1.UsageDigitalSignature,
			},
		},
	}
}

// BuildCSRApproval builds a CertificateSigningRequestApproval for a system admin credential.
func BuildCSRApproval(owner *azcorearm.ResourceID, credName, hcpNamespace string) *certificatesv1alpha1.CertificateSigningRequestApproval {
	requireOwner(owner)
	return &certificatesv1alpha1.CertificateSigningRequestApproval{
		TypeMeta: metav1.TypeMeta{
			APIVersion: certificatesv1alpha1.SchemeGroupVersion.String(),
			Kind:       "CertificateSigningRequestApproval",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        fmt.Sprintf("system-admin-credential-%s", credName),
			Namespace:   hcpNamespace,
			Annotations: ownerAnnotation(owner),
		},
	}
}

// BuildRevocationRequest builds a CertificateRevocationRequest that revokes all
// customer-break-glass certificates for the cluster.
func BuildRevocationRequest(owner *azcorearm.ResourceID, revokeOpSuffix, hcpNamespace string) *certificatesv1alpha1.CertificateRevocationRequest {
	requireOwner(owner)
	return &certificatesv1alpha1.CertificateRevocationRequest{
		TypeMeta: metav1.TypeMeta{
			APIVersion: certificatesv1alpha1.SchemeGroupVersion.String(),
			Kind:       "CertificateRevocationRequest",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        fmt.Sprintf("system-admin-credential-revocation-%s", revokeOpSuffix),
			Namespace:   hcpNamespace,
			Annotations: ownerAnnotation(owner),
		},
		Spec: certificatesv1alpha1.CertificateRevocationRequestSpec{
			SignerClass: customerBreakGlassRevocationSignerClass,
		},
	}
}
