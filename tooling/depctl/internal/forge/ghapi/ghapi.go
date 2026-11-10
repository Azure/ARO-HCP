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

// Package ghapi reads pull-request evidence with the GitHub REST API.
//
// It is a read-only adapter: no method here mutates a repository, branch, pull
// request or comment. The token is read from GH_TOKEN or GITHUB_TOKEN and is
// never stored in a record or included in an error.
package ghapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/go-github/v69/github"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/forge"
)

// holdLabel is the label whose transitions block merging.
const holdLabel = "do-not-merge/hold"

// Reader reads evidence through the GitHub REST API.
type Reader struct {
	client *github.Client
	owner  string
	repo   string
}

// New returns a reader for an OWNER/REPO using the environment token.
func New(repository string) (*Reader, error) {
	owner, repo, err := splitRepository(repository)
	if err != nil {
		return nil, err
	}
	token := ""
	for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if token = os.Getenv(name); token != "" {
			break
		}
	}
	if token == "" {
		return nil, forge.ErrAuthentication
	}
	client := github.NewClient(&http.Client{Timeout: 2 * time.Minute})
	return &Reader{client: client.WithAuthToken(token), owner: owner, repo: repo}, nil
}

// NewWithClient returns a reader over an injected client, used by tests to
// point the API at a local server.
func NewWithClient(repository string, client *github.Client) (*Reader, error) {
	owner, repo, err := splitRepository(repository)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, errors.New("github client is nil")
	}
	return &Reader{client: client, owner: owner, repo: repo}, nil
}

// Splits OWNER/REPO and rejects anything else.
func splitRepository(repository string) (string, string, error) {
	parts := strings.Split(repository, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("repository %q is not OWNER/REPO", repository)
	}
	return parts[0], parts[1], nil
}

// PullRequest implements forge.Reader.
func (r *Reader) PullRequest(ctx context.Context, number int) (forge.PullRequest, error) {
	pull, _, err := r.client.PullRequests.Get(ctx, r.owner, r.repo, number)
	if err != nil {
		return forge.PullRequest{}, fmt.Errorf("read pull request #%d: %w", number, err)
	}
	if pull.GetNumber() != number {
		return forge.PullRequest{}, fmt.Errorf("%w: pull request %d returned %d",
			forge.ErrEvidenceIncomplete, number, pull.GetNumber())
	}
	return convertPull(pull), nil
}

// Converts an API record into the forge view.
func convertPull(pull *github.PullRequest) forge.PullRequest {
	converted := forge.PullRequest{
		Number: pull.GetNumber(), Author: pull.GetUser().GetLogin(), State: pull.GetState(),
		Draft: pull.GetDraft(), Merged: pull.GetMerged(), Title: pull.GetTitle(), Body: pull.GetBody(),
		HeadRef: pull.GetHead().GetRef(), HeadSHA: pull.GetHead().GetSHA(),
		HeadRepo: pull.GetHead().GetRepo().GetFullName(),
		BaseRef:  pull.GetBase().GetRef(), BaseRepo: pull.GetBase().GetRepo().GetFullName(),
	}
	for _, label := range pull.Labels {
		converted.Labels = append(converted.Labels, label.GetName())
	}
	return converted
}

// OpenPullRequests implements forge.Reader.
func (r *Reader) OpenPullRequests(ctx context.Context, author string) ([]forge.PullRequest, error) {
	issues, err := paginate(func(options *github.ListOptions) ([]*github.Issue, *github.Response, error) {
		return r.client.Issues.ListByRepo(ctx, r.owner, r.repo, &github.IssueListByRepoOptions{
			State:       "open",
			Creator:     author,
			ListOptions: *options,
		})
	})
	if err != nil {
		return nil, fmt.Errorf("list open pull requests by %s: %w", author, err)
	}
	var pulls []forge.PullRequest
	seen := map[int]bool{}
	for _, issue := range issues {
		if issue.PullRequestLinks == nil || issue.GetUser().GetLogin() != author {
			continue
		}
		number := issue.GetNumber()
		if seen[number] {
			return nil, fmt.Errorf("%w: duplicate pull request %d in paginated inventory",
				forge.ErrEvidenceIncomplete, number)
		}
		seen[number] = true
		pull, err := r.PullRequest(ctx, number)
		if err != nil {
			return nil, err
		}
		if pull.State == "open" {
			pulls = append(pulls, pull)
		}
	}
	return pulls, nil
}

// Snapshot implements forge.Reader.
func (r *Reader) Snapshot(ctx context.Context, number int, expectedHead string) (forge.Snapshot, error) {
	pull, err := r.PullRequest(ctx, number)
	if err != nil {
		return forge.Snapshot{}, err
	}
	if expectedHead != "" && pull.HeadSHA != expectedHead {
		return forge.Snapshot{}, fmt.Errorf("%w: pull request head moved from %s to %s",
			forge.ErrConflict, expectedHead, pull.HeadSHA)
	}
	head := pull.HeadSHA
	commits, err := paginate(func(options *github.ListOptions) ([]*github.RepositoryCommit, *github.Response, error) {
		return r.client.PullRequests.ListCommits(ctx, r.owner, r.repo, number, options)
	})
	if err != nil {
		return forge.Snapshot{}, fmt.Errorf("list commits: %w", err)
	}
	comments, err := paginate(func(options *github.ListOptions) ([]*github.IssueComment, *github.Response, error) {
		return r.client.Issues.ListComments(ctx, r.owner, r.repo, number, &github.IssueListCommentsOptions{
			ListOptions: *options,
		})
	})
	if err != nil {
		return forge.Snapshot{}, fmt.Errorf("list comments: %w", err)
	}
	events, err := paginate(func(options *github.ListOptions) ([]*github.IssueEvent, *github.Response, error) {
		return r.client.Issues.ListIssueEvents(ctx, r.owner, r.repo, number, options)
	})
	if err != nil {
		return forge.Snapshot{}, fmt.Errorf("list issue events: %w", err)
	}
	statuses, err := paginate(func(options *github.ListOptions) ([]*github.RepoStatus, *github.Response, error) {
		return r.client.Repositories.ListStatuses(ctx, r.owner, r.repo, head, options)
	})
	if err != nil {
		return forge.Snapshot{}, fmt.Errorf("list statuses: %w", err)
	}
	checks, err := paginate(func(options *github.ListOptions) ([]*github.CheckRun, *github.Response, error) {
		latest := "latest"
		result, response, err := r.client.Checks.ListCheckRunsForRef(ctx, r.owner, r.repo, head,
			&github.ListCheckRunsOptions{Filter: &latest, ListOptions: *options})
		if err != nil {
			return nil, nil, err
		}
		return result.CheckRuns, response, nil
	})
	if err != nil {
		return forge.Snapshot{}, fmt.Errorf("list check runs: %w", err)
	}
	latest, err := r.PullRequest(ctx, number)
	if err != nil {
		return forge.Snapshot{}, err
	}
	if latest.HeadSHA != head {
		return forge.Snapshot{}, fmt.Errorf("%w: pull request head changed while fetching evidence",
			forge.ErrEvidenceIncomplete)
	}
	snapshot := forge.Snapshot{Pull: latest}
	for _, commit := range commits {
		snapshot.Commits = append(snapshot.Commits, commit.GetSHA())
	}
	for _, comment := range comments {
		snapshot.Comments = append(snapshot.Comments, forge.Comment{
			ID: int(comment.GetID()), Author: comment.GetUser().GetLogin(), Body: comment.GetBody(),
			CreatedAt: timestamp(comment.CreatedAt), UpdatedAt: timestamp(comment.UpdatedAt),
		})
	}
	for _, event := range events {
		if event.GetLabel().GetName() != holdLabel {
			continue
		}
		if event.GetEvent() != "labeled" && event.GetEvent() != "unlabeled" {
			continue
		}
		snapshot.HoldEvents = append(snapshot.HoldEvents, forge.HoldEvent{
			ID: int(event.GetID()), Event: event.GetEvent(), Actor: event.GetActor().GetLogin(),
			CreatedAt: timestamp(event.CreatedAt),
		})
	}
	for _, status := range statuses {
		snapshot.Statuses = append(snapshot.Statuses, forge.Status{
			ID: int(status.GetID()), Context: status.GetContext(), State: status.GetState(),
			CreatedAt: timestamp(status.CreatedAt), TargetURL: status.GetTargetURL(),
		})
	}
	for _, check := range checks {
		snapshot.Checks = append(snapshot.Checks, forge.Check{
			ID: int(check.GetID()), Name: check.GetName(), HeadSHA: check.GetHeadSHA(),
			Status: check.GetStatus(), Conclusion: check.GetConclusion(), DetailsURL: check.GetDetailsURL(),
		})
	}
	return snapshot, nil
}

// BranchHead implements forge.Reader.
func (r *Reader) BranchHead(ctx context.Context, branch string) (string, bool, error) {
	detail, response, err := r.client.Repositories.GetBranch(ctx, r.owner, r.repo, branch, 1)
	if err != nil {
		if response != nil && response.StatusCode == http.StatusNotFound {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read branch %s: %w", branch, err)
	}
	sha := detail.GetCommit().GetSHA()
	if sha == "" {
		return "", false, fmt.Errorf("%w: branch %s returned no head", forge.ErrEvidenceIncomplete, branch)
	}
	return sha, true, nil
}

// Runs one collection query over every page.
func paginate[T any](fetch func(*github.ListOptions) ([]T, *github.Response, error)) ([]T, error) {
	var all []T
	options := &github.ListOptions{PerPage: 100}
	for {
		items, response, err := fetch(options)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
		if response == nil || response.NextPage == 0 {
			return all, nil
		}
		options.Page = response.NextPage
	}
}

// Formats an API timestamp as RFC 3339, or empty when absent.
func timestamp(value *github.Timestamp) string {
	if value == nil || value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}
