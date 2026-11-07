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

// Package orchestration composes discovery, policy, candidate preparation,
// validation and evidence into the lifecycle state machine.
package orchestration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/consumers"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/discovery"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/evidence"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/forge"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/model"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/policy"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/repository"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/schema"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/state"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/validation"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/workspace"
)

// Deps are the injected adapters of one engine.
//
// A nil Writer means the engine cannot publish at all; a nil Resolver means
// discovery must stay offline. Adapters are never discovered from the
// environment by the engine itself.
type Deps struct {
	Clock    func() time.Time
	Registry *consumers.Registry
	Resolver discovery.Resolver
	Forge    forge.Reader
	Writer   forge.Writer
	Evidence evidence.Fetcher
	Journal  *state.Journal
	// Runner executes consumers; when nil a default runner is used.
	Runner *consumers.Runner
}

// Engine runs the lifecycle.
type Engine struct {
	deps Deps
}

// New returns an engine, filling in safe defaults.
func New(deps Deps) *Engine {
	if deps.Clock == nil {
		deps.Clock = time.Now
	}
	if deps.Registry == nil {
		deps.Registry = consumers.Default()
	}
	if deps.Runner == nil {
		deps.Runner = consumers.NewRunner(deps.Registry)
	}
	if deps.Evidence == nil {
		deps.Evidence = evidence.NewHTTPFetcher()
	}
	return &Engine{deps: deps}
}

// Clock returns the injected clock.
func (e *Engine) Clock() time.Time { return e.deps.Clock() }

// Capabilities reports which adapters are configured.
// Reports which adapters are configured.
func (e *Engine) Capabilities() (readForge, writeForge, resolver, evidenceFetcher bool) {
	return e.deps.Forge != nil, e.deps.Writer != nil, e.deps.Resolver != nil, e.deps.Evidence != nil
}

// Returns the injected clock as an RFC3339 timestamp.
func (e *Engine) timestamp() string { return e.deps.Clock().UTC().Format(time.RFC3339) }

// Inventory discovers repository pins.
func (e *Engine) Inventory(ctx context.Context, repositoryPath, revision, profile string, offline bool) (model.Inventory, error) {
	checkout, err := repository.Open(repositoryPath)
	if err != nil {
		return model.Inventory{}, err
	}
	resolved := revision
	if resolved == "" {
		resolved, err = checkout.Head(ctx)
		if err != nil {
			return model.Inventory{}, err
		}
	}
	return discovery.Build(ctx, checkout, resolved, profile, discovery.Options{
		Offline:  offline,
		Resolver: e.deps.Resolver,
		Clock:    e.deps.Clock,
	})
}

// PlanPinned hashes and returns a plan for the requested targets.
func (e *Engine) PlanPinned(inventory model.Inventory, targets map[string]string, selection policy.Selection) (model.Plan, error) {
	plan := model.Plan{
		Envelope:      schema.Envelope{SchemaVersion: schema.Version, Kind: model.KindPlan},
		Repository:    inventory.Repository,
		BaseSHA:       inventory.BaseSHA,
		Profile:       inventory.Profile,
		CreatedAt:     e.timestamp(),
		EvidenceScope: inventory.EvidenceScope,
		Families:      selection.Families,
		Blockers:      selection.Blockers,
	}
	if len(selection.Families) == 0 {
		plan.Outcome = model.OutcomeNoChange
	} else if len(plan.Blockers) > 0 {
		plan.Outcome = model.OutcomeBlocked
	} else {
		plan.Outcome = model.OutcomePrepared
		plan.Consumers = policy.RequiredConsumers(selection.Families)
	}
	digest, err := schema.Digest(planIdentity(plan))
	if err != nil {
		return model.Plan{}, err
	}
	plan.PlanID = digest
	plan.PolicyDigest, err = e.policyDigest()
	if err != nil {
		return model.Plan{}, err
	}
	return plan, nil
}

// Plan produces a plan from an inventory record.
func (e *Engine) Plan(inventory model.Inventory, targets map[string]string) (model.Plan, error) {
	selection, err := policy.SelectFamilies(inventory, targets)
	if err != nil {
		return model.Plan{}, err
	}
	return e.PlanPinned(inventory, targets, selection)
}

// PlanIdentity is the canonical identity of a plan: repository, base, profile,
// policy, sorted targets, scope and consumers. Timestamps and run IDs are
// deliberately excluded so a retried run is the same logical plan.
// PlanIdentity is the canonical identity of a plan: repository, base, profile,
// policy, sorted targets, scope and consumers. Timestamps and run IDs are
// deliberately excluded so a retried run is the same logical plan.
func planIdentity(plan model.Plan) map[string]any {
	var targets []string
	for _, family := range plan.Families {
		for _, target := range family.Targets {
			targets = append(targets, family.Family+" "+target.Source+" "+target.Variable+" "+target.From+"->"+target.To)
		}
	}
	sort.Strings(targets)
	var consumers []string
	for _, consumer := range plan.Consumers {
		keys := make([]string, 0, len(consumer.Params))
		for key := range consumer.Params {
			keys = append(keys, key+"="+consumer.Params[key])
		}
		sort.Strings(keys)
		consumers = append(consumers, consumer.ID+" "+strings.Join(keys, ","))
	}
	sort.Strings(consumers)
	return map[string]any{
		"repository": plan.Repository,
		"baseSHA":    plan.BaseSHA,
		"profile":    plan.Profile,
		"targets":    targets,
		"consumers":  consumers,
		"outcome":    plan.Outcome,
	}
}

// Digests the compiled policy that shaped a plan.
func (e *Engine) policyDigest() (string, error) {
	limits := policy.DefaultLimits()
	return schema.Digest(map[string]any{
		"limits":    limits,
		"toolOwner": policy.ToolOwner,
		"toolLabel": policy.ToolLabel,
		"toolBase":  policy.ToolBase,
		"holdLabel": policy.HoldLabel,
		"required":  []string{"ci/pipeline-gate", "ci/prow/e2e-parallel"},
		"consumers": e.deps.Registry.IDs(),
	})
}

// Operation is one declared candidate edit, reported by a dry run.
type Operation struct {
	Kind     string `json:"kind"`
	Path     string `json:"path"`
	Variable string `json:"variable,omitempty"`
	From     string `json:"from,omitempty"`
	To       string `json:"to,omitempty"`
}

// Preview is the dry-run result: declared operations, never a fabricated patch.
type Preview struct {
	PlanID     string      `json:"planID"`
	BaseSHA    string      `json:"baseSHA"`
	DryRun     bool        `json:"dryRun"`
	Operations []Operation `json:"operations"`
	Generators []string    `json:"generators"`
	Note       string      `json:"note"`
}

// Declare lists the operations apply would perform.
func Declare(plan model.Plan) Preview {
	preview := Preview{
		PlanID: plan.PlanID, BaseSHA: plan.BaseSHA, DryRun: true,
		Note: "dry run declares planned operations only; no install, generation, candidate, commit or remote write occurred",
	}
	generators := map[string]bool{}
	for _, family := range plan.Families {
		for _, target := range family.Targets {
			operation := Operation{Path: target.Source, Variable: target.Variable, From: target.From, To: target.To}
			switch family.Kind {
			case model.FamilyGoModule:
				operation.Kind = "go-mod-edit"
				generators["bingo-regenerate"] = true
			case model.FamilyGitHubRelease:
				operation.Kind = "make-variable"
			}
			preview.Operations = append(preview.Operations, operation)
		}
	}
	for id := range generators {
		preview.Generators = append(preview.Generators, id)
	}
	sort.Strings(preview.Generators)
	return preview
}

// ApplyWithCheckout prepares an isolated candidate from a known checkout.
func (e *Engine) ApplyWithCheckout(ctx context.Context, checkout *repository.Repo, plan model.Plan, candidateDir, logDir string) (model.Candidate, *workspace.Candidate, error) {
	if plan.Outcome != model.OutcomePrepared {
		return model.Candidate{}, nil, fmt.Errorf("plan outcome %q cannot be applied", plan.Outcome)
	}
	head, err := checkout.Head(ctx)
	if err != nil {
		return model.Candidate{}, nil, err
	}
	if head != plan.BaseSHA {
		return model.Candidate{}, nil, fmt.Errorf("%w: base moved from %s to %s", ErrStale, plan.BaseSHA, head)
	}
	if err := e.ensureUnmodified(checkout, plan); err != nil {
		return model.Candidate{}, nil, err
	}
	candidate, err := workspace.Prepare(ctx, checkout, candidateDir, plan.BaseSHA)
	if err != nil {
		return model.Candidate{}, nil, err
	}
	record := model.Candidate{
		Envelope:    schema.Envelope{SchemaVersion: schema.Version, Kind: model.KindCandidate},
		PlanID:      plan.PlanID,
		BaseSHA:     plan.BaseSHA,
		Workspace:   candidate.Path(),
		FileDigests: map[string]string{},
	}
	generated := map[string]string{}
	identities := map[string]bool{}
	var generatorRuns []model.ConsumerRef
	for _, family := range plan.Families {
		for _, target := range family.Targets {
			path := filepath.Join(candidate.Path(), filepath.FromSlash(target.Source))
			switch family.Kind {
			case model.FamilyGitHubRelease:
				if err := workspace.SetVersionVariable(path, target.Variable, target.To); err != nil {
					_ = candidate.Close(ctx, checkout)
					return model.Candidate{}, nil, err
				}
			case model.FamilyGoModule:
				// The repository updates a tool pin with its own native tool.
				version := target.To
				if !strings.HasPrefix(version, "v") {
					version = "v" + version
				}
				generatorRuns = append(generatorRuns, model.ConsumerRef{
					ID:     "bingo-get",
					Params: map[string]string{"tool": target.ToolName, "version": version},
				})
				generated["bingo-get"] = ".bingo/Variables.mk"
			default:
				_ = candidate.Close(ctx, checkout)
				return model.Candidate{}, nil, fmt.Errorf("unsupported family kind %q", family.Kind)
			}
		}
	}
	for _, reference := range generatorRuns {
		result := e.deps.Runner.Run(ctx, candidate.Path(), reference, logDir)
		if result.Status != model.ConsumerPassed {
			_ = candidate.Close(ctx, checkout)
			return model.Candidate{}, nil, fmt.Errorf("%w: generator %s: %s %s",
				ErrGeneration, reference.ID, result.Status, result.Reason)
		}
		identities[reference.ID] = true
	}
	// A second generation pass must be a no-op; a moving tree means the native
	// tooling does not converge and the candidate is not trustworthy.
	if len(generatorRuns) > 0 {
		before, err := candidate.Root.TreeDigest(ctx, plan.BaseSHA)
		if err != nil {
			_ = candidate.Close(ctx, checkout)
			return model.Candidate{}, nil, err
		}
		repeat := e.deps.Runner.Run(ctx, candidate.Path(), generatorRuns[0], logDir)
		if repeat.Status != model.ConsumerPassed {
			_ = candidate.Close(ctx, checkout)
			return model.Candidate{}, nil, fmt.Errorf("%w: generator %s did not converge: %s %s",
				ErrGeneration, repeat.ID, repeat.Status, repeat.Reason)
		}
		after, err := candidate.Root.TreeDigest(ctx, plan.BaseSHA)
		if err != nil {
			_ = candidate.Close(ctx, checkout)
			return model.Candidate{}, nil, err
		}
		if before != after {
			_ = candidate.Close(ctx, checkout)
			return model.Candidate{}, nil, fmt.Errorf(
				"%w: repeated generation changed the tree; nonconvergent native tooling", ErrGeneration)
		}
	}
	changed, err := candidate.Root.ChangedPaths(ctx, plan.BaseSHA)
	if err != nil {
		_ = candidate.Close(ctx, checkout)
		return model.Candidate{}, nil, err
	}
	if err := e.checkScope(changed, plan); err != nil {
		_ = candidate.Close(ctx, checkout)
		return model.Candidate{}, nil, err
	}
	patch, err := candidate.Root.Diff(ctx, plan.BaseSHA)
	if err != nil {
		_ = candidate.Close(ctx, checkout)
		return model.Candidate{}, nil, err
	}
	digests, err := candidate.Root.MaterializedDigest(changed)
	if err != nil {
		_ = candidate.Close(ctx, checkout)
		return model.Candidate{}, nil, err
	}
	treeDigest, err := candidate.Root.TreeDigest(ctx, plan.BaseSHA)
	if err != nil {
		_ = candidate.Close(ctx, checkout)
		return model.Candidate{}, nil, err
	}
	record.ChangedPaths = changed
	record.FileDigests = digests
	record.TreeDigest = treeDigest
	record.DiffDigest = digestOf(patch)
	for _, path := range sortedKeys(generatedPaths(generated, changed)) {
		record.Generated = append(record.Generated, model.GeneratedPath{
			Path: path, Producer: generatedPaths(generated, changed)[path],
		})
	}
	for _, id := range sortedKeys(identities) {
		record.Tools = append(record.Tools, model.ToolIdentity{ID: id, Source: "github.com/Azure/ARO-HCP"})
	}
	candidateID, err := schema.DigestsWithPrefix("depctl/candidate/v1",
		plan.PlanID, plan.BaseSHA, treeDigest, record.DiffDigest, changed)
	if err != nil {
		_ = candidate.Close(ctx, checkout)
		return model.Candidate{}, nil, err
	}
	record.CandidateID = candidateID
	return record, candidate, nil
}

// ErrStale reports a base, source or head that moved.
var ErrStale = errors.New("stale input")

// ErrGeneration reports a failed native generation step.
var ErrGeneration = errors.New("candidate generation failed")

// Returns the sha256 digest of raw bytes.
func digestOf(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Attributes changed generated paths to their producer.
func generatedPaths(generated map[string]string, changed []string) map[string]string {
	paths := map[string]string{}
	for _, path := range changed {
		for id, generatedPath := range generated {
			if path == generatedPath {
				paths[path] = id
			}
		}
	}
	return paths
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// Refuses a source tree whose tracked edits overlap a plan
// target. Unrelated local edits are ignored but never used as evidence.
// Refuses a source tree whose tracked edits overlap a plan
// target. Unrelated local edits are ignored but never used as evidence.
func (e *Engine) ensureUnmodified(checkout *repository.Repo, plan model.Plan) error {
	dirty, err := checkout.DirtyPaths(context.Background())
	if err != nil {
		return err
	}
	targets := map[string]bool{}
	for _, family := range plan.Families {
		for _, target := range family.Targets {
			targets[target.Source] = true
		}
	}
	for _, path := range dirty {
		if targets[path] {
			return fmt.Errorf("%w: source checkout has uncommitted changes to %s", ErrStale, path)
		}
	}
	return nil
}

// Refuses a candidate that edits outside the permitted classes.
func (e *Engine) checkScope(changed []string, plan model.Plan) error {
	permitted := map[string]bool{}
	for _, family := range plan.Families {
		for _, target := range family.Targets {
			permitted[target.Source] = true
		}
		if family.Kind == model.FamilyGoModule {
			permitted[".bingo/Variables.mk"] = true
			permitted[".bingo/variables.env"] = true
			for _, target := range family.Targets {
				permitted[target.Source] = true
			}
		}
	}
	for _, path := range changed {
		if strings.HasPrefix(path, ".bingo/") && (strings.HasSuffix(path, ".mod") || strings.HasSuffix(path, ".sum")) {
			continue
		}
		if !permitted[path] {
			return fmt.Errorf("%w: candidate modified %s, which the plan does not cover", ErrGeneration, path)
		}
	}
	return nil
}

// Verify runs every required consumer on the baseline and the candidate.
func (e *Engine) Verify(ctx context.Context, checkout *repository.Repo, plan model.Plan, record model.Candidate, candidate *workspace.Candidate, logDir string) (model.Validation, error) {
	baseline, err := workspace.Prepare(ctx, checkout, filepath.Join(logDir, "baseline"), plan.BaseSHA)
	if err != nil {
		return model.Validation{}, err
	}
	defer func() { _ = baseline.Close(ctx, checkout) }()
	var results []model.ConsumerResult
	for _, required := range plan.Consumers {
		baselineResult := e.deps.Runner.Run(ctx, baseline.Path(), required, logDir)
		baselineResult.Phase = validation.PhaseBaseline
		results = append(results, baselineResult)
		candidateResult := e.deps.Runner.Run(ctx, candidate.Path(), required, logDir)
		candidateResult.Phase = validation.PhaseCandidate
		results = append(results, candidateResult)
	}
	return validation.Compare(plan, record, results), nil
}

// BuildProposal packages a validated candidate, conferring no authorization.
func (e *Engine) BuildProposal(ctx context.Context, checkout *repository.Repo, plan model.Plan, candidate *workspace.Candidate, record model.Candidate, verdict model.Validation, artifactDir string) (model.Proposal, error) {
	if verdict.Verdict != model.VerdictValidated {
		return model.Proposal{}, fmt.Errorf("candidate validation verdict %q does not permit a proposal", verdict.Verdict)
	}
	patch, err := candidate.Root.Diff(ctx, plan.BaseSHA)
	if err != nil {
		return model.Proposal{}, err
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
		Expected:            model.StateExpectation{BranchExists: false, BaseSHA: plan.BaseSHA},
		RiskNotes: []string{
			"publication must re-read the base revision, ownership and single-flight slot before writing",
		},
	}
	proposal.Body = proposalBody(plan, family, record)
	proposalID, err := schema.DigestsWithPrefix("depctl/proposal/v1",
		plan.PlanID, record.CandidateID, record.DiffDigest, proposal.GroupID, proposal.Branch, proposal.Base)
	if err != nil {
		return model.Proposal{}, err
	}
	proposal.ProposalID = proposalID
	return proposal, nil
}

// Renders the proposal body from the plan and candidate.
func proposalBody(plan model.Plan, family model.PlannedFamily, candidate model.Candidate) string {
	var builder strings.Builder
	builder.WriteString(policy.ToolMarkers(family.Family)[0])
	builder.WriteString("\n\n")
	fmt.Fprintf(&builder, "Update `%s` from `%s` to `%s`.\n\n", family.Family, family.From, family.To)
	builder.WriteString("Pins updated by native tooling:\n\n")
	for _, target := range family.Targets {
		if target.Variable != "" {
			fmt.Fprintf(&builder, "- `%s` %s: `%s` -> `%s`\n", target.Source, target.Variable, target.From, target.To)
			continue
		}
		fmt.Fprintf(&builder, "- `%s` (`%s`): `%s` -> `%s`\n", target.Source, target.ToolName, target.From, target.To)
	}
	builder.WriteString("\nBaseline and candidate consumers ran in an isolated worktree:\n\n")
	for _, consumer := range plan.Consumers {
		fmt.Fprintf(&builder, "- `%s`\n", consumer.ID)
	}
	builder.WriteString("\nCandidate base: `")
	builder.WriteString(plan.BaseSHA)
	builder.WriteString("`\n")
	return builder.String()
}

// normalizer lets a record stabilize its own JSON shape.
type normalizer interface{ Normalize() }

// NormalizeRecord normalizes both pointer and value records so that a record
// serialized by a caller still emits its collections as empty arrays.
// Normalizes both pointer and value records so that a record
// serialized by a caller still emits its collections as empty arrays.
func NormalizeRecord(record any) any {
	if value, ok := record.(normalizer); ok {
		value.Normalize()
		return record
	}
	pointer := reflect.New(reflect.TypeOf(record))
	pointer.Elem().Set(reflect.ValueOf(record))
	if value, ok := pointer.Interface().(normalizer); ok {
		value.Normalize()
		return pointer.Interface()
	}
	return record
}

// WriteRecord writes a versioned record atomically.
func WriteRecord(path string, record any) error {
	if path == "" {
		return nil
	}
	record = NormalizeRecord(record)
	encoded, err := schema.Canonical(record)
	if err != nil {
		return err
	}
	pretty := make([]byte, 0, len(encoded)+1)
	pretty = append(pretty, encoded...)
	pretty = append(pretty, '\n')
	if directory := filepath.Dir(path); directory != "." {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return fmt.Errorf("create output directory: %w", err)
		}
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".record-")
	if err != nil {
		return fmt.Errorf("create output file: %w", err)
	}
	defer func() { _ = os.Remove(temporary.Name()) }()
	if _, err := temporary.Write(pretty); err != nil {
		temporary.Close()
		return fmt.Errorf("write output file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close output file: %w", err)
	}
	return os.Rename(temporary.Name(), path)
}
