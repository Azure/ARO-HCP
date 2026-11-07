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

package policy

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/forge"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/model"
)

// Maintenance constants preserved from the current automation.
const (
	Robot     = "aro-hcp-robot[bot]"
	MergeBot  = "openshift-merge-bot[bot]"
	ProwBot   = "openshift-ci[bot]"
	HoldLabel = "do-not-merge/hold"

	UpgradeTitle    = "chore(gh-aw): [auto] upgrade agentic workflows (AROSLSRE-2267)"
	DependencyTitle = "fix(deps): "
	DependencyLabel = "agentic-dependabot"

	// RecoveryMarker opens the durable per-head retry marker.
	RecoveryMarker = "gh-aw-recompile:recovery:"

	// MinRecoveryEvidence and MaxRecoveryEvidence bound a recovery justification.
	MinRecoveryEvidence = 40
	MaxRecoveryEvidence = 4000
)

// Owned kinds.
const (
	KindUpgrade    = "upgrade"
	KindDependency = "dependency"
	KindTool       = "tool"
)

// Release outcomes for a held proposal.
const (
	ReleaseBlocked    = "blocked"
	ReleaseNotHeld    = "not-held"
	ReleaseCancelPend = "cancel-pending"
	ReleaseEligible   = "eligible"
)

var (
	shaPattern       = regexp.MustCompile(`^[0-9a-f]{40}$`)
	retryHoldPattern = regexp.MustCompile(`/hold\s*\n\s*\nRevision ([0-9a-f]{40}) was retested ([1-9][0-9]*) times: holding\s*`)
	holdCommand      = regexp.MustCompile(`(?im)^/hold(?:\s.*)?$`)
	cancelCommand    = regexp.MustCompile(`(?im)^/(?:remove-hold|hold\s+cancel|unhold)(?:\s.*)?$`)
	upgradeBranch    = regexp.MustCompile(`^upgrade-agentic-workflows-[A-Za-z0-9_-]+$`)
	trustedURL       = regexp.MustCompile(`https://(?:prow\.ci\.openshift\.org|gcsweb-ci\.apps\.ci\.l2s4\.p1\.openshiftapps\.com|gcs\.ci\.openshift\.org|storage\.googleapis\.com|github\.com)/`)
	anyURL           = regexp.MustCompile(`https://[^\s)<>]+`)
	requiredChecks   = []string{"ci/pipeline-gate", "ci/prow/e2e-parallel"}
	failingStates    = map[string]bool{
		"failure": true, "error": true, "timed_out": true, "cancelled": true,
		"action_required": true, "startup_failure": true,
	}
	pendingStates = map[string]bool{
		"pending": true, "queued": true, "in_progress": true, "waiting": true,
	}
)

// OwnedKind classifies a published same-repository robot proposal.
//
// The author alone never confers ownership: head and base repositories, base
// ref and the profile markers all have to agree.
func OwnedKind(pull PullRequest, repository string) (string, error) {
	if pull.Author == "" || pull.HeadRepo == "" || pull.BaseRepo == "" {
		return "", fmt.Errorf("%w: pull request identity is unavailable", forge.ErrEvidenceIncomplete)
	}
	if pull.Author != Robot || pull.HeadRepo != repository || pull.BaseRepo != repository || pull.Base != "main" {
		return "", nil
	}
	labels := map[string]bool{}
	for _, label := range pull.Labels {
		labels[label] = true
	}
	switch {
	case pull.Title == UpgradeTitle && upgradeBranch.MatchString(pull.HeadRef):
		return KindUpgrade, nil
	case strings.HasPrefix(pull.Title, DependencyTitle) && labels[DependencyLabel]:
		return KindDependency, nil
	case strings.HasPrefix(pull.Title, strings.TrimSpace(ToolTitlePrefix)+" bump ") && labels[ToolLabel]:
		return KindTool, nil
	}
	return "", nil
}

// ActiveHolds replays hold and cancel commands in chronological order.
//
// A cancellation clears the author's own holds, and the two trusted bots may
// clear an evidenced merge-bot retry hold. Any other cross-author cancellation
// is ignored, so a bot cannot release a human hold.
func ActiveHolds(comments []forge.Comment, commits map[string]bool) ([]model.ActiveHold, bool, error) {
	seen := map[int]forge.Comment{}
	ordered := make([]forge.Comment, 0, len(comments))
	for _, comment := range comments {
		if comment.ID <= 0 {
			return nil, false, fmt.Errorf("%w: comment identity is missing", forge.ErrEvidenceIncomplete)
		}
		if existing, duplicate := seen[comment.ID]; duplicate {
			if existing != comment {
				return nil, false, errors.New("conflicting duplicate comment evidence")
			}
			continue
		}
		seen[comment.ID] = comment
		ordered = append(ordered, comment)
	}
	sort.SliceStable(ordered, func(left, right int) bool {
		leftTime, err := parseTime(ordered[left].CreatedAt)
		if err != nil {
			return false
		}
		rightTime, err := parseTime(ordered[right].CreatedAt)
		if err != nil {
			return true
		}
		if leftTime.Equal(rightTime) {
			return ordered[left].ID < ordered[right].ID
		}
		return leftTime.Before(rightTime)
	})
	var active []model.ActiveHold
	cancellationPending := false
	for _, comment := range ordered {
		if _, err := parseTime(comment.CreatedAt); err != nil {
			return nil, false, err
		}
		command := ""
		switch {
		case cancelCommand.MatchString(comment.Body):
			command = "cancel"
		case holdCommand.MatchString(comment.Body):
			command = "hold"
		}
		if command == "" {
			continue
		}
		if comment.Author == "" {
			return nil, false, fmt.Errorf("%w: hold command author is unavailable", forge.ErrEvidenceIncomplete)
		}
		edited, err := isEdited(comment)
		if err != nil {
			return nil, false, err
		}
		retry := retryHoldPattern.FindStringSubmatch(comment.Body)
		knownRetry := comment.Author == MergeBot && !edited && command == "hold" &&
			retry != nil && commits[retry[1]]
		if command == "cancel" && !edited {
			remaining := make([]model.ActiveHold, 0, len(active))
			for _, hold := range active {
				own := hold.Author == comment.Author
				botRetry := (comment.Author == Robot || comment.Author == MergeBot) && hold.Kind == "retry-exhaustion"
				if own || botRetry {
					cancellationPending = cancellationPending || botRetry
					continue
				}
				remaining = append(remaining, hold)
			}
			active = remaining
			continue
		}
		kind := "human"
		switch {
		case knownRetry:
			kind = "retry-exhaustion"
		case comment.Author == Robot || comment.Author == MergeBot || edited:
			kind = "unexplained"
		}
		hold := model.ActiveHold{CommentID: comment.ID, Author: comment.Author, Kind: kind}
		if knownRetry {
			hold.SHA = retry[1]
		}
		active = append(active, hold)
	}
	return active, cancellationPending && len(active) == 0, nil
}

// CIEvidence requires positive non-Tide CI and rejects failing, pending or
// unknown current-head evidence.
// Requires positive non-Tide CI and rejects failing, pending or
// unknown current-head evidence.
func CIEvidence(statuses []forge.Status, checks []forge.Check, expectedHead string) (model.CIEvidence, error) {
	latest := map[string]forge.Status{}
	for _, status := range statuses {
		if status.Context == "" {
			return model.CIEvidence{}, fmt.Errorf("%w: status context is missing", forge.ErrEvidenceIncomplete)
		}
		if status.ID <= 0 {
			return model.CIEvidence{}, fmt.Errorf("%w: status identity is missing", forge.ErrEvidenceIncomplete)
		}
		created, err := parseTime(status.CreatedAt)
		if err != nil {
			return model.CIEvidence{}, err
		}
		existing, ok := latest[status.Context]
		if !ok {
			latest[status.Context] = status
			continue
		}
		existingTime, err := parseTime(existing.CreatedAt)
		if err != nil {
			return model.CIEvidence{}, err
		}
		if created.After(existingTime) || (created.Equal(existingTime) && status.ID > existing.ID) {
			latest[status.Context] = status
		}
	}
	evidence := model.CIEvidence{}
	positive := map[string]bool{}
	for _, status := range latest {
		if strings.EqualFold(status.Context, "tide") {
			continue
		}
		if status.State == "success" {
			positive[status.Context] = true
			continue
		}
		evidence.Blocked = append(evidence.Blocked, model.BlockedCheck{
			Name: status.Context, State: status.State, URL: status.TargetURL, Pending: status.State == "pending",
		})
	}
	for _, check := range checks {
		if check.Name == "" {
			return model.CIEvidence{}, fmt.Errorf("%w: check-run name is missing", forge.ErrEvidenceIncomplete)
		}
		if check.HeadSHA != expectedHead {
			return model.CIEvidence{}, fmt.Errorf("%w: check-run %s does not match the expected head", forge.ErrEvidenceIncomplete, check.Name)
		}
		if strings.EqualFold(check.Name, "tide") {
			continue
		}
		switch {
		case check.Status != "completed":
			evidence.Blocked = append(evidence.Blocked, model.BlockedCheck{
				Name: check.Name, State: check.Status, URL: check.DetailsURL, Pending: true,
			})
		case check.Conclusion == "success":
			positive[check.Name] = true
		case check.Conclusion == "neutral" || check.Conclusion == "skipped":
			// Neutral and skipped checks are never positive evidence.
			evidence.Blocked = append(evidence.Blocked, model.BlockedCheck{
				Name: check.Name, State: check.Conclusion, URL: check.DetailsURL,
			})
		default:
			evidence.Blocked = append(evidence.Blocked, model.BlockedCheck{
				Name: check.Name, State: check.Conclusion, URL: check.DetailsURL,
			})
		}
	}
	for name := range positive {
		evidence.Positive = append(evidence.Positive, name)
	}
	sort.Strings(evidence.Positive)
	sort.SliceStable(evidence.Blocked, func(left, right int) bool {
		return evidence.Blocked[left].Name < evidence.Blocked[right].Name
	})
	return evidence, nil
}

// TrustedHoldLabel reports whether the current hold label was set by a trusted
// Prow controller, using the latest label event.
// Reports whether the current hold label was set by a trusted
// Prow controller, using the latest label event.
func TrustedHoldLabel(events []forge.HoldEvent) (bool, error) {
	if len(events) == 0 {
		return false, nil
	}
	type recorded struct {
		event forge.HoldEvent
		time  time.Time
	}
	var entries []recorded
	for _, event := range events {
		if event.ID <= 0 || (event.Event != "labeled" && event.Event != "unlabeled") || event.Actor == "" {
			return false, fmt.Errorf("%w: hold label provenance is missing", forge.ErrEvidenceIncomplete)
		}
		created, err := parseTime(event.CreatedAt)
		if err != nil {
			return false, err
		}
		entries = append(entries, recorded{event: event, time: created})
	}
	sort.SliceStable(entries, func(left, right int) bool {
		if entries[left].time.Equal(entries[right].time) {
			return entries[left].event.ID < entries[right].event.ID
		}
		return entries[left].time.Before(entries[right].time)
	})
	last := entries[len(entries)-1].event
	return last.Event == "labeled" && (last.Actor == ProwBot || last.Actor == MergeBot), nil
}

// Assess builds the read-only maintenance decision for one proposal.
func Assess(repository string, number int, expectedHead string, snapshot forge.Snapshot) (model.MaintenanceDecision, error) {
	if number <= 0 {
		return model.MaintenanceDecision{}, errors.New("pull request number must be positive")
	}
	if !shaPattern.MatchString(expectedHead) {
		return model.MaintenanceDecision{}, errors.New("expected head must be a full lowercase commit SHA")
	}
	if len(snapshot.Commits) == 0 {
		return model.MaintenanceDecision{}, fmt.Errorf("%w: commit list is empty", forge.ErrEvidenceIncomplete)
	}
	commits := map[string]bool{}
	for _, sha := range snapshot.Commits {
		if !shaPattern.MatchString(sha) {
			return model.MaintenanceDecision{}, fmt.Errorf("%w: commit revision %q is invalid", forge.ErrEvidenceIncomplete, sha)
		}
		commits[sha] = true
	}
	pull := snapshot.Pull
	if !shaPattern.MatchString(pull.HeadSHA) {
		return model.MaintenanceDecision{}, fmt.Errorf("%w: current head is missing", forge.ErrEvidenceIncomplete)
	}
	kind, err := OwnedKind(toPolicyPull(pull), repository)
	if err != nil {
		return model.MaintenanceDecision{}, err
	}
	held := false
	for _, label := range pull.Labels {
		if label == HoldLabel {
			held = true
		}
	}
	labelTrusted, err := TrustedHoldLabel(snapshot.HoldEvents)
	if err != nil {
		return model.MaintenanceDecision{}, err
	}
	holds, cancellationPending, err := ActiveHolds(snapshot.Comments, commits)
	if err != nil {
		return model.MaintenanceDecision{}, err
	}
	ci, err := CIEvidence(snapshot.Statuses, snapshot.Checks, expectedHead)
	if err != nil {
		return model.MaintenanceDecision{}, err
	}
	decision := model.MaintenanceDecision{
		Repository: repository, PullRequest: number, ExpectedHead: expectedHead,
		Head: pull.HeadSHA, Kind: kind, State: pull.State, Draft: pull.Draft, Merged: pull.Merged,
		Held: held, ActiveHolds: holds, CI: ci,
		RecoveryMarker: fmt.Sprintf("<!-- %s%s -->", RecoveryMarker, pull.HeadSHA),
	}
	var blockers []string
	if pull.Number != number {
		blockers = append(blockers, "pull request number does not match")
	}
	if kind == "" {
		blockers = append(blockers, "pull request is not an owned robot upgrade, dependency, or tool PR")
	}
	if pull.State != "open" || pull.Merged || pull.Draft {
		blockers = append(blockers, "pull request must be open, unmerged, and ready for review")
	}
	if pull.HeadSHA != expectedHead || !commits[pull.HeadSHA] {
		blockers = append(blockers, "current head does not match the expected, evidenced pull request head")
	}
	for _, hold := range holds {
		if hold.Kind != "retry-exhaustion" {
			blockers = append(blockers, "active human or unexplained hold must not be cleared automatically")
			break
		}
	}
	if held && !labelTrusted {
		blockers = append(blockers, "hold label has human, unknown, or inconsistent provenance")
	}
	if held && len(holds) == 0 && !cancellationPending {
		blockers = append(blockers, "hold label has no active evidenced retry-exhaustion command")
	}
	decision.ReleaseBlockers = append([]string{}, blockers...)
	decision.RetryBlockers = append([]string{}, blockers...)
	decision.RefreshBlockers = append([]string{}, blockers...)
	if kind != KindUpgrade {
		decision.RefreshBlockers = append(decision.RefreshBlockers, "compiler refresh requires the owned upgrade PR identity")
	}
	pending := false
	unknown := false
	for _, check := range ci.Blocked {
		if check.Pending || pendingStates[check.State] {
			pending = true
		} else if !failingStates[check.State] {
			unknown = true
		}
	}
	if pending {
		decision.RetryBlockers = append(decision.RetryBlockers, "current-head CI is pending")
	}
	if unknown {
		decision.RetryBlockers = append(decision.RetryBlockers, "current-head CI contains unknown evidence")
	}
	failures := 0
	for _, check := range ci.Blocked {
		if failingStates[check.State] && check.URL != "" {
			failures++
		}
	}
	if failures == 0 {
		decision.RetryBlockers = append(decision.RetryBlockers, "no current-head failing CI URL")
	}
	if markerConsumed(snapshot.Comments, pull.HeadSHA) {
		decision.RetryBlockers = append(decision.RetryBlockers, "single recovery budget for this head is already consumed")
	}
	var status string
	switch {
	case len(decision.ReleaseBlockers) > 0:
		status = ReleaseBlocked
	case !held:
		status = ReleaseNotHeld
	case cancellationPending:
		status = ReleaseCancelPend
	default:
		status = ReleaseEligible
	}
	if held && len(holds) > 0 {
		if len(ci.Blocked) > 0 {
			decision.ReleaseBlockers = append(decision.ReleaseBlockers, "current head has failing, pending, or unknown non-Tide CI")
		}
		if len(ci.Positive) == 0 {
			decision.ReleaseBlockers = append(decision.ReleaseBlockers, "current head has no positive non-Tide CI evidence")
		}
		positive := map[string]bool{}
		for _, name := range ci.Positive {
			positive[name] = true
		}
		for _, required := range requiredChecks {
			if !positive[required] {
				decision.ReleaseBlockers = append(decision.ReleaseBlockers, "required pipeline gate and E2E checks must succeed before automatic hold recovery")
				break
			}
		}
	}
	if len(decision.ReleaseBlockers) > 0 {
		status = ReleaseBlocked
	}
	decision.ReleaseStatus = status
	decision.CanReleaseHold = status == ReleaseEligible
	decision.CanRecoverRetry = held && len(holds) > 0 && !cancellationPending && len(decision.RetryBlockers) == 0
	decision.CanRefreshUpgrade = len(decision.RefreshBlockers) == 0
	decision.AllowedActions = AllowedActions(decision)
	decision.Action, decision.ActionReason = DecideAction(decision)
	return decision, nil
}

// AllowedActions enumerates the actions the evidence permits. An empty list is
// a truthful outcome, not a failure to find work.
// Enumerates the actions the evidence permits. An empty list is
// a truthful outcome, not a failure to find work.
func AllowedActions(decision model.MaintenanceDecision) []string {
	var actions []string
	if decision.CanReleaseHold {
		actions = append(actions, "release-hold")
	}
	if decision.CanRecoverRetry {
		actions = append(actions, "recover-retry")
	}
	if decision.CanRefreshUpgrade {
		actions = append(actions, "refresh-upgrade")
	}
	if len(actions) == 0 {
		actions = append(actions, "investigate")
	}
	return actions
}

// DecideAction selects the single read-only action for this run.
//
// Investigation is first class: a failure with no diagnosis resumes evidence
// collection instead of being reported as maintenance completion.
func DecideAction(decision model.MaintenanceDecision) (string, string) {
	if decision.State != "open" || decision.Merged {
		return "none", "pull request is not open"
	}
	if decision.Kind == "" {
		return "none", "pull request is not owned by this profile"
	}
	if decision.CanReleaseHold {
		return "release-hold", "current head has green required CI and only retry-exhaustion holds"
	}
	if decision.Held || len(decision.CI.Blocked) > 0 {
		// Diagnosis comes before any retry: budget eligibility is not a reason
		// to retest, and creating another proposal is not maintenance.
		return "investigate", "hold or failing CI requires diagnosis before any retry or release"
	}
	if decision.CanRecoverRetry {
		return "recover-retry", "one justified retest is permitted for this head"
	}
	if decision.CanRefreshUpgrade {
		return "refresh-upgrade", "compiler refresh evidence is complete"
	}
	return "wait", "no permitted action; evidence is incomplete"
}

// RecoveryEvidence validates a diagnosed recovery justification.
func RecoveryEvidence(evidence string, decision model.MaintenanceDecision) error {
	length := len(evidence)
	if length < MinRecoveryEvidence || length > MaxRecoveryEvidence {
		return fmt.Errorf("recovery requires %d-%d characters of diagnosed evidence", MinRecoveryEvidence, MaxRecoveryEvidence)
	}
	if regexp.MustCompile(`(?m)^\s*/`).MatchString(evidence) {
		return errors.New("recovery evidence cannot contain commands")
	}
	if strings.Contains(evidence, RecoveryMarker) {
		return errors.New("recovery evidence cannot contain markers")
	}
	failed := map[string]bool{}
	for _, check := range decision.CI.Blocked {
		if check.URL != "" {
			failed[trimPunctuation(check.URL)] = true
		}
	}
	foundFailure, foundTrusted := false, false
	for _, match := range anyURL.FindAllString(evidence, -1) {
		candidate := trimPunctuation(match)
		if failed[candidate] {
			foundFailure = true
			continue
		}
		if trustedURL.MatchString(candidate) {
			foundTrusted = true
		}
	}
	if !foundFailure {
		return errors.New("recovery needs the current failure URL")
	}
	if !foundTrusted {
		return errors.New("recovery needs a trusted comparison run URL")
	}
	return nil
}

// Trims trailing sentence punctuation from a URL.
func trimPunctuation(value string) string {
	return strings.TrimRight(value, ".;,")
}

// Reports whether the recovery marker already exists for a head.
func markerConsumed(comments []forge.Comment, head string) bool {
	marker := fmt.Sprintf("<!-- %s%s -->", RecoveryMarker, head)
	for _, comment := range comments {
		if comment.Author == Robot && strings.Contains(comment.Body, marker) {
			return true
		}
	}
	return false
}

// Converts a forge view into a policy view.
func toPolicyPull(pull forge.PullRequest) PullRequest {
	return PullRequest{
		Number: pull.Number, Author: pull.Author, State: pull.State, Draft: pull.Draft,
		Base: pull.BaseRef, HeadRef: pull.HeadRef, Title: pull.Title, Body: pull.Body,
		Labels: pull.Labels, HeadRepo: pull.HeadRepo, BaseRepo: pull.BaseRepo,
	}
}

// Parses an RFC3339 evidence timestamp.
func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: evidence timestamp %q is not RFC3339", forge.ErrEvidenceIncomplete, value)
	}
	return parsed.UTC(), nil
}

// Reports whether a comment was edited after creation.
func isEdited(comment forge.Comment) (bool, error) {
	created, err := parseTime(comment.CreatedAt)
	if err != nil {
		return false, err
	}
	updated, err := parseTime(comment.UpdatedAt)
	if err != nil {
		return false, err
	}
	return !created.Equal(updated), nil
}
