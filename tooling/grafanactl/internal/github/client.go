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

// Package github holds the GitHub operations the dashboard preview needs:
// keeping one marked comment up to date on a pull request.
package github

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/go-logr/logr"
	gh "github.com/google/go-github/v69/github"
)

const (
	// commentPageSize is the largest page GitHub serves for issue comments.
	commentPageSize = 100
	// botAuthorType is the account type GitHub reports for a comment that an
	// app wrote. A person's account reports "User", which no one can forge.
	botAuthorType = "Bot"
)

// Comment is one comment on a pull request.
type Comment struct {
	ID     int64
	Body   string
	Author string
	// AuthorType is the account type of the author: "Bot" for an app, "User"
	// for a person.
	AuthorType string
}

// Client reads and writes the comments of one repository.
type Client struct {
	api   *gh.Client
	owner string
	repo  string
}

// NewClient builds a client that authenticates with the given token.
func NewClient(token, owner, repo string) (*Client, error) {
	if token == "" {
		return nil, fmt.Errorf("github token is required")
	}
	return NewClientForAPI(gh.NewClient(nil).WithAuthToken(token), owner, repo)
}

// NewClientForAPI builds a client over a GitHub client the caller supplies,
// which lets a test point it at a local server.
func NewClientForAPI(api *gh.Client, owner, repo string) (*Client, error) {
	if api == nil {
		return nil, fmt.Errorf("github API client is required")
	}
	if owner == "" || repo == "" {
		return nil, fmt.Errorf("repository owner and name are required")
	}
	return &Client{api: api, owner: owner, repo: repo}, nil
}

// ListComments reads every comment on a pull request. GitHub serves them one
// page at a time and names the next page in the Link header, which the client
// reports as NextPage.
func (c *Client) ListComments(ctx context.Context, number int) ([]Comment, error) {
	options := &gh.IssueListCommentsOptions{
		ListOptions: gh.ListOptions{PerPage: commentPageSize},
	}

	var comments []Comment
	for {
		page, response, err := c.api.Issues.ListComments(ctx, c.owner, c.repo, number, options)
		if err != nil {
			return nil, fmt.Errorf("failed to list comments on pull request %d: %w", number, err)
		}
		for _, comment := range page {
			comments = append(comments, buildComment(comment))
		}
		if response == nil || response.NextPage == 0 {
			return comments, nil
		}
		options.Page = response.NextPage
	}
}

// CreateComment adds a comment to a pull request.
func (c *Client) CreateComment(ctx context.Context, number int, body string) (Comment, error) {
	created, _, err := c.api.Issues.CreateComment(ctx, c.owner, c.repo, number, &gh.IssueComment{Body: &body})
	if err != nil {
		return Comment{}, fmt.Errorf("failed to comment on pull request %d: %w", number, err)
	}
	return buildComment(created), nil
}

// UpdateComment replaces the body of one comment.
func (c *Client) UpdateComment(ctx context.Context, id int64, body string) (Comment, error) {
	updated, _, err := c.api.Issues.EditComment(ctx, c.owner, c.repo, id, &gh.IssueComment{Body: &body})
	if err != nil {
		return Comment{}, fmt.Errorf("failed to update comment %d: %w", id, err)
	}
	return buildComment(updated), nil
}

// buildComment converts one comment from the GitHub representation.
func buildComment(comment *gh.IssueComment) Comment {
	return Comment{
		ID:         comment.GetID(),
		Body:       comment.GetBody(),
		Author:     comment.GetUser().GetLogin(),
		AuthorType: comment.GetUser().GetType(),
	}
}

// DeleteComment removes one comment.
func (c *Client) DeleteComment(ctx context.Context, id int64) error {
	if _, err := c.api.Issues.DeleteComment(ctx, c.owner, c.repo, id); err != nil {
		return fmt.Errorf("failed to delete comment %d: %w", id, err)
	}
	return nil
}

// UpsertMarkedComment leaves a pull request with exactly one comment from an app
// that starts with marker: it updates the newest match, deletes the older
// matches, and adds a comment when nothing matches.
//
// A comment matches only when an app wrote it. The marker is a public constant,
// so anyone can open a comment with it. Without the author check, such a comment
// would take the place of the comment the last run left: this code would delete
// its own comment, and write the body into a comment that a person can edit
// afterwards.
func (c *Client) UpsertMarkedComment(ctx context.Context, number int, marker, body string) (Comment, error) {
	logger := logr.FromContextOrDiscard(ctx)

	if marker == "" {
		return Comment{}, fmt.Errorf("comment marker is required")
	}

	comments, err := c.ListComments(ctx, number)
	if err != nil {
		return Comment{}, err
	}

	var marked []Comment
	for _, comment := range comments {
		if comment.AuthorType == botAuthorType && strings.HasPrefix(comment.Body, marker) {
			marked = append(marked, comment)
		}
	}

	if len(marked) == 0 {
		created, err := c.CreateComment(ctx, number, body)
		if err != nil {
			return Comment{}, err
		}
		logger.Info("Posted new PR comment", "comment-id", created.ID)
		return created, nil
	}

	// GitHub issues comment IDs in ascending order, so the largest is the newest.
	slices.SortFunc(marked, func(a, b Comment) int { return cmp.Compare(a.ID, b.ID) })
	newest := marked[len(marked)-1]

	// Delete old marked comments (there should only be one)
	for _, stale := range marked[:len(marked)-1] {
		if err := c.DeleteComment(ctx, stale.ID); err != nil {
			return Comment{}, err
		}
		logger.Info("Deleted stale preview comment", "comment-id", stale.ID)
	}

	updated, err := c.UpdateComment(ctx, newest.ID, body)
	if err != nil {
		return Comment{}, err
	}
	logger.Info("Updated existing PR comment", "comment-id", updated.ID)
	return updated, nil
}
