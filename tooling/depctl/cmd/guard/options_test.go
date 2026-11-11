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

package guard

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/forge"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/policy"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/safeoutput"
	"github.com/Azure/ARO-HCP/tooling/depctl/pkg/depctl"
)

func writeArtifact(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agent-output.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func exitCode(t *testing.T, err error) int {
	t.Helper()
	var exit *depctl.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("error = %v, want an exit error", err)
	}
	return exit.Code
}

func TestValidateRequiresBothFlags(t *testing.T) {
	options := &RawOptions{AgentOutput: "artifact.json"}
	if _, err := options.Validate(context.Background()); exitCode(t, err) != depctl.ExitInvalid {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateRejectsAMalformedArtifact(t *testing.T) {
	path := writeArtifact(t, `{"items":{}}`)
	options := &RawOptions{AgentOutput: path, Repository: "Azure/ARO-HCP"}
	if _, err := options.Validate(context.Background()); exitCode(t, err) != depctl.ExitInvalid {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestCompleteRequiresAToken(t *testing.T) {
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	path := writeArtifact(t, `{"items":[]}`)
	validated, err := (&RawOptions{AgentOutput: path, Repository: "Azure/ARO-HCP"}).Validate(context.Background())
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if _, err := validated.Complete(context.Background()); exitCode(t, err) != depctl.ExitIncomplete {
		t.Fatalf("Complete() error = %v", err)
	}
}

func TestRunDefersWhileAnOwnedProposalIsOpen(t *testing.T) {
	path := writeArtifact(t, `{"items":[{"type":"create_pull_request","branch":"b","title":"t","body":"b"}]}`)
	validated, err := (&RawOptions{AgentOutput: path, Repository: "Azure/ARO-HCP"}).Validate(context.Background())
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	fake := forge.NewFake()
	fake.Seed(forge.PullRequest{
		Number: 42, Author: policy.ToolOwner, State: "open", BaseRef: policy.ToolBase,
		Title: policy.ToolTitlePrefix + "bump yq", Body: policy.ToolBodyMarker + " mikefarah/yq -->",
		Labels: []string{policy.ToolLabel},
	}, forge.Snapshot{})
	options := &CompletedOptions{ValidatedOptions: validated, reader: fake}
	options.out, options.err = os.Stdout, os.Stderr
	if err := options.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var written struct {
		Items []struct {
			Type string `json:"type"`
		} `json:"items"`
	}
	if err := json.Unmarshal(content, &written); err != nil {
		t.Fatal(err)
	}
	if len(written.Items) != 1 || written.Items[0].Type != "noop" {
		t.Fatalf("items = %+v", written.Items)
	}
}

type failingReader struct{}

func (failingReader) PullRequest(context.Context, int) (forge.PullRequest, error) {
	return forge.PullRequest{}, forge.ErrEvidenceIncomplete
}

func (failingReader) Snapshot(context.Context, int, string) (forge.Snapshot, error) {
	return forge.Snapshot{}, forge.ErrEvidenceIncomplete
}

func (failingReader) OpenPullRequests(context.Context, string) ([]forge.PullRequest, error) {
	return nil, forge.ErrEvidenceIncomplete
}

func (failingReader) BranchHead(context.Context, string) (string, bool, error) {
	return "", false, forge.ErrEvidenceIncomplete
}

func TestRunLeavesTheArtifactUntouchedWhenEvidenceIsIncomplete(t *testing.T) {
	original := `{"items":[{"type":"create_pull_request","branch":"b","title":"t","body":"b"}]}`
	path := writeArtifact(t, original)
	validated, err := (&RawOptions{AgentOutput: path, Repository: "Azure/ARO-HCP"}).Validate(context.Background())
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	options := &CompletedOptions{ValidatedOptions: validated, reader: failingReader{}}
	options.out, options.err = os.Stdout, os.Stderr
	if code := exitCode(t, options.Run(context.Background())); code != depctl.ExitIncomplete {
		t.Fatalf("Run() exit code = %d, want %d", code, depctl.ExitIncomplete)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != original {
		t.Fatalf("artifact changed on failure: %s", content)
	}
}

func TestGuardErrorUsesTheDocumentedCodes(t *testing.T) {
	cases := []struct {
		err  error
		code int
	}{
		{err: errors.New("write: no space left on device"), code: depctl.ExitInternal},
		{err: safeoutput.ErrInvalidArtifact, code: depctl.ExitInvalid},
		{err: forge.ErrEvidenceIncomplete, code: depctl.ExitIncomplete},
	}
	for _, testCase := range cases {
		if code := exitCode(t, guardError(testCase.err)); code != testCase.code {
			t.Fatalf("guardError(%v) = %d, want %d", testCase.err, code, testCase.code)
		}
	}
}
