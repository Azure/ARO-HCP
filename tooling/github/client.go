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
	"regexp"
	"strings"
	"time"

	gh "github.com/google/go-github/v69/github"
)

var repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
var shaPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
var keyPattern = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

// Client accesses one repository through caller-authenticated clients.
type Client struct {
	api         *gh.Client
	writer      *gh.Client
	ci          *gh.Client
	owner, repo string
	actor       string
}

// NewClient constructs a client without reading credentials or changing authentication.
func NewClient(repository string, api *gh.Client) (*Client, error) {
	if !repositoryPattern.MatchString(repository) || api == nil {
		return nil, fmt.Errorf("invalid repository or nil client")
	}
	parts := strings.Split(repository, "/")
	return &Client{api: api, writer: api, ci: api, owner: parts[0], repo: parts[1]}, nil
}

// WithWriter returns an independent client using an explicit write credential and clears any configured actor.
func (c *Client) WithWriter(writer *gh.Client) (*Client, error) {
	if writer == nil {
		return nil, fmt.Errorf("nil writer")
	}
	clone := *c
	clone.writer = writer
	clone.actor = ""
	return &clone, nil
}

// WithCIReader returns an independent client with a separate credential for CI evidence.
func (c *Client) WithCIReader(reader *gh.Client) (*Client, error) {
	if reader == nil {
		return nil, fmt.Errorf("nil CI reader")
	}
	clone := *c
	clone.ci = reader
	return &clone, nil
}

// WithActor returns an independent client bound to a trusted expected writer login, including App bot logins.
// The caller must obtain this identity from trusted configuration, not untrusted proposal metadata.
func (c *Client) WithActor(actor string) (*Client, error) {
	if strings.TrimSpace(actor) != actor || actor == "" || strings.ContainsAny(actor, "/\r\n\t ") {
		return nil, fmt.Errorf("invalid writer actor")
	}
	clone := *c
	clone.actor = actor
	return &clone, nil
}

// Actor returns the explicitly trusted writer identity, or discovers a user-token identity.
// App installation tokens should use WithActor because GitHub does not support /user for them.
func (c *Client) Actor(ctx context.Context) (string, error) {
	if c.actor != "" {
		return c.actor, nil
	}
	user, _, err := c.writer.Users.Get(ctx, "")
	if err != nil {
		return "", err
	}
	if user.GetLogin() == "" {
		return "", ErrEvidenceIncomplete
	}
	return user.GetLogin(), nil
}

// PullRequest reads complete identity metadata for one positive pull-request number.
func (c *Client) PullRequest(ctx context.Context, number int) (PullRequest, error) {
	if number <= 0 {
		return PullRequest{}, fmt.Errorf("invalid pull request number")
	}
	req, err := c.api.NewRequest("GET", fmt.Sprintf("repos/%s/%s/pulls/%d", c.owner, c.repo, number), nil)
	if err != nil {
		return PullRequest{}, err
	}
	var raw json.RawMessage
	if _, err = c.api.Do(ctx, req, &raw); err != nil {
		return PullRequest{}, err
	}
	p, err := decodePull(raw)
	if err != nil {
		return PullRequest{}, err
	}
	out, err := pullIdentity(p)
	if err != nil {
		return PullRequest{}, fmt.Errorf("pull request %d: %w", number, err)
	}
	if out.Number != number || out.BaseRepo != c.owner+"/"+c.repo {
		return PullRequest{}, fmt.Errorf("pull request number or target repository differs: %w", ErrEvidenceIncomplete)
	}
	if p.Merged == nil || p.Labels == nil {
		return PullRequest{}, fmt.Errorf("missing merge state or labels: %w", ErrEvidenceIncomplete)
	}
	out.Merged = p.GetMerged()
	for _, l := range p.Labels {
		if l == nil || l.GetName() == "" {
			return PullRequest{}, fmt.Errorf("missing label name: %w", ErrEvidenceIncomplete)
		}
		out.Labels = append(out.Labels, l.GetName())
	}
	return out, nil
}

// DecodePull distinguishes an explicitly empty body from missing ownership evidence.
func decodePull(raw json.RawMessage) (*gh.PullRequest, error) {
	var fields struct {
		Body json.RawMessage `json:"body"`
	}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("decode pull request body: %w: %w", ErrEvidenceIncomplete, err)
	}
	if len(fields.Body) == 0 {
		return nil, fmt.Errorf("missing pull request body: %w", ErrEvidenceIncomplete)
	}
	var p gh.PullRequest
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("decode pull request: %w: %w", ErrEvidenceIncomplete, err)
	}
	if strings.TrimSpace(string(fields.Body)) == "null" {
		p.Body = gh.Ptr("")
	}
	return &p, nil
}

// PullIdentity validates metadata shared by list and detail responses, excluding detail-only merge state.
func pullIdentity(p *gh.PullRequest) (PullRequest, error) {
	if p == nil || p.GetNumber() <= 0 || p.GetUser().GetLogin() == "" {
		return PullRequest{}, fmt.Errorf("missing pull request number or author: %w", ErrEvidenceIncomplete)
	}
	if p.State == nil || (p.GetState() != "open" && p.GetState() != "closed") {
		return PullRequest{}, fmt.Errorf("missing or invalid pull request state: %w", ErrEvidenceIncomplete)
	}
	if p.Draft == nil || p.Title == nil || p.Body == nil {
		return PullRequest{}, fmt.Errorf("missing pull request readiness, title, or body: %w", ErrEvidenceIncomplete)
	}
	if !shaPattern.MatchString(p.GetHead().GetSHA()) || p.GetHead().GetRef() == "" || !repositoryPattern.MatchString(p.GetHead().GetRepo().GetFullName()) {
		return PullRequest{}, fmt.Errorf("incomplete source branch identity: %w", ErrEvidenceIncomplete)
	}
	if p.GetBase().GetRef() == "" || !repositoryPattern.MatchString(p.GetBase().GetRepo().GetFullName()) {
		return PullRequest{}, fmt.Errorf("incomplete target branch identity: %w", ErrEvidenceIncomplete)
	}
	return PullRequest{Number: p.GetNumber(), Author: p.GetUser().GetLogin(), State: p.GetState(), Draft: p.GetDraft(), Title: p.GetTitle(), Body: p.GetBody(), HeadRef: p.GetHead().GetRef(), HeadSHA: p.GetHead().GetSHA(), HeadRepo: p.GetHead().GetRepo().GetFullName(), BaseRef: p.GetBase().GetRef(), BaseRepo: p.GetBase().GetRepo().GetFullName()}, nil
}

// Paginate rejects null pages and nonadvancing pagination links.
func paginate[T any](fetch func(*gh.ListOptions) ([]T, *gh.Response, error)) ([]T, error) {
	all := make([]T, 0)
	options := &gh.ListOptions{Page: 1, PerPage: 100}
	for {
		items, response, err := fetch(options)
		if err != nil {
			return nil, err
		}
		if items == nil || response == nil {
			return nil, ErrEvidenceIncomplete
		}
		all = append(all, items...)
		if response.NextPage == 0 {
			return all, nil
		}
		if response.NextPage != options.Page+1 {
			return nil, ErrEvidenceIncomplete
		}
		options.Page = response.NextPage
	}
}

// Timestamp formats an optional API timestamp without inventing evidence.
func timestamp(t *gh.Timestamp) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// BoundPull reads and enforces a full expected head SHA.
func (c *Client) boundPull(ctx context.Context, number int, head string) (PullRequest, error) {
	if !shaPattern.MatchString(head) {
		return PullRequest{}, fmt.Errorf("expected head must be a full SHA")
	}
	p, err := c.PullRequest(ctx, number)
	if err == nil && p.HeadSHA != head {
		return PullRequest{}, ErrConflict
	}
	return p, err
}

// Comments reads every comment page and rejects incomplete or duplicate records.
func (c *Client) comments(ctx context.Context, number int) ([]*gh.IssueComment, error) {
	items, err := paginate(func(o *gh.ListOptions) ([]*gh.IssueComment, *gh.Response, error) {
		return c.api.Issues.ListComments(ctx, c.owner, c.repo, number, &gh.IssueListCommentsOptions{ListOptions: *o})
	})
	if err != nil {
		return nil, err
	}
	seen := map[int64]bool{}
	for _, v := range items {
		if v == nil || v.GetID() <= 0 || v.Body == nil || v.GetUser().GetLogin() == "" || v.CreatedAt == nil || v.UpdatedAt == nil || seen[v.GetID()] {
			return nil, ErrEvidenceIncomplete
		}
		seen[v.GetID()] = true
	}
	return items, nil
}

// Snapshot collects every evidence page, retains commit attribution and rechecks identity after collection.
func (c *Client) Snapshot(ctx context.Context, number int, expectedHead string) (Snapshot, error) {
	p, err := c.boundPull(ctx, number, expectedHead)
	if err != nil {
		return Snapshot{}, err
	}
	s := Snapshot{Pull: p}
	commits, err := paginate(func(o *gh.ListOptions) ([]*gh.RepositoryCommit, *gh.Response, error) {
		return c.api.PullRequests.ListCommits(ctx, c.owner, c.repo, number, o)
	})
	if err != nil {
		return Snapshot{}, err
	}
	// The REST commits endpoint truncates at 250; equality cannot prove completeness.
	if len(commits) >= 250 {
		return Snapshot{}, ErrEvidenceIncomplete
	}
	seenCommits := map[string]bool{}
	for _, v := range commits {
		if v == nil || !shaPattern.MatchString(v.GetSHA()) || seenCommits[v.GetSHA()] {
			return Snapshot{}, ErrEvidenceIncomplete
		}
		seenCommits[v.GetSHA()] = true
		s.Commits = append(s.Commits, v.GetSHA())
	}
	if len(commits) == 0 || commits[len(commits)-1].GetSHA() != expectedHead {
		return Snapshot{}, ErrEvidenceIncomplete
	}
	s.CommitDetails = commits
	comments, err := c.comments(ctx, number)
	if err != nil {
		return Snapshot{}, err
	}
	for _, v := range comments {
		s.Comments = append(s.Comments, Comment{ID: int(v.GetID()), Author: v.GetUser().GetLogin(), Body: v.GetBody(), CreatedAt: timestamp(v.CreatedAt), UpdatedAt: timestamp(v.UpdatedAt)})
	}
	events, err := paginate(func(o *gh.ListOptions) ([]*gh.IssueEvent, *gh.Response, error) {
		return c.api.Issues.ListIssueEvents(ctx, c.owner, c.repo, number, o)
	})
	if err != nil {
		return Snapshot{}, err
	}
	seen := map[int64]bool{}
	for _, v := range events {
		if v == nil || v.GetID() <= 0 || v.Event == nil || seen[v.GetID()] {
			return Snapshot{}, ErrEvidenceIncomplete
		}
		seen[v.GetID()] = true
		if v.GetEvent() != "labeled" && v.GetEvent() != "unlabeled" {
			continue
		}
		if v.Label == nil || v.GetLabel().GetName() == "" {
			return Snapshot{}, ErrEvidenceIncomplete
		}
		if v.GetLabel().GetName() != "do-not-merge/hold" {
			continue
		}
		if v.GetActor().GetLogin() == "" || v.CreatedAt == nil {
			return Snapshot{}, ErrEvidenceIncomplete
		}
		s.HoldEvents = append(s.HoldEvents, HoldEvent{ID: int(v.GetID()), Event: v.GetEvent(), Actor: v.GetActor().GetLogin(), CreatedAt: timestamp(v.CreatedAt)})
	}
	ci, err := c.CI(ctx, expectedHead)
	if err != nil {
		return Snapshot{}, err
	}
	s.Statuses, s.Checks = ci.Statuses, ci.Checks
	latest, err := c.boundPull(ctx, number, expectedHead)
	if err != nil {
		return Snapshot{}, err
	}
	if latest.HeadRef != p.HeadRef || latest.HeadRepo != p.HeadRepo || latest.BaseRef != p.BaseRef || latest.Author != p.Author || latest.State != p.State {
		return Snapshot{}, ErrConflict
	}
	s.Pull = latest
	return s, nil
}

// CI reads every status and latest-check page for a full commit SHA without collecting PR history.
func (c *Client) CI(ctx context.Context, head string) (CommitCI, error) {
	if !shaPattern.MatchString(head) {
		return CommitCI{}, fmt.Errorf("CI head must be a full SHA")
	}
	ci := CommitCI{}
	statuses, err := paginate(func(o *gh.ListOptions) ([]*gh.RepoStatus, *gh.Response, error) {
		return c.ci.Repositories.ListStatuses(ctx, c.owner, c.repo, head, o)
	})
	if err != nil {
		return CommitCI{}, fmt.Errorf("list commit statuses: %w", err)
	}
	seen := map[int64]bool{}
	for _, v := range statuses {
		if v == nil || v.GetID() <= 0 || v.GetContext() == "" || v.GetState() == "" || v.CreatedAt == nil || seen[v.GetID()] {
			return CommitCI{}, fmt.Errorf("invalid or duplicate commit status: %w", ErrEvidenceIncomplete)
		}
		seen[v.GetID()] = true
		ci.Statuses = append(ci.Statuses, Status{ID: int(v.GetID()), Context: v.GetContext(), State: v.GetState(), CreatedAt: timestamp(v.CreatedAt), TargetURL: v.GetTargetURL()})
	}
	totalChecks := -1
	checks, err := paginate(func(o *gh.ListOptions) ([]*gh.CheckRun, *gh.Response, error) {
		filter := "latest"
		v, r, e := c.ci.Checks.ListCheckRunsForRef(ctx, c.owner, c.repo, head, &gh.ListCheckRunsOptions{Filter: &filter, ListOptions: *o})
		if e != nil {
			return nil, r, e
		}
		if v == nil || v.Total == nil || v.GetTotal() < 0 {
			return nil, r, fmt.Errorf("missing check-run total: %w", ErrEvidenceIncomplete)
		}
		if totalChecks >= 0 && totalChecks != v.GetTotal() {
			return nil, r, fmt.Errorf("changed check-run total: %w", ErrEvidenceIncomplete)
		}
		totalChecks = v.GetTotal()
		return v.CheckRuns, r, nil
	})
	if err != nil {
		return CommitCI{}, fmt.Errorf("list latest check runs: %w", err)
	}
	if len(checks) != totalChecks {
		return CommitCI{}, fmt.Errorf("check-run count differs from total: %w", ErrEvidenceIncomplete)
	}
	seen = map[int64]bool{}
	for _, v := range checks {
		if v == nil || v.GetID() <= 0 || v.GetName() == "" || v.GetHeadSHA() != head || v.GetStatus() == "" || seen[v.GetID()] {
			return CommitCI{}, fmt.Errorf("invalid, foreign-head, or duplicate check run: %w", ErrEvidenceIncomplete)
		}
		seen[v.GetID()] = true
		ci.Checks = append(ci.Checks, Check{ID: int(v.GetID()), Name: v.GetName(), HeadSHA: v.GetHeadSHA(), Status: v.GetStatus(), Conclusion: v.GetConclusion(), DetailsURL: v.GetDetailsURL()})
	}
	return ci, nil
}
