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

package dependabot

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/google/go-github/v69/github"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/forge"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/model"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/policy"
	automation "github.com/Azure/ARO-HCP/tooling/github"
)

type nativeInventoryKey struct {
	group, target string
}

type nativeInventoryGroupRecord struct {
	Group      any                     `json:"group"`
	Ecosystem  string                  `json:"ecosystem,omitempty"`
	Family     string                  `json:"family,omitempty"`
	Target     string                  `json:"target,omitempty"`
	Sources    []map[string]any        `json:"sources"`
	Blockers   []string                `json:"blockers"`
	NextAction nativeInventoryDecision `json:"next_action"`
}

type nativeInventoryDecision struct {
	Action        string                    `json:"action"`
	Reason        string                    `json:"reason"`
	ReplacementPR int                       `json:"replacement_pr,omitempty"`
	SourcePRs     []nativePublicationSource `json:"source_prs,omitempty"`
}

type nativeInventoryReplacement struct {
	Number             int                       `json:"number"`
	HeadSHA            string                    `json:"head_sha"`
	Merged             bool                      `json:"merged"`
	Group              string                    `json:"group"`
	Sources            []nativePublicationSource `json:"sources"`
	URL                *string                   `json:"url"`
	PublicationPending bool                      `json:"publication_pending"`
	CI                 map[string]any            `json:"ci,omitempty"`
	Held               *bool                     `json:"held,omitempty"`
	BlockedHolds       *bool                     `json:"blocked_holds,omitempty"`
}

type nativeInventorySourceEvidence struct {
	number int
	key    nativeInventoryKey
	files  []*github.CommitFile
	err    error
}

// Inventory observes complete native sources, owned replacements and current-head closure requests without writes.
func (p *NativePublisher) Inventory(ctx context.Context) (map[string]any, error) {
	base, err := p.mainSHA(ctx)
	if err != nil {
		return nil, fmt.Errorf("read inventory base: %w", err)
	}
	current, err := p.nativeCreatorInventory(ctx, nativePublicationBot)
	if err != nil {
		return nil, fmt.Errorf("collect native source inventory: %w", err)
	}
	groups := make([]*nativeInventoryGroupRecord, 0)
	byKey := make(map[nativeInventoryKey]*nativeInventoryGroupRecord)
	evidence := make([]nativeInventorySourceEvidence, 0, len(current))
	for _, pull := range current {
		row, source, err := p.nativeInventorySource(ctx, pull)
		if err != nil {
			return nil, fmt.Errorf("inventory source #%d: %w", pull.GetNumber(), err)
		}
		evidence = append(evidence, source)
		if source.err != nil {
			groups = append(groups, &nativeInventoryGroupRecord{Sources: []map[string]any{row}, Blockers: []string{source.err.Error()}})
			continue
		}
		group := byKey[source.key]
		if group == nil {
			group = &nativeInventoryGroupRecord{
				Group: source.key.group, Target: source.key.target, Ecosystem: row["ecosystem"].(string),
				Family: row["family"].(string), Blockers: []string{},
			}
			byKey[source.key] = group
			groups = append(groups, group)
		}
		group.Sources = append(group.Sources, row)
		if row["blocked_holds"].(bool) {
			group.Blockers = append(group.Blockers, fmt.Sprintf("PR #%d: human/unknown hold", source.number))
		}
	}
	replacements, err := p.nativeInventoryReplacements(ctx)
	if err != nil {
		return nil, fmt.Errorf("collect native replacement inventory: %w", err)
	}
	matching := make(map[string][]*nativeInventoryReplacement)
	for i := range replacements {
		row := &replacements[i]
		matching[row.Group] = append(matching[row.Group], row)
	}
	for _, rows := range matching {
		slices.SortFunc(rows, func(a, b *nativeInventoryReplacement) int { return cmp.Compare(a.Number, b.Number) })
	}
	for _, group := range groups {
		slices.SortFunc(group.Sources, func(a, b map[string]any) int { return cmp.Compare(a["number"].(int), b["number"].(int)) })
		id, _ := group.Group.(string)
		decision := nativeInventoryNextAction(group, matching[id])
		if decision.Action == "prepare_replacement" {
			if err := nativeInventoryCheckSiblings(group, evidence); err != nil {
				group.Blockers = append(group.Blockers, err.Error())
				decision = nativeInventoryNextAction(group, matching[id])
			}
		}
		group.NextAction = decision
	}
	latest, err := p.mainSHA(ctx)
	if err != nil {
		return nil, fmt.Errorf("recheck inventory base: %w", err)
	}
	if latest != base {
		return nil, nativeReject("main changed during inventory")
	}
	return map[string]any{"repository": p.repository, "base_sha": base, "groups": groups, "replacements": replacements}, nil
}

// NativeInventorySource retains policy failures as evidence without disguising failed API requests as no work.
func (p *NativePublisher) nativeInventorySource(ctx context.Context, pull *github.PullRequest) (map[string]any, nativeInventorySourceEvidence, error) {
	number, head := pull.GetNumber(), pull.GetHead().GetSHA()
	var headValue any
	if pull.Head != nil && pull.Head.SHA != nil {
		headValue = head
	}
	row := map[string]any{"number": number, "head_sha": headValue}
	evidence := nativeInventorySourceEvidence{number: number}
	snapshot, err := p.nativePublicationSnapshot(ctx, number, head, nativePublicationBot)
	if err != nil {
		var policyErr *NativePolicyError
		if !errors.As(err, &policyErr) && !errors.Is(err, automation.ErrConflict) && !errors.Is(err, automation.ErrEvidenceIncomplete) {
			return nil, evidence, fmt.Errorf("read source snapshot: %w", err)
		}
		evidence.err = err
		return row, evidence, nil
	}
	evidence.files = snapshot.files
	intent, err := p.nativePublicationSourceIntent(ctx, snapshot)
	if err != nil {
		var policyErr *NativePolicyError
		if !errors.As(err, &policyErr) {
			return nil, evidence, fmt.Errorf("derive native source intent: %w", err)
		}
		evidence.err = err
		return row, evidence, nil
	}
	target, _ := intent["target"].(string)
	if target == "" {
		evidence.err = nativeReject("no verifiable dependency version target")
		return row, evidence, nil
	}
	ci, err := nativeInventoryCI(snapshot)
	if err != nil {
		return nil, evidence, fmt.Errorf("evaluate source CI: %w", err)
	}
	evidence.key = nativeInventoryKey{group: intent["group"].(string), target: target}
	intent["number"] = number
	intent["ci"] = ci
	intent["held"] = nativePublicationHasLabel(snapshot.pull, policy.HoldLabel)
	intent["blocked_holds"] = snapshot.blocked
	intent["closure_requests"] = nativeInventoryClosureRequests(head, snapshot.comments)
	return intent, evidence, nil
}

// NativeInventoryCI emits the same shared CI policy result and required keys as snapshot-evidence.
func nativeInventoryCI(snapshot nativePublicationSnapshot) (map[string]any, error) {
	evidence, err := nativeSnapshotCI(snapshot)
	if err != nil {
		return nil, fmt.Errorf("evaluate current-head CI evidence: %w", err)
	}
	if evidence.Positive == nil {
		evidence.Positive = []string{}
	}
	blocked := make([]map[string]any, 0, len(evidence.Blocked))
	for _, check := range evidence.Blocked {
		blocked = append(blocked, map[string]any{"name": check.Name, "state": check.State, "url": check.URL, "pending": check.Pending})
	}
	return map[string]any{"positive": evidence.Positive, "blocked": blocked}, nil
}

// NativeSnapshotCI evaluates the current head with the shared CI policy.
func nativeSnapshotCI(snapshot nativePublicationSnapshot) (model.CIEvidence, error) {
	statuses := make([]forge.Status, len(snapshot.ci.Statuses))
	for i, status := range snapshot.ci.Statuses {
		statuses[i] = forge.Status(status)
	}
	checks := make([]forge.Check, len(snapshot.ci.Checks))
	for i, check := range snapshot.ci.Checks {
		checks[i] = forge.Check(check)
	}
	return policy.CIEvidence(statuses, checks, snapshot.pull.GetHead().GetSHA())
}

// NativeInventoryReplacements retains verified merged ownership and open maintenance evidence.
func (p *NativePublisher) nativeInventoryReplacements(ctx context.Context) ([]nativeInventoryReplacement, error) {
	pulls, err := p.nativeCreatorInventory(ctx, nativePublicationRobot)
	if err != nil {
		return nil, fmt.Errorf("read robot publications: %w", err)
	}
	rows := make([]nativeInventoryReplacement, 0, len(pulls))
	for _, pull := range pulls {
		number, head := pull.GetNumber(), pull.GetHead().GetSHA()
		labeled := nativePublicationHasLabel(pull, nativePublicationLabel)
		if !labeled && !strings.Contains(pull.GetBody(), nativePublicationMarker) {
			return nil, nativeReject(fmt.Sprintf("replacement #%d lost ownership evidence during inventory", number))
		}
		if pull.GetState() == "closed" && pull.Merged != nil && !pull.GetMerged() {
			continue
		}
		merged := pull.GetMerged()
		meta, err := p.validateReplacement(pull, merged, !merged)
		if err != nil {
			return nil, fmt.Errorf("validate inventory replacement #%d: %w", number, err)
		}
		sources := meta["sources"].([]any)
		row := nativeInventoryReplacement{
			Number: number, HeadSHA: head, Merged: merged, Group: meta["group"].(string), URL: pull.HTMLURL,
			PublicationPending: !labeled, Sources: make([]nativePublicationSource, 0, len(sources)),
		}
		for _, source := range sources {
			identity := source.(map[string]any)
			number, _ := nativeInteger(identity["number"])
			row.Sources = append(row.Sources, nativePublicationSource{Number: int(number), HeadSHA: identity["head_sha"].(string)})
		}
		if !merged {
			snapshot, err := p.nativePublicationSnapshot(ctx, number, head, nativePublicationRobot)
			if err != nil {
				return nil, fmt.Errorf("inventory replacement snapshot #%d: %w", number, err)
			}
			if !sameNativePublicationSnapshotPull(pull, snapshot.pull) {
				return nil, nativeReject(fmt.Sprintf("replacement #%d changed during inventory", number))
			}
			row.CI, err = nativeInventoryCI(snapshot)
			if err != nil {
				return nil, fmt.Errorf("inventory replacement CI #%d: %w", number, err)
			}
			row.Held = github.Ptr(nativePublicationHasLabel(snapshot.pull, policy.HoldLabel))
			row.BlockedHolds = github.Ptr(snapshot.blocked)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// NativeInventoryClosureRequests accepts only exact current-head commands from the trusted robot.
func nativeInventoryClosureRequests(head string, comments []automation.Comment) []int {
	prefix := "<!-- " + nativePublicationMarker + "close:" + head + ":"
	seen := make(map[int]bool)
	requests := []int{}
	for _, comment := range comments {
		if comment.Author != nativePublicationRobot {
			continue
		}
		marker, _, newline := strings.Cut(comment.Body, "\n")
		number, ok := strings.CutPrefix(marker, prefix)
		if !newline || !ok {
			continue
		}
		number, ok = strings.CutSuffix(number, " -->")
		if !ok || !nativeNumberPattern.MatchString(number) || !strings.HasSuffix(strings.TrimRightFunc(comment.Body, unicode.IsSpace), "\n/close") {
			continue
		}
		pr, err := strconv.Atoi(number)
		if err == nil && !seen[pr] {
			seen[pr] = true
			requests = append(requests, pr)
		}
	}
	slices.Sort(requests)
	return requests
}

// NativeInventoryCheckSiblings fails closed unless omitted current sources have disjoint files or proved different intent.
func nativeInventoryCheckSiblings(group *nativeInventoryGroupRecord, evidence []nativeInventorySourceEvidence) error {
	selected := make(map[int]bool, len(group.Sources))
	for _, source := range group.Sources {
		selected[source["number"].(int)] = true
	}
	key := nativeInventoryKey{group: group.Group.(string), target: group.Target}
	for _, source := range evidence {
		if selected[source.number] || nativePublicationSiblingDisjoint(source.files, group.Ecosystem) {
			continue
		}
		if source.err != nil {
			return fmt.Errorf("PR #%d: %w", source.number, source.err)
		}
		if source.key == key {
			return nativeReject(fmt.Sprintf("PR #%d: replacement omits current same-family/target native source #%d", source.number, source.number))
		}
	}
	return nil
}

// NativeInventoryNextAction classifies verified identities without using native-source CI as authority.
func nativeInventoryNextAction(group *nativeInventoryGroupRecord, replacements []*nativeInventoryReplacement) nativeInventoryDecision {
	if len(group.Blockers) != 0 {
		return nativeInventoryDecision{Action: "blocked", Reason: strings.Join(slices.Sorted(slices.Values(group.Blockers)), "; ")}
	}
	var open *nativeInventoryReplacement
	for _, replacement := range replacements {
		if replacement.Merged {
			continue
		}
		if open != nil {
			return nativeInventoryDecision{Action: "blocked", Reason: "ambiguous open matching replacements"}
		}
		open = replacement
	}
	if open != nil && *open.BlockedHolds {
		return nativeInventoryDecision{Action: "blocked", Reason: "replacement has human/unknown hold", ReplacementPR: open.Number}
	}
	sources := make(map[nativePublicationSource][]int, len(group.Sources))
	for _, row := range group.Sources {
		sources[nativePublicationSource{Number: row["number"].(int), HeadSHA: row["head_sha"].(string)}] = row["closure_requests"].([]int)
	}
	pending := make(map[nativePublicationSource]bool)
	for _, replacement := range replacements {
		if !replacement.Merged {
			continue
		}
		for _, source := range replacement.Sources {
			if _, found := slices.BinarySearch(sources[source], replacement.Number); found {
				pending[source] = true
			}
		}
	}
	for _, replacement := range slices.Backward(replacements) {
		if !replacement.Merged {
			continue
		}
		var mapped []nativePublicationSource
		for _, source := range replacement.Sources {
			if _, current := sources[source]; current && !pending[source] {
				mapped = append(mapped, source)
			}
		}
		if len(mapped) != 0 {
			return nativeInventoryDecision{
				Action: "close_superseded", Reason: "merged replacement maps exact source heads; closure guard must verify coverage",
				ReplacementPR: replacement.Number, SourcePRs: mapped,
			}
		}
	}
	if len(pending) != 0 && len(pending) == len(sources) {
		identities := make([]nativePublicationSource, 0, len(pending))
		for source := range pending {
			identities = append(identities, source)
		}
		slices.SortFunc(identities, func(a, b nativePublicationSource) int { return cmp.Compare(a.Number, b.Number) })
		return nativeInventoryDecision{
			Action: "wait_for_closure", Reason: "trusted current-head closure commands already requested; wait for observed Prow closure",
			SourcePRs: identities,
		}
	}
	if open != nil {
		return nativeInventoryDecision{Action: "maintain_replacement", Reason: "reuse existing replacement; its current-head CI governs further progress", ReplacementPR: open.Number}
	}
	return nativeInventoryDecision{Action: "prepare_replacement", Reason: "no open matching replacement or merged exact-source mapping; native CI is evidence only"}
}
