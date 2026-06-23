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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	certificatesv1alpha1 "github.com/openshift/hypershift/api/certificates/v1alpha1"
)

// CRRNamePrefix is the on-MC `metadata.name` prefix for the per-revoke
// CertificateRevocationRequest object. The full name is
// `<CRRNamePrefix>-<revokeOpSuffix>` where the suffix is the first 16
// hex chars of the revoke Operation's ID — see PLAN.md.
const CRRNamePrefix = "system-admin-credential-crr"

// CustomerBreakGlassSignerClass is the HyperShift signer class whose
// previously-issued certificates the CRR revokes. It is HyperShift's
// contract string; renaming requires a HyperShift change.
const CustomerBreakGlassSignerClass = "customer-break-glass"

// BuildRevocationRequest returns a HyperShift CertificateRevocationRequest
// k8s object ready to be served by an ApplyDesire. Revoking
// signerClass=customer-break-glass invalidates every cert HyperShift's
// customer-break-glass signer has issued for this cluster up to the
// point the CRR lands.
//
// owner is required and is written to metadata.annotations.
// revokeOpSuffix is the per-revoke uniqueness suffix; pass the first 16
// chars of the revoke Operation's OperationID.Name (with dashes
// stripped).
func BuildRevocationRequest(
	owner *azcorearm.ResourceID,
	revokeOpSuffix string,
	namespace string,
) (*certificatesv1alpha1.CertificateRevocationRequest, error) {
	if revokeOpSuffix == "" {
		return nil, fmt.Errorf("revokeOpSuffix must not be empty")
	}
	if namespace == "" {
		return nil, fmt.Errorf("namespace must not be empty")
	}

	crr := &certificatesv1alpha1.CertificateRevocationRequest{
		TypeMeta: metav1.TypeMeta{
			APIVersion: certificatesv1alpha1.SchemeGroupVersion.String(),
			Kind:       "CertificateRevocationRequest",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      CRRNamePrefix + "-" + revokeOpSuffix,
			Namespace: namespace,
		},
		Spec: certificatesv1alpha1.CertificateRevocationRequestSpec{
			SignerClass: CustomerBreakGlassSignerClass,
		},
	}
	setOwnerAnnotation(&crr.ObjectMeta, owner)

	return crr, nil
}
