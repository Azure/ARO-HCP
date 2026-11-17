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

package ghapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestMaintenanceRequiresBothScopedTokens(t *testing.T) {
	for _, missing := range []string{"GH_TOKEN", "CI_TOKEN"} {
		t.Run(missing, func(t *testing.T) {
			t.Setenv("GH_TOKEN", "repository-token")
			t.Setenv("CI_TOKEN", "ci-token")
			t.Setenv("GITHUB_TOKEN", "fallback-must-not-authorize")
			t.Setenv(missing, "")
			if _, err := NewMaintenance("owner/repo"); err == nil {
				t.Fatal("missing scoped token accepted")
			}
		})
	}
}

func TestMaintenanceScopesCIReadsAndRepositoryCommentWrites(t *testing.T) {
	t.Setenv("GH_TOKEN", "repository-token")
	t.Setenv("CI_TOKEN", "ci-token")
	head := strings.Repeat("a", 40)
	seen := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		seen[path]++
		ci := strings.Contains(path, "/statuses") || strings.Contains(path, "/check-runs")
		token := "repository-token"
		if ci {
			token = "ci-token"
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Errorf("%s token=%q", path, got)
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "POST":
			if ci {
				t.Error("CI client attempted write")
			}
			var input struct {
				Body string `json:"body"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			if input.Body != "/hold cancel" {
				t.Errorf("body=%q", input.Body)
			}
			fmt.Fprintf(w, `{"id":42,"body":%q}`, input.Body)
		case path == "/repos/owner/repo/pulls/1":
			fmt.Fprintf(w, `{"number":1,"state":"open","draft":false,"merged":false,"body":"","title":"owned","labels":[],"user":{"login":"aro-hcp-robot[bot]"},"head":{"sha":%q,"ref":"branch","repo":{"full_name":"owner/repo"}},"base":{"ref":"main","repo":{"full_name":"owner/repo"}}}`, head)
		case strings.HasSuffix(path, "/commits"):
			fmt.Fprintf(w, `[{"sha":%q}]`, head)
		case strings.HasSuffix(path, "/check-runs"):
			fmt.Fprint(w, `{"check_runs":[]}`)
		default:
			fmt.Fprint(w, `[]`)
		}
	}))
	defer server.Close()
	reader, err := NewMaintenance("owner/repo")
	if err != nil {
		t.Fatal(err)
	}
	endpoint, _ := url.Parse(server.URL + "/")
	reader.client.BaseURL = endpoint
	reader.ciClient.BaseURL = endpoint
	if _, err := reader.Snapshot(context.Background(), 1, head); err != nil {
		t.Fatal(err)
	}
	id, err := reader.PostMaintenanceComment(context.Background(), 1, "/hold cancel")
	if err != nil || id != 42 {
		t.Fatalf("id=%d err=%v", id, err)
	}
	if seen["/repos/owner/repo/pulls/1"] != 2 {
		t.Fatal("snapshot did not re-read PR identity")
	}
}

func TestMaintenanceCommentMalformedOrFailedReceiptNeverRetries(t *testing.T) {
	for _, reply := range []string{`{}`, `{"id":1,"body":"wrong"}`, `{"id":0,"body":"/hold cancel"}`, `{"id":true,"body":"/hold cancel"}`, `not json`, `error`} {
		t.Run(reply, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				if reply == "error" {
					w.WriteHeader(503)
				}
				fmt.Fprint(w, reply)
			}))
			defer server.Close()
			t.Setenv("GH_TOKEN", "repository-token")
			t.Setenv("CI_TOKEN", "ci-token")
			reader, err := NewMaintenance("owner/repo")
			if err != nil {
				t.Fatal(err)
			}
			reader.client.BaseURL, _ = url.Parse(server.URL + "/")
			if _, err := reader.PostMaintenanceComment(context.Background(), 1, "/hold cancel"); err == nil || calls != 1 {
				t.Fatalf("err=%v calls=%d", err, calls)
			}
		})
	}
}
