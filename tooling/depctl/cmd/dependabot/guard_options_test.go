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
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGuardOptionsValidate(t *testing.T) {
	for _, tc := range []struct {
		name  string
		opts  GuardOptions
		want  string
		valid bool
	}{
		{name: "missing inputs", opts: GuardOptions{}, want: "--repository OWNER/REPO and --agent-output FILE are required"},
		{name: "repository form", opts: GuardOptions{Repository: "ARO-HCP", AgentOutput: "out.json"}, want: "must be OWNER/REPO"},
		{name: "complete", opts: GuardOptions{Repository: "Azure/ARO-HCP", AgentOutput: "out.json"}, valid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			validated, err := tc.opts.Validate()
			if tc.valid {
				if err != nil || validated == nil {
					t.Fatalf("err = %v, want validated options", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if validated != nil {
				t.Fatal("a rejected invocation returned validated options")
			}
		})
	}
}

func TestRetestOptionsValidate(t *testing.T) {
	complete := RetestOptions{Repository: "Azure/ARO-HCP", PullRequest: 12, Head: strings.Repeat("a", 40), Evidence: "failed=u1;comparison=u2"}
	for _, tc := range []struct {
		name  string
		opts  RetestOptions
		want  string
		valid bool
	}{
		{name: "missing inputs", opts: RetestOptions{}, want: "--repository, --pull-request, --head and --evidence are required"},
		{name: "repository form", opts: RetestOptions{Repository: "ARO-HCP", PullRequest: 1, Head: "a", Evidence: "e"}, want: "must be OWNER/REPO"},
		{name: "complete", opts: complete, valid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			validated, err := tc.opts.Validate()
			if tc.valid {
				if err != nil || validated == nil {
					t.Fatalf("err = %v, want validated options", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestValidateProposalPrintsCanonicalForm(t *testing.T) {
	dir := t.TempDir()
	proposal := filepath.Join(dir, "proposal.json")
	content, err := json.Marshal(map[string]any{
		"data": map[string]any{
			"ecosystem":      "go",
			"package_family": "golang.org/x/net",
			"target_version": "v0.33.0",
			"manifests":      []any{"go.mod"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(proposal, content, 0o600); err != nil {
		t.Fatal(err)
	}
	out := &bytes.Buffer{}
	if err := runValidateProposal(proposal, out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"title"`) {
		t.Fatalf("canonical form has no title: %s", out.String())
	}
}

func TestValidateProposalRejectsAnEmptyPath(t *testing.T) {
	if err := runValidateProposal("", &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "exactly one proposal path") {
		t.Fatalf("err = %v, want a path requirement", err)
	}
}
