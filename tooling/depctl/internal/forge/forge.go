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

// Package forge defines the pull-request evidence and mutation boundary.
//
// Reading and writing are separate interfaces so a candidate or planning stage
// can be constructed without any write capability at all.
package forge

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

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

// URL returns the canonical proposal URL for a repository.
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

// HoldEvent is one hold label transition.
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

// Snapshot is the complete evidence set for one assessed pull request.
type Snapshot struct {
	Pull       PullRequest `json:"pull"`
	Commits    []string    `json:"commits"`
	Comments   []Comment   `json:"comments"`
	HoldEvents []HoldEvent `json:"holdEvents"`
	Statuses   []Status    `json:"statuses"`
	Checks     []Check     `json:"checks"`
}

// CreateRequest is a proposal publication request.
type CreateRequest struct {
	Branch string
	Base   string
	Commit string
	Title  string
	Body   string
	Labels []string
}

// Receipt records what a writer believes it did; it never counts as authority.
type Receipt struct {
	Number   int
	URL      string
	Author   string
	HeadRepo string
	Base     string
	HeadSHA  string
}

// Reader reads complete, paginated remote evidence.
type Reader interface {
	// PullRequest returns one proposal.
	PullRequest(ctx context.Context, number int) (PullRequest, error)
	// Snapshot returns complete evidence, refusing a head that moved mid-read.
	Snapshot(ctx context.Context, number int, expectedHead string) (Snapshot, error)
	// OpenPullRequests returns every open proposal by author across all pages.
	OpenPullRequests(ctx context.Context, author string) ([]PullRequest, error)
	// BranchHead returns the current head of a branch; exists is false when absent.
	BranchHead(ctx context.Context, branch string) (string, bool, error)
}

// Writer performs enumerated mutations. It is injected only into a stage that
// is authorized for them.
type Writer interface {
	CreateBranch(ctx context.Context, branch, baseCommit string) error
	CreatePullRequest(ctx context.Context, request CreateRequest) (Receipt, error)
	Comment(ctx context.Context, number int, body string) error
}

// ErrEvidenceIncomplete reports missing or ambiguous remote evidence.
var ErrEvidenceIncomplete = errors.New("remote evidence is incomplete")

// ErrConflict reports a precondition that no longer holds.
var ErrConflict = errors.New("remote state conflicts with the expected state")

// Mutation is one recorded write attempt.
type Mutation struct {
	Operation  string
	Branch     string
	Number     int
	Expected   string
	Successful bool
	Note       string
}

// Fake is an in-memory forge used by scenarios and unit tests.
type Fake struct {
	mutex      sync.Mutex
	pulls      map[int]PullRequest
	snapshots  map[int]Snapshot
	order      []int
	pageSize   int
	failures   map[string]int
	mutations  []Mutation
	comments   map[int][]Comment
	branches   map[string]string
	nextNumber int
}

// NewFake returns an empty fake forge.
func NewFake() *Fake {
	return &Fake{
		pulls:      map[int]PullRequest{},
		snapshots:  map[int]Snapshot{},
		failures:   map[string]int{},
		comments:   map[int][]Comment{},
		branches:   map[string]string{},
		pageSize:   30,
		nextNumber: 1000,
	}
}

// SetPageSize simulates a paginated API.
func (f *Fake) SetPageSize(size int) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	if size > 0 {
		f.pageSize = size
	}
}

// FailOn makes the given operation fail on its next occurrence.
func (f *Fake) FailOn(operation string) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.failures[operation]++
}

// Seed adds a proposal and its snapshot.
func (f *Fake) Seed(pull PullRequest, snapshot Snapshot) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.pulls[pull.Number] = pull
	snapshot.Pull = pull
	f.snapshots[pull.Number] = snapshot
	f.order = append(f.order, pull.Number)
}

// UpdatePull replaces the proposal view, keeping its evidence.
func (f *Fake) UpdatePull(pull PullRequest) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.pulls[pull.Number] = pull
	snapshot := f.snapshots[pull.Number]
	snapshot.Pull = pull
	f.snapshots[pull.Number] = snapshot
}

// Mutations returns a copy of the recorded write attempts.
func (f *Fake) Mutations() []Mutation {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return append([]Mutation{}, f.mutations...)
}

// Comments returns the comments posted to a proposal.
func (f *Fake) Comments(number int) []Comment {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return append([]Comment{}, f.comments[number]...)
}

// Consumes a queued injected failure for an operation.
func (f *Fake) consumeFailure(operation string) bool {
	if f.failures[operation] > 0 {
		f.failures[operation]--
		return true
	}
	return false
}

// PullRequest implements Reader.
func (f *Fake) PullRequest(_ context.Context, number int) (PullRequest, error) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	pull, ok := f.pulls[number]
	if !ok {
		return PullRequest{}, fmt.Errorf("%w: pull request #%d is unknown", ErrEvidenceIncomplete, number)
	}
	return pull, nil
}

// Snapshot implements Reader.
func (f *Fake) Snapshot(_ context.Context, number int, expectedHead string) (Snapshot, error) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	snapshot, ok := f.snapshots[number]
	if !ok {
		return Snapshot{}, fmt.Errorf("%w: pull request #%d has no evidence", ErrEvidenceIncomplete, number)
	}
	if f.consumeFailure("snapshot") {
		return Snapshot{}, fmt.Errorf("%w: snapshot read failed", ErrEvidenceIncomplete)
	}
	if expectedHead != "" && snapshot.Pull.HeadSHA != expectedHead {
		return Snapshot{}, fmt.Errorf("%w: pull request head moved from %s to %s",
			ErrConflict, expectedHead, snapshot.Pull.HeadSHA)
	}
	snapshot.Comments = append(snapshot.Comments, f.comments[number]...)
	sort.SliceStable(snapshot.Comments, func(left, right int) bool {
		return snapshot.Comments[left].CreatedAt < snapshot.Comments[right].CreatedAt
	})
	return snapshot, nil
}

// OpenPullRequests implements Reader with simulated pagination.
func (f *Fake) OpenPullRequests(_ context.Context, author string) ([]PullRequest, error) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	if f.consumeFailure("list") {
		return nil, fmt.Errorf("%w: pagination truncated", ErrEvidenceIncomplete)
	}
	var all []PullRequest
	for _, number := range f.order {
		pull := f.pulls[number]
		if pull.Author == author && pull.State == "open" {
			all = append(all, pull)
		}
	}
	if f.pageSize > 0 && len(all) > f.pageSize {
		// Exercise the paginated path without losing entries.
		var collected []PullRequest
		for start := 0; start < len(all); start += f.pageSize {
			end := start + f.pageSize
			if end > len(all) {
				end = len(all)
			}
			collected = append(collected, all[start:end]...)
		}
		return collected, nil
	}
	return all, nil
}

// CreateBranch implements Writer.
func (f *Fake) CreateBranch(_ context.Context, branch, baseCommit string) error {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	if f.consumeFailure("create-branch") {
		f.branches[branch] = baseCommit
		f.mutations = append(f.mutations, Mutation{Operation: "create-branch", Branch: branch, Expected: baseCommit})
		return fmt.Errorf("%w: branch creation response lost", ErrConflict)
	}
	if _, exists := f.branches[branch]; exists {
		return fmt.Errorf("%w: branch %s already exists", ErrConflict, branch)
	}
	f.branches[branch] = baseCommit
	f.mutations = append(f.mutations, Mutation{Operation: "create-branch", Branch: branch, Expected: baseCommit, Successful: true})
	return nil
}

// BranchHead implements Reader.
func (f *Fake) BranchHead(_ context.Context, branch string) (string, bool, error) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	if f.consumeFailure("branch") {
		return "", false, fmt.Errorf("%w: branch read failed", ErrEvidenceIncomplete)
	}
	head, exists := f.branches[branch]
	return head, exists, nil
}

// CreatePullRequest implements Writer.
func (f *Fake) CreatePullRequest(_ context.Context, request CreateRequest) (Receipt, error) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	if f.consumeFailure("create-pull-request") {
		f.mutations = append(f.mutations, Mutation{Operation: "create-pull-request", Branch: request.Branch, Expected: request.Base})
		return Receipt{}, fmt.Errorf("%w: pull-request creation response lost", ErrConflict)
	}
	f.nextNumber++
	number := f.nextNumber
	pull := PullRequest{
		Number: number, Author: "aro-hcp-robot[bot]", State: "open",
		Title: request.Title, Body: request.Body, Labels: request.Labels,
		HeadRef: request.Branch, HeadSHA: request.Commit, HeadRepo: "Azure/ARO-HCP",
		BaseRef: request.Base, BaseRepo: "Azure/ARO-HCP",
	}
	f.pulls[number] = pull
	f.snapshots[number] = Snapshot{Pull: pull}
	f.order = append(f.order, number)
	f.mutations = append(f.mutations, Mutation{
		Operation: "create-pull-request", Branch: request.Branch, Number: number,
		Expected: request.Base, Successful: true,
	})
	return Receipt{
		Number: number, URL: fmt.Sprintf("https://example.invalid/Azure/ARO-HCP/pull/%d", number),
		Author: pull.Author, HeadRepo: pull.HeadRepo, Base: pull.BaseRef, HeadSHA: pull.HeadSHA,
	}, nil
}

// Comment implements Writer.
func (f *Fake) Comment(_ context.Context, number int, body string) error {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	if f.consumeFailure("comment") {
		f.mutations = append(f.mutations, Mutation{Operation: "comment", Number: number})
		return fmt.Errorf("%w: comment response lost", ErrConflict)
	}
	f.comments[number] = append(f.comments[number], Comment{
		ID: len(f.comments[number]) + 1, Author: "aro-hcp-robot[bot]", Body: body,
		CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z",
	})
	f.mutations = append(f.mutations, Mutation{Operation: "comment", Number: number, Successful: true, Note: strings.SplitN(body, "\n", 2)[0]})
	return nil
}
