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
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/schema"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/workspace"
)

func TestExistingCandidateOpensAtItsCommit(t *testing.T) {
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

	// The documented flow reopens the committed candidate, not its base.
	if _, err := workspace.OpenExisting(ctx, record.Workspace, record.BaseSHA, record.BaseSHA); err == nil {
		t.Fatal("opening the candidate at its base must be refused")
	}
	reopened, err := workspace.OpenExisting(ctx, record.Workspace, record.CommitSHA, record.BaseSHA)
	if err != nil {
		t.Fatalf("OpenExisting() error = %v", err)
	}
	// An omitted log directory must not place a baseline worktree in the source.
	if _, err := engine.Verify(ctx, checkout, plan, record, reopened, ""); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
}

func TestPlanConsumersDoNotLeakExecutableParameters(t *testing.T) {
	ctx := context.Background()
	fixture, _ := preparePlan(t, "1.1.0")
	registry := consumers.Default()
	// A plan built with the production registry, then verified with it: the
	// consistency gate accepts no parameters, so binding a version to it fails.
	engine := orchestration.New(orchestration.Deps{Clock: FixedClock(), Registry: registry, Resolver: DefaultResolver()})
	inventory, err := engine.Inventory(ctx, fixture.Dir, fixture.BaseSHA, "tools", false)
	if err != nil {
		t.Fatalf("Inventory() error = %v", err)
	}
	plan, err := engine.Plan(inventory, map[string]string{"Azure/bicep": "0.49.0"})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if err := engine.ValidatePlan(ctx, fixture.Dir, plan); err != nil {
		t.Fatalf("ValidatePlan() error = %v", err)
	}
	checkout, err := repository.Open(fixture.Dir)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	record, candidate, err := engine.ApplyWithCheckout(ctx, checkout, plan, filepath.Join(t.TempDir(), "candidate"), t.TempDir())
	if err != nil {
		t.Fatalf("ApplyWithCheckout() error = %v", err)
	}
	defer func() { _ = candidate.Close(ctx, checkout) }()
	verdict, err := engine.Verify(ctx, checkout, plan, record, candidate, t.TempDir())
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	for _, result := range verdict.Results {
		if result.ID != "ci-versions-verify" {
			continue
		}
		if result.Status == model.ConsumerError {
			t.Fatalf("the consistency gate received an unsupported parameter: %s", result.Reason)
		}
	}
}

func TestTamperedInventoryToolNameDoesNotDropTheCompilerGate(t *testing.T) {
	ctx := context.Background()
	fixture, _ := preparePlan(t, "1.1.0")
	registry, _ := FixtureRegistry()
	engine := orchestration.New(orchestration.Deps{Clock: FixedClock(), Registry: registry, Resolver: DefaultResolver()})
	inventory, err := engine.Inventory(ctx, fixture.Dir, fixture.BaseSHA, "tools", false)
	if err != nil {
		t.Fatalf("Inventory() error = %v", err)
	}
	for index, family := range inventory.Families {
		if family.Family == "Azure/bicep" {
			inventory.Families[index].ToolNames = []string{"ordinary"}
			for pinIndex := range inventory.Families[index].Pins {
				inventory.Families[index].Pins[pinIndex].ToolName = "ordinary"
			}
		}
	}
	plan, err := engine.Plan(inventory, map[string]string{"Azure/bicep": "0.49.0"})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	found := false
	for _, consumer := range plan.Consumers {
		if consumer.ID == "bicep-fixtures" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the compiler gate must come from the repository identity, got %+v", plan.Consumers)
	}
}

func TestForgedInventoryCurrentCannotDowngrade(t *testing.T) {
	ctx := context.Background()
	fixture, _ := preparePlan(t, "1.1.0")
	registry, _ := FixtureRegistry()
	engine := orchestration.New(orchestration.Deps{Clock: FixedClock(), Registry: registry, Resolver: DefaultResolver()})
	inventory, err := engine.Inventory(ctx, fixture.Dir, fixture.BaseSHA, "tools", false)
	if err != nil {
		t.Fatalf("Inventory() error = %v", err)
	}
	for index := range inventory.Families {
		if inventory.Families[index].Family == fixture.ToolModule {
			inventory.Families[index].Current = "0.1.0"
			for pinIndex := range inventory.Families[index].Pins {
				inventory.Families[index].Pins[pinIndex].Current = "0.1.0"
			}
		}
	}
	plan, err := engine.Plan(inventory, map[string]string{fixture.ToolModule: "0.5.0"})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if err := engine.ValidatePlan(ctx, fixture.Dir, plan); err == nil {
		t.Fatal("a downgrade from the repository pin must be refused")
	}
}

func TestSubstitutedCandidateCommitIsRefused(t *testing.T) {
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
	forged := record
	forged.CommitSHA = strings.Repeat("a", 40)
	if err := orchestration.VerifyCandidateBinding(ctx, candidate, forged); err == nil {
		t.Fatal("a substituted commit must be refused")
	}
	forgedID := record
	forgedID.CandidateID = "sha256:" + strings.Repeat("0", 64)
	if err := orchestration.VerifyCandidateBinding(ctx, candidate, forgedID); err == nil {
		t.Fatal("a substituted candidate identity must be refused")
	}
}

func TestNewGeneratedFileIsRecordedAndBound(t *testing.T) {
	ctx := context.Background()
	fixture, _ := preparePlan(t, "1.1.0")
	checkout, err := repository.Open(fixture.Dir)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	script := filepath.Join(fixture.Dir, "hack", "generating_bingo.sh")
	content := "#!/usr/bin/env bash\nset -euo pipefail\nroot=\"$(git rev-parse --show-toplevel)\"\n" +
		"cd \"$root/.bingo\"\nGOWORK=off GOTOOLCHAIN=local go mod edit -modfile=tool.mod -require=example.com/tool@v${2#v}\n" +
		"printf 'example.com/tool v%s h1:generated\\n' \"${2#v}\" > tool.sum\n" +
		"cd \"$root\"\npython3 hack/gen_bingo.py\n"
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatalf("write generator: %v", err)
	}
	registry, err := consumers.NewRegistry(
		consumers.Spec{ID: "bingo-get", Program: "bash", Arguments: []string{script, "{{tool}}", "{{version}}"},
			Timeout: 60 * time.Second, AllowedParams: []string{"tool", "version"}},
		consumers.Spec{ID: "ci-versions-verify", Program: "make",
			Arguments:     []string{"-C", "dev-infrastructure/openshift-ci", "verify"},
			Prerequisites: []consumers.Prerequisite{{Binary: "make"}, {Binary: "bash"}},
			Timeout:       60 * time.Second, AllowedParams: []string{"expectedVersion"}},
		consumers.Spec{ID: "bingo-regenerate", Program: "make", Arguments: []string{"bingo-regenerate"},
			Prerequisites: []consumers.Prerequisite{{Binary: "make"}, {Binary: "bash"}, {Binary: "python3"}, {Binary: "go"}},
			Timeout:       60 * time.Second},
	)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	engine := orchestration.New(orchestration.Deps{Clock: FixedClock(), Registry: registry, Resolver: DefaultResolver()})
	inventory, err := engine.Inventory(ctx, fixture.Dir, fixture.BaseSHA, "tools", false)
	if err != nil {
		t.Fatalf("Inventory() error = %v", err)
	}
	plan, err := engine.Plan(inventory, map[string]string{fixture.ToolModule: "1.1.0"})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	record, candidate, err := engine.ApplyWithCheckout(ctx, checkout, plan, filepath.Join(t.TempDir(), "candidate"), t.TempDir())
	if err != nil {
		t.Fatalf("ApplyWithCheckout() error = %v", err)
	}
	defer func() { _ = candidate.Close(ctx, checkout) }()
	found := false
	for _, changed := range record.ChangedPaths {
		if changed == ".bingo/tool.sum" {
			found = true
		}
	}
	if !found {
		t.Fatalf("a new generated file must appear in the record: %v", record.ChangedPaths)
	}
	if err := orchestration.VerifyCandidateBinding(ctx, candidate, record); err != nil {
		t.Fatalf("the freshly applied candidate must satisfy its own binding: %v", err)
	}
}

func TestMultiFamilySelectionPreparesOneFamily(t *testing.T) {
	inventory := model.Inventory{Complete: true, Families: []model.Family{
		{Family: "Azure/bicep", Kind: model.FamilyGitHubRelease, Current: "0.48.1", Status: model.VersionOutdated,
			Pins: []model.Pin{{ToolName: "bicep", Source: policy.CIVersionsFile, Variable: "BICEP_VERSION", Current: "0.48.1"}}},
		{Family: "prometheus/prometheus", Kind: model.FamilyGitHubRelease, Current: "3.15.1", Status: model.VersionOutdated,
			Pins: []model.Pin{{ToolName: "promtool", Source: policy.CIVersionsFile, Variable: "PROMTOOL_VERSION", Current: "3.15.1"}}},
	}}
	selection, err := policy.SelectFamilies(inventory, map[string]string{
		"Azure/bicep": "0.49.0", "prometheus/prometheus": "3.16.0",
	})
	if err != nil {
		t.Fatalf("SelectFamilies() error = %v", err)
	}
	if len(selection.Families) != 1 {
		t.Fatalf("expected one prepared family, got %d", len(selection.Families))
	}
	deferred := false
	for _, skipped := range selection.Skipped {
		if strings.Contains(skipped.Reason, "one family per preparation") {
			deferred = true
		}
	}
	if !deferred {
		t.Fatalf("the deferred family must be reported: %+v", selection.Skipped)
	}
}

func TestUnboundComparisonIsNotAccepted(t *testing.T) {
	ctx := context.Background()
	fixture, _ := preparePlan(t, "1.1.0")
	head := strings.Repeat("9", 40)
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
	decision, err := engine.MaintenanceInspect(ctx, "Azure/ARO-HCP", 42, orchestration.MaintenanceOptions{
		Investigate: true, BaseSHA: fixture.BaseSHA,
		ChangedPaths:   func(context.Context, string, string) ([]string, error) { return []string{".bingo/tool.mod"}, nil },
		ComparisonURLs: []string{ComparisonProwURL},
		EvidenceDir:    t.TempDir(),
	})
	if err != nil {
		t.Fatalf("MaintenanceInspect() error = %v", err)
	}
	if decision.Investigation == nil || decision.Investigation.Comparisons[0].Accepted {
		t.Fatalf("a comparison without its own revision must not be accepted: %+v", decision.Investigation)
	}
	if decision.Action == "recover-retry" {
		t.Fatal("an unbound comparison must not authorize recovery")
	}
}

func TestReleaseTargetKeepsBareVersion(t *testing.T) {
	ctx := context.Background()
	fixture, _ := preparePlan(t, "1.1.0")
	registry, _ := FixtureRegistry()
	engine := orchestration.New(orchestration.Deps{Clock: FixedClock(), Registry: registry, Resolver: DefaultResolver()})
	inventory, err := engine.Inventory(ctx, fixture.Dir, fixture.BaseSHA, "tools", false)
	if err != nil {
		t.Fatalf("Inventory() error = %v", err)
	}
	prefixed, err := engine.Plan(inventory, map[string]string{"Azure/bicep": "v0.49.0"})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	for _, target := range prefixed.Families[0].Targets {
		if strings.HasPrefix(target.To, "v") {
			t.Fatalf("CI release targets must be unprefixed, got %q", target.To)
		}
	}
	if err := engine.ValidatePlan(ctx, fixture.Dir, prefixed); err != nil {
		t.Fatalf("the canonical plan must validate: %v", err)
	}
}

func TestMaintenanceAllowedActionsMatchTheDecision(t *testing.T) {
	ctx := context.Background()
	fixture, _ := preparePlan(t, "1.1.0")
	head := strings.Repeat("7", 40)
	fake := forge.NewFake()
	fake.Seed(forge.PullRequest{
		Number: 44, Author: policy.ToolOwner, State: "open", BaseRef: "main", BaseRepo: "Azure/ARO-HCP",
		HeadRepo: "Azure/ARO-HCP", HeadRef: "agentic-tool-upgrade-tool", HeadSHA: head,
		Title:  policy.ToolTitle("example.com/tool", "1.1.0"),
		Body:   "<!-- agentic-tool-family:example.com/tool -->",
		Labels: []string{policy.ToolLabel, policy.HoldLabel},
	}, forge.Snapshot{
		Commits: []string{head},
		Comments: []forge.Comment{{
			ID: 1, Author: policy.MergeBot,
			Body:      "/hold\n\nRevision " + head + " was retested 3 times: holding\n",
			CreatedAt: "2026-11-05T00:00:00Z", UpdatedAt: "2026-11-05T00:00:00Z",
		}},
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
			Revisions: map[string]string{ProwURL: head},
			Log:       "error: ManagedNode infra config cache not synchronized\n",
		},
	})
	decision, err := engine.MaintenanceInspect(ctx, "Azure/ARO-HCP", 44, orchestration.MaintenanceOptions{
		Investigate: true, BaseSHA: fixture.BaseSHA,
		ChangedPaths: func(context.Context, string, string) ([]string, error) { return []string{".bingo/tool.mod"}, nil },
		EvidenceDir:  t.TempDir(),
	})
	if err != nil {
		t.Fatalf("MaintenanceInspect() error = %v", err)
	}
	found := false
	for _, action := range decision.AllowedActions {
		if action == decision.Action {
			found = true
		}
	}
	if !found {
		t.Fatalf("allowed actions %v do not include the selected action %q", decision.AllowedActions, decision.Action)
	}
}

func TestForgedInventoryCurrentCannotDowngradeRelease(t *testing.T) {
	ctx := context.Background()
	fixture, _ := preparePlan(t, "1.1.0")
	registry, _ := FixtureRegistry()
	engine := orchestration.New(orchestration.Deps{Clock: FixedClock(), Registry: registry, Resolver: DefaultResolver()})
	inventory, err := engine.Inventory(ctx, fixture.Dir, fixture.BaseSHA, "tools", false)
	if err != nil {
		t.Fatalf("Inventory() error = %v", err)
	}
	for index := range inventory.Families {
		if inventory.Families[index].Family == "Azure/bicep" {
			inventory.Families[index].Current = "0.1.0"
			for pinIndex := range inventory.Families[index].Pins {
				inventory.Families[index].Pins[pinIndex].Current = "0.1.0"
			}
		}
	}
	plan, err := engine.Plan(inventory, map[string]string{"Azure/bicep": "0.47.0"})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	err = engine.ValidatePlan(ctx, fixture.Dir, plan)
	if err == nil {
		t.Fatal("a release downgrade from the repository pin must be refused")
	}
	if !strings.Contains(err.Error(), "downgrade") && !strings.Contains(err.Error(), "pins") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDecodedMultiFamilyPlanIsRefused(t *testing.T) {
	ctx := context.Background()
	fixture, plan := preparePlan(t, "1.1.0")
	registry, _ := FixtureRegistry()
	engine := orchestration.New(orchestration.Deps{Clock: FixedClock(), Registry: registry})
	second := plan.Families[0]
	second.Family = "Azure/bicep"
	second.Kind = model.FamilyGitHubRelease
	second.From = "0.48.1"
	second.To = "0.49.0"
	second.Targets = []model.PinTarget{
		{ToolName: "bicep", Source: policy.CIVersionsFile, Variable: "BICEP_VERSION", Kind: model.EditMakeVariable, From: "0.48.1", To: "0.49.0"},
		{ToolName: "bicep", Source: policy.CIImageDockerfile, Variable: "BICEP_VERSION", Kind: model.EditDockerfileArg, From: "0.48.1", To: "0.49.0"},
	}
	combined := plan
	combined.Families = []model.PlannedFamily{plan.Families[0], second}
	err := engine.ValidatePlan(ctx, fixture.Dir, combined)
	if err == nil {
		t.Fatal("a two-family preparation must be refused")
	}
	if !strings.Contains(err.Error(), "exactly one family") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCandidateRegressionInAnyFailingCheckWins(t *testing.T) {
	ctx := context.Background()
	fixture, _ := preparePlan(t, "1.1.0")
	head := strings.Repeat("8", 40)
	otherJob := "https://prow.ci.openshift.org/view/gs/test-platform-results-public/pr-logs/pull/Azure_ARO-HCP/7892/another-job/2117218708026822656"
	fake := forge.NewFake()
	fake.Seed(forge.PullRequest{
		Number: 45, Author: policy.ToolOwner, State: "open", BaseRef: "main", BaseRepo: "Azure/ARO-HCP",
		HeadRepo: "Azure/ARO-HCP", HeadRef: "agentic-tool-upgrade-tool", HeadSHA: head,
		Title: policy.ToolTitle("example.com/tool", "1.1.0"),
		Body:  "<!-- agentic-tool-family:example.com/tool -->", Labels: []string{policy.ToolLabel},
	}, forge.Snapshot{
		Commits: []string{head},
		Statuses: []forge.Status{
			{ID: 1, Context: "ci/prow/e2e-parallel", State: "failure", CreatedAt: "2026-11-05T00:00:00Z", TargetURL: ProwURL},
			{ID: 2, Context: "ci/prow/unit", State: "failure", CreatedAt: "2026-11-05T00:00:00Z", TargetURL: otherJob},
		},
		Checks: []forge.Check{
			{ID: 1, Name: "ci/prow/e2e-parallel", HeadSHA: head, Status: "completed", Conclusion: "failure", DetailsURL: ProwURL},
			{ID: 2, Name: "ci/prow/unit", HeadSHA: head, Status: "completed", Conclusion: "failure", DetailsURL: otherJob},
		},
	})
	engine := orchestration.New(orchestration.Deps{
		Clock: FixedClock(), Forge: fake,
		Evidence: FakeFetcher{
			Result:    "FAILURE",
			Results:   map[string]string{ComparisonProwURL: "SUCCESS"},
			Revisions: map[string]string{ProwURL: head, otherJob: head},
			Logs: map[string]string{
				ProwURL:  "error: network timeout\n",
				otherJob: "error: .bingo/tool.mod candidate regression\n",
			},
		},
	})
	decision, err := engine.MaintenanceInspect(ctx, "Azure/ARO-HCP", 45, orchestration.MaintenanceOptions{
		Investigate: true, BaseSHA: fixture.BaseSHA,
		ChangedPaths:   func(context.Context, string, string) ([]string, error) { return []string{".bingo/tool.mod"}, nil },
		ComparisonURLs: []string{ComparisonProwURL},
		EvidenceDir:    t.TempDir(),
	})
	if err != nil {
		t.Fatalf("MaintenanceInspect() error = %v", err)
	}
	if decision.Investigation == nil || decision.Investigation.Diagnosis != model.DiagnosisCandidateRegression {
		t.Fatalf("a candidate regression in any failing check must win: %+v", decision.Investigation)
	}
	if decision.Action == "recover-retry" {
		t.Fatal("a candidate regression must never authorize recovery")
	}
}

func TestForgedVerdictIsRefusedAtPackaging(t *testing.T) {
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
	forged := verdict
	forged.Verdict = model.VerdictValidated
	forged.Results = nil
	forged.NotRun = nil
	if _, err := engine.BuildProposal(ctx, checkout, plan, candidate, record, forged, t.TempDir()); err == nil {
		t.Fatal("a verdict without results must not authorize a proposal")
	}
	// A pass followed by a failure for the same consumer must not be read as a
	// validated result set.
	duplicated := verdict
	duplicated.Results = append(append([]model.ConsumerResult{}, verdict.Results...), model.ConsumerResult{
		ID: "ci-versions-verify", Phase: "candidate", Status: model.ConsumerFailed,
	})
	if _, err := engine.BuildProposal(ctx, checkout, plan, candidate, record, duplicated, t.TempDir()); err == nil {
		t.Fatal("contradictory duplicate results must not authorize a proposal")
	}
	if _, err := engine.BuildProposal(ctx, checkout, plan, candidate, record, verdict, t.TempDir()); err != nil {
		t.Fatalf("the genuine verdict must authorize packaging: %v", err)
	}
}

func TestVerificationRejectsAnAdvancedSource(t *testing.T) {
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
	// Advancing the source invalidates the plan even when pins did not change.
	if err := os.WriteFile(filepath.Join(fixture.Dir, "NOTES.md"), []byte("later\n"), 0o644); err != nil {
		t.Fatalf("write source change: %v", err)
	}
	if err := fixture.Commit(ctx, "later change"); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if _, err := engine.Verify(ctx, checkout, plan, record, candidate, t.TempDir()); err == nil {
		t.Fatal("an advanced source revision must be refused as stale")
	}
}

func TestRunReportsDeferredFamilies(t *testing.T) {
	ctx := context.Background()
	fixture, _ := preparePlan(t, "1.1.0")
	registry, _ := FixtureRegistry()
	engine := orchestration.New(orchestration.Deps{
		Clock: FixedClock(), Registry: registry, Resolver: DefaultResolver(),
	})
	work := t.TempDir()
	result, err := engine.Run(ctx, orchestration.RunOptions{
		RepositoryPath: fixture.Dir, Profile: "tools",
		Targets: map[string]string{"Azure/bicep": "0.49.0", fixture.ToolModule: "1.1.0"},
		WorkDir: work, StateDir: filepath.Join(work, "state"), DryRun: true,
		OutputPath: filepath.Join(work, "run.json"),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(result.Families) < 2 {
		t.Fatalf("a deferred family must be reported: %+v", result.Families)
	}
	found := false
	for _, family := range result.Families {
		if strings.Contains(family.Reason, "one family per preparation") {
			found = true
		}
	}
	if !found {
		t.Fatalf("deferred reason missing: %+v", result.Families)
	}
}

func TestInventoryRefusesAnUnrelatedRevision(t *testing.T) {
	ctx := context.Background()
	fixture, _ := preparePlan(t, "1.1.0")
	registry, _ := FixtureRegistry()
	engine := orchestration.New(orchestration.Deps{Clock: FixedClock(), Registry: registry, Resolver: DefaultResolver()})
	other := strings.Repeat("a", 40)
	if _, err := engine.Inventory(ctx, fixture.Dir, other, "tools", false); err == nil {
		t.Fatal("an inventory must not be attributed to a revision it did not read")
	}
	inventory, err := engine.Inventory(ctx, fixture.Dir, "", "tools", false)
	if err != nil {
		t.Fatalf("Inventory() error = %v", err)
	}
	if inventory.BaseSHA != fixture.BaseSHA {
		t.Fatalf("inventory base = %s, want %s", inventory.BaseSHA, fixture.BaseSHA)
	}
}

func TestCandidatePathThroughSymlinkIntoSourceIsRefused(t *testing.T) {
	ctx := context.Background()
	fixture, plan := preparePlan(t, "1.1.0")
	checkout, err := repository.Open(fixture.Dir)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	link := filepath.Join(t.TempDir(), "into-source")
	if err := os.Symlink(fixture.Dir, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	registry, _ := FixtureRegistry()
	engine := orchestration.New(orchestration.Deps{Clock: FixedClock(), Registry: registry})
	_, _, err = engine.ApplyWithCheckout(ctx, checkout, plan, filepath.Join(link, "candidate"), t.TempDir())
	if err == nil {
		t.Fatal("a candidate path that resolves inside the source checkout must be refused")
	}
	if _, statErr := os.Stat(filepath.Join(fixture.Dir, "candidate")); statErr == nil {
		t.Fatal("no worktree may be created inside the source checkout")
	}
}

func TestInvestigationRetryUsesAFreshEvidenceDirectory(t *testing.T) {
	ctx := context.Background()
	fixture, _ := preparePlan(t, "1.1.0")
	head := strings.Repeat("5", 40)
	fake := forge.NewFake()
	fake.Seed(forge.PullRequest{
		Number: 46, Author: policy.ToolOwner, State: "open", BaseRef: "main", BaseRepo: "Azure/ARO-HCP",
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
	evidenceDir := t.TempDir()
	stateDir := t.TempDir()
	newEngine := func() *orchestration.Engine {
		return orchestration.New(orchestration.Deps{
			Clock: FixedClock(), Forge: fake,
			Evidence: FakeFetcher{
				Result:    "FAILURE",
				Revisions: map[string]string{ProwURL: head},
				Log:       "error: network timeout\n",
			},
		})
	}
	for attempt := 1; attempt <= 2; attempt++ {
		decision, err := newEngine().MaintenanceInspect(ctx, "Azure/ARO-HCP", 46, orchestration.MaintenanceOptions{
			Investigate: true, BaseSHA: fixture.BaseSHA,
			ChangedPaths: func(context.Context, string, string) ([]string, error) { return []string{".bingo/tool.mod"}, nil },
			EvidenceDir:  evidenceDir, StateDir: stateDir,
		})
		if err != nil {
			t.Fatalf("attempt %d: MaintenanceInspect() error = %v", attempt, err)
		}
		if decision.Investigation == nil || decision.Investigation.Attempt != attempt {
			t.Fatalf("attempt %d: unexpected investigation %+v", attempt, decision.Investigation)
		}
	}
	for _, name := range []string{"attempt-1", "attempt-2"} {
		if _, err := os.Stat(filepath.Join(evidenceDir, name)); err != nil {
			t.Fatalf("expected %s evidence: %v", name, err)
		}
	}
}

func TestRefusedCandidateLeavesNoDirectoryInsideSource(t *testing.T) {
	ctx := context.Background()
	fixture, _ := preparePlan(t, "1.1.0")
	checkout, err := repository.Open(fixture.Dir)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	// The parent does not exist yet; refusing must not create it inside source.
	refused := filepath.Join(fixture.Dir, "new-parent", "candidate")
	if _, err := workspace.Prepare(ctx, checkout, refused, fixture.BaseSHA); err == nil {
		t.Fatal("a candidate inside the source checkout must be refused")
	}
	if _, statErr := os.Stat(filepath.Join(fixture.Dir, "new-parent")); statErr == nil {
		t.Fatal("a refused candidate created a directory inside the source checkout")
	}
	// The same through a symlinked ancestor.
	link := filepath.Join(t.TempDir(), "into-source")
	if err := os.Symlink(fixture.Dir, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := workspace.Prepare(ctx, checkout, filepath.Join(link, "another", "candidate"), fixture.BaseSHA); err == nil {
		t.Fatal("a candidate through a symlink into the source must be refused")
	}
	if _, statErr := os.Stat(filepath.Join(fixture.Dir, "another")); statErr == nil {
		t.Fatal("a refused symlinked candidate created a directory inside the source checkout")
	}
}

func TestCandidateNestedOutsideSourceIsCreated(t *testing.T) {
	ctx := context.Background()
	fixture, _ := preparePlan(t, "1.1.0")
	checkout, err := repository.Open(fixture.Dir)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	nested := filepath.Join(t.TempDir(), "a", "b", "candidate")
	candidate, err := workspace.Prepare(ctx, checkout, nested, fixture.BaseSHA)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	defer func() { _ = candidate.Close(ctx, checkout) }()
	if info, err := os.Stat(filepath.Join(nested, ".bingo")); err != nil || !info.IsDir() {
		t.Fatalf("the nested candidate was not created: %v", err)
	}
}

func TestVerifyRejectsACandidateInsideTheSource(t *testing.T) {
	ctx := context.Background()
	fixture, plan := preparePlan(t, "1.1.0")
	checkout, err := repository.Open(fixture.Dir)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	inside, err := workspace.OpenExisting(ctx, fixture.Dir, fixture.BaseSHA, fixture.BaseSHA)
	if err != nil {
		t.Fatalf("OpenExisting() error = %v", err)
	}
	record := model.Candidate{
		Envelope: schema.Envelope{SchemaVersion: schema.Version, Kind: model.KindCandidate},
		PlanID:   plan.PlanID, BaseSHA: plan.BaseSHA, CommitSHA: fixture.BaseSHA,
		Workspace: fixture.Dir,
	}
	registry, _ := FixtureRegistry()
	engine := orchestration.New(orchestration.Deps{Clock: FixedClock(), Registry: registry})
	if _, err := engine.Verify(ctx, checkout, plan, record, inside, t.TempDir()); err == nil {
		t.Fatal("a candidate inside the source checkout must be refused")
	}
}

func TestProposalRejectsACandidateFromAnotherPlan(t *testing.T) {
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
	foreign := record
	foreign.PlanID = "sha256:" + strings.Repeat("1", 64)
	if _, err := engine.BuildProposal(ctx, checkout, plan, candidate, foreign, verdict, t.TempDir()); err == nil {
		t.Fatal("a candidate from another plan must be refused")
	}
	movedBase := record
	movedBase.BaseSHA = strings.Repeat("2", 40)
	if _, err := engine.BuildProposal(ctx, checkout, plan, candidate, movedBase, verdict, t.TempDir()); err == nil {
		t.Fatal("a candidate from another base must be refused")
	}
}

func TestProposalCreatesTheArtifactDirectory(t *testing.T) {
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
	artifacts := filepath.Join(t.TempDir(), "new", "artifacts")
	proposal, err := engine.BuildProposal(ctx, checkout, plan, candidate, record, verdict, artifacts)
	if err != nil {
		t.Fatalf("BuildProposal() must create the artifact directory: %v", err)
	}
	if _, err := os.Stat(proposal.PatchPath); err != nil {
		t.Fatalf("expected the patch at %s: %v", proposal.PatchPath, err)
	}
}
