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
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/tooling/depctl/pkg/depctl"
)

// TestNativeReconcileValidation exercises the command's offline and credential boundaries.
func TestNativeReconcileValidation(t *testing.T) {
	const validRequest = `{"type":"native_action","request":"{\"number\":7,\"expected_head_sha\":\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\",\"action\":\"admit\"}"}`
	for _, tc := range []struct {
		name       string
		repository string
		envelope   string
		noFlag     bool
		invalid    bool
		writeError bool
	}{
		{name: "invalid repository", repository: "Azure/ARO-HCP/extra", envelope: `{"items":[]}`, invalid: true},
		{name: "missing output flag", noFlag: true, invalid: true},
		{name: "missing output file", invalid: true},
		{name: "malformed envelope", envelope: `{`, invalid: true},
		{name: "agent errors", envelope: `{"items":[],"errors":["agent failed"]}`, invalid: true},
		{name: "invalid action", envelope: `{"items":[` + strings.Replace(validRequest, "admit", "merge", 1) + `]}`, invalid: true},
		{name: "multiple native outputs", envelope: `{"items":[` + validRequest + `,` + validRequest + `]}`, invalid: true},
		{name: "CI credential cannot authorize a valid action", envelope: `{"items":[` + validRequest + `]}`, writeError: true},
		{name: "empty output succeeds without a write credential", envelope: `{"items":[]}`},
		{name: "unrelated output succeeds without a write credential", envelope: `{"items":[{"type":"noop"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GH_TOKEN", "")
			t.Setenv("GITHUB_TOKEN", "")
			t.Setenv("CI_TOKEN", "")
			if tc.writeError {
				t.Setenv("CI_TOKEN", "ci-only")
			}
			dir := t.TempDir()
			t.Setenv("PATH", dir)
			path := filepath.Join(dir, "agent_output.json")
			if tc.envelope != "" {
				if err := os.WriteFile(path, []byte(tc.envelope), 0600); err != nil {
					t.Fatal(err)
				}
			}
			repository := tc.repository
			if repository == "" {
				repository = "Azure/ARO-HCP"
			}
			args := []string{"native", "reconcile", "--repository", repository}
			if !tc.noFlag {
				args = append(args, "--output-file", path)
			}
			output, err := executeNativeCommand(t, args)
			switch {
			case tc.invalid:
				var exit *depctl.ExitError
				if !errors.As(err, &exit) || exit.Code != depctl.ExitInvalid || output != "" {
					t.Fatalf("expected validation failure with no receipt; output=%q, error=%v", output, err)
				}
			case tc.writeError:
				if err == nil || !strings.Contains(err.Error(), "requires a GitHub write credential") || output != "" {
					t.Fatalf("expected missing write credential with no receipt; output=%q, error=%v", output, err)
				}
			default:
				if err != nil || output != "{\"status\":\"no-request\"}\n" {
					t.Fatalf("expected offline no-request receipt; output=%q, error=%v", output, err)
				}
			}
		})
	}
}
