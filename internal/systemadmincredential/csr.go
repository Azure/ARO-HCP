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
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	certificatesv1 "k8s.io/api/certificates/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CSRExpirationSeconds is the lifetime the dispatcher requests for every
// SystemAdminCredential cert. ~24 hours, matching cluster-service.
const CSRExpirationSeconds int32 = 86353

// CSRNamePrefix is the on-MC `metadata.name` prefix for per-credential
// CertificateSigningRequest objects. The full name is
// `<CSRNamePrefix>-<credName>`.
const CSRNamePrefix = "system-admin-credential-csr"

// BuildCSR returns a CertificateSigningRequest k8s object ready to be
// served by an ApplyDesire. The signer name and namespace identify the
// HyperShift control-plane-pki-operator that will sign the request.
//
// The function generates the PEM-encoded CSR request payload by signing
// a fresh x509.CertificateRequest with the credential's private key. The
// caller's private-key PEM must have been produced by GenerateKeypair in
// this same package.
//
// owner is required and is written to metadata.annotations under
// OwnerAnnotationKey — see PLAN.md's "Owner annotation" section.
func BuildCSR(
	owner *azcorearm.ResourceID,
	credName string,
	signerName string,
	namespace string,
	username string,
	privateKeyPEM []byte,
) (*certificatesv1.CertificateSigningRequest, error) {
	if credName == "" {
		return nil, fmt.Errorf("credName must not be empty")
	}
	if signerName == "" {
		return nil, fmt.Errorf("signerName must not be empty")
	}
	if username == "" {
		return nil, fmt.Errorf("username must not be empty")
	}

	key, err := parseRSAPrivateKey(privateKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("BuildCSR: %w", err)
	}

	template := &x509.CertificateRequest{
		Subject: pkix.Name{
			CommonName: username,
		},
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, template, key)
	if err != nil {
		return nil, fmt.Errorf("BuildCSR: signing CertificateRequest: %w", err)
	}
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})

	expirationSeconds := CSRExpirationSeconds
	csr := &certificatesv1.CertificateSigningRequest{
		TypeMeta: metav1.TypeMeta{
			APIVersion: certificatesv1.SchemeGroupVersion.String(),
			Kind:       "CertificateSigningRequest",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: CSRNamePrefix + "-" + credName,
			// CSRs are cluster-scoped; the namespace argument is unused on the
			// k8s object but is still meaningful for the signer (see CSRA
			// below, which IS namespaced). Keep the argument symmetric with
			// BuildCSRA so callers cannot pass mismatched values.
			Labels: map[string]string{
				"aro-hcp.openshift.io/credential-name": credName,
			},
		},
		Spec: certificatesv1.CertificateSigningRequestSpec{
			Request:           csrPEM,
			SignerName:        signerName,
			ExpirationSeconds: &expirationSeconds,
			Usages: []certificatesv1.KeyUsage{
				certificatesv1.UsageClientAuth,
				certificatesv1.UsageDigitalSignature,
			},
		},
	}
	setOwnerAnnotation(&csr.ObjectMeta, owner)
	_ = namespace // intentionally unused: see comment above

	return csr, nil
}
