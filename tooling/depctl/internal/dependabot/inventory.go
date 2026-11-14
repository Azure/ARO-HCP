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

// Package dependabot implements the security remediation policy shared by the
// dependabot commands.
package dependabot

import (
	"errors"
	"fmt"
	"regexp"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/forge"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/policy"
)

// ErrInventory reports unusable or duplicate security proposal metadata.
var ErrInventory = errors.New("security pull-request inventory is incomplete")

// Record is the identity projection the remediation agent reads.
type Record struct {
	Number   int      `json:"number"`
	Title    string   `json:"title"`
	Body     string   `json:"body"`
	Head     string   `json:"head"`
	HeadRepo string   `json:"head_repo"`
	Base     string   `json:"base"`
	SHA      string   `json:"sha"`
	Draft    bool     `json:"draft"`
	Author   string   `json:"author"`
	Labels   []string `json:"labels"`
}

// commit matches a full lowercase commit SHA.
var commit = regexp.MustCompile(`^[0-9a-f]{40}$`)

// OwnedSecurityProposals returns the open security proposals this profile owns.
func OwnedSecurityProposals(pulls []forge.PullRequest, repository string) ([]Record, error) {
	records := make([]Record, 0, len(pulls))
	seen := map[int]bool{}
	for _, pull := range pulls {
		if pull.Author != policy.Robot || pull.State != "open" {
			continue
		}
		if !policy.IsSecurityProposal(policy.PullRequest{
			Number: pull.Number, Author: pull.Author, State: pull.State,
			Title: pull.Title, Body: pull.Body, Labels: pull.Labels,
		}) {
			continue
		}
		if pull.BaseRef != policy.ToolBase {
			continue
		}
		if seen[pull.Number] {
			return nil, fmt.Errorf("%w: duplicate pull request %d", ErrInventory, pull.Number)
		}
		if pull.HeadRef == "" || !commit.MatchString(pull.HeadSHA) ||
			pull.HeadRepo != repository || pull.BaseRepo != repository {
			return nil, fmt.Errorf("%w: pull request %d has an incomplete or foreign head/base",
				ErrInventory, pull.Number)
		}
		seen[pull.Number] = true
		records = append(records, Record{
			Number: pull.Number, Title: pull.Title, Body: pull.Body,
			Head: pull.HeadRef, HeadRepo: pull.HeadRepo, Base: pull.BaseRef,
			SHA: pull.HeadSHA, Draft: pull.Draft, Author: pull.Author,
			Labels: append([]string{}, pull.Labels...),
		})
	}
	return records, nil
}
