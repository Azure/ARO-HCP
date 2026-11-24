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

package github

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	gh "github.com/google/go-github/v69/github"
)

// PublicationRequest describes a caller-prepared, already-pushed branch. Publish never changes Git refs.
// HeadRepository is the exact source repository, including a fork when publishing across repositories.
// ExpectedBase is the required base branch tip, checked read-only before and after publication.
// Marker must be an exact standalone HTML comment owned by the caller, not a substring of another marker.
// Existing proposals retain their readiness; new proposals default to drafts.
type PublicationRequest struct {
	HeadRepository string
	HeadRef        string
	ExpectedHead   string
	BaseRef        string
	ExpectedBase   string
	Actor          string
	Marker         string
	Title          string
	Body           string
	ExpectedFiles  []string
	// ReadyForReview explicitly opts new PRs into the caller's ready-publication policy.
	// It never changes an existing proposal's readiness.
	ReadyForReview bool
}

// HasMarker matches an exact marker line, avoiding substring ownership claims.
func hasMarker(body, marker string) bool {
	for _, line := range strings.Split(body, "\n") {
		if line == marker {
			return true
		}
	}
	return false
}

// BranchHead reads one branch tip without modifying it.
func (c *Client) branchHead(ctx context.Context, repository, ref string) (string, error) {
	parts := strings.Split(repository, "/")
	v, _, err := c.api.Repositories.GetBranch(ctx, parts[0], parts[1], ref, 1)
	if err != nil {
		return "", err
	}
	if !shaPattern.MatchString(v.GetCommit().GetSHA()) {
		return "", ErrEvidenceIncomplete
	}
	return v.GetCommit().GetSHA(), nil
}

// PublicationHeads checks the caller's prepared branch and explicit read-only base expectation.
func (c *Client) publicationHeads(ctx context.Context, r PublicationRequest) error {
	head, err := c.branchHead(ctx, r.HeadRepository, r.HeadRef)
	if err != nil {
		return err
	}
	base, err := c.branchHead(ctx, c.owner+"/"+c.repo, r.BaseRef)
	if err != nil {
		return err
	}
	if head != r.ExpectedHead || base != r.ExpectedBase {
		return ErrConflict
	}
	return nil
}

// OwnedPulls inventories all open proposals before selecting an exact owned identity.
func (c *Client) ownedPulls(ctx context.Context, r PublicationRequest) ([]PullRequest, error) {
	open, err := c.OpenPullRequests(ctx)
	if err != nil {
		return nil, err
	}
	var owned []PullRequest
	for _, p := range open {
		sameBranch := p.HeadRepo == r.HeadRepository && p.HeadRef == r.HeadRef
		sameMarker := hasMarker(p.Body, r.Marker)
		if !sameBranch && !sameMarker {
			continue
		}
		if !sameBranch || !sameMarker {
			return nil, fmt.Errorf("proposal branch and ownership marker disagree: %w", ErrConflict)
		}
		if err := c.publicationIdentity(p, r); err != nil {
			return nil, err
		}
		owned = append(owned, p)
	}
	if len(owned) > 1 {
		return nil, fmt.Errorf("multiple owned proposals: %w", ErrConflict)
	}
	return owned, nil
}

// OpenPullRequests reads the complete open pull request inventory, rejecting
// pages that could hide an entry or describe a foreign target.
func (c *Client) OpenPullRequests(ctx context.Context) ([]PullRequest, error) {
	records, err := paginate(func(o *gh.ListOptions) ([]json.RawMessage, *gh.Response, error) {
		path := fmt.Sprintf("repos/%s/%s/pulls?state=open&per_page=%d&page=%d", c.owner, c.repo, o.PerPage, o.Page)
		req, err := c.api.NewRequest("GET", path, nil)
		if err != nil {
			return nil, nil, err
		}
		var page []json.RawMessage
		response, err := c.api.Do(ctx, req, &page)
		return page, response, err
	})
	if err != nil {
		return nil, err
	}
	open := make([]PullRequest, 0, len(records))
	seen := map[int]bool{}
	for _, raw := range records {
		v, err := decodePull(raw)
		if err != nil {
			return nil, fmt.Errorf("list open pull requests: %w", err)
		}
		p, err := pullIdentity(v)
		if err != nil {
			return nil, fmt.Errorf("list open pull requests: %w", err)
		}
		if p.BaseRepo != c.owner+"/"+c.repo || p.State != "open" {
			return nil, fmt.Errorf("open pull request inventory contains a foreign target or state: %w", ErrEvidenceIncomplete)
		}
		// A duplicate number means the inventory cannot be trusted to describe a
		// pull request once, so every consumer rejects it rather than deduplicating.
		if seen[p.Number] {
			return nil, fmt.Errorf("duplicate pull request number in inventory: %w", ErrEvidenceIncomplete)
		}
		seen[p.Number] = true
		open = append(open, p)
	}
	return open, nil
}

// OpenProposals reports the numbers of open pull requests authored by actor that
// carry marker or whose head branch starts with branchPrefix. A run uses it to
// stop before doing expensive work while its own proposal is still pending.
func (c *Client) OpenProposals(ctx context.Context, actor, marker, branchPrefix string) ([]int, error) {
	if actor == "" || (marker == "" && branchPrefix == "") {
		return nil, fmt.Errorf("proposal inventory requires an actor and a marker or a branch prefix")
	}
	open, err := c.OpenPullRequests(ctx)
	if err != nil {
		return nil, err
	}
	numbers := make([]int, 0, len(open))
	for _, p := range open {
		if p.Author != actor {
			continue
		}
		if marker != "" && hasMarker(p.Body, marker) {
			numbers = append(numbers, p.Number)
			continue
		}
		if branchPrefix != "" && strings.HasPrefix(p.HeadRef, branchPrefix) {
			numbers = append(numbers, p.Number)
		}
	}
	sort.Ints(numbers)
	return numbers, nil
}

// PublicationIdentity checks the exact owner, source, target, and head of a selected proposal.
func (c *Client) publicationIdentity(p PullRequest, r PublicationRequest) error {
	if p.Author != r.Actor {
		return fmt.Errorf("proposal author differs: %w", ErrConflict)
	}
	if p.HeadRepo != r.HeadRepository || p.HeadRef != r.HeadRef || p.HeadSHA != r.ExpectedHead {
		return fmt.Errorf("proposal source branch differs: %w", ErrConflict)
	}
	if p.BaseRepo != c.owner+"/"+c.repo || p.BaseRef != r.BaseRef || p.State != "open" || p.Merged {
		return fmt.Errorf("proposal target or open state differs: %w", ErrConflict)
	}
	if !hasMarker(p.Body, r.Marker) {
		return fmt.Errorf("proposal ownership marker differs: %w", ErrConflict)
	}
	return nil
}

// ValidatePublication checks caller-owned branch, content, and file expectations before API access.
func (c *Client) validatePublication(r PublicationRequest) error {
	if !repositoryPattern.MatchString(r.HeadRepository) || r.HeadRef == "" || r.BaseRef == "" {
		return fmt.Errorf("publication requires source repository and source and target refs")
	}
	if r.HeadRepository == c.owner+"/"+c.repo && r.HeadRef == r.BaseRef {
		return fmt.Errorf("publication source and target branches must differ")
	}
	if !shaPattern.MatchString(r.ExpectedHead) || !shaPattern.MatchString(r.ExpectedBase) {
		return fmt.Errorf("publication requires full source and target SHAs")
	}
	if r.Actor == "" || strings.TrimSpace(r.Title) == "" {
		return fmt.Errorf("publication requires actor and title")
	}
	if !strings.HasPrefix(r.Marker, "<!-- ") || !strings.HasSuffix(r.Marker, " -->") || strings.ContainsAny(r.Marker, "\r\n") || strings.Count(r.Marker, "<!--") != 1 || strings.Count(r.Marker, "-->") != 1 {
		return fmt.Errorf("publication marker must be one standalone HTML comment")
	}
	if !hasMarker(r.Body, r.Marker) {
		return fmt.Errorf("publication body must contain its exact marker")
	}
	if len(r.ExpectedFiles) == 0 {
		return fmt.Errorf("publication requires expected files")
	}
	paths := make(map[string]bool, len(r.ExpectedFiles))
	for _, path := range r.ExpectedFiles {
		if path == "" || paths[path] {
			return fmt.Errorf("publication expected files contain an empty or duplicate path")
		}
		paths[path] = true
	}
	return nil
}

// VerifyFiles compares every paginated PR file with the caller's exact expected path set.
func (c *Client) verifyFiles(ctx context.Context, number int, expected []string) error {
	files, err := paginate(func(o *gh.ListOptions) ([]*gh.CommitFile, *gh.Response, error) {
		return c.api.PullRequests.ListFiles(ctx, c.owner, c.repo, number, o)
	})
	if err != nil {
		return err
	}
	// GitHub caps this endpoint at 3000 files, so reaching that bound is not complete evidence.
	if len(files) >= 3000 {
		return ErrEvidenceIncomplete
	}
	want := make(map[string]bool, len(expected))
	for _, path := range expected {
		if path == "" || want[path] {
			return fmt.Errorf("invalid expected files")
		}
		want[path] = true
	}
	seen := map[string]bool{}
	for _, f := range files {
		if f == nil || f.GetFilename() == "" || seen[f.GetFilename()] {
			return ErrEvidenceIncomplete
		}
		seen[f.GetFilename()] = true
		if !want[f.GetFilename()] {
			return ErrConflict
		}
	}
	if len(seen) != len(want) {
		return ErrConflict
	}
	return nil
}

// Publish finds, creates or updates exactly one owned proposal and verifies all remote evidence.
// Write failures are indeterminate, never automatically retried. Concurrent publishers require external serialization.
// GitHub cannot atomically condition PR metadata writes on a head SHA; post-write races fail indeterminate.
func (c *Client) Publish(ctx context.Context, r PublicationRequest) (Receipt, error) {
	receipt := Receipt{RequestedHead: r.ExpectedHead, Outcome: "not-applicable"}
	if err := c.validatePublication(r); err != nil {
		return receipt, err
	}
	actor, err := c.Actor(ctx)
	if err != nil {
		return receipt, err
	}
	if actor != r.Actor {
		return receipt, ErrConflict
	}
	receipt.Author = actor
	if err = c.publicationHeads(ctx, r); err != nil {
		return receipt, err
	}
	owned, err := c.ownedPulls(ctx, r)
	if err != nil {
		return receipt, err
	}
	var prior *PullRequest
	if len(owned) == 1 {
		prior = &owned[0]
		receipt.Number = prior.Number
		if err = c.verifyFiles(ctx, prior.Number, r.ExpectedFiles); err != nil {
			return receipt, err
		}
	}
	if err = c.publicationHeads(ctx, r); err != nil {
		return receipt, err
	}
	receipt.Outcome = "requested"
	if prior == nil {
		head := r.HeadRef
		var headRepo *string
		if r.HeadRepository != c.owner+"/"+c.repo {
			parts := strings.Split(r.HeadRepository, "/")
			head = parts[0] + ":" + r.HeadRef
			headRepo = &parts[1]
		}
		p, _, writeErr := c.writer.PullRequests.Create(ctx, c.owner, c.repo, &gh.NewPullRequest{Title: &r.Title, Body: &r.Body, Head: &head, HeadRepo: headRepo, Base: &r.BaseRef, Draft: gh.Ptr(!r.ReadyForReview)})
		if writeErr != nil || p == nil || p.GetNumber() <= 0 {
			receipt.Outcome = "indeterminate"
			return receipt, indeterminate("create proposal", writeErr)
		}
		receipt.Number = p.GetNumber()
	} else if prior.Title != r.Title || prior.Body != r.Body {
		_, _, writeErr := c.writer.PullRequests.Edit(ctx, c.owner, c.repo, prior.Number, &gh.PullRequest{Title: &r.Title, Body: &r.Body})
		if writeErr != nil {
			receipt.Outcome = "indeterminate"
			return receipt, indeterminate("update proposal", writeErr)
		}
	}
	receipt.URL = fmt.Sprintf("https://github.com/%s/%s/pull/%d", c.owner, c.repo, receipt.Number)
	verified, err := c.ownedPulls(ctx, r)
	if err != nil || len(verified) != 1 || verified[0].Number != receipt.Number {
		receipt.Outcome = "indeterminate"
		return receipt, indeterminate("verify proposal inventory", err)
	}
	p := verified[0]
	if p.Title != r.Title || p.Body != r.Body || (prior == nil && p.Draft == r.ReadyForReview) || (prior != nil && p.Draft != prior.Draft) {
		receipt.Outcome = "indeterminate"
		return receipt, indeterminate("proposal content or readiness differs", nil)
	}
	if err = c.verifyFiles(ctx, p.Number, r.ExpectedFiles); err != nil {
		receipt.Outcome = "indeterminate"
		return receipt, indeterminate("verify proposal files", err)
	}
	if err = c.publicationHeads(ctx, r); err != nil {
		receipt.Outcome = "indeterminate"
		return receipt, indeterminate("verify publication branch heads", err)
	}
	latest, err := c.boundPull(ctx, p.Number, r.ExpectedHead)
	if err != nil {
		receipt.Outcome = "indeterminate"
		return receipt, indeterminate("read back published proposal", err)
	}
	if err = c.publicationIdentity(latest, r); err != nil {
		receipt.Outcome = "indeterminate"
		return receipt, indeterminate("verify published proposal identity", err)
	}
	if latest.Title != r.Title || latest.Body != r.Body || latest.Draft != p.Draft {
		receipt.Outcome = "indeterminate"
		return receipt, indeterminate("published proposal content or readiness differs", nil)
	}
	receipt.ObservedHead = p.HeadSHA
	receipt.HeadSHA = p.HeadSHA
	receipt.HeadRepo = p.HeadRepo
	receipt.Base = p.BaseRef
	receipt.Outcome = "observed"
	return receipt, nil
}
