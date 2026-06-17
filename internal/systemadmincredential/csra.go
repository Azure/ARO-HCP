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

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	certificatesv1alpha1 "github.com/openshift/hypershift/api/certificates/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CSRANamePrefix is the on-MC `metadata.name` prefix for per-credential
// CertificateSigningRequestApproval objects. The full name is
// `<CSRANamePrefix>-<credName>` so it aligns with the matching CSR name
// only by suffix, not by full string — the CSRA's name does not have to
// equal the CSR's name; HyperShift's control-plane-pki-operator pairs
// them by labels and namespace.
const CSRANamePrefix = "system-admin-credential-csra"

// BuildCSRA returns a HyperShift CertificateSigningRequestApproval k8s
// object ready to be served by an ApplyDesire. The mere presence of the
// CSRA in the cluster's HCP namespace tells the HyperShift
// control-plane-pki-operator that the matching CSR is permitted to be
// signed.
//
// owner is required and is written to metadata.annotations.
func BuildCSRA(
	owner *azcorearm.ResourceID,
	credName string,
	namespace string,
) (*certificatesv1alpha1.CertificateSigningRequestApproval, error) {
	if credName == "" {
		return nil, fmt.Errorf("credName must not be empty")
	}
	if namespace == "" {
		return nil, fmt.Errorf("namespace must not be empty")
	}

	csra := &certificatesv1alpha1.CertificateSigningRequestApproval{
		TypeMeta: metav1.TypeMeta{
			APIVersion: certificatesv1alpha1.SchemeGroupVersion.String(),
			Kind:       "CertificateSigningRequestApproval",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      CSRANamePrefix + "-" + credName,
			Namespace: namespace,
			Labels: map[string]string{
				"aro-hcp.openshift.io/credential-name": credName,
			},
		},
	}
	setOwnerAnnotation(&csra.ObjectMeta, owner)

	return csra, nil
}
