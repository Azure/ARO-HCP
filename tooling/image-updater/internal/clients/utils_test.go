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

package clients

import (
	"testing"
	"time"
)

func TestIsValidArchitecture(t *testing.T) {
	tests := []struct {
		name         string
		architecture string
		want         bool
	}{
		{
			name:         "amd64 is valid",
			architecture: "amd64",
			want:         true,
		},
		{
			name:         "x86_64 is valid",
			architecture: "x86_64",
			want:         true,
		},
		{
			name:         "arm64 is invalid",
			architecture: "arm64",
			want:         false,
		},
		{
			name:         "arm is invalid",
			architecture: "arm",
			want:         false,
		},
		{
			name:         "ppc64le is invalid",
			architecture: "ppc64le",
			want:         false,
		},
		{
			name:         "s390x is invalid",
			architecture: "s390x",
			want:         false,
		},
		{
			name:         "empty string is invalid",
			architecture: "",
			want:         false,
		},
		{
			name:         "unknown architecture is invalid",
			architecture: "unknown",
			want:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsValidArchitecture(tt.architecture); got != tt.want {
				t.Errorf("IsValidArchitecture() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsContainerImageTag(t *testing.T) {
	tests := []struct {
		name    string
		tagName string
		want    bool
	}{
		// Valid container image tags
		{"latest tag", "latest", true},
		{"version tag", "v1.2.3", true},
		{"commit hash tag", "abc123def", true},
		{"branch tag", "main-20241016", true},
		{"numeric tag", "1.0", true},

		// Invalid metadata tags
		{"signature file", "sha256-abc123.sig", false},
		{"attestation file", "sha256-abc123.att", false},
		{"sbom file", "sha256-abc123.sbom", false},
		{"provenance file", "sha256-abc123.prov", false},
		{"vulnerability file", "sha256-abc123.vuln", false},
		{"uppercase signature", "SHA256-ABC123.SIG", false},
		{"mixed case attestation", "Sha256-Abc123.Att", false},

		// Edge cases
		{"tag with sig in name but not extension", "config-sig-v1", true},
		{"tag with att in name but not extension", "battery-v1", true},
		{"plain sha256 without extension", "sha256-abc123", true}, // This would be a valid tag name
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsContainerImageTag(tt.tagName); got != tt.want {
				t.Errorf("IsContainerImageTag(%q) = %v, want %v", tt.tagName, got, tt.want)
			}
		})
	}
}

func TestParseTimestamp(t *testing.T) {
	tests := []struct {
		name      string
		timestamp string
		wantErr   bool
	}{
		{
			name:      "invalid timestamp format",
			timestamp: "invalid-timestamp",
			wantErr:   true,
		},
		{
			name:      "empty timestamp",
			timestamp: "",
			wantErr:   true,
		},
		{
			name:      "RFC3339 format not supported",
			timestamp: "2023-01-01T12:00:00Z",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseTimestamp(tt.timestamp)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseTimestamp() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got.IsZero() {
				t.Errorf("ParseTimestamp() returned zero time for valid timestamp")
			}
		})
	}
}

func TestTagArchitectureFiltering(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name     string
		tags     []Tag
		wantTags []string
	}{
		{
			name: "filters out non-amd64 architectures",
			tags: []Tag{
				{Name: "v1.0.0", Digest: "sha256:amd64", Architecture: "amd64", LastModified: now},
				{Name: "v1.0.0-arm", Digest: "sha256:arm64", Architecture: "arm64", LastModified: now},
				{Name: "v1.0.0-x86", Digest: "sha256:x86_64", Architecture: "x86_64", LastModified: now},
				{Name: "v1.0.0-ppc", Digest: "sha256:ppc64le", Architecture: "ppc64le", LastModified: now},
			},
			wantTags: []string{"v1.0.0", "v1.0.0-x86"},
		},
		{
			name: "all amd64 tags pass through",
			tags: []Tag{
				{Name: "v1.0.0", Digest: "sha256:digest1", Architecture: "amd64", LastModified: now},
				{Name: "v2.0.0", Digest: "sha256:digest2", Architecture: "amd64", LastModified: now},
			},
			wantTags: []string{"v1.0.0", "v2.0.0"},
		},
		{
			name: "all non-amd64 tags filtered out",
			tags: []Tag{
				{Name: "v1.0.0-arm", Digest: "sha256:arm64", Architecture: "arm64", LastModified: now},
				{Name: "v1.0.0-ppc", Digest: "sha256:ppc64le", Architecture: "ppc64le", LastModified: now},
			},
			wantTags: []string{},
		},
		{
			name:     "empty tag list",
			tags:     []Tag{},
			wantTags: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Filter tags that would be accepted by IsValidArchitecture
			var validTags []Tag
			for _, tag := range tt.tags {
				if IsValidArchitecture(tag.Architecture) {
					validTags = append(validTags, tag)
				}
			}

			// Check that we got the expected tag names
			if len(validTags) != len(tt.wantTags) {
				t.Errorf("Got %d valid tags, want %d", len(validTags), len(tt.wantTags))
			}

			for _, wantName := range tt.wantTags {
				found := false
				for _, tag := range validTags {
					if tag.Name == wantName {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Expected tag %s not found in filtered results", wantName)
				}
			}
		})
	}
}
