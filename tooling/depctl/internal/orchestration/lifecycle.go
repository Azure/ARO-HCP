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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/forge"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/model"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/policy"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/repository"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/schema"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/state"
)

// RunOptions describe one lifecycle invocation.
type RunOptions struct {
	// RepositoryPath is the source checkout.
	RepositoryPath string
	// Profile is the policy profile; only "tools" is implemented.
	Profile string
	// Offline restricts discovery to local pins.
	Offline bool
	// Targets are explicit family=version targets. A tools run never invents one.
	Targets map[string]string
	// WorkDir holds candidates, evidence and logs.
	WorkDir string
	// StateDir holds the journal and persistent budgets.
	StateDir string
	// DryRun declares operations without creating a candidate.
	DryRun bool
	// KeepCandidate leaves the candidate worktree in place for inspection.
	KeepCandidate bool
	// OutputPath is the optional run result file.
	OutputPath string
}

// ErrAuthority reports a write that the current stage is not authorized for.
var ErrAuthority = errors.New("no write authority in this stage")

// ErrSingleFlight reports an existing owned proposal that defers new work.
var ErrSingleFlight = errors.New("an owned proposal is already open")

// Run executes discover, plan, prepare, validate and propose.
//
// It stops before any remote write. Missing authority is an outcome, never a
// failure that a retry could fix.
func (e *Engine) Run(ctx context.Context, options RunOptions) (model.RunResult, error) {
	if options.Profile != "tools" {
		return model.RunResult{}, fmt.Errorf("unsupported profile %q", options.Profile)
	}
	journal, err := e.openJournal(options.StateDir)
	if err != nil {
		return model.RunResult{}, err
	}
	if journal != nil {
		release, err := journal.Lock()
		if err != nil {
			return model.RunResult{}, err
		}
		defer release()
	}
	runID := state.NewRunID(e.deps.Clock)
	result := model.RunResult{
		Envelope:      schema.Envelope{SchemaVersion: schema.Version, Kind: model.KindRunResult},
		Operation:     "run",
		RunID:         runID,
		Profile:       options.Profile,
		EvidenceScope: model.ScopeLocalOnly,
		Phase:         model.PhaseDiscover,
		Outcome:       model.OutcomeFailed,
	}
	logf := func(phase, kind, action string, detail map[string]any) {
		if journal == nil {
			return
		}
		_ = journal.Append(state.Event{RunID: runID, Phase: phase, Kind: kind, Action: action, Detail: detail}, e.deps.Clock)
	}

	checkout, err := repository.Open(options.RepositoryPath)
	if err != nil {
		return result, err
	}
	base, err := checkout.Head(ctx)
	if err != nil {
		return result, err
	}
	result.Repository = repositoryName(ctx, checkout)
	result.BaseSHA = base
	result.PolicyDigest, err = e.policyDigest()
	if err != nil {
		return result, err
	}
	logf(model.PhaseDiscover, state.EventIntent, "discover", map[string]any{"base": base, "offline": options.Offline})

	inventory, err := e.Inventory(ctx, options.RepositoryPath, base, options.Profile, options.Offline)
	if err != nil {
		return finish(result, model.OutcomeFailed, model.PhaseDiscover, err.Error(), options.OutputPath)
	}
	result.EvidenceScope = inventory.EvidenceScope
	if err := e.writeArtifact(options.WorkDir, "inventory.json", inventory); err != nil {
		return result, err
	}
	logf(model.PhaseDiscover, state.EventObserved, "inventory", map[string]any{"complete": inventory.Complete, "errors": inventory.Errors})

	result.Phase = model.PhasePlan
	plan, err := e.Plan(inventory, options.Targets)
	if err != nil {
		return finish(result, model.OutcomeBlocked, model.PhasePlan, err.Error(), options.OutputPath)
	}
	result.PlanID = plan.PlanID
	result.Families = summaries(plan)
	result.Blockers = append(result.Blockers, plan.Blockers...)
	if err := e.writeArtifact(options.WorkDir, "plan.json", plan); err != nil {
		return result, err
	}
	logf(model.PhasePlan, state.EventDecision, "plan", map[string]any{"planID": plan.PlanID, "outcome": plan.Outcome})

	switch plan.Outcome {
	case model.OutcomeNoChange:
		result.Complete = inventory.Complete
		return finish(result, model.OutcomeNoChange, model.PhasePlan, "", options.OutputPath)
	case model.OutcomeBlocked:
		return finish(result, model.OutcomeBlocked, model.PhasePlan, strings.Join(plan.Blockers, "; "), options.OutputPath)
	}

	if options.DryRun {
		preview := Declare(plan)
		if err := e.writeArtifact(options.WorkDir, "preview.json", preview); err != nil {
			return result, err
		}
		result.Complete = true
		result.Actions = append(result.Actions, "dry-run: declared operations only")
		return finish(result, model.OutcomePrepared, model.PhaseAwaitAuth, "", options.OutputPath)
	}

	result.Phase = model.PhasePrepare
	candidateDir := filepath.Join(options.WorkDir, "candidate")
	record, candidate, err := e.ApplyWithCheckout(ctx, checkout, plan, candidateDir, options.WorkDir)
	if err != nil {
		return finish(result, outcomeFor(err), model.PhasePrepare, err.Error(), options.OutputPath)
	}
	result.CandidateID = record.CandidateID
	defer func() {
		if !options.KeepCandidate {
			_ = candidate.Close(ctx, checkout)
		}
	}()
	if err := e.writeArtifact(options.WorkDir, "candidate.json", record); err != nil {
		return result, err
	}
	logf(model.PhasePrepare, state.EventObserved, "candidate", map[string]any{
		"candidateID": record.CandidateID, "changedPaths": record.ChangedPaths,
	})

	result.Phase = model.PhaseValidate
	verdict, err := e.Verify(ctx, checkout, plan, record, candidate, options.WorkDir)
	if err != nil {
		return finish(result, model.OutcomeFailed, model.PhaseValidate, err.Error(), options.OutputPath)
	}
	if err := e.writeArtifact(options.WorkDir, "validation.json", verdict); err != nil {
		return result, err
	}
	logf(model.PhaseValidate, state.EventObserved, "validation", map[string]any{"verdict": verdict.Verdict, "notRun": verdict.NotRun})
	result.Phase = model.PhaseProposalReady
	if verdict.Verdict != model.VerdictValidated {
		result.Blockers = append(result.Blockers, "validation verdict: "+verdict.Verdict)
		result.Blockers = append(result.Blockers, verdict.NotRun...)
		return finish(result, model.OutcomeBlocked, model.PhaseValidate, "", options.OutputPath)
	}
	proposal, err := e.BuildProposal(ctx, checkout, plan, candidate, record, verdict, options.WorkDir)
	if err != nil {
		return finish(result, model.OutcomeFailed, model.PhaseProposalReady, err.Error(), options.OutputPath)
	}
	if err := e.writeArtifact(options.WorkDir, "proposal.json", proposal); err != nil {
		return result, err
	}
	logf(model.PhaseAwaitAuth, state.EventDecision, "proposal", map[string]any{
		"proposalID": proposal.ProposalID, "branch": proposal.Branch,
	})
	result.Actions = append(result.Actions, "candidate prepared and validated; no remote write attempted")
	result.Complete = true
	return finish(result, model.OutcomeAwaitingAuthorization, model.PhaseAwaitAuth, "", options.OutputPath)
}

// Maps an error onto a terminal outcome.
func outcomeFor(err error) string {
	switch {
	case errors.Is(err, ErrStale):
		return model.OutcomeStale
	case errors.Is(err, ErrGeneration):
		return model.OutcomeFailed
	case errors.Is(err, context.Canceled):
		return model.OutcomeInterrupted
	default:
		return model.OutcomeFailed
	}
}

// Renders the planned families for the run result.
func summaries(plan model.Plan) []model.FamilySummary {
	var summaries []model.FamilySummary
	for _, family := range plan.Families {
		summaries = append(summaries, model.FamilySummary{
			Family: family.Family, Status: model.VersionOutdated, From: family.From, To: family.To,
		})
	}
	return summaries
}

// Records a terminal outcome and writes the run result.
func finish(result model.RunResult, outcome, phase, message, outputPath string) (model.RunResult, error) {
	result.Outcome = outcome
	result.Phase = phase
	if message != "" {
		result.Errors = append(result.Errors, message)
	}
	sort.Strings(result.Blockers)
	if err := WriteRecord(outputPath, result); err != nil {
		return result, err
	}
	return result, nil
}

// Returns OWNER/REPO from the checkout remotes.
func repositoryName(ctx context.Context, checkout *repository.Repo) string {
	name, err := checkout.TopLevel(ctx)
	if err != nil {
		return ""
	}
	return name
}

// Returns the configured journal or one opened at the state directory.
func (e *Engine) openJournal(directory string) (*state.Journal, error) {
	if directory == "" {
		return nil, nil
	}
	if e.deps.Journal != nil {
		return e.deps.Journal, nil
	}
	return state.Open(directory)
}

// Writes one phase artifact into the work directory.
func (e *Engine) writeArtifact(directory, name string, record any) error {
	if directory == "" {
		return nil
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create work directory: %w", err)
	}
	return WriteRecord(filepath.Join(directory, name), record)
}

// Publish performs the trusted write stage for one validated proposal.
//
// The candidate tree is not executed here. Every precondition is re-read from
// the forge immediately before a write, and a lost response is reconciled
// against remote truth instead of being retried blindly.
func (e *Engine) Publish(ctx context.Context, proposal model.Proposal) (model.PublicationReceipt, error) {
	if e.deps.Writer == nil || e.deps.Forge == nil {
		return model.PublicationReceipt{}, fmt.Errorf("%w: publication adapters are not configured", ErrAuthority)
	}
	receipt := model.PublicationReceipt{
		Envelope:     schema.Envelope{SchemaVersion: schema.Version, Kind: model.KindPublication},
		OperationKey: proposal.ProposalID,
		Repository:   proposal.Repository,
		Branch:       proposal.Branch,
		ObservedAt:   e.timestamp(),
	}
	if reconciled, reconcileErr := e.reconcileProposal(ctx, proposal); reconcileErr == nil {
		// A previous attempt already published this operation: reuse the match.
		receipt.CommitSHA = reconciled.HeadSHA
		receipt.PullRequestNumber = reconciled.Number
		receipt.PullRequestURL = reconciled.URL(proposal.Repository)
		receipt.Confirmed = true
		return receipt, nil
	}
	open, err := e.deps.Forge.OpenPullRequests(ctx, policy.ToolOwner)
	if err != nil {
		return receipt, err
	}
	if _, exists := policy.OpenOwnedToolProposal(toPolicyPulls(open)); exists {
		receipt.Unfinished = append(receipt.Unfinished, "single-flight slot is occupied by an owned tool proposal")
		return receipt, ErrSingleFlight
	}
	branchHead, exists, err := e.deps.Forge.BranchHead(ctx, proposal.Branch)
	if err != nil {
		receipt.Unfinished = append(receipt.Unfinished, "existing branch state could not be read")
		return receipt, err
	}
	if exists {
		if branchHead != proposal.Expected.BaseSHA {
			receipt.Unfinished = append(receipt.Unfinished, "branch already exists at an unexpected head")
			return receipt, ErrStale
		}
		receipt.CommitSHA = branchHead
	}
	e.journal("publish", state.EventIntent, "create-branch", map[string]any{
		"branch": proposal.Branch, "base": proposal.Expected.BaseSHA,
	})
	if err := e.deps.Writer.CreateBranch(ctx, proposal.Branch, proposal.Expected.BaseSHA); err != nil {
		// The response may have been lost after the branch was created.
		head, created, readErr := e.deps.Forge.BranchHead(ctx, proposal.Branch)
		if readErr != nil || !created {
			receipt.Unfinished = append(receipt.Unfinished, "branch creation outcome is unknown")
			return receipt, err
		}
		if head != proposal.Expected.BaseSHA {
			receipt.Unfinished = append(receipt.Unfinished, "reconciled branch head is not the expected base")
			return receipt, ErrStale
		}
		receipt.CommitSHA = head
	}
	if receipt.CommitSHA == "" {
		head, created, err := e.deps.Forge.BranchHead(ctx, proposal.Branch)
		if err != nil || !created {
			receipt.Unfinished = append(receipt.Unfinished, "branch head could not be read")
			if err == nil {
				err = fmt.Errorf("%w: branch does not exist after creation", ErrStale)
			}
			return receipt, err
		}
		receipt.CommitSHA = head
	}
	e.journal("publish", state.EventIntent, "create-pull-request", map[string]any{
		"branch": proposal.Branch, "base": proposal.Base, "commit": receipt.CommitSHA,
	})
	created, err := e.deps.Writer.CreatePullRequest(ctx, forge.CreateRequest{
		Branch: proposal.Branch, Base: proposal.Base, Commit: receipt.CommitSHA,
		Title: proposal.Title, Body: proposal.Body, Labels: proposal.Labels,
	})
	if err != nil {
		reconciled, readErr := e.reconcileProposal(ctx, proposal)
		if readErr != nil {
			receipt.Unfinished = append(receipt.Unfinished, "pull-request creation outcome is unknown")
			return receipt, err
		}
		receipt.PullRequestNumber = reconciled.Number
		receipt.PullRequestURL = reconciled.URL(proposal.Repository)
		receipt.CommitSHA = reconciled.HeadSHA
		receipt.Confirmed = true
		return receipt, nil
	}
	receipt.PullRequestNumber = created.Number
	receipt.PullRequestURL = created.URL
	receipt.ObservedAuthor = created.Author
	receipt.ObservedHeadRepo = created.HeadRepo
	receipt.ObservedBase = created.Base
	receipt.CommitSHA = created.HeadSHA
	observed, err := e.deps.Forge.PullRequest(ctx, created.Number)
	if err != nil {
		receipt.Unfinished = append(receipt.Unfinished, "created proposal could not be re-read")
		return receipt, err
	}
	if observed.Author != policy.ToolOwner || observed.HeadRepo != proposal.Repository || observed.BaseRepo != proposal.Repository {
		receipt.Unfinished = append(receipt.Unfinished, "observed proposal identity does not match the owned identity")
		return receipt, fmt.Errorf("%w: observed identity mismatch", ErrAuthority)
	}
	if observed.Draft {
		receipt.Unfinished = append(receipt.Unfinished, "proposal was created as a draft")
	}
	receipt.Confirmed = !observed.Draft
	receipt.ObservedAt = e.timestamp()
	e.journal("publish", state.EventObserved, "create-pull-request", map[string]any{
		"number": observed.Number, "confirmed": receipt.Confirmed,
	})
	return receipt, nil
}

// Records one lifecycle event when a journal is configured. Publication
// writes its intent before the effect and its observation afterwards so a crash
// can be reconciled against remote truth.
// Records one lifecycle event when a journal is configured. Publication
// writes its intent before the effect and its observation afterwards so a crash
// can be reconciled against remote truth.
func (e *Engine) journal(phase, kind, action string, detail map[string]any) {
	if e.deps.Journal == nil {
		return
	}
	_ = e.deps.Journal.Append(state.Event{
		Phase: phase, Kind: kind, Action: action, Detail: detail,
	}, e.deps.Clock)
}

// Finds the unique owned proposal for a branch and marker.
func (e *Engine) reconcileProposal(ctx context.Context, proposal model.Proposal) (forge.PullRequest, error) {
	open, err := e.deps.Forge.OpenPullRequests(ctx, policy.ToolOwner)
	if err != nil {
		return forge.PullRequest{}, err
	}
	var matches []forge.PullRequest
	for _, pull := range open {
		if pull.HeadRef != proposal.Branch {
			continue
		}
		for _, marker := range proposal.Markers {
			if strings.Contains(pull.Body, marker) {
				matches = append(matches, pull)
				break
			}
		}
	}
	if len(matches) != 1 {
		return forge.PullRequest{}, fmt.Errorf("%w: %d matching proposals after a lost response", ErrStale, len(matches))
	}
	return matches[0], nil
}

// Converts forge views into policy views.
func toPolicyPulls(pulls []forge.PullRequest) []policy.PullRequest {
	converted := make([]policy.PullRequest, 0, len(pulls))
	for _, pull := range pulls {
		converted = append(converted, policy.PullRequest{
			Number: pull.Number, Author: pull.Author, State: pull.State, Draft: pull.Draft,
			Base: pull.BaseRef, HeadRef: pull.HeadRef, Title: pull.Title, Body: pull.Body,
			Labels: pull.Labels, HeadRepo: pull.HeadRepo, BaseRepo: pull.BaseRepo,
		})
	}
	return converted
}

// DecideContinuation applies typed continuation policy to one trusted event.
func DecideContinuation(event ContinuationEvent) model.ContinuationDecision {
	decision := model.ContinuationDecision{
		Envelope:   schema.Envelope{SchemaVersion: schema.Version, Kind: model.KindContinuation},
		Repository: event.Repository, PullRequest: event.PullRequest, Event: event.Event,
	}
	pull := policy.PullRequest{
		Number: event.PullRequest, Author: event.Author, State: event.State, Draft: event.Draft,
		Base: event.BaseRef, HeadRef: event.HeadRef, Title: event.Title, Body: event.Body,
		Labels: event.Labels, HeadRepo: event.HeadRepo, BaseRepo: event.BaseRepo,
	}
	switch {
	case event.Event != "closed":
		decision.Reason = "event is not a pull-request close"
	case !event.Merged:
		decision.Reason = "pull request closed without merging"
	case event.Author != policy.ToolOwner:
		decision.Reason = "author is not the owned robot identity"
	case event.HeadRepo != event.Repository || event.BaseRepo != event.Repository:
		decision.Reason = "head or base repository is outside the repository"
	case event.BaseRef != policy.ToolBase:
		decision.Reason = "base ref is not main"
	case !policy.IsToolProposal(pull):
		decision.Reason = "pull request does not carry the tool profile markers"
	default:
		decision.Reason = "merged owned tool proposal"
	}
	switch {
	case decision.Reason == "merged owned tool proposal" && event.Target != "":
		decision.Eligible = true
		decision.Target = event.Target
	case decision.Reason == "merged owned tool proposal":
		decision.Reason = "continuation target is not configured"
	}
	return decision
}

// ContinuationEvent is the trusted close-event view.
type ContinuationEvent struct {
	Repository  string
	PullRequest int
	Event       string
	Merged      bool
	Author      string
	State       string
	Draft       bool
	Title       string
	Body        string
	Labels      []string
	HeadRef     string
	HeadRepo    string
	BaseRef     string
	BaseRepo    string
	Target      string
}
