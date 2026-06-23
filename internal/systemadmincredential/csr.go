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
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"

	certificatesv1 "k8s.io/api/certificates/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

const (
	// defaultExpirationSeconds is ~24 hours, matching cluster-service's
	// credential TTL.
	defaultExpirationSeconds = int32(86400)

	// signerNameTemplate is the HyperShift signer name format for
	// customer-break-glass certificates.
	signerNameTemplate = "hypershift.openshift.io/%s.customer-break-glass"
)

// CSRName returns the Kubernetes CSR object name for a credential.
func CSRName(credName string) string {
	return fmt.Sprintf("system-admin-credential-%s", credName)
}

// BuildCSR constructs a CertificateSigningRequest object for a
// system-admin credential. The PKCS#10 request is signed with the
// private key; the CSR carries CN=username and targets the HyperShift
// customer-break-glass signer in the given HCP namespace.
//
// The returned object is intended for use in an ApplyDesire's
// KubeContent. It carries the owner annotation on its metadata.
func BuildCSR(owner *azcorearm.ResourceID, credName, hcpNamespace, username string, privateKeyPEM []byte) (*certificatesv1.CertificateSigningRequest, error) {
	requireOwner(owner)

	csrPEM, err := generateCSRPEM(username, privateKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("failed to generate CSR PEM: %w", err)
	}

	expirationSeconds := defaultExpirationSeconds

	return &certificatesv1.CertificateSigningRequest{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "certificates.k8s.io/v1",
			Kind:       "CertificateSigningRequest",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        CSRName(credName),
			Annotations: ownerAnnotation(owner),
		},
		Spec: certificatesv1.CertificateSigningRequestSpec{
			Request:           csrPEM,
			SignerName:        fmt.Sprintf(signerNameTemplate, hcpNamespace),
			ExpirationSeconds: &expirationSeconds,
			Usages: []certificatesv1.KeyUsage{
				certificatesv1.UsageClientAuth,
			},
		},
	}, nil
}

// generateCSRPEM creates a PEM-encoded PKCS#10 CSR signed with the
// given private key.
func generateCSRPEM(username string, privateKeyPEM []byte) ([]byte, error) {
	block, _ := pem.Decode(privateKeyPEM)
	if block == nil {
		return nil, fmt.Errorf("failed to decode private key PEM")
	}

	privKey, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}

	template := &x509.CertificateRequest{
		Subject: pkix.Name{
			CommonName: username,
		},
	}

	csrDER, err := x509.CreateCertificateRequest(rand.Reader, template, privKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create certificate request: %w", err)
	}

	return pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE REQUEST",
		Bytes: csrDER,
	}), nil
}
