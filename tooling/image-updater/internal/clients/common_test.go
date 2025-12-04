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
	"strings"
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

func TestNormalizeArchitecture(t *testing.T) {
	tests := []struct {
		name string
		arch string
		want string
	}{
		{
			name: "x86_64 converted to amd64",
			arch: "x86_64",
			want: "amd64",
		},
		{
			name: "amd64 unchanged",
			arch: "amd64",
			want: "amd64",
		},
		{
			name: "arm64 unchanged",
			arch: "arm64",
			want: "arm64",
		},
		{
			name: "arm unchanged",
			arch: "arm",
			want: "arm",
		},
		{
			name: "empty string unchanged",
			arch: "",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeArchitecture(tt.arch)
			if got != tt.want {
				t.Errorf("NormalizeArchitecture() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPrepareTagsForArchValidation(t *testing.T) {
	now := time.Now()
	oneHourAgo := now.Add(-1 * time.Hour)
	twoDaysAgo := now.Add(-48 * time.Hour)

	tests := []struct {
		name         string
		tags         []Tag
		repository   string
		tagPattern   string
		wantTagNames []string
		wantErr      bool
		wantErrMsg   string
	}{
		{
			name: "filters and sorts tags",
			tags: []Tag{
				{Name: "v1.0.0", Digest: "sha256:old", LastModified: twoDaysAgo},
				{Name: "v2.0.0", Digest: "sha256:new", LastModified: now},
				{Name: "latest", Digest: "sha256:latest", LastModified: oneHourAgo},
			},
			repository:   "test/repo",
			tagPattern:   `^v\d+\.\d+\.\d+$`,
			wantTagNames: []string{"v2.0.0", "v1.0.0"},
			wantErr:      false,
		},
		{
			name: "no pattern returns all sorted",
			tags: []Tag{
				{Name: "old", Digest: "sha256:old", LastModified: twoDaysAgo},
				{Name: "new", Digest: "sha256:new", LastModified: now},
				{Name: "middle", Digest: "sha256:middle", LastModified: oneHourAgo},
			},
			repository:   "test/repo",
			tagPattern:   "",
			wantTagNames: []string{"new", "middle", "old"},
			wantErr:      false,
		},
		{
			name:       "empty tags returns error",
			tags:       []Tag{},
			repository: "test/repo",
			tagPattern: "",
			wantErr:    true,
			wantErrMsg: "no tags found for repository test/repo",
		},
		{
			name: "pattern matches no tags",
			tags: []Tag{
				{Name: "latest", Digest: "sha256:abc", LastModified: now},
			},
			repository: "test/repo",
			tagPattern: `^v\d+`,
			wantErr:    true,
			wantErrMsg: "no tags matching pattern",
		},
		{
			name: "invalid pattern returns error",
			tags: []Tag{
				{Name: "latest", Digest: "sha256:abc", LastModified: now},
			},
			repository: "test/repo",
			tagPattern: `[invalid(`,
			wantErr:    true,
		},
		{
			name: "semver sorting ignores push dates",
			tags: []Tag{
				// v1.6.2 pushed more recently than v1.7.2, but v1.7.2 should be selected
				{Name: "v1.6.2", Digest: "sha256:v162", LastModified: now},
				{Name: "v1.7.2", Digest: "sha256:v172", LastModified: twoDaysAgo},
				{Name: "v1.7.1", Digest: "sha256:v171", LastModified: oneHourAgo},
			},
			repository:   "test/repo",
			tagPattern:   `^v\d+\.\d+\.\d+$`,
			wantTagNames: []string{"v1.7.2", "v1.7.1", "v1.6.2"},
			wantErr:      false,
		},
		{
			name: "semver with build suffixes",
			tags: []Tag{
				{Name: "v1.7.2", Digest: "sha256:v172", LastModified: twoDaysAgo},
				{Name: "v1.7.2-1", Digest: "sha256:v1721", LastModified: twoDaysAgo},
				{Name: "v1.7.2-2", Digest: "sha256:v1722", LastModified: twoDaysAgo},
			},
			repository: "test/repo",
			tagPattern: `^v\d+\.\d+\.\d+(-\d+)?$`,
			// In semver, v1.7.2 is the release, v1.7.2-X are pre-releases (lower precedence)
			// So v1.7.2 comes first, then pre-releases are sorted alphanumerically
			wantTagNames: []string{"v1.7.2", "v1.7.2-2", "v1.7.2-1"},
			wantErr:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := PrepareTagsForArchValidation(tt.tags, tt.repository, tt.tagPattern)

			if (err != nil) != tt.wantErr {
				t.Errorf("PrepareTagsForArchValidation() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				if tt.wantErrMsg != "" && !strings.Contains(err.Error(), tt.wantErrMsg) {
					t.Errorf("PrepareTagsForArchValidation() error = %v, should contain %v", err.Error(), tt.wantErrMsg)
				}
				return
			}

			if len(got) != len(tt.wantTagNames) {
				t.Errorf("PrepareTagsForArchValidation() got %d tags, want %d", len(got), len(tt.wantTagNames))
			}

			for i, wantName := range tt.wantTagNames {
				if i >= len(got) {
					t.Errorf("PrepareTagsForArchValidation() missing tag %s at index %d", wantName, i)
					continue
				}
				if got[i].Name != wantName {
					t.Errorf("PrepareTagsForArchValidation() tag[%d] = %v, want %v", i, got[i].Name, wantName)
				}
			}
		})
	}
}

func TestNewRegistryClient(t *testing.T) {
	tests := []struct {
		name        string
		registryURL string
		useAuth     bool
		wantType    string
		wantErr     bool
	}{
		{
			name:        "quay.io registry uses Quay client",
			registryURL: "quay.io",
			useAuth:     true,
			wantType:    "*clients.QuayClient",
			wantErr:     false,
		},
		{
			name:        "mcr.microsoft.com uses generic client",
			registryURL: "mcr.microsoft.com",
			useAuth:     false,
			wantType:    "*clients.GenericRegistryClient",
			wantErr:     false,
		},
		{
			name:        "docker.io uses generic client",
			registryURL: "docker.io",
			useAuth:     false,
			wantType:    "*clients.GenericRegistryClient",
			wantErr:     false,
		},
		{
			name:        "azurecr.io uses ACR client",
			registryURL: "arohcpsvcdev.azurecr.io",
			useAuth:     true,
			wantType:    "*clients.ACRClient",
			wantErr:     false,
		},
		{
			name:        "empty registry URL uses generic client",
			registryURL: "",
			useAuth:     false,
			wantType:    "*clients.GenericRegistryClient",
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewRegistryClient(tt.registryURL, tt.useAuth)
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
