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
	owner, repo string
	actor       string
}

// NewClient constructs a client without reading credentials or changing authentication.
func NewClient(repository string, api *gh.Client) (*Client, error) {
	if !repositoryPattern.MatchString(repository) || api == nil {
		return nil, fmt.Errorf("invalid repository or nil client")
	}
	parts := strings.Split(repository, "/")
	return &Client{api: api, writer: api, owner: parts[0], repo: parts[1]}, nil
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
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return PullRequest{}, ErrEvidenceIncomplete
	}
	body, ok := fields["body"]
	if !ok {
		return PullRequest{}, ErrEvidenceIncomplete
	}
	var p gh.PullRequest
	if json.Unmarshal(raw, &p) != nil {
		return PullRequest{}, ErrEvidenceIncomplete
	}
	if strings.TrimSpace(string(body)) == "null" {
		p.Body = gh.Ptr("")
	}
	if p.GetNumber() != number || p.User == nil || p.GetUser().GetLogin() == "" || p.State == nil || (p.GetState() != "open" && p.GetState() != "closed") || p.Draft == nil || p.Merged == nil || p.Title == nil || p.Body == nil || p.Labels == nil || p.Head == nil || p.Base == nil || !shaPattern.MatchString(p.GetHead().GetSHA()) || p.GetHead().GetRef() == "" || !repositoryPattern.MatchString(p.GetHead().GetRepo().GetFullName()) || p.GetBase().GetRef() == "" || p.GetBase().GetRepo().GetFullName() != c.owner+"/"+c.repo {
		return PullRequest{}, ErrEvidenceIncomplete
	}
	out := PullRequest{Number: number, Author: p.GetUser().GetLogin(), State: p.GetState(), Draft: p.GetDraft(), Merged: p.GetMerged(), Title: p.GetTitle(), Body: p.GetBody(), HeadRef: p.GetHead().GetRef(), HeadSHA: p.GetHead().GetSHA(), HeadRepo: p.GetHead().GetRepo().GetFullName(), BaseRef: p.GetBase().GetRef(), BaseRepo: p.GetBase().GetRepo().GetFullName()}
	for _, l := range p.Labels {
		if l == nil || l.GetName() == "" {
			return PullRequest{}, ErrEvidenceIncomplete
		}
		out.Labels = append(out.Labels, l.GetName())
	}
	return out, nil
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

// Snapshot collects every evidence page and rechecks identity after collection.
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
	statuses, err := paginate(func(o *gh.ListOptions) ([]*gh.RepoStatus, *gh.Response, error) {
		return c.api.Repositories.ListStatuses(ctx, c.owner, c.repo, expectedHead, o)
	})
	if err != nil {
		return Snapshot{}, err
	}
	seen = map[int64]bool{}
	for _, v := range statuses {
		if v == nil || v.GetID() <= 0 || v.GetContext() == "" || v.GetState() == "" || v.CreatedAt == nil || seen[v.GetID()] {
			return Snapshot{}, ErrEvidenceIncomplete
		}
		seen[v.GetID()] = true
		s.Statuses = append(s.Statuses, Status{ID: int(v.GetID()), Context: v.GetContext(), State: v.GetState(), CreatedAt: timestamp(v.CreatedAt), TargetURL: v.GetTargetURL()})
	}
	totalChecks := -1
	checks, err := paginate(func(o *gh.ListOptions) ([]*gh.CheckRun, *gh.Response, error) {
		filter := "latest"
		v, r, e := c.api.Checks.ListCheckRunsForRef(ctx, c.owner, c.repo, expectedHead, &gh.ListCheckRunsOptions{Filter: &filter, ListOptions: *o})
		if e != nil {
			return nil, r, e
		}
		if v == nil || v.Total == nil || v.GetTotal() < 0 {
			return nil, r, ErrEvidenceIncomplete
		}
		if totalChecks >= 0 && totalChecks != v.GetTotal() {
			return nil, r, ErrEvidenceIncomplete
		}
		totalChecks = v.GetTotal()
		return v.CheckRuns, r, nil
	})
	if err != nil {
		return Snapshot{}, err
	}
	if len(checks) != totalChecks {
		return Snapshot{}, ErrEvidenceIncomplete
	}
	seen = map[int64]bool{}
	for _, v := range checks {
		if v == nil || v.GetID() <= 0 || v.GetName() == "" || v.GetHeadSHA() != expectedHead || v.GetStatus() == "" || seen[v.GetID()] {
			return Snapshot{}, ErrEvidenceIncomplete
		}
		seen[v.GetID()] = true
		s.Checks = append(s.Checks, Check{ID: int(v.GetID()), Name: v.GetName(), HeadSHA: v.GetHeadSHA(), Status: v.GetStatus(), Conclusion: v.GetConclusion(), DetailsURL: v.GetDetailsURL()})
	}
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
