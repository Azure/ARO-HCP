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

package controller

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"

	certificatesv1 "k8s.io/api/certificates/v1"
	certapplyv1 "k8s.io/client-go/applyconfigurations/certificates/v1"
	"k8s.io/klog/v2"

	sessiongatev1alpha1 "github.com/Azure/ARO-HCP/sessiongate/pkg/apis/sessiongate/v1alpha1"
)

const (
	// CSRSignerNameTemplate must match the hypershift CSR approver.
	// See: https://github.com/openshift/hypershift/blob/main/control-plane-operator/controllers/hostedcontrolplane/csr/reconcile.go
	CSRSignerNameTemplate = "hypershift.openshift.io/%s.sre-break-glass"
)

func isCSRApproved(csr *certificatesv1.CertificateSigningRequest) bool {
	for _, condition := range csr.Status.Conditions {
		if condition.Type == "Approved" && condition.Status == "True" {
			return true
		}
	}
	return false
}

func createCSRRequestBody(session *sessiongatev1alpha1.Session, privateKey *rsa.PrivateKey) ([]byte, error) {
	subject := pkix.Name{
		CommonName:   CSRCommonName(session.Spec.Owner.Name),
		Organization: []string{session.Spec.AccessLevel.Group},
	}
	template := x509.CertificateRequest{
		Subject:            subject,
		SignatureAlgorithm: x509.SHA256WithRSA,
	}

	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &template, privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create certificate request: %w", err)
	}

	// Encode to PEM
	csrPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE REQUEST",
		Bytes: csrDER,
	})
	return csrPEM, nil
}

func createCSRApplyConfiguration(session *sessiongatev1alpha1.Session, privateKey *rsa.PrivateKey) (*certapplyv1.CertificateSigningRequestApplyConfiguration, error) {
	csrPEM, err := createCSRRequestBody(session, privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create CSR request body: %w", err)
	}
	// Kubernetes requires CSR expirationSeconds to be at least MinCSRExpirationSeconds.
	// We use max(TTL, MinCSRExpirationSeconds) here because session access is controlled
	// by session expiration, not certificate expiration. Once a session expires, the
	// authorization policy is removed and access to the HCP is blocked regardless of
	// certificate validity.
	csrExpirationSeconds := max(int32(session.Spec.TTL.Seconds()), MinCSRExpirationSeconds)

	return certapplyv1.CertificateSigningRequest(getCSRNameForSession(session)).
		WithLabels(map[string]string{
			LabelManagedBy: ControllerAgentName,
		}).
		WithAnnotations(map[string]string{
			AnnotationSessiongate: fmt.Sprintf("%s/%s", session.Namespace, session.Name),
		}).
		WithSpec(certapplyv1.CertificateSigningRequestSpec().
			WithRequest(csrPEM...).
			WithSignerName(CSRSignerName(session.Spec.HostedControlPlane.Namespace)).
			WithExpirationSeconds(csrExpirationSeconds).
			WithUsages(
				certificatesv1.UsageClientAuth,
				certificatesv1.UsageDigitalSignature,
			)), nil
}

func CSRCommonName(user string) string {
	return fmt.Sprintf("system:sre-break-glass:%s", user)
}

func getCSRNameForSession(session *sessiongatev1alpha1.Session) string {
	return fmt.Sprintf("sessiongate-%s", getDeterministicSuffixForSession(session.Namespace, session.Name))
}

// CSRSignerName returns the signer name for break-glass CSRs in the given HCP namespace.
func CSRSignerName(hcpNamespace string) string {
	return fmt.Sprintf(CSRSignerNameTemplate, hcpNamespace)
}

// validateCSR checks if an existing CSR matches the expected private key and session details.
// Prevents accepting a CSR created by a different controller or with mismatched credentials,
// which could lead to security issues or credential misuse.
func validateCSR(csr *certificatesv1.CertificateSigningRequest, privateKey *rsa.PrivateKey, user, organization string) bool {
	if csr == nil || len(csr.Spec.Request) == 0 {
		klog.ErrorS(nil, "CSR is nil or has no request", "csr", csr.Name)
		return false
	}

	// Parse the PEM-encoded CSR
	block, _ := pem.Decode(csr.Spec.Request)
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		klog.ErrorS(nil, "CSR is not a valid PEM-encoded certificate request", "csr", csr.Name)
		return false
	}

	// Parse the certificate request
	parsedCSR, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		klog.ErrorS(err, "Failed to parse certificate request", "csr", csr.Name)
		return false
	}

	// Verify the CSR signature hasn't been tampered with
	if err := parsedCSR.CheckSignature(); err != nil {
		klog.ErrorS(err, "CSR signature verification failed", "csr", csr.Name)
		return false
	}

	// Verify the public key matches our private key
	csrPublicKey, ok := parsedCSR.PublicKey.(*rsa.PublicKey)
	if !ok {
		klog.ErrorS(nil, "CSR public key is not an RSA public key", "csr", csr.Name)
		return false
	}
	if !privateKeyAndPublicKeyMatch(privateKey, csrPublicKey) {
		klog.ErrorS(nil, "CSR public key does not match private key", "csr", csr.Name)
		return false
	}

	// Verify the subject fields using common function
	expectedCN := CSRCommonName(user)
	if parsedCSR.Subject.CommonName != expectedCN {
		klog.ErrorS(nil, "CSR common name does not match expected", "csr", csr.Name, "expected", expectedCN, "actual", parsedCSR.Subject.CommonName)
		return false
	}

	if len(parsedCSR.Subject.Organization) != 1 || parsedCSR.Subject.Organization[0] != organization {
		klog.ErrorS(nil, "CSR organization does not match expected", "csr", csr.Name, "expected", organization, "actual", parsedCSR.Subject.Organization)
		return false
	}

	return true
}
