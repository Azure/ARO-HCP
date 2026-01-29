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
	"encoding/json"
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/tooling/image-updater/internal/yaml"
)

// TestFormatResults tests the main formatting function with various inputs and formats
func TestFormatResults(t *testing.T) {
	tests := []struct {
		name         string
		updates      map[string][]yaml.Update
		format       string
		dryRun       bool
		wantErr      bool
		wantEmpty    bool
		wantContains []string
	}{
		{
			name: "table format with updates",
			updates: map[string][]yaml.Update{
				"config.yaml": {
					{
						Name:      "frontend",
						OldDigest: "sha256:abc123def456789",
						NewDigest: "sha256:xyz789012345678",
						Tag:       "v1.2.3",
						Date:      "2025-12-17 10:30",
					},
				},
			},
			format:    "table",
			dryRun:    false,
			wantErr:   false,
			wantEmpty: false,
			wantContains: []string{
				"NAME",
				"OLD DIGEST",
				"NEW DIGEST",
				"TAG",
				"DATE",
				"STATUS",
				"frontend",
				"abc123def456",
				"xyz789012345",
				"v1.2.3",
				"2025-12-17 10:30",
				"updated",
			},
		},
		{
			name: "markdown format with updates",
			updates: map[string][]yaml.Update{
				"config.yaml": {
					{
						Name:      "backend",
						OldDigest: "sha256:old123456789",
						NewDigest: "sha256:new987654321",
						Tag:       "v2.0.0",
						Date:      "2025-12-17 14:45",
					},
				},
			},
			format:    "markdown",
			dryRun:    false,
			wantErr:   false,
			wantEmpty: false,
			wantContains: []string{
				"| Name | Old Digest | New Digest | Tag | Date | Status |",
				"| --- | --- | --- | --- | --- | --- |",
				"| backend |",
				"old123456789",
				"new987654321",
				"v2.0.0",
				"2025-12-17 14:45",
				"updated",
			},
		},
		{
			name: "json format with updates",
			updates: map[string][]yaml.Update{
				"config.yaml": {
					{
						Name:      "service",
						OldDigest: "sha256:olddigest",
						NewDigest: "sha256:newdigest",
						Tag:       "v3.0.0",
						Date:      "2025-12-17 16:00",
					},
				},
			},
			format:    "json",
			dryRun:    false,
			wantErr:   false,
			wantEmpty: false,
			wantContains: []string{
				`"name": "service"`,
				`"old_digest": "sha256:olddigest"`,
				`"new_digest": "sha256:newdigest"`,
				`"tag": "v3.0.0"`,
				`"date": "2025-12-17 16:00"`,
				`"status": "updated"`,
			},
		},
		{
			name: "dry-run mode sets correct status",
			updates: map[string][]yaml.Update{
				"config.yaml": {
					{
						Name:      "dryrun-test",
						OldDigest: "sha256:old",
						NewDigest: "sha256:new",
						Tag:       "v1.0.0",
						Date:      "",
					},
				},
			},
			format:    "json",
			dryRun:    true,
			wantErr:   false,
			wantEmpty: false,
			wantContains: []string{
				`"status": "dry-run"`,
			},
		},
		{
			name:      "empty updates returns empty string",
			updates:   map[string][]yaml.Update{},
			format:    "table",
			dryRun:    false,
			wantErr:   false,
			wantEmpty: true,
		},
		{
			name:      "nil updates returns error",
			updates:   nil,
			format:    "table",
			dryRun:    false,
			wantErr:   true,
			wantEmpty: false,
		},
		{
			name: "unsupported format returns error",
			updates: map[string][]yaml.Update{
				"config.yaml": {
					{Name: "test", OldDigest: "old", NewDigest: "new"},
				},
			},
			format:  "xml",
			dryRun:  false,
			wantErr: true,
		},
		{
			name: "multiple updates across files are deduplicated",
			updates: map[string][]yaml.Update{
				"config1.yaml": {
					{
						Name:      "frontend",
						OldDigest: "sha256:old1",
						NewDigest: "sha256:new1",
						Tag:       "v1.0.0",
					},
				},
				"config2.yaml": {
					{
						Name:      "frontend", // Duplicate name
						OldDigest: "sha256:old2",
						NewDigest: "sha256:new2",
						Tag:       "v1.0.1",
					},
					{
						Name:      "backend",
						OldDigest: "sha256:old3",
						NewDigest: "sha256:new3",
						Tag:       "v2.0.0",
					},
				},
			},
			format:    "json",
			dryRun:    false,
			wantErr:   false,
			wantEmpty: false,
			// Should only have 2 results (frontend once, backend once)
			wantContains: []string{
				`"name": "frontend"`,
				`"name": "backend"`,
			},
		},
		{
			name: "empty tag and date show as dash in table",
			updates: map[string][]yaml.Update{
				"config.yaml": {
					{
						Name:      "no-metadata",
						OldDigest: "sha256:old",
						NewDigest: "sha256:new",
						Tag:       "",
						Date:      "",
					},
				},
			},
			format:    "table",
			dryRun:    false,
			wantErr:   false,
			wantEmpty: false,
			wantContains: []string{
				"no-metadata",
				"-", // For empty tag and date
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := FormatResults(tt.updates, tt.format, tt.dryRun)

			// Check error expectation
			if (err != nil) != tt.wantErr {
				t.Errorf("FormatResults() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			// Check empty expectation
			if tt.wantEmpty && result != "" {
				t.Errorf("FormatResults() expected empty result, got: %s", result)
				return
			}

			// Check content expectations
			for _, want := range tt.wantContains {
				if !strings.Contains(result, want) {
					t.Errorf("FormatResults() result missing expected string %q\nGot:\n%s", want, result)
				}
			}

			// For JSON format, validate it's valid JSON
			if tt.format == "json" && !tt.wantErr && result != "" {
				var parsed []UpdateResult
				if err := json.Unmarshal([]byte(result), &parsed); err != nil {
					t.Errorf("FormatResults() produced invalid JSON: %v\nJSON:\n%s", err, result)
				}
			}
		})
	}
}

// TestTruncateDigest tests digest truncation logic
func TestTruncateDigest(t *testing.T) {
	tests := []struct {
		name   string
		digest string
		length int
		want   string
	}{
		{
			name:   "digest with sha256 prefix longer than length",
			digest: "sha256:abc123def456789",
			length: 7,
			want:   "abc123d…",
		},
		{
			name:   "digest without prefix longer than length",
			digest: "xyz789012345678",
			length: 10,
			want:   "xyz7890123…",
		},
		{
			name:   "digest shorter than length",
			digest: "sha256:short",
			length: 20,
			want:   "short",
		},
		{
			name:   "empty digest",
			digest: "",
			length: 10,
			want:   "",
		},
		{
			name:   "digest exactly at length",
			digest: "sha256:exact12",
			length: 7,
			want:   "exact12",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateDigest(tt.digest, tt.length)
			if got != tt.want {
				t.Errorf("truncateDigest() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestValueOrDefault tests the helper function
func TestValueOrDefault(t *testing.T) {
	tests := []struct {
		name         string
		value        string
		defaultValue string
		want         string
	}{
		{
			name:         "non-empty value returns value",
			value:        "test",
			defaultValue: "default",
			want:         "test",
		},
		{
			name:         "empty value returns default",
			value:        "",
			defaultValue: "default",
			want:         "default",
		},
		{
			name:         "whitespace is not empty",
			value:        " ",
			defaultValue: "default",
			want:         " ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := valueOrDefault(tt.value, tt.defaultValue)
			if got != tt.want {
				t.Errorf("valueOrDefault() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestConvertToResults tests the conversion and deduplication logic
func TestConvertToResults(t *testing.T) {
	tests := []struct {
		name       string
		updates    map[string][]yaml.Update
		dryRun     bool
		wantCount  int
		wantStatus string
	}{
		{
			name: "deduplicates by name",
			updates: map[string][]yaml.Update{
				"file1": {
					{Name: "frontend", OldDigest: "old1", NewDigest: "new1"},
				},
				"file2": {
					{Name: "frontend", OldDigest: "old2", NewDigest: "new2"}, // Duplicate
					{Name: "backend", OldDigest: "old3", NewDigest: "new3"},
				},
			},
			dryRun:    false,
			wantCount: 2, // Only frontend and backend, not duplicated frontend
		},
		{
			name: "sets dry-run status when dryRun is true and digests differ",
			updates: map[string][]yaml.Update{
				"file": {
					{Name: "service", OldDigest: "old", NewDigest: "new"},
				},
			},
			dryRun:     true,
			wantCount:  1,
			wantStatus: "dry-run",
		},
		{
			name: "sets updated status when dryRun is false and digests differ",
			updates: map[string][]yaml.Update{
				"file": {
					{Name: "service", OldDigest: "old", NewDigest: "new"},
				},
			},
			dryRun:     false,
			wantCount:  1,
			wantStatus: "updated",
		},
		{
			name: "sets unchanged status when digests match",
			updates: map[string][]yaml.Update{
				"file": {
					{Name: "service", OldDigest: "same", NewDigest: "same"},
				},
			},
			dryRun:     false,
			wantCount:  1,
			wantStatus: "unchanged",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results := convertToResults(tt.updates, tt.dryRun)

			if len(results) != tt.wantCount {
				t.Errorf("convertToResults() returned %d results, want %d", len(results), tt.wantCount)
			}

			if tt.wantStatus != "" && len(results) > 0 {
				if results[0].Status != tt.wantStatus {
					t.Errorf("convertToResults() status = %v, want %v", results[0].Status, tt.wantStatus)
				}
			}
		})
	}
}
