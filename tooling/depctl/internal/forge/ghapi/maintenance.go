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

package ghapi

import (
	"context"
	"errors"
	"os"

	"github.com/google/go-github/v69/github"
)

// NewMaintenance separates repository identity reads/writes from CI reads.
// GH_TOKEN is deliberately mandatory; CI_TOKEN never authorizes comments.
func NewMaintenance(repository string) (*Reader, error) {
	reader, err := NewWithToken(repository, os.Getenv("GH_TOKEN"))
	if err != nil {
		return nil, err
	}
	ci, err := NewWithToken(repository, os.Getenv("CI_TOKEN"))
	if err != nil {
		return nil, err
	}
	reader.ciClient = ci.client
	reader.strictMaintenance = true
	return reader, nil
}

// PostMaintenanceComment writes one already-authorized fixed-command comment
// and validates the receipt. An uncertain response must never be retried blindly.
func (r *Reader) PostMaintenanceComment(ctx context.Context, number int, body string) (int, error) {
	if number <= 0 {
		return 0, errors.New("invalid pull request number")
	}
	comment, _, err := r.client.Issues.CreateComment(ctx, r.owner, r.repo, number, &github.IssueComment{Body: &body})
	if err != nil {
		return 0, err
	}
	if comment == nil || comment.GetID() <= 0 || comment.GetBody() != body {
		return 0, errors.New("hold cancellation response is incomplete; inspect the PR before retrying")
	}
	return int(comment.GetID()), nil
}
