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

package dependabot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNativeSandboxReadChecksCredentialIsolationAndReleaseIdentity verifies fail-closed tag proof.
func TestNativeSandboxReadChecksCredentialIsolationAndReleaseIdentity(t *testing.T) {
	const official = "3d3c42e5aac5ba805825da76410c181273ba90b1"
	for _, test := range []struct {
		name, payload, credential string
		accepted                  bool
	}{
		{name: "official tag", payload: `[{"name":"v7.0.1","commit":{"sha":"` + official + `"}}]`, accepted: true},
		{name: "wrong immutable release", payload: `[{"name":"v7.0.1","commit":{"sha":"` + strings.Repeat("f", 40) + `"}}]`},
		{name: "filtered tag list", payload: `[]`},
		{name: "missing requested tag", payload: `[{"name":"v7.0.0","commit":{"sha":"` + official + `"}}]`},
		{name: "object instead of tag list", payload: `{"name":"v7.0.1","commit":{"sha":"` + official + `"}}`},
		{name: "missing tag commit", payload: `[{"name":"v7.0.1"}]`},
		{name: "repository credential", credential: "GH_TOKEN"},
		{name: "GitHub credential", credential: "GITHUB_TOKEN"},
		{name: "CI credential", credential: "CI_TOKEN"},
		{name: "inference credential", credential: "COPILOT_GITHUB_TOKEN"},
		{name: "MCP credential", credential: "GITHUB_MCP_SERVER_TOKEN"},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN", "CI_TOKEN", "COPILOT_GITHUB_TOKEN", "GITHUB_MCP_SERVER_TOKEN"} {
				t.Setenv(name, "")
			}
			if test.credential != "" {
				t.Setenv(test.credential, "synthetic-secret")
			}
			directory := t.TempDir()
			marker := filepath.Join(directory, "api-read")
			t.Setenv("NATIVE_READ_MARKER", marker)
			script := "#!/bin/sh\n[ \"$*\" = 'api --method GET repos/actions/checkout/tags?per_page=100&page=1' ] || exit 97\nprintf x > \"$NATIVE_READ_MARKER\"\nprintf '%s' '" + test.payload + "'\n"
			if err := os.WriteFile(filepath.Join(directory, "gh"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
			output, err := executeNativeCommand(t, []string{"native", "check-sandbox-read"})
			if test.accepted {
				var receipt map[string]string
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal([]byte(output), &receipt); err != nil || receipt["status"] != "verified" {
					t.Fatalf("missing release verification receipt: %q %v", output, err)
				}
			} else if err == nil || output != "" || strings.Contains(err.Error(), "synthetic-secret") {
				t.Fatalf("unsafe preflight result: output=%q error=%v", output, err)
			}
			if test.credential != "" {
				if _, err := os.Stat(marker); !os.IsNotExist(err) {
					t.Fatalf("API command ran with visible credentials: %v", err)
				}
			}
		})
	}
}
