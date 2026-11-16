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
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/model"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/policy"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/repository"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/schema"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/validation"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/workspace"
)

// Refuses a candidate that edits outside the permitted classes.
//
// Permitted classes are derived from the validated targets, not from the
// plan's own claims, so an out-of-scope generated path fails closed.
func checkScope(changed []string, plan model.Plan) error {
	permitted := map[string]bool{}
	for _, family := range plan.Families {
		for _, target := range family.Targets {
			permitted[target.Source] = true
			// A planned module edit also records the module checksum file, but
			// no other module may change.
			if target.Kind == model.EditBingoModule && strings.HasSuffix(target.Source, ".mod") {
				permitted[strings.TrimSuffix(target.Source, ".mod")+".sum"] = true
			}
		}
		// The shared Bingo helpers are only derived for a Go-module family.
		if family.Kind == model.FamilyGoModule {
			permitted[workspace.BingoHelperPath] = true
			permitted[".bingo/variables.env"] = true
		}
	}
	for _, changedPath := range changed {
		if permitted[changedPath] {
			continue
		}
		return fmt.Errorf("%w: candidate modified %s, which the plan does not cover", ErrGeneration, changedPath)
	}
	return nil
}

// Verify runs every required consumer on the baseline and the candidate.
//
// The candidate must still match its record before and after the consumers run:
// a worktree edited after apply is stale, and a consumer that rewrites tracked
// files is drift, not validation.
func (e *Engine) Verify(ctx context.Context, checkout *repository.Repo, plan model.Plan, record model.Candidate, candidate *workspace.Candidate, logDir string) (model.Validation, error) {
	if err := e.ValidatePlan(ctx, checkout.Path, plan); err != nil {
		return model.Validation{}, err
	}
	if err := e.ensureSourceCurrent(ctx, checkout, plan); err != nil {
		return model.Validation{}, err
	}
	if err := workspace.AssertOutsideSource(candidate.Path(), checkout.Path); err != nil {
		return model.Validation{}, err
	}
	if err := verifyRecordMatchesPlan(candidate, plan, record); err != nil {
		return model.Validation{}, err
	}
	if err := VerifyCandidateBinding(ctx, candidate, record); err != nil {
		return model.Validation{}, err
	}
	if logDir == "" {
		// Never place the baseline worktree inside the source checkout.
		allocated, err := os.MkdirTemp("", "depctl-verify-")
		if err != nil {
			return model.Validation{}, err
		}
		logDir = allocated
	}
	baseline, err := workspace.Prepare(ctx, checkout, filepath.Join(logDir, "baseline"), plan.BaseSHA)
	if err != nil {
		return model.Validation{}, err
	}
	defer func() { _ = baseline.Close(ctx, checkout) }()
	// Consumers are derived from compiled policy, so a plan with a stripped or
	// extended consumer list cannot weaken the gates that actually run.
	refs := consumerPlan(plan.Families)
	var results []model.ConsumerResult
	for _, reference := range refs {
		baselineResult := e.deps.Runner.Run(ctx, baseline.Path(), e.phaseConsumerRef(reference, plan.Families, true), logDir)
		baselineResult.Phase = validation.PhaseBaseline
		results = append(results, baselineResult)
		candidateResult := e.deps.Runner.Run(ctx, candidate.Path(), e.phaseConsumerRef(reference, plan.Families, false), logDir)
		candidateResult.Phase = validation.PhaseCandidate
		results = append(results, candidateResult)
	}
	if err := VerifyCandidateBinding(ctx, candidate, record); err != nil {
		return model.Validation{}, err
	}
	return validation.Compare(plan, record, results), nil
}

// BuildProposal packages a validated candidate, conferring no authorization.
//
// The patch is recomputed from the worktree and must match the validated
// digest; the candidate commit is the only revision a publisher may publish.
func (e *Engine) BuildProposal(ctx context.Context, checkout *repository.Repo, plan model.Plan, candidate *workspace.Candidate, record model.Candidate, verdict model.Validation, artifactDir string) (model.Proposal, error) {
	if verdict.Verdict != model.VerdictValidated {
		return model.Proposal{}, fmt.Errorf("candidate validation verdict %q does not permit a proposal", verdict.Verdict)
	}
	if verdict.PlanID != plan.PlanID || verdict.CandidateID != record.CandidateID {
		return model.Proposal{}, fmt.Errorf("%w: validation does not belong to this plan and candidate", ErrPlanRejected)
	}
	if err := e.ValidatePlan(ctx, checkout.Path, plan); err != nil {
		return model.Proposal{}, err
	}
	if err := e.ensureSourceCurrent(ctx, checkout, plan); err != nil {
		return model.Proposal{}, err
	}
	// A decoded verdict is untrusted: recompute it from the recorded results
	// and refuse a contradictory record.
	recomputed := validation.Compare(plan, record, verdict.Results)
	if recomputed.Verdict != model.VerdictValidated || verdict.Verdict != recomputed.Verdict || !verdict.Complete {
		return model.Proposal{}, fmt.Errorf("%w: validation record is not a validated result set (verdict %q, recomputed %q)",
			ErrPlanRejected, verdict.Verdict, recomputed.Verdict)
	}
	if err := workspace.AssertOutsideSource(candidate.Path(), checkout.Path); err != nil {
		return model.Proposal{}, err
	}
	if err := verifyRecordMatchesPlan(candidate, plan, record); err != nil {
		return model.Proposal{}, err
	}
	if err := VerifyCandidateBinding(ctx, candidate, record); err != nil {
		return model.Proposal{}, err
	}
	if len(plan.Families) != 1 {
		return model.Proposal{}, fmt.Errorf("%w: a proposal covers exactly one family, got %d",
			ErrPlanRejected, len(plan.Families))
	}
	if !repository.IsCommit(record.CommitSHA) {
		return model.Proposal{}, fmt.Errorf("%w: the candidate has no committed revision", ErrCandidateDrift)
	}
	patch, err := candidate.Root.Diff(ctx, plan.BaseSHA)
	if err != nil {
		return model.Proposal{}, err
	}
	if digestOf(patch) != record.DiffDigest {
		return model.Proposal{}, fmt.Errorf("%w: the emitted patch does not match the validated digest", ErrCandidateDrift)
	}
	if artifactDir != "" {
		if err := os.MkdirAll(artifactDir, 0o755); err != nil {
			return model.Proposal{}, fmt.Errorf("create artifact directory: %w", err)
		}
	}
	patchPath := filepath.Join(artifactDir, "candidate.patch")
	if err := os.WriteFile(patchPath, patch, 0o600); err != nil {
		return model.Proposal{}, fmt.Errorf("write candidate patch: %w", err)
	}
	family := plan.Families[0]
	proposal := model.Proposal{
		Envelope:            schema.Envelope{SchemaVersion: schema.Version, Kind: model.KindProposal},
		PlanID:              plan.PlanID,
		CandidateID:         record.CandidateID,
		ValidationID:        verdict.PlanID,
		GroupID:             family.Family,
		Repository:          plan.Repository,
		Base:                policy.ToolBase,
		Branch:              policy.ToolBranch(family.Family, family.To),
		Title:               policy.ToolTitle(family.Family, family.To),
		Labels:              []string{"dependencies", policy.ToolLabel},
		Markers:             policy.ToolMarkers(family.Family),
		DiffDigest:          record.DiffDigest,
		PatchPath:           patchPath,
		PermittedOperations: []string{"create-branch", "create-pull-request", "label"},
		Expected: model.StateExpectation{
			BranchExists:  false,
			BranchHeadSHA: record.CommitSHA,
			BaseSHA:       plan.BaseSHA,
		},
		RiskNotes: append([]string{
			"publication must re-read the base revision, ownership and single-flight slot before writing",
			"publication must push the committed candidate revision and verify the observed head",
		}, policy.ConsumerGaps(plan.Families)...),
	}
	proposal.Body = proposalBody(plan, family, record)
	proposalID, err := schema.DigestsWithPrefix("depctl/proposal/v1",
		plan.PlanID, record.CandidateID, record.DiffDigest, proposal.GroupID, proposal.Branch, proposal.Base, record.CommitSHA)
	if err != nil {
		return model.Proposal{}, err
	}
	proposal.ProposalID = proposalID
	return proposal, nil
}

// verifyRecordMatchesPlan binds a candidate record to the trusted plan: the
// same plan and base, a change set the plan covers, and target files that
// actually carry the planned values.
func verifyRecordMatchesPlan(candidate *workspace.Candidate, plan model.Plan, record model.Candidate) error {
	if record.PlanID != plan.PlanID {
		return fmt.Errorf("%w: candidate belongs to plan %s, not %s", ErrPlanRejected, record.PlanID, plan.PlanID)
	}
	if record.BaseSHA != plan.BaseSHA {
		return fmt.Errorf("%w: candidate base %s is not the plan base %s", ErrStale, record.BaseSHA, plan.BaseSHA)
	}
	if err := checkScope(record.ChangedPaths, plan); err != nil {
		return err
	}
	for _, family := range plan.Families {
		for _, target := range family.Targets {
			location, err := workspace.ConfinePath(candidate.Path(), target.Source)
			if err != nil {
				return err
			}
			content, err := os.ReadFile(location)
			if err != nil {
				return fmt.Errorf("%w: read %s: %v", ErrCandidateDrift, target.Source, err)
			}
			if !carriesTarget(content, family, target) {
				return fmt.Errorf("%w: %s does not carry %s", ErrCandidateDrift, target.Source, target.To)
			}
		}
	}
	return nil
}

// carriesTarget reports whether a candidate file records the planned value.
func carriesTarget(content []byte, family model.PlannedFamily, target model.PinTarget) bool {
	quoted := regexp.QuoteMeta(target.To)
	switch target.Kind {
	case model.EditMakeVariable:
		return regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(target.Variable) + `\s*\?=\s*` + quoted + `\s*$`).Match(content)
	case model.EditDockerfileArg:
		return regexp.MustCompile(`(?m)^ARG ` + regexp.QuoteMeta(target.Variable) + `=` + quoted + `\s*$`).Match(content)
	case model.EditBingoModule:
		return regexp.MustCompile(`(?m)^require ` + regexp.QuoteMeta(family.Family) + `\s+v?` + quoted + `(?:\s|$)`).Match(content)
	default:
		return false
	}
}
