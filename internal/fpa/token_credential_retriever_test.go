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

package fpa

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

// atomicUpdateFile simulates a configmap/secret/secretproviderclass rotation using the AtomicWriter pattern.
// Kubernetes CSI SecretProviderClass and Secrets/ConfigMaps use this pattern:
// secrets are written into a new timestamped dir, a ..data_tmp symlink is created pointing to it,
// and then rename(..data_tmp, ..data) atomically replaces the old ..data symlink.
func atomicUpdateCert(t *testing.T, dir, filename string, notBefore time.Time) {
	t.Helper()

	certPEM, keyPEM, err := generateTestCertificate(t, notBefore)
	require.NoError(t, err)
	content := string(append(keyPEM, certPEM...))

	versionedDir := filepath.Join(dir, fmt.Sprintf("..%s", notBefore.Format(time.RFC3339)))
	err = os.MkdirAll(versionedDir, 0755)
	require.NoError(t, err)

	secretPath := filepath.Join(versionedDir, filename)
	err = os.WriteFile(secretPath, []byte(content), 0644)
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

// generateTestCertificate generates a self-signed certificate for testing
func generateTestCertificate(t *testing.T, notBefore time.Time) (certPEM, keyPEM []byte, err error) {
	t.Helper()

	notAfter := notBefore.Add(23 * time.Hour)

	// Generate private key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}

	// Create certificate template
	template := x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), // Unique serial for each cert
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

func TestReloadingCredentialLoadsInitialCertificate(t *testing.T) {
	dir := t.TempDir()
	bundleFileName := "bundle"
	bundlePath := filepath.Join(dir, bundleFileName)

	certNotBefore := time.Now().Add(-1 * time.Hour)
	atomicUpdateCert(t, dir, bundleFileName, certNotBefore)

	cred, err := NewFirstPartyApplicationTokenCredentialRetriever(
		t.Context(),
		slog.New(slog.NewTextHandler(os.Stdout, nil)),
		"11111111-1111-1111-1111-111111111111",
		bundlePath,
		azcore.ClientOptions{},
		1*time.Hour,
	)
	require.NoError(t, err)

	// Verify certificate was loaded
	assert.Equal(t, certNotBefore.UTC().Truncate(time.Second), cred.NotBefore().UTC().Truncate(time.Second))
}

func TestReloadingCredentialRotation(t *testing.T) {
	mountDir := t.TempDir()

	now := time.Now()
	cert1NotBefore := now.Add(-1 * time.Hour)
	cert2NotBefore := now // Newer certificate

	// Create initial certificate
	atomicUpdateCert(t, mountDir, "bundle", cert1NotBefore)

	bundlePath := filepath.Join(mountDir, "bundle")

	cred, err := NewFirstPartyApplicationTokenCredentialRetriever(
		t.Context(),
		slog.New(slog.NewTextHandler(os.Stdout, nil)),
		"11111111-1111-1111-1111-111111111111",
		bundlePath,
		azcore.ClientOptions{},
		50*time.Millisecond,
	)
	require.NoError(t, err)

	// Verify initial certificate
	initialNotBefore := cred.NotBefore()
	assert.Equal(t, cert1NotBefore.UTC().Truncate(time.Second), initialNotBefore.UTC().Truncate(time.Second))

	// Create second certificate (newer)
	atomicUpdateCert(t, mountDir, "bundle", cert2NotBefore)

	// Wait for rotation to be detected and certificate to be reloaded
	assert.Eventually(t, func() bool {
		return cred.NotBefore().UTC().Truncate(time.Second).Equal(cert2NotBefore.UTC().Truncate(time.Second))
	}, 5*time.Second, 100*time.Millisecond, "certificate should be reloaded after CSI rotation")

	// Verify new certificate is loaded
	finalNotBefore := cred.NotBefore()

	assert.True(t, finalNotBefore.After(initialNotBefore), "new certificate should be newer")
}
