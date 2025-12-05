// Copyright 2025 Microsoft Corporation
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

package output

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/tooling/image-updater/internal/yaml"
)

func TestPrintSummary(t *testing.T) {
	tests := []struct {
		name         string
		totalImages  int
		updatedCount int
		dryRun       bool
		wantContains []string
	}{
		{
			name:         "dry run with updates",
			totalImages:  5,
			updatedCount: 2,
			dryRun:       true,
			wantContains: []string{
				"Summary",
				"Total images checked: 5",
				"Updates available:    2",
				"dry-run",
				"No files were modified",
			},
		},
		{
			name:         "actual run with updates",
			totalImages:  5,
			updatedCount: 2,
			dryRun:       false,
			wantContains: []string{
				"Summary",
				"Total images checked: 5",
				"Images updated:       2",
			},
		},
		{
			name:         "no updates needed",
			totalImages:  5,
			updatedCount: 0,
			dryRun:       false,
			wantContains: []string{
				"Summary",
				"Total images checked: 5",
				"Images updated:       0",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			PrintSummary(&buf, tt.totalImages, tt.updatedCount, tt.dryRun)
			output := buf.String()

			for _, want := range tt.wantContains {
				if !strings.Contains(output, want) {
					t.Errorf("PrintSummary() output missing %q\nGot:\n%s", want, output)
				}
			}
		})
	}
}

func TestGenerateCommitMessage(t *testing.T) {
	tests := []struct {
		name         string
		updates      map[string][]yaml.Update
		wantContains []string
	}{
		{
			name: "single update with full digest",
			updates: map[string][]yaml.Update{
				"config.yaml": {
					{
						Name:      "frontend",
						OldDigest: "sha256:abc123def456",
						NewDigest: "sha256:789xyz012345",
						Tag:       "v1.2.3",
						Date:      "2025-12-05 10:30",
					},
				},
			},
			wantContains: []string{
				"Updated images in target config files:",
				"| Image | Old SHA | New SHA | Version | Timestamp |",
				"| frontend | abc123d… | 789xyz0… | v1.2.3 | 2025-12-05 10:30 |",
			},
		},
		{
			name: "single update without sha256 prefix",
			updates: map[string][]yaml.Update{
				"config.yaml": {
					{
						Name:      "backend",
						OldDigest: "abc123def456",
						NewDigest: "789xyz012345",
						Tag:       "v2.0.0",
						Date:      "2025-12-05 11:00",
					},
				},
			},
			wantContains: []string{
				"| backend | abc123d… | 789xyz0… | v2.0.0 | 2025-12-05 11:00 |",
			},
		},
		{
			name: "multiple updates",
			updates: map[string][]yaml.Update{
				"config.yaml": {
					{
						Name:      "frontend",
						OldDigest: "sha256:abc123",
						NewDigest: "sha256:def456",
						Tag:       "v1.0.0",
						Date:      "2025-12-05 10:00",
					},
					{
						Name:      "backend",
						OldDigest: "sha256:111222",
						NewDigest: "sha256:333444",
						Tag:       "v2.0.0",
						Date:      "2025-12-05 11:00",
					},
				},
			},
			wantContains: []string{
				"| frontend | abc123 | def456 | v1.0.0 | 2025-12-05 10:00 |",
				"| backend | 111222 | 333444 | v2.0.0 | 2025-12-05 11:00 |",
			},
		},
		{
			name: "update with missing metadata",
			updates: map[string][]yaml.Update{
				"config.yaml": {
					{
						Name:      "service",
						OldDigest: "sha256:old123",
						NewDigest: "sha256:new456",
						Tag:       "",
						Date:      "",
					},
				},
			},
			wantContains: []string{
				"| service | old123 | new456 | - | - |",
			},
		},
		{
			name: "deduplication - same image in multiple files",
			updates: map[string][]yaml.Update{
				"config1.yaml": {
					{
						Name:      "frontend",
						OldDigest: "sha256:abc123def",
						NewDigest: "sha256:def456ghi",
						Tag:       "v1.0.0",
						Date:      "2025-12-05 10:00",
					},
				},
				"config2.yaml": {
					{
						Name:      "frontend",
						OldDigest: "sha256:abc123def",
						NewDigest: "sha256:def456ghi",
						Tag:       "v1.0.0",
						Date:      "2025-12-05 10:00",
					},
				},
			},
			wantContains: []string{
				"| frontend | abc123d… | def456g… | v1.0.0 | 2025-12-05 10:00 |",
			},
		},
		{
			name: "short digests not truncated",
			updates: map[string][]yaml.Update{
				"config.yaml": {
					{
						Name:      "test",
						OldDigest: "abc",
						NewDigest: "def",
						Tag:       "v1.0.0",
						Date:      "2025-12-05 10:00",
					},
				},
			},
			wantContains: []string{
				"| test | abc | def | v1.0.0 | 2025-12-05 10:00 |",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GenerateCommitMessage(tt.updates)

			for _, want := range tt.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("GenerateCommitMessage() missing %q\nGot:\n%s", want, got)
				}
			}
		})
	}
}

func TestGenerateCommitMessage_Empty(t *testing.T) {
	tests := []struct {
		name    string
		updates map[string][]yaml.Update
	}{
		{
			name:    "nil updates",
			updates: nil,
		},
		{
			name:    "empty updates map",
			updates: map[string][]yaml.Update{},
		},
		{
			name: "empty updates slice",
			updates: map[string][]yaml.Update{
				"config.yaml": {},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GenerateCommitMessage(tt.updates)
			if got != "" {
				t.Errorf("GenerateCommitMessage() = %q, want empty string", got)
			}
		})
	}
}
