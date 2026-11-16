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

// Package policy holds pure decisions: it never loads credentials, executes a
// process, reads the environment or touches the network.
package policy

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/model"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/semver"
)

// Tools profile constants, preserved from the current automation.
const (
	// ToolOwner is the robot identity that owns tool update output.
	ToolOwner = "aro-hcp-robot[bot]"
	// ToolLabel marks an owned tool proposal.
	ToolLabel = "agentic-tool-upgrade"
	// ToolTitlePrefix starts every owned tool proposal title.
	ToolTitlePrefix = "chore(tools): "
	// ToolBodyMarker opens the family marker in a proposal body.
	ToolBodyMarker = "<!-- agentic-tool-family:"
	// ToolBranchPrefix starts an owned tool branch name.
	ToolBranchPrefix = "agentic-tool-upgrade-"
	// ToolBase is the only base branch a tool proposal may target.
	ToolBase = "main"
	// MaxFamilyAttempts bounds eligible family attempts in one run.
	MaxFamilyAttempts = 2
	// MaxProposalsPerRun bounds proposals created by one run.
	MaxProposalsPerRun = 1
	// CIVersionsFile holds the openshift-ci tool version assignments.
	CIVersionsFile = "dev-infrastructure/openshift-ci/versions.mk"
	// CIImageDockerfile holds the matching image build arguments.
	CIImageDockerfile = "dev-infrastructure/openshift-ci/Dockerfile"
	// BingoDirectory holds the tool module files.
	BingoDirectory = ".bingo"
)

// Limits are the throughput settings of one profile. They are policy, not
// architecture, and only change through a separate reviewed decision.
type Limits struct {
	MaxFamilyAttempts int `json:"maxFamilyAttempts"`
	MaxProposals      int `json:"maxProposals"`
}

// DefaultLimits returns the preserved baseline throughput limits.
func DefaultLimits() Limits {
	return Limits{MaxFamilyAttempts: MaxFamilyAttempts, MaxProposals: MaxProposalsPerRun}
}

var branchSanitizer = regexp.MustCompile(`[^a-z0-9.]+`)

// ToolBranch returns the stable branch name for a family and target version.
func ToolBranch(family, target string) string {
	name := strings.ToLower(family)
	name = strings.ReplaceAll(name, "/", "-")
	name = branchSanitizer.ReplaceAllString(name, "-")
	return ToolBranchPrefix + strings.Trim(name, "-") + "-" + strings.TrimPrefix(target, "v")
}

// ToolMarkers returns the family marker written into the proposal body.
func ToolMarkers(family string) []string {
	return []string{ToolBodyMarker + family + " -->"}
}

// ToolTitle returns the owned proposal title for a family and target.
func ToolTitle(family, target string) string {
	return ToolTitlePrefix + "bump " + family + " to " + strings.TrimPrefix(target, "v")
}

// PullRequest is the minimal forge view a policy decision needs.
type PullRequest struct {
	Number   int
	Author   string
	State    string
	Draft    bool
	Base     string
	HeadRef  string
	Title    string
	Body     string
	Labels   []string
	HeadRepo string
	BaseRepo string
}

// IsToolProposal reports whether a pull request carries this profile's markers,
// including the pre-label publication window.
// Reports whether a pull request carries this profile's markers,
// including the pre-label publication window.
func IsToolProposal(pull PullRequest) bool {
	for _, label := range pull.Labels {
		if label == ToolLabel {
			return true
		}
	}
	return strings.HasPrefix(pull.Title, ToolTitlePrefix) && strings.Contains(pull.Body, ToolBodyMarker)
}

// OpenOwnedToolProposal returns the single-flight blocker, if one exists.
//
// Only this profile's own identity, base and markers count. Unrelated bot or
// human proposals never block a tool proposal.
func OpenOwnedToolProposal(pulls []PullRequest) (int, bool) {
	for _, pull := range pulls {
		if pull.Author != ToolOwner || pull.State != "open" || pull.Base != ToolBase {
			continue
		}
		if IsToolProposal(pull) {
			return pull.Number, true
		}
	}
	return 0, false
}

// Selection is the outcome of planning a tools run.
type Selection struct {
	Families []model.PlannedFamily
	Skipped  []model.FamilySummary
	Blockers []string
}

// SelectFamilies chooses target families from an inventory and explicit targets.
//
// Explicit targets are required for every selected family: an inventory row that
// only knows a version is outdated cannot invent a target. Rows without a
// requested target are skipped with a reason so that a quiet run stays quiet.
func SelectFamilies(inventory model.Inventory, targets map[string]string) (Selection, error) {
	selection := Selection{}
	if !inventory.Complete {
		selection.Blockers = append(selection.Blockers, "inventory is incomplete; version states are not authoritative")
	}
	byFamily := make(map[string]model.Family, len(inventory.Families))
	for _, family := range inventory.Families {
		byFamily[family.Family] = family
	}
	var requested []string
	for family := range targets {
		requested = append(requested, family)
	}
	sort.Strings(requested)
	for _, name := range requested {
		family, known := byFamily[name]
		if !known {
			return Selection{}, fmt.Errorf("target family %q is not in the inventory", name)
		}
		if family.Status == model.VersionError {
			selection.Skipped = append(selection.Skipped, model.FamilySummary{
				Family: name, Status: family.Status, Reason: family.Reason,
			})
			continue
		}
		target := targets[name]
		if err := semver.Validate(target); err != nil {
			return Selection{}, fmt.Errorf("target for %s: %w", name, err)
		}
		planned, upToDate, err := planFamily(family, target)
		if err != nil {
			return Selection{}, err
		}
		if upToDate {
			selection.Skipped = append(selection.Skipped, model.FamilySummary{
				Family: name, Status: model.VersionCurrent, From: family.Current,
				Reason: "already at the requested target",
			})
			continue
		}
		selection.Families = append(selection.Families, planned)
	}
	for _, family := range inventory.Families {
		if _, wanted := targets[family.Family]; wanted {
			continue
		}
		selection.Skipped = append(selection.Skipped, model.FamilySummary{
			Family: family.Family, Status: family.Status, From: family.Current, Reason: "no target requested",
		})
	}
	sort.Slice(selection.Families, func(left, right int) bool {
		return selection.Families[left].Family < selection.Families[right].Family
	})
	// Every requested family is accounted for before a cap drops it: work that
	// silently disappears cannot be distinguished from work already done.
	if len(selection.Families) > MaxFamilyAttempts {
		for _, dropped := range selection.Families[MaxFamilyAttempts:] {
			selection.Skipped = append(selection.Skipped, model.FamilySummary{
				Family: dropped.Family, Status: model.VersionOutdated,
				From: dropped.From, To: dropped.To,
				Reason: "family attempt budget is exhausted; prepared in a later run",
			})
		}
		selection.Families = selection.Families[:MaxFamilyAttempts]
	}
	// A preparation produces one proposal, and a proposal covers one family.
	// Further eligible families are reported instead of being merged together.
	if len(selection.Families) > MaxProposalsPerRun {
		for _, deferred := range selection.Families[MaxProposalsPerRun:] {
			selection.Skipped = append(selection.Skipped, model.FamilySummary{
				Family: deferred.Family, Status: model.VersionOutdated,
				From: deferred.From, To: deferred.To,
				Reason: "one family per preparation; prepared in a later run",
			})
		}
		selection.Families = selection.Families[:MaxProposalsPerRun]
	}
	return selection, nil
}

// Builds the edit plan for one family. upToDate reports that every
// pin already carries the requested target, which is a quiet no-change.
// Builds the edit plan for one family. upToDate reports that every
// pin already carries the requested target, which is a quiet no-change.
func planFamily(family model.Family, target string) (model.PlannedFamily, bool, error) {
	canonical := target
	if family.Kind == model.FamilyGitHubRelease {
		// CI release variables and image URLs want the bare version; only Go
		// module pins carry the "v" prefix.
		canonical = strings.TrimPrefix(target, "v")
	}
	planned := model.PlannedFamily{
		Family: family.Family,
		Kind:   family.Kind,
		From:   family.Current,
		To:     canonical,
	}
	for _, pin := range family.Pins {
		if pin.Error != "" {
			return model.PlannedFamily{}, false, fmt.Errorf("family %s pin %s: %s", family.Family, pin.ToolName, pin.Error)
		}
		if pin.Current == target {
			continue
		}
		if pin.Current != "" {
			compared, err := semver.CompareStrings(canonical, pin.Current)
			if err != nil {
				return model.PlannedFamily{}, false, fmt.Errorf("family %s: %w", family.Family, err)
			}
			if compared < 0 {
				return model.PlannedFamily{}, false, fmt.Errorf(
					"family %s: refusing to downgrade %s from %s to %s", family.Family, pin.ToolName, pin.Current, canonical)
			}
			if compared == 0 {
				continue
			}
		}
		kind := model.EditBingoModule
		if family.Kind == model.FamilyGitHubRelease {
			kind = model.EditMakeVariable
		}
		planned.Targets = append(planned.Targets, model.PinTarget{
			ToolName: pin.ToolName,
			Source:   pin.Source,
			Variable: pin.Variable,
			Kind:     kind,
			From:     pin.Current,
			To:       canonical,
		})
		if family.Kind == model.FamilyGitHubRelease {
			// The CI image Dockerfile must carry the same version, so the bump
			// edits both members of the pair and not just versions.mk.
			planned.Targets = append(planned.Targets, model.PinTarget{
				ToolName: pin.ToolName,
				Source:   CIImageDockerfile,
				Variable: pin.Variable,
				Kind:     model.EditDockerfileArg,
				From:     pin.Current,
				To:       canonical,
			})
		}
	}
	if len(planned.Targets) == 0 {
		return model.PlannedFamily{}, true, nil
	}
	return planned, false, nil
}

// RequiredConsumers returns the fixed consumer plan for the planned families.
//
// The mapping is compiled policy: a plan may not add, drop or substitute a gate.
func RequiredConsumers(planned []model.PlannedFamily) []model.ConsumerRef {
	seen := map[string]bool{}
	var consumers []model.ConsumerRef
	add := func(id string) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		consumers = append(consumers, model.ConsumerRef{ID: id})
	}
	add("ci-versions-verify")
	for _, family := range planned {
		switch family.Kind {
		case model.FamilyGitHubRelease:
			if consumer, known := ReleaseConsumer(family.Family); known {
				add(consumer)
			}
		case model.FamilyGoModule:
			add("bingo-regenerate")
			for _, downstream := range downstreamConsumers(family.Family) {
				add(downstream)
			}
		}
	}
	sort.Slice(consumers, func(left, right int) bool { return consumers[left].ID < consumers[right].ID })
	return consumers
}

// releaseConsumers maps an upstream repository to the compiler consumer that
// validates it. The mapping is compiled policy: a record cannot choose it.
var releaseConsumers = map[string]string{
	"prometheus/prometheus": "promtool-rules",
	"Azure/bicep":           "bicep-fixtures",
}

// ReleaseConsumer returns the compiler consumer for an upstream repository.
func ReleaseConsumer(repository string) (string, bool) {
	consumer, known := releaseConsumers[repository]
	return consumer, known
}

// Maps a tool family to the consumers that must see the
// rebuilt tool. A family without an entry is still covered by the build that
// `bingo get` performs, and the plan records that gap in its risk notes.
func downstreamConsumers(family string) []string {
	if strings.Contains(family, "yamlfmt") {
		return []string{"yamlfmt-verify"}
	}
	return nil
}

// ConsumerGaps reports planned families with no registered downstream consumer.
func ConsumerGaps(planned []model.PlannedFamily) []string {
	var gaps []string
	for _, family := range planned {
		if family.Kind != model.FamilyGoModule || len(downstreamConsumers(family.Family)) > 0 {
			continue
		}
		gaps = append(gaps, family.Family+" has no registered downstream consumer; the bingo install is the only check")
	}
	return gaps
}
