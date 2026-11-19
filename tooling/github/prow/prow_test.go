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

package prow_test

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	github "github.com/Azure/ARO-HCP/tooling/github"
	"github.com/Azure/ARO-HCP/tooling/github/prow"
)

const (
	head          = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	otherHead     = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	stamp         = "2026-11-19T12:00:00Z"
	failureURL    = "https://prow.ci.openshift.org/view/gs/test-platform-results/pr-logs/pull/Azure_ARO-HCP/42/e2e/100"
	comparisonURL = "https://prow.ci.openshift.org/view/gs/test-platform-results/logs/e2e/99"
	newRunURL     = "https://prow.ci.openshift.org/view/gs/test-platform-results/pr-logs/pull/Azure_ARO-HCP/42/e2e/101"
	reason        = "Transient infrastructure outage has recovered"
)

type source struct {
	snapshot     github.Snapshot
	reads        int
	writes       int
	bodies       []string
	keys         []string
	onRead       func(*source)
	onPost       func(*source)
	readErr      error
	postErr      error
	receiptActor string
}

func (s *source) Snapshot(ctx context.Context, number int, expectedHead string) (github.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return github.Snapshot{}, err
	}
	s.reads++
	if s.onRead != nil {
		s.onRead(s)
	}
	return s.snapshot, s.readErr
}

func (s *source) PostComment(ctx context.Context, number int, expectedHead, key, body string) (github.Receipt, error) {
	if err := ctx.Err(); err != nil {
		return github.Receipt{}, err
	}
	if s.onPost != nil {
		s.onPost(s)
	}
	if s.snapshot.Pull.HeadSHA != expectedHead {
		return github.Receipt{}, errors.New("source rejected changed head before write")
	}
	if s.postErr != nil {
		return github.Receipt{}, s.postErr
	}
	s.writes++
	s.bodies = append(s.bodies, body)
	s.keys = append(s.keys, key)
	actor := s.receiptActor
	if actor == "" {
		actor = "automation[bot]"
	}
	comment := github.Comment{ID: 100 + s.writes, Author: actor, Body: body + "\n\n" + github.CommentMarker(key, expectedHead), CreatedAt: stamp, UpdatedAt: stamp}
	s.snapshot.Comments = append(s.snapshot.Comments, comment)
	return github.Receipt{Number: number, URL: fmt.Sprintf("https://github.com/Azure/ARO-HCP/pull/%d#issuecomment-%d", number, comment.ID), Author: actor, CommentID: comment.ID, RequestedHead: expectedHead, ObservedHead: expectedHead, Outcome: "observed"}, nil
}

func config() prow.Config {
	return prow.Config{Repository: "Azure/ARO-HCP", Actors: prow.Actors{Writer: "automation[bot]", RetryBots: []string{"merge[bot]"}, Controllers: []string{"prow[bot]"}}, BudgetNamespace: "dependency", RequiredChecks: []string{"ci/prow/e2e"}, JobTriggers: map[string]string{"e2e": "e2e"}}
}

func fixture() *source {
	return &source{snapshot: github.Snapshot{
		Pull:       github.PullRequest{Number: 42, State: "open", HeadSHA: head, HeadRepo: "Azure/ARO-HCP", BaseRepo: "Azure/ARO-HCP", Labels: []string{"do-not-merge/hold"}},
		Commits:    []string{head},
		Comments:   []github.Comment{{ID: 1, Author: "merge[bot]", Body: "/hold\n\nRevision " + head + " was retested 3 times: holding", CreatedAt: stamp, UpdatedAt: stamp}},
		HoldEvents: []github.HoldEvent{{ID: 1, Event: "labeled", Actor: "prow[bot]", CreatedAt: stamp}},
		Statuses:   []github.Status{{ID: 1, Context: "ci/prow/e2e", State: "success", CreatedAt: stamp, TargetURL: failureURL}},
	}}
}

func client(t *testing.T, s *source) *prow.Client {
	t.Helper()
	c, err := prow.New(s, config())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func holdRequest() prow.HoldRequest {
	return prow.HoldRequest{Number: 42, ExpectedHead: head, Reason: reason}
}

func retestRequest() prow.RetestRequest {
	return prow.RetestRequest{Number: 42, ExpectedHead: head, Reason: reason, FailureURL: failureURL, ComparisonURL: comparisonURL}
}

func failing() *source {
	s := fixture()
	s.snapshot.Statuses[0].State = "failure"
	return s
}

func TestReleaseHoldOwnershipAndProvenance(t *testing.T) {
	cases := []struct {
		name   string
		change func(*source)
	}{
		{"human hold", func(s *source) { s.snapshot.Comments[0].Author = "alice" }},
		{"unknown bot", func(s *source) { s.snapshot.Comments[0].Author = "unknown[bot]" }},
		{"unexplained trusted bot", func(s *source) { s.snapshot.Comments[0].Body = "/hold" }},
		{"edited bot", func(s *source) { s.snapshot.Comments[0].UpdatedAt = "2026-11-19T12:01:00Z" }},
		{"missing command timestamp", func(s *source) { s.snapshot.Comments[0].UpdatedAt = "" }},
		{"unrelated revision hold", func(s *source) {
			s.snapshot.Comments[0].Body = strings.ReplaceAll(s.snapshot.Comments[0].Body, head, otherHead)
		}},
		{"unknown revision", func(s *source) { s.snapshot.Commits = nil }},
		{"unknown label actor", func(s *source) { s.snapshot.HoldEvents[0].Actor = "alice" }},
		{"missing label provenance", func(s *source) { s.snapshot.HoldEvents = nil }},
		{"latest label removed", func(s *source) {
			s.snapshot.HoldEvents = append(s.snapshot.HoldEvents, github.HoldEvent{ID: 2, Event: "unlabeled", Actor: "prow[bot]", CreatedAt: stamp})
		}},
		{"conflicting comment page", func(s *source) {
			duplicate := s.snapshot.Comments[0]
			duplicate.Author = "alice"
			s.snapshot.Comments = append(s.snapshot.Comments, duplicate)
		}},
		{"conflicting label page", func(s *source) {
			duplicate := s.snapshot.HoldEvents[0]
			duplicate.Actor = "alice"
			s.snapshot.HoldEvents = append(s.snapshot.HoldEvents, duplicate)
		}},
		{"ambiguous commands", func(s *source) { s.snapshot.Comments[0].Body += "\n/hold cancel" }},
		{"human plus bot", func(s *source) {
			s.snapshot.Comments = append(s.snapshot.Comments, github.Comment{ID: 2, Author: "alice", Body: "/hold for review", CreatedAt: stamp, UpdatedAt: stamp})
		}},
		{"pending CI", func(s *source) { s.snapshot.Statuses[0].State = "pending" }},
		{"failed CI", func(s *source) { s.snapshot.Statuses[0].State = "failure" }},
		{"missing required CI", func(s *source) { s.snapshot.Statuses = nil }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			s := fixture()
			test.change(s)
			_, err := client(t, s).ReleaseHold(context.Background(), holdRequest())
			if err == nil || s.writes != 0 {
				t.Fatalf("unsafe hold release: err=%v writes=%d", err, s.writes)
			}
		})
	}
}

func TestReleaseRequestIsNotObservation(t *testing.T) {
	for _, revision := range []string{head, otherHead} {
		t.Run(revision, func(t *testing.T) {
			s := fixture()
			if revision != head {
				s.snapshot.Commits = append([]string{revision}, s.snapshot.Commits...)
				s.snapshot.Comments[0].Body = strings.ReplaceAll(s.snapshot.Comments[0].Body, head, revision)
			}
			c := client(t, s)
			result, err := c.ReleaseHold(context.Background(), holdRequest())
			if err != nil || result.Receipt.Outcome != "requested" || s.writes != 1 || s.reads != 2 || !strings.HasPrefix(s.bodies[0], "/hold cancel\n\n") {
				t.Fatalf("request result=%+v err=%v reads=%d writes=%d", result, err, s.reads, s.writes)
			}
			s.snapshot.Pull.Labels = nil
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			receipt, err := c.Observe(ctx, result, time.Millisecond)
			if err != nil || receipt.Outcome != "observed" || receipt.CommentID != result.Receipt.CommentID {
				t.Fatalf("observed receipt=%+v err=%v", receipt, err)
			}
		})
	}
}

func TestCancellationReplayPreservesHumanHold(t *testing.T) {
	s := fixture()
	s.snapshot.Comments = append(s.snapshot.Comments,
		github.Comment{ID: 2, Author: "alice", Body: "/hold needs review", CreatedAt: stamp, UpdatedAt: stamp},
		github.Comment{ID: 3, Author: "automation[bot]", Body: "/hold cancel", CreatedAt: stamp, UpdatedAt: stamp})
	if _, err := client(t, s).ReleaseHold(context.Background(), holdRequest()); err == nil || s.writes != 0 {
		t.Fatalf("bot cancellation must not clear human hold: err=%v writes=%d", err, s.writes)
	}
	s.snapshot.Comments = append(s.snapshot.Comments, github.Comment{ID: 4, Author: "alice", Body: "/unhold", CreatedAt: stamp, UpdatedAt: stamp})
	result, err := client(t, s).ReleaseHold(context.Background(), holdRequest())
	if err != nil || result.Receipt.Outcome != "not-applicable" || s.writes != 0 || !strings.Contains(result.Detail, "pending") {
		t.Fatalf("pending cancellation duplicated: result=%+v err=%v writes=%d", result, err, s.writes)
	}
}

func TestReleaseAlreadyAbsentAndDeduplicated(t *testing.T) {
	s := fixture()
	c := client(t, s)
	first, err := c.ReleaseHold(context.Background(), holdRequest())
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.ReleaseHold(context.Background(), holdRequest())
	if err != nil || second.Receipt.Outcome != "duplicate" || second.Receipt.CommentID != first.Receipt.CommentID || s.writes != 1 {
		t.Fatalf("release duplicate result=%+v err=%v writes=%d", second, err, s.writes)
	}
	s = fixture()
	s.snapshot.Pull.Labels = nil
	result, err := client(t, s).ReleaseHold(context.Background(), holdRequest())
	if err != nil || result.Receipt.Outcome != "not-applicable" || s.writes != 0 {
		t.Fatalf("absent hold result=%+v err=%v", result, err)
	}
}

func TestRetestEvidenceAndCI(t *testing.T) {
	cases := []struct {
		name   string
		change func(*source, *prow.RetestRequest)
	}{
		{"success not failure", func(s *source, r *prow.RetestRequest) { s.snapshot.Statuses[0].State = "success" }},
		{"pending required", func(s *source, r *prow.RetestRequest) { s.snapshot.Statuses[0].State = "pending" }},
		{"pending required check", func(s *source, r *prow.RetestRequest) {
			s.snapshot.Statuses = nil
			s.snapshot.Checks = []github.Check{{ID: 2, Name: "ci/prow/e2e", HeadSHA: head, Status: "queued"}}
		}},
		{"neutral required check", func(s *source, r *prow.RetestRequest) {
			s.snapshot.Statuses = nil
			s.snapshot.Checks = []github.Check{{ID: 2, Name: "ci/prow/e2e", HeadSHA: head, Status: "completed", Conclusion: "neutral"}}
		}},
		{"skipped required check", func(s *source, r *prow.RetestRequest) {
			s.snapshot.Statuses = nil
			s.snapshot.Checks = []github.Check{{ID: 2, Name: "ci/prow/e2e", HeadSHA: head, Status: "completed", Conclusion: "skipped"}}
		}},
		{"missing required", func(s *source, r *prow.RetestRequest) { s.snapshot.Statuses[0].Context = "other" }},
		{"missing status id", func(s *source, r *prow.RetestRequest) { s.snapshot.Statuses[0].ID = 0 }},
		{"bad status timestamp", func(s *source, r *prow.RetestRequest) { s.snapshot.Statuses[0].CreatedAt = "yesterday" }},
		{"wrong failure", func(s *source, r *prow.RetestRequest) { r.FailureURL = newRunURL }},
		{"comparison same run", func(s *source, r *prow.RetestRequest) { r.ComparisonURL = failureURL }},
		{"comparison wrong job", func(s *source, r *prow.RetestRequest) {
			r.ComparisonURL = strings.Replace(comparisonURL, "/e2e/", "/unit/", 1)
		}},
		{"comparison untrusted host", func(s *source, r *prow.RetestRequest) {
			r.ComparisonURL = strings.Replace(comparisonURL, "prow.ci.openshift.org", "prow.ci.openshift.org.evil.test", 1)
		}},
		{"comparison URL query", func(s *source, r *prow.RetestRequest) { r.ComparisonURL += "?token=ignored" }},
		{"named job injection", func(s *source, r *prow.RetestRequest) { r.Job = "e2e\n/approve" }},
		{"named wrong job", func(s *source, r *prow.RetestRequest) { r.Job = "unit" }},
		{"old check head", func(s *source, r *prow.RetestRequest) {
			s.snapshot.Checks = []github.Check{{ID: 2, Name: "other", HeadSHA: otherHead, Status: "completed", Conclusion: "success"}}
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			s, request := failing(), retestRequest()
			test.change(s, &request)
			_, err := client(t, s).Retest(context.Background(), request)
			if err == nil || s.writes != 0 {
				t.Fatalf("unsafe recovery err=%v writes=%d", err, s.writes)
			}
		})
	}
}

func TestRecoveryBudgetAndNamespaces(t *testing.T) {
	s := failing()
	c := client(t, s)
	first, err := c.Retest(context.Background(), retestRequest())
	if err != nil || first.Receipt.Outcome != "requested" || s.bodies[0] != "/retest-required\n\nReason: "+reason+"\nFailure: "+failureURL+"\nComparison: "+comparisonURL {
		t.Fatalf("recovery result=%+v err=%v", first, err)
	}
	request := retestRequest()
	request.Job = "e2e"
	request.Reason = "A different diagnosis must not buy another recovery"
	second, err := c.Retest(context.Background(), request)
	if err != nil || second.Receipt.Outcome != "duplicate" || second.Receipt.CommentID != first.Receipt.CommentID || s.writes != 1 {
		t.Fatalf("named job bypassed budget: result=%+v err=%v writes=%d", second, err, s.writes)
	}
	cfg := config()
	cfg.BudgetNamespace = "workflow"
	other, err := prow.New(s, cfg)
	if err != nil {
		t.Fatal(err)
	}
	third, err := other.Retest(context.Background(), request)
	if err != nil || third.Receipt.Outcome != "requested" || s.writes != 2 || !strings.HasPrefix(s.bodies[1], "/test e2e\n") || s.keys[0] == s.keys[1] {
		t.Fatalf("independent profile budget lost: result=%+v err=%v writes=%d", third, err, s.writes)
	}
}

func TestEditedAndAmbiguousRecoveryMetadataFailClosed(t *testing.T) {
	for _, change := range []func(*github.Comment){
		func(comment *github.Comment) { comment.UpdatedAt = "2026-11-19T12:01:00Z" },
		func(comment *github.Comment) {
			comment.Body = "/approve\n\n" + github.CommentMarker("prow/dependency/retest", head)
		},
		func(comment *github.Comment) {
			comment.Body += "\n" + github.CommentMarker("prow/dependency/retest", head)
		},
	} {
		s := failing()
		c := client(t, s)
		if _, err := c.Retest(context.Background(), retestRequest()); err != nil {
			t.Fatal(err)
		}
		change(&s.snapshot.Comments[len(s.snapshot.Comments)-1])
		if _, err := c.Retest(context.Background(), retestRequest()); err == nil || s.writes != 1 {
			t.Fatalf("ambiguous prior recovery reused budget: err=%v writes=%d", err, s.writes)
		}
	}
}

func TestObservationNeedsNewSameJobRunNotNewStatusID(t *testing.T) {
	s := failing()
	c := client(t, s)
	result, err := c.Retest(context.Background(), retestRequest())
	if err != nil {
		t.Fatal(err)
	}
	s.onRead = func(s *source) {
		s.snapshot.Statuses[0].ID++
		if s.reads == 4 {
			s.snapshot.Statuses[0].TargetURL = strings.Replace(newRunURL, "/e2e/", "/unit/", 1)
		}
		if s.reads >= 5 {
			s.snapshot.Statuses[0].TargetURL = newRunURL
			s.snapshot.Statuses[0].State = "pending"
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	receipt, err := c.Observe(ctx, result, time.Millisecond)
	if err != nil || receipt.Outcome != "observed" || s.reads != 5 || s.writes != 1 {
		t.Fatalf("new-run observation receipt=%+v err=%v reads=%d writes=%d", receipt, err, s.reads, s.writes)
	}
}

func TestObservationTimeoutIsIndeterminate(t *testing.T) {
	s := failing()
	c := client(t, s)
	result, err := c.Retest(context.Background(), retestRequest())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Observe(context.Background(), result, time.Millisecond); err == nil {
		t.Fatal("unbounded observation accepted")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	receipt, err := c.Observe(ctx, result, time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) || receipt.Outcome != "indeterminate" || s.writes != 1 {
		t.Fatalf("timeout must not claim effect: receipt=%+v err=%v", receipt, err)
	}
}

func TestHeadAndPolicyRacesPreventWrites(t *testing.T) {
	for _, race := range []string{"initial head", "second read head", "new pending CI", "new human hold", "root prewrite head"} {
		t.Run(race, func(t *testing.T) {
			s := fixture()
			s.onRead = func(s *source) {
				if race == "initial head" || race == "second read head" && s.reads == 2 {
					s.snapshot.Pull.HeadSHA = otherHead
				}
				if s.reads == 2 && race == "new pending CI" {
					s.snapshot.Statuses[0].State = "pending"
				}
				if s.reads == 2 && race == "new human hold" {
					s.snapshot.Comments = append(s.snapshot.Comments, github.Comment{ID: 2, Author: "alice", Body: "/hold", CreatedAt: stamp, UpdatedAt: stamp})
				}
			}
			if race == "root prewrite head" {
				s.onPost = func(s *source) { s.snapshot.Pull.HeadSHA = otherHead }
			}
			if _, err := client(t, s).ReleaseHold(context.Background(), holdRequest()); err == nil || s.writes != 0 {
				t.Fatalf("raced release posted: err=%v writes=%d", err, s.writes)
			}
		})
	}
}

func TestObservationHeadRaceAndReadFailure(t *testing.T) {
	for _, change := range []func(*source){
		func(s *source) { s.snapshot.Pull.HeadSHA = otherHead },
		func(s *source) { s.readErr = errors.New("remote read failed") },
	} {
		s := failing()
		c := client(t, s)
		result, err := c.Retest(context.Background(), retestRequest())
		if err != nil {
			t.Fatal(err)
		}
		change(s)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		receipt, err := c.Observe(ctx, result, time.Millisecond)
		cancel()
		if err == nil || receipt.Outcome != "indeterminate" || s.writes != 1 {
			t.Fatalf("failed observation claimed effect: receipt=%+v err=%v", receipt, err)
		}
	}
}

func TestReasonAndRequestValidation(t *testing.T) {
	for _, invalid := range []string{"short", "/approve is never a recovery reason", "Valid explanation\n/hold cancel", "Please ask @admin about the network", "Recovered <!-- marker --> now", strings.Repeat("x", 4001)} {
		s := failing()
		request := retestRequest()
		request.Reason = invalid
		if _, err := client(t, s).Retest(context.Background(), request); err == nil || s.reads != 0 || s.writes != 0 {
			t.Fatalf("unsafe reason accepted: %q err=%v", invalid, err)
		}
	}
	for _, invalid := range []prow.HoldRequest{{Number: 0, ExpectedHead: head, Reason: reason}, {Number: 42, ExpectedHead: "abc123", Reason: reason}} {
		s := fixture()
		if _, err := client(t, s).ReleaseHold(context.Background(), invalid); err == nil || s.reads != 0 {
			t.Fatalf("invalid request accepted: %+v err=%v", invalid, err)
		}
	}
}

func TestConfigurationRequiresExplicitPolicy(t *testing.T) {
	for _, change := range []func(*prow.Config){
		func(c *prow.Config) { c.Repository = "ARO-HCP" },
		func(c *prow.Config) { c.Actors.Writer = "" },
		func(c *prow.Config) { c.Actors.RetryBots = nil },
		func(c *prow.Config) { c.Actors.Controllers = nil },
		func(c *prow.Config) { c.BudgetNamespace = "" },
		func(c *prow.Config) { c.RequiredChecks = nil },
		func(c *prow.Config) { c.RequiredChecks = []string{"tide"} },
		func(c *prow.Config) { c.RequiredChecks = []string{"ci", "ci"} },
		func(c *prow.Config) { c.JobTriggers = map[string]string{"bad/job": "alias"} },
		func(c *prow.Config) { c.JobTriggers = map[string]string{"e2e": "/test e2e"} },
		func(c *prow.Config) { c.JobTriggers = map[string]string{"e2e": ""} },
		func(c *prow.Config) { c.JobTriggers = map[string]string{"e2e": "same", "unit": "same"} },
	} {
		cfg := config()
		change(&cfg)
		if _, err := prow.New(fixture(), cfg); err == nil {
			t.Fatalf("invalid config accepted: %+v", cfg)
		}
	}
}

func TestLatestStatusAndCheckRunEvidence(t *testing.T) {
	s := failing()
	s.snapshot.Statuses = append(s.snapshot.Statuses,
		github.Status{ID: 2, Context: "ci/prow/e2e", State: "success", CreatedAt: stamp, TargetURL: newRunURL},
		github.Status{ID: 3, Context: "tide", State: "pending", CreatedAt: stamp})
	if _, err := client(t, s).ReleaseHold(context.Background(), holdRequest()); err != nil {
		t.Fatal(err)
	}
	s = failing()
	s.snapshot.Checks = []github.Check{{ID: 10, Name: "ci/prow/e2e", HeadSHA: head, Status: "completed", Conclusion: "failure", DetailsURL: failureURL}}
	s.snapshot.Statuses = nil
	if _, err := client(t, s).Retest(context.Background(), retestRequest()); err != nil {
		t.Fatal(err)
	}
}

func TestContextAndSourceFailuresPropagate(t *testing.T) {
	s := failing()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client(t, s).Retest(ctx, retestRequest()); !errors.Is(err, context.Canceled) || s.writes != 0 || s.reads != 0 {
		t.Fatalf("cancelled request performed IO: err=%v reads=%d writes=%d", err, s.reads, s.writes)
	}
	s = failing()
	s.postErr = errors.New("ambiguous remote publication failure")
	if _, err := client(t, s).Retest(context.Background(), retestRequest()); !errors.Is(err, s.postErr) {
		t.Fatalf("publication error lost: %v", err)
	}
}

func TestRecoveryBudgetResetsOnlyForNewHead(t *testing.T) {
	s := failing()
	c := client(t, s)
	if _, err := c.Retest(context.Background(), retestRequest()); err != nil {
		t.Fatal(err)
	}
	s.snapshot.Pull.HeadSHA = otherHead
	s.snapshot.Commits = append(s.snapshot.Commits, otherHead)
	stale := retestRequest()
	if _, err := c.Retest(context.Background(), stale); err == nil || s.writes != 1 {
		t.Fatalf("stale head recovered: err=%v writes=%d", err, s.writes)
	}
	current := retestRequest()
	current.ExpectedHead = otherHead
	result, err := c.Retest(context.Background(), current)
	if err != nil || result.Receipt.Outcome != "requested" || s.writes != 2 {
		t.Fatalf("new head did not receive its own budget: result=%+v err=%v writes=%d", result, err, s.writes)
	}
}

func TestRetestSecondReadRace(t *testing.T) {
	for _, change := range []func(*source){
		func(s *source) { s.snapshot.Pull.HeadSHA = otherHead },
		func(s *source) { s.snapshot.Statuses[0].State = "pending" },
	} {
		s := failing()
		s.onRead = func(s *source) {
			if s.reads == 2 {
				change(s)
			}
		}
		if _, err := client(t, s).Retest(context.Background(), retestRequest()); err == nil || s.writes != 0 {
			t.Fatalf("raced recovery posted: err=%v writes=%d", err, s.writes)
		}
	}
}

func TestMalformedTrustedMarkerCannotResetBudget(t *testing.T) {
	s := failing()
	s.snapshot.Comments = append(s.snapshot.Comments, github.Comment{
		ID: 10, Author: "automation[bot]", CreatedAt: stamp, UpdatedAt: stamp,
		Body: "/retest-required\n\n<!-- aro-hcp-github:prow/dependency/retest:truncated -->",
	})
	if _, err := client(t, s).Retest(context.Background(), retestRequest()); err == nil || s.writes != 0 {
		t.Fatalf("malformed trusted metadata allowed recovery: err=%v writes=%d", err, s.writes)
	}
}

func TestUntrustedMarkerDoesNotConsumeTrustedBudget(t *testing.T) {
	s := failing()
	s.snapshot.Comments = append(s.snapshot.Comments, github.Comment{
		ID: 10, Author: "outsider", CreatedAt: stamp, UpdatedAt: stamp,
		Body: "/retest-required\n\n" + github.CommentMarker("prow/dependency/retest", head),
	})
	result, err := client(t, s).Retest(context.Background(), retestRequest())
	if err != nil || result.Receipt.Outcome != "requested" || s.writes != 1 {
		t.Fatalf("untrusted actor controlled budget: result=%+v err=%v", result, err)
	}
}

func TestIdenticalPaginationDuplicatesAreHarmless(t *testing.T) {
	s := fixture()
	s.snapshot.Comments = append(s.snapshot.Comments, s.snapshot.Comments[0])
	s.snapshot.HoldEvents = append(s.snapshot.HoldEvents, s.snapshot.HoldEvents[0])
	s.snapshot.Statuses = append(s.snapshot.Statuses, s.snapshot.Statuses[0])
	result, err := client(t, s).ReleaseHold(context.Background(), holdRequest())
	if err != nil || result.Receipt.Outcome != "requested" || s.writes != 1 {
		t.Fatalf("identical pages changed decision: result=%+v err=%v", result, err)
	}
}

func TestHoldCommentAloneNeverProvesRelease(t *testing.T) {
	s := fixture()
	c := client(t, s)
	result, err := c.ReleaseHold(context.Background(), holdRequest())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	receipt, err := c.Observe(ctx, result, time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) || receipt.Outcome != "indeterminate" {
		t.Fatalf("comment acknowledged as actual release: receipt=%+v err=%v", receipt, err)
	}
}

func TestReceiptActorMismatchIsIndeterminate(t *testing.T) {
	s := failing()
	s.receiptActor = "unexpected[bot]"
	result, err := client(t, s).Retest(context.Background(), retestRequest())
	if err == nil || result.Receipt.Outcome != "indeterminate" {
		t.Fatalf("unexpected writer accepted: result=%+v err=%v", result, err)
	}
}

func TestPublicationErrorPreservesIndeterminateRequest(t *testing.T) {
	s := failing()
	s.postErr = errors.New("connection lost after remote request")
	result, err := client(t, s).Retest(context.Background(), retestRequest())
	if !errors.Is(err, s.postErr) || result.Receipt.Outcome != "indeterminate" || result.Receipt.Number != 42 || result.Receipt.RequestedHead != head {
		t.Fatalf("uncertain publication identity lost: result=%+v err=%v", result, err)
	}
}

func TestUnknownCrossAuthorCancellationDoesNotTriggerDuplicate(t *testing.T) {
	s := fixture()
	s.snapshot.Comments = append(s.snapshot.Comments, github.Comment{
		ID: 2, Author: "unknown", Body: "/hold cancel", CreatedAt: stamp, UpdatedAt: stamp,
	})
	if _, err := client(t, s).ReleaseHold(context.Background(), holdRequest()); err == nil || s.writes != 0 {
		t.Fatalf("unknown pending cancellation triggered another: err=%v writes=%d", err, s.writes)
	}
}

func TestNamedCanonicalJobUsesTrustedTriggerAlias(t *testing.T) {
	const job = "pull-ci-Azure-ARO-HCP-main-api-validation"
	s := failing()
	request := retestRequest()
	request.Job = job
	request.FailureURL = strings.ReplaceAll(failureURL, "/e2e/", "/"+job+"/")
	request.ComparisonURL = strings.ReplaceAll(comparisonURL, "/e2e/", "/"+job+"/")
	s.snapshot.Statuses[0].Context = "ci/prow/api-validation"
	s.snapshot.Statuses[0].TargetURL = request.FailureURL
	cfg := config()
	cfg.RequiredChecks = []string{"ci/prow/api-validation"}
	cfg.JobTriggers = map[string]string{job: "api-validation"}
	c, err := prow.New(s, cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.JobTriggers[job] = "mutated-after-construction"
	result, err := c.Retest(context.Background(), request)
	if err != nil || result.Receipt.Outcome != "requested" || s.writes != 1 {
		t.Fatalf("canonical job request result=%+v err=%v writes=%d", result, err, s.writes)
	}
	trigger := regexp.MustCompile(`(?m)^/test( | .* )api-validation,?($|\s.*)`)
	if !trigger.MatchString(s.bodies[0]) || !strings.HasPrefix(s.bodies[0], "/test api-validation\n") {
		t.Fatalf("command does not match configured Prow trigger: %q", s.bodies[0])
	}
	resumedClient := clientWithConfig(t, s, configForJob(job))
	resumed, err := resumedClient.Retest(context.Background(), request)
	if err != nil || resumed.Receipt.Outcome != "duplicate" || s.writes != 1 {
		t.Fatalf("alias could not resume canonical job: result=%+v err=%v", resumed, err)
	}
	s.snapshot.Statuses[0].TargetURL = strings.ReplaceAll(newRunURL, "/e2e/", "/"+job+"/")
	s.snapshot.Statuses[0].State = "pending"
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	receipt, err := resumedClient.Observe(ctx, resumed, time.Millisecond)
	if err != nil || receipt.Outcome != "observed" {
		t.Fatalf("resumed alias did not target canonical job: receipt=%+v err=%v", receipt, err)
	}
}

func configForJob(job string) prow.Config {
	cfg := config()
	cfg.RequiredChecks = []string{"ci/prow/api-validation"}
	cfg.JobTriggers = map[string]string{job: "api-validation"}
	return cfg
}

func clientWithConfig(t *testing.T, s *source, cfg prow.Config) *prow.Client {
	t.Helper()
	c, err := prow.New(s, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestNamedRetestRequiresMappingBeforeIO(t *testing.T) {
	for _, triggers := range []map[string]string{nil, {"other": "e2e"}} {
		s := failing()
		cfg := config()
		cfg.JobTriggers = triggers
		request := retestRequest()
		request.Job = "e2e"
		_, err := clientWithConfig(t, s, cfg).Retest(context.Background(), request)
		if err == nil || s.reads != 0 || s.writes != 0 {
			t.Fatalf("missing mapping performed IO: err=%v reads=%d writes=%d", err, s.reads, s.writes)
		}
	}
}

func TestPriorNamedTriggerMustRemainTrusted(t *testing.T) {
	for _, triggers := range []map[string]string{nil, {"e2e": "different-alias"}} {
		s := failing()
		request := retestRequest()
		request.Job = "e2e"
		if _, err := client(t, s).Retest(context.Background(), request); err != nil {
			t.Fatal(err)
		}
		cfg := config()
		cfg.JobTriggers = triggers
		// A required-set retry must not bypass an unknown prior named alias.
		_, err := clientWithConfig(t, s, cfg).Retest(context.Background(), retestRequest())
		if err == nil || s.writes != 1 {
			t.Fatalf("unknown prior alias accepted: err=%v writes=%d", err, s.writes)
		}
	}
}

func TestObservationTargetsExcludeHistoricalJobs(t *testing.T) {
	for _, named := range []bool{false, true} {
		for _, mode := range []string{"posted", "duplicate", "resumed", "duplicate-after-job-change", "resumed-after-job-change"} {
			for _, observedJob := range []string{"e2e", "old-job"} {
				t.Run(fmt.Sprintf("named=%t/%s/new=%s", named, mode, observedJob), func(t *testing.T) {
					s := failing()
					s.snapshot.Statuses = append(s.snapshot.Statuses, github.Status{
						ID: 2, Context: "ci/prow/e2e", State: "failure", CreatedAt: "2026-11-19T11:00:00Z",
						TargetURL: strings.ReplaceAll(strings.ReplaceAll(failureURL, "/e2e/", "/old-job/"), "/100", "/80"),
					})
					request := retestRequest()
					if named {
						request.Job = "e2e"
					}
					c := client(t, s)
					result, err := c.Retest(context.Background(), request)
					if err != nil {
						t.Fatal(err)
					}
					if strings.HasSuffix(mode, "-after-job-change") {
						s.snapshot.Statuses = append(s.snapshot.Statuses, github.Status{
							ID: 3, Context: "ci/prow/e2e", State: "pending", CreatedAt: "2026-11-19T12:01:00Z",
							TargetURL: strings.ReplaceAll(strings.ReplaceAll(failureURL, "/e2e/", "/old-job/"), "/100", "/81"),
						})
					}
					if mode != "posted" {
						if strings.HasPrefix(mode, "resumed") {
							c = client(t, s)
						}
						result, err = c.Retest(context.Background(), request)
						if err != nil || result.Receipt.Outcome != "duplicate" {
							t.Fatalf("duplicate result=%+v err=%v", result, err)
						}
					}
					s.snapshot.Statuses = append(s.snapshot.Statuses, github.Status{
						ID: 4, Context: "ci/prow/e2e", State: "pending", CreatedAt: "2026-11-19T12:02:00Z",
						TargetURL: strings.ReplaceAll(newRunURL, "/e2e/", "/"+observedJob+"/"),
					})
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
					defer cancel()
					receipt, err := c.Observe(ctx, result, time.Millisecond)
					if observedJob == "e2e" {
						if err != nil || receipt.Outcome != "observed" {
							t.Fatalf("new targeted run not observed: receipt=%+v err=%v", receipt, err)
						}
					} else if !errors.Is(err, context.DeadlineExceeded) || receipt.Outcome != "indeterminate" {
						t.Fatalf("historical job falsely proved execution: receipt=%+v err=%v", receipt, err)
					}
					if s.writes != 1 {
						t.Fatalf("observation or duplicate posted again: writes=%d", s.writes)
					}
				})
			}
		}
	}
}

func TestOptionalCIDoesNotBlockRequiredRecoveryOrRelease(t *testing.T) {
	for _, state := range []string{"neutral", "skipped", "failure", "pending", "unknown"} {
		for _, action := range []string{"release", "retest"} {
			t.Run(action+"/"+state, func(t *testing.T) {
				s := fixture()
				if action == "retest" {
					s = failing()
				}
				s.snapshot.Statuses = append(s.snapshot.Statuses, github.Status{ID: 30, Context: "optional", State: state, CreatedAt: stamp})
				s.snapshot.Checks = []github.Check{{ID: 31, Name: "CodeQL", HeadSHA: head, Status: "completed", Conclusion: state}}
				if state == "pending" {
					s.snapshot.Checks[0].Status = "queued"
				}
				c := client(t, s)
				var result prow.Result
				var err error
				if action == "retest" {
					result, err = c.Retest(context.Background(), retestRequest())
				} else {
					result, err = c.ReleaseHold(context.Background(), holdRequest())
				}
				if err != nil || s.writes != 1 || result.Receipt.Outcome != "requested" {
					t.Fatalf("optional CI blocked valid required-check action: result=%+v err=%v writes=%d", result, err, s.writes)
				}
			})
		}
	}
}
