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

package dependabot

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-github/v69/github"

	automation "github.com/Azure/ARO-HCP/tooling/github"
)

// TestNativePublicationMetadata checks exact ownership schemas and integer preservation.
func TestNativePublicationMetadata(t *testing.T) {
	group := strings.Repeat("a", 24)
	head := strings.Repeat("b", 40)
	valid := `{"group":"` + group + `","sources":[{"number":12,"head_sha":"` + head + `"}]}`
	marker := func(document string) string { return "<!-- native-dependabot-maintenance:" + document + " -->" }
	tests := []struct {
		name      string
		body      string
		wantError bool
	}{
		{name: "valid standalone", body: "Title\n\n" + marker(valid)},
		{name: "duplicate markers", body: marker(valid) + "\n" + marker(valid), wantError: true},
		{name: "inline marker", body: "text " + marker(valid), wantError: true},
		{name: "unknown field", body: marker(strings.Replace(valid, `"sources":`, `"extra":true,"sources":`, 1)), wantError: true},
		{name: "duplicate field", body: marker(strings.Replace(valid, `"group":`, `"group":"`+group+`","group":`, 1)), wantError: true},
		{name: "fractional identity", body: marker(strings.Replace(valid, `"number":12`, `"number":12.0`, 1)), wantError: true},
		{name: "boolean identity", body: marker(strings.Replace(valid, `"number":12`, `"number":true`, 1)), wantError: true},
		{name: "uppercase head", body: marker(strings.ReplaceAll(valid, head, strings.ToUpper(head))), wantError: true},
		{name: "empty sources", body: marker(`{"group":"` + group + `","sources":[]}`), wantError: true},
		{name: "duplicate sources", body: marker(`{"group":"` + group + `","sources":[{"number":12,"head_sha":"` + head + `"},{"number":12,"head_sha":"` + head + `"}]}`), wantError: true},
		{name: "unsorted sources", body: marker(`{"group":"` + group + `","sources":[{"number":12,"head_sha":"` + head + `"},{"number":11,"head_sha":"` + head + `"}]}`), wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			meta, err := nativePublicationMetadata(test.body)
			if (err != nil) != test.wantError {
				t.Fatalf("metadata error = %v, want error %v", err, test.wantError)
			}
			if test.wantError {
				return
			}
			number := meta["sources"].([]any)[0].(map[string]any)["number"]
			if number != json.Number("12") {
				t.Fatalf("source integer = %#v", number)
			}
		})
	}
}

// TestNativePublicationReplacement checks ready bot ownership and the narrow unlabeled publication window.
func TestNativePublicationReplacement(t *testing.T) {
	group := strings.Repeat("a", 24)
	head := strings.Repeat("b", 40)
	body := `<!-- native-dependabot-maintenance:{"group":"` + group + `","sources":[{"number":1,"head_sha":"` + head + `"}]} -->`
	tests := []struct {
		name      string
		mutate    func(*github.PullRequest)
		merged    bool
		window    bool
		wantError bool
	}{
		{name: "ready owned"},
		{name: "unlabeled rejected", mutate: func(p *github.PullRequest) { p.Labels = []*github.Label{} }, wantError: true},
		{name: "unlabeled publication window", mutate: func(p *github.PullRequest) { p.Labels = []*github.Label{} }, window: true},
		{name: "human robot login", mutate: func(p *github.PullRequest) { p.User.Type = github.Ptr("User") }, wantError: true},
		{name: "foreign author", mutate: func(p *github.PullRequest) { p.User.Login = github.Ptr(nativePublicationBot) }, wantError: true},
		{name: "fork", mutate: func(p *github.PullRequest) { p.Head.Repo.FullName = github.Ptr("other/repo") }, wantError: true},
		{name: "foreign target", mutate: func(p *github.PullRequest) { p.Base.Repo.FullName = github.Ptr("other/repo") }, wantError: true},
		{name: "nonmain", mutate: func(p *github.PullRequest) { p.Base.Ref = github.Ptr("release") }, wantError: true},
		{name: "draft", mutate: func(p *github.PullRequest) { p.Draft = github.Ptr(true) }, wantError: true},
		{name: "missing readiness", mutate: func(p *github.PullRequest) { p.Draft = nil }, wantError: true},
		{name: "missing merge state", mutate: func(p *github.PullRequest) { p.Merged = nil }, wantError: true},
		{name: "missing labels", mutate: func(p *github.PullRequest) { p.Labels = nil }, wantError: true},
		{name: "malformed label", mutate: func(p *github.PullRequest) { p.Labels = []*github.Label{{}} }, wantError: true},
		{name: "wrong title", mutate: func(p *github.PullRequest) { p.Title = github.Ptr("fix(deps): update") }, wantError: true},
		{name: "wrong branch", mutate: func(p *github.PullRequest) { p.Head.Ref = github.Ptr("native-dependabot-other") }, wantError: true},
		{name: "merged predecessor", mutate: func(p *github.PullRequest) { p.State = github.Ptr("closed"); p.Merged = github.Ptr(true) }, merged: true},
		{name: "closed unmerged", mutate: func(p *github.PullRequest) { p.State = github.Ptr("closed") }, merged: true, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pull := &github.PullRequest{
				Number: github.Ptr(4), State: github.Ptr("open"), Draft: github.Ptr(false), Merged: github.Ptr(false),
				User:   &github.User{Login: github.Ptr(nativePublicationRobot), Type: github.Ptr("Bot")},
				Head:   &github.PullRequestBranch{Ref: github.Ptr("native-dependabot-" + group), SHA: github.Ptr(head), Repo: &github.Repository{FullName: github.Ptr("Azure/ARO-HCP")}},
				Base:   &github.PullRequestBranch{Ref: github.Ptr("main"), Repo: &github.Repository{FullName: github.Ptr("Azure/ARO-HCP")}},
				Labels: []*github.Label{{Name: github.Ptr(nativePublicationLabel)}}, Title: github.Ptr("chore(deps): update"), Body: github.Ptr(body),
			}
			if test.mutate != nil {
				test.mutate(pull)
			}
			publisher := &NativePublisher{repository: "Azure/ARO-HCP"}
			_, err := publisher.validateReplacement(pull, test.merged, test.window)
			if (err != nil) != test.wantError {
				t.Fatalf("replacement error = %v, want error %v", err, test.wantError)
			}
		})
	}
}

// TestNativePublicationHolds distinguishes known retries from human, edited and unknown-label holds.
func TestNativePublicationHolds(t *testing.T) {
	sha := strings.Repeat("a", 40)
	timestamp := "2026-01-01T00:00:00Z"
	tests := []struct {
		name        string
		comments    []automation.Comment
		events      []automation.HoldEvent
		held        bool
		wantBlocked bool
		wantError   bool
	}{
		{name: "no holds"},
		{name: "human hold without label", comments: []automation.Comment{{ID: 1, Author: "human", Body: "/hold", CreatedAt: timestamp, UpdatedAt: timestamp}}, wantBlocked: true},
		{name: "unknown hold label", held: true, wantBlocked: true},
		{name: "trusted label without command", held: true, events: []automation.HoldEvent{{ID: 1, Event: "labeled", Actor: policyProwBotForNativeTest, CreatedAt: timestamp}}, wantBlocked: true},
		{name: "bot unexplained hold", comments: []automation.Comment{{ID: 1, Author: nativePublicationRobot, Body: "/hold", CreatedAt: timestamp, UpdatedAt: timestamp}}, wantBlocked: true},
		{name: "trusted retry exhaustion", held: true, events: []automation.HoldEvent{{ID: 1, Event: "labeled", Actor: policyProwBotForNativeTest, CreatedAt: timestamp}}, comments: []automation.Comment{{ID: 1, Author: "openshift-merge-bot[bot]", Body: "/hold\n\nRevision " + sha + " was retested 3 times: holding", CreatedAt: timestamp, UpdatedAt: timestamp}}},
		{name: "retry head absent from branch", comments: []automation.Comment{{ID: 1, Author: "openshift-merge-bot[bot]", Body: "/hold\n\nRevision " + strings.Repeat("b", 40) + " was retested 3 times: holding", CreatedAt: timestamp, UpdatedAt: timestamp}}, wantBlocked: true},
		{name: "edited retry is unexplained", comments: []automation.Comment{{ID: 1, Author: "openshift-merge-bot[bot]", Body: "/hold\n\nRevision " + sha + " was retested 3 times: holding", CreatedAt: timestamp, UpdatedAt: "2026-01-01T00:00:01Z"}}, wantBlocked: true},
		{name: "trusted retry cancellation pending", held: true, events: []automation.HoldEvent{{ID: 1, Event: "labeled", Actor: policyProwBotForNativeTest, CreatedAt: timestamp}}, comments: []automation.Comment{{ID: 1, Author: "openshift-merge-bot[bot]", Body: "/hold\n\nRevision " + sha + " was retested 3 times: holding", CreatedAt: timestamp, UpdatedAt: timestamp}, {ID: 2, Author: nativePublicationRobot, Body: "/hold cancel", CreatedAt: timestamp, UpdatedAt: timestamp}}},
		{name: "own cancellation", comments: []automation.Comment{{ID: 1, Author: "human", Body: "/hold", CreatedAt: timestamp, UpdatedAt: timestamp}, {ID: 2, Author: "human", Body: "/hold cancel", CreatedAt: timestamp, UpdatedAt: timestamp}}},
		{name: "foreign cancellation cannot clear human", comments: []automation.Comment{{ID: 1, Author: "human", Body: "/hold", CreatedAt: timestamp, UpdatedAt: timestamp}, {ID: 2, Author: nativePublicationRobot, Body: "/hold cancel", CreatedAt: timestamp, UpdatedAt: timestamp}}, wantBlocked: true},
		{name: "bad history", held: true, events: []automation.HoldEvent{{ID: 1, Event: "labeled", Actor: policyProwBotForNativeTest, CreatedAt: "bad"}}, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := automation.Snapshot{Commits: []string{sha}, Comments: test.comments, HoldEvents: test.events}
			if test.held {
				snapshot.Pull.Labels = []string{"do-not-merge/hold"}
			}
			blocked, err := nativePublicationBlockedHolds(snapshot)
			if (err != nil) != test.wantError || blocked != test.wantBlocked {
				t.Fatalf("holds = blocked %v, error %v; want blocked %v, error %v", blocked, err, test.wantBlocked, test.wantError)
			}
		})
	}
}

const policyProwBotForNativeTest = "openshift-ci[bot]"
