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
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	corev1apply "k8s.io/client-go/applyconfigurations/core/v1"
	metav1apply "k8s.io/client-go/applyconfigurations/meta/v1"

	sessiongatev1alpha1 "github.com/Azure/ARO-HCP/sessiongate/pkg/apis/sessiongate/v1alpha1"
)

const (
	// Secret data keys
	secretKeyPrivateKey  = "privateKey"
	secretKeyCertificate = "certificate"
	labelManagedBy       = "app.kubernetes.io/managed-by"
)

type CredentialSecret struct {
	fieldManager     string
	sessionName      string
	sessionNamespace string
	sessionUID       types.UID
	data             map[string][]byte
}

func NewCredentialSecret(sessionName string, sessionNamespace string, sessionUID types.UID, fieldManager string, data map[string][]byte) *CredentialSecret {
	return &CredentialSecret{
		fieldManager:     fieldManager,
		sessionName:      sessionName,
		sessionNamespace: sessionNamespace,
		sessionUID:       sessionUID,
		data:             data,
	}
}

func (c *CredentialSecret) GetPrivateKeyBytes() ([]byte, bool) {
	privateKeyBytes, exists := c.data[secretKeyPrivateKey]
	if !exists || len(privateKeyBytes) == 0 {
		return nil, false
	}
	return privateKeyBytes, true
}

func (c *CredentialSecret) GetPrivateKey() (*rsa.PrivateKey, bool) {
	privateKeyBytes, exists := c.GetPrivateKeyBytes()
	if !exists || len(privateKeyBytes) == 0 {
		return nil, false
	}
	// Decode PEM
	block, _ := pem.Decode(privateKeyBytes)
	if block == nil {
		return nil, false
	}
	privateKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, false
	}
	return privateKey, true
}

func (c *CredentialSecret) GetCertificate() ([]byte, bool) {
	certificateBytes, exists := c.data[secretKeyCertificate]
	if !exists {
		return nil, false
	}
	if len(certificateBytes) == 0 {
		return nil, false
	}
	return certificateBytes, true
}

// ValidateCertificateMatchesPrivateKey checks if the stored certificate's public key
// matches the stored private key. Returns true if they match, false otherwise.
func (c *CredentialSecret) ValidateCertificateMatchesPrivateKey() bool {
	// Get the private key
	privateKey, privateKeyExists := c.GetPrivateKey()
	if !privateKeyExists {
		return false
	}

	// Get the certificate
	certificateBytes, certificateExists := c.GetCertificate()
	if !certificateExists {
		return false
	}

	// Parse the PEM-encoded certificate
	block, _ := pem.Decode(certificateBytes)
	if block == nil || block.Type != "CERTIFICATE" {
		return false
	}

	// Parse the certificate
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false
	}

	// Verify the public key in the certificate matches our private key
	certPublicKey, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok {
		return false
	}

	// Compare the public keys
	if !PrivateKeyAndPublicKeyMatch(privateKey, certPublicKey) {
		return false
	}
	return true
}

func (c *CredentialSecret) ApplyConfigurationForPrivateKey(privateKey *rsa.PrivateKey) *corev1apply.SecretApplyConfiguration {
	return c.applyConfigurationForFields(map[string][]byte{
		secretKeyPrivateKey:  EncodePrivateKey(privateKey),
		secretKeyCertificate: nil,
	})
}

func (c *CredentialSecret) ApplyConfigurationForCertificate(certificate []byte) *corev1apply.SecretApplyConfiguration {
	return c.applyConfigurationForFields(map[string][]byte{
		secretKeyCertificate: certificate,
	})
}

func (c *CredentialSecret) applyConfigurationForFields(fields map[string][]byte) *corev1apply.SecretApplyConfiguration {
	// make a copy of the data
	dataCopy := make(map[string][]byte)
	for k, v := range c.data {
		dataCopy[k] = v
	}
	for k, v := range fields {
		dataCopy[k] = v
	}
	return corev1apply.Secret(c.sessionName, c.sessionNamespace).
		WithLabels(map[string]string{
			labelManagedBy: c.fieldManager,
		}).
		WithOwnerReferences(
			metav1apply.OwnerReference().
				WithAPIVersion(sessiongatev1alpha1.SchemeGroupVersion.String()).
				WithKind("Session").
				WithName(c.sessionName).
				WithUID(c.sessionUID).
				WithController(true).
				WithBlockOwnerDeletion(true),
		).
		WithType(corev1.SecretTypeOpaque).
		WithData(dataCopy)
}

func EncodePrivateKey(privateKey *rsa.PrivateKey) []byte {
	return pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	})
}

func PrivateKeyAndPublicKeyMatch(privateKey *rsa.PrivateKey, publicKey *rsa.PublicKey) bool {
	return privateKey.PublicKey.N.Cmp(publicKey.N) == 0 && privateKey.PublicKey.E == publicKey.E
}
