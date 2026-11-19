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

// Package prow requests bounded Prow maintenance actions from explicit evidence.
// A posted comment is a request, not proof of effect. Remote comment writes are
// non-atomic: these guards and operation keys do not provide compare-and-swap or
// exactly-once execution. Callers must serialize maintenance for each proposal.
package prow

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	github "github.com/Azure/ARO-HCP/tooling/github"
)

// Source supplies complete action evidence, narrow observation reads and guarded
// comment publication. Snapshot includes all comment/history pages; CI includes
// all statuses and the latest checks per name and App, bound to the requested head.
// PostComment authenticates its actor, deduplicates its head-bound operation key,
// and rechecks proposal identity, state, head and comments before writing.
type Source interface {
	PullRequest(context.Context, int) (github.PullRequest, error)
	CI(context.Context, string) (github.CommitCI, error)
	Snapshot(context.Context, int, string) (github.Snapshot, error)
	PostComment(context.Context, int, string, string, string) (github.Receipt, error)
}

// Actors is a caller-trusted roster, never inferred from comment text or bot names.
// Writer is the authenticated Source actor; RetryBots may author retry-exhaustion
// holds, and Controllers may apply the hold label. Writer and RetryBots may cancel
// evidenced retry holds, but never another actor's human or unexplained hold.
type Actors struct {
	Writer      string
	RetryBots   []string
	Controllers []string
}

// Config preserves caller-specific required checks and recovery budget namespaces.
// Use distinct namespaces for policies whose one-recovery-per-head budgets differ.
type Config struct {
	Repository      string
	Actors          Actors
	BudgetNamespace string
	RequiredChecks  []string
	// JobTriggers maps canonical Prow job names to unique trusted /test aliases.
	// Named requests require a mapping; required-set requests do not.
	JobTriggers map[string]string
}

// Client implements only hold release and diagnosed CI recovery; it cannot approve,
// merge, override policy, publish source, or issue arbitrary Prow commands.
type Client struct {
	source Source
	config Config
}

// HoldRequest identifies a held proposal and explains the requested release.
type HoldRequest struct {
	Number       int
	ExpectedHead string
	Reason       string
}

// RetestRequest binds a diagnosis to a current failure and a distinct comparison
// run of the same canonical Prow job. Job optionally selects that canonical job;
// Config.JobTriggers supplies its /test alias. Otherwise /retest-required is used.
// All forms share one namespace/head budget.
type RetestRequest struct {
	Number        int
	ExpectedHead  string
	Reason        string
	FailureURL    string
	ComparisonURL string
	Job           string
}

// Result carries the request receipt and private observation baseline. Keep the
// returned value intact for Observe, or call Retest again to resume from its comment.
type Result struct {
	Receipt  github.Receipt
	Detail   string
	client   *Client
	action   string
	baseline map[string]bool
	targets  map[string]bool
}

var (
	shaPattern        = regexp.MustCompile(`^[0-9a-f]{40}$`)
	repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	namePattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,199}$`)
	actorPattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*(?:\[bot\])?$`)
)

// New validates and copies policy inputs without reading authentication or environment.
func New(source Source, config Config) (*Client, error) {
	if source == nil || !repositoryPattern.MatchString(config.Repository) || !namePattern.MatchString(config.BudgetNamespace) {
		return nil, errors.New("source, fully qualified repository and budget namespace are required")
	}
	if !actorPattern.MatchString(config.Actors.Writer) || len(config.Actors.RetryBots) == 0 || len(config.Actors.Controllers) == 0 {
		return nil, errors.New("explicit writer, retry-bot and hold-controller rosters are required")
	}
	for _, actor := range append(append([]string{config.Actors.Writer}, config.Actors.RetryBots...), config.Actors.Controllers...) {
		if !actorPattern.MatchString(actor) {
			return nil, errors.New("invalid trusted actor")
		}
	}
	if len(config.RequiredChecks) == 0 {
		return nil, errors.New("caller must supply required CI checks")
	}
	seen := map[string]bool{}
	for _, name := range config.RequiredChecks {
		if strings.TrimSpace(name) != name || name == "" || strings.EqualFold(name, "tide") || seen[name] {
			return nil, errors.New("required CI check names must be nonempty, unique and non-Tide")
		}
		seen[name] = true
	}
	triggers := make(map[string]string, len(config.JobTriggers))
	aliases := make(map[string]bool, len(config.JobTriggers))
	for job, alias := range config.JobTriggers {
		if !namePattern.MatchString(job) || !namePattern.MatchString(alias) || aliases[alias] {
			return nil, errors.New("Prow jobs and trigger aliases must be valid names with unique aliases")
		}
		triggers[job] = alias
		aliases[alias] = true
	}
	config.JobTriggers = triggers
	config.RequiredChecks = append([]string(nil), config.RequiredChecks...)
	config.Actors.RetryBots = append([]string(nil), config.Actors.RetryBots...)
	config.Actors.Controllers = append([]string(nil), config.Actors.Controllers...)
	return &Client{source: source, config: config}, nil
}

// ReleaseHold requests cancellation only for proven bot retry-exhaustion holds
// with trusted label provenance and positive, complete required CI. One fresh
// snapshot gates the request; publication guards are not compare-and-swap.
func (c *Client) ReleaseHold(ctx context.Context, request HoldRequest) (Result, error) {
	if err := validateRequest(request.Number, request.ExpectedHead, request.Reason); err != nil {
		return Result{}, err
	}
	snapshot, err := c.snapshot(ctx, request.Number, request.ExpectedHead)
	if err != nil {
		return Result{}, err
	}
	if prior, found, err := c.prior(snapshot, "release", request.ExpectedHead); err != nil || found {
		return prior, err
	}
	if !hasHold(snapshot) {
		return c.noAction(request.Number, request.ExpectedHead, "hold label is absent"), nil
	}
	pending, err := c.releaseEligible(snapshot)
	if err != nil {
		return Result{}, err
	}
	if pending {
		return c.noAction(request.Number, request.ExpectedHead, "hold cancellation is already pending"), nil
	}
	return c.post(ctx, request.Number, request.ExpectedHead, "release", "/hold cancel\n\nReason: "+request.Reason, snapshot, nil)
}

// Retest requests one diagnosed recovery per namespace and head, never while any
// required CI is pending or unknown. Named-job and required-set recovery
// consume the same budget. One fresh snapshot gates the request; guarded publication
// is not compare-and-swap. The trusted request comment preserves its original baseline.
// Failed publication is not evidence that no write occurred.
func (c *Client) Retest(ctx context.Context, request RetestRequest) (Result, error) {
	if err := validateRequest(request.Number, request.ExpectedHead, request.Reason); err != nil {
		return Result{}, err
	}
	if request.Job != "" && (!namePattern.MatchString(request.Job) || c.config.JobTriggers[request.Job] == "") {
		return Result{}, errors.New("named Prow job requires a trusted trigger mapping")
	}
	snapshot, err := c.snapshot(ctx, request.Number, request.ExpectedHead)
	if err != nil {
		return Result{}, err
	}
	if prior, found, err := c.prior(snapshot, "retest", request.ExpectedHead); err != nil || found {
		return prior, err
	}
	targets, err := c.retestEligible(snapshot, request)
	if err != nil {
		return Result{}, err
	}
	command := "/retest-required"
	if request.Job != "" {
		command = "/test " + c.config.JobTriggers[request.Job]
	}
	body := command + "\n\nReason: " + request.Reason + "\nFailure: " + request.FailureURL + "\nComparison: " + request.ComparisonURL
	return c.post(ctx, request.Number, request.ExpectedHead, "retest", body, snapshot, targets)
}

// Observe polls until the hold label disappears or a targeted job has a new
// current-head Prow run identity, never treating comment creation as execution.
// A caller deadline and positive interval are mandatory. On cancellation, read
// failure or a head change the returned receipt is indeterminate, not observed.
func (c *Client) Observe(ctx context.Context, result Result, interval time.Duration) (github.Receipt, error) {
	receipt := result.Receipt
	if result.client != c || (result.action != "release" && result.action != "retest") || interval <= 0 {
		return receipt, errors.New("observation requires an intact result and positive interval")
	}
	if _, bounded := ctx.Deadline(); !bounded {
		return receipt, errors.New("observation requires a context deadline")
	}
	if receipt.Outcome == "not-applicable" {
		return receipt, nil
	}
	for {
		pull, err := c.pull(ctx, receipt.Number, receipt.RequestedHead)
		if err != nil {
			receipt.Outcome = "indeterminate"
			return receipt, err
		}
		receipt.ObservedHead = pull.HeadSHA
		observed := result.action == "release" && !hasHold(github.Snapshot{Pull: pull})
		if result.action == "retest" {
			ci, err := c.source.CI(ctx, receipt.RequestedHead)
			if err != nil {
				receipt.Outcome = "indeterminate"
				return receipt, err
			}
			snapshot := github.Snapshot{Pull: pull, Statuses: ci.Statuses, Checks: ci.Checks}
			if err := validateChecks(snapshot); err != nil {
				receipt.Outcome = "indeterminate"
				return receipt, err
			}
			for _, run := range runs(snapshot) {
				target := run.name + "\x00" + run.job
				if result.targets[target] && !result.baseline[run.identity] {
					observed = true
				}
			}
			// Revalidate identity after CI even when no new run was found.
			final, err := c.pull(ctx, receipt.Number, receipt.RequestedHead)
			if err != nil || !samePull(pull, final) {
				receipt.Outcome = "indeterminate"
				if err == nil {
					err = errors.New("proposal identity changed during CI observation")
				}
				return receipt, err
			}
		}
		if observed {
			receipt.Outcome = "observed"
			return receipt, nil
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			receipt.Outcome = "indeterminate"
			return receipt, ctx.Err()
		case <-timer.C:
		}
	}
}

// Validates a complete head identity and a single-line, inert diagnosis.
func validateRequest(number int, head, reason string) error {
	if number <= 0 || !shaPattern.MatchString(head) {
		return errors.New("positive pull request number and full lowercase head SHA are required")
	}
	if len(reason) < 20 || len(reason) > 4000 || strings.TrimSpace(reason) != reason || strings.ContainsAny(reason, "\r\n@<>") || strings.HasPrefix(reason, "/") {
		return errors.New("reason must be 20-4000 characters without commands, markers, mentions or line breaks; provide URLs separately")
	}
	return nil
}

// Reads and independently checks the source's head and proposal identity contract.
func (c *Client) snapshot(ctx context.Context, number int, head string) (github.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return github.Snapshot{}, err
	}
	snapshot, err := c.source.Snapshot(ctx, number, head)
	if err != nil {
		return github.Snapshot{}, err
	}
	pull := snapshot.Pull
	if err := c.validatePull(pull, number, head); err != nil {
		return github.Snapshot{}, err
	}
	if err := validateChecks(snapshot); err != nil {
		return github.Snapshot{}, err
	}
	return snapshot, nil
}

// Reads only proposal state and validates its current maintenance identity.
func (c *Client) pull(ctx context.Context, number int, head string) (github.PullRequest, error) {
	if err := ctx.Err(); err != nil {
		return github.PullRequest{}, err
	}
	pull, err := c.source.PullRequest(ctx, number)
	if err != nil {
		return github.PullRequest{}, err
	}
	return pull, c.validatePull(pull, number, head)
}

// Validates the open, non-draft same-repository proposal and requested head.
func (c *Client) validatePull(pull github.PullRequest, number int, head string) error {
	if pull.Number != number || pull.HeadSHA != head || pull.State != "open" || pull.Merged || pull.Draft || pull.BaseRepo != c.config.Repository || pull.HeadRepo != c.config.Repository {
		return errors.New("proposal identity, current head or open non-draft same-repository state changed")
	}
	return nil
}

// Compares stable proposal identity across the CI read, excluding mutable content.
func samePull(before, after github.PullRequest) bool {
	return before.Number == after.Number && before.Author == after.Author &&
		before.HeadSHA == after.HeadSHA && before.HeadRef == after.HeadRef && before.HeadRepo == after.HeadRepo &&
		before.BaseRef == after.BaseRef && before.BaseRepo == after.BaseRepo &&
		before.State == after.State && before.Merged == after.Merged && before.Draft == after.Draft
}

// Rejects check evidence that is not bound to the requested current head.
func validateChecks(snapshot github.Snapshot) error {
	for _, check := range snapshot.Checks {
		if check.HeadSHA != snapshot.Pull.HeadSHA {
			return errors.New("check evidence is for a different head")
		}
	}
	return nil
}

// Posts a fixed operation with durable exact-target observation metadata.
func (c *Client) post(ctx context.Context, number int, head, action, body string, snapshot github.Snapshot, targets map[string]bool) (Result, error) {
	result := Result{client: c, action: action}
	if action == "retest" {
		metadata := newRecoveryMetadata(head, snapshot, targets)
		encoded, err := encodeRecoveryMetadata(metadata)
		if err != nil {
			return Result{}, err
		}
		body += "\n\n" + recoveryPrefix + encoded + " -->"
		result.targets, result.baseline = metadata.observation()
	}
	// GitHub's character limit is conservatively enforced in bytes, including
	// the one root operation marker that PostComment appends.
	if len(body)+len("\n\n")+len(github.CommentMarker(c.key(action), head)) > maxCommentBytes {
		return Result{}, errors.New("request comment exceeds GitHub's size limit")
	}
	receipt, err := c.source.PostComment(ctx, number, head, c.key(action), body)
	result.Receipt = receipt
	if err != nil {
		result.Receipt.Number = number
		result.Receipt.RequestedHead = head
		result.Receipt.Outcome = "indeterminate"
		return result, err
	}
	if receipt.Number != number || receipt.RequestedHead != head || receipt.ObservedHead != head || receipt.Author != c.config.Actors.Writer || receipt.CommentID <= 0 || (receipt.Outcome != "observed" && receipt.Outcome != "duplicate") {
		result.Receipt.Outcome = "indeterminate"
		return result, errors.New("comment receipt does not match the configured writer and requested head")
	}
	if result.Receipt.Outcome == "observed" {
		result.Receipt.Outcome = "requested"
	}
	return result, nil
}

// Builds a namespace-scoped key; the root marker separately binds it to the head.
func (c *Client) key(action string) string {
	return "prow/" + c.config.BudgetNamespace + "/" + action
}

// Records an inapplicable request without claiming that a remote action happened.
func (c *Client) noAction(number int, head, detail string) Result {
	return Result{Receipt: github.Receipt{Number: number, RequestedHead: head, ObservedHead: head, Outcome: "not-applicable"}, Detail: detail, client: c, action: "release"}
}

// Reports whether the complete label set includes Prow's hold label.
func hasHold(snapshot github.Snapshot) bool {
	for _, label := range snapshot.Pull.Labels {
		if label == "do-not-merge/hold" {
			return true
		}
	}
	return false
}

// Tests exact trusted actor membership without guessing account privileges.
func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

// Builds a receipt for an existing authenticated operation without posting again.
func (c *Client) duplicate(snapshot github.Snapshot, comment github.Comment, action, head string) Result {
	return Result{Receipt: github.Receipt{Number: snapshot.Pull.Number, URL: fmt.Sprintf("https://github.com/%s/pull/%d#issuecomment-%d", c.config.Repository, snapshot.Pull.Number, comment.ID), Author: comment.Author, CommentID: comment.ID, RequestedHead: head, ObservedHead: head, Outcome: "duplicate"}, client: c, action: action}
}
