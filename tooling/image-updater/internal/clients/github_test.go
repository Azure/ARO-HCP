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
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetLatestReleaseTag(t *testing.T) {
	tests := []struct {
		name       string
		response   string
		statusCode int
		wantTag    string
		wantErr    bool
		errContain string
	}{
		{
			name:       "strips v prefix",
			response:   `{"tag_name": "v1.28.3"}`,
			statusCode: http.StatusOK,
			wantTag:    "1.28.3",
		},
		{
			name:       "no v prefix",
			response:   `{"tag_name": "1.28.3"}`,
			statusCode: http.StatusOK,
			wantTag:    "1.28.3",
		},
		{
			name:       "non-200 status",
			response:   `{"message": "Not Found"}`,
			statusCode: http.StatusNotFound,
			wantErr:    true,
			errContain: "returned 404",
		},
		{
			name:       "empty tag_name",
			response:   `{"tag_name": ""}`,
			statusCode: http.StatusOK,
			wantErr:    true,
			errContain: "empty tag_name",
		},
		{
			name:       "malformed JSON",
			response:   `{not json`,
			statusCode: http.StatusOK,
			wantErr:    true,
			errContain: "decode",
		},
		{
			name:       "rate limited",
			response:   `{"message": "API rate limit exceeded"}`,
			statusCode: http.StatusForbidden,
			wantErr:    true,
			errContain: "returned 403",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/repos/istio/istio/releases/latest" {
					t.Errorf("unexpected path: %s", r.URL.Path)
				}
				if got := r.Header.Get("Accept"); got != "application/vnd.github.v3+json" {
					t.Errorf("unexpected Accept header: %s", got)
				}
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.response))
			}))
			defer srv.Close()

			// Override the API base for testing
			origBase := githubAPIBaseURL
			defer func() { setGitHubAPIBase(origBase) }()
			setGitHubAPIBase(srv.URL)

			tag, err := GetLatestReleaseTag(context.Background(), "istio/istio")
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errContain != "" {
					if got := err.Error(); !contains(got, tt.errContain) {
						t.Errorf("error %q should contain %q", got, tt.errContain)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tag != tt.wantTag {
				t.Errorf("got tag %q, want %q", tag, tt.wantTag)
			}
		})
	}
}

func TestGetLatestReleaseTag_AuthHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"tag_name": "v1.0.0"}`))
	}))
	defer srv.Close()

	origBase := githubAPIBaseURL
	defer func() { setGitHubAPIBase(origBase) }()
	setGitHubAPIBase(srv.URL)

	t.Run("with GITHUB_TOKEN", func(t *testing.T) {
		t.Setenv("GITHUB_TOKEN", "test-token-123")
		_, err := GetLatestReleaseTag(context.Background(), "owner/repo")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gotAuth != "Bearer test-token-123" {
			t.Errorf("got Authorization %q, want %q", gotAuth, "Bearer test-token-123")
		}
	})

	t.Run("without GITHUB_TOKEN", func(t *testing.T) {
		t.Setenv("GITHUB_TOKEN", "")
		_, err := GetLatestReleaseTag(context.Background(), "owner/repo")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gotAuth != "" {
			t.Errorf("got Authorization %q, want empty", gotAuth)
		}
	})
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
