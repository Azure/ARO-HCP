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

package systemadmincredential

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
)

// keypairBits is the RSA modulus size used for every break-glass
// credential. 2048 matches what cluster-service writes today and what
// HyperShift's customer-break-glass signer expects on the input. Bumping
// this is a separate decision that needs coordination with HyperShift.
const keypairBits = 2048

// GenerateKeypair generates a fresh RSA keypair and returns both halves
// PEM-encoded. The dispatcher writes these onto
// SystemAdminCredentialSpec.PublicKeyPEM / .PrivateKeyPEM and then drops
// the in-memory keys; the private key never leaves Cosmos.
func GenerateKeypair() (publicPEM, privatePEM []byte, err error) {
	key, err := rsa.GenerateKey(rand.Reader, keypairBits)
	if err != nil {
		return nil, nil, fmt.Errorf("generating RSA keypair: %w", err)
	}

	privDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("marshaling private key: %w", err)
	}
	privatePEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER})

	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return nil, nil, fmt.Errorf("marshaling public key: %w", err)
	}
	publicPEM = pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})

	return publicPEM, privatePEM, nil
}

// parseRSAPrivateKey decodes the dispatcher-generated PEM back into an
// *rsa.PrivateKey for callers that need to sign a CSR. We only support
// PKCS#8 encoding because that is the only form GenerateKeypair emits;
// rejecting other forms keeps callers from accidentally bringing in
// unexpected key material.
func parseRSAPrivateKey(privateKeyPEM []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(privateKeyPEM)
	if block == nil {
		return nil, fmt.Errorf("decoding private-key PEM: no PEM block found")
	}
	if block.Type != "PRIVATE KEY" {
		return nil, fmt.Errorf("decoding private-key PEM: unexpected block type %q", block.Type)
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing PKCS#8 private key: %w", err)
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("private key is not RSA: got %T", key)
	}
	return rsaKey, nil
}
