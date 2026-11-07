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

// Package ghcli reads pull-request evidence with the gh CLI.
//
// It is a read-only adapter: no method here mutates a repository, branch, pull
// request or comment. It is constructed only by commands that request remote
// read evidence.
package ghcli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/forge"
)

// Reader reads evidence through gh.
type Reader struct {
	// Repository is OWNER/REPO.
	Repository string
	// Timeout bounds one API call.
	Timeout time.Duration
}

// New returns a reader for a repository.
func New(repository string) *Reader {
	return &Reader{Repository: repository, Timeout: 2 * time.Minute}
}

// Runs one paginated read-only API query.
func (r *Reader) query(ctx context.Context, endpoint string, target any) error {
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	runContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var stdout, stderr bytes.Buffer
	command := exec.CommandContext(runContext, "gh", "api", "--paginate", "--slurp", endpoint)
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("gh api %s: %w: %s", endpoint, err, strings.TrimSpace(stderr.String()))
	}
	return decodePages(stdout.Bytes(), target)
}

// Flattens the --slurp array-of-pages form.
func decodePages(content []byte, target any) error {
	trimmed := bytes.TrimSpace(content)
	if len(trimmed) == 0 {
		return fmt.Errorf("%w: empty API response", forge.ErrEvidenceIncomplete)
	}
	if trimmed[0] == '[' && json.Valid(trimmed) {
		var pages []json.RawMessage
		if err := json.Unmarshal(trimmed, &pages); err == nil {
			if len(pages) > 0 && len(pages[0]) > 0 && pages[0][0] == '[' {
				var flat []json.RawMessage
				for _, page := range pages {
					var items []json.RawMessage
					if err := json.Unmarshal(page, &items); err != nil {
						return fmt.Errorf("%w: paginated response is malformed", forge.ErrEvidenceIncomplete)
					}
					flat = append(flat, items...)
				}
				combined, err := json.Marshal(flat)
				if err != nil {
					return err
				}
				return json.Unmarshal(combined, target)
			}
		}
	}
	return json.Unmarshal(trimmed, target)
}

type apiPull struct {
	Number int    `json:"number"`
	State  string `json:"state"`
	Draft  bool   `json:"draft"`
	Merged bool   `json:"merged"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	User   struct {
		Login string `json:"login"`
	} `json:"user"`
	Head struct {
		SHA  string `json:"sha"`
		Ref  string `json:"ref"`
		Repo struct {
			FullName string `json:"full_name"`
		} `json:"repo"`
	} `json:"head"`
	Base struct {
		Ref  string `json:"ref"`
		Repo struct {
			FullName string `json:"full_name"`
		} `json:"repo"`
	} `json:"base"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

// Converts an API record into the forge view.
func (p apiPull) convert() forge.PullRequest {
	pull := forge.PullRequest{
		Number: p.Number, Author: p.User.Login, State: p.State, Draft: p.Draft, Merged: p.Merged,
		Title: p.Title, Body: p.Body, HeadRef: p.Head.Ref, HeadSHA: p.Head.SHA,
		HeadRepo: p.Head.Repo.FullName, BaseRef: p.Base.Ref, BaseRepo: p.Base.Repo.FullName,
	}
	for _, label := range p.Labels {
		pull.Labels = append(pull.Labels, label.Name)
	}
	return pull
}

// PullRequest implements forge.Reader.
func (r *Reader) PullRequest(ctx context.Context, number int) (forge.PullRequest, error) {
	var pulls []apiPull
	if err := r.query(ctx, fmt.Sprintf("repos/%s/pulls/%d", r.Repository, number), &pulls); err != nil {
		return forge.PullRequest{}, err
	}
	if len(pulls) != 1 {
		return forge.PullRequest{}, fmt.Errorf("%w: pull request %d returned %d records", forge.ErrEvidenceIncomplete, number, len(pulls))
	}
	return pulls[0].convert(), nil
}

// OpenPullRequests implements forge.Reader.
func (r *Reader) OpenPullRequests(ctx context.Context, author string) ([]forge.PullRequest, error) {
	var issues []struct {
		Number int    `json:"number"`
		State  string `json:"state"`
		Title  string `json:"title"`
		Body   string `json:"body"`
		User   struct {
			Login string `json:"login"`
		} `json:"user"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
		PullRequest *struct{} `json:"pull_request"`
	}
	query := fmt.Sprintf("repos/%s/issues?state=open&creator=%s&per_page=100",
		r.Repository, strings.ReplaceAll(author, "[bot]", "%5Bbot%5D"))
	if err := r.query(ctx, query, &issues); err != nil {
		return nil, err
	}
	var pulls []forge.PullRequest
	seen := map[int]bool{}
	for _, issue := range issues {
		if issue.PullRequest == nil || issue.User.Login != author {
			continue
		}
		if seen[issue.Number] {
			return nil, fmt.Errorf("%w: duplicate pull request %d in paginated inventory", forge.ErrEvidenceIncomplete, issue.Number)
		}
		seen[issue.Number] = true
		pull, err := r.PullRequest(ctx, issue.Number)
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
		return forge.Snapshot{}, fmt.Errorf("%w: pull request head moved from %s to %s", forge.ErrConflict, expectedHead, pull.HeadSHA)
	}
	head := pull.HeadSHA
	var commits []struct {
		SHA string `json:"sha"`
	}
	if err := r.query(ctx, fmt.Sprintf("repos/%s/pulls/%d/commits?per_page=100", r.Repository, number), &commits); err != nil {
		return forge.Snapshot{}, err
	}
	var comments []struct {
		ID   int    `json:"id"`
		Body string `json:"body"`
		User struct {
			Login string `json:"login"`
		} `json:"user"`
		CreatedAt string `json:"created_at"`
		UpdatedAt string `json:"updated_at"`
	}
	if err := r.query(ctx, fmt.Sprintf("repos/%s/issues/%d/comments?per_page=100", r.Repository, number), &comments); err != nil {
		return forge.Snapshot{}, err
	}
	var events []struct {
		ID    int    `json:"id"`
		Event string `json:"event"`
		Label struct {
			Name string `json:"name"`
		} `json:"label"`
		Actor struct {
			Login string `json:"login"`
		} `json:"actor"`
		CreatedAt string `json:"created_at"`
	}
	if err := r.query(ctx, fmt.Sprintf("repos/%s/issues/%d/events?per_page=100", r.Repository, number), &events); err != nil {
		return forge.Snapshot{}, err
	}
	var statuses []struct {
		ID        int    `json:"id"`
		Context   string `json:"context"`
		State     string `json:"state"`
		TargetURL string `json:"target_url"`
		CreatedAt string `json:"created_at"`
	}
	if err := r.query(ctx, fmt.Sprintf("repos/%s/commits/%s/statuses?per_page=100", r.Repository, head), &statuses); err != nil {
		return forge.Snapshot{}, err
	}
	var checks struct {
		CheckRuns []struct {
			ID         int    `json:"id"`
			Name       string `json:"name"`
			HeadSHA    string `json:"head_sha"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			DetailsURL string `json:"details_url"`
		} `json:"check_runs"`
	}
	if err := r.query(ctx, fmt.Sprintf("repos/%s/commits/%s/check-runs?per_page=100&filter=latest", r.Repository, head), &checks); err != nil {
		return forge.Snapshot{}, err
	}
	latest, err := r.PullRequest(ctx, number)
	if err != nil {
		return forge.Snapshot{}, err
	}
	if latest.HeadSHA != head {
		return forge.Snapshot{}, fmt.Errorf("%w: pull request head changed while fetching evidence", forge.ErrEvidenceIncomplete)
	}
	snapshot := forge.Snapshot{Pull: latest}
	for _, commit := range commits {
		snapshot.Commits = append(snapshot.Commits, commit.SHA)
	}
	for _, comment := range comments {
		snapshot.Comments = append(snapshot.Comments, forge.Comment{
			ID: comment.ID, Author: comment.User.Login, Body: comment.Body,
			CreatedAt: comment.CreatedAt, UpdatedAt: comment.UpdatedAt,
		})
	}
	for _, event := range events {
		if event.Label.Name != "do-not-merge/hold" || (event.Event != "labeled" && event.Event != "unlabeled") {
			continue
		}
		snapshot.HoldEvents = append(snapshot.HoldEvents, forge.HoldEvent{
			ID: event.ID, Event: event.Event, Actor: event.Actor.Login, CreatedAt: event.CreatedAt,
		})
	}
	for _, status := range statuses {
		snapshot.Statuses = append(snapshot.Statuses, forge.Status{
			ID: status.ID, Context: status.Context, State: status.State,
			CreatedAt: status.CreatedAt, TargetURL: status.TargetURL,
		})
	}
	for _, check := range checks.CheckRuns {
		snapshot.Checks = append(snapshot.Checks, forge.Check{
			ID: check.ID, Name: check.Name, HeadSHA: check.HeadSHA, Status: check.Status,
			Conclusion: check.Conclusion, DetailsURL: check.DetailsURL,
		})
	}
	return snapshot, nil
}

// BranchHead implements forge.Reader.
func (r *Reader) BranchHead(ctx context.Context, branch string) (string, bool, error) {
	var branches []struct {
		Name   string `json:"name"`
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	err := r.query(ctx, fmt.Sprintf("repos/%s/branches/%s", r.Repository, branch), &branches)
	if err != nil {
		if strings.Contains(err.Error(), "404") {
			return "", false, nil
		}
		return "", false, err
	}
	if len(branches) != 1 {
		return "", false, fmt.Errorf("%w: branch %s returned %d records", forge.ErrEvidenceIncomplete, branch, len(branches))
	}
	return branches[0].Commit.SHA, true, nil
}

// EnvironmentToken reports whether a gh token is present, without exposing it.
func EnvironmentToken() bool {
	for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if os.Getenv(name) != "" {
			return true
		}
	}
	return false
}
