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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
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
	client    *github.Client
	owner     string
	repo      string
	prefilter func(forge.PullRequest) bool
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

// NewWithPrefilter returns a reader that applies the filter to issue metadata
// before fetching pull-request details, so unrelated proposals are never read.
func NewWithPrefilter(repository string, prefilter func(forge.PullRequest) bool) (*Reader, error) {
	reader, err := New(repository)
	if err != nil {
		return nil, err
	}
	reader.prefilter = prefilter
	return reader, nil
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
	endpoint := fmt.Sprintf("repos/%s/%s/pulls/%d", r.owner, r.repo, number)
	request, err := r.client.NewRequest("GET", endpoint, nil)
	if err != nil {
		return forge.PullRequest{}, err
	}
	var raw json.RawMessage
	if _, err := r.client.Do(ctx, request, &raw); err != nil {
		return forge.PullRequest{}, fmt.Errorf("read pull request #%d: %w", number, err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return forge.PullRequest{}, fmt.Errorf("%w: pull request #%d is malformed",
			forge.ErrEvidenceIncomplete, number)
	}
	body, present := fields["body"]
	if !present {
		return forge.PullRequest{}, fmt.Errorf("%w: pull request #%d body is missing",
			forge.ErrEvidenceIncomplete, number)
	}
	var pull github.PullRequest
	if err := json.Unmarshal(raw, &pull); err != nil {
		return forge.PullRequest{}, fmt.Errorf("%w: pull request #%d is malformed",
			forge.ErrEvidenceIncomplete, number)
	}
	// An explicit null body is legitimate; normalize it so a marker check sees
	// an empty string instead of a nil pointer.
	if isNullJSON(body) {
		pull.Body = github.Ptr("")
	}
	if pull.GetNumber() != number {
		return forge.PullRequest{}, fmt.Errorf("%w: pull request %d returned %d",
			forge.ErrEvidenceIncomplete, number, pull.GetNumber())
	}
	// Missing detail metadata would otherwise filter an owned proposal out,
	// which must fail closed instead of allowing publication.
	if pull.Number == nil || pull.GetNumber() <= 0 ||
		pull.User == nil || pull.GetUser().GetLogin() == "" ||
		pull.State == nil || (pull.GetState() != "open" && pull.GetState() != "closed") ||
		pull.Base == nil || pull.Base.Ref == nil || pull.GetBase().GetRef() == "" ||
		pull.Title == nil || pull.Labels == nil {
		return forge.PullRequest{}, fmt.Errorf("%w: pull request #%d metadata is incomplete",
			forge.ErrEvidenceIncomplete, number)
	}
	// A label without a name would become an empty marker and silently exclude
	// an owned proposal, so reject it before conversion.
	for _, label := range pull.Labels {
		if label == nil || label.Name == nil {
			return forge.PullRequest{}, fmt.Errorf("%w: pull request #%d labels are incomplete",
				forge.ErrEvidenceIncomplete, number)
		}
	}
	return convertPull(&pull), nil
}

// Reports whether a raw JSON value is explicitly null.
func isNullJSON(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
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
	issues, err := r.listIssues(ctx, author)
	if err != nil {
		return nil, fmt.Errorf("list open pull requests by %s: %w", author, err)
	}
	var pulls []forge.PullRequest
	seen := map[int]bool{}
	for _, issue := range issues {
		// Fail closed on incomplete metadata instead of silently skipping an
		// issue whose ownership cannot be established.
		if issue == nil || issue.GetNumber() <= 0 || issue.GetUser().GetLogin() == "" ||
			(issue.GetState() != "open" && issue.GetState() != "closed") {
			return nil, fmt.Errorf("%w: incomplete issue metadata", forge.ErrEvidenceIncomplete)
		}
		// Duplicate detection runs before any filter: a repeated number can
		// signal a pagination shift that also drops an owned proposal.
		if seen[issue.GetNumber()] {
			return nil, fmt.Errorf("%w: duplicate pull request %d in paginated inventory",
				forge.ErrEvidenceIncomplete, issue.GetNumber())
		}
		seen[issue.GetNumber()] = true
		if issue.PullRequestLinks == nil || issue.GetUser().GetLogin() != author {
			continue
		}
		if issue.Title == nil || issue.Labels == nil || issue.Body == nil {
			return nil, fmt.Errorf("%w: issue %d metadata is incomplete", forge.ErrEvidenceIncomplete, issue.GetNumber())
		}
		for _, label := range issue.Labels {
			if label == nil || label.Name == nil {
				return nil, fmt.Errorf("%w: issue %d labels are incomplete", forge.ErrEvidenceIncomplete, issue.GetNumber())
			}
		}
		if r.prefilter != nil && !r.prefilter(issueProposal(issue)) {
			continue
		}
		number := issue.GetNumber()
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

// Lists issues while keeping the raw pull_request marker distinguishable from
// an absent one: typed decoding maps both null and absent to nil.
func (r *Reader) listIssues(ctx context.Context, author string) ([]*github.Issue, error) {
	var issues []*github.Issue
	options := &github.ListOptions{PerPage: 100}
	for {
		endpoint := fmt.Sprintf("repos/%s/%s/issues?state=open&creator=%s&per_page=%d&page=%d",
			r.owner, r.repo, url.QueryEscape(author), options.PerPage, options.Page)
		request, err := r.client.NewRequest("GET", endpoint, nil)
		if err != nil {
			return nil, err
		}
		var page []json.RawMessage
		response, err := r.client.Do(ctx, request, &page)
		if err != nil {
			return nil, err
		}
		// A JSON null body decodes to a nil slice and would otherwise look like
		// a complete, empty inventory.
		if page == nil {
			return nil, fmt.Errorf("%w: issue page is not a list", forge.ErrEvidenceIncomplete)
		}
		for index, raw := range page {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
				return nil, fmt.Errorf("%w: issue %d is not an object", forge.ErrEvidenceIncomplete, index)
			}
			marker, present := fields["pull_request"]
			if present {
				trimmed := bytes.TrimSpace(marker)
				if bytes.Equal(trimmed, []byte("null")) || len(trimmed) == 0 || trimmed[0] != '{' {
					return nil, fmt.Errorf("%w: issue %d has a malformed pull_request marker",
						forge.ErrEvidenceIncomplete, index)
				}
			}
			body, hasBody := fields["body"]
			if !hasBody {
				return nil, fmt.Errorf("%w: issue %d body is missing", forge.ErrEvidenceIncomplete, index)
			}
			var issue github.Issue
			if err := json.Unmarshal(raw, &issue); err != nil {
				return nil, fmt.Errorf("%w: issue %d is malformed", forge.ErrEvidenceIncomplete, index)
			}
			if isNullJSON(body) {
				issue.Body = github.Ptr("")
			}
			issues = append(issues, &issue)
		}
		if response == nil || response.NextPage == 0 {
			return issues, nil
		}
		options.Page = response.NextPage
	}
}

// Converts issue-level metadata into the proposal view used for prefiltering.
func issueProposal(issue *github.Issue) forge.PullRequest {
	proposal := forge.PullRequest{
		Number: issue.GetNumber(), Author: issue.GetUser().GetLogin(), State: issue.GetState(),
		Title: issue.GetTitle(), Body: issue.GetBody(),
	}
	for _, label := range issue.Labels {
		proposal.Labels = append(proposal.Labels, label.GetName())
	}
	return proposal
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
