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

package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	gh "github.com/google/go-github/v69/github"
)

const testHead = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const testBase = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

// TestClient constructs an entirely local API fixture.
func testClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	api := gh.NewClient(server.Client())
	api.BaseURL, _ = url.Parse(server.URL + "/")
	client, err := NewClient("owner/repo", api)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

// TestPull supplies complete typed pull-request identity metadata.
func testPull() *gh.PullRequest {
	return &gh.PullRequest{Number: gh.Ptr(7), User: &gh.User{Login: gh.Ptr("robot")}, State: gh.Ptr("open"), Draft: gh.Ptr(true), Merged: gh.Ptr(false), Title: gh.Ptr("title"), Body: gh.Ptr("body\n<!-- owned -->"), Labels: []*gh.Label{}, Head: &gh.PullRequestBranch{Ref: gh.Ptr("update"), SHA: gh.Ptr(testHead), Repo: &gh.Repository{FullName: gh.Ptr("owner/repo")}}, Base: &gh.PullRequestBranch{Ref: gh.Ptr("main"), SHA: gh.Ptr(testBase), Repo: &gh.Repository{FullName: gh.Ptr("owner/repo")}}}
}

// TestComment supplies a complete typed comment record.
func testComment(body string) *gh.IssueComment {
	stamp := gh.Timestamp{}
	return &gh.IssueComment{ID: gh.Ptr(int64(12)), User: &gh.User{Login: gh.Ptr("robot")}, Body: &body, CreatedAt: &stamp, UpdatedAt: &stamp}
}

// Respond writes a typed local fixture as JSON.
func respond(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	if p, ok := v.(*gh.PullRequest); ok {
		raw, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err = json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		if p.Labels != nil {
			fields["labels"] = p.Labels
		}
		v = fields
	}
	if p, ok := v.(*gh.ListCheckRunsResults); ok {
		v = map[string]any{"total_count": p.Total, "check_runs": p.CheckRuns}
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Error(err)
	}
}

func TestSnapshotPagesLatestChecksAndHeadRace(t *testing.T) {
	for _, race := range []bool{false, true} {
		t.Run(fmt.Sprint(race), func(t *testing.T) {
			pullReads := 0
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/repos/owner/repo/pulls/7":
					p := testPull()
					pullReads++
					if race && pullReads > 1 {
						p.Head.SHA = gh.Ptr(testBase)
					}
					respond(t, w, p)
				case "/repos/owner/repo/pulls/7/commits":
					respond(t, w, []*gh.RepositoryCommit{{SHA: gh.Ptr(testHead)}})
				case "/repos/owner/repo/issues/7/comments":
					if r.URL.Query().Get("page") == "1" {
						w.Header().Set("Link", fmt.Sprintf("<http://%s%s?page=2>; rel=\"next\"", r.Host, r.URL.Path))
						respond(t, w, []*gh.IssueComment{testComment("one")})
					} else {
						v := testComment("two")
						v.ID = gh.Ptr(int64(13))
						respond(t, w, []*gh.IssueComment{v})
					}
				case "/repos/owner/repo/issues/7/events":
					respond(t, w, []*gh.IssueEvent{})
				case "/repos/owner/repo/commits/" + testHead + "/statuses":
					respond(t, w, []*gh.RepoStatus{})
				case "/repos/owner/repo/commits/" + testHead + "/check-runs":
					checks := []*gh.CheckRun{
						{ID: gh.Ptr(int64(1)), Name: gh.Ptr("verify"), HeadSHA: gh.Ptr(testHead), Status: gh.Ptr("completed"), Conclusion: gh.Ptr("failure")},
						{ID: gh.Ptr(int64(2)), Name: gh.Ptr("verify"), HeadSHA: gh.Ptr(testHead), Status: gh.Ptr("completed"), Conclusion: gh.Ptr("success")},
					}
					if r.URL.Query().Get("filter") == "latest" {
						checks = checks[1:]
					}
					respond(t, w, &gh.ListCheckRunsResults{Total: gh.Ptr(len(checks)), CheckRuns: checks})
				default:
					t.Errorf("unexpected %s", r.URL)
					w.WriteHeader(404)
				}
			})
			snapshot, err := client.Snapshot(context.Background(), 7, testHead)
			if race {
				if !errors.Is(err, ErrConflict) {
					t.Fatalf("want head conflict, got %v", err)
				}
			} else if err != nil || len(snapshot.Comments) != 2 || len(snapshot.Checks) != 1 || snapshot.Checks[0].Conclusion != "success" {
				t.Fatalf("snapshot %+v: %v", snapshot, err)
			}
		})
	}
}

func TestPostCommentVerifiedAndDeduplicated(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprint(existing), func(t *testing.T) {
			writes := 0
			full := "/retest\n\n" + CommentMarker("retest", testHead)
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/user":
					respond(t, w, &gh.User{Login: gh.Ptr("robot")})
				case "/repos/owner/repo/pulls/7":
					respond(t, w, testPull())
				case "/repos/owner/repo/issues/7/comments":
					if r.Method == "POST" {
						writes++
						var v gh.IssueComment
						if json.NewDecoder(r.Body).Decode(&v) != nil || v.GetBody() != full {
							t.Error("wrong comment body")
						}
						respond(t, w, testComment(full))
					} else if existing {
						respond(t, w, []*gh.IssueComment{testComment(full)})
					} else {
						respond(t, w, []*gh.IssueComment{})
					}
				case "/repos/owner/repo/issues/comments/12":
					respond(t, w, testComment(full))
				default:
					t.Errorf("unexpected %s", r.URL)
					w.WriteHeader(404)
				}
			})
			receipt, err := client.PostComment(context.Background(), 7, testHead, "retest", "/retest")
			if err != nil || receipt.CommentID != 12 || receipt.Author != "robot" {
				t.Fatalf("%+v %v", receipt, err)
			}
			if existing && writes != 0 || !existing && writes != 1 {
				t.Fatalf("writes %d", writes)
			}
		})
	}
}

func TestCommentLostWriteIsIndeterminate(t *testing.T) {
	writes := 0
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user":
			respond(t, w, &gh.User{Login: gh.Ptr("robot")})
		case "/repos/owner/repo/pulls/7":
			respond(t, w, testPull())
		case "/repos/owner/repo/issues/7/comments":
			if r.Method == "POST" {
				writes++
				w.WriteHeader(502)
			} else {
				respond(t, w, []*gh.IssueComment{})
			}
		default:
			w.WriteHeader(404)
		}
	})
	receipt, err := client.PostComment(context.Background(), 7, testHead, "x", "/retest")
	if !errors.Is(err, ErrIndeterminate) || receipt.Outcome != "indeterminate" || writes != 1 {
		t.Fatalf("%+v %v writes=%d", receipt, err, writes)
	}
}

func TestRejectPartialPullAndNullPage(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) { respond(t, w, &gh.PullRequest{Number: gh.Ptr(7)}) })
	if _, err := client.PullRequest(context.Background(), 7); !errors.Is(err, ErrEvidenceIncomplete) {
		t.Fatal(err)
	}
	_, err := paginate(func(*gh.ListOptions) ([]int, *gh.Response, error) { return nil, &gh.Response{}, nil })
	if !errors.Is(err, ErrEvidenceIncomplete) {
		t.Fatal(err)
	}
}

func TestPaginationRejectsMissingOrRepeatedPages(t *testing.T) {
	for name, next := range map[string]int{"cycle": 1, "skipped": 3} {
		t.Run(name, func(t *testing.T) {
			_, err := paginate(func(*gh.ListOptions) ([]int, *gh.Response, error) {
				return []int{1}, &gh.Response{NextPage: next}, nil
			})
			if !errors.Is(err, ErrEvidenceIncomplete) {
				t.Fatal(err)
			}
		})
	}
}

func TestPublicationRejectsAmbiguityAndChecksAllFiles(t *testing.T) {
	for _, ambiguous := range []bool{false, true} {
		t.Run(fmt.Sprint(ambiguous), func(t *testing.T) {
			writes := 0
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					writes++
					t.Error("unexpected mutation")
				}
				switch {
				case r.URL.Path == "/user":
					respond(t, w, &gh.User{Login: gh.Ptr("robot")})
				case strings.HasPrefix(r.URL.Path, "/repos/owner/repo/branches/"):
					sha := testHead
					if strings.HasSuffix(r.URL.Path, "/main") {
						sha = testBase
					}
					respond(t, w, &gh.Branch{Commit: &gh.RepositoryCommit{SHA: &sha}})
				case r.URL.Path == "/repos/owner/repo/pulls":
					items := []*gh.PullRequest{{Number: gh.Ptr(7)}}
					if ambiguous {
						items = append(items, &gh.PullRequest{Number: gh.Ptr(8)})
					}
					respond(t, w, items)
				case r.URL.Path == "/repos/owner/repo/pulls/7" || r.URL.Path == "/repos/owner/repo/pulls/8":
					p := testPull()
					if strings.HasSuffix(r.URL.Path, "/8") {
						p.Number = gh.Ptr(8)
					}
					respond(t, w, p)
				case r.URL.Path == "/repos/owner/repo/pulls/7/files":
					if r.URL.Query().Get("page") == "1" {
						w.Header().Set("Link", fmt.Sprintf("<http://%s%s?page=2>; rel=\"next\"", r.Host, r.URL.Path))
						respond(t, w, []*gh.CommitFile{{Filename: gh.Ptr("expected")}})
					} else {
						respond(t, w, []*gh.CommitFile{{Filename: gh.Ptr("unexpected")}})
					}
				default:
					t.Errorf("unexpected %s", r.URL)
					w.WriteHeader(404)
				}
			})
			_, err := client.Publish(context.Background(), PublicationRequest{HeadRepository: "owner/repo", HeadRef: "update", ExpectedHead: testHead, BaseRef: "main", ExpectedBase: testBase, Actor: "robot", Marker: "<!-- owned -->", Title: "title", Body: "body\n<!-- owned -->", ExpectedFiles: []string{"expected"}})
			if !errors.Is(err, ErrConflict) || writes != 0 {
				t.Fatalf("err=%v writes=%d", err, writes)
			}
		})
	}
}

func TestPublicationReadinessIsExplicitAndExistingReadinessIsPreserved(t *testing.T) {
	cases := []struct {
		name                   string
		existing, ready, draft bool
	}{
		{name: "default draft", draft: true},
		{name: "explicit ready", ready: true},
		{name: "existing ready", existing: true},
		{name: "existing draft despite ready request", existing: true, ready: true, draft: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			existing := tc.existing
			published := existing
			writes := 0
			p := testPull()
			p.Draft = gh.Ptr(tc.draft)
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/user":
					respond(t, w, &gh.User{Login: gh.Ptr("robot")})
				case strings.HasPrefix(r.URL.Path, "/repos/owner/repo/branches/"):
					sha := testHead
					if strings.HasSuffix(r.URL.Path, "/main") {
						sha = testBase
					}
					respond(t, w, &gh.Branch{Commit: &gh.RepositoryCommit{SHA: &sha}})
				case r.URL.Path == "/repos/owner/repo/pulls" && r.Method == "POST":
					writes++
					var request gh.NewPullRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
					}
					if request.GetDraft() == tc.ready || request.GetHead() != "update" || request.GetBase() != "main" {
						t.Error("unsafe create request")
					}
					published = true
					respond(t, w, p)
				case r.URL.Path == "/repos/owner/repo/pulls":
					items := []*gh.PullRequest{}
					if published {
						items = append(items, &gh.PullRequest{Number: gh.Ptr(7)})
					}
					respond(t, w, items)
				case r.URL.Path == "/repos/owner/repo/pulls/7":
					respond(t, w, p)
				case r.URL.Path == "/repos/owner/repo/pulls/7/files":
					respond(t, w, []*gh.CommitFile{{Filename: gh.Ptr("expected")}})
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL)
					w.WriteHeader(404)
				}
			})
			receipt, err := client.Publish(context.Background(), PublicationRequest{HeadRepository: "owner/repo", HeadRef: "update", ExpectedHead: testHead, BaseRef: "main", ExpectedBase: testBase, Actor: "robot", Marker: "<!-- owned -->", Title: "title", Body: "body\n<!-- owned -->", ExpectedFiles: []string{"expected"}, ReadyForReview: tc.ready})
			if err != nil || receipt.Outcome != "observed" || receipt.Number != 7 || p.GetDraft() != tc.draft {
				t.Fatalf("%+v %v", receipt, err)
			}
			if existing && writes != 0 || !existing && writes != 1 {
				t.Fatalf("writes %d", writes)
			}
		})
	}
}

func TestCommentAdversarialEvidence(t *testing.T) {
	for _, mode := range []string{"foreign-author", "changed-body", "duplicate-id", "bad-readback"} {
		t.Run(mode, func(t *testing.T) {
			writes := 0
			full := "/retest\n\n" + CommentMarker("retest", testHead)
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/user":
					respond(t, w, &gh.User{Login: gh.Ptr("robot")})
				case "/repos/owner/repo/pulls/7":
					respond(t, w, testPull())
				case "/repos/owner/repo/issues/7/comments":
					if r.Method == "POST" {
						writes++
						respond(t, w, testComment(full))
						return
					}
					items := []*gh.IssueComment{}
					switch mode {
					case "foreign-author":
						v := testComment(full)
						v.User.Login = gh.Ptr("attacker")
						items = append(items, v)
					case "changed-body":
						items = append(items, testComment("malicious\n\n"+CommentMarker("retest", testHead)))
					case "duplicate-id":
						items = append(items, testComment(full), testComment(full))
					}
					respond(t, w, items)
				case "/repos/owner/repo/issues/comments/12":
					v := testComment(full)
					if mode == "bad-readback" {
						v.User.Login = gh.Ptr("attacker")
					}
					respond(t, w, v)
				default:
					w.WriteHeader(404)
				}
			})
			receipt, err := client.PostComment(context.Background(), 7, testHead, "retest", "/retest")
			switch mode {
			case "foreign-author":
				if err != nil || writes != 1 || receipt.Outcome != "observed" {
					t.Fatalf("%+v %v writes=%d", receipt, err, writes)
				}
			case "changed-body":
				if !errors.Is(err, ErrConflict) || writes != 0 {
					t.Fatalf("%v writes=%d", err, writes)
				}
			case "duplicate-id":
				if !errors.Is(err, ErrEvidenceIncomplete) || writes != 0 {
					t.Fatalf("%v writes=%d", err, writes)
				}
			case "bad-readback":
				if !errors.Is(err, ErrIndeterminate) || writes != 1 {
					t.Fatalf("%v writes=%d", err, writes)
				}
			}
		})
	}
}

func TestAppActorSkipsUserEndpoint(t *testing.T) {
	full := "/retest\n\n" + CommentMarker("retest", testHead)
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/owner/repo/pulls/7":
			respond(t, w, testPull())
		case "/repos/owner/repo/issues/7/comments":
			v := testComment(full)
			v.User.Login = gh.Ptr("updater[bot]")
			respond(t, w, []*gh.IssueComment{v})
		case "/repos/owner/repo/issues/comments/12":
			v := testComment(full)
			v.User.Login = gh.Ptr("updater[bot]")
			respond(t, w, v)
		default:
			t.Errorf("unexpected request %s", r.URL)
			w.WriteHeader(403)
		}
	})
	client, err := client.WithActor("updater[bot]")
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := client.PostComment(context.Background(), 7, testHead, "retest", "/retest")
	if err != nil || receipt.Author != "updater[bot]" || receipt.Outcome != "duplicate" {
		t.Fatalf("%+v %v", receipt, err)
	}
}

func TestPublicationUpdatesOnlyOwnedMetadata(t *testing.T) {
	p := testPull()
	p.Draft = gh.Ptr(false)
	p.Title = gh.Ptr("old")
	writes := 0
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/user":
			respond(t, w, &gh.User{Login: gh.Ptr("robot")})
		case strings.HasPrefix(r.URL.Path, "/repos/owner/repo/branches/"):
			sha := testHead
			if strings.HasSuffix(r.URL.Path, "/main") {
				sha = testBase
			}
			respond(t, w, &gh.Branch{Commit: &gh.RepositoryCommit{SHA: &sha}})
		case r.URL.Path == "/repos/owner/repo/pulls":
			respond(t, w, []*gh.PullRequest{{Number: gh.Ptr(7)}})
		case r.URL.Path == "/repos/owner/repo/pulls/7":
			if r.Method == "PATCH" {
				writes++
				var fields map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&fields); err != nil {
					t.Error(err)
				}
				if len(fields) != 2 || fields["title"] == nil || fields["body"] == nil {
					t.Errorf("unexpected edit fields %v", fields)
				}
				p.Title = gh.Ptr("title")
			}
			respond(t, w, p)
		case r.URL.Path == "/repos/owner/repo/pulls/7/files":
			respond(t, w, []*gh.CommitFile{{Filename: gh.Ptr("expected")}})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
			w.WriteHeader(404)
		}
	})
	receipt, err := client.Publish(context.Background(), PublicationRequest{HeadRepository: "owner/repo", HeadRef: "update", ExpectedHead: testHead, BaseRef: "main", ExpectedBase: testBase, Actor: "robot", Marker: "<!-- owned -->", Title: "title", Body: "body\n<!-- owned -->", ExpectedFiles: []string{"expected"}})
	if err != nil || writes != 1 || receipt.Outcome != "observed" || p.GetDraft() {
		t.Fatalf("%+v %v writes=%d draft=%v", receipt, err, writes, p.GetDraft())
	}
}

func TestPublicationForkIdentity(t *testing.T) {
	published := false
	p := testPull()
	p.Head.Repo.FullName = gh.Ptr("source/fork")
	sourceReads := 0
	baseReads := 0
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/source/fork/branches/update":
			sourceReads++
			respond(t, w, &gh.Branch{Commit: &gh.RepositoryCommit{SHA: gh.Ptr(testHead)}})
		case r.URL.Path == "/repos/owner/repo/branches/main":
			baseReads++
			respond(t, w, &gh.Branch{Commit: &gh.RepositoryCommit{SHA: gh.Ptr(testBase)}})
		case r.URL.Path == "/repos/owner/repo/pulls" && r.Method == "POST":
			var request gh.NewPullRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			if request.GetHead() != "source:update" || request.GetHeadRepo() != "fork" || !request.GetDraft() {
				t.Error("wrong fork identity")
			}
			published = true
			respond(t, w, p)
		case r.URL.Path == "/repos/owner/repo/pulls":
			items := []*gh.PullRequest{}
			if published {
				items = append(items, &gh.PullRequest{Number: gh.Ptr(7)})
			}
			respond(t, w, items)
		case r.URL.Path == "/repos/owner/repo/pulls/7":
			respond(t, w, p)
		case r.URL.Path == "/repos/owner/repo/pulls/7/files":
			respond(t, w, []*gh.CommitFile{{Filename: gh.Ptr("expected")}})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
			w.WriteHeader(404)
		}
	})
	client, err := client.WithActor("robot")
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := client.Publish(context.Background(), PublicationRequest{HeadRepository: "source/fork", HeadRef: "update", ExpectedHead: testHead, BaseRef: "main", ExpectedBase: testBase, Actor: "robot", Marker: "<!-- owned -->", Title: "title", Body: "body\n<!-- owned -->", ExpectedFiles: []string{"expected"}})
	if err != nil || receipt.HeadRepo != "source/fork" || sourceReads == 0 || baseReads == 0 {
		t.Fatalf("%+v %v source=%d base=%d", receipt, err, sourceReads, baseReads)
	}
}
