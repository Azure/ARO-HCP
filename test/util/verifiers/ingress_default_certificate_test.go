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
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates"
)

func TestOneCertLeafPolicyAndHostname(test *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	for _, scenario := range []struct {
		name      string
		issuer    string
		host      string
		wantError bool
	}{
		{name: "OneCert matching hostname", issuer: "OneCertV2-PublicCA", host: "example.com"},
		{name: "self signed policy", issuer: "Self", host: "example.com", wantError: true},
		{name: "wrong hostname", issuer: "OneCertV2-PublicCA", host: "example.invalid", wantError: true},
		{name: "missing policy", host: "example.com", wantError: true},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			certificate := azcertificates.GetCertificateResponse{
				Certificate: azcertificates.Certificate{CER: server.Certificate().Raw},
			}
			if scenario.issuer != "" {
				certificate.Policy = &azcertificates.CertificatePolicy{IssuerParameters: &azcertificates.IssuerParameters{Name: to.Ptr(scenario.issuer)}}
			}
			err := verifyOneCertLeaf(certificate, scenario.host)
			if (err != nil) != scenario.wantError {
				test.Fatalf("verifyOneCertLeaf error = %v, wantError = %v", err, scenario.wantError)
			}
		})
	}
}

func TestIngressCertificateProbeRejectsUntrustedTLS(test *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	_, err := ProbeIngressCertificate(context.Background(), strings.TrimPrefix(server.URL, "https://"))
	if err == nil {
		test.Fatal("public probe accepted an untrusted certificate")
	}
}

func TestIngressCertificateVMProbe(test *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusFound, http.StatusServiceUnavailable} {
		test.Run(http.StatusText(status), func(test *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.WriteHeader(status)
			}))
			defer server.Close()
			certificateFile := filepath.Join(test.TempDir(), "ca.pem")
			if err := os.WriteFile(certificateFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
				test.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, "sh", "-c", IngressCertificateProbeCommand(strings.TrimPrefix(server.URL, "https://")))
			command.Env = append(os.Environ(), "SSL_CERT_FILE="+certificateFile)
			output, err := command.CombinedOutput()
			if status != http.StatusOK {
				if err == nil || !strings.Contains(string(output), "expected 200") {
					test.Fatalf("expected HTTP failure, got output %q, error %v", output, err)
				}
				return
			}
			if err != nil || strings.TrimSpace(string(output)) != certificateFingerprint(server.Certificate().Raw) {
				test.Fatalf("VM probe must return the served leaf fingerprint: output %q, error %v", output, err)
			}
		})
	}
}
