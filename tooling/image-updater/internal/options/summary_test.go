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

package options

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testUpdatesJSON = `[
  {"name": "hypershift", "old_digest": "sha256:1111", "new_digest": "sha256:2222", "tag": "v1", "date": "2026-11-27", "status": "updated"},
  {"name": "thanos", "old_digest": "sha256:3333", "new_digest": "sha256:3333", "tag": "", "date": "2026-11-20", "status": "unchanged"}
]`

const testReceiptJSON = `{
  "Number": 42,
  "URL": "https://github.com/Azure/ARO-HCP/pull/42",
  "HeadSHA": "abc123"
}`

// writeSummaryFixture writes content to a temporary file and returns its path.
func writeSummaryFixture(t *testing.T, name, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write %s: %v", name, err)
	}
	return path
}

// validSummaryOptions returns options that pass validation.
func validSummaryOptions(t *testing.T) *RawSummaryOptions {
	t.Helper()

	return &RawSummaryOptions{
		UpdatesPath: writeSummaryFixture(t, "updates.json", testUpdatesJSON),
		ReceiptPath: writeSummaryFixture(t, "receipt.json", testReceiptJSON),
		OutputPath:  filepath.Join(t.TempDir(), "summary.html"),
		BodyPath:    filepath.Join(t.TempDir(), "body.md"),
		Revision:    "0123456789abcdef",
		Branch:      "main",
		Prebuilt:    true,
	}
}

// TestSummaryValidate covers the required input checks.
func TestSummaryValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*RawSummaryOptions)
		wantErr string
	}{
		{
			name:   "complete input",
			mutate: func(*RawSummaryOptions) {},
		},
		{
			name:    "updates are required",
			mutate:  func(o *RawSummaryOptions) { o.UpdatesPath = "" },
			wantErr: "--updates is required",
		},
		{
			name: "body only, before publishing",
			mutate: func(o *RawSummaryOptions) {
				o.ReceiptPath = ""
				o.OutputPath = ""
				o.Revision = ""
			},
		},
		{
			name: "no destination",
			mutate: func(o *RawSummaryOptions) {
				o.OutputPath = ""
				o.BodyPath = ""
				o.JSONPath = ""
			},
			wantErr: "one of --output, --json-output or --body-output is required",
		},
		{
			name: "revision is optional for a body-only run",
			mutate: func(o *RawSummaryOptions) {
				o.Revision = ""
				o.OutputPath = ""
			},
		},
		{
			name: "revision is required for the report",
			mutate: func(o *RawSummaryOptions) {
				o.Revision = ""
				o.BodyPath = ""
			},
			wantErr: "--revision is required with --output or --json-output",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw := validSummaryOptions(t)
			test.mutate(raw)

			_, err := raw.Validate(context.Background())
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate returned an error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Validate error %v does not contain %q", err, test.wantErr)
			}
		})
	}
}

// TestSummaryComplete covers the artifact parsing.
func TestSummaryComplete(t *testing.T) {
	tests := []struct {
		name       string
		updates    string
		receipt    string
		wantErr    string
		wantURL    string
		wantTotal  int
		wantUpdate int
	}{
		{
			name:       "valid artifacts",
			updates:    testUpdatesJSON,
			receipt:    testReceiptJSON,
			wantURL:    "https://github.com/Azure/ARO-HCP/pull/42",
			wantTotal:  2,
			wantUpdate: 1,
		},
		{
			name:    "invalid updates JSON",
			updates: "not json",
			receipt: testReceiptJSON,
			wantErr: "parse the update results",
		},
		{
			name:    "receipt without a URL",
			updates: testUpdatesJSON,
			receipt: `{"Number": 42}`,
			wantErr: "has no pull request URL",
		},
		{
			name:    "invalid receipt JSON",
			updates: testUpdatesJSON,
			receipt: "{",
			wantErr: "parse the publish receipt",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw := validSummaryOptions(t)
			raw.UpdatesPath = writeSummaryFixture(t, "updates.json", test.updates)
			raw.ReceiptPath = writeSummaryFixture(t, "receipt.json", test.receipt)

			validated, err := raw.Validate(context.Background())
			if err != nil {
				t.Fatalf("Validate returned an error: %v", err)
			}
			completed, err := validated.Complete(context.Background())
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("Complete error %v does not contain %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Complete returned an error: %v", err)
			}
			if completed.Report.PullRequest.URL != test.wantURL {
				t.Fatalf("pull request URL is %q", completed.Report.PullRequest.URL)
			}
			if len(completed.Report.Updates) != test.wantTotal {
				t.Fatalf("reported components is %d", len(completed.Report.Updates))
			}
			if completed.Report.UpdatedComponents != test.wantUpdate {
				t.Fatalf("updated components is %d", completed.Report.UpdatedComponents)
			}
		})
	}
}

// TestSummaryRun covers the written artifacts.
func TestSummaryRun(t *testing.T) {
	raw := validSummaryOptions(t)
	raw.JSONPath = filepath.Join(filepath.Dir(raw.OutputPath), "summary.json")

	validated, err := raw.Validate(context.Background())
	if err != nil {
		t.Fatalf("Validate returned an error: %v", err)
	}
	completed, err := validated.Complete(context.Background())
	if err != nil {
		t.Fatalf("Complete returned an error: %v", err)
	}
	if err := completed.Run(); err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}

	page, err := os.ReadFile(raw.OutputPath)
	if err != nil {
		t.Fatalf("failed to read the HTML summary: %v", err)
	}
	for _, want := range []string{"0123456789abcdef", "components changed: 1", "pull/42"} {
		if !strings.Contains(string(page), want) {
			t.Fatalf("HTML summary does not contain %q", want)
		}
	}

	body, err := os.ReadFile(raw.BodyPath)
	if err != nil {
		t.Fatalf("failed to read the pull request body: %v", err)
	}
	if !strings.Contains(string(body), "| hypershift |") {
		t.Fatalf("body does not contain the changed component: %s", body)
	}

	data, err := os.ReadFile(raw.JSONPath)
	if err != nil {
		t.Fatalf("failed to read the JSON summary: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("the JSON summary is not valid JSON: %v", err)
	}
	if decoded["revision"] != "0123456789abcdef" {
		t.Fatalf("JSON summary revision is %v", decoded["revision"])
	}
}

// TestSummaryCountsRehearsalRows checks that dry-run entries count as changes.
func TestSummaryCountsRehearsalRows(t *testing.T) {
	raw := validSummaryOptions(t)
	raw.UpdatesPath = writeSummaryFixture(t, "updates.json", `[
  {"name": "hypershift", "old_digest": "sha256:1111", "new_digest": "sha256:2222", "status": "dry-run"},
  {"name": "thanos", "old_digest": "sha256:3333", "new_digest": "sha256:3333", "status": "unchanged"}
]`)

	validated, err := raw.Validate(context.Background())
	if err != nil {
		t.Fatalf("Validate returned an error: %v", err)
	}
	completed, err := validated.Complete(context.Background())
	if err != nil {
		t.Fatalf("Complete returned an error: %v", err)
	}
	if completed.Report.UpdatedComponents != 1 {
		t.Fatalf("updated components is %d, want 1", completed.Report.UpdatedComponents)
	}
}

// TestSummaryBodyWithoutReceipt covers preparing the body before publishing.
func TestSummaryBodyWithoutReceipt(t *testing.T) {
	raw := &RawSummaryOptions{
		UpdatesPath: writeSummaryFixture(t, "updates.json", testUpdatesJSON),
		BodyPath:    filepath.Join(t.TempDir(), "body.md"),
	}

	validated, err := raw.Validate(context.Background())
	if err != nil {
		t.Fatalf("Validate returned an error: %v", err)
	}
	completed, err := validated.Complete(context.Background())
	if err != nil {
		t.Fatalf("Complete returned an error: %v", err)
	}
	if completed.Report.PullRequest.URL != "" {
		t.Fatalf("pull request URL is %q without a receipt", completed.Report.PullRequest.URL)
	}
	if err := completed.Run(); err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}

	body, err := os.ReadFile(raw.BodyPath)
	if err != nil {
		t.Fatalf("failed to read the pull request body: %v", err)
	}
	if !strings.Contains(string(body), "| hypershift |") {
		t.Fatalf("body does not contain the changed component: %s", body)
	}
}

// TestSummaryPullRequestURLWithoutReceipt covers linking the card before publishing.
func TestSummaryPullRequestURLWithoutReceipt(t *testing.T) {
	raw := &RawSummaryOptions{
		UpdatesPath:    writeSummaryFixture(t, "updates.json", testUpdatesJSON),
		OutputPath:     filepath.Join(t.TempDir(), "summary.html"),
		Revision:       "0123456789abcdef",
		PullRequestURL: "https://github.com/Azure/ARO-HCP/pull/8277",
	}

	validated, err := raw.Validate(context.Background())
	if err != nil {
		t.Fatalf("Validate returned an error: %v", err)
	}
	completed, err := validated.Complete(context.Background())
	if err != nil {
		t.Fatalf("Complete returned an error: %v", err)
	}
	if completed.Report.PullRequest.Number != 8277 {
		t.Fatalf("pull request number is %d", completed.Report.PullRequest.Number)
	}
	if err := completed.Run(); err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}

	page, err := os.ReadFile(raw.OutputPath)
	if err != nil {
		t.Fatalf("failed to read the HTML summary: %v", err)
	}
	if !strings.Contains(string(page), "https://github.com/Azure/ARO-HCP/pull/8277") {
		t.Fatalf("page does not link the pull request: %s", page)
	}
}

// TestWriteArtifactReplacesAtomically covers the artifact replacement.
func TestWriteArtifactReplacesAtomically(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "summary.html")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatalf("failed to seed the artifact: %v", err)
	}

	if err := writeArtifact(path, []byte("new")); err != nil {
		t.Fatalf("writeArtifact returned an error: %v", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read the artifact: %v", err)
	}
	if string(content) != "new" {
		t.Fatalf("artifact content is %q", content)
	}

	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("failed to read the directory: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("directory has %d entries, want only the artifact", len(entries))
	}
}
