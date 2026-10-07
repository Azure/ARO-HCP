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
	"crypto/x509"
	"fmt"
	"net/http"
	"slices"
	"time"

	"k8s.io/client-go/rest"

	"github.com/Azure/ARO-HCP/test/util/framework"
)

type defaultIngressCertificateVerifier struct {
	routeHost       string
	ingressWildcard string
	selfSigned      bool
	roots           *x509.CertPool
	expectedLeaf    *x509.Certificate
	timeout         time.Duration
}

func VerifyDefaultIngressCertificate(routeHost, ingressWildcard string, timeout time.Duration) HostedClusterVerifier {
	return defaultIngressCertificateVerifier{routeHost: routeHost, ingressWildcard: ingressWildcard, selfSigned: framework.IsDevelopmentEnvironment(), timeout: timeout}
}

func VerifyCustomIngressCertificate(routeHost, ingressWildcard string, certificate *x509.Certificate, roots *x509.CertPool, timeout time.Duration) HostedClusterVerifier {
	return defaultIngressCertificateVerifier{routeHost: routeHost, ingressWildcard: ingressWildcard, expectedLeaf: certificate, roots: roots, timeout: timeout}
}

func (verifier defaultIngressCertificateVerifier) Name() string {
	if verifier.expectedLeaf != nil {
		return "VerifyCustomIngressCertificate"
	}
	return "VerifyDefaultIngressCertificate"
}

func (verifier defaultIngressCertificateVerifier) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	if verifier.timeout <= 0 {
		return fmt.Errorf("%s: timeout must be > 0, got %s", verifier.Name(), verifier.timeout)
	}
	if verifier.routeHost == "" {
		return fmt.Errorf("%s: route host must not be empty", verifier.Name())
	}
	if verifier.ingressWildcard == "" {
		return fmt.Errorf("%s: ingress wildcard must not be empty", verifier.Name())
	}
	if err := framework.WaitForDNSResolution(ctx, verifier.routeHost, framework.DNSResolutionTimeout); err != nil {
		return fmt.Errorf("DNS for route host %s did not resolve: %w", verifier.routeHost, err)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	transport.TLSClientConfig = verifier.tlsConfig()
	client := &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return pollUntilReady(ctx, verifier.Name(), verifier.timeout, DefaultPollInterval, adminRESTConfig, DefaultDiagnoseTimeout,
		func(ctx context.Context, _ *rest.Config) string {
			printNegotiatedCertificate(ctx, verifier.Name(), verifier.routeHost)
			return ""
		},
		func(ctx context.Context) error {
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+verifier.routeHost, nil)
			if err != nil {
				return err
			}
			response, err := client.Do(request)
			if err != nil {
				return fmt.Errorf("ingress route must serve HTTPS with the expected certificate: %w", err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				return fmt.Errorf("default ingress route returned HTTP %d, expected HTTP 200", response.StatusCode)
			}
			return nil
		})
}

func (verifier defaultIngressCertificateVerifier) tlsConfig() *tls.Config {
	return &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: verifier.selfSigned,
		RootCAs:            verifier.roots,
		VerifyConnection: func(state tls.ConnectionState) error {
			if err := verifyDefaultIngressCertificate(state, verifier.routeHost, verifier.ingressWildcard, verifier.selfSigned); err != nil {
				return err
			}
			if verifier.expectedLeaf != nil && !state.PeerCertificates[0].Equal(verifier.expectedLeaf) {
				return fmt.Errorf("route %q is not serving the customer-specified certificate", verifier.routeHost)
			}
			return nil
		},
	}
}

func verifyDefaultIngressCertificate(state tls.ConnectionState, host, ingressWildcard string, selfSigned bool) error {
	if len(state.PeerCertificates) == 0 {
		return fmt.Errorf("route %q served no TLS leaf certificate", host)
	}
	if !selfSigned && len(state.VerifiedChains) == 0 {
		return fmt.Errorf("route %q served no verified TLS certificate chain", host)
	}
	certificate := state.PeerCertificates[0]
	if err := certificate.VerifyHostname(host); err != nil {
		return fmt.Errorf("default ingress certificate does not cover route %q: %w", host, err)
	}
	if !slices.Contains(certificate.DNSNames, ingressWildcard) {
		return fmt.Errorf("default ingress certificate SANs %v do not contain %q", certificate.DNSNames, ingressWildcard)
	}
	if certificate.SerialNumber == nil || certificate.SerialNumber.Sign() <= 0 {
		return fmt.Errorf("default ingress certificate has invalid serial number %v", certificate.SerialNumber)
	}
	if now := time.Now(); now.Before(certificate.NotBefore) || now.After(certificate.NotAfter) {
		return fmt.Errorf("default ingress certificate validity %s to %s does not include current time", certificate.NotBefore, certificate.NotAfter)
	}
	return nil
}
