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

func TestFilterTagsByPattern(t *testing.T) {
	tests := []struct {
		name         string
		tags         []Tag
		tagPattern   string
		wantTagNames []string
		wantErr      bool
	}{
		{
			name: "empty pattern returns all tags",
			tags: []Tag{
				{Name: "v1.0.0", Digest: "sha256:abc"},
				{Name: "v2.0.0", Digest: "sha256:def"},
			},
			tagPattern:   "",
			wantTagNames: []string{"v1.0.0", "v2.0.0"},
			wantErr:      false,
		},
		{
			name: "valid pattern filters correctly",
			tags: []Tag{
				{Name: "v1.0.0", Digest: "sha256:abc"},
				{Name: "v2.0.0", Digest: "sha256:def"},
				{Name: "latest", Digest: "sha256:ghi"},
			},
			tagPattern:   `^v\d+\.\d+\.\d+$`,
			wantTagNames: []string{"v1.0.0", "v2.0.0"},
			wantErr:      false,
		},
		{
			name: "pattern matches no tags",
			tags: []Tag{
				{Name: "v1.0.0", Digest: "sha256:abc"},
				{Name: "v2.0.0", Digest: "sha256:def"},
			},
			tagPattern:   `^release-`,
			wantTagNames: []string{},
			wantErr:      false,
		},
		{
			name: "invalid regex pattern returns error",
			tags: []Tag{
				{Name: "v1.0.0", Digest: "sha256:abc"},
			},
			tagPattern:   `[invalid(`,
			wantTagNames: []string{},
			wantErr:      true,
		},
		{
			name: "pattern matches single tag",
			tags: []Tag{
				{Name: "v1.0.0", Digest: "sha256:abc"},
				{Name: "latest", Digest: "sha256:def"},
			},
			tagPattern:   `^latest$`,
			wantTagNames: []string{"latest"},
			wantErr:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FilterTagsByPattern(tt.tags, tt.tagPattern)
			if (err != nil) != tt.wantErr {
				t.Errorf("FilterTagsByPattern() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			// Check that we have exactly the expected tags
			if len(got) != len(tt.wantTagNames) {
				t.Errorf("FilterTagsByPattern() got %d tags, want %d", len(got), len(tt.wantTagNames))
			}

			for _, wantName := range tt.wantTagNames {
				found := false
				for _, tag := range got {
					if tag.Name == wantName {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("FilterTagsByPattern() missing tag %s", wantName)
				}
			}

			// Check we don't have unexpected tags
			for _, tag := range got {
				found := false
				for _, want := range tt.wantTagNames {
					if tag.Name == want {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("FilterTagsByPattern() has unexpected tag %s", tag.Name)
				}
			}
		})
	}
}

func TestProcessTags(t *testing.T) {
	now := time.Now()
	oneHourAgo := now.Add(-1 * time.Hour)
	twoDaysAgo := now.Add(-48 * time.Hour)
	oneWeekAgo := now.Add(-7 * 24 * time.Hour)

	tests := []struct {
		name       string
		tags       []Tag
		repository string
		tagPattern string
		wantDigest string
		wantErr    bool
		wantErrMsg string
	}{
		{
			name: "selects newest tag by date",
			tags: []Tag{
				{Name: "v1.0.0", Digest: "sha256:old", LastModified: oneWeekAgo},
				{Name: "v2.0.0", Digest: "sha256:newest", LastModified: now},
				{Name: "v1.5.0", Digest: "sha256:middle", LastModified: twoDaysAgo},
			},
			repository: "test/repo",
			tagPattern: "",
			wantDigest: "sha256:newest",
			wantErr:    false,
		},
		{
			name: "filters by pattern then selects newest",
			tags: []Tag{
				{Name: "v1.0.0", Digest: "sha256:old-v1", LastModified: oneWeekAgo},
				{Name: "v2.0.0", Digest: "sha256:new-v2", LastModified: now},
				{Name: "latest", Digest: "sha256:latest", LastModified: oneHourAgo},
			},
			repository: "test/repo",
			tagPattern: `^v\d+\.\d+\.\d+$`,
			wantDigest: "sha256:new-v2",
			wantErr:    false,
		},
		{
			name:       "empty tag list returns error",
			tags:       []Tag{},
			repository: "test/repo",
			tagPattern: "",
			wantErr:    true,
			wantErrMsg: "no valid tags found for repository test/repo",
		},
		{
			name: "pattern matches no tags returns error",
			tags: []Tag{
				{Name: "v1.0.0", Digest: "sha256:abc", LastModified: now},
			},
			repository: "test/repo",
			tagPattern: `^release-`,
			wantErr:    true,
			wantErrMsg: "no tags matching pattern ^release- found for repository test/repo",
		},
		{
			name: "invalid regex pattern returns error",
			tags: []Tag{
				{Name: "v1.0.0", Digest: "sha256:abc", LastModified: now},
			},
			repository: "test/repo",
			tagPattern: `[invalid(`,
			wantErr:    true,
		},
		{
			name: "sorts by date descending correctly",
			tags: []Tag{
				{Name: "oldest", Digest: "sha256:oldest", LastModified: oneWeekAgo},
				{Name: "middle", Digest: "sha256:middle", LastModified: twoDaysAgo},
				{Name: "newer", Digest: "sha256:newer", LastModified: oneHourAgo},
				{Name: "newest", Digest: "sha256:newest", LastModified: now},
			},
			repository: "test/repo",
			tagPattern: "",
			wantDigest: "sha256:newest",
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ProcessTags(tt.tags, tt.repository, tt.tagPattern)
			if (err != nil) != tt.wantErr {
				t.Errorf("ProcessTags() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				if tt.wantErrMsg != "" && err.Error() != tt.wantErrMsg {
					t.Errorf("ProcessTags() error message = %v, want %v", err.Error(), tt.wantErrMsg)
				}
				return
			}
			if got != tt.wantDigest {
				t.Errorf("ProcessTags() digest = %v, want %v", got, tt.wantDigest)
			}
		})
	}
}

func TestNewRegistryClient(t *testing.T) {
	tests := []struct {
		name        string
		registryURL string
		wantType    string
		wantErr     bool
	}{
		{
			name:        "quay.io registry",
			registryURL: "quay.io",
			wantType:    "*clients.QuayClient",
			wantErr:     false,
		},
		{
			name:        "quay.io with subdomain",
			registryURL: "registry.quay.io",
			wantType:    "*clients.QuayClient",
			wantErr:     false,
		},
		{
			name:        "unsupported registry",
			registryURL: "docker.io",
			wantType:    "",
			wantErr:     true,
		},
		{
			name:        "empty registry URL",
			registryURL: "",
			wantType:    "",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewRegistryClient(tt.registryURL)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewRegistryClient() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if got == nil {
					t.Errorf("NewRegistryClient() returned nil client for valid registry")
				}
			}
		})
	}
}
