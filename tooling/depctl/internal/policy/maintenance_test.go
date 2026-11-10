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

package policy

import (
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/forge"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/model"
)

const (
	headSHA      = "39af3a51dca8b0507907cd9f00c2d33ad659e227"
	timeFixture  = "2026-11-05T15:00:00Z"
	timeLater    = "2026-11-05T16:00:00Z"
	timeLaterYet = "2026-11-05T17:00:00Z"
)

func robotToolPull() forge.PullRequest {
	return forge.PullRequest{
		Number: 7892, Author: Robot, State: "open", Title: "chore(tools): bump github.com/example/tool to 1.2.0",
		Body:    "<!-- agentic-tool-family:github.com/example/tool -->",
		Labels:  []string{ToolLabel},
		HeadRef: "agentic-tool-upgrade-tool-1.2.0", HeadSHA: headSHA,
		HeadRepo: "Azure/ARO-HCP", BaseRef: "main", BaseRepo: "Azure/ARO-HCP",
	}
}

func TestOwnedKindRequiresFullIdentity(t *testing.T) {
	pull := robotToolPull()
	kind, err := OwnedKind(PullRequest{
		Number: pull.Number, Author: pull.Author, State: pull.State, Base: pull.BaseRef,
		HeadRef: pull.HeadRef, Title: pull.Title, Body: pull.Body, Labels: pull.Labels,
		HeadRepo: pull.HeadRepo, BaseRepo: pull.BaseRepo,
	}, "Azure/ARO-HCP")
	if err != nil || kind != KindTool {
		t.Fatalf("OwnedKind() = %q, %v", kind, err)
	}
	forked := pull
	forked.HeadRepo = "someone/ARO-HCP"
	if kind, _ := OwnedKind(PullRequest{
		Author: forked.Author, Base: forked.BaseRef, HeadRef: forked.HeadRef,
		Title: forked.Title, Body: forked.Body, Labels: forked.Labels,
		HeadRepo: forked.HeadRepo, BaseRepo: forked.BaseRepo,
	}, "Azure/ARO-HCP"); kind != "" {
		t.Fatalf("forked head must not be owned, got %q", kind)
	}
	human := pull
	human.Author = "raelga"
	if kind, _ := OwnedKind(PullRequest{
		Author: human.Author, Base: human.BaseRef, HeadRef: human.HeadRef,
		Title: human.Title, Body: human.Body, Labels: human.Labels,
		HeadRepo: human.HeadRepo, BaseRepo: human.BaseRepo,
	}, "Azure/ARO-HCP"); kind != "" {
		t.Fatalf("human author must not be owned, got %q", kind)
	}
}

func TestActiveHoldsReplaysCommands(t *testing.T) {
	commits := map[string]bool{headSHA: true}
	retryBody := "/hold\n\nRevision " + headSHA + " was retested 3 times: holding\n"

	cases := []struct {
		name        string
		comments    []forge.Comment
		wantKinds   []string
		wantPending bool
	}{
		{
			name: "retry exhaustion hold",
			comments: []forge.Comment{{
				ID: 1, Author: MergeBot, Body: retryBody, CreatedAt: timeFixture, UpdatedAt: timeFixture,
			}},
			wantKinds: []string{"retry-exhaustion"},
		},
		{
			name: "human hold is preserved",
			comments: []forge.Comment{{
				ID: 1, Author: "reviewer", Body: "/hold until tests pass", CreatedAt: timeFixture, UpdatedAt: timeFixture,
			}},
			wantKinds: []string{"human"},
		},
		{
			name: "bot cannot cancel a human hold",
			comments: []forge.Comment{
				{ID: 1, Author: "reviewer", Body: "/hold", CreatedAt: timeFixture, UpdatedAt: timeFixture},
				{ID: 2, Author: Robot, Body: "/hold cancel", CreatedAt: timeLater, UpdatedAt: timeLater},
			},
			wantKinds: []string{"human"},
		},
		{
			name: "edited retry hold is unexplained",
			comments: []forge.Comment{{
				ID: 1, Author: MergeBot, Body: retryBody, CreatedAt: timeFixture, UpdatedAt: timeLater,
			}},
			wantKinds: []string{"unexplained"},
		},
		{
			name: "retry hold with unknown sha is unexplained",
			comments: []forge.Comment{{
				ID: 1, Author: MergeBot,
				Body:      "/hold\n\nRevision " + strings.Repeat("a", 40) + " was retested 2 times: holding\n",
				CreatedAt: timeFixture, UpdatedAt: timeFixture,
			}},
			wantKinds: []string{"unexplained"},
		},
		{
			name: "author cancels own hold",
			comments: []forge.Comment{
				{ID: 1, Author: "reviewer", Body: "/hold", CreatedAt: timeFixture, UpdatedAt: timeFixture},
				{ID: 2, Author: "reviewer", Body: "/unhold", CreatedAt: timeLater, UpdatedAt: timeLater},
			},
		},
		{
			name: "cancel wins inside one comment",
			comments: []forge.Comment{{
				ID: 1, Author: "reviewer", Body: "/hold\n/hold cancel", CreatedAt: timeFixture, UpdatedAt: timeFixture,
			}},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			holds, pending, err := ActiveHolds(testCase.comments, commits)
			if err != nil {
				t.Fatalf("ActiveHolds() error = %v", err)
			}
			var kinds []string
			for _, hold := range holds {
				kinds = append(kinds, hold.Kind)
			}
			if strings.Join(kinds, ",") != strings.Join(testCase.wantKinds, ",") {
				t.Fatalf("hold kinds = %v, want %v", kinds, testCase.wantKinds)
			}
			if pending != testCase.wantPending {
				t.Fatalf("pending = %t, want %t", pending, testCase.wantPending)
			}
		})
	}
}

func TestCIEvidenceRequiresPositiveCurrentHeadResults(t *testing.T) {
	statuses := []forge.Status{
		{ID: 1, Context: "tide", State: "failure", CreatedAt: timeFixture},
		{ID: 2, Context: "ci/prow/unit", State: "success", CreatedAt: timeFixture},
		{ID: 3, Context: "ci/prow/unit", State: "pending", CreatedAt: timeLater},
	}
	checks := []forge.Check{
		{ID: 1, Name: "ci/prow/e2e-parallel", HeadSHA: headSHA, Status: "completed", Conclusion: "skipped"},
	}
	evidence, err := CIEvidence(statuses, checks, headSHA)
	if err != nil {
		t.Fatalf("CIEvidence() error = %v", err)
	}
	if len(evidence.Positive) != 0 {
		t.Fatalf("old or neutral results must not be positive: %v", evidence.Positive)
	}
	for _, blocked := range evidence.Blocked {
		if blocked.Name == "ci/prow/e2e-parallel" && !blocked.Pending && blocked.State == "skipped" {
			return
		}
	}
	t.Fatalf("a skipped required check must be reported as blocked: %+v", evidence.Blocked)
}

func TestCIEvidenceRejectsForeignHeadCheck(t *testing.T) {
	checks := []forge.Check{{ID: 1, Name: "ci/prow/e2e-parallel", HeadSHA: strings.Repeat("b", 40), Status: "completed", Conclusion: "success"}}
	if _, err := CIEvidence(nil, checks, headSHA); err == nil {
		t.Fatal("expected a foreign-head check-run error")
	}
}

func TestAssessEligibleReleaseRequiresGreenRequiredChecks(t *testing.T) {
	pull := robotToolPull()
	pull.Labels = append(pull.Labels, HoldLabel)
	snapshot := forge.Snapshot{
		Pull:    pull,
		Commits: []string{headSHA},
		Comments: []forge.Comment{{
			ID: 1, Author: MergeBot,
			Body:      "/hold\n\nRevision " + headSHA + " was retested 3 times: holding\n",
			CreatedAt: timeFixture, UpdatedAt: timeFixture,
		}},
		HoldEvents: []forge.HoldEvent{{ID: 1, Event: "labeled", Actor: ProwBot, CreatedAt: timeFixture}},
		Statuses: []forge.Status{
			{ID: 1, Context: "ci/pipeline-gate", State: "success", CreatedAt: timeFixture},
			{ID: 2, Context: "ci/prow/e2e-parallel", State: "success", CreatedAt: timeFixture},
		},
	}
	decision, err := Assess("Azure/ARO-HCP", pull.Number, headSHA, snapshot)
	if err != nil {
		t.Fatalf("Assess() error = %v", err)
	}
	if !decision.CanReleaseHold || decision.ReleaseStatus != ReleaseEligible {
		t.Fatalf("expected an eligible release, got %+v (%v)", decision.ReleaseStatus, decision.ReleaseBlockers)
	}
	if decision.Action != "release-hold" {
		t.Fatalf("expected release-hold action, got %q", decision.Action)
	}

	snapshot.Statuses[1].State = "failure"
	blocked, err := Assess("Azure/ARO-HCP", pull.Number, headSHA, snapshot)
	if err != nil {
		t.Fatalf("Assess() error = %v", err)
	}
	if blocked.CanReleaseHold {
		t.Fatal("a failing required check must block release")
	}
	if blocked.Action != "investigate" {
		t.Fatalf("expected investigate action, got %q", blocked.Action)
	}
}

func TestAssessConsumesPerHeadRecoveryBudget(t *testing.T) {
	pull := robotToolPull()
	pull.Labels = append(pull.Labels, HoldLabel)
	retry := forge.Comment{
		ID: 1, Author: MergeBot,
		Body:      "/hold\n\nRevision " + headSHA + " was retested 3 times: holding\n",
		CreatedAt: timeFixture, UpdatedAt: timeFixture,
	}
	marker := forge.Comment{
		ID: 2, Author: Robot, Body: "<!-- " + RecoveryMarker + headSHA + " -->\nrecovered once",
		CreatedAt: timeLater, UpdatedAt: timeLater,
	}
	snapshot := forge.Snapshot{
		Pull: pull, Commits: []string{headSHA},
		Comments:   []forge.Comment{retry, marker},
		HoldEvents: []forge.HoldEvent{{ID: 1, Event: "labeled", Actor: ProwBot, CreatedAt: timeFixture}},
		Statuses: []forge.Status{
			{ID: 1, Context: "ci/prow/e2e-parallel", State: "failure", CreatedAt: timeLaterYet,
				TargetURL: "https://prow.ci.openshift.org/view/gs/test-platform-results-public/pr-logs/pull/Azure_ARO-HCP/7892/job/1"},
		},
	}
	decision, err := Assess("Azure/ARO-HCP", pull.Number, headSHA, snapshot)
	if err != nil {
		t.Fatalf("Assess() error = %v", err)
	}
	if decision.CanRecoverRetry {
		t.Fatalf("a consumed budget must block recovery: %v", decision.RetryBlockers)
	}
	if !strings.Contains(strings.Join(decision.RetryBlockers, " "), "already consumed") {
		t.Fatalf("expected the consumed-budget blocker, got %v", decision.RetryBlockers)
	}
}

func TestRecoveryEvidenceRequiresFailureAndTrustedComparison(t *testing.T) {
	decision := model.MaintenanceDecision{
		CI: model.CIEvidence{Blocked: []model.BlockedCheck{{
			Name:  "ci/prow/e2e-parallel",
			State: "failure",
			URL:   "https://prow.ci.openshift.org/view/gs/test-platform-results-public/pr-logs/pull/Azure_ARO-HCP/7892/job/1",
		}}},
	}
	if err := RecoveryEvidence("https://prow.ci.openshift.org/view/gs/test-platform-results-public/pr-logs/pull/Azure_ARO-HCP/7892/job/1 is the failure", decision); err == nil {
		t.Fatal("expected a comparison refusal")
	}
	evidence := "https://prow.ci.openshift.org/view/gs/test-platform-results-public/pr-logs/pull/Azure_ARO-HCP/7892/job/1 " +
		"failed identically to https://prow.ci.openshift.org/view/gs/test-platform-results-public/logs/periodic/job/2"
	if err := RecoveryEvidence(evidence, decision); err != nil {
		t.Fatalf("RecoveryEvidence() error = %v", err)
	}
	if err := RecoveryEvidence(evidence+"\n/hold cancel", decision); err == nil {
		t.Fatal("commands in evidence must be refused")
	}
}
