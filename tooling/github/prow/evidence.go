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

package prow

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	github "github.com/Azure/ARO-HCP/tooling/github"
)

var (
	holdCommand   = regexp.MustCompile(`(?im)^/hold(?:[ \t].*)?$`)
	cancelCommand = regexp.MustCompile(`(?im)^/(?:remove-hold|hold[ \t]+cancel|unhold)(?:[ \t].*)?$`)
	retryHold     = regexp.MustCompile(`^/hold\s*\n\s*\nRevision ([0-9a-f]{40}) was retested ([1-9][0-9]*) times: holding\s*$`)
	commandLine   = regexp.MustCompile(`(?m)^[ \t]*/[^\r\n]*`)
	prowRun       = regexp.MustCompile(`^https://prow\.ci\.openshift\.org/view/gs/([a-z0-9][a-z0-9._-]{1,61}[a-z0-9])/((?:pr-logs/pull/[A-Za-z0-9][A-Za-z0-9_.-]*/[1-9][0-9]*|logs)/([A-Za-z0-9][A-Za-z0-9_.-]*)/([1-9][0-9]*))/?$`)
)

// Parses complete timestamps and rejects edits to command-bearing metadata.
func unedited(comment github.Comment) error {
	created, err := time.Parse(time.RFC3339, comment.CreatedAt)
	if err != nil {
		return errors.New("comment creation time is unavailable")
	}
	updated, err := time.Parse(time.RFC3339, comment.UpdatedAt)
	if err != nil || !created.Equal(updated) {
		return errors.New("edited or ambiguous command metadata")
	}
	return nil
}

// Orders unique comments before replay, refusing conflicting pagination evidence.
func orderedComments(comments []github.Comment) ([]github.Comment, error) {
	seen := map[int]github.Comment{}
	ordered := make([]github.Comment, 0, len(comments))
	for _, comment := range comments {
		if comment.ID <= 0 {
			return nil, errors.New("comment identity is missing")
		}
		if prior, ok := seen[comment.ID]; ok {
			if prior != comment {
				return nil, errors.New("conflicting duplicate comment evidence")
			}
			continue
		}
		if _, err := time.Parse(time.RFC3339, comment.CreatedAt); err != nil {
			return nil, errors.New("comment creation time is unavailable")
		}
		seen[comment.ID] = comment
		ordered = append(ordered, comment)
	}
	sort.Slice(ordered, func(i, j int) bool {
		left, _ := time.Parse(time.RFC3339, ordered[i].CreatedAt)
		right, _ := time.Parse(time.RFC3339, ordered[j].CreatedAt)
		if left.Equal(right) {
			return ordered[i].ID < ordered[j].ID
		}
		return left.Before(right)
	})
	return ordered, nil
}

// Replays hold ownership and validates the latest controller label transition.
// This is deliberately limited to provenance, not a repository policy engine.
func (c *Client) releaseEligible(snapshot github.Snapshot) (bool, error) {
	ordered, err := orderedComments(snapshot.Comments)
	if err != nil {
		return false, err
	}
	type hold struct {
		author string
		retry  bool
	}
	var active []hold
	pending := false
	for _, comment := range ordered {
		cancel := cancelCommand.MatchString(comment.Body)
		holds := holdCommand.MatchString(comment.Body)
		if !cancel && !holds {
			continue
		}
		if comment.Author == "" || unedited(comment) != nil || len(commandLine.FindAllString(comment.Body, -1)) != 1 {
			return false, errors.New("hold command metadata is edited, missing or ambiguous")
		}
		if cancel {
			remaining := active[:0]
			for _, existing := range active {
				botCancel := existing.retry && (comment.Author == c.config.Actors.Writer || contains(c.config.Actors.RetryBots, comment.Author))
				if existing.author == comment.Author || botCancel {
					pending = pending || botCancel
					continue
				}
				if existing.retry {
					return false, errors.New("untrusted cross-author cancellation may already be pending")
				}
				remaining = append(remaining, existing)
			}
			active = remaining
			continue
		}
		match := retryHold.FindStringSubmatch(comment.Body)
		known := match != nil && contains(c.config.Actors.RetryBots, comment.Author) && contains(snapshot.Commits, match[1])
		active = append(active, hold{author: comment.Author, retry: known})
	}
	if len(active) == 0 && pending {
		return true, nil
	}
	if len(active) == 0 {
		return false, errors.New("hold label has no active command provenance")
	}
	for _, existing := range active {
		if !existing.retry {
			return false, errors.New("human, unknown or unexplained hold cannot be released")
		}
	}
	var latest github.HoldEvent
	var latestTime time.Time
	seen := map[int]github.HoldEvent{}
	for _, event := range snapshot.HoldEvents {
		if prior, ok := seen[event.ID]; ok && prior != event {
			return false, errors.New("conflicting hold-label events")
		}
		seen[event.ID] = event
		created, err := time.Parse(time.RFC3339, event.CreatedAt)
		if err != nil || event.ID <= 0 || event.Actor == "" || (event.Event != "labeled" && event.Event != "unlabeled") {
			return false, errors.New("hold-label provenance is incomplete")
		}
		if latest.ID == 0 || created.After(latestTime) || (created.Equal(latestTime) && event.ID > latest.ID) {
			latest, latestTime = event, created
		}
	}
	if latest.Event != "labeled" || !contains(c.config.Actors.Controllers, latest.Actor) {
		return false, errors.New("hold label was not applied by a trusted controller")
	}
	states, err := c.ci(snapshot)
	if err != nil {
		return false, err
	}
	for _, state := range states {
		if state.state != "success" {
			return false, errors.New("hold release requires positive, complete current-head CI")
		}
	}
	return false, nil
}

// ciState retains terminal evidence and its actual current failure URL.
type ciState struct {
	state string
	url   string
}

// Selects current commit statuses without discarding history needed for baselines.
func latestStatuses(snapshot github.Snapshot) (map[string]github.Status, error) {
	latest := map[string]github.Status{}
	seenStatuses := map[int]github.Status{}
	for _, status := range snapshot.Statuses {
		created, err := time.Parse(time.RFC3339, status.CreatedAt)
		if status.ID <= 0 || status.Context == "" || err != nil {
			return nil, errors.New("incomplete commit-status evidence")
		}
		if prior, ok := seenStatuses[status.ID]; ok && prior != status {
			return nil, errors.New("conflicting commit-status evidence")
		}
		seenStatuses[status.ID] = status
		prior, ok := latest[status.Context]
		priorTime, _ := time.Parse(time.RFC3339, prior.CreatedAt)
		if !ok || created.After(priorTime) || (created.Equal(priorTime) && status.ID > prior.ID) {
			latest[status.Context] = status
		}
	}
	return latest, nil
}

// Classifies only required CI; missing, pending and nonpositive required evidence fail closed.
func (c *Client) ci(snapshot github.Snapshot) (map[string]ciState, error) {
	latest, err := latestStatuses(snapshot)
	if err != nil {
		return nil, err
	}
	states := map[string]ciState{}
	for name, status := range latest {
		if !contains(c.config.RequiredChecks, name) {
			continue
		}
		state, err := terminal(status.State)
		if err != nil {
			return nil, fmt.Errorf("CI %s: %w", name, err)
		}
		states[name] = ciState{state: state, url: status.TargetURL}
	}
	seenChecks := map[int]github.Check{}
	for _, check := range snapshot.Checks {
		if check.ID <= 0 || check.Name == "" || check.HeadSHA != snapshot.Pull.HeadSHA {
			return nil, errors.New("incomplete or old-head check evidence")
		}
		if prior, ok := seenChecks[check.ID]; ok {
			if prior != check {
				return nil, errors.New("conflicting check-run evidence")
			}
			continue
		}
		seenChecks[check.ID] = check
		if !contains(c.config.RequiredChecks, check.Name) {
			continue
		}
		if check.Status != "completed" {
			return nil, errors.New("CI is pending or unknown")
		}
		state, err := terminal(check.Conclusion)
		if err != nil {
			return nil, err
		}
		if prior, ok := states[check.Name]; ok && (prior.state != state || prior.url != check.DetailsURL) {
			return nil, errors.New("ambiguous CI runs for the same check name")
		}
		states[check.Name] = ciState{state: state, url: check.DetailsURL}
	}
	for _, name := range c.config.RequiredChecks {
		if _, ok := states[name]; !ok {
			return nil, errors.New("required CI check is missing")
		}
	}
	return states, nil
}

// Classifies only known terminal states; skipped and neutral are not success.
func terminal(state string) (string, error) {
	switch state {
	case "success":
		return "success", nil
	case "failure", "error", "timed_out", "cancelled", "action_required", "startup_failure":
		return "failure", nil
	default:
		return "", errors.New("CI is pending, unknown or not positive")
	}
}

// Validates diagnosis links and retains exact current context, bucket and job targets.
func (c *Client) retestEligible(snapshot github.Snapshot, request RetestRequest) (map[string]bool, error) {
	states, err := c.ci(snapshot)
	if err != nil {
		return nil, err
	}
	failure := prowRun.FindStringSubmatch(request.FailureURL)
	comparison := prowRun.FindStringSubmatch(request.ComparisonURL)
	if failure == nil || comparison == nil || failure[1] != comparison[1] || failure[3] != comparison[3] || failure[4] == comparison[4] {
		return nil, errors.New("failure and comparison must be distinct canonical Prow runs of the same bucket and job")
	}
	if request.Job != "" && request.Job != failure[3] {
		return nil, errors.New("named job must match the diagnosed failure")
	}
	targets := map[string]bool{}
	matched := false
	for _, name := range c.config.RequiredChecks {
		state := states[name]
		if state.state == "failure" && (request.Job == "" || state.url == request.FailureURL) {
			if run := prowRun.FindStringSubmatch(state.url); run != nil {
				targets[name+"\x00"+run[1]+"/"+run[3]] = true
			}
			if state.url == request.FailureURL {
				matched = true
			}
		}
	}
	if !matched {
		return nil, errors.New("failure URL must identify a current failing required check")
	}
	return targets, nil
}

// runIdentity binds an evidence name to a canonical Prow job/run identity.
type runIdentity struct {
	name     string
	identity string
	job      string
}

// Collects canonical run identities without treating a new status ID as a run.
func runs(snapshot github.Snapshot) []runIdentity {
	var result []runIdentity
	for _, status := range snapshot.Statuses {
		if match := prowRun.FindStringSubmatch(status.TargetURL); match != nil && status.ID > 0 {
			result = append(result, runIdentity{name: status.Context, identity: match[1] + "/" + match[2], job: match[1] + "/" + match[3]})
		}
	}
	for _, check := range snapshot.Checks {
		if match := prowRun.FindStringSubmatch(check.DetailsURL); match != nil && check.ID > 0 && check.HeadSHA == snapshot.Pull.HeadSHA {
			result = append(result, runIdentity{name: check.Name, identity: match[1] + "/" + match[2], job: match[1] + "/" + match[3]})
		}
	}
	return result
}

// Reconstructs only the originally diagnosed context, bucket and job from trusted
// comment metadata and exact run history. Missing historical context stays unobservable.
func (c *Client) priorTargets(snapshot github.Snapshot, body, job string) (map[string]bool, error) {
	failureURL := ""
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "Failure: ") {
			if failureURL != "" {
				return nil, errors.New("ambiguous prior failure metadata")
			}
			failureURL = strings.TrimPrefix(line, "Failure: ")
		}
	}
	failure := prowRun.FindStringSubmatch(failureURL)
	if failure == nil || (job != "" && failure[3] != job) {
		return nil, errors.New("prior recovery lacks a matching canonical failure URL")
	}
	targets := map[string]bool{}
	for _, run := range runs(snapshot) {
		if run.identity == failure[1]+"/"+failure[2] && contains(c.config.RequiredChecks, run.name) {
			targets[run.name+"\x00"+run.job] = true
		}
	}
	return targets, nil
}

// Finds an existing budget operation, resolving named trigger aliases only through
// trusted configuration. Edited or malformed metadata never permits another write.
func (c *Client) prior(snapshot github.Snapshot, action, head string) (Result, bool, error) {
	ordered, err := orderedComments(snapshot.Comments)
	if err != nil {
		return Result{}, false, err
	}
	prefix := "<!-- aro-hcp-github:" + c.key(action) + ":"
	marker := github.CommentMarker(c.key(action), head)
	var found *github.Comment
	for _, comment := range ordered {
		if comment.Author != c.config.Actors.Writer || !strings.Contains(comment.Body, prefix) {
			continue
		}
		if err := unedited(comment); err != nil {
			return Result{}, false, err
		}
		markerAt := strings.Index(comment.Body, prefix)
		markerText := comment.Body[markerAt:]
		markerHead := strings.TrimSuffix(strings.TrimPrefix(markerText, prefix), " -->")
		if !shaPattern.MatchString(markerHead) || markerText != github.CommentMarker(c.key(action), markerHead) || strings.Count(comment.Body, "<!-- aro-hcp-github:") != 1 || !strings.HasSuffix(comment.Body, "\n\n"+markerText) {
			return Result{}, false, errors.New("ambiguous operation marker")
		}
		if markerText != marker {
			continue
		}
		commands := commandLine.FindAllString(comment.Body, -1)
		valid := len(commands) == 1 && ((action == "release" && commands[0] == "/hold cancel") || (action == "retest" && (commands[0] == "/retest-required" || strings.HasPrefix(commands[0], "/test ") && namePattern.MatchString(strings.TrimPrefix(commands[0], "/test ")))))
		if !valid || !strings.HasSuffix(comment.Body, "\n\n"+marker) || found != nil {
			return Result{}, false, errors.New("ambiguous or duplicate operation metadata")
		}
		copy := comment
		found = &copy
	}
	if found == nil {
		return Result{}, false, nil
	}
	result := c.duplicate(snapshot, *found, action, head)
	result.baseline = map[string]bool{}
	result.targets = map[string]bool{}
	if action == "retest" {
		command := commandLine.FindAllString(found.Body, -1)[0]
		job := ""
		if command != "/retest-required" {
			alias := strings.TrimPrefix(command, "/test ")
			for canonical, configured := range c.config.JobTriggers {
				if configured == alias {
					if job != "" {
						return Result{}, false, errors.New("ambiguous prior Prow trigger alias")
					}
					job = canonical
				}
			}
			if job == "" {
				return Result{}, false, errors.New("unknown prior Prow trigger alias")
			}
		}
		metadata, present, err := decodeRecoveryMetadata(found.Body, marker, head, job)
		if err != nil {
			return Result{}, false, err
		}
		if present {
			result.targets, result.baseline = metadata.observation()
		} else {
			// Legacy comments still consume the budget. Without an original
			// baseline, runs already visible now cannot prove execution.
			result.targets, err = c.priorTargets(snapshot, found.Body, job)
			if err != nil {
				return Result{}, false, err
			}
			jobs := map[string]bool{}
			for target := range result.targets {
				_, job, _ := strings.Cut(target, "\x00")
				jobs[job] = true
			}
			for _, run := range runs(snapshot) {
				if jobs[run.job] {
					result.baseline[run.identity] = true
				}
			}
		}
	}
	return result, true, nil
}
