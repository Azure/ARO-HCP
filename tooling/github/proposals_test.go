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
	"net/http"
	"strings"
	"testing"

	gh "github.com/google/go-github/v69/github"
)

// proposalPull builds one open pull request served by the inventory.
func proposalPull(number int, author, head, body string) *gh.PullRequest {
	return &gh.PullRequest{
		Number: gh.Ptr(number), User: &gh.User{Login: gh.Ptr(author)},
		State: gh.Ptr("open"), Draft: gh.Ptr(true), Title: gh.Ptr("title"), Body: gh.Ptr(body),
		Head: &gh.PullRequestBranch{
			Ref: gh.Ptr(head), SHA: gh.Ptr(testHead),
			Repo: &gh.Repository{FullName: gh.Ptr("owner/repo")},
		},
		Base: &gh.PullRequestBranch{
			Ref:  gh.Ptr("main"),
			Repo: &gh.Repository{FullName: gh.Ptr("owner/repo")},
		},
	}
}

// proposalClient serves one inventory page.
func proposalClient(t *testing.T, pulls ...*gh.PullRequest) *Client {
	t.Helper()
	return testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || !strings.HasPrefix(r.URL.Path, "/repos/owner/repo/pulls") {
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		respond(t, w, pulls)
	})
}

func TestOpenProposalsSelectsOnlyOwnedProposals(t *testing.T) {
	client := proposalClient(t,
		proposalPull(9003, "robot", "bot/test-priorities-2", "body\n<!-- owned -->"),
		proposalPull(9001, "robot", "bot/test-priorities-1", "body"),
		proposalPull(9002, "someone", "bot/test-priorities-3", "body\n<!-- owned -->"),
		proposalPull(9004, "robot", "unrelated", "body"),
	)
	numbers, err := client.OpenProposals(context.Background(), "robot", "<!-- owned -->", "bot/test-priorities-")
	if err != nil {
		t.Fatal(err)
	}
	want := []int{9001, 9003}
	if len(numbers) != len(want) || numbers[0] != want[0] || numbers[1] != want[1] {
		t.Fatalf("numbers = %v, want %v", numbers, want)
	}
}

func TestOpenProposalsRejectsAnEmptySelector(t *testing.T) {
	client := proposalClient(t)
	for _, tc := range []struct {
		name         string
		actor        string
		marker       string
		branchPrefix string
	}{
		{name: "no actor", marker: "<!-- owned -->"},
		{name: "no selector", actor: "robot"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := client.OpenProposals(context.Background(), tc.actor, tc.marker, tc.branchPrefix); err == nil {
				t.Fatal("an empty selector was accepted")
			}
		})
	}
}
