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

//go:build !release

package fpa

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// generateTestCertificate generates a self-signed certificate for testing
func generateTestCertificate(t *testing.T, serialNumber int64) (certPEM, keyPEM []byte, err error) {
	t.Helper()

	notBefore := time.Now().Add(-1 * time.Hour)
	notAfter := notBefore.Add(24 * time.Hour)

	// Generate private key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}

	// Create certificate template
	template := x509.Certificate{
		SerialNumber: big.NewInt(serialNumber),
		Subject: pkix.Name{
			Organization: []string{"Test Org"},
			CommonName:   "test-client",
		},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}

	// Create self-signed certificate
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return nil, nil, err
	}

	// Encode certificate to PEM
	certPEM = pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certDER,
	})

	// Encode private key to PEM
	keyPEM = pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	})

	return certPEM, keyPEM, nil
}

// atomicUpdateCert simulates a configmap/secret/secretproviderclass rotation using the AtomicWriter pattern.
// Kubernetes CSI SecretProviderClass and Secrets/ConfigMaps use this pattern:
// secrets are written into a new unique dir, a ..data_tmp symlink is created pointing to it,
// and then rename(..data_tmp, ..data) atomically replaces the old ..data symlink.
func atomicUpdateCert(t *testing.T, dir, filename string, serialNumber int64) {
	t.Helper()

	certPEM, keyPEM, err := generateTestCertificate(t, serialNumber)
	require.NoError(t, err)
	content := append(keyPEM, certPEM...)

	versionedDir := filepath.Join(dir, fmt.Sprintf("..%d", serialNumber))
	err = os.MkdirAll(versionedDir, 0755)
	require.NoError(t, err)

	secretPath := filepath.Join(versionedDir, filename)
	err = os.WriteFile(secretPath, content, 0644)
	require.NoError(t, err)

	dataLink := filepath.Join(dir, "..data")
	dataTmpLink := filepath.Join(dir, "..data_tmp")

	_ = os.Remove(dataTmpLink)

	err = os.Symlink(filepath.Base(versionedDir), dataTmpLink)
	require.NoError(t, err)

	err = os.Rename(dataTmpLink, dataLink)
	require.NoError(t, err)

	secretLink := filepath.Join(dir, filename)
	if _, err := os.Lstat(secretLink); os.IsNotExist(err) {
		err = os.Symlink(filepath.Join("..data", filename), secretLink)
		require.NoError(t, err)
	}
}
