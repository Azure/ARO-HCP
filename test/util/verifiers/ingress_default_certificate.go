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

package verifiers

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"k8s.io/client-go/rest"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates"
)

type verifyIngressDefaultCertificate struct {
	credential azcore.TokenCredential
	vaultURLs  []string
	host       string
	probe      func(context.Context) (string, error)
	timeout    time.Duration
}

func VerifyIngressDefaultCertificate(credential azcore.TokenCredential, vaultURLs []string, host string, probe func(context.Context) (string, error), timeout time.Duration) HostedClusterVerifier {
	return verifyIngressDefaultCertificate{credential: credential, vaultURLs: vaultURLs, host: host, probe: probe, timeout: timeout}
}

func (verifier verifyIngressDefaultCertificate) Name() string {
	return "VerifyIngressDefaultCertificate"
}

func (verifier verifyIngressDefaultCertificate) Verify(ctx context.Context, adminConfig *rest.Config) error {
	if len(verifier.vaultURLs) == 0 {
		return fmt.Errorf("at least one OneCert certificate vault URL is required")
	}
	clients := make([]*azcertificates.Client, 0, len(verifier.vaultURLs))
	for _, vaultURL := range verifier.vaultURLs {
		client, err := azcertificates.NewClient(vaultURL, verifier.credential, nil)
		if err != nil {
			return fmt.Errorf("create Key Vault certificate client: %w", err)
		}
		clients = append(clients, client)
	}
	return pollUntilReady(ctx, verifier.Name(), verifier.timeout, DefaultPollInterval, adminConfig, 0, nil, func(ctx context.Context) error {
		fingerprint, err := verifier.probe(ctx)
		if err != nil {
			return fmt.Errorf("reach sample app over trusted HTTPS: %w", err)
		}
		fingerprint = strings.TrimSpace(fingerprint)
		decoded, err := hex.DecodeString(fingerprint)
		if err != nil || len(decoded) != sha256.Size {
			return fmt.Errorf("HTTPS probe did not return a SHA-256 leaf fingerprint")
		}
		for _, client := range clients {
			pager := client.NewListCertificatePropertiesPager(nil)
			for pager.More() {
				page, err := pager.NextPage(ctx)
				if err != nil {
					return fmt.Errorf("list Key Vault certificates: %w", err)
				}
				for _, properties := range page.Value {
					if properties.ID == nil {
						continue
					}
					certificate, err := client.GetCertificate(ctx, properties.ID.Name(), "", nil)
					if err != nil {
						return fmt.Errorf("get Key Vault certificate %s: %w", properties.ID.Name(), err)
					}
					if certificateFingerprint(certificate.CER) != fingerprint {
						continue
					}
					return verifyOneCertLeaf(certificate, verifier.host)
				}
			}
		}
		return fmt.Errorf("served ingress leaf %s does not match any certificate in the configured Key Vaults", fingerprint)
	})
}

func verifyOneCertLeaf(certificate azcertificates.GetCertificateResponse, host string) error {
	if certificate.Policy == nil || certificate.Policy.IssuerParameters == nil || certificate.Policy.IssuerParameters.Name == nil || *certificate.Policy.IssuerParameters.Name != "OneCertV2-PublicCA" {
		return fmt.Errorf("matching Key Vault certificate must use OneCertV2-PublicCA, not a self-signed issuer")
	}
	leaf, err := x509.ParseCertificate(certificate.CER)
	if err != nil {
		return fmt.Errorf("parse Key Vault leaf: %w", err)
	}
	if err := leaf.VerifyHostname(host); err != nil {
		return fmt.Errorf("Key Vault leaf must cover the sample app hostname: %w", err)
	}
	return nil
}

func certificateFingerprint(der []byte) string {
	fingerprint := sha256.Sum256(der)
	return hex.EncodeToString(fingerprint[:])
}

func ProbeIngressCertificate(ctx context.Context, host string) (string, error) {
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport:     transport,
		Timeout:       30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+host, nil)
	if err != nil {
		return "", err
	}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("sample app returned HTTP %d, expected 200", response.StatusCode)
	}
	if response.TLS == nil || len(response.TLS.VerifiedChains) == 0 || len(response.TLS.PeerCertificates) == 0 {
		return "", fmt.Errorf("sample app returned no verified TLS certificate chain")
	}
	return certificateFingerprint(response.TLS.PeerCertificates[0].Raw), nil
}

func IngressCertificateProbeCommand(host string) string {
	return fmt.Sprintf(`python3 - <<'PYTHON'
import base64
import hashlib
import http.client
import ssl

host = base64.b64decode(%q).decode("ascii")
context = ssl.create_default_context()
context.minimum_version = ssl.TLSVersion.TLSv1_2
connection = http.client.HTTPSConnection(host, timeout=30, context=context)
try:
    connection.connect()
    leaf = connection.sock.getpeercert(binary_form=True)
    connection.request("GET", "/")
    response = connection.getresponse()
    if response.status != 200:
        raise RuntimeError("sample app returned HTTP %%d, expected 200" %% response.status)
    print(hashlib.sha256(leaf).hexdigest())
finally:
    connection.close()
PYTHON`, base64.StdEncoding.EncodeToString([]byte(host)))
}
