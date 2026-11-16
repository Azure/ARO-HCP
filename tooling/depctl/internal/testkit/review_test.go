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

package testkit

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/consumers"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/forge"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/model"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/orchestration"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/policy"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/repository"
)

// preparePlan builds a fixture and a plan for one explicit target.
func preparePlan(t *testing.T, target string) (*Fixture, model.Plan) {
	t.Helper()
	ctx := context.Background()
	fixture, err := CreateFixture(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("CreateFixture() error = %v", err)
	}
	if _, err := fixture.AddBareRemote(ctx, filepath.Join(t.TempDir(), "remote.git")); err != nil {
		t.Fatalf("AddBareRemote() error = %v", err)
	}
	registry, err := FixtureRegistry()
	if err != nil {
		t.Fatalf("FixtureRegistry() error = %v", err)
	}
	engine := orchestration.New(orchestration.Deps{
		Clock: FixedClock(), Registry: registry, Resolver: DefaultResolver(),
	})
	inventory, err := engine.Inventory(ctx, fixture.Dir, fixture.BaseSHA, "tools", false)
	if err != nil {
		t.Fatalf("Inventory() error = %v", err)
	}
	plan, err := engine.Plan(inventory, map[string]string{fixture.ToolModule: target})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan.Outcome != model.OutcomePrepared {
		t.Fatalf("plan outcome = %q (%v)", plan.Outcome, plan.Blockers)
	}
	return fixture, plan
}

func TestPlanRejectsTraversalEditSite(t *testing.T) {
	ctx := context.Background()
	fixture, plan := preparePlan(t, "1.1.0")
	checkout, err := repository.Open(fixture.Dir)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	plan.Families[0].Targets[0].Source = "../repo/.bingo/tool.mod"
	registry, _ := FixtureRegistry()
	engine := orchestration.New(orchestration.Deps{Clock: FixedClock(), Registry: registry})
	_, _, err = engine.ApplyWithCheckout(ctx, checkout, plan, filepath.Join(t.TempDir(), "candidate"), t.TempDir())
	if err == nil {
		t.Fatal("a traversal edit site must be refused")
	}
	if _, statErr := os.Stat(filepath.Join(fixture.Dir, ".bingo", "tool.mod")); statErr != nil {
		t.Fatalf("source checkout missing its tool module: %v", statErr)
	}
	content, err := os.ReadFile(filepath.Join(fixture.Dir, ".bingo", "tool.mod"))
	if err != nil {
		t.Fatalf("read source tool module: %v", err)
	}
	if strings.Contains(string(content), "1.1.0") {
		t.Fatal("the source checkout must never be edited")
	}
}

func TestPlanRejectsMultilineVersion(t *testing.T) {
	ctx := context.Background()
	fixture, plan := preparePlan(t, "1.1.0")
	checkout, err := repository.Open(fixture.Dir)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	plan.Families[0].Targets[0].To = "1.1.0\nreview-proof:;false"
	registry, _ := FixtureRegistry()
	engine := orchestration.New(orchestration.Deps{Clock: FixedClock(), Registry: registry})
	if _, _, err := engine.ApplyWithCheckout(ctx, checkout, plan, filepath.Join(t.TempDir(), "candidate"), t.TempDir()); err == nil {
		t.Fatal("a multiline target version must be refused")
	}
}

func TestPlanRejectsStrippedConsumersAndTamperedIdentity(t *testing.T) {
	ctx := context.Background()
	fixture, plan := preparePlan(t, "1.1.0")
	registry, _ := FixtureRegistry()
	engine := orchestration.New(orchestration.Deps{Clock: FixedClock(), Registry: registry})

	stripped := plan
	stripped.Consumers = nil
	if err := engine.ValidatePlan(ctx, fixture.Dir, stripped); err == nil {
		t.Fatal("a plan with no consumers must be refused")
	}

	extra := plan
	extra.Consumers = append(append([]model.ConsumerRef{}, plan.Consumers...), model.ConsumerRef{ID: "yamlfmt-verify"})
	if err := engine.ValidatePlan(ctx, fixture.Dir, extra); err == nil {
		t.Fatal("a plan with an extra consumer must be refused")
	}

	tampered := plan
	tampered.PlanID = "sha256:" + strings.Repeat("0", 64)
	if err := engine.ValidatePlan(ctx, fixture.Dir, tampered); err == nil {
		t.Fatal("a plan with a mismatched identity must be refused")
	}

	if err := engine.ValidatePlan(ctx, fixture.Dir, plan); err != nil {
		t.Fatalf("the untouched plan must validate: %v", err)
	}
}

func TestCandidateDriftIsRefusedBeforeVerification(t *testing.T) {
	ctx := context.Background()
	fixture, plan := preparePlan(t, "1.1.0")
	checkout, err := repository.Open(fixture.Dir)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	registry, _ := FixtureRegistry()
	engine := orchestration.New(orchestration.Deps{Clock: FixedClock(), Registry: registry})
	record, candidate, err := engine.ApplyWithCheckout(ctx, checkout, plan, filepath.Join(t.TempDir(), "candidate"), t.TempDir())
	if err != nil {
		t.Fatalf("ApplyWithCheckout() error = %v", err)
	}
	if !repository.IsCommit(record.CommitSHA) {
		t.Fatalf("the candidate must carry a committed revision, got %q", record.CommitSHA)
	}
	// Editing validated bytes must be detected, not silently packaged.
	if err := os.WriteFile(filepath.Join(candidate.Path(), ".bingo", "tool.mod"),
		[]byte("module _\n\ngo 1.26.0\n\nrequire example.com/tool v1.5.0\n"), 0o644); err != nil {
		t.Fatalf("tamper candidate: %v", err)
	}
	if err := orchestration.VerifyCandidateBinding(ctx, candidate, record); err == nil {
		t.Fatal("a worktree edited after apply must be refused")
	}
}

func TestApplyRejectsNonconvergentGeneration(t *testing.T) {
	ctx := context.Background()
	fixture, _ := preparePlan(t, "1.1.0")
	checkout, err := repository.Open(fixture.Dir)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	// A generator that changes the tree on every invocation must fail closed.
	script := filepath.Join(fixture.Dir, "hack", "drifting_bingo.sh")
	content := "#!/usr/bin/env bash\nset -euo pipefail\n" +
		"echo \"# drift $(date +%s%N)\" >> \"$(git rev-parse --show-toplevel)/.bingo/tool.mod\"\n"
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatalf("write generator: %v", err)
	}
	registry, err := consumers.NewRegistry(consumers.Spec{
		ID: "bingo-get", Program: "bash",
		Arguments:     []string{script},
		Timeout:       30 * time.Second,
		AllowedParams: []string{"tool", "version"},
	})
	if err != nil {
		t.Fatalf("build registry: %v", err)
	}
	// Policy identity covers the consumer registry, so the plan must be built by
	// an engine that uses the same registry this apply uses.
	planning := orchestration.New(orchestration.Deps{Clock: FixedClock(), Registry: registry, Resolver: DefaultResolver()})
	inventory, err := planning.Inventory(ctx, fixture.Dir, fixture.BaseSHA, "tools", false)
	if err != nil {
		t.Fatalf("Inventory() error = %v", err)
	}
	plan, err := planning.Plan(inventory, map[string]string{fixture.ToolModule: "1.1.0"})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	_, _, err = planning.ApplyWithCheckout(ctx, checkout, plan, filepath.Join(t.TempDir(), "candidate"), t.TempDir())
	if err == nil {
		t.Fatal("a generator that never converges must fail the candidate")
	}
	if !strings.Contains(err.Error(), "converge") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPublicationBindsTheCandidateRevision(t *testing.T) {
	ctx := context.Background()
	fixture, plan := preparePlan(t, "1.1.0")
	checkout, err := repository.Open(fixture.Dir)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	registry, _ := FixtureRegistry()
	engine := orchestration.New(orchestration.Deps{Clock: FixedClock(), Registry: registry})
	record, candidate, err := engine.ApplyWithCheckout(ctx, checkout, plan, filepath.Join(t.TempDir(), "candidate"), t.TempDir())
	if err != nil {
		t.Fatalf("ApplyWithCheckout() error = %v", err)
	}
	defer func() { _ = candidate.Close(ctx, checkout) }()
	verdict, err := engine.Verify(ctx, checkout, plan, record, candidate, t.TempDir())
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	proposal, err := engine.BuildProposal(ctx, checkout, plan, candidate, record, verdict, t.TempDir())
	if err != nil {
		t.Fatalf("BuildProposal() error = %v", err)
	}
	if proposal.Expected.BranchHeadSHA != record.CommitSHA {
		t.Fatalf("the proposal must bind the candidate commit, got %q", proposal.Expected.BranchHeadSHA)
	}
	fake := forge.NewFake()
	publisher := orchestration.New(orchestration.Deps{Clock: FixedClock(), Forge: fake, Writer: fake})
	receipt, err := publisher.Publish(ctx, proposal)
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if receipt.CommitSHA != record.CommitSHA {
		t.Fatalf("published head %q is not the candidate %q", receipt.CommitSHA, record.CommitSHA)
	}
	pull, err := fake.PullRequest(ctx, receipt.PullRequestNumber)
	if err != nil {
		t.Fatalf("PullRequest() error = %v", err)
	}
	if pull.HeadSHA != record.CommitSHA {
		t.Fatalf("the published proposal head %q is not the candidate %q", pull.HeadSHA, record.CommitSHA)
	}
}

func TestInvestigationRefusesUnboundComparisonAndMissingPaths(t *testing.T) {
	ctx := context.Background()
	fixture, _ := preparePlan(t, "1.1.0")
	head := strings.Repeat("d", 40)
	fake := forge.NewFake()
	fake.Seed(forge.PullRequest{
		Number: 42, Author: policy.ToolOwner, State: "open", BaseRef: "main", BaseRepo: "Azure/ARO-HCP",
		HeadRepo: "Azure/ARO-HCP", HeadRef: "agentic-tool-upgrade-tool", HeadSHA: head,
		Title: policy.ToolTitle("example.com/tool", "1.1.0"),
		Body:  "<!-- agentic-tool-family:example.com/tool -->", Labels: []string{policy.ToolLabel},
	}, forge.Snapshot{
		Commits: []string{head},
		Statuses: []forge.Status{{
			ID: 1, Context: "ci/prow/e2e-parallel", State: "failure",
			CreatedAt: "2026-11-05T00:00:00Z", TargetURL: ProwURL,
		}},
		Checks: []forge.Check{{
			ID: 1, Name: "ci/prow/e2e-parallel", HeadSHA: head, Status: "completed",
			Conclusion: "failure", DetailsURL: ProwURL,
		}},
	})
	engine := orchestration.New(orchestration.Deps{
		Clock: FixedClock(), Forge: fake,
		Evidence: FakeFetcher{
			Result:    "FAILURE",
			Results:   map[string]string{ComparisonProwURL: "SUCCESS"},
			Revisions: map[string]string{ProwURL: head},
			Log:       "error: network timeout\n",
		},
	})
	// No base revision and no comparison supplied: the diagnosis must stay unknown.
	decision, err := engine.MaintenanceInspect(ctx, "Azure/ARO-HCP", 42, orchestration.MaintenanceOptions{
		Investigate: true, EvidenceDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("MaintenanceInspect() error = %v", err)
	}
	if decision.Investigation == nil || decision.Investigation.Diagnosis != model.DiagnosisUnknownIncomplete {
		t.Fatalf("missing changed-file evidence must stay unknown, got %+v", decision.Investigation)
	}
	if decision.Action == "recover-retry" {
		t.Fatal("an incomplete diagnosis must not authorize recovery")
	}

	// A comparison run from a different job must not be accepted.
	decision, err = engine.MaintenanceInspect(ctx, "Azure/ARO-HCP", 42, orchestration.MaintenanceOptions{
		Investigate: true, BaseSHA: fixture.BaseSHA,
		ChangedPaths:   func(context.Context, string, string) ([]string, error) { return []string{".bingo/tool.mod"}, nil },
		ComparisonURLs: []string{"https://prow.ci.openshift.org/view/gs/test-platform-results-public/pr-logs/pull/Azure_ARO-HCP/1/other-job/2117218708026822999"},
		EvidenceDir:    t.TempDir(),
	})
	if err != nil {
		t.Fatalf("MaintenanceInspect() error = %v", err)
	}
	if decision.Investigation == nil || len(decision.Investigation.Comparisons) != 1 {
		t.Fatalf("expected one judged comparison: %+v", decision.Investigation)
	}
	if decision.Investigation.Comparisons[0].Accepted {
		t.Fatal("a comparison from another job must never be accepted")
	}
	if decision.Investigation.Diagnosis != model.DiagnosisUnknownIncomplete {
		t.Fatalf("diagnosis = %q", decision.Investigation.Diagnosis)
	}
}

func TestDiagnosisDoesNotBypassHoldOrBudget(t *testing.T) {
	ctx := context.Background()
	fixture, _ := preparePlan(t, "1.1.0")
	head := strings.Repeat("e", 40)
	fake := forge.NewFake()
	pull := forge.PullRequest{
		Number: 43, Author: policy.ToolOwner, State: "open", BaseRef: "main", BaseRepo: "Azure/ARO-HCP",
		HeadRepo: "Azure/ARO-HCP", HeadRef: "agentic-tool-upgrade-tool", HeadSHA: head,
		Title:  policy.ToolTitle("example.com/tool", "1.1.0"),
		Body:   "<!-- agentic-tool-family:example.com/tool -->",
		Labels: []string{policy.ToolLabel, policy.HoldLabel},
	}
	fake.Seed(pull, forge.Snapshot{
		Commits: []string{head},
		Comments: []forge.Comment{
			{ID: 1, Author: policy.MergeBot, Body: "/hold\n\nRevision " + head + " was retested 3 times: holding\n",
				CreatedAt: "2026-11-05T00:00:00Z", UpdatedAt: "2026-11-05T00:00:00Z"},
			{ID: 2, Author: policy.Robot, Body: "<!-- " + policy.RecoveryMarker + head + " -->\nrecovered once",
				CreatedAt: "2026-11-05T01:00:00Z", UpdatedAt: "2026-11-05T01:00:00Z"},
			{ID: 3, Author: "reviewer", Body: "/hold pending review",
				CreatedAt: "2026-11-05T02:00:00Z", UpdatedAt: "2026-11-05T02:00:00Z"},
		},
		HoldEvents: []forge.HoldEvent{{ID: 1, Event: "labeled", Actor: policy.ProwBot, CreatedAt: "2026-11-05T00:00:00Z"}},
		Statuses: []forge.Status{{
			ID: 1, Context: "ci/prow/e2e-parallel", State: "failure",
			CreatedAt: "2026-11-05T03:00:00Z", TargetURL: ProwURL,
		}},
		Checks: []forge.Check{{
			ID: 1, Name: "ci/prow/e2e-parallel", HeadSHA: head, Status: "completed",
			Conclusion: "failure", DetailsURL: ProwURL,
		}},
	})
	engine := orchestration.New(orchestration.Deps{
		Clock: FixedClock(), Forge: fake,
		Evidence: FakeFetcher{
			Result:    "FAILURE",
			Results:   map[string]string{ComparisonProwURL: "SUCCESS"},
			Revisions: map[string]string{ProwURL: head},
			Log:       "error: ManagedNode infra config cache not synchronized\n",
		},
	})
	decision, err := engine.MaintenanceInspect(ctx, "Azure/ARO-HCP", 43, orchestration.MaintenanceOptions{
		Investigate: true, BaseSHA: fixture.BaseSHA,
		ChangedPaths:   func(context.Context, string, string) ([]string, error) { return []string{".bingo/tool.mod"}, nil },
		ComparisonURLs: []string{ComparisonProwURL},
		EvidenceDir:    t.TempDir(),
	})
	if err != nil {
		t.Fatalf("MaintenanceInspect() error = %v", err)
	}
	if decision.CanRecoverRetry {
		t.Fatalf("a human hold and a consumed budget must block recovery: %v", decision.RetryBlockers)
	}
	if decision.Action == "recover-retry" {
		t.Fatalf("diagnosis must not expand authorization, action = %q", decision.Action)
	}
	if len(decision.ActiveHolds) < 2 {
		t.Fatalf("expected the human and retry holds to be preserved: %+v", decision.ActiveHolds)
	}
}
