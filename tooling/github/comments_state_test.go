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
	"testing"

	gh "github.com/google/go-github/v69/github"
)

func TestPostCommentRevalidatesStateAndIdentity(t *testing.T) {
	changes := []struct {
		name   string
		mutate func(*gh.PullRequest)
	}{
		{name: "stable"},
		{name: "closed", mutate: func(p *gh.PullRequest) { p.State = gh.Ptr("closed") }},
		{name: "merged", mutate: func(p *gh.PullRequest) { p.Merged = gh.Ptr(true) }},
		{name: "author", mutate: func(p *gh.PullRequest) { p.User.Login = gh.Ptr("other") }},
		{name: "head", mutate: func(p *gh.PullRequest) { p.Head.SHA = gh.Ptr(testBase) }},
		{name: "source-ref", mutate: func(p *gh.PullRequest) { p.Head.Ref = gh.Ptr("other") }},
		{name: "target-ref", mutate: func(p *gh.PullRequest) { p.Base.Ref = gh.Ptr("other") }},
		{name: "source-repository", mutate: func(p *gh.PullRequest) { p.Head.Repo.FullName = gh.Ptr("other/repo") }},
		{name: "target-repository", mutate: func(p *gh.PullRequest) { p.Base.Repo.FullName = gh.Ptr("owner/other") }},
	}
	phases := []struct {
		name      string
		changeAt  int
		duplicate bool
	}{
		{name: "before-write", changeAt: 2},
		{name: "after-write", changeAt: 3},
		{name: "duplicate-before-readback", changeAt: 2, duplicate: true},
		{name: "duplicate-after-readback", changeAt: 3, duplicate: true},
	}
	for _, phase := range phases {
		for _, change := range changes {
			t.Run(phase.name+"/"+change.name, func(t *testing.T) {
				pullReads, writes := 0, 0
				full := "/retest\n\n" + CommentMarker("retest", testHead)
				client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
					switch r.Method + " " + r.URL.Path {
					case "GET /user":
						respond(t, w, &gh.User{Login: gh.Ptr("robot")})
					case "GET /repos/owner/repo/pulls/7":
						pullReads++
						p := testPull()
						if change.mutate != nil && pullReads >= phase.changeAt {
							change.mutate(p)
						}
						respond(t, w, p)
					case "GET /repos/owner/repo/issues/7/comments":
						comments := []*gh.IssueComment{}
						if phase.duplicate {
							comments = append(comments, testComment(full))
						}
						respond(t, w, comments)
					case "POST /repos/owner/repo/issues/7/comments":
						writes++
						respond(t, w, testComment(full))
					case "GET /repos/owner/repo/issues/comments/12":
						respond(t, w, testComment(full))
					default:
						t.Errorf("unexpected request %s %s", r.Method, r.URL)
						w.WriteHeader(http.StatusNotFound)
					}
				})

				receipt, err := client.PostComment(context.Background(), 7, testHead, "retest", "/retest")
				wantWrites, wantCommentID := 1, 12
				wantOutcome := "observed"
				var wantErr error
				if phase.duplicate {
					wantWrites, wantOutcome = 0, "duplicate"
				}
				if change.mutate != nil {
					if phase.duplicate || phase.changeAt == 2 {
						wantErr, wantWrites, wantOutcome = ErrConflict, 0, "not-applicable"
						if phase.changeAt == 2 {
							wantCommentID = 0
						}
					} else {
						wantErr, wantOutcome = ErrIndeterminate, "indeterminate"
					}
				}
				if !errors.Is(err, wantErr) || writes != wantWrites || receipt.Outcome != wantOutcome || receipt.CommentID != wantCommentID {
					t.Fatalf("receipt=%+v err=%v writes=%d pullReads=%d; want outcome=%s commentID=%d err=%v writes=%d", receipt, err, writes, pullReads, wantOutcome, wantCommentID, wantErr, wantWrites)
				}
				if receipt.Number != 7 || receipt.RequestedHead != testHead || receipt.ObservedHead != testHead || receipt.Author != "robot" || receipt.URL != "https://github.com/owner/repo/pull/7" {
					t.Fatalf("receipt lost original proposal or comment identity: %+v", receipt)
				}
			})
		}
	}
}
