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

// Package publish turns the committed digest update into one owned pull
// request, reusing the rolling branch when a run already opened one.
package publish

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"

	githublib "github.com/Azure/ARO-HCP/tooling/github"
)

// errNoChanges reports a run whose commit does not differ from the base branch.
var errNoChanges = errors.New("no committed changes differ from the base branch")

// publishBackoff bounds publication retries while GitHub catches up with the pushed branch.
// Steps bounds the loop, so no Cap is needed: with a Cap, the last delay can end the loop
// without a following attempt.
var publishBackoff = wait.Backoff{Duration: time.Second, Factor: 2, Jitter: 0.2, Steps: 5}

// Publisher is the verified publication surface used by Options.
type Publisher interface {
	OpenPullRequests(ctx context.Context) ([]githublib.PullRequest, error)
	Publish(ctx context.Context, request githublib.PublicationRequest) (githublib.Receipt, error)
}

// Input holds the validated input of one publication.
type Input struct {
	Repo          string
	Base          string
	BaseCommit    string
	HeadRepo      string
	Branch        string
	Title         string
	Marker        string
	CommitMessage string
	GitName       string
	GitEmail      string
	Actor         string
	Paths         []string
	Body          []byte
	DryRun        bool
}

// Option configures the publication dependencies.
type Option func(*Options)

// WithToken sets the credential used to push and publish.
func WithToken(token TokenSource) Option {
	return func(options *Options) { options.token = token }
}

// WithWriter sets where a run writes its receipt.
func WithWriter(out io.Writer) Option {
	return func(options *Options) { options.out = out }
}

// Options is the completed publication that Run executes.
type Options struct {
	input        Input
	out          io.Writer
	token        TokenSource
	git          gitOps
	publisherFor func(ctx context.Context) (Publisher, error)
}

// NewOptions completes the input with git and publication dependencies.
func NewOptions(input Input, options ...Option) (*Options, error) {
	completed := &Options{input: input, out: io.Discard}
	for _, option := range options {
		option(completed)
	}
	if completed.token == nil {
		return nil, fmt.Errorf("publication requires a token source")
	}
	completed.git = defaultGit(input.Repo, input.HeadRepo, completed.token)
	completed.publisherFor = func(ctx context.Context) (Publisher, error) {
		value, err := completed.token(ctx)
		if err != nil {
			return nil, err
		}
		return newPublisher(input.HeadRepo, value, input.Actor)
	}
	return completed, nil
}

// Input returns a copy of the completed publication input.
func (o *Options) Input() Input {
	return o.input
}

// Run commits the intended changes, publishes them and reports the verified receipt.
func (o *Options) Run(ctx context.Context) error {
	committed, err := o.git.commit(ctx, o.input.CommitMessage, o.input.GitName, o.input.GitEmail, o.input.Paths)
	if err != nil {
		return fmt.Errorf("commit the digest update: %w", err)
	}
	if o.input.DryRun {
		fmt.Fprintf(o.out, "Dry run: committed %d files on %s and stopped before publishing.\n", len(committed), o.input.Branch)
		return nil
	}
	if len(committed) == 0 {
		return fmt.Errorf("%w: nothing differs from %s", errNoChanges, o.input.Base)
	}
	head, err := o.git.head(ctx)
	if err != nil {
		return fmt.Errorf("read the commit to publish: %w", err)
	}
	baseCommit, err := o.git.revParse(ctx, o.input.BaseCommit)
	if err != nil {
		return fmt.Errorf("read the preparation base %s: %w", o.input.BaseCommit, err)
	}
	files, err := o.git.changedFiles(ctx, baseCommit)
	if err != nil {
		return fmt.Errorf("list the published files: %w", err)
	}
	if len(files) == 0 {
		return fmt.Errorf("%w: nothing differs from %s", errNoChanges, o.input.Base)
	}
	if err := onlyIntended(files, o.input.Paths); err != nil {
		return fmt.Errorf("the committed change set is not the intended one: %w: %w", githublib.ErrConflict, err)
	}
	remoteBase, err := o.git.remoteTip(ctx, o.input.Base)
	if err != nil {
		return fmt.Errorf("read the %s tip: %w", o.input.Base, err)
	}
	// The base moves while the update is prepared. Publish against the current
	// tip: the pull request diff is computed from the merge base, so the prepared
	// files stay the complete change set, and CI validates the merged result.
	if remoteBase != "" {
		baseCommit = remoteBase
	}
	branchTip, err := o.git.remoteTip(ctx, o.input.Branch)
	if err != nil {
		return fmt.Errorf("read the %s tip: %w", o.input.Branch, err)
	}
	publisher, err := o.publisherFor(ctx)
	if err != nil {
		return err
	}
	prior, err := o.ownedProposal(ctx, publisher, branchTip)
	if err != nil {
		return err
	}
	if err := o.git.push(ctx, o.input.Branch, branchTip); err != nil {
		return err
	}
	request := o.publicationRequest(head, baseCommit, files, prior)
	receipt, err := o.publishWithRetry(ctx, publisher, request)
	if err != nil {
		return fmt.Errorf("publish the pull request: %w", err)
	}
	encoder := json.NewEncoder(o.out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(receipt)
}

// publishWithRetry retries pre-write conflicts while GitHub catches up with the pushed branch.
// A proposal head trails its branch for a moment after a force push, and the library refuses
// the mismatch before writing anything. Indeterminate writes are never retried.
func (o *Options) publishWithRetry(ctx context.Context, publisher Publisher, request githublib.PublicationRequest) (githublib.Receipt, error) {
	var receipt githublib.Receipt
	var conflict error
	err := wait.ExponentialBackoffWithContext(ctx, publishBackoff, func(context.Context) (bool, error) {
		attempt, err := publisher.Publish(ctx, request)
		receipt = attempt
		if err == nil {
			return true, nil
		}
		if errors.Is(err, githublib.ErrIndeterminate) || !errors.Is(err, githublib.ErrConflict) {
			return false, err
		}
		conflict = err
		return false, nil
	})
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return receipt, err
	}
	if wait.Interrupted(err) && conflict != nil {
		return receipt, conflict
	}
	return receipt, err
}

// ownedProposal returns the open proposal this run must update, refusing a branch it does not own.
func (o *Options) ownedProposal(ctx context.Context, publisher Publisher, branchTip string) (*githublib.PullRequest, error) {
	open, err := publisher.OpenPullRequests(ctx)
	if err != nil {
		return nil, fmt.Errorf("read open proposals: %w", err)
	}
	candidates := make([]githublib.PullRequest, 0, len(open))
	for _, proposal := range open {
		onBranch := proposal.HeadRepo == o.input.HeadRepo && proposal.HeadRef == o.input.Branch
		owned := proposal.Author == o.input.Actor && hasMarker(proposal.Body, o.input.Marker)
		if onBranch || owned {
			candidates = append(candidates, proposal)
		}
	}
	switch len(candidates) {
	case 0:
		return nil, nil
	case 1:
	default:
		numbers := make([]int, 0, len(candidates))
		for _, candidate := range candidates {
			numbers = append(numbers, candidate.Number)
		}
		return nil, fmt.Errorf("multiple proposals claim the rolling branch: %v: %w", numbers, githublib.ErrConflict)
	}
	prior := candidates[0]
	if prior.HeadRepo != o.input.HeadRepo || prior.HeadRef != o.input.Branch {
		return nil, fmt.Errorf("owned proposal %d is on %s@%s, not %s@%s: %w", prior.Number, prior.HeadRepo, prior.HeadRef, o.input.HeadRepo, o.input.Branch, githublib.ErrConflict)
	}
	if prior.Author != o.input.Actor || !hasMarker(prior.Body, o.input.Marker) {
		return nil, fmt.Errorf("proposal %d uses the rolling branch but is not owned by %s: %w", prior.Number, o.input.Actor, githublib.ErrConflict)
	}
	if prior.HeadSHA != branchTip {
		return nil, fmt.Errorf("proposal %d head %s differs from branch tip %q: %w", prior.Number, prior.HeadSHA, branchTip, githublib.ErrConflict)
	}
	return &prior, nil
}

// publicationRequest binds the pushed commit and the authorized proposal text.
func (o *Options) publicationRequest(head, baseCommit string, files []string, prior *githublib.PullRequest) githublib.PublicationRequest {
	request := githublib.PublicationRequest{
		HeadRepository: o.input.HeadRepo,
		HeadRef:        o.input.Branch,
		ExpectedHead:   head,
		BaseRef:        o.input.Base,
		ExpectedBase:   baseCommit,
		Actor:          o.input.Actor,
		Marker:         o.input.Marker,
		Title:          o.input.Title,
		Body:           string(o.input.Body) + "\n" + o.input.Marker + "\n",
		ExpectedFiles:  files,
		ReadyForReview: true,
	}
	if prior == nil {
		return request
	}
	request.ExpectedNumber = prior.Number
	request.ExpectedTitle = prior.Title
	request.ExpectedBody = prior.Body
	return request
}

// hasMarker reports whether body carries marker as one exact line.
func hasMarker(body, marker string) bool {
	for _, line := range strings.Split(body, "\n") {
		if line == marker {
			return true
		}
	}
	return false
}
