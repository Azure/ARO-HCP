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

package certs

import (
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/rand"
	"os"
	"testing"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/internal/testutil"
)

// deterministicReader provides deterministic randomness for testing
type deterministicReader struct {
	rng *rand.Rand
}

// newDeterministicReader creates a new deterministic reader with the given seed
func newDeterministicReader(seed int64) io.Reader {
	return &deterministicReader{
		rng: rand.New(rand.NewSource(seed)),
	}
}

// Read implements io.Reader interface
func (d *deterministicReader) Read(p []byte) (n int, err error) {
	for i := range p {
		p[i] = byte(d.rng.Intn(256))
	}
	return len(p), nil
}

// getTestPrivateKey returns a fixed private key for deterministic testing
func getTestPrivateKey() *rsa.PrivateKey {
	// Load the test key from fixture file in root testdata directory
	keyPEM, err := os.ReadFile("../../../testdata/test_private_key.pem")
	if err != nil {
		panic("failed to read test private key fixture: " + err.Error())
	}

	block, _ := pem.Decode(keyPEM)
	if block == nil {
		panic("failed to decode test private key")
	}

	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		panic("failed to parse test private key: " + err.Error())
	}

	return key
}

// formatCSRForReadability converts a PEM-encoded CSR to a human-readable format
func formatCSRForReadability(csrPEM []byte) (string, error) {
	block, _ := pem.Decode(csrPEM)
	if block == nil {
		return "", fmt.Errorf("failed to decode PEM block")
	}

	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("failed to parse CSR: %w", err)
	}

	// Format in OpenSSL-style output
	result := "Certificate Request:\n"
	result += "    Data:\n"
	result += fmt.Sprintf("        Version: %d\n", csr.Version)
	result += fmt.Sprintf("        Subject: %s\n", csr.Subject.String())
	result += "        Subject Public Key Info:\n"
	result += fmt.Sprintf("            Public Key Algorithm: %s\n", csr.PublicKeyAlgorithm)
	if rsaKey, ok := csr.PublicKey.(*rsa.PublicKey); ok {
		result += fmt.Sprintf("                Public-Key: (%d bit)\n", rsaKey.N.BitLen())
		result += "                Modulus:\n"
		// Format modulus in hex with colons like OpenSSL
		modBytes := rsaKey.N.Bytes()
		for i, b := range modBytes {
			if i%16 == 0 {
				result += "                    "
			}
			result += fmt.Sprintf("%02x", b)
			if i < len(modBytes)-1 {
				result += ":"
			}
			if (i+1)%16 == 0 || i == len(modBytes)-1 {
				result += "\n"
			}
		}
		result += fmt.Sprintf("                Exponent: %d (0x%x)\n", rsaKey.E, rsaKey.E)
	}
	result += fmt.Sprintf("    Signature Algorithm: %s\n", csr.SignatureAlgorithm)

	return result, nil
}

func TestGenerateCSR(t *testing.T) {
	tests := []struct {
		name       string
		user       string
		privileged bool
		seed       int64
	}{
		{
			name:       "unprivileged-session",
			user:       "b-user@microsoft.com",
			privileged: false,
			seed:       12345,
		},
		{
			name:       "privileged-session",
			user:       "b-user@microsoft.com",
			privileged: true,
			seed:       67890,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use fixed key for deterministic testing
			key := getTestPrivateKey()

			subject := BuildSubject(tt.user, tt.privileged)
			csrPEM, err := generateCSRWithRngSource(newDeterministicReader(tt.seed), key, subject)
			if err != nil {
				t.Fatalf("failed to generate CSR: %v", err)
			}

			// Convert to human-readable format and validate against golden fixture
			readableCSR, err := formatCSRForReadability(csrPEM)
			if err != nil {
				t.Fatalf("failed to format CSR: %v", err)
			}
			testutil.CompareWithFixture(t, readableCSR, testutil.WithExtension(".txt"))
		})
	}
}

func TestBuildSubject(t *testing.T) {
	tests := []struct {
		name       string
		user       string
		privileged bool
		expected   pkix.Name
	}{
		{
			name:       "unprivileged-session",
			user:       "b-user@microsoft.com",
			privileged: false,
			expected: pkix.Name{
				CommonName:   "system:sre-break-glass:b-user@microsoft.com",
				Organization: []string{"aro-sre"},
			},
		},
		{
			name:       "privileged-session",
			user:       "b-user@microsoft.com",
			privileged: true,
			expected: pkix.Name{
				CommonName:   "system:sre-break-glass:b-user@microsoft.com",
				Organization: []string{"aro-sre-cluster-admin"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			subject := BuildSubject(tc.user, tc.privileged)

			if subject.CommonName != tc.expected.CommonName {
				t.Errorf("expected CN %s, got %s", tc.expected.CommonName, subject.CommonName)
			}

			if len(subject.Organization) != len(tc.expected.Organization) {
				t.Errorf("expected Organization length %d, got %d", len(tc.expected.Organization), len(subject.Organization))
			}

			for i, expectedOrg := range tc.expected.Organization {
				if i >= len(subject.Organization) || subject.Organization[i] != expectedOrg {
					t.Errorf("expected Organization %v, got %v", tc.expected.Organization, subject.Organization)
					break
				}
			}
		})
	}
}
