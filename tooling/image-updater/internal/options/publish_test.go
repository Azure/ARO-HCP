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
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testBaseCommit = "dddddddddddddddddddddddddddddddddddddddd"

// validPublishOptions returns raw options that pass validation.
func validPublishOptions() *RawPublishOptions {
	raw := DefaultPublishOptions()
	raw.BodyPath = "body.md"
	raw.Paths = []string{"tooling/image-updater/config.yaml"}
	raw.BaseCommit = testBaseCommit
	raw.TokenPath = "/tmp/token"
	return raw
}

// TestRawPublishOptionsValidate covers the validation boundary.
func TestRawPublishOptionsValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*RawPublishOptions)
		wantErr string
	}{
		{
			name:   "live publication with a token file",
			mutate: func(*RawPublishOptions) {},
		},
		{
			name: "dry run needs no credentials or base commit",
			mutate: func(o *RawPublishOptions) {
				o.DryRun = true
				o.TokenPath = ""
				o.BaseCommit = ""
			},
		},
		{
			name:    "body is required",
			mutate:  func(o *RawPublishOptions) { o.BodyPath = "" },
			wantErr: "--body-file is required",
		},
		{
			name:    "marker must be an HTML comment",
			mutate:  func(o *RawPublishOptions) { o.Marker = "not-a-marker" },
			wantErr: "--marker must be one standalone HTML comment",
		},
		{
			name:    "head repository must be owner/name",
			mutate:  func(o *RawPublishOptions) { o.HeadRepo = "ARO-HCP" },
			wantErr: "--head-repo must be owner/name",
		},
		{
			name:    "base and branch must differ",
			mutate:  func(o *RawPublishOptions) { o.Branch = "main" },
			wantErr: "--base and --branch must differ",
		},
		{
			name:    "publishable paths are required",
			mutate:  func(o *RawPublishOptions) { o.Paths = nil },
			wantErr: "--paths or --paths-file is required",
		},
		{
			name:    "live publication pins the preparation base",
			mutate:  func(o *RawPublishOptions) { o.BaseCommit = "" },
			wantErr: "--base-commit must be a full commit SHA",
		},
		{
			name:    "the preparation base is a full SHA",
			mutate:  func(o *RawPublishOptions) { o.BaseCommit = "abc123" },
			wantErr: "--base-commit must be a full commit SHA",
		},
		{
			name: "credentials are required without a dry run",
			mutate: func(o *RawPublishOptions) {
				o.TokenPath = ""
			},
			wantErr: "--token-file or the App credential files are required unless --dry-run",
		},
		{
			name: "App credentials are required together",
			mutate: func(o *RawPublishOptions) {
				o.TokenPath = ""
				o.AppIDPath = "/tmp/id"
			},
			wantErr: "--app-id-file and --app-key-file are required together",
		},
		{
			name: "App files satisfy credentials",
			mutate: func(o *RawPublishOptions) {
				o.TokenPath = ""
				o.AppIDPath = "/tmp/id"
				o.AppKeyPath = "/tmp/key"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw := validPublishOptions()
			test.mutate(raw)
			_, err := raw.Validate()
			switch {
			case test.wantErr == "" && err != nil:
				t.Fatalf("Validate() error = %v, want none", err)
			case test.wantErr != "" && err == nil:
				t.Fatalf("Validate() error = nil, want %q", test.wantErr)
			case test.wantErr != "" && !strings.Contains(err.Error(), test.wantErr):
				t.Fatalf("Validate() error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

// TestValidatedPublishOptionsCompleteReadsLocalInput covers completion without credentials.
func TestValidatedPublishOptionsCompleteReadsLocalInput(t *testing.T) {
	dir := t.TempDir()
	bodyPath := filepath.Join(dir, "body.md")
	if err := os.WriteFile(bodyPath, []byte("digest summary"), 0o600); err != nil {
		t.Fatalf("write body: %v", err)
	}
	pathsPath := filepath.Join(dir, "paths.txt")
	if err := os.WriteFile(pathsPath, []byte("tooling/image-updater/config.yaml\nbackend/zz_fixture.yaml\ntooling/image-updater/config.yaml\n"), 0o600); err != nil {
		t.Fatalf("write paths: %v", err)
	}
	raw := DefaultPublishOptions()
	raw.BodyPath = bodyPath
	raw.PathsFile = pathsPath
	raw.Paths = []string{"frontend/zz_fixture.yaml"}
	raw.DryRun = true
	validated, err := raw.Validate()
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	completed, err := validated.Complete(context.Background())
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	input := completed.Input()
	if string(input.Body) != "digest summary" {
		t.Errorf("Body = %q, want the body file contents", input.Body)
	}
	want := []string{"frontend/zz_fixture.yaml", "tooling/image-updater/config.yaml", "backend/zz_fixture.yaml"}
	if len(input.Paths) != len(want) {
		t.Fatalf("Paths = %v, want %v", input.Paths, want)
	}
	for i, path := range want {
		if input.Paths[i] != path {
			t.Errorf("Paths[%d] = %q, want %q", i, input.Paths[i], path)
		}
	}
	if input.Branch != "bot/image-updater" || input.Marker != "<!-- aro-hcp-image-updater -->" {
		t.Errorf("completed options did not keep the rolling defaults: %+v", input)
	}
}

// TestCompleteRejectsEscapingPaths covers path normalization.
func TestCompleteRejectsEscapingPaths(t *testing.T) {
	dir := t.TempDir()
	bodyPath := filepath.Join(dir, "body.md")
	if err := os.WriteFile(bodyPath, []byte("body"), 0o600); err != nil {
		t.Fatalf("write body: %v", err)
	}
	raw := DefaultPublishOptions()
	raw.BodyPath = bodyPath
	raw.Paths = []string{"../escape.yaml"}
	raw.DryRun = true
	validated, err := raw.Validate()
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if _, err := validated.Complete(context.Background()); err == nil || !strings.Contains(err.Error(), "repository-relative") {
		t.Fatalf("Complete() error = %v, want a path refusal", err)
	}
}
