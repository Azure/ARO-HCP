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
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"testing"
)

func TestGeneratePrivateKey(t *testing.T) {
	key, err := GeneratePrivateKey(2048)
	if err != nil {
		t.Fatalf("failed to generate private key: %v", err)
	}

	if key == nil {
		t.Fatal("expected private key, got nil")
	}

	if key.Size() != 2048/8 {
		t.Errorf("expected key size 256 bytes, got %d", key.Size())
	}
}

func TestGenerateCSR(t *testing.T) {
	key, err := GeneratePrivateKey(2048)
	if err != nil {
		t.Fatalf("failed to generate private key: %v", err)
	}

	subject := BuildSubject("testuser")
	csrPEM, err := GenerateCSR(key, subject)
	if err != nil {
		t.Fatalf("failed to generate CSR: %v", err)
	}

	// Parse the CSR to verify it's valid
	block, _ := pem.Decode(csrPEM)
	if block == nil {
		t.Fatal("failed to decode PEM block")
	}

	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		t.Fatalf("failed to parse CSR: %v", err)
	}

	if csr.Subject.CommonName != "system:sre-break-glass:testuser" {
		t.Errorf("expected CN 'system:sre-break-glass:testuser', got %s", csr.Subject.CommonName)
	}

	if len(csr.Subject.Organization) == 0 || csr.Subject.Organization[0] != "sre-group" {
		t.Errorf("expected Organization 'sre-group', got %v", csr.Subject.Organization)
	}
}

func TestBuildSubject(t *testing.T) {
	subject := BuildSubject("testuser")

	expected := pkix.Name{
		CommonName:   "system:sre-break-glass:testuser",
		Organization: []string{"sre-group"},
	}

	if subject.CommonName != expected.CommonName {
		t.Errorf("expected CN %s, got %s", expected.CommonName, subject.CommonName)
	}

	if len(subject.Organization) != 1 || subject.Organization[0] != expected.Organization[0] {
		t.Errorf("expected Organization %v, got %v", expected.Organization, subject.Organization)
	}
}

func TestEncodePrivateKey(t *testing.T) {
	key, err := GeneratePrivateKey(2048)
	if err != nil {
		t.Fatalf("failed to generate private key: %v", err)
	}

	pemData := EncodePrivateKey(key)

	// Parse the PEM to verify it's valid
	block, _ := pem.Decode(pemData)
	if block == nil {
		t.Fatal("failed to decode PEM block")
	}

	if block.Type != "RSA PRIVATE KEY" {
		t.Errorf("expected PEM type 'RSA PRIVATE KEY', got %s", block.Type)
	}

	parsedKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		t.Fatalf("failed to parse private key: %v", err)
	}

	if parsedKey.Size() != key.Size() {
		t.Errorf("parsed key size %d does not match original %d", parsedKey.Size(), key.Size())
	}
}
