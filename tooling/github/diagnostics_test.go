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
	"errors"
	"net/http"
	"strings"
	"testing"

	gh "github.com/google/go-github/v69/github"
)

func TestPullRequestRejectsMissingMetadata(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*gh.PullRequest)
	}{
		{name: "number", mutate: func(p *gh.PullRequest) { p.Number = nil }},
		{name: "wrong number", mutate: func(p *gh.PullRequest) { p.Number = gh.Ptr(8) }},
		{name: "author", mutate: func(p *gh.PullRequest) { p.User = nil }},
		{name: "state", mutate: func(p *gh.PullRequest) { p.State = nil }},
		{name: "unknown state", mutate: func(p *gh.PullRequest) { p.State = gh.Ptr("unknown") }},
		{name: "draft", mutate: func(p *gh.PullRequest) { p.Draft = nil }},
		{name: "merged", mutate: func(p *gh.PullRequest) { p.Merged = nil }},
		{name: "title", mutate: func(p *gh.PullRequest) { p.Title = nil }},
		{name: "body", mutate: func(p *gh.PullRequest) { p.Body = nil }},
		{name: "labels", mutate: func(p *gh.PullRequest) { p.Labels = nil }},
		{name: "label name", mutate: func(p *gh.PullRequest) { p.Labels = []*gh.Label{{}} }},
		{name: "head SHA", mutate: func(p *gh.PullRequest) { p.Head.SHA = nil }},
		{name: "head ref", mutate: func(p *gh.PullRequest) { p.Head.Ref = nil }},
		{name: "head repository", mutate: func(p *gh.PullRequest) { p.Head.Repo = nil }},
		{name: "base ref", mutate: func(p *gh.PullRequest) { p.Base.Ref = nil }},
		{name: "base repository", mutate: func(p *gh.PullRequest) { p.Base.Repo = nil }},
		{name: "foreign base repository", mutate: func(p *gh.PullRequest) { p.Base.Repo.FullName = gh.Ptr("other/repo") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				p := testPull()
				tc.mutate(p)
				respond(t, w, p)
			})
			if _, err := client.PullRequest(context.Background(), 7); !errors.Is(err, ErrEvidenceIncomplete) {
				t.Fatalf("got %v, want incomplete evidence", err)
			}
		})
	}
}

func TestUncertainWritesRetainSDKCause(t *testing.T) {
	for _, operation := range []string{"comment", "create", "edit"} {
		t.Run(operation, func(t *testing.T) {
			writes := 0
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					writes++
					w.WriteHeader(http.StatusBadGateway)
					respond(t, w, map[string]string{"message": "upstream unavailable"})
					return
				}
				switch {
				case strings.HasPrefix(r.URL.Path, "/repos/owner/repo/branches/"):
					sha := testHead
					if strings.HasSuffix(r.URL.Path, "/main") {
						sha = testBase
					}
					respond(t, w, &gh.Branch{Commit: &gh.RepositoryCommit{SHA: &sha}})
				case r.URL.Path == "/repos/owner/repo/pulls":
					items := []*gh.PullRequest{}
					if operation == "edit" {
						p := testListedPull(testPull())
						p.Title = gh.Ptr("old title")
						items = append(items, p)
					}
					respond(t, w, items)
				case r.URL.Path == "/repos/owner/repo/pulls/7":
					respond(t, w, testPull())
				case r.URL.Path == "/repos/owner/repo/pulls/7/files":
					respond(t, w, []*gh.CommitFile{{Filename: gh.Ptr("expected")}})
				case r.URL.Path == "/repos/owner/repo/issues/7/comments":
					respond(t, w, []*gh.IssueComment{})
				default:
					t.Errorf("unexpected request %s", r.URL)
					w.WriteHeader(http.StatusNotFound)
				}
			})
			client, err := client.WithActor("robot")
			if err != nil {
				t.Fatal(err)
			}
			var receipt Receipt
			if operation == "comment" {
				receipt, err = client.PostComment(context.Background(), 7, testHead, "retest", "/retest")
			} else {
				receipt, err = client.Publish(context.Background(), testPublicationRequest())
			}
			wantWrites := 1
			if operation == "create" {
				wantWrites = 3
			}
			var cause *gh.ErrorResponse
			if !errors.Is(err, ErrIndeterminate) || !errors.As(err, &cause) || cause.Response.StatusCode != http.StatusBadGateway || writes != wantWrites || receipt.Outcome != "indeterminate" {
				t.Fatalf("receipt=%+v err=%v cause=%+v writes=%d", receipt, err, cause, writes)
			}
		})
	}
}

func TestUncertainReadbackRetainsEvidenceClassification(t *testing.T) {
	for _, operation := range []string{"comment", "publish"} {
		t.Run(operation, func(t *testing.T) {
			pullReads := 0
			full := "/retest\n\n" + CommentMarker("retest", testHead)
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasPrefix(r.URL.Path, "/repos/owner/repo/branches/"):
					sha := testHead
					if strings.HasSuffix(r.URL.Path, "/main") {
						sha = testBase
					}
					respond(t, w, &gh.Branch{Commit: &gh.RepositoryCommit{SHA: &sha}})
				case r.URL.Path == "/repos/owner/repo/pulls":
					respond(t, w, []*gh.PullRequest{testListedPull(testPull())})
				case r.URL.Path == "/repos/owner/repo/pulls/7":
					pullReads++
					p := testPull()
					if operation == "publish" || pullReads == 3 {
						p.Merged = nil
					}
					respond(t, w, p)
				case r.URL.Path == "/repos/owner/repo/pulls/7/files":
					respond(t, w, []*gh.CommitFile{{Filename: gh.Ptr("expected")}})
				case r.URL.Path == "/repos/owner/repo/issues/7/comments":
					if r.Method == "POST" {
						respond(t, w, testComment(full))
					} else {
						respond(t, w, []*gh.IssueComment{})
					}
				case r.URL.Path == "/repos/owner/repo/issues/comments/12":
					respond(t, w, testComment(full))
				default:
					t.Errorf("unexpected request %s", r.URL)
					w.WriteHeader(http.StatusNotFound)
				}
			})
			client, err := client.WithActor("robot")
			if err != nil {
				t.Fatal(err)
			}
			if operation == "comment" {
				_, err = client.PostComment(context.Background(), 7, testHead, "retest", "/retest")
			} else {
				_, err = client.Publish(context.Background(), testPublicationRequest())
			}
			if !errors.Is(err, ErrIndeterminate) || !errors.Is(err, ErrEvidenceIncomplete) {
				t.Fatalf("got %v, want indeterminate and incomplete evidence", err)
			}
		})
	}
}
