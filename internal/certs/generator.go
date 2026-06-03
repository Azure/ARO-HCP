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

package certs

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
)

func GeneratePrivateKey(bits int) (*rsa.PrivateKey, error) {
	return generatePrivateKeyWithReader(rand.Reader, bits)
}

func generatePrivateKeyWithReader(reader io.Reader, bits int) (*rsa.PrivateKey, error) {
	key, err := rsa.GenerateKey(reader, bits)
	if err != nil {
		return nil, fmt.Errorf("failed to generate private key: %w", err)
	}
	return key, nil
}

func GenerateCSR(privateKey *rsa.PrivateKey, subject pkix.Name) ([]byte, error) {
	return generateCSRWithRngSource(rand.Reader, privateKey, subject)
}

func generateCSRWithRngSource(rngSource io.Reader, privateKey *rsa.PrivateKey, subject pkix.Name) ([]byte, error) {
	template := x509.CertificateRequest{
		Subject:            subject,
		SignatureAlgorithm: x509.SHA256WithRSA,
	}

	csrDER, err := x509.CreateCertificateRequest(rngSource, &template, privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create certificate request: %w", err)
	}

	csrPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE REQUEST",
		Bytes: csrDER,
	})

	return csrPEM, nil
}

func EncodePrivateKey(key *rsa.PrivateKey) []byte {
	return pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
}

func BuildSubject(user string, privileged bool) pkix.Name {
	organization := "aro-sre"
	if privileged {
		organization = "aro-sre-cluster-admin"
	}

	return pkix.Name{
		CommonName:   fmt.Sprintf("system:sre-break-glass:%s", user),
		Organization: []string{organization},
	}
}
