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
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/tooling/depctl/pkg/depctl"
)

func TestNativePublishRejectsUnsafeInputsBeforeCredentials(t *testing.T) {
	for _, tc := range []struct {
		name       string
		repository string
		output     string
		missing    bool
	}{
		{name: "invalid repository", repository: "Azure/ARO-HCP/extra", output: `{"items":[]}`},
		{name: "malformed output", repository: "Azure/ARO-HCP", output: `{`},
		{name: "missing output", repository: "Azure/ARO-HCP", missing: true},
		{name: "missing artifact", repository: "Azure/ARO-HCP", output: `{"items":[{"type":"native_replacement","request_sha256":"` + strings.Repeat("0", 64) + `"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GH_TOKEN", "")
			t.Setenv("GITHUB_TOKEN", "")
			t.Setenv("CI_TOKEN", "")
			t.Setenv("PATH", t.TempDir())
			root := t.TempDir()
			outputFile := filepath.Join(root, "agent_output.json")
			if !tc.missing {
				if err := os.WriteFile(outputFile, []byte(tc.output), 0600); err != nil {
					t.Fatal(err)
				}
			}
			output, err := executeNativeCommand(t, []string{"native", "publish", "--repository", tc.repository, "--repo", root, "--output-file", outputFile})
			var exit *depctl.ExitError
			if !errors.As(err, &exit) || exit.Code != depctl.ExitInvalid {
				t.Fatalf("unsafe request must fail before authentication: output=%q error=%v", output, err)
			}
		})
	}
}

func TestNativePublishNoRequestNeedsNoCredentialsOrGit(t *testing.T) {
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("CI_TOKEN", "")
	t.Setenv("PATH", t.TempDir())
	root := t.TempDir()
	outputFile := filepath.Join(root, "agent_output.json")
	if err := os.WriteFile(outputFile, []byte(`{"items":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	output, err := executeNativeCommand(t, []string{"native", "publish", "--repository", "Azure/ARO-HCP", "--repo", root, "--output-file", outputFile})
	if err != nil {
		t.Fatalf("empty publication must succeed offline: %v; output=%s", err, output)
	}
	var receipt struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(output), &receipt); err != nil || receipt.Status != "no-request" {
		t.Fatalf("unexpected no-request receipt: %s, error=%v", output, err)
	}
}
