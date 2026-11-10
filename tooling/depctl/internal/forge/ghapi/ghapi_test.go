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
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-github/v69/github"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/forge"
)

// newTestReader points a reader at a local API server.
func newTestReader(t *testing.T, handler http.Handler) *Reader {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := github.NewClient(server.Client())
	client.BaseURL = mustParse(t, server.URL+"/")
	reader, err := NewWithClient("owner/repo", client)
	if err != nil {
		t.Fatalf("NewWithClient() error = %v", err)
	}
	return reader
}

func mustParse(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return parsed
}

func TestSnapshotCollectsEveryCollectionAndFiltersHolds(t *testing.T) {
	commentPage := 0
	reader := newTestReader(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/repos/owner/repo/pulls/7":
			fmt.Fprint(writer, `{"number":7,"state":"open","title":"t","user":{"login":"bot"},
				"head":{"sha":"h1","ref":"branch","repo":{"full_name":"owner/repo"}},
				"base":{"ref":"main","repo":{"full_name":"owner/repo"}},"labels":[{"name":"dependencies"}]}`)
		case "/repos/owner/repo/pulls/7/commits":
			fmt.Fprint(writer, `[{"sha":"c1"}]`)
		case "/repos/owner/repo/issues/7/comments":
			commentPage++
			if commentPage == 1 {
				writer.Header().Set("Link", fmt.Sprintf(`<http://%s/repos/owner/repo/issues/7/comments?page=2>; rel="next"`, request.Host))
				fmt.Fprint(writer, `[{"id":1,"body":"first","user":{"login":"a"},"created_at":"2026-01-01T00:00:00Z"}]`)
				return
			}
			fmt.Fprint(writer, `[{"id":2,"body":"second","user":{"login":"b"},"created_at":"2026-01-02T00:00:00Z"}]`)
		case "/repos/owner/repo/issues/7/events":
			fmt.Fprint(writer, `[{"id":10,"event":"labeled","label":{"name":"do-not-merge/hold"},"actor":{"login":"a"},"created_at":"2026-01-03T00:00:00Z"},
				{"id":11,"event":"labeled","label":{"name":"dependencies"},"actor":{"login":"a"}},
				{"id":12,"event":"unlabeled","label":{"name":"do-not-merge/hold"},"actor":{"login":"b"},"created_at":"2026-01-04T00:00:00Z"}]`)
		case "/repos/owner/repo/commits/h1/statuses":
			fmt.Fprint(writer, `[{"id":20,"context":"ci/prow/e2e","state":"failure","target_url":"https://x","created_at":"2026-01-05T00:00:00Z"}]`)
		case "/repos/owner/repo/commits/h1/check-runs":
			fmt.Fprint(writer, `{"total_count":1,"check_runs":[{"id":30,"name":"job","head_sha":"h1","status":"completed","conclusion":"success","details_url":"https://y"}]}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	snapshot, err := reader.Snapshot(context.Background(), 7, "h1")
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if len(snapshot.Comments) != 2 {
		t.Fatalf("comments = %d, want 2 across pages", len(snapshot.Comments))
	}
	if got := snapshot.Comments[1].CreatedAt; got != "2026-01-02T00:00:00Z" {
		t.Fatalf("comment timestamp = %q", got)
	}
	if len(snapshot.Commits) != 1 || snapshot.Commits[0] != "c1" {
		t.Fatalf("commits = %v", snapshot.Commits)
	}
	if len(snapshot.HoldEvents) != 2 || snapshot.HoldEvents[0].Event != "labeled" {
		t.Fatalf("hold events = %+v", snapshot.HoldEvents)
	}
	if len(snapshot.Statuses) != 1 || snapshot.Statuses[0].State != "failure" {
		t.Fatalf("statuses = %+v", snapshot.Statuses)
	}
	if len(snapshot.Checks) != 1 || snapshot.Checks[0].Conclusion != "success" {
		t.Fatalf("checks = %+v", snapshot.Checks)
	}
	if len(snapshot.Pull.Labels) != 1 || snapshot.Pull.Labels[0] != "dependencies" {
		t.Fatalf("labels = %v", snapshot.Pull.Labels)
	}
}

func TestSnapshotRefusesAMovedHead(t *testing.T) {
	pullReads := 0
	reader := newTestReader(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.URL.Path == "/repos/owner/repo/pulls/7":
			pullReads++
			fmt.Fprintf(writer, `{"number":7,"state":"open","head":{"sha":"h%d"}}`, pullReads)
		case strings.HasSuffix(request.URL.Path, "/commits"),
			strings.HasSuffix(request.URL.Path, "/comments"),
			strings.HasSuffix(request.URL.Path, "/events"),
			strings.HasSuffix(request.URL.Path, "/statuses"):
			fmt.Fprint(writer, `[]`)
		case strings.HasSuffix(request.URL.Path, "/check-runs"):
			fmt.Fprint(writer, `{"total_count":0,"check_runs":[]}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	_, err := reader.Snapshot(context.Background(), 7, "h1")
	if !errors.Is(err, forge.ErrEvidenceIncomplete) {
		t.Fatalf("Snapshot() error = %v, want ErrEvidenceIncomplete", err)
	}
}

func TestSnapshotRefusesAHeadThatMovedMidRead(t *testing.T) {
	pullReads := 0
	reader := newTestReader(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.URL.Path == "/repos/owner/repo/pulls/7":
			pullReads++
			head := "h1"
			if pullReads > 1 {
				head = "h2"
			}
			fmt.Fprintf(writer, `{"number":7,"state":"open","head":{"sha":"%s"}}`, head)
		case strings.HasSuffix(request.URL.Path, "/commits"),
			strings.HasSuffix(request.URL.Path, "/comments"),
			strings.HasSuffix(request.URL.Path, "/events"),
			strings.HasSuffix(request.URL.Path, "/statuses"):
			fmt.Fprint(writer, `[]`)
		case strings.HasSuffix(request.URL.Path, "/check-runs"):
			fmt.Fprint(writer, `{"total_count":0,"check_runs":[]}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	_, err := reader.Snapshot(context.Background(), 7, "")
	if !errors.Is(err, forge.ErrEvidenceIncomplete) {
		t.Fatalf("Snapshot() error = %v, want ErrEvidenceIncomplete", err)
	}
}

func TestOpenPullRequestsSkipsIssuesAndDuplicates(t *testing.T) {
	reader := newTestReader(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/repos/owner/repo/issues":
			fmt.Fprint(writer, `[{"number":7,"user":{"login":"bot"},"pull_request":{"url":"u"}},
				{"number":8,"user":{"login":"bot"}},
				{"number":9,"user":{"login":"someone"}}]`)
		case "/repos/owner/repo/pulls/7":
			fmt.Fprint(writer, `{"number":7,"state":"open","user":{"login":"bot"},"head":{"sha":"h1"}}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	pulls, err := reader.OpenPullRequests(context.Background(), "bot")
	if err != nil {
		t.Fatalf("OpenPullRequests() error = %v", err)
	}
	if len(pulls) != 1 || pulls[0].Number != 7 {
		t.Fatalf("pulls = %+v", pulls)
	}
}

func TestBranchHeadReportsAMissingBranch(t *testing.T) {
	reader := newTestReader(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.NotFound(writer, request)
	}))
	sha, exists, err := reader.BranchHead(context.Background(), "gone")
	if err != nil {
		t.Fatalf("BranchHead() error = %v", err)
	}
	if exists || sha != "" {
		t.Fatalf("BranchHead() = %q, %t, want empty and absent", sha, exists)
	}
}

func TestNewRequiresATokenAndNeverEchoesIt(t *testing.T) {
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	if _, err := New("owner/repo"); !errors.Is(err, forge.ErrAuthentication) {
		t.Fatalf("New() error = %v, want ErrAuthentication", err)
	}
	t.Setenv("GITHUB_TOKEN", "secret-value")
	reader, err := New("owner/repo")
	if err != nil || reader == nil {
		t.Fatalf("New() = %v, %v", reader, err)
	}
}

func TestNewRejectsMalformedRepository(t *testing.T) {
	for _, repository := range []string{"owner", "owner/", "/repo", "a/b/c"} {
		if _, err := New(repository); err == nil {
			t.Fatalf("New(%q) accepted a malformed repository", repository)
		}
	}
}
