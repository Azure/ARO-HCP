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

package dependabot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/cli"
	dependabotpolicy "github.com/Azure/ARO-HCP/tooling/depctl/internal/dependabot"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/forge"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/forge/ghapi"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/policy"
)

// dependabotRepositoryPattern matches the OWNER/REPO form these commands accept.
var dependabotRepositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// rawReader is the raw JSON surface the guard needs.
type rawReader interface {
	REST(ctx context.Context, endpoint string) ([]byte, error)
}

// GuardOptions are the unvalidated publication-guard flags.
type GuardOptions struct {
	Repository  string
	AgentOutput string
}

// ValidatedGuard are the guard options that passed validation.
type ValidatedGuard struct {
	*GuardOptions
}

// Validate checks the guard inputs before any client is built.
func (o *GuardOptions) Validate() (*ValidatedGuard, error) {
	if o.Repository == "" || o.AgentOutput == "" {
		return nil, cli.Invalid(errors.New("--repository OWNER/REPO and --agent-output FILE are required"))
	}
	if !dependabotRepositoryPattern.MatchString(o.Repository) {
		return nil, cli.Invalid(errors.New("--repository must be OWNER/REPO"))
	}
	return &ValidatedGuard{GuardOptions: o}, nil
}

// CompletedGuard are the guard options with their reader and warning sink.
type CompletedGuard struct {
	*ValidatedGuard
	reader forge.Reader
	raw    rawReader
	warn   func(string)
}

// Complete builds the read-only reader and the warning sink.
func (v *ValidatedGuard) Complete(errOut io.Writer) (*CompletedGuard, error) {
	reader, err := readerFactory(v.Repository)
	if err != nil {
		return nil, cli.Invalid(err)
	}
	raw, ok := reader.(rawReader)
	if !ok {
		return nil, cli.Internal(errors.New("reader does not expose the raw API"))
	}
	warn := func(message string) {
		fmt.Fprintf(errOut, "::warning::%s\n", message)
	}
	return &CompletedGuard{ValidatedGuard: v, reader: reader, raw: raw, warn: warn}, nil
}

// Run validates proposals and rewrites the agent output in place.
func (c *CompletedGuard) Run(ctx context.Context) error {
	pulls, err := c.reader.OpenPullRequests(ctx, policy.Robot)
	if err != nil {
		return cli.MapEngineError(err)
	}
	records, err := dependabotpolicy.OwnedSecurityProposals(pulls, c.Repository)
	if err != nil {
		return cli.Incomplete("%v", err)
	}
	numbers := make([]int, 0, len(records))
	for _, record := range records {
		numbers = append(numbers, record.Number)
	}
	content, err := os.ReadFile(c.AgentOutput)
	if err != nil {
		return cli.Internal(fmt.Errorf("read agent output: %w", err))
	}
	var output map[string]any
	if err := json.Unmarshal(content, &output); err != nil {
		return cli.Internal(fmt.Errorf("decode agent output: %w", err))
	}
	if err := dependabotpolicy.GuardCreate(ctx, c.Repository, output, numbers, c.raw.REST, c.warn); err != nil {
		return cli.Incomplete("%v", err)
	}
	return writeAgentOutput(c.AgentOutput, output)
}

// writeAgentOutput replaces the agent output atomically so a failed write never
// leaves a truncated file behind.
func writeAgentOutput(path string, output map[string]any) error {
	encoded, err := json.Marshal(output)
	if err != nil {
		return cli.Internal(err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), "dependabot-guard-*.json")
	if err != nil {
		return cli.Internal(err)
	}
	name := temporary.Name()
	if _, err := temporary.Write(append(encoded, '\n')); err != nil {
		_ = temporary.Close()
		_ = os.Remove(name)
		return cli.Internal(err)
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(name)
		return cli.Internal(err)
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return cli.Internal(err)
	}
	return nil
}

// newRetestReader builds the raw evidence API the retest guard reads.
func newRetestReader(repository, token string) (dependabotpolicy.RawAPI, error) {
	reader, err := ghapi.NewWithToken(repository, token)
	if err != nil {
		return nil, err
	}
	return reader.REST, nil
}

// runGuard runs the guard command phases in order.
func runGuard(ctx context.Context, cmd *cobra.Command, options *GuardOptions) error {
	validated, err := options.Validate()
	if err != nil {
		return err
	}
	completed, err := validated.Complete(cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	return completed.Run(ctx)
}

// ValidateProposalOptions are the unvalidated preflight inputs.
type ValidateProposalOptions struct {
	Path string
}

// ValidatedProposal are the preflight inputs that passed validation.
type ValidatedProposal struct {
	*ValidateProposalOptions
}

// Validate checks that a proposal path was supplied.
func (o *ValidateProposalOptions) Validate() (*ValidatedProposal, error) {
	if o.Path == "" {
		return nil, cli.Invalid(errors.New("validate requires exactly one proposal path"))
	}
	return &ValidatedProposal{ValidateProposalOptions: o}, nil
}

// CompletedProposal are the preflight inputs with their content read.
type CompletedProposal struct {
	*ValidatedProposal
	content []byte
	out     io.Writer
}

// Complete reads the proposal, failing before any canonicalisation work.
func (v *ValidatedProposal) Complete(out io.Writer) (*CompletedProposal, error) {
	content, err := os.ReadFile(v.Path)
	if err != nil {
		return nil, cli.Internal(fmt.Errorf("read proposal: %w", err))
	}
	return &CompletedProposal{ValidatedProposal: v, content: content, out: out}, nil
}

// Run prints the proposal in its canonical form.
func (c *CompletedProposal) Run() error {
	var item map[string]any
	if err := json.Unmarshal(c.content, &item); err != nil {
		return cli.Invalid(err)
	}
	candidate, err := dependabotpolicy.Group(item)
	if err != nil {
		return cli.Invalid(err)
	}
	item["title"] = candidate.Title
	encoded, err := json.Marshal(item)
	if err != nil {
		return cli.Internal(err)
	}
	fmt.Fprintln(c.out, string(encoded))
	return nil
}

// runValidateProposal runs the preflight phases in order.
func runValidateProposal(path string, out io.Writer) error {
	raw := &ValidateProposalOptions{Path: path}
	validated, err := raw.Validate()
	if err != nil {
		return err
	}
	completed, err := validated.Complete(out)
	if err != nil {
		return err
	}
	return completed.Run()
}

// retestOptions are the unvalidated retest flags.
// RetestOptions are the unvalidated retest flags.
type RetestOptions struct {
	Repository  string
	PullRequest int
	Head        string
	Evidence    string
}

// ValidatedRetest are the retest options that passed validation.
type ValidatedRetest struct {
	*RetestOptions
}

// Validate checks the retest inputs before any client is built.
func (o *RetestOptions) Validate() (*ValidatedRetest, error) {
	if o.Repository == "" || o.PullRequest <= 0 || o.Head == "" || o.Evidence == "" {
		return nil, cli.Invalid(errors.New("--repository, --pull-request, --head and --evidence are required"))
	}
	if !dependabotRepositoryPattern.MatchString(o.Repository) {
		return nil, cli.Invalid(errors.New("--repository must be OWNER/REPO"))
	}
	return &ValidatedRetest{RetestOptions: o}, nil
}

// CompletedRetest are the retest options with their reader, clock and output.
type CompletedRetest struct {
	*ValidatedRetest
	rest dependabotpolicy.RawAPI
	now  func() time.Time
	out  io.Writer
}

// Complete builds the evidence reader from the ambient token.
func (v *ValidatedRetest) Complete(out io.Writer) (*CompletedRetest, error) {
	token := os.Getenv("CI_TOKEN")
	if token == "" {
		token = os.Getenv("GH_TOKEN")
	}
	reader, err := newRetestReader(v.Repository, token)
	if err != nil {
		return nil, cli.Incomplete("retest evidence reader: %v", err)
	}
	return &CompletedRetest{ValidatedRetest: v, rest: reader, now: func() time.Time { return time.Now().UTC() }, out: out}, nil
}

// Run verifies the retest request against current CI evidence.
func (c *CompletedRetest) Run(ctx context.Context) error {
	if err := dependabotpolicy.GuardRetest(ctx, c.Repository, c.PullRequest, c.Head, c.Evidence, c.rest, c.now()); err != nil {
		return cli.Incomplete("%v", err)
	}
	fmt.Fprintf(c.out, "PR #%d: current failing check and comparison run verified; retest cooldown clear\n", c.PullRequest)
	return nil
}

// runRetest runs the retest phases in order.
func runRetest(ctx context.Context, cmd *cobra.Command, options *RetestOptions) error {
	validated, err := options.Validate()
	if err != nil {
		return err
	}
	completed, err := validated.Complete(cmd.OutOrStdout())
	if err != nil {
		return err
	}
	return completed.Run(ctx)
}
