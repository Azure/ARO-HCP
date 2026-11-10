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
	"regexp"
	"sort"
	"strings"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/discovery"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/model"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/policy"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/schema"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/semver"
)

// ErrPlanRejected reports a plan that does not match compiled policy or the
// repository's own pin layout.
var ErrPlanRejected = errors.New("plan does not match trusted policy")

var (
	bingoModulePattern = regexp.MustCompile(`^\.bingo/([a-z0-9][a-z0-9._-]*)\.mod$`)
	plainValuePattern  = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._+-]*$`)
)

// ValidatePlan rederives a plan's edits, consumers and identity from compiled
// policy and the repository pins before any candidate work happens.
//
// A record is untrusted input: its sources, variables, consumer list and digest
// are rechecked, never taken as authority.
func (e *Engine) ValidatePlan(ctx context.Context, root string, plan model.Plan) error {
	if plan.Kind != model.KindPlan {
		return fmt.Errorf("%w: unexpected record kind %q", ErrPlanRejected, plan.Kind)
	}
	if plan.Outcome != model.OutcomePrepared {
		return fmt.Errorf("%w: outcome %q cannot be prepared", ErrPlanRejected, plan.Outcome)
	}
	if len(plan.Families) == 0 {
		return fmt.Errorf("%w: the plan selects no family", ErrPlanRejected)
	}
	if len(plan.Families) != 1 {
		// One preparation produces one proposal, and a proposal covers one
		// family. The attempt budget is separate policy for later runs.
		return fmt.Errorf("%w: a preparation prepares exactly one family, got %d",
			ErrPlanRejected, len(plan.Families))
	}
	ciVariables, ciErr := discovery.CIToolVariables(ctx, root)
	for _, family := range plan.Families {
		if err := e.validateFamily(ctx, root, family, ciVariables, ciErr); err != nil {
			return err
		}
	}
	required := consumerIDs(policy.RequiredConsumers(plan.Families))
	declared := consumerIDs(plan.Consumers)
	if strings.Join(required, ",") != strings.Join(declared, ",") {
		return fmt.Errorf("%w: plan consumers %v do not match the required consumers %v",
			ErrPlanRejected, declared, required)
	}
	for _, consumer := range plan.Consumers {
		if len(consumer.Params) > 0 {
			return fmt.Errorf("%w: consumer %s carries plan-time parameters", ErrPlanRejected, consumer.ID)
		}
	}
	expectedPolicy, err := e.policyDigest()
	if err != nil {
		return err
	}
	if plan.PolicyDigest != expectedPolicy {
		return fmt.Errorf("%w: policy digest %q does not match %q", ErrPlanRejected, plan.PolicyDigest, expectedPolicy)
	}
	identity, err := schema.DigestsWithPrefix("depctl/plan/v1", planIdentity(plan))
	if err != nil {
		return err
	}
	if plan.PlanID != identity {
		return fmt.Errorf("%w: plan identity %q does not match the rederived identity %q",
			ErrPlanRejected, plan.PlanID, identity)
	}
	return nil
}

// Validates one planned family against its trusted source.
func (e *Engine) validateFamily(ctx context.Context, root string, family model.PlannedFamily, ciVariables map[string]string, ciErr error) error {
	if err := semver.Validate(family.To); err != nil {
		return fmt.Errorf("%w: family %s: %v", ErrPlanRejected, family.Family, err)
	}
	if err := validValue(family.To); err != nil {
		return fmt.Errorf("%w: family %s: %v", ErrPlanRejected, family.Family, err)
	}
	if len(family.Targets) == 0 {
		return fmt.Errorf("%w: family %s has no target", ErrPlanRejected, family.Family)
	}
	switch family.Kind {
	case model.FamilyGitHubRelease:
		if ciErr != nil {
			return fmt.Errorf("%w: CI pin metadata is unavailable: %v", ErrPlanRejected, ciErr)
		}
		return validateReleaseTargets(ctx, root, ciVariables, family)
	case model.FamilyGoModule:
		return validateModuleTargets(ctx, root, family)
	default:
		return fmt.Errorf("%w: unsupported family kind %q", ErrPlanRejected, family.Kind)
	}
}

// Requires the paired versions.mk assignment and
// Dockerfile argument for a discovered CI variable, and binds the family to the
// repository that variable tracks.
func validateReleaseTargets(ctx context.Context, root string, ciVariables map[string]string, family model.PlannedFamily) error {
	if !plainValuePattern.MatchString(family.To) || strings.HasPrefix(family.To, "v") {
		return fmt.Errorf("%w: CI release target %q must be an unprefixed version", ErrPlanRejected, family.To)
	}
	variables := map[string]bool{}
	seenMake, seenArg := false, false
	for _, target := range family.Targets {
		variables[target.Variable] = true
		if target.To != family.To {
			return fmt.Errorf("%w: target %s/%s does not carry the family target", ErrPlanRejected, target.Source, target.Variable)
		}
		repository, known := ciVariables[target.Variable]
		if !known {
			return fmt.Errorf("%w: variable %q is not a discovered CI tool pin", ErrPlanRejected, target.Variable)
		}
		if repository != family.Family {
			return fmt.Errorf("%w: variable %s tracks %s, not %s", ErrPlanRejected, target.Variable, repository, family.Family)
		}
		if _, supported := policy.ReleaseConsumer(family.Family); !supported {
			return fmt.Errorf("%w: no compiler consumer is registered for %s", ErrPlanRejected, family.Family)
		}
		switch {
		case target.Kind == model.EditMakeVariable && target.Source == policy.CIVersionsFile:
			seenMake = true
		case target.Kind == model.EditDockerfileArg && target.Source == policy.CIImageDockerfile:
			seenArg = true
		default:
			return fmt.Errorf("%w: unsupported release edit %s (%s)", ErrPlanRejected, target.Source, target.Kind)
		}
	}
	if !seenMake || !seenArg {
		return fmt.Errorf("%w: family %s must edit both %s and %s", ErrPlanRejected, family.Family, policy.CIVersionsFile, policy.CIImageDockerfile)
	}
	if len(variables) != 1 {
		return fmt.Errorf("%w: family %s must edit one CI variable, got %d", ErrPlanRejected, family.Family, len(variables))
	}
	if family.From == "" {
		return fmt.Errorf("%w: family %s records no baseline", ErrPlanRejected, family.Family)
	}
	for variable := range variables {
		pinned, err := discovery.CIPin(ctx, root, variable)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrPlanRejected, err)
		}
		if family.From != "" && strings.TrimPrefix(family.From, "v") != strings.TrimPrefix(pinned, "v") {
			return fmt.Errorf("%w: %s pins %s, not the recorded %s", ErrPlanRejected, variable, pinned, family.From)
		}
		argument, err := discovery.CIArg(ctx, root, variable)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrPlanRejected, err)
		}
		if strings.TrimPrefix(argument, "v") != strings.TrimPrefix(pinned, "v") {
			return fmt.Errorf("%w: Dockerfile ARG %s is %s but %s pins %s",
				ErrPlanRejected, variable, argument, policy.CIVersionsFile, pinned)
		}
		compared, err := semver.CompareStrings(family.To, pinned)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrPlanRejected, err)
		}
		if compared < 0 {
			return fmt.Errorf("%w: %s would downgrade %s to %s", ErrPlanRejected, variable, pinned, family.To)
		}
	}
	return nil
}

// Requires each .bingo/<tool>.mod edit to pin the family
// module the plan claims.
func validateModuleTargets(ctx context.Context, root string, family model.PlannedFamily) error {
	for _, target := range family.Targets {
		matches := bingoModulePattern.FindStringSubmatch(target.Source)
		if matches == nil || matches[1] != target.ToolName {
			return fmt.Errorf("%w: %q is not the tool module for %q", ErrPlanRejected, target.Source, target.ToolName)
		}
		if target.Kind != model.EditBingoModule {
			return fmt.Errorf("%w: module target kind %q is unsupported", ErrPlanRejected, target.Kind)
		}
		if target.To != family.To {
			return fmt.Errorf("%w: target %s does not carry the family target", ErrPlanRejected, target.Source)
		}
		modulePath, pinned, err := discovery.BingoPin(ctx, root, target.ToolName)
		if err != nil {
			return fmt.Errorf("%w: %s: %v", ErrPlanRejected, target.Source, err)
		}
		if modulePath != family.Family {
			return fmt.Errorf("%w: %s pins %s, not %s", ErrPlanRejected, target.Source, modulePath, family.Family)
		}
		if target.From != "" && strings.TrimPrefix(target.From, "v") != strings.TrimPrefix(pinned, "v") {
			return fmt.Errorf("%w: %s pins %s, not the recorded %s", ErrPlanRejected, target.Source, pinned, target.From)
		}
		compared, err := semver.CompareStrings(family.To, pinned)
		if err != nil {
			return fmt.Errorf("%w: %s: %v", ErrPlanRejected, target.Source, err)
		}
		if compared < 0 {
			return fmt.Errorf("%w: %s would downgrade %s to %s", ErrPlanRejected, target.Source, pinned, family.To)
		}
	}
	return nil
}

// Rejects any target value that could alter generated file structure.
func validValue(value string) error {
	if !plainValuePattern.MatchString(value) {
		return fmt.Errorf("%q is not a plain version", value)
	}
	return nil
}

// Returns sorted, de-duplicated consumer identifiers.
func consumerIDs(consumers []model.ConsumerRef) []string {
	ids := make([]string, 0, len(consumers))
	seen := map[string]bool{}
	for _, consumer := range consumers {
		if seen[consumer.ID] {
			continue
		}
		seen[consumer.ID] = true
		ids = append(ids, consumer.ID)
	}
	sort.Strings(ids)
	return ids
}

// Returns the phase-specific parameters that bind a consumer to
// the exact executable the plan expects.
func consumerParams(consumerID string, family model.PlannedFamily, baseline bool) map[string]string {
	var version string
	for _, target := range family.Targets {
		switch consumerID {
		case "bicep-fixtures":
			if target.Variable == "BICEP_VERSION" {
				version = target.From
				if !baseline {
					version = target.To
				}
			}
		case "promtool-rules":
			if target.Variable == "PROMTOOL_VERSION" {
				version = target.From
				if !baseline {
					version = target.To
				}
			}
		}
	}
	if version == "" {
		return nil
	}
	return map[string]string{"expectedVersion": strings.TrimPrefix(version, "v")}
}

// Returns the planned family a consumer validates.
func familyForConsumer(consumerID string, families []model.PlannedFamily) (model.PlannedFamily, bool) {
	for _, family := range families {
		if len(consumerParams(consumerID, family, true)) > 0 {
			return family, true
		}
	}
	return model.PlannedFamily{}, false
}

// Expands the compiled consumer set into phase-ready references.
func consumerPlan(families []model.PlannedFamily) []model.ConsumerRef {
	refs := policy.RequiredConsumers(families)
	expanded := make([]model.ConsumerRef, 0, len(refs))
	for _, ref := range refs {
		if family, ok := familyForConsumer(ref.ID, families); ok {
			ref.Params = consumerParams(ref.ID, family, true)
		}
		expanded = append(expanded, ref)
	}
	return expanded
}

// Rebinds a consumer reference to one execution phase,
// including the executable selected for that phase when one is configured.
func (e *Engine) phaseConsumerRef(ref model.ConsumerRef, families []model.PlannedFamily, baseline bool) model.ConsumerRef {
	family, ok := familyForConsumer(ref.ID, families)
	if !ok {
		return ref
	}
	bound := ref
	bound.Params = consumerParams(ref.ID, family, baseline)
	program := executableProgram(ref.ID)
	if program == "" {
		return bound
	}
	selected := e.deps.ToolPaths.Candidate
	if baseline {
		selected = e.deps.ToolPaths.Baseline
	}
	if path := selected[program]; path != "" {
		if bound.Params == nil {
			bound.Params = map[string]string{}
		}
		bound.Params["executablePath"] = path
	}
	return bound
}

// Maps a consumer to the executable it version-checks.
func executableProgram(consumerID string) string {
	switch consumerID {
	case "bicep-fixtures":
		return "bicep"
	case "promtool-rules":
		return "promtool"
	default:
		return ""
	}
}
