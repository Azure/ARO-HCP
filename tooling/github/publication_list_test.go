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
	"strconv"
	"strings"
	"testing"

	gh "github.com/google/go-github/v69/github"
)

func testPublicationRequest() PublicationRequest {
	return PublicationRequest{HeadRepository: "owner/repo", HeadRef: "update", ExpectedHead: testHead, BaseRef: "main", ExpectedBase: testBase, Actor: "robot", Marker: "<!-- owned -->", Title: "title", Body: "body\n<!-- owned -->", ExpectedFiles: []string{"expected"}}
}

func TestPublicationInventoryDoesNotReadUnrelatedPulls(t *testing.T) {
	const unrelated = 266
	listReads, unrelatedReads, candidateReads := 0, 0, 0
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("unexpected mutation %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
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
			listReads++
			if r.URL.Query().Get("state") != "open" || r.URL.Query().Get("per_page") != "100" {
				t.Errorf("incorrect inventory query %s", r.URL)
			}
			page, err := strconv.Atoi(r.URL.Query().Get("page"))
			if err != nil || page < 1 || page > 3 {
				t.Errorf("invalid inventory page %s", r.URL)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			items := []*gh.PullRequest{}
			for i := (page - 1) * 100; i < page*100 && i < unrelated; i++ {
				p := testListedPull(testPull())
				p.Number = gh.Ptr(i + 100)
				p.User.Login = gh.Ptr("contributor")
				p.Head.Ref = gh.Ptr(fmt.Sprintf("unrelated-%d", i))
				p.Body = gh.Ptr("Unrelated proposal")
				items = append(items, p)
			}
			if page < 3 {
				w.Header().Set("Link", fmt.Sprintf("<http://%s%s?page=%d>; rel=\"next\"", r.Host, r.URL.Path, page+1))
			} else {
				items = append(items, testListedPull(testPull()))
			}
			respond(t, w, items)
		case r.URL.Path == "/repos/owner/repo/pulls/7":
			candidateReads++
			respond(t, w, testPull())
		case r.URL.Path == "/repos/owner/repo/pulls/7/files":
			respond(t, w, []*gh.CommitFile{{Filename: gh.Ptr("expected")}})
		case strings.HasPrefix(r.URL.Path, "/repos/owner/repo/pulls/"):
			unrelatedReads++
			w.WriteHeader(http.StatusNotFound)
		default:
			t.Errorf("unexpected request %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	receipt, err := client.Publish(context.Background(), testPublicationRequest())
	if err != nil || receipt.Outcome != "observed" || listReads != 6 || candidateReads != 1 || unrelatedReads != 0 {
		t.Fatalf("receipt=%+v err=%v list=%d candidate=%d unrelated=%d", receipt, err, listReads, candidateReads, unrelatedReads)
	}
}

func TestOwnedPullInventoryRejectsIncompleteOrConflictingRecords(t *testing.T) {
	cases := []struct {
		name      string
		mutate    func(*gh.PullRequest)
		field     string
		duplicate bool
		want      error
	}{
		{name: "missing number", mutate: func(p *gh.PullRequest) { p.Number = nil }, want: ErrEvidenceIncomplete},
		{name: "missing author", mutate: func(p *gh.PullRequest) { p.User = nil }, want: ErrEvidenceIncomplete},
		{name: "missing source", mutate: func(p *gh.PullRequest) { p.Head.Repo = nil }, want: ErrEvidenceIncomplete},
		{name: "missing target", mutate: func(p *gh.PullRequest) { p.Base = nil }, want: ErrEvidenceIncomplete},
		{name: "missing body", field: "body", want: ErrEvidenceIncomplete},
		{name: "missing state", mutate: func(p *gh.PullRequest) { p.State = nil }, want: ErrEvidenceIncomplete},
		{name: "missing draft", mutate: func(p *gh.PullRequest) { p.Draft = nil }, want: ErrEvidenceIncomplete},
		{name: "missing title", mutate: func(p *gh.PullRequest) { p.Title = nil }, want: ErrEvidenceIncomplete},
		{name: "foreign target repository", mutate: func(p *gh.PullRequest) { p.Base.Repo.FullName = gh.Ptr("foreign/repo") }, want: ErrEvidenceIncomplete},
		{name: "closed list entry", mutate: func(p *gh.PullRequest) { p.State = gh.Ptr("closed") }, want: ErrEvidenceIncomplete},
		{name: "unrelated missing author", mutate: func(p *gh.PullRequest) { p.Head.Ref = gh.Ptr("unrelated"); p.Body = gh.Ptr("unrelated"); p.User = nil }, want: ErrEvidenceIncomplete},
		{name: "foreign author", mutate: func(p *gh.PullRequest) { p.User.Login = gh.Ptr("other") }, want: ErrConflict},
		{name: "foreign source", mutate: func(p *gh.PullRequest) { p.Head.Repo.FullName = gh.Ptr("foreign/repo") }, want: ErrConflict},
		{name: "foreign head", mutate: func(p *gh.PullRequest) { p.Head.SHA = gh.Ptr(testBase) }, want: ErrConflict},
		{name: "foreign target ref", mutate: func(p *gh.PullRequest) { p.Base.Ref = gh.Ptr("other") }, want: ErrConflict},
		{name: "marker substring", mutate: func(p *gh.PullRequest) { p.Body = gh.Ptr("prefix <!-- owned --> suffix") }, want: ErrConflict},
		{name: "duplicate number across pages", duplicate: true, want: ErrEvidenceIncomplete},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pullReads := 0
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/repos/owner/repo/pulls" {
					pullReads++
					w.WriteHeader(http.StatusNotFound)
					return
				}
				p := testListedPull(testPull())
				if tc.mutate != nil {
					tc.mutate(p)
				}
				if tc.duplicate && r.URL.Query().Get("page") == "1" {
					w.Header().Set("Link", fmt.Sprintf("<http://%s%s?page=2>; rel=\"next\"", r.Host, r.URL.Path))
				}
				if tc.field != "" {
					raw, err := json.Marshal(p)
					if err != nil {
						t.Fatal(err)
					}
					var fields map[string]any
					if err := json.Unmarshal(raw, &fields); err != nil {
						t.Fatal(err)
					}
					delete(fields, tc.field)
					respond(t, w, []any{fields})
					return
				}
				respond(t, w, []*gh.PullRequest{p})
			})
			_, err := client.ownedPulls(context.Background(), testPublicationRequest())
			if !errors.Is(err, tc.want) || pullReads != 0 {
				t.Fatalf("err=%v pullReads=%d", err, pullReads)
			}
		})
	}
}

func TestOwnedPullInventoryAcceptsExplicitNullUnrelatedBody(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		p := testListedPull(testPull())
		p.Head.Ref = gh.Ptr("unrelated")
		raw, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		fields["body"] = nil
		respond(t, w, []any{fields})
	})
	owned, err := client.ownedPulls(context.Background(), testPublicationRequest())
	if err != nil || len(owned) != 0 {
		t.Fatalf("owned=%+v err=%v", owned, err)
	}
}

func TestOwnedPullInventoryRejectsNullEvidence(t *testing.T) {
	for _, records := range [][]*gh.PullRequest{nil, {nil}} {
		client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			respond(t, w, records)
		})
		if _, err := client.ownedPulls(context.Background(), testPublicationRequest()); !errors.Is(err, ErrEvidenceIncomplete) {
			t.Fatalf("records=%+v err=%v", records, err)
		}
	}
}
