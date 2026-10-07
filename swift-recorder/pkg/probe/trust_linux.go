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

package probe

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"errors"
)

const (
	maxTrustTotalBytes  = 128 * 1024
	maxTrustBundleBytes = maxTrustTotalBytes / 2
)

// Pools are rebuilt for each request so CA rotation cannot retain stale trust.
// Invalid bundles are absent, not replaced with system roots or another bundle.
func loadTrustBundles(bundles map[string]string) map[string]*x509.CertPool {
	pools := make(map[string]*x509.CertPool)
	// Two known bundles at 64 KiB each bound accepted PEM to 128 KiB.
	// Unknown identifiers and rejected bundles cannot consume another's budget.
	for _, id := range []string{TrustRoot, TrustIgnition} {
		bundle := bundles[id]
		if len(bundle) == 0 || len(bundle) > maxTrustBundleBytes {
			continue
		}
		pool := x509.NewCertPool()
		data := bytes.TrimSpace([]byte(bundle))
		valid := len(data) > 0
		hasCA := false
		for len(data) > 0 {
			block, rest := pem.Decode(data)
			// Decode can skip junk and malformed blocks. Reject those too, as
			// well as mixed certificate/private-key input.
			if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 || !bytes.HasPrefix(data, []byte("-----BEGIN CERTIFICATE-----")) || bytes.Count(data[:len(data)-len(rest)], []byte("-----BEGIN ")) != 1 {
				valid = false
				break
			}
			cert, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				valid = false
				break
			}
			data = bytes.TrimSpace(rest)
			// HyperShift root bundles can also contain ingress serving certs.
			if !cert.IsCA {
				continue
			}
			if !cert.BasicConstraintsValid || (cert.KeyUsage != 0 && cert.KeyUsage&x509.KeyUsageCertSign == 0) {
				valid = false
				break
			}
			pool.AddCert(cert)
			hasCA = true
		}
		if valid && hasCA {
			pools[id] = pool
		}
	}
	return pools
}

func tlsError(err error) string {
	var authority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	switch {
	case errors.As(err, &authority):
		return "TLS verification failed: unknown authority"
	case errors.As(err, &hostname):
		return "TLS verification failed: server name mismatch"
	case errors.As(err, &invalid):
		if invalid.Reason == x509.Expired {
			return "TLS verification failed: certificate expired or not yet valid"
		}
		return "TLS verification failed: invalid certificate"
	default:
		return protocolError("TLS handshake failed", err)
	}
}
