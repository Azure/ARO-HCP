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
			fmt.Fprint(writer, `{"number":7,"state":"open","title":"t","body":"","user":{"login":"bot"},
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
			fmt.Fprintf(writer, `{"number":7,"state":"open","title":"t","body":"","user":{"login":"bot"},"labels":[],
				"head":{"sha":"h%d"},"base":{"ref":"main"}}`, pullReads)
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
			fmt.Fprintf(writer, `{"number":7,"state":"open","title":"t","body":"","user":{"login":"bot"},"labels":[],
				"head":{"sha":"%s"},"base":{"ref":"main"}}`, head)
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
			fmt.Fprint(writer, `[{"number":7,"state":"open","title":"t","body":"","labels":[],"user":{"login":"bot"},"pull_request":{"url":"u"}},
				{"number":8,"state":"open","title":"t","body":"","labels":[],"user":{"login":"bot"}},
				{"number":9,"state":"open","title":"t","body":"","labels":[],"user":{"login":"someone"}}]`)
		case "/repos/owner/repo/pulls/7":
			fmt.Fprint(writer, `{"number":7,"state":"open","title":"t","body":"","user":{"login":"bot"},"labels":[],
				"head":{"sha":"h1"},"base":{"ref":"main"}}`)
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

func TestPullRequestRejectsIncompleteDetails(t *testing.T) {
	cases := map[string]string{
		"missing base":     `{"number":7,"state":"open","title":"t","body":"","user":{"login":"bot"},"labels":[]}`,
		"missing base ref": `{"number":7,"state":"open","title":"t","body":"","user":{"login":"bot"},"labels":[],"base":{}}`,
		"missing user":     `{"number":7,"state":"open","title":"t","labels":[],"base":{"ref":"main"}}`,
		"missing state":    `{"number":7,"title":"t","body":"","user":{"login":"bot"},"labels":[],"base":{"ref":"main"}}`,
		"missing title":    `{"number":7,"state":"open","user":{"login":"bot"},"labels":[],"base":{"ref":"main"}}`,
		"missing labels":   `{"number":7,"state":"open","title":"t","body":"","user":{"login":"bot"},"base":{"ref":"main"}}`,
		"missing body":     `{"number":7,"state":"open","title":"t","user":{"login":"bot"},"labels":[],"base":{"ref":"main"}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			reader := newTestReader(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				if request.URL.Path == "/repos/owner/repo/pulls/7" {
					fmt.Fprint(writer, body)
					return
				}
				http.NotFound(writer, request)
			}))
			if _, err := reader.PullRequest(context.Background(), 7); !errors.Is(err, forge.ErrEvidenceIncomplete) {
				t.Fatalf("PullRequest() error = %v, want ErrEvidenceIncomplete", err)
			}
		})
	}
}

func TestOpenPullRequestsFailsClosedOnIncompleteIssueMetadata(t *testing.T) {
	reader := newTestReader(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/repos/owner/repo/issues" {
			fmt.Fprint(writer, `[{"number":7,"state":"open","pull_request":{"url":"u"}}]`)
			return
		}
		http.NotFound(writer, request)
	}))
	_, err := reader.OpenPullRequests(context.Background(), "bot")
	if !errors.Is(err, forge.ErrEvidenceIncomplete) {
		t.Fatalf("OpenPullRequests() error = %v, want ErrEvidenceIncomplete", err)
	}
}

func TestPullRequestAcceptsANullBody(t *testing.T) {
	reader := newTestReader(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/repos/owner/repo/pulls/7" {
			fmt.Fprint(writer, `{"number":7,"state":"open","title":"t","body":null,"user":{"login":"bot"},"labels":[],"base":{"ref":"main"}}`)
			return
		}
		http.NotFound(writer, request)
	}))
	pull, err := reader.PullRequest(context.Background(), 7)
	if err != nil {
		t.Fatalf("PullRequest() error = %v", err)
	}
	if pull.Body != "" {
		t.Fatalf("body = %q, want empty", pull.Body)
	}
}

func TestOpenPullRequestsAcceptsANullIssueBody(t *testing.T) {
	reader := newTestReader(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/repos/owner/repo/issues" {
			fmt.Fprint(writer, `[{"number":8,"state":"open","title":"image update","body":null,"labels":[],"user":{"login":"someone"},"pull_request":{"url":"u"}}]`)
			return
		}
		http.NotFound(writer, request)
	}))
	pulls, err := reader.OpenPullRequests(context.Background(), "bot")
	if err != nil {
		t.Fatalf("OpenPullRequests() error = %v", err)
	}
	if len(pulls) != 0 {
		t.Fatalf("pulls = %+v, want none", pulls)
	}
}

func TestOpenPullRequestsRejectsUnknownIssueStatesAndDuplicates(t *testing.T) {
	cases := map[string]string{
		"unknown state": `[{"number":7,"state":"unknown","title":"t","labels":[],"user":{"login":"bot"},"pull_request":{"url":"u"}}]`,
		"duplicate ordinary issues": `[{"number":8,"state":"open","title":"t","labels":[],"user":{"login":"bot"}},
			{"number":8,"state":"open","title":"t","labels":[],"user":{"login":"bot"}}]`,
		"duplicate unrelated proposals": `[{"number":9,"state":"open","title":"t","labels":[],"user":{"login":"someone"},"pull_request":{"url":"u"}},
			{"number":9,"state":"open","title":"t","labels":[],"user":{"login":"someone"},"pull_request":{"url":"u"}}]`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			reader := newTestReader(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				if request.URL.Path == "/repos/owner/repo/issues" {
					fmt.Fprint(writer, body)
					return
				}
				http.NotFound(writer, request)
			}))
			if _, err := reader.OpenPullRequests(context.Background(), "bot"); !errors.Is(err, forge.ErrEvidenceIncomplete) {
				t.Fatalf("OpenPullRequests() error = %v, want ErrEvidenceIncomplete", err)
			}
		})
	}
}

func TestOpenPullRequestsFailsClosedWhenListBodyIsMissing(t *testing.T) {
	fetched := false
	reader := newTestReader(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/repos/owner/repo/issues":
			// An unlabelled tooling title without a list body must not be
			// filtered out before the detail fetch.
			fmt.Fprint(writer, `[{"number":7,"state":"open","title":"chore(tools): bump yq","labels":[],"user":{"login":"bot"},"pull_request":{"url":"u"}}]`)
		case "/repos/owner/repo/pulls/7":
			fetched = true
			http.Error(writer, "detail must not be fetched", http.StatusInternalServerError)
		default:
			http.NotFound(writer, request)
		}
	}))
	reader.prefilter = func(forge.PullRequest) bool { return true }
	if _, err := reader.OpenPullRequests(context.Background(), "bot"); !errors.Is(err, forge.ErrEvidenceIncomplete) {
		t.Fatalf("OpenPullRequests() error = %v, want ErrEvidenceIncomplete", err)
	}
	if fetched {
		t.Fatal("detail was fetched for a malformed list entry")
	}
}

func TestOpenPullRequestsKeepsMarkerMatchesBeforeFetchingDetails(t *testing.T) {
	reader := newTestReader(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/repos/owner/repo/issues":
			// Unlabelled but carrying the family marker in the list body: the
			// prefilter must keep it so the detail can be read.
			fmt.Fprint(writer, `[{"number":7,"state":"open","title":"chore(tools): bump yq","body":"<!-- agentic-tool-family: mikefarah/yq -->","labels":[],"user":{"login":"bot"},"pull_request":{"url":"u"}}]`)
		case "/repos/owner/repo/pulls/7":
			fmt.Fprint(writer, `{"number":7,"state":"open","title":"chore(tools): bump yq","body":"<!-- agentic-tool-family: mikefarah/yq -->","user":{"login":"bot"},"labels":[],
				"head":{"sha":"h1"},"base":{"ref":"main"}}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	reader.prefilter = func(pull forge.PullRequest) bool {
		return pull.Author == "bot" && pull.State == "open" &&
			pull.Title == "chore(tools): bump yq" && pull.Body != ""
	}
	pulls, err := reader.OpenPullRequests(context.Background(), "bot")
	if err != nil {
		t.Fatalf("OpenPullRequests() error = %v", err)
	}
	if len(pulls) != 1 || pulls[0].Number != 7 || pulls[0].Body == "" {
		t.Fatalf("pulls = %+v", pulls)
	}
}

func TestOpenPullRequestsPrefiltersBeforeFetchingDetails(t *testing.T) {
	fetched := map[int]bool{}
	var filtered []int
	reader := newTestReader(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/repos/owner/repo/issues":
			fmt.Fprint(writer, `[{"number":7,"state":"open","title":"bump","body":"","labels":[],"user":{"login":"bot"},"pull_request":{"url":"u"}},
				{"number":8,"state":"open","title":"image update","body":"","labels":[],"user":{"login":"bot"},"pull_request":{"url":"u"}}]`)
		case "/repos/owner/repo/pulls/7":
			fetched[7] = true
			fmt.Fprint(writer, `{"number":7,"state":"open","title":"bump","body":"","user":{"login":"bot"},"labels":[],
				"head":{"sha":"h1"},"base":{"ref":"main"}}`)
		case "/repos/owner/repo/pulls/8":
			fetched[8] = true
			http.Error(writer, "unexpected detail fetch", http.StatusInternalServerError)
		default:
			http.NotFound(writer, request)
		}
	}))
	reader.prefilter = func(pull forge.PullRequest) bool {
		filtered = append(filtered, pull.Number)
		return pull.Number == 7
	}
	pulls, err := reader.OpenPullRequests(context.Background(), "bot")
	if err != nil {
		t.Fatalf("OpenPullRequests() error = %v", err)
	}
	if len(pulls) != 1 || pulls[0].Number != 7 {
		t.Fatalf("pulls = %+v", pulls)
	}
	if fetched[8] {
		t.Fatal("an unrelated proposal was fetched in detail")
	}
	if len(filtered) != 2 || filtered[0] != 7 || filtered[1] != 8 {
		t.Fatalf("prefilter saw %v", filtered)
	}
}

func TestOpenPullRequestsRejectsMalformedPullRequestMarkers(t *testing.T) {
	cases := map[string]string{
		"null marker":   `[{"number":7,"state":"open","user":{"login":"bot"},"pull_request":null}]`,
		"string marker": `[{"number":7,"state":"open","user":{"login":"bot"},"pull_request":"x"}]`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			reader := newTestReader(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				if request.URL.Path == "/repos/owner/repo/issues" {
					fmt.Fprint(writer, body)
					return
				}
				http.NotFound(writer, request)
			}))
			if _, err := reader.OpenPullRequests(context.Background(), "bot"); !errors.Is(err, forge.ErrEvidenceIncomplete) {
				t.Fatalf("OpenPullRequests() error = %v, want ErrEvidenceIncomplete", err)
			}
		})
	}
}

func TestOpenPullRequestsListsAcrossPages(t *testing.T) {
	reader := newTestReader(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/repos/owner/repo/issues":
			switch request.URL.Query().Get("page") {
			case "0":
				writer.Header().Set("Link", fmt.Sprintf(`<http://%s/repos/owner/repo/issues?page=2>; rel="next"`, request.Host))
				fmt.Fprint(writer, `[{"number":8,"state":"open","title":"t","body":"","labels":[],"user":{"login":"bot"}}]`)
			case "2":
				fmt.Fprint(writer, `[{"number":7,"state":"open","title":"t","body":"","labels":[],"user":{"login":"bot"},"pull_request":{"url":"u"}}]`)
			default:
				http.Error(writer, "unexpected page", http.StatusBadRequest)
			}
		case "/repos/owner/repo/pulls/7":
			fmt.Fprint(writer, `{"number":7,"state":"open","title":"t","body":"","user":{"login":"bot"},"labels":[],
				"head":{"sha":"h1"},"base":{"ref":"main"}}`)
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

func TestOpenPullRequestsRejectsNullPages(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"first page": func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			if request.URL.Path == "/repos/owner/repo/issues" {
				fmt.Fprint(writer, `null`)
				return
			}
			http.NotFound(writer, request)
		},
		"later page": func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			if request.URL.Path != "/repos/owner/repo/issues" {
				http.NotFound(writer, request)
				return
			}
			if request.URL.Query().Get("page") == "0" {
				writer.Header().Set("Link", fmt.Sprintf(`<http://%s/repos/owner/repo/issues?page=2>; rel="next"`, request.Host))
				fmt.Fprint(writer, `[]`)
				return
			}
			fmt.Fprint(writer, `null`)
		},
	}
	for name, handler := range cases {
		t.Run(name, func(t *testing.T) {
			reader := newTestReader(t, handler)
			if _, err := reader.OpenPullRequests(context.Background(), "bot"); !errors.Is(err, forge.ErrEvidenceIncomplete) {
				t.Fatalf("OpenPullRequests() error = %v, want ErrEvidenceIncomplete", err)
			}
		})
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
