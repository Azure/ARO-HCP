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
)

func TestIngressCertificateProbeRejectsUntrustedTLS(test *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	err := ProbeIngressCertificate(test.Context(), strings.TrimPrefix(server.URL, "https://"))
	if err == nil {
		test.Fatal("public probe accepted an untrusted certificate")
	}
}

func TestIngressCertificateProbes(test *testing.T) {
	if host := os.Getenv("AROHCP_TEST_INGRESS_PROBE_HOST"); host != "" {
		if err := ProbeIngressCertificate(test.Context(), host); err != nil {
			test.Fatal(err)
		}
		return
	}
	for _, scenario := range []struct {
		name      string
		status    int
		trust     bool
		wrongHost bool
		wantError string
	}{
		{name: "trusted certificate", status: http.StatusOK, trust: true},
		{name: "untrusted certificate", status: http.StatusOK, wantError: "certificate"},
		{name: "wrong hostname", status: http.StatusOK, trust: true, wrongHost: true, wantError: "certificate"},
		{name: "redirect", status: http.StatusFound, trust: true, wantError: "expected 200"},
		{name: "unavailable app", status: http.StatusServiceUnavailable, trust: true, wantError: "expected 200"},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.WriteHeader(scenario.status)
			}))
			defer server.Close()
			host := strings.TrimPrefix(server.URL, "https://")
			if scenario.wrongHost {
				host = strings.Replace(host, "127.0.0.1", "localhost", 1)
			}
			certificateFile := filepath.Join(test.TempDir(), "ca.pem")
			if err := os.WriteFile(certificateFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
				test.Fatal(err)
			}
			for _, probe := range []string{"public", "private"} {
				test.Run(probe, func(test *testing.T) {
					ctx, cancel := context.WithTimeout(test.Context(), 10*time.Second)
					defer cancel()
					command := exec.CommandContext(ctx, "sh", "-c", IngressCertificateProbeCommand(host))
					if probe == "public" {
						command = exec.CommandContext(ctx, os.Args[0], "-test.run=^TestIngressCertificateProbes$")
					}
					command.Env = append(os.Environ(), "AROHCP_TEST_INGRESS_PROBE_HOST="+host)
					if scenario.trust {
						command.Env = append(command.Env, "SSL_CERT_FILE="+certificateFile)
					}
					output, err := command.CombinedOutput()
					if scenario.wantError != "" {
						if err == nil || !strings.Contains(string(output), scenario.wantError) {
							test.Fatalf("expected %q failure, got output %q, error %v", scenario.wantError, output, err)
						}
						return
					}
					if err != nil {
						test.Fatalf("probe must accept trusted HTTPS: output %q, error %v", output, err)
					}
				})
			}
		})
	}
}
