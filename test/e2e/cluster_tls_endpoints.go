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

package e2e

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"embed"
	"fmt"
	"net"
	"net/url"
	"time"
)

// Based on our OneCert configuration, the PKIs we need in this directory come from
// https://eng.ms/docs/products/onecert-certificates-key-vault-and-dsms/key-vault-dsms/reference/ca-details
//
//go:embed azure-cas/*.crt
var azureCAs embed.FS

func tlsCertsFromURL(ctx context.Context, u string) ([]*x509.Certificate, error) {
	parsedURL, err := url.Parse(u)
	if err != nil {
		return nil, err
	}
	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: 10 * time.Second},
		Config:    &tls.Config{InsecureSkipVerify: true},
	}
	conn, err := dialer.DialContext(ctx, "tcp", parsedURL.Host)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	state := conn.(*tls.Conn).ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return nil, fmt.Errorf("no certificates served")
	}
	return state.PeerCertificates, nil
}

func loadAzureCAs(directory string) (*x509.CertPool, error) {
	pool := x509.NewCertPool()
	entries, err := azureCAs.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("reading embedded %s directory: %w", directory, err)
	}
	for _, entry := range entries {
		data, err := azureCAs.ReadFile(directory + "/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("reading embedded CA %s: %w", entry.Name(), err)
		}
		cert, err := x509.ParseCertificate(data)
		if err != nil {
			return nil, fmt.Errorf("parsing CA certificate %s: %w", entry.Name(), err)
		}
		pool.AddCert(cert)
	}
	return pool, nil
}

func verifyCertChain(certs []*x509.Certificate, roots *x509.CertPool) error {
	if len(certs) == 0 {
		return fmt.Errorf("no certificates provided for verification")
	}

	intermediates := x509.NewCertPool()
	for _, c := range certs[1:] {
		intermediates.AddCert(c)
	}
	_, err := certs[0].Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediates,
	})
	return err
}
