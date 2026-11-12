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

// Package safeoutput validates and serializes agent safe-output proposals.
//
// Artifacts are the JSON handoff between an agentic workflow and the trusted
// publication step. A guard only rewrites that local artifact: it never creates
// a proposal, pushes a branch or comments on a pull request.
package safeoutput

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/forge"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/policy"
)

// ErrInvalidArtifact reports an artifact that must not be published.
var ErrInvalidArtifact = errors.New("invalid safe-output proposal inventory")

// CreateProposal is the item type that publishes one pull request.
const CreateProposal = "create_pull_request"

// Noop is the item type that reports deferred work without a mutation.
const Noop = "noop"

// Artifact is a parsed safe-output file with unknown fields preserved.
type Artifact struct {
	fields map[string]json.RawMessage
	items  []json.RawMessage
}

// Parse reads a safe-output artifact and checks its envelope.
func Parse(content []byte) (*Artifact, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(content, &fields); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidArtifact, err)
	}
	if fields == nil {
		return nil, fmt.Errorf("%w: artifact is not an object", ErrInvalidArtifact)
	}
	if raw, ok := fields["errors"]; ok && !isEmptyJSON(raw) {
		var reported []json.RawMessage
		if err := json.Unmarshal(raw, &reported); err != nil || len(reported) > 0 {
			return nil, fmt.Errorf("%w: agent output reported errors", ErrInvalidArtifact)
		}
	}
	raw, ok := fields["items"]
	if !ok {
		return nil, fmt.Errorf("%w: items are missing", ErrInvalidArtifact)
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil || items == nil {
		return nil, fmt.Errorf("%w: items is not a list", ErrInvalidArtifact)
	}
	for index, item := range items {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(item, &fields); err != nil || fields == nil {
			return nil, fmt.Errorf("%w: item %d is not an object", ErrInvalidArtifact, index)
		}
		var kind string
		if err := json.Unmarshal(fields["type"], &kind); err != nil || kind == "" {
			return nil, fmt.Errorf("%w: item %d has no type", ErrInvalidArtifact, index)
		}
	}
	return &Artifact{fields: fields, items: items}, nil
}

// Marshal renders the artifact with the current item list.
func (a *Artifact) Marshal() ([]byte, error) {
	encoded, err := json.Marshal(a.items)
	if err != nil {
		return nil, err
	}
	a.fields["items"] = encoded
	content, err := json.MarshalIndent(a.fields, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(content, '\n'), nil
}

// HasCreate reports whether the artifact would publish a proposal.
func (a *Artifact) HasCreate() bool {
	for _, item := range a.items {
		if itemType(item) == CreateProposal {
			return true
		}
	}
	return false
}

// Validate checks every create request before any remote read.
func (a *Artifact) Validate(repository string) error {
	for index, item := range a.items {
		if itemType(item) != CreateProposal {
			continue
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(item, &fields); err != nil {
			return fmt.Errorf("%w: item %d is malformed", ErrInvalidArtifact, index)
		}
		for _, name := range []string{"branch", "title", "body"} {
			raw, ok := fields[name]
			if !ok {
				return fmt.Errorf("%w: create_pull_request requires %s", ErrInvalidArtifact, name)
			}
			var value string
			if err := json.Unmarshal(raw, &value); err != nil || strings.TrimSpace(value) == "" {
				return fmt.Errorf("%w: create_pull_request requires %s", ErrInvalidArtifact, name)
			}
		}
		target := repository
		if raw, ok := fields["repo"]; ok {
			if isNull(raw) {
				return fmt.Errorf("%w: create_pull_request repo is null", ErrInvalidArtifact)
			}
			if err := json.Unmarshal(raw, &target); err != nil || strings.TrimSpace(target) == "" {
				return fmt.Errorf("%w: create_pull_request repo is malformed", ErrInvalidArtifact)
			}
		}
		if !strings.EqualFold(target, repository) {
			return fmt.Errorf("%w: tooling proposals must target the current repository", ErrInvalidArtifact)
		}
		base := policy.ToolBase
		if raw, ok := fields["base"]; ok {
			if isNull(raw) {
				return fmt.Errorf("%w: create_pull_request base is null", ErrInvalidArtifact)
			}
			if err := json.Unmarshal(raw, &base); err != nil {
				return fmt.Errorf("%w: create_pull_request base is malformed", ErrInvalidArtifact)
			}
		}
		if base != "" && base != policy.ToolBase {
			return fmt.Errorf("%w: tooling proposals must target %s", ErrInvalidArtifact, policy.ToolBase)
		}
	}
	return nil
}

// Defer replaces every create request with one noop summary.
func (a *Artifact) Defer(active []int) {
	var kept []json.RawMessage
	for _, item := range a.items {
		if itemType(item) == CreateProposal {
			continue
		}
		kept = append(kept, item)
	}
	a.items = kept
	if len(a.items) > 0 {
		return
	}
	message := "Waiting for existing tooling PR(s): " + joinNumbers(active)
	noop, err := json.Marshal(map[string]string{"type": Noop, "message": message})
	if err != nil {
		return
	}
	a.items = []json.RawMessage{noop}
}

// Result reports what the guard observed and whether it deferred work.
type Result struct {
	Active   []int
	Deferred bool
}

// Guard validates the artifact and defers it while an owned proposal is open.
func Guard(ctx context.Context, reader forge.Reader, repository string, artifact *Artifact) (Result, error) {
	if err := artifact.Validate(repository); err != nil {
		return Result{}, err
	}
	pulls, err := reader.OpenPullRequests(ctx, policy.ToolOwner)
	if err != nil {
		return Result{}, err
	}
	active := OwnedToolProposals(pulls)
	result := Result{Active: active}
	if len(active) == 0 || !artifact.HasCreate() {
		return result, nil
	}
	artifact.Defer(active)
	result.Deferred = true
	return result, nil
}

// ToolProposalFilter reports whether issue-level metadata already proves this
// profile's ownership and tooling markers, so unrelated robot proposals are
// never fetched in detail.
func ToolProposalFilter(pull forge.PullRequest) bool {
	if pull.Author != policy.ToolOwner || pull.State != "open" {
		return false
	}
	return policy.IsToolProposal(policy.PullRequest{
		Number: pull.Number, Author: pull.Author, State: pull.State,
		Title: pull.Title, Body: pull.Body, Labels: pull.Labels,
	})
}

// OwnedToolProposals returns the open proposals this profile owns.
func OwnedToolProposals(pulls []forge.PullRequest) []int {
	var numbers []int
	for _, pull := range pulls {
		if pull.Author != policy.ToolOwner || pull.State != "open" || pull.BaseRef != policy.ToolBase {
			continue
		}
		if policy.IsToolProposal(policy.PullRequest{
			Number: pull.Number, Author: pull.Author, State: pull.State, Base: pull.BaseRef,
			Title: pull.Title, Body: pull.Body, Labels: pull.Labels,
		}) {
			numbers = append(numbers, pull.Number)
		}
	}
	sort.Ints(numbers)
	return numbers
}

// WriteArtifact replaces the artifact atomically; a failed guard leaves it intact.
func WriteArtifact(path string, artifact *Artifact) error {
	content, err := artifact.Marshal()
	if err != nil {
		return err
	}
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, "depctl-guard-")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer func() {
		if temporaryName != "" {
			_ = os.Remove(temporaryName)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return err
	}
	temporaryName = ""
	return nil
}

// Reports whether a raw JSON value is explicitly null.
func isNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

// Reports whether a raw JSON value is null or an empty list.
func isEmptyJSON(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) ||
		bytes.Equal(trimmed, []byte("[]"))
}

// Returns the item type, or an empty string when it is absent.
func itemType(raw json.RawMessage) string {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return ""
	}
	var kind string
	if err := json.Unmarshal(fields["type"], &kind); err != nil {
		return ""
	}
	return kind
}

// Formats proposal numbers for the deferral message.
func joinNumbers(numbers []int) string {
	parts := make([]string, 0, len(numbers))
	for _, number := range numbers {
		parts = append(parts, fmt.Sprintf("#%d", number))
	}
	return strings.Join(parts, ", ")
}

// Numbers formats proposal numbers for operator messages.
func Numbers(numbers []int) string { return joinNumbers(numbers) }
