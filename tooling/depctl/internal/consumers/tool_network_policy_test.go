//go:build toolintegration

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

package consumers

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestToolUpdaterNetworkPolicy checks the generated AWF policy against real updater requests and release redirects.
func TestToolUpdaterNetworkPolicy(t *testing.T) {
	const root = "../../../.."
	allowedDomains := readToolUpdaterAllowDomains(t, filepath.Join(root, ".github/workflows/tool-version-updates.lock.yml"))
	if slices.Contains(allowedDomains, "*") {
		t.Fatal("generated AWF policy allows every domain")
	}
	tests := []struct {
		url         string
		wantAllowed bool
	}{
		{"https://github.com/Azure/bicep/releases/download/v0.48.1/bicep-linux-x64", true},
		{"https://github.com/prometheus/prometheus/releases/download/v3.2.1/prometheus-3.2.1.linux-amd64.tar.gz", true},
		{"https://release-assets.githubusercontent.com/github-production-release-asset/123/asset", true},
		{"https://example.invalid/asset", false},
		{"https://api.github.com.evil.invalid/releases/latest", false},
		{"https://github.com.evil.invalid/asset", false},
		{"https://release-assets.githubusercontent.com.evil.invalid/asset", false},
	}
	t.Run("release redirects and unrelated hosts", func(t *testing.T) {
		for _, tc := range tests {
			t.Run(tc.url, func(t *testing.T) {
				host := toolUpdaterURLHost(t, tc.url)
				if got := toolUpdaterDomainAllowed(allowedDomains, host); got != tc.wantAllowed {
					t.Errorf("generated AWF policy allows %q: got %t, want %t", tc.url, got, tc.wantAllowed)
				}
			})
		}
	})
	t.Run("updater requests", func(t *testing.T) {
		for _, rawURL := range captureToolUpdaterRequests(t, filepath.Join(root, "dev-infrastructure/openshift-ci")) {
			t.Run(rawURL, func(t *testing.T) {
				host := toolUpdaterURLHost(t, rawURL)
				if host != "api.github.com" {
					t.Errorf("updater requested %q; expected only api.github.com", rawURL)
				}
				if !toolUpdaterDomainAllowed(allowedDomains, host) {
					t.Errorf("updater request blocked by generated AWF policy: %s", rawURL)
				}
			})
		}
	})
}

// ReadToolUpdaterAllowDomains decodes the network object embedded in the generated AWF config, not reporting metadata.
func readToolUpdaterAllowDomains(t *testing.T, lockPath string) []string {
	t.Helper()
	lock, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read generated workflow: %v", err)
	}
	for line := range strings.SplitSeq(string(lock), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, `printf '%s\n' `) || !strings.HasSuffix(line, ` > "${RUNNER_TEMP}/gh-aw/awf-config.json"`) {
			continue
		}
		_, networkJSON, ok := strings.Cut(line, `\"network\":`)
		if !ok {
			continue
		}
		// Decode only the network object: the rest of the config can contain shell expansions.
		var network struct {
			AllowDomains []string `json:"allowDomains"`
		}
		if err := json.NewDecoder(strings.NewReader(strings.ReplaceAll(networkJSON, `\"`, `"`))).Decode(&network); err != nil {
			t.Fatalf("decode network object in generated AWF config: %v", err)
		}
		if len(network.AllowDomains) == 0 {
			t.Fatal("generated AWF network object has no allowDomains")
		}
		return network.AllowDomains
	}
	t.Fatalf("no AWF network config found in %s", lockPath)
	return nil
}

// ToolUpdaterURLHost extracts the hostname without a port or user information.
func toolUpdaterURLHost(t *testing.T, rawURL string) string {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse URL %q: %v", rawURL, err)
	}
	if parsed.Hostname() == "" {
		t.Fatalf("URL %q has no hostname", rawURL)
	}
	return strings.ToLower(parsed.Hostname())
}

// ToolUpdaterDomainAllowed applies AWF's plain-domain matching to a normalized hostname.
func toolUpdaterDomainAllowed(domains []string, host string) bool {
	for _, domain := range domains {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

// CaptureToolUpdaterRequests runs the actual updater against copies of its inputs and records stubbed curl calls.
func captureToolUpdaterRequests(t *testing.T, tools string) []string {
	t.Helper()
	scratch := t.TempDir()
	for _, name := range []string{"update-tool-versions.sh", "versions.mk", "Dockerfile"} {
		contents, err := os.ReadFile(filepath.Join(tools, name))
		if err != nil {
			t.Fatalf("read updater input %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(scratch, name), contents, 0600); err != nil {
			t.Fatalf("copy updater input %s: %v", name, err)
		}
	}
	commands := filepath.Join(scratch, "bin")
	external := filepath.Join(scratch, "external-tools")
	for _, dir := range []string{commands, external} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatalf("create fixture directory %s: %v", dir, err)
		}
	}
	for _, name := range []string{"dirname", "grep", "sort", "tail", "sed"} {
		executable, err := exec.LookPath(name)
		if err != nil {
			t.Fatalf("find updater prerequisite %s: %v", name, err)
		}
		executable, err = filepath.Abs(executable)
		if err != nil {
			t.Fatalf("make updater prerequisite path absolute for %s: %v", name, err)
		}
		if err := os.Symlink(executable, filepath.Join(commands, name)); err != nil {
			t.Fatalf("link updater prerequisite %s: %v", name, err)
		}
	}
	jq, err := exec.LookPath("jq")
	if err != nil {
		t.Fatalf("find repository-pinned jq on PATH: %v", err)
	}
	jq, err = filepath.Abs(jq)
	if err != nil {
		t.Fatalf("make jq executable path absolute: %v", err)
	}
	jq, err = filepath.EvalSymlinks(jq)
	if err != nil {
		t.Fatalf("resolve jq executable: %v", err)
	}
	// The trace proves the updater uses the selected external jq rather than a system fallback.
	const jqWrapper = `#!/bin/bash
set -euo pipefail
printf '%s\n' "$JQ_BIN" >>"$JQ_TRACE"
exec "$JQ_BIN" "$@"
`
	const curlStub = `#!/bin/bash
set -euo pipefail
for argument in "$@"; do
  case "$argument" in https://*) printf '%s\n' "$argument" >>"$URL_LOG";; esac
done
printf '%s\n' '{"tag_name":"v0.0.0"}'
`
	if err := os.WriteFile(filepath.Join(external, "jq"), []byte(jqWrapper), 0700); err != nil {
		t.Fatalf("install jq wrapper: %v", err)
	}
	if err := os.WriteFile(filepath.Join(commands, "curl"), []byte(curlStub), 0700); err != nil {
		t.Fatalf("install curl stub: %v", err)
	}
	requestLog := filepath.Join(scratch, "requests")
	jqTrace := filepath.Join(scratch, "jq-executions")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/bash", filepath.Join(scratch, "update-tool-versions.sh"))
	cmd.Env = []string{
		"PATH=" + commands + string(os.PathListSeparator) + external,
		"HOME=" + scratch,
		"URL_LOG=" + requestLog,
		"JQ_BIN=" + jq,
		"JQ_TRACE=" + jqTrace,
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run updater with stubbed curl: %v\n%s", err, out)
	}
	trace, err := os.ReadFile(jqTrace)
	if err != nil {
		t.Fatalf("read jq execution trace: %v", err)
	}
	for executable := range strings.SplitSeq(strings.TrimSuffix(string(trace), "\n"), "\n") {
		if executable != jq {
			t.Errorf("updater executed jq at %q, want %q", executable, jq)
		}
	}
	requests, err := os.ReadFile(requestLog)
	if err != nil {
		t.Fatalf("read updater request log: %v", err)
	}
	if len(requests) == 0 {
		t.Fatal("updater did not make any HTTPS requests")
	}
	return strings.Split(strings.TrimSuffix(string(requests), "\n"), "\n")
}
