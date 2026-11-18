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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/consumers"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/evidence"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/forge"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/model"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/orchestration"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/policy"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/repository"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/schema"
)

// FixedClock returns a deterministic clock that advances one second per call.
func FixedClock() func() time.Time {
	current := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	return func() time.Time {
		current = current.Add(time.Second)
		return current
	}
}

// FakeResolver serves deterministic upstream versions.
type FakeResolver struct {
	Releases map[string]string
	Modules  map[string]string
}

// LatestRelease implements discovery.Resolver.
func (r FakeResolver) LatestRelease(_ context.Context, repository string) (string, error) {
	if version, ok := r.Releases[repository]; ok {
		return version, nil
	}
	return "", fmt.Errorf("unknown repository %s", repository)
}

// LatestModule implements discovery.Resolver.
func (r FakeResolver) LatestModule(_ context.Context, module string) (string, error) {
	if version, ok := r.Modules[module]; ok {
		return version, nil
	}
	return "", fmt.Errorf("unknown module %s", module)
}

// DefaultResolver resolves every fixture family.
func DefaultResolver() FakeResolver {
	return FakeResolver{
		Releases: map[string]string{"prometheus/prometheus": "3.16.0", "Azure/bicep": "0.49.0"},
		Modules:  map[string]string{fixtureToolModule: "1.1.0"},
	}
}

// FakeFetcher writes deterministic Prow-like artifacts.
type FakeFetcher struct {
	// Result is the finished.json result of a run.
	Result string
	// Log is the build log content.
	Log string
	// Results overrides the finished.json result per URL.
	Results map[string]string
	// Revisions overrides the recorded run revision per URL.
	Revisions map[string]string
	// Logs overrides the build log per URL.
	Logs map[string]string
	// FailOn makes a URL fail to retrieve.
	FailOn map[string]bool
}

// Fetch implements evidence.Fetcher.
func (f FakeFetcher) Fetch(_ context.Context, prowURL, directory string) (model.EvidenceManifest, error) {
	if f.FailOn[prowURL] {
		return model.EvidenceManifest{ProwURL: prowURL, Missing: []string{"build-log.txt"}},
			fmt.Errorf("%w: simulated retrieval failure", evidence.ErrArtifact)
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return model.EvidenceManifest{}, err
	}
	result := f.Result
	if override, ok := f.Results[prowURL]; ok {
		result = override
	}
	manifest := model.EvidenceManifest{
		Envelope: schema.Envelope{SchemaVersion: schema.Version, Kind: model.KindEvidenceManifest},
		ProwURL:  prowURL, Complete: true, Result: result,
	}
	revision := f.Revisions[prowURL]
	log := f.Log
	if override, ok := f.Logs[prowURL]; ok {
		log = override
	}
	manifest.Revision = revision
	files := map[string]string{
		"finished.json": fmt.Sprintf("{\"result\": %q, \"revision\": %q}\n", result, revision),
		"build-log.txt": log,
	}
	for _, name := range []string{"finished.json", "build-log.txt"} {
		content := files[name]
		path := filepath.Join(directory, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			return model.EvidenceManifest{}, err
		}
		sum := sha256.Sum256([]byte(content))
		manifest.Files = append(manifest.Files, model.EvidenceFile{
			Name: name, Path: path, Bytes: int64(len(content)),
			Digest: "sha256:" + hex.EncodeToString(sum[:]),
		})
	}
	return manifest, nil
}

// FixtureRegistry returns the consumers the fixture repository provides.
func FixtureRegistry() (*consumers.Registry, error) {
	return consumers.NewRegistry(
		consumers.Spec{
			ID: "ci-versions-verify", Program: "make",
			Arguments: []string{"-C", "dev-infrastructure/openshift-ci", "verify"},
			Prerequisites: []consumers.Prerequisite{
				{Binary: "make"}, {Binary: "bash"},
			},
			Timeout:       2 * time.Minute,
			AllowedParams: []string{"expectedVersion"},
		},
		consumers.Spec{
			ID: "bingo-get", Program: "bash",
			Arguments: []string{"hack/bingo_get.sh", "{{tool}}", "{{version}}"},
			Prerequisites: []consumers.Prerequisite{
				{Binary: "bash"}, {Binary: "python3"}, {Binary: "go"},
			},
			Timeout:       2 * time.Minute,
			AllowedParams: []string{"tool", "version"},
		},
		consumers.Spec{
			ID: "bicep-fixtures", Program: "make", Arguments: []string{"bicep-fixtures"},
			Prerequisites: []consumers.Prerequisite{{Binary: "make"}, {Binary: "bash"}},
			Timeout:       2 * time.Minute,
			AllowedParams: []string{"expectedVersion"},
		},
		consumers.Spec{
			ID: "promtool-rules", Program: "make", Arguments: []string{"promtool-rules"},
			Prerequisites: []consumers.Prerequisite{{Binary: "make"}, {Binary: "bash"}},
			Timeout:       2 * time.Minute,
			AllowedParams: []string{"expectedVersion"},
		},
		consumers.Spec{
			ID: "bingo-regenerate", Program: "make", Arguments: []string{"bingo-regenerate"},
			Prerequisites: []consumers.Prerequisite{
				{Binary: "make"}, {Binary: "bash"}, {Binary: "python3"}, {Binary: "go"},
			},
			Timeout: 2 * time.Minute,
		},
	)
}

// Scenario names.
const (
	ScenarioLifecycle            = "lifecycle"
	ScenarioNoUpdate             = "no-update"
	ScenarioGenerationFails      = "generation-fails"
	ScenarioValidationRegression = "validation-regression"
	ScenarioHeldFailure          = "held-failure"
	ScenarioCIPinPair            = "ci-pin-pair"
)

// Scenarios lists the supported names.
func Scenarios() []string {
	return []string{ScenarioLifecycle, ScenarioNoUpdate, ScenarioGenerationFails, ScenarioValidationRegression, ScenarioHeldFailure, ScenarioCIPinPair}
}

// Report is the outcome of one simulated lifecycle.
type Report struct {
	Scenario     string
	FixtureBase  string
	Run          model.RunResult
	Candidate    model.Candidate
	Validation   *model.Validation
	Proposal     *model.Proposal
	Publication  *model.PublicationReceipt
	Maintenance  *model.MaintenanceDecision
	Continuation *model.ContinuationDecision
	Mutations    []forge.Mutation
	Capabilities []string
	Expectation  string
}

// ProwURL is a canonical public Prow run URL used by the scenarios.
const ProwURL = "https://prow.ci.openshift.org/view/gs/test-platform-results-public/pr-logs/pull/Azure_ARO-HCP/7892/pull-ci-Azure-ARO-HCP-main-e2e-parallel/2117218708026822656"

// ComparisonProwURL is a passing run of the same job on another revision.
const ComparisonProwURL = "https://prow.ci.openshift.org/view/gs/test-platform-results-public/pr-logs/pull/Azure_ARO-HCP/7892/pull-ci-Azure-ARO-HCP-main-e2e-parallel/2117218708026822999"

// RunScenario builds a fixture and drives the complete local lifecycle.
func RunScenario(ctx context.Context, name, workDir string) (Report, error) {
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return Report{}, err
	}
	fixture, err := CreateFixture(ctx, filepath.Join(workDir, "repo"))
	if err != nil {
		return Report{}, err
	}
	if _, err := fixture.AddBareRemote(ctx, filepath.Join(workDir, "remote.git")); err != nil {
		return Report{}, err
	}
	registry, err := FixtureRegistry()
	if err != nil {
		return Report{}, err
	}
	clock := FixedClock()
	target := "1.1.0"
	expectation := "candidate prepared and validated, publication simulated, maintenance and continuation decided"
	switch name {
	case ScenarioNoUpdate:
		target = fixture.ToolVersion
		expectation = "no change, no candidate, no writes"
	case ScenarioGenerationFails:
		target = "9.0.0"
		expectation = "generation failure ends the run without a proposal or write"
	case ScenarioValidationRegression:
		target = "1.2.0"
		expectation = "candidate consumer failure blocks publication"
	case ScenarioCIPinPair:
		target = "0.49.0"
	case ScenarioHeldFailure:
		target = "1.1.0"
	case ScenarioLifecycle:
	default:
		return Report{}, fmt.Errorf("unknown scenario %q", name)
	}
	report := Report{Scenario: name, FixtureBase: fixture.BaseSHA, Expectation: expectation}

	prepareEngine := orchestration.New(orchestration.Deps{
		Clock:    clock,
		Registry: registry,
		Resolver: DefaultResolver(),
		Evidence: FakeFetcher{Result: "FAILURE", Log: "error: ManagedNode infra config cache not synchronized"},
	})
	report.Capabilities = append(report.Capabilities, "candidate stage: no forge writer configured")
	runWork := filepath.Join(workDir, "run")
	family := fixture.ToolModule
	if name == ScenarioCIPinPair {
		family = "Azure/bicep"
	}
	run, err := prepareEngine.Run(ctx, orchestration.RunOptions{
		RepositoryPath: fixture.Dir,
		Profile:        "tools",
		Offline:        false,
		Targets:        map[string]string{family: target},
		WorkDir:        runWork,
		StateDir:       filepath.Join(workDir, "state"),
		OutputPath:     filepath.Join(runWork, "run.json"),
	})
	if err != nil {
		return report, err
	}
	report.Run = run
	candidate, candidateErr := readCandidate(filepath.Join(runWork, "candidate.json"))
	if candidateErr == nil {
		report.Candidate = candidate
	}

	validation, err := readValidation(filepath.Join(runWork, "validation.json"))
	if err == nil {
		report.Validation = &validation
	}
	if run.Outcome != model.OutcomeAwaitingAuthorization {
		return report, nil
	}
	proposal, err := readProposal(filepath.Join(runWork, "proposal.json"))
	if err != nil {
		return report, err
	}
	report.Proposal = &proposal

	fake := forge.NewFake()
	fake.SetPageSize(2)
	publishEngine := orchestration.New(orchestration.Deps{
		Clock: clock, Registry: registry, Forge: fake, Writer: fake,
		Evidence: FakeFetcher{Result: "FAILURE", Log: "error: ManagedNode infra config cache not synchronized"},
	})
	report.Capabilities = append(report.Capabilities, "publication stage: candidate tree is not executed")
	receipt, err := publishEngine.Publish(ctx, proposal)
	if err != nil {
		return report, err
	}
	report.Publication = &receipt
	report.Mutations = fake.Mutations()

	pull, err := fake.PullRequest(ctx, receipt.PullRequestNumber)
	if err != nil {
		return report, err
	}
	seedEvidence(fake, pull, name)

	maintenanceEngine := orchestration.New(orchestration.Deps{
		Clock:    clock,
		Registry: registry,
		Forge:    fake,
		Evidence: scenarioFetcher(name, pull.HeadSHA),
	})
	checkout, err := repository.Open(fixture.Dir)
	if err != nil {
		return report, err
	}
	base := fixture.BaseSHA
	decision, err := maintenanceEngine.MaintenanceInspect(ctx, pull.BaseRepo, receipt.PullRequestNumber, orchestration.MaintenanceOptions{
		Investigate:    true,
		BaseSHA:        base,
		ChangedPaths:   checkout.ChangedPathsBetween,
		ComparisonURLs: comparisonURLs(name),
		EvidenceDir:    filepath.Join(workDir, "evidence"),
		StateDir:       filepath.Join(workDir, "state"),
	})
	if err != nil {
		return report, err
	}
	report.Maintenance = &decision

	merged := pull
	merged.State = "closed"
	merged.Merged = true
	continuation := orchestration.DecideContinuation(orchestration.ContinuationEvent{
		Repository: merged.BaseRepo, PullRequest: merged.Number, Event: "closed", Merged: true,
		Author: merged.Author, State: merged.State, Title: merged.Title, Body: merged.Body,
		Labels: merged.Labels, HeadRef: merged.HeadRef, HeadRepo: merged.HeadRepo,
		BaseRef: merged.BaseRef, BaseRepo: merged.BaseRepo, Target: "tool-version-updates",
	})
	report.Continuation = &continuation
	return report, nil
}

// Returns the comparison runs a scenario supplies.
func comparisonURLs(name string) []string {
	if name != ScenarioHeldFailure {
		return nil
	}
	return []string{ComparisonProwURL}
}

// Returns the evidence fetcher for a scenario.
func scenarioFetcher(name, head string) FakeFetcher {
	// The comparison run is the same job on another revision.
	revisions := map[string]string{ProwURL: head, ComparisonProwURL: strings.Repeat("f", 40)}
	if name != ScenarioHeldFailure {
		return FakeFetcher{Result: "FAILURE", Log: "error: unrelated", Revisions: revisions}
	}
	return FakeFetcher{
		Result:    "FAILURE",
		Results:   map[string]string{ComparisonProwURL: "SUCCESS"},
		Revisions: revisions,
		Log:       "2026-11-05T00:00:00Z error: ManagedNode infra config cache not synchronized\n",
	}
}

// Installs a snapshot for the published proposal.
func seedEvidence(fake *forge.Fake, pull forge.PullRequest, name string) {
	base := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	snapshot := forge.Snapshot{Pull: pull, Commits: []string{pull.HeadSHA}}
	if name == ScenarioHeldFailure {
		held := pull
		held.Labels = append(held.Labels, policy.HoldLabel)
		fake.UpdatePull(held)
		snapshot.Pull = held
		snapshot.Comments = []forge.Comment{{
			ID: 1, Author: policy.MergeBot,
			Body:      "/hold\n\nRevision " + pull.HeadSHA + " was retested 3 times: holding\n",
			CreatedAt: base.Format(time.RFC3339), UpdatedAt: base.Format(time.RFC3339),
		}}
		snapshot.HoldEvents = []forge.HoldEvent{{
			ID: 1, Event: "labeled", Actor: policy.ProwBot, CreatedAt: base.Format(time.RFC3339),
		}}
		snapshot.Statuses = []forge.Status{
			{ID: 1, Context: "ci/pipeline-gate", State: "failure", CreatedAt: base.Format(time.RFC3339), TargetURL: ProwURL},
			{ID: 2, Context: "ci/prow/unit", State: "success", CreatedAt: base.Format(time.RFC3339)},
		}
		snapshot.Checks = []forge.Check{
			{ID: 1, Name: "ci/prow/e2e-parallel", HeadSHA: pull.HeadSHA, Status: "completed", Conclusion: "failure", DetailsURL: ProwURL},
			{ID: 2, Name: "tide", HeadSHA: pull.HeadSHA, Status: "completed", Conclusion: "failure"},
		}
		fake.Seed(snapshot.Pull, snapshot)
		return
	}
	// A merged proposal with a full green current head.
	for index, context := range []string{"ci/pipeline-gate", "ci/prow/e2e-parallel", "ci/prow/unit"} {
		snapshot.Statuses = append(snapshot.Statuses, forge.Status{
			ID: index + 1, Context: context, State: "success", CreatedAt: base.Format(time.RFC3339),
		})
	}
	fake.Seed(pull, snapshot)
}

// Reads a proposal record from disk.
func readProposal(path string) (model.Proposal, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return model.Proposal{}, fmt.Errorf("read proposal: %w", err)
	}
	var proposal model.Proposal
	if err := schema.DecodeEnvelope(content, model.KindProposal, &proposal); err != nil {
		return model.Proposal{}, err
	}
	return proposal, nil
}

// Reads a validation record from disk.
func readCandidate(path string) (model.Candidate, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return model.Candidate{}, err
	}
	var record model.Candidate
	if err := schema.DecodeEnvelope(content, model.KindCandidate, &record); err != nil {
		return model.Candidate{}, err
	}
	return record, nil
}

// Reads a validation record from disk.
func readValidation(path string) (model.Validation, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return model.Validation{}, err
	}
	var record model.Validation
	if err := schema.DecodeEnvelope(content, model.KindValidation, &record); err != nil {
		return model.Validation{}, err
	}
	return record, nil
}

// FormatReport renders a human summary derived from the typed result.
func FormatReport(report Report) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "scenario: %s\n", report.Scenario)
	fmt.Fprintf(&builder, "expectation: %s\n", report.Expectation)
	fmt.Fprintf(&builder, "base: %s\n", report.FixtureBase)
	fmt.Fprintf(&builder, "outcome: %s (phase %s, complete=%t)\n", report.Run.Outcome, report.Run.Phase, report.Run.Complete)
	if report.Validation != nil {
		fmt.Fprintf(&builder, "validation: %s\n", report.Validation.Verdict)
	}
	if report.Publication != nil {
		fmt.Fprintf(&builder, "publication: number=%d confirmed=%t\n", report.Publication.PullRequestNumber, report.Publication.Confirmed)
	}
	if report.Maintenance != nil {
		fmt.Fprintf(&builder, "maintenance: action=%s diagnosis=%s canRecoverRetry=%t\n",
			report.Maintenance.Action, diagnosis(report.Maintenance), report.Maintenance.CanRecoverRetry)
	}
	if report.Continuation != nil {
		fmt.Fprintf(&builder, "continuation: eligible=%t target=%s (%s)\n",
			report.Continuation.Eligible, report.Continuation.Target, report.Continuation.Reason)
	}
	fmt.Fprintf(&builder, "mutations: %d\n", len(report.Mutations))
	for _, mutation := range report.Mutations {
		fmt.Fprintf(&builder, "  - %s branch=%s number=%d successful=%t\n",
			mutation.Operation, mutation.Branch, mutation.Number, mutation.Successful)
	}
	for _, capability := range report.Capabilities {
		fmt.Fprintf(&builder, "capability: %s\n", capability)
	}
	return builder.String()
}

// Returns the investigation diagnosis, or none.
func diagnosis(decision *model.MaintenanceDecision) string {
	if decision.Investigation == nil {
		return "none"
	}
	return decision.Investigation.Diagnosis
}
