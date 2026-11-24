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

func TestNativeSandboxReadChecksCredentialIsolationAndReleaseIdentity(t *testing.T) {
	const official = "3d3c42e5aac5ba805825da76410c181273ba90b1"
	const mismatch = "sandbox cannot verify the official checkout release"
	for _, test := range []struct {
		name, pin, credential string
		expectedError         string
		accepted, collection  bool
	}{
		{name: "proxy", pin: official, accepted: true},
		{name: "wrong immutable release", pin: strings.Repeat("f", 40), expectedError: mismatch},
		{name: "proxy collection", pin: official, accepted: true, collection: true},
		{name: "wrong collection release", pin: strings.Repeat("f", 40), collection: true, expectedError: mismatch},
		{name: "repository credential", pin: official, credential: "GH_TOKEN"},
		{name: "GitHub credential", pin: official, credential: "GITHUB_TOKEN"},
		{name: "CI credential", pin: official, credential: "CI_TOKEN"},
		{name: "inference credential", pin: official, credential: "COPILOT_GITHUB_TOKEN"},
		{name: "MCP credential", pin: official, credential: "GITHUB_MCP_SERVER_TOKEN"},
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
			payload := `{"sha":"` + test.pin + `"}`
			if test.collection {
				payload = "[" + payload + "]"
			}
			script := "#!/bin/sh\n[ \"$*\" = 'api --method GET repos/actions/checkout/commits/v7.0.1' ] || exit 97\nprintf x > \"$NATIVE_READ_MARKER\"\nprintf '%s' '" + payload + "'\n"
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
			if test.expectedError != "" && (err == nil || !strings.Contains(err.Error(), test.expectedError)) {
				t.Fatalf("want release identity rejection %q, got %v", test.expectedError, err)
			}
			if test.credential != "" {
				if _, err := os.Stat(marker); !os.IsNotExist(err) {
					t.Fatalf("API command ran with visible credentials: %v", err)
				}
			}
		})
	}
}
