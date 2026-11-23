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

package prioritiespublish

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile writes one fixture file and returns its path.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	file := filepath.Join(dir, name)
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

// newRawOptions returns a complete flag set pointing at one change summary.
func newRawOptions(t *testing.T, files ...string) (*RawOptions, *[]string) {
	t.Helper()
	dir := t.TempDir()
	changes := map[string]any{"changes": []map[string]any{}}
	for _, file := range files {
		changes["changes"] = append(changes["changes"].([]map[string]any), map[string]any{"path": file})
	}
	encoded, err := json.Marshal(changes)
	if err != nil {
		t.Fatal(err)
	}
	calls := &[]string{}
	raw := DefaultRawOptions()
	raw.Repo = dir
	raw.Branch = "bot/test-priorities-1"
	raw.BodyPath = writeFile(t, dir, "body.md", "body")
	raw.Marker = "<!-- aro-hcp-test-priorities:v1 -->"
	raw.Changes = writeFile(t, dir, "changes.json", string(encoded))
	raw.DryRun = true
	return raw, calls
}

// completedFor runs the phases with a recording git and returns the options.
func completedFor(t *testing.T, raw *RawOptions, calls *[]string) (*completedOptions, error) {
	t.Helper()
	validated, err := raw.Validate()
	if err != nil {
		return nil, err
	}
	completed, err := validated.Complete(io.Discard)
	if err != nil {
		return nil, err
	}
	completed.git = func(_ context.Context, args ...string) error {
		*calls = append(*calls, strings.Join(args, " "))
		return nil
	}
	return completed, nil
}

func TestValidateRequiresPublicationInput(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*RawOptions)
		want string
	}{
		{name: "branch", edit: func(r *RawOptions) { r.Branch = "" }, want: "--branch"},
		{name: "body", edit: func(r *RawOptions) { r.BodyPath = "" }, want: "--body-file"},
		{name: "marker", edit: func(r *RawOptions) { r.Marker = "" }, want: "--marker"},
		{name: "changes", edit: func(r *RawOptions) { r.Changes = "" }, want: "--changes"},
		{name: "token", edit: func(r *RawOptions) { r.DryRun = false }, want: "--token-path"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := newRawOptions(t)
			tc.edit(raw)
			validated, err := raw.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if validated != nil {
				t.Fatal("a rejected invocation returned validated options")
			}
		})
	}
}

func TestValidateAcceptsACompleteInvocation(t *testing.T) {
	raw, _ := newRawOptions(t, "test/e2e/one.go")
	if _, err := raw.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestCompleteRejectsFilesOutsideTheAllowlist(t *testing.T) {
	raw, calls := newRawOptions(t, "test/e2e/one.go", "README.md")
	if _, err := completedFor(t, raw, calls); err == nil || !strings.Contains(err.Error(), "outside the refresh allowlist") {
		t.Fatalf("err = %v, want allowlist rejection", err)
	}
	if len(*calls) != 0 {
		t.Errorf("git ran despite the rejection: %v", *calls)
	}
}

func TestCompleteRejectsAnEmptyChangeSummary(t *testing.T) {
	raw, calls := newRawOptions(t)
	if _, err := completedFor(t, raw, calls); err == nil || !strings.Contains(err.Error(), "no files to publish") {
		t.Fatalf("err = %v, want empty summary rejection", err)
	}
}

func TestRunStagesOnlyReviewedPaths(t *testing.T) {
	raw, calls := newRawOptions(t, "test/e2e/one.go", "test/e2e/one.go", "test/testdata/zz_fixture_TestMainListSuitesForEachSuite_dev.txt")
	completed, err := completedFor(t, raw, calls)
	if err != nil {
		t.Fatal(err)
	}
	if err := completed.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(*calls, "\n")
	if !strings.Contains(joined, "add -- test/e2e/one.go test/testdata/zz_fixture_TestMainListSuitesForEachSuite_dev.txt") {
		t.Fatalf("staging is not limited to the reviewed paths:\n%s", joined)
	}
	if strings.Count(joined, "test/e2e/one.go") != 1 {
		t.Errorf("a duplicate path was staged twice:\n%s", joined)
	}
	if strings.Contains(joined, "add -A") || strings.Contains(joined, "add .") {
		t.Errorf("staging used a blanket add:\n%s", joined)
	}
	if !strings.Contains(joined, "user.name=aro-hcp-robot[bot]") {
		t.Errorf("the commit does not use the bot identity:\n%s", joined)
	}
}

func TestRunDryRunStopsBeforePublishing(t *testing.T) {
	raw, calls := newRawOptions(t, "test/e2e/one.go")
	validated, err := raw.Validate()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	completed, err := validated.Complete(&out)
	if err != nil {
		t.Fatal(err)
	}
	completed.git = func(_ context.Context, args ...string) error {
		*calls = append(*calls, strings.Join(args, " "))
		return nil
	}
	if err := completed.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Dry run") {
		t.Errorf("missing dry-run report: %q", out.String())
	}
	if strings.Contains(strings.Join(*calls, " "), "push") {
		t.Errorf("a dry run pushed: %v", *calls)
	}
}

func TestNewPublicationClientBindsTheActor(t *testing.T) {
	client, err := newPublicationClient("Azure/ARO-HCP", "token", "aro-hcp-robot[bot]")
	if err != nil {
		t.Fatal(err)
	}
	actor, err := client.Actor(context.Background())
	if err != nil {
		t.Fatalf("Actor queried the API instead of using the bound identity: %v", err)
	}
	if actor != "aro-hcp-robot[bot]" {
		t.Fatalf("actor = %q, want the bound login", actor)
	}
}
