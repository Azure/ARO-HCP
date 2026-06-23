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

// BuildCSRA constructs a CertificateSigningRequestApproval object.
// The approval's name must match the CSR name (same credName) and
// is placed in the HCP namespace. It carries the owner annotation.
func BuildCSRA(owner *azcorearm.ResourceID, credName, hcpNamespace string) *certificatesv1alpha1.CertificateSigningRequestApproval {
	requireOwner(owner)

	return &certificatesv1alpha1.CertificateSigningRequestApproval{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "certificates.hypershift.openshift.io/v1alpha1",
			Kind:       "CertificateSigningRequestApproval",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        fmt.Sprintf("system-admin-credential-%s", credName),
			Namespace:   hcpNamespace,
			Annotations: ownerAnnotation(owner),
		},
	}
}
