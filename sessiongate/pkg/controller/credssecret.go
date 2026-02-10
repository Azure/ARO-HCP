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
	"encoding/pem"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	corev1apply "k8s.io/client-go/applyconfigurations/core/v1"
	metav1apply "k8s.io/client-go/applyconfigurations/meta/v1"

	sessiongatev1alpha1 "github.com/Azure/ARO-HCP/sessiongate/pkg/apis/sessiongate/v1alpha1"
)

const (
	// Secret data keys
	secretKeyPrivateKey  = "privateKey"
	secretKeyCertificate = "certificate"
)

type CredentialSecret struct {
	privateKey  []byte
	certificate []byte
}

func credentialSecretNameForSession(session *sessiongatev1alpha1.Session) string {
	if session.Status.CredentialsSecretRef != "" {
		return session.Status.CredentialsSecretRef
	}
	return fmt.Sprintf("sessiongate-%s", getDeterministicSuffixForSession(session.Namespace, session.Name))
}

func NewCredentialSecret(secret *corev1.Secret) *CredentialSecret {
	if secret == nil {
		return &CredentialSecret{
			privateKey:  nil,
			certificate: nil,
		}
	}
	data := secret.Data
	return &CredentialSecret{
		privateKey:  data[secretKeyPrivateKey],
		certificate: data[secretKeyCertificate],
	}
}

func (c *CredentialSecret) GetPrivateKeyBytes() ([]byte, bool) {
	if len(c.privateKey) == 0 {
		return nil, false
	}
	return c.privateKey, true
}

func (c *CredentialSecret) GetPrivateKey() (*rsa.PrivateKey, bool) {
	privateKeyBytes, exists := c.GetPrivateKeyBytes()
	if !exists {
		return nil, false
	}
	privateKey, err := decodePrivateKey(privateKeyBytes)
	if err != nil {
		return nil, false
	}
	return privateKey, true
}

func (c *CredentialSecret) GetCertificate() ([]byte, bool) {
	if len(c.certificate) == 0 {
		return nil, false
	}
	return c.certificate, true
}

func (c *CredentialSecret) ApplyConfigurationForPrivateKey(session *sessiongatev1alpha1.Session, privateKey *rsa.PrivateKey) *corev1apply.SecretApplyConfiguration {
	return c.applyConfiguration(session, encodePrivateKey(privateKey), nil)
}

func (c *CredentialSecret) ApplyConfigurationForCertificate(session *sessiongatev1alpha1.Session, certificate []byte) *corev1apply.SecretApplyConfiguration {
	return c.applyConfiguration(session, c.privateKey, certificate)
}

func (c *CredentialSecret) applyConfiguration(session *sessiongatev1alpha1.Session, privateKey, certificate []byte) *corev1apply.SecretApplyConfiguration {
	data := map[string][]byte{
		secretKeyPrivateKey:  privateKey,
		secretKeyCertificate: certificate,
	}
	return corev1apply.Secret(credentialSecretNameForSession(session), session.Namespace).
		WithLabels(map[string]string{
			LabelManagedBy: ControllerAgentName,
		}).
		WithOwnerReferences(
			metav1apply.OwnerReference().
				WithAPIVersion(sessiongatev1alpha1.SchemeGroupVersion.String()).
				WithKind("Session").
				WithName(session.Name).
				WithUID(session.UID).
				WithController(true).
				WithBlockOwnerDeletion(true),
		).
		WithType(corev1.SecretTypeOpaque).
		WithData(data)
}

func createPrivateKey(size int) (*rsa.PrivateKey, error) {
	return rsa.GenerateKey(rand.Reader, size)
}

func encodePrivateKey(privateKey *rsa.PrivateKey) []byte {
	return pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	})
}

func decodePrivateKey(privateKeyBytes []byte) (*rsa.PrivateKey, error) {
	block, rest := pem.Decode(privateKeyBytes)
	if block == nil || len(rest) > 0 {
		// we expect private keys to be a single PEM encoded block
		// this decode functions expects to be used in tandem with
		// the createPrivateKey and encodePrivateKey functions to ensure
		// this invariant is maintained
		//
		// also the ApplyConfigurationForPrivateKey function is the only valid
		// way to bring a single private key into the secret
		return nil, fmt.Errorf("private key is not a single PEM encoded block")
	}
	return x509.ParsePKCS1PrivateKey(block.Bytes)
}

func privateKeyAndPublicKeyMatch(privateKey *rsa.PrivateKey, publicKey *rsa.PublicKey) bool {
	return privateKey.N.Cmp(publicKey.N) == 0 && privateKey.E == publicKey.E
}
