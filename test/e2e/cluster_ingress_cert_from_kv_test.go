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

package e2e

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestPinnedDigiCertRoot(t *testing.T) {
	der, err := digiCertCAs.ReadFile("digicert-cas/DigiCertHighAssuranceEVRootCA.crt")
	require.NoError(t, err)
	sum := sha256.Sum256(der)
	require.Equal(t, "7431e5f4c3c1ce4690774f0b61e05440883ba9a01ed00ba6abd7806ed3b118cf", hex.EncodeToString(sum[:]),
		"root must match DigiCert's published SHA-256 fingerprint")
	root, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	trust, err := loadEmbeddedCAs(digiCertCAs, "digicert-cas")
	require.NoError(t, err)
	require.NoError(t, verifyCertChain([]*x509.Certificate{root}, trust),
		"the embedded DigiCert root must be trusted independently of the runner's CA bundle")
}

func TestIngressTLSSecretCertificateTrust(t *testing.T) {
	now := time.Now()
	issue := func(template, parent *x509.Certificate, signer ed25519.PrivateKey) (*x509.Certificate, ed25519.PrivateKey) {
		publicKey, key, err := ed25519.GenerateKey(rand.Reader)
		require.NoError(t, err)
		if parent == nil {
			parent, signer = template, key
		}
		der, err := x509.CreateCertificate(rand.Reader, template, parent, publicKey, signer)
		require.NoError(t, err)
		cert, err := x509.ParseCertificate(der)
		require.NoError(t, err)
		return cert, key
	}
	root, rootKey := issue(&x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test root"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}, nil, nil)
	intermediate, intermediateKey := issue(&x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: "test intermediate"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}, root, rootKey)
	const wildcard = "*.apps.example.com"
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(3),
		DNSNames:     []string{wildcard},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leaf, leafKey := issue(leafTemplate, intermediate, intermediateKey)
	leafTemplate.SerialNumber = big.NewInt(4)
	leafTemplate.NotAfter = now.Add(-time.Minute)
	expired, expiredKey := issue(leafTemplate, intermediate, intermediateKey)
	trust := x509.NewCertPool()
	trust.AddCert(root)

	for _, test := range []struct {
		name        string
		certificate *x509.Certificate
		key         ed25519.PrivateKey
		chain       []*x509.Certificate
		roots       *x509.CertPool
		san         string
		wrongSHA    bool
		wantErr     string
	}{
		{name: "explicit root trusts a CA absent from the system bundle", certificate: leaf, key: leafKey, chain: []*x509.Certificate{intermediate}, roots: trust, san: wildcard},
		{name: "root supplied by the Secret must not become trusted", certificate: leaf, key: leafKey, chain: []*x509.Certificate{intermediate, root}, roots: x509.NewCertPool(), san: wildcard, wantErr: "unknown authority"},
		{name: "nil roots must not fall back to the system bundle", certificate: leaf, key: leafKey, san: wildcard, wantErr: "requires an explicit trusted CA pool"},
		{name: "missing intermediate", certificate: leaf, key: leafKey, roots: trust, san: wildcard, wantErr: "unknown authority"},
		{name: "incorrect wildcard SAN", certificate: leaf, key: leafKey, chain: []*x509.Certificate{intermediate}, roots: trust, san: "*.other.example.com", wantErr: "do not include expected"},
		{name: "previous certificate version", certificate: leaf, key: leafKey, chain: []*x509.Certificate{intermediate}, roots: trust, san: wildcard, wrongSHA: true, wantErr: "leaf SHA256="},
		{name: "expired leaf", certificate: expired, key: expiredKey, chain: []*x509.Certificate{intermediate}, roots: trust, san: wildcard, wantErr: "expired"},
		{name: "mismatched private key", certificate: leaf, key: expiredKey, chain: []*x509.Certificate{intermediate}, roots: trust, san: wildcard, wantErr: "private key does not match public key"},
	} {
		t.Run(test.name, func(t *testing.T) {
			certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: test.certificate.Raw})
			for _, cert := range test.chain {
				certPEM = append(certPEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})...)
			}
			keyDER, err := x509.MarshalPKCS8PrivateKey(test.key)
			require.NoError(t, err)
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Namespace: "openshift-ingress", Name: "ingress-tls"},
				Type:       corev1.SecretTypeTLS,
				Data: map[string][]byte{
					corev1.TLSCertKey:       certPEM,
					corev1.TLSPrivateKeyKey: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}),
				},
			}
			sum := sha256.Sum256(test.certificate.Raw)
			want := hex.EncodeToString(sum[:])
			if test.wrongSHA {
				want = "another-certificate-version"
			}
			got, err := expectTLSSecretCertificate(t.Context(), fake.NewSimpleClientset(secret), secret.Namespace, secret.Name, want, test.san, test.roots)
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
				require.Nil(t, got, "an invalid certificate must not be used for the route verification")
				return
			}
			require.NoError(t, err)
			require.True(t, got.Equal(test.certificate), "must return the exact Key Vault certificate")
		})
	}
}
