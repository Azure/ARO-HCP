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

package systemadmincredential

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	certificatesv1alpha1 "github.com/openshift/hypershift/api/certificates/v1alpha1"
)

const (
	// customerBreakGlassSignerClass is the HyperShift CRR signer class
	// that revokes all customer-break-glass certificates for a cluster.
	customerBreakGlassSignerClass = "customer-break-glass"
)

// BuildRevocationRequest constructs a CertificateRevocationRequest
// object for revoking all customer-break-glass certificates. The CRR
// name carries the revoke operation's 16-char suffix and is placed in
// the HCP namespace. It carries the owner annotation.
func BuildRevocationRequest(owner *azcorearm.ResourceID, revokeOpSuffix, hcpNamespace string) *certificatesv1alpha1.CertificateRevocationRequest {
	requireOwner(owner)

	return &certificatesv1alpha1.CertificateRevocationRequest{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "certificates.hypershift.openshift.io/v1alpha1",
			Kind:       "CertificateRevocationRequest",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        fmt.Sprintf("system-admin-credential-revocation-%s", revokeOpSuffix),
			Namespace:   hcpNamespace,
			Annotations: ownerAnnotation(owner),
		},
		Spec: certificatesv1alpha1.CertificateRevocationRequestSpec{
			SignerClass: customerBreakGlassSignerClass,
		},
	}
}
