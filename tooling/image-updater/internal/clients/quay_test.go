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
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewQuayClient(t *testing.T) {
	client := NewQuayClient()

	if client == nil {
		t.Fatal("NewQuayClient() returned nil")
	}

	if client.baseURL != "https://quay.io/api/v1" {
		t.Errorf("NewQuayClient() baseURL = %v, want %v", client.baseURL, "https://quay.io/api/v1")
	}

	if client.httpClient == nil {
		t.Error("NewQuayClient() httpClient should not be nil")
	}
}

func TestQuayClient_getAllTags(t *testing.T) {
	tests := []struct {
		name         string
		repository   string
		responses    []struct {
			body       string
			statusCode int
		}
		wantTagCount int
		wantErr      bool
	}{
		{
			name:       "successful single page tag retrieval",
			repository: "test/repo",
			responses: []struct {
				body       string
				statusCode int
			}{
				{
					body: `{
						"tags": [
							{"name": "v1.0.0", "manifest_digest": "sha256:abc123", "last_modified": "Mon, 02 Jan 2006 15:04:05 -0700"},
							{"name": "v2.0.0", "manifest_digest": "sha256:def456", "last_modified": "Wed, 15 Nov 2023 10:30:00 +0000"}
						],
						"page": 1,
						"has_additional": false
					}`,
					statusCode: http.StatusOK,
				},
			},
			wantTagCount: 2,
			wantErr:      false,
		},
		{
			name:       "successful multi-page tag retrieval",
			repository: "test/largerepo",
			responses: []struct {
				body       string
				statusCode int
			}{
				{
					body: `{
						"tags": [
							{"name": "v1.0.0", "manifest_digest": "sha256:abc123", "last_modified": "Mon, 02 Jan 2006 15:04:05 -0700"}
						],
						"page": 1,
						"has_additional": true
					}`,
					statusCode: http.StatusOK,
				},
				{
					body: `{
						"tags": [
							{"name": "v2.0.0", "manifest_digest": "sha256:def456", "last_modified": "Wed, 15 Nov 2023 10:30:00 +0000"}
						],
						"page": 2,
						"has_additional": false
					}`,
					statusCode: http.StatusOK,
				},
			},
			wantTagCount: 2,
			wantErr:      false,
		},
		{
			name:       "empty tags list",
			repository: "test/empty",
			responses: []struct {
				body       string
				statusCode int
			}{
				{
					body:       `{"tags": [], "page": 1, "has_additional": false}`,
					statusCode: http.StatusOK,
				},
			},
			wantTagCount: 0,
			wantErr:      false,
		},
		{
			name:       "repository not found",
			repository: "test/notfound",
			responses: []struct {
				body       string
				statusCode int
			}{
				{
					body:       `{"error": "not found"}`,
					statusCode: http.StatusNotFound,
				},
			},
			wantTagCount: 0,
			wantErr:      true,
		},
		{
			name:       "invalid JSON response",
			repository: "test/invalid",
			responses: []struct {
				body       string
				statusCode int
			}{
				{
					body:       `invalid json`,
					statusCode: http.StatusOK,
				},
			},
			wantTagCount: 0,
			wantErr:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			responseIndex := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if responseIndex >= len(tt.responses) {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}

				resp := tt.responses[responseIndex]
				responseIndex++

				w.WriteHeader(resp.statusCode)
				w.Write([]byte(resp.body))
			}))
			defer server.Close()

			client := &QuayClient{
				httpClient: server.Client(),
				baseURL:    server.URL,
			}

			got, err := client.getAllTags(tt.repository)
			if (err != nil) != tt.wantErr {
				t.Errorf("getAllTags() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if len(got) != tt.wantTagCount {
					t.Errorf("getAllTags() got %d tags, want %d", len(got), tt.wantTagCount)
				}

				// Verify that digests are populated
				for _, tag := range got {
					if tag.Digest == "" {
						t.Errorf("getAllTags() tag %s has empty digest", tag.Name)
					}
				}
			}
		})
	}
}
