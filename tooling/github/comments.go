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
// A failed write or readback is indeterminate: callers must reconcile before attempting another write.
// GitHub provides no atomic head-conditioned comment write; a head move during the write is reported as indeterminate.
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
	if _, err = c.boundPull(ctx, number, expectedHead); err != nil {
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
		if _, err = c.boundPull(ctx, number, expectedHead); err != nil {
			return receipt, err
		}
		receipt.Outcome = "duplicate"
		return receipt, nil
	}
	receipt.Outcome = "requested"
	posted, _, err := c.writer.Issues.CreateComment(ctx, c.owner, c.repo, number, &gh.IssueComment{Body: &full})
	if err != nil || posted == nil || posted.GetID() <= 0 {
		receipt.Outcome = "indeterminate"
		return receipt, ErrIndeterminate
	}
	receipt.CommentID = int(posted.GetID())
	observed, _, err := c.api.Issues.GetComment(ctx, c.owner, c.repo, posted.GetID())
	if err != nil || observed == nil || observed.GetID() != posted.GetID() || observed.GetBody() != full || observed.GetUser().GetLogin() != actor {
		receipt.Outcome = "indeterminate"
		return receipt, ErrIndeterminate
	}
	latest, err := c.boundPull(ctx, number, expectedHead)
	if err != nil {
		receipt.Outcome = "indeterminate"
		return receipt, ErrIndeterminate
	}
	receipt.ObservedHead = latest.HeadSHA
	receipt.Outcome = "observed"
	return receipt, nil
}
