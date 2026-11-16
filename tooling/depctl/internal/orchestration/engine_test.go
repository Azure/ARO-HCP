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

package orchestration

import (
	"context"
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/forge"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/model"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/policy"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/schema"
)

func testProposal() model.Proposal {
	return model.Proposal{
		ProposalID: "proposal-1",
		Repository: "Azure/ARO-HCP",
		Base:       "main",
		Branch:     "agentic-tool-upgrade-github.com-example-tool-1.1.0",
		Title:      "chore(tools): bump github.com/example/tool to 1.1.0",
		Body:       "<!-- agentic-tool-family:github.com/example/tool -->\n\nbody",
		Labels:     []string{"dependencies", policy.ToolLabel},
		Markers:    []string{"<!-- agentic-tool-family:github.com/example/tool -->"},
		Expected: model.StateExpectation{
			BaseSHA:       strings.Repeat("a", 40),
			BranchHeadSHA: strings.Repeat("b", 40),
		},
	}
}

func TestDeclareReportsOperationsWithoutCandidates(t *testing.T) {
	plan := model.Plan{
		PlanID: "plan-1", BaseSHA: strings.Repeat("a", 40), Outcome: model.OutcomePrepared,
		Families: []model.PlannedFamily{
			{Family: "github.com/example/tool", Kind: model.FamilyGoModule,
				Targets: []model.PinTarget{{ToolName: "tool", Source: ".bingo/tool.mod", From: "1.0.0", To: "1.1.0"}}},
			{Family: "Azure/bicep", Kind: model.FamilyGitHubRelease,
				Targets: []model.PinTarget{{ToolName: "bicep", Source: "dev-infrastructure/openshift-ci/versions.mk",
					Variable: "BICEP_VERSION", From: "0.48.1", To: "0.49.0"}}},
		},
	}
	preview := Declare(plan)
	if len(preview.Operations) != 2 {
		t.Fatalf("operations = %+v", preview.Operations)
	}
	if len(preview.Generators) != 1 || preview.Generators[0] != "bingo-regenerate" {
		t.Fatalf("generators = %v", preview.Generators)
	}
	if !strings.Contains(preview.Note, "no install") {
		t.Fatalf("dry-run note = %q", preview.Note)
	}
}

func TestPublishRequiresAdapters(t *testing.T) {
	engine := New(Deps{})
	if _, err := engine.Publish(context.Background(), testProposal()); err == nil {
		t.Fatal("publish without adapters must fail")
	}
}

func TestPublishRefusesExistingOwnedProposal(t *testing.T) {
	fake := forge.NewFake()
	fake.Seed(forge.PullRequest{
		Number: 42, Author: policy.ToolOwner, State: "open", BaseRef: "main", BaseRepo: "Azure/ARO-HCP",
		HeadRepo: "Azure/ARO-HCP", Title: "chore(tools): bump something to 2.0.0",
		Body: "<!-- agentic-tool-family:something -->",
	}, forge.Snapshot{})
	engine := New(Deps{Forge: fake, Writer: fake})
	if _, err := engine.Publish(context.Background(), testProposal()); err == nil {
		t.Fatal("single-flight must refuse a second tool proposal")
	}
	if len(fake.Mutations()) != 0 {
		t.Fatalf("a refused publication must not mutate: %+v", fake.Mutations())
	}
}

func TestPublishReconcilesLostCreateResponse(t *testing.T) {
	fake := forge.NewFake()
	fake.FailOn("create-pull-request")
	engine := New(Deps{Forge: fake, Writer: fake})
	proposal := testProposal()
	receipt, err := engine.Publish(context.Background(), proposal)
	if err == nil {
		t.Fatal("expected the lost response to surface")
	}
	if receipt.Confirmed {
		t.Fatal("an unconfirmed receipt must not claim success")
	}
	if len(receipt.Unfinished) == 0 {
		t.Fatal("expected an explicit unfinished outcome")
	}

	// The fake did not create a proposal, so a retry must still be refused only
	// when the branch exists without a unique match.
	fake2 := forge.NewFake()
	fake2.FailOn("create-branch")
	engine = New(Deps{Forge: fake2, Writer: fake2})
	recovered, err := engine.Publish(context.Background(), proposal)
	if err != nil {
		t.Fatalf("a lost branch response must reconcile against the observed branch: %v", err)
	}
	if !recovered.Confirmed || recovered.PullRequestNumber == 0 {
		t.Fatalf("unexpected reconciled receipt %+v", recovered)
	}
	creates := 0
	for _, mutation := range fake2.Mutations() {
		if mutation.Operation == "create-branch" {
			creates++
		}
	}
	if creates != 1 {
		t.Fatalf("a lost response must not duplicate the branch: %d attempts", creates)
	}
}

func TestPublishIsRepeatSafe(t *testing.T) {
	fake := forge.NewFake()
	engine := New(Deps{Forge: fake, Writer: fake})
	proposal := testProposal()
	first, err := engine.Publish(context.Background(), proposal)
	if err != nil {
		t.Fatalf("first publish: %v", err)
	}
	if !first.Confirmed || first.PullRequestNumber == 0 {
		t.Fatalf("unexpected first receipt %+v", first)
	}
	second, err := engine.Publish(context.Background(), proposal)
	if err != nil {
		t.Fatalf("second publish: %v", err)
	}
	if second.PullRequestNumber != first.PullRequestNumber {
		t.Fatalf("a repeat must reuse the proposal: %d vs %d", second.PullRequestNumber, first.PullRequestNumber)
	}
	creates := 0
	for _, mutation := range fake.Mutations() {
		if mutation.Operation == "create-pull-request" {
			creates++
		}
	}
	if creates != 1 {
		t.Fatalf("expected exactly one create, got %d", creates)
	}
}

func TestDecideContinuation(t *testing.T) {
	base := ContinuationEvent{
		Repository: "Azure/ARO-HCP", PullRequest: 42, Event: "closed", Merged: true,
		Author: policy.ToolOwner, State: "closed",
		Title:   "chore(tools): bump github.com/example/tool to 1.1.0",
		Body:    "<!-- agentic-tool-family:github.com/example/tool -->",
		Labels:  []string{policy.ToolLabel},
		HeadRef: "agentic-tool-upgrade-tool", HeadRepo: "Azure/ARO-HCP",
		BaseRef: "main", BaseRepo: "Azure/ARO-HCP", Target: "tool-version-updates",
	}
	cases := []struct {
		name   string
		mutate func(*ContinuationEvent)
		want   bool
		reason string
	}{
		{name: "merged owned proposal", want: true},
		{name: "not merged", mutate: func(event *ContinuationEvent) { event.Merged = false }, reason: "closed without merging"},
		{name: "human author", mutate: func(event *ContinuationEvent) { event.Author = "raelga" }, reason: "owned robot identity"},
		{name: "foreign repository", mutate: func(event *ContinuationEvent) { event.HeadRepo = "someone/ARO-HCP" }, reason: "outside the repository"},
		{name: "other base", mutate: func(event *ContinuationEvent) { event.BaseRef = "release-4.20" }, reason: "base ref is not main"},
		{name: "missing markers", mutate: func(event *ContinuationEvent) {
			event.Labels = nil
			event.Body = ""
		}, reason: "tool profile markers"},
		{name: "no target", mutate: func(event *ContinuationEvent) { event.Target = "" }, reason: "target is not configured"},
		{name: "not a close", mutate: func(event *ContinuationEvent) { event.Event = "opened" }, reason: "not a pull-request close"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			event := base
			if testCase.mutate != nil {
				testCase.mutate(&event)
			}
			decision := DecideContinuation(event)
			if decision.Eligible != testCase.want {
				t.Fatalf("eligible = %t, want %t (%s)", decision.Eligible, testCase.want, decision.Reason)
			}
			if testCase.reason != "" && !strings.Contains(decision.Reason, testCase.reason) {
				t.Fatalf("reason = %q, want it to mention %q", decision.Reason, testCase.reason)
			}
		})
	}
}

func TestInvestigatesMissingEvidenceAsIncomplete(t *testing.T) {
	decision := model.MaintenanceDecision{
		Head: strings.Repeat("c", 40),
		CI: model.CIEvidence{Blocked: []model.BlockedCheck{{
			Name: "ci/prow/e2e-parallel", State: "failure", URL: "https://example.com/not-prow",
		}}},
	}
	engine := New(Deps{})
	investigation := engine.Investigate(context.Background(), forge.Snapshot{}, decision, MaintenanceOptions{})
	if investigation.Diagnosis != model.DiagnosisUnknownIncomplete {
		t.Fatalf("diagnosis = %q, want %q", investigation.Diagnosis, model.DiagnosisUnknownIncomplete)
	}
	if len(investigation.Missing) == 0 {
		t.Fatal("missing evidence must be recorded")
	}
	if investigation.Evidence[0].Status != "unsupported" {
		t.Fatalf("non-Prow URLs must be unsupported, got %+v", investigation.Evidence[0])
	}
}

func TestSignatureNormalizesVolatileTokens(t *testing.T) {
	first := Signature("2026-11-05T00:00:00Z error: ManagedNode infra config cache not synchronized (sha 39af3a51dca8b0507907cd9f00c2d33ad659e227)\n")
	second := Signature("2026-11-06T00:00:00Z error: ManagedNode infra config cache not synchronized (sha abcdef1234567)\n")
	if first == "" || first != second {
		t.Fatalf("signatures differ:\n%q\n%q", first, second)
	}
	if Signature("") != "" {
		t.Fatal("an empty log has no signature")
	}
}

func TestPublishRejectsReconciledProposalAtAnotherRevision(t *testing.T) {
	fake := forge.NewFake()
	proposal := testProposal()
	// A previous attempt left a matching proposal at a different revision, and
	// the create response is lost, so reconciliation decides.
	fake.Seed(forge.PullRequest{
		Number: 77, Author: policy.ToolOwner, State: "open", BaseRef: "main", BaseRepo: proposal.Repository,
		HeadRepo: proposal.Repository, HeadRef: proposal.Branch, HeadSHA: strings.Repeat("c", 40),
		Title: proposal.Title, Body: proposal.Body, Labels: proposal.Labels,
	}, forge.Snapshot{})
	fake.FailOn("create-pull-request")
	engine := New(Deps{Forge: fake, Writer: fake})
	receipt, err := engine.Publish(context.Background(), proposal)
	if err == nil {
		t.Fatal("a reconciled proposal at another revision must be refused")
	}
	if receipt.Confirmed {
		t.Fatal("an unconfirmed receipt must not claim success")
	}
}

func TestDeclareUsesEmptyCollections(t *testing.T) {
	preview := Declare(model.Plan{PlanID: "plan-1"})
	encoded, err := schema.Canonical(preview)
	if err != nil {
		t.Fatalf("Canonical() error = %v", err)
	}
	if strings.Contains(string(encoded), "null") {
		t.Fatalf("a preview must not emit null collections: %s", encoded)
	}
}

func TestCarriesTargetRecognizesEachEditKind(t *testing.T) {
	family := model.PlannedFamily{Family: "example.com/tool", Kind: model.FamilyGoModule}
	cases := []struct {
		name    string
		content string
		target  model.PinTarget
		family  model.PlannedFamily
		want    bool
	}{
		{"make variable", "BICEP_VERSION ?= 0.49.0\n", model.PinTarget{Kind: model.EditMakeVariable, Variable: "BICEP_VERSION", To: "0.49.0"}, model.PlannedFamily{}, true},
		{"make variable absent", "BICEP_VERSION ?= 0.48.1\n", model.PinTarget{Kind: model.EditMakeVariable, Variable: "BICEP_VERSION", To: "0.49.0"}, model.PlannedFamily{}, false},
		{"dockerfile arg", "ARG BICEP_VERSION=0.49.0\n", model.PinTarget{Kind: model.EditDockerfileArg, Variable: "BICEP_VERSION", To: "0.49.0"}, model.PlannedFamily{}, true},
		{"bingo module", "require example.com/tool v1.1.0 // cmd/tool\n", model.PinTarget{Kind: model.EditBingoModule, To: "1.1.0"}, family, true},
		{"bingo module stale", "require example.com/tool v1.0.0 // cmd/tool\n", model.PinTarget{Kind: model.EditBingoModule, To: "1.1.0"}, family, false},
		{"unknown kind", "anything\n", model.PinTarget{Kind: "shell", To: "1.1.0"}, family, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := carriesTarget([]byte(testCase.content), testCase.family, testCase.target); got != testCase.want {
				t.Fatalf("carriesTarget() = %t, want %t", got, testCase.want)
			}
		})
	}
}
