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

package main

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

func TestSplitList(t *testing.T) {
	got := splitList(" a, b ,,c ")
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestVaultBaseURL(t *testing.T) {
	if got := vaultBaseURL("my-vault"); got != "https://my-vault.vault.azure.net" {
		t.Errorf("name expansion: got %q", got)
	}
	if got := vaultBaseURL("https://my-vault.vault.azure.net/"); got != "https://my-vault.vault.azure.net" {
		t.Errorf("url passthrough and trim: got %q", got)
	}
}

func TestFilterCertificates(t *testing.T) {
	certs := []certificate{
		{Name: "maestro-server-usw3"},
		{Name: "hcp-underlay-usw3-mgmt-1"},
		{Name: "hcp-underlay-usw3-mgmt-2"},
		{Name: "frontend-cert-int-usw3"},
		{Name: "rplogs"},
	}
	patterns := []string{"maestro-server-*", "hcp-underlay-*-mgmt-*"}
	got := filterCertificates(certs, patterns)
	if len(got) != 3 {
		t.Fatalf("expected 3 matches, got %d: %v", len(got), got)
	}
	for _, cert := range got {
		if cert.Name == "frontend-cert-int-usw3" || cert.Name == "rplogs" {
			t.Errorf("pattern allowlist admitted %q", cert.Name)
		}
	}
}

func TestFilterCertificatesNoMatch(t *testing.T) {
	certs := []certificate{{Name: "frontend-cert-int-usw3"}}
	if got := filterCertificates(certs, []string{"maestro-*"}); len(got) != 0 {
		t.Errorf("expected no matches, got %v", got)
	}
}

func TestRunIsBestEffortWithoutInputs(t *testing.T) {
	// No vaults or patterns configured, and no Azure credential reachable in the
	// test environment. The run must report and return nil so the pipeline step
	// stays green.
	dir := t.TempDir()
	evidence := filepath.Join(dir, "evidence.jsonl")
	env := map[string]string{"EVIDENCE_PATH": evidence}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()

	if err := run(t.Context(), func(key string) string { return env[key] }, w); err != nil {
		t.Fatalf("run must not return an error: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close pipe: %v", err)
	}
}

func TestOpenEvidenceDisabled(t *testing.T) {
	file, closeFn, err := openEvidence("")
	if err != nil || file != nil || closeFn != nil {
		t.Fatalf("expected a disabled evidence writer, got file=%v err=%v", file, err)
	}
}

func TestEmitWritesOneJSONLinePerRecord(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "evidence.jsonl")
	file, closeFn, err := openEvidence(path)
	if err != nil {
		t.Fatalf("open evidence: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	emit(file, logger, record{Vault: "v", Certificate: "c", Action: "delete", Result: "planned"})
	closeFn()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read evidence: %v", err)
	}
	if len(content) == 0 || content[len(content)-1] != '\n' {
		t.Fatalf("evidence must end with a newline, got %q", string(content))
	}
}

func TestSplitCertificateID(t *testing.T) {
	cases := []struct {
		id      string
		name    string
		version string
		ok      bool
	}{
		// The list API returns ids without a version; this is the shape the
		// previous parser read as name="certificates".
		{"https://v.vault.azure.net/certificates/maestro-server-usw3", "maestro-server-usw3", "", true},
		{"https://v.vault.azure.net/certificates/maestro-server-usw3/abc123", "maestro-server-usw3", "abc123", true},
		{"https://v.vault.azure.net/certificates/cxclusterlogs/", "cxclusterlogs", "", true},
		{"https://v.vault.azure.net/secrets/nope", "", "", false},
		{"https://v.vault.azure.net/certificates", "", "", false},
		{"://bad", "", "", false},
	}
	for _, c := range cases {
		name, version, ok := splitCertificateID(c.id)
		if ok != c.ok || name != c.name || version != c.version {
			t.Errorf("splitCertificateID(%q) = (%q,%q,%v), want (%q,%q,%v)", c.id, name, version, ok, c.name, c.version, c.ok)
		}
	}
}

func TestPatternsMatchRenderedTemplates(t *testing.T) {
	// Mirrors config/config.yaml: maestro.server.mqttClientName and
	// maestro.agent.consumerName, which the pipeline patterns must keep matching
	// in every environment, not just INT.
	regions := []string{"usw3", "uksouth", "eastus2", "centralus"}
	stamps := []string{"1", "2"}
	for _, region := range regions {
		server := "maestro-server-" + region
		if got := filterCertificates([]certificate{{Name: server}}, []string{"maestro-server-*"}); len(got) != 1 {
			t.Errorf("maestro-server pattern missed %q", server)
		}
		for _, stamp := range stamps {
			consumer := "hcp-underlay-" + region + "-mgmt-" + stamp
			if got := filterCertificates([]certificate{{Name: consumer}}, []string{"hcp-underlay-*-mgmt-*"}); len(got) != 1 {
				t.Errorf("consumer pattern missed %q", consumer)
			}
		}
	}

	// Other certificates share the same vaults and must never match.
	unrelated := []certificate{
		{Name: "cxclusterlogs"},
		{Name: "rplogs"},
		{Name: "geneva-logs-admin"},
		{Name: "frontend-cert-int-usw3"},
		{Name: "admin-api-cert-int-usw3"},
		{Name: "sessiongate-cert-int-usw3"},
	}
	for _, pattern := range []string{"maestro-server-*", "hcp-underlay-*-mgmt-*"} {
		if got := filterCertificates(unrelated, []string{pattern}); len(got) != 0 {
			t.Errorf("pattern %q matched unrelated certificates: %v", pattern, got)
		}
	}
}

func TestIsAlreadyDeleted(t *testing.T) {
	notFound := &azcore.ResponseError{StatusCode: http.StatusNotFound}
	conflict := &azcore.ResponseError{StatusCode: http.StatusConflict}
	forbidden := &azcore.ResponseError{StatusCode: http.StatusForbidden}
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{notFound, true},
		{conflict, true},
		{forbidden, false},
		{fmt.Errorf("plain"), false},
	}
	for _, c := range cases {
		if got := isAlreadyDeleted(c.err); got != c.want {
			t.Errorf("isAlreadyDeleted(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}
