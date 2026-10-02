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
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net/http"
	"time"

	"k8s.io/client-go/rest"
)

type verifyIngressDefaultCertificate struct {
	probe   func(context.Context) error
	timeout time.Duration
}

func VerifyIngressDefaultCertificate(probe func(context.Context) error, timeout time.Duration) HostedClusterVerifier {
	return verifyIngressDefaultCertificate{probe: probe, timeout: timeout}
}

func (verifier verifyIngressDefaultCertificate) Name() string {
	return "VerifyIngressDefaultCertificate"
}

func (verifier verifyIngressDefaultCertificate) Verify(ctx context.Context, adminConfig *rest.Config) error {
	return pollUntilReady(ctx, verifier.Name(), verifier.timeout, DefaultPollInterval, adminConfig, 0, nil, func(ctx context.Context) error {
		if err := verifier.probe(ctx); err != nil {
			return fmt.Errorf("reach sample app over trusted HTTPS: %w", err)
		}
		return nil
	})
}

func ProbeIngressCertificate(ctx context.Context, host string) error {
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport:     transport,
		Timeout:       30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+host, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("sample app returned HTTP %d, expected 200", response.StatusCode)
	}
	if response.TLS == nil || len(response.TLS.VerifiedChains) == 0 || len(response.TLS.PeerCertificates) == 0 {
		return fmt.Errorf("sample app returned no verified TLS certificate chain")
	}
	return nil
}

func IngressCertificateProbeCommand(host string) string {
	return fmt.Sprintf(`python3 - <<'PYTHON'
import base64
import http.client
import ssl

host = base64.b64decode(%q).decode("ascii")
context = ssl.create_default_context()
context.minimum_version = ssl.TLSVersion.TLSv1_2
connection = http.client.HTTPSConnection(host, timeout=30, context=context)
try:
    connection.connect()
    connection.request("GET", "/")
    response = connection.getresponse()
    if response.status != 200:
        raise RuntimeError("sample app returned HTTP %%d, expected 200" %% response.status)
finally:
    connection.close()
PYTHON`, base64.StdEncoding.EncodeToString([]byte(host)))
}
