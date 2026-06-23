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
	"crypto/x509"
	"encoding/pem"
	"testing"
)

func TestGenerateKeypair(t *testing.T) {
	pubPEM, privPEM, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair() error = %v", err)
	}

	// Verify public key decodes
	pubBlock, _ := pem.Decode(pubPEM)
	if pubBlock == nil {
		t.Fatal("failed to decode public key PEM")
	}
	if pubBlock.Type != "PUBLIC KEY" {
		t.Errorf("public key PEM type = %q, want %q", pubBlock.Type, "PUBLIC KEY")
	}
	_, err = x509.ParsePKIXPublicKey(pubBlock.Bytes)
	if err != nil {
		t.Fatalf("failed to parse public key: %v", err)
	}

	// Verify private key decodes
	privBlock, _ := pem.Decode(privPEM)
	if privBlock == nil {
		t.Fatal("failed to decode private key PEM")
	}
	if privBlock.Type != "PRIVATE KEY" {
		t.Errorf("private key PEM type = %q, want %q", privBlock.Type, "PRIVATE KEY")
	}
	_, err = x509.ParsePKCS8PrivateKey(privBlock.Bytes)
	if err != nil {
		t.Fatalf("failed to parse private key: %v", err)
	}
}
