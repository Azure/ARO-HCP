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

package certificate

import (
	"crypto"
	"crypto/x509"
	"fmt"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

// AzureIdentityFileReader implements CertificateReader for file-based certificates
// that are used to authenticate with an Azure identity.
type AzureIdentityFileReader struct {
	filePath string
}

var _ Reader = (*AzureIdentityFileReader)(nil)

// NewAzureIdentityFileReader creates a new file-based certificate reader.
func NewAzureIdentityFileReader(filePath string) *AzureIdentityFileReader {
	return &AzureIdentityFileReader{
		filePath: filePath,
	}
}

// ReadCertificate reads and parses the certificate from the file. It expects
// the certificate to be in PEM or PKCS#12 format. Keys in PEM format or PKCS#12
// certificates that use SHA256 for message authentication are not supported.
// ParseCertificates loads certificates and a private key, in PEM or PKCS#12 format, for use with [NewClientCertificateCredential].
// Pass nil for password if the private key isn't encrypted. This function has limitations, for example it can't decrypt keys in
// PEM format or PKCS#12 certificates that use SHA256 for message authentication. If you encounter such limitations, consider
// using another module to load the certificate and private key.
func (f *AzureIdentityFileReader) ReadCertificate() ([]*x509.Certificate, crypto.PrivateKey, error) {
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
