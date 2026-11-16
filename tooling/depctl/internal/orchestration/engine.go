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
	// ToolPaths selects the exact executable per validation phase, keyed by
	// program name. Without an entry, the consumer uses PATH and its preflight
	// requires that executable to report the planned version.
	ToolPaths ToolPaths
}

// ToolPaths selects phase-specific executables for version-pinned consumers.
type ToolPaths struct {
	Baseline  map[string]string
	Candidate map[string]string
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
		Skipped:       selection.Skipped,
		Blockers:      selection.Blockers,
	}
	switch {
	case len(plan.Blockers) > 0:
		// An incomplete inventory is never a successful no-op, even when no
		// family was selected.
		plan.Outcome = model.OutcomeBlocked
	case len(selection.Families) == 0:
		plan.Outcome = model.OutcomeNoChange
	default:
		plan.Outcome = model.OutcomePrepared
		plan.Consumers = policy.RequiredConsumers(selection.Families)
	}
	var err error
	plan.PolicyDigest, err = e.policyDigest()
	if err != nil {
		return model.Plan{}, err
	}
	digest, err := schema.DigestsWithPrefix("depctl/plan/v1", planIdentity(plan))
	if err != nil {
		return model.Plan{}, err
	}
	plan.PlanID = digest
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
	var skipped []string
	for _, family := range plan.Skipped {
		skipped = append(skipped, family.Family+" "+family.Status+" "+family.Reason)
	}
	sort.Strings(skipped)
	return map[string]any{
		"repository":   plan.Repository,
		"baseSHA":      plan.BaseSHA,
		"profile":      plan.Profile,
		"policyDigest": plan.PolicyDigest,
		"targets":      targets,
		"skipped":      skipped,
		"consumers":    consumers,
		"outcome":      plan.Outcome,
	}
}

// Digests the compiled policy that shaped a plan.
func (e *Engine) policyDigest() (string, error) {
	limits := policy.DefaultLimits()
	ids := e.deps.Registry.IDs()
	// The digest covers the reviewed executable of each consumer, not only its
	// ID, so a plan is bound to the commands it was evaluated against.
	specs := make([]string, 0, len(ids))
	for _, id := range ids {
		spec, err := e.deps.Registry.Get(id)
		if err != nil {
			return "", err
		}
		specs = append(specs, strings.Join([]string{
			id, spec.Program, strings.Join(spec.Arguments, " "), spec.Directory,
			strings.Join(spec.Environment, " "),
		}, "|"))
	}
	return schema.Digest(map[string]any{
		"limits":        limits,
		"toolOwner":     policy.ToolOwner,
		"toolLabel":     policy.ToolLabel,
		"toolBase":      policy.ToolBase,
		"holdLabel":     policy.HoldLabel,
		"required":      []string{"ci/pipeline-gate", "ci/prow/e2e-parallel"},
		"consumers":     ids,
		"consumerSpecs": specs,
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
		Operations: []Operation{},
		Generators: []string{},
		Note:       "dry run declares planned operations only; no install, generation, candidate, commit or remote write occurred",
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
//
// The plan is revalidated against compiled policy and repository pins, every
// edit site is confined inside the candidate, and the resulting tree is
// committed so a publisher can bind one immutable revision.
func (e *Engine) ApplyWithCheckout(ctx context.Context, checkout *repository.Repo, plan model.Plan, candidateDir, logDir string) (model.Candidate, *workspace.Candidate, error) {
	head, err := checkout.Head(ctx)
	if err != nil {
		return model.Candidate{}, nil, err
	}
	if head != plan.BaseSHA {
		return model.Candidate{}, nil, fmt.Errorf("%w: base moved from %s to %s", ErrStale, plan.BaseSHA, head)
	}
	if err := e.ValidatePlan(ctx, checkout.Path, plan); err != nil {
		return model.Candidate{}, nil, err
	}
	if err := e.ensureUnmodified(checkout, plan); err != nil {
		return model.Candidate{}, nil, err
	}
	candidate, err := workspace.Prepare(ctx, checkout, candidateDir, plan.BaseSHA)
	if err != nil {
		return model.Candidate{}, nil, err
	}
	fail := func(err error) (model.Candidate, *workspace.Candidate, error) {
		_ = candidate.Close(ctx, checkout)
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
			location, err := workspace.ConfinePath(candidate.Path(), target.Source)
			if err != nil {
				return fail(err)
			}
			switch target.Kind {
			case model.EditMakeVariable:
				if err := workspace.SetVersionVariable(location, target.Variable, target.To); err != nil {
					return fail(err)
				}
			case model.EditDockerfileArg:
				if err := workspace.SetDockerfileArg(location, target.Variable, target.To); err != nil {
					return fail(err)
				}
			case model.EditBingoModule:
				// The repository updates a tool pin with its own native tool.
				version := target.To
				if !strings.HasPrefix(version, "v") {
					version = "v" + version
				}
				generatorRuns = append(generatorRuns, model.ConsumerRef{
					ID:     "bingo-get",
					Params: map[string]string{"tool": target.ToolName, "version": version},
				})
				generated["bingo-get"] = workspace.BingoHelperPath
			default:
				return fail(fmt.Errorf("%w: unsupported edit kind %q", ErrPlanRejected, target.Kind))
			}
		}
	}
	for _, reference := range generatorRuns {
		result := e.deps.Runner.Run(ctx, candidate.Path(), reference, logDir)
		if result.Status != model.ConsumerPassed {
			return fail(fmt.Errorf("%w: generator %s: %s %s", ErrGeneration, reference.ID, result.Status, result.Reason))
		}
		identities[reference.ID] = true
	}
	// Every generator must converge. A second complete pass may not change the
	// materialized tree, so the digest has to describe the worktree, not the base.
	if len(generatorRuns) > 0 {
		before, err := candidate.Root.MaterializedTreeDigest(ctx, plan.BaseSHA)
		if err != nil {
			return fail(err)
		}
		for _, reference := range generatorRuns {
			repeat := e.deps.Runner.Run(ctx, candidate.Path(), reference, logDir)
			if repeat.Status != model.ConsumerPassed {
				return fail(fmt.Errorf("%w: generator %s did not converge: %s %s", ErrGeneration, repeat.ID, repeat.Status, repeat.Reason))
			}
		}
		after, err := candidate.Root.MaterializedTreeDigest(ctx, plan.BaseSHA)
		if err != nil {
			return fail(err)
		}
		if before != after {
			return fail(fmt.Errorf("%w: repeated generation changed the tree; nonconvergent native tooling", ErrGeneration))
		}
	}
	if err := checkScopeAfterGeneration(ctx, candidate, plan); err != nil {
		return fail(err)
	}
	// The candidate is committed before its record is derived: a new generated
	// file changes representation once staged, so digests taken before the
	// commit would not describe the committed revision.
	commit, err := candidate.Root.CommitAll(ctx, "depctl candidate "+plan.PlanID)
	if err != nil {
		return fail(err)
	}
	changed, err := candidate.Root.ChangedPaths(ctx, plan.BaseSHA)
	if err != nil {
		return fail(err)
	}
	if err := checkScope(changed, plan); err != nil {
		return fail(err)
	}
	patch, err := candidate.Root.Diff(ctx, plan.BaseSHA)
	if err != nil {
		return fail(err)
	}
	digests, err := candidate.Root.MaterializedDigest(changed)
	if err != nil {
		return fail(err)
	}
	treeDigest, err := candidate.Root.MaterializedTreeDigest(ctx, plan.BaseSHA)
	if err != nil {
		return fail(err)
	}
	attributed := generatedPaths(generated, changed)
	for _, changedPath := range sortedKeys(attributed) {
		record.Generated = append(record.Generated, model.GeneratedPath{Path: changedPath, Producer: attributed[changedPath]})
	}
	record.ChangedPaths = changed
	record.FileDigests = digests
	record.TreeDigest = treeDigest
	record.DiffDigest = digestOf(patch)
	record.CommitSHA = commit
	for _, id := range sortedKeys(identities) {
		record.Tools = append(record.Tools, model.ToolIdentity{ID: id, Source: "github.com/Azure/ARO-HCP"})
	}
	candidateID, err := schema.DigestsWithPrefix("depctl/candidate/v1",
		plan.PlanID, plan.BaseSHA, treeDigest, record.DiffDigest, changed, commit)
	if err != nil {
		return fail(err)
	}
	record.CandidateID = candidateID
	return record, candidate, nil
}

// ErrStale reports a base, source or head that moved.
var ErrStale = errors.New("stale input")

// ErrGeneration reports a failed native generation step.
var ErrGeneration = errors.New("candidate generation failed")

// ErrCandidateDrift reports a candidate whose bytes no longer match its record.
var ErrCandidateDrift = errors.New("candidate content does not match its record")

// Refuses out-of-scope generated files before commit.
func checkScopeAfterGeneration(ctx context.Context, candidate *workspace.Candidate, plan model.Plan) error {
	changed, err := candidate.Root.ChangedPaths(ctx, plan.BaseSHA)
	if err != nil {
		return err
	}
	untracked, err := candidate.Root.UntrackedPaths(ctx)
	if err != nil {
		return err
	}
	return checkScope(append(changed, untracked...), plan)
}

// Recomputes the materialized state of a candidate worktree.
func candidateDigest(ctx context.Context, candidate *workspace.Candidate, base string) (string, string, []string, error) {
	changed, err := candidate.Root.ChangedPaths(ctx, base)
	if err != nil {
		return "", "", nil, err
	}
	patch, err := candidate.Root.Diff(ctx, base)
	if err != nil {
		return "", "", nil, err
	}
	treeDigest, err := candidate.Root.MaterializedTreeDigest(ctx, base)
	if err != nil {
		return "", "", nil, err
	}
	return treeDigest, digestOf(patch), changed, nil
}

// VerifyCandidateBinding refuses a worktree that drifted from its record.
func VerifyCandidateBinding(ctx context.Context, candidate *workspace.Candidate, record model.Candidate) error {
	head, err := candidate.Root.Head(ctx)
	if err != nil {
		return err
	}
	if !repository.IsCommit(record.CommitSHA) || head != record.CommitSHA {
		return fmt.Errorf("%w: worktree head %s is not the recorded commit %s", ErrCandidateDrift, head, record.CommitSHA)
	}
	// A candidate must carry its own commit: a decoded record that reuses the
	// base revision would publish an empty branch for an uncommitted change.
	if record.CommitSHA == record.BaseSHA {
		return fmt.Errorf("%w: candidate commit %s is the base revision", ErrCandidateDrift, record.CommitSHA)
	}
	dirty, err := candidate.Root.DirtyPaths(ctx)
	if err != nil {
		return fmt.Errorf("read candidate worktree state: %w", err)
	}
	if len(dirty) > 0 {
		return fmt.Errorf("%w: candidate worktree has uncommitted changes to %s",
			ErrCandidateDrift, strings.Join(dirty, ", "))
	}
	treeDigest, diffDigest, changed, err := candidateDigest(ctx, candidate, record.BaseSHA)
	if err != nil {
		return err
	}
	if treeDigest != record.TreeDigest || diffDigest != record.DiffDigest {
		return fmt.Errorf("%w: digests changed (tree %s, diff %s)", ErrCandidateDrift, treeDigest, diffDigest)
	}
	if strings.Join(changed, ",") != strings.Join(record.ChangedPaths, ",") {
		return fmt.Errorf("%w: changed paths differ from the record", ErrCandidateDrift)
	}
	identity, err := schema.DigestsWithPrefix("depctl/candidate/v1",
		record.PlanID, record.BaseSHA, record.TreeDigest, record.DiffDigest, record.ChangedPaths, record.CommitSHA)
	if err != nil {
		return err
	}
	if identity != record.CandidateID {
		return fmt.Errorf("%w: candidate identity %q does not match %q", ErrCandidateDrift, identity, record.CandidateID)
	}
	return nil
}

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

// ensureSourceCurrent reuses apply's source guards for verification and
// packaging: the base revision must still be HEAD and planned files clean.
func (e *Engine) ensureSourceCurrent(ctx context.Context, checkout *repository.Repo, plan model.Plan) error {
	head, err := checkout.Head(ctx)
	if err != nil {
		return err
	}
	if head != plan.BaseSHA {
		return fmt.Errorf("%w: base moved from %s to %s", ErrStale, plan.BaseSHA, head)
	}
	return e.ensureUnmodified(checkout, plan)
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
	pretty := append(encoded, '\n')
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
