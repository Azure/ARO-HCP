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
	"errors"
	"fmt"
)

// ErrEvidenceIncomplete reports missing or ambiguous remote evidence.
var ErrEvidenceIncomplete = errors.New("remote evidence is incomplete")

// ErrConflict reports a failed remote precondition.
var ErrConflict = errors.New("remote state conflicts with expectation")

// ErrIndeterminate reports a write whose result could not be verified; callers must reconcile, not blindly retry.
var ErrIndeterminate = errors.New("remote write result is indeterminate")

// Indeterminate retains the operation and underlying failure alongside the uncertain-write classification.
func indeterminate(operation string, cause error) error {
	if cause != nil {
		return fmt.Errorf("%s: %w: %w", operation, ErrIndeterminate, cause)
	}
	return fmt.Errorf("%s: %w", operation, ErrIndeterminate)
}

// PullRequest is the identity and review state of one proposal.
type PullRequest struct {
	Number   int      `json:"number"`
	Author   string   `json:"author"`
	State    string   `json:"state"`
	Draft    bool     `json:"draft"`
	Merged   bool     `json:"merged"`
	Title    string   `json:"title"`
	Body     string   `json:"body"`
	Labels   []string `json:"labels"`
	HeadRef  string   `json:"headRef"`
	HeadSHA  string   `json:"headSHA"`
	HeadRepo string   `json:"headRepo"`
	BaseRef  string   `json:"baseRef"`
	BaseRepo string   `json:"baseRepo"`
}

// URL returns the canonical proposal URL.
func (p PullRequest) URL(repository string) string {
	return fmt.Sprintf("https://github.com/%s/pull/%d", repository, p.Number)
}

// Comment is one issue comment.
type Comment struct {
	ID        int    `json:"id"`
	Author    string `json:"author"`
	Body      string `json:"body"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// HoldEvent is one hold-label transition.
type HoldEvent struct {
	ID        int    `json:"id"`
	Event     string `json:"event"`
	Actor     string `json:"actor"`
	CreatedAt string `json:"createdAt"`
}

// Status is one commit status.
type Status struct {
	ID        int    `json:"id"`
	Context   string `json:"context"`
	State     string `json:"state"`
	CreatedAt string `json:"createdAt"`
	TargetURL string `json:"targetURL"`
}

// Check is one check run.
type Check struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	HeadSHA    string `json:"headSHA"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	DetailsURL string `json:"detailsURL"`
}

// CommitCI is the complete status history and latest check runs for one commit.
type CommitCI struct {
	Statuses []Status `json:"statuses"`
	Checks   []Check  `json:"checks"`
}

// Snapshot is a complete, head-bound evidence set.
type Snapshot struct {
	Pull       PullRequest `json:"pull"`
	Commits    []string    `json:"commits"`
	Comments   []Comment   `json:"comments"`
	HoldEvents []HoldEvent `json:"holdEvents"`
	Statuses   []Status    `json:"statuses"`
	Checks     []Check     `json:"checks"`
}

// Receipt describes verified observations, not authorization to act.
type Receipt struct {
	Number        int
	URL           string
	Author        string
	HeadRepo      string
	Base          string
	HeadSHA       string
	CommentID     int
	RequestedHead string
	ObservedHead  string
	Outcome       string
}
