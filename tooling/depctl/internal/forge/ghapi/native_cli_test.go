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

package ghapi

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestNativeCLIReadSandboxBoundary verifies read-only transport and JSON preservation.
func TestNativeCLIReadSandboxBoundary(t *testing.T) {
	const endpoint = "repos/actions/checkout/tags?per_page=100&page=1"
	for _, test := range []struct {
		name, endpoint, script, want, failure string
		pages, ci, invoked                    bool
	}{
		{name: "read-only JSON", endpoint: endpoint, script: `printf '%s' '{"sha":"release"}'`, want: `{"sha":"release"}`, invoked: true},
		{name: "single tag remains array", endpoint: endpoint, script: `printf '%s' '[{"name":"v7.0.1","commit":{"sha":"release"}}]'`, want: `[{"name":"v7.0.1","commit":{"sha":"release"}}]`, invoked: true},
		{name: "empty array preserved", endpoint: endpoint, script: `printf '%s' '[]'`, want: `[]`, invoked: true},
		{name: "multiple records preserved", endpoint: endpoint, script: `printf '%s' '[{"sha":"release"},{"sha":"other"}]'`, want: `[{"sha":"release"},{"sha":"other"}]`, invoked: true},
		{name: "nested collection preserved", endpoint: endpoint, script: `printf '%s' '[[{"sha":"release"}]]'`, want: `[[{"sha":"release"}]]`, invoked: true},
		{name: "whitespace preserved", endpoint: endpoint, script: `printf '  {"sha":"release"}\n'`, want: "  {\"sha\":\"release\"}\n", invoked: true},
		{name: "null evidence", endpoint: endpoint, script: `printf '%s' 'null'`, failure: "JSON object or array", invoked: true},
		{name: "null collection item preserved", endpoint: endpoint, script: `printf '%s' '[null]'`, want: `[null]`, invoked: true},
		{name: "scalar rejected", endpoint: endpoint, script: `printf '%s' '"release"'`, failure: "JSON object or array", invoked: true},
		{name: "pagination rejected", endpoint: endpoint, pages: true, failure: "does not support"},
		{name: "CI credentials rejected", endpoint: endpoint, ci: true, failure: "does not support"},
		{name: "absolute URL rejected", endpoint: "https://api.github.com/" + endpoint, failure: "relative repository"},
		{name: "fragment rejected", endpoint: endpoint + "#fragment", failure: "relative repository"},
		{name: "incomplete JSON rejected", endpoint: endpoint, script: `printf '%s' '[{"sha":"release"}'`, failure: "complete UTF-8 JSON", invoked: true},
		{name: "invalid UTF-8 rejected", endpoint: endpoint, script: `printf '{"sha":"\377"}'`, failure: "complete UTF-8 JSON", invoked: true},
		{name: "empty response rejected", endpoint: endpoint, script: `printf ''`, failure: "complete UTF-8 JSON", invoked: true},
		{name: "invalid JSON rejected", endpoint: endpoint, script: `printf '%s' '<html>denied</html>'`, failure: "complete UTF-8 JSON", invoked: true},
		{name: "proxy failure reason", endpoint: endpoint, script: "printf 'HTTP 403: proxy denied repository read\\n' >&2\nexit 17", failure: "HTTP 403: proxy denied repository read", invoked: true},
		{name: "empty stderr", endpoint: endpoint, script: "exit 17", failure: "exit status 17", invoked: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			marker := filepath.Join(directory, "called")
			t.Setenv("NATIVE_READ_MARKER", marker)
			t.Setenv("CI_TOKEN", "synthetic-ci-token")
			t.Setenv("GH_TOKEN", "synthetic-existing-token")
			script := "#!/bin/sh\nprintf x > \"$NATIVE_READ_MARKER\"\n[ \"$*\" = 'api --method GET " + endpoint + "' ] || exit 98\n[ \"$GH_TOKEN\" = 'synthetic-existing-token' ] || exit 99\n" + test.script + "\n"
			if err := os.WriteFile(filepath.Join(directory, "gh"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", directory)
			payload, err := NativeCLIRead(context.Background(), test.endpoint, test.pages, test.ci)
			if test.failure == "" {
				if err != nil || string(payload) != test.want {
					t.Fatalf("unexpected GET result: %s, %v", payload, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.failure) || len(payload) != 0 {
				t.Fatalf("expected %q without response payload, got %s, %v", test.failure, payload, err)
			}
			if strings.HasPrefix(test.script, "exit 17") || strings.Contains(test.script, "\nexit 17") {
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != 17 {
					t.Fatalf("wrapped process failure lost exit status: %v", err)
				}
			}
			_, markerErr := os.Stat(marker)
			if test.invoked && markerErr != nil || !test.invoked && !os.IsNotExist(markerErr) {
				t.Fatalf("unexpected sandbox process execution: %v", markerErr)
			}
		})
	}
}
