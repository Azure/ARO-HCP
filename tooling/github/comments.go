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
	"fmt"
	"strings"

	gh "github.com/google/go-github/v69/github"
)

// CommentMarker identifies an idempotency key on exactly one commit; invalid inputs return an empty marker.
func CommentMarker(key, head string) string {
	if !keyPattern.MatchString(key) || !shaPattern.MatchString(head) {
		return ""
	}
	return "<!-- aro-hcp-github:" + key + ":" + head + " -->"
}

// PostComment posts once after a complete trusted-author deduplication read and verifies the returned comment by ID.
// Fresh reads require the pull to remain open, unmerged, and bound to its original author, repositories, refs, and head.
// A failed write or readback is indeterminate: callers must reconcile before attempting another write.
// GitHub provides no atomic state- or head-conditioned comment write; a change first observed after writing is indeterminate.
func (c *Client) PostComment(ctx context.Context, number int, expectedHead, key, body string) (Receipt, error) {
	receipt := Receipt{Number: number, RequestedHead: expectedHead, Outcome: "not-applicable"}
	marker := CommentMarker(key, expectedHead)
	if marker == "" || strings.TrimSpace(body) == "" || strings.Contains(body, "<!-- aro-hcp-github:") {
		return receipt, fmt.Errorf("invalid comment key, head, or body")
	}
	pull, err := c.boundPull(ctx, number, expectedHead)
	if err != nil {
		return receipt, err
	}
	if pull.State != "open" || pull.Merged {
		return receipt, ErrConflict
	}
	receipt.URL = pull.URL(c.owner + "/" + c.repo)
	receipt.ObservedHead = pull.HeadSHA
	actor, err := c.Actor(ctx)
	if err != nil {
		return receipt, err
	}
	receipt.Author = actor
	full := body + "\n\n" + marker
	comments, err := c.comments(ctx, number)
	if err != nil {
		return receipt, err
	}
	var duplicate *gh.IssueComment
	for _, v := range comments {
		if v.GetUser().GetLogin() != actor || !strings.Contains(v.GetBody(), marker) {
			continue
		}
		if duplicate != nil || v.GetBody() != full {
			return receipt, ErrConflict
		}
		duplicate = v
	}
	if err = c.checkCommentPull(ctx, pull); err != nil {
		return receipt, err
	}
	if duplicate != nil {
		receipt.CommentID = int(duplicate.GetID())
		observed, _, readErr := c.api.Issues.GetComment(ctx, c.owner, c.repo, duplicate.GetID())
		if readErr != nil {
			return receipt, readErr
		}
		if observed == nil || observed.GetID() != duplicate.GetID() || observed.GetBody() != full || observed.GetUser().GetLogin() != actor {
			return receipt, ErrConflict
		}
		if err = c.checkCommentPull(ctx, pull); err != nil {
			return receipt, err
		}
		receipt.Outcome = "duplicate"
		return receipt, nil
	}
	receipt.Outcome = "requested"
	posted, _, err := c.writer.Issues.CreateComment(ctx, c.owner, c.repo, number, &gh.IssueComment{Body: &full})
	if err != nil || posted == nil || posted.GetID() <= 0 {
		receipt.Outcome = "indeterminate"
		return receipt, indeterminate("create comment", err)
	}
	receipt.CommentID = int(posted.GetID())
	observed, _, err := c.api.Issues.GetComment(ctx, c.owner, c.repo, posted.GetID())
	if err != nil || observed == nil || observed.GetID() != posted.GetID() || observed.GetBody() != full || observed.GetUser().GetLogin() != actor {
		receipt.Outcome = "indeterminate"
		return receipt, indeterminate("read back comment", err)
	}
	if err = c.checkCommentPull(ctx, pull); err != nil {
		receipt.Outcome = "indeterminate"
		return receipt, indeterminate("verify proposal after comment write", err)
	}
	receipt.Outcome = "observed"
	return receipt, nil
}

// CheckCommentPull revalidates the state and original identity without implying an atomic comment-write precondition.
func (c *Client) checkCommentPull(ctx context.Context, initial PullRequest) error {
	p, _, err := c.api.PullRequests.Get(ctx, c.owner, c.repo, initial.Number)
	if err != nil {
		return err
	}
	if p == nil || p.GetNumber() <= 0 || p.GetUser().GetLogin() == "" {
		return fmt.Errorf("missing comment proposal number or author: %w", ErrEvidenceIncomplete)
	}
	if p.State == nil || (p.GetState() != "open" && p.GetState() != "closed") || p.Merged == nil {
		return fmt.Errorf("missing comment proposal state: %w", ErrEvidenceIncomplete)
	}
	if !shaPattern.MatchString(p.GetHead().GetSHA()) || p.GetHead().GetRef() == "" || !repositoryPattern.MatchString(p.GetHead().GetRepo().GetFullName()) {
		return fmt.Errorf("incomplete comment proposal source: %w", ErrEvidenceIncomplete)
	}
	if p.GetBase().GetRef() == "" || !repositoryPattern.MatchString(p.GetBase().GetRepo().GetFullName()) {
		return fmt.Errorf("incomplete comment proposal target: %w", ErrEvidenceIncomplete)
	}
	if p.GetNumber() != initial.Number || p.GetUser().GetLogin() != initial.Author {
		return fmt.Errorf("comment proposal number or author changed: %w", ErrConflict)
	}
	if p.GetState() != "open" || p.GetMerged() {
		return fmt.Errorf("comment proposal is no longer open and unmerged: %w", ErrConflict)
	}
	if p.GetHead().GetSHA() != initial.HeadSHA || p.GetHead().GetRef() != initial.HeadRef || p.GetHead().GetRepo().GetFullName() != initial.HeadRepo {
		return fmt.Errorf("comment proposal source changed: %w", ErrConflict)
	}
	if p.GetBase().GetRef() != initial.BaseRef || p.GetBase().GetRepo().GetFullName() != initial.BaseRepo {
		return fmt.Errorf("comment proposal target changed: %w", ErrConflict)
	}
	return nil
}
