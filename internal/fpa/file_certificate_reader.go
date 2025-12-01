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
	"crypto"
	"crypto/x509"
	"fmt"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

// FileCertificateReader implements CertificateReader for file-based certificates.
type FileCertificateReader struct {
	filePath string
}

// NewFileCertificateReader creates a new file-based certificate reader.
func NewFileCertificateReader(filePath string) *FileCertificateReader {
	return &FileCertificateReader{
		filePath: filePath,
	}
}

// ReadCertificate reads and parses the certificate from the file.
func (f *FileCertificateReader) ReadCertificate() ([]*x509.Certificate, crypto.PrivateKey, error) {
	data, err := os.ReadFile(f.filePath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read certificate file: %w", err)
	}

	certs, key, err := azidentity.ParseCertificates(data, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse certificate: %w", err)
	}

	if len(certs) == 0 {
		return nil, nil, fmt.Errorf("no certificates found in file")
	}

	return certs, key, nil
}
