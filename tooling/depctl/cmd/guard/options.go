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

package guard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/cli"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/forge"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/forge/ghapi"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/safeoutput"
)

// readerFactory builds the read-only proposal reader.
var readerFactory = func(repository string) (forge.Reader, error) {
	// Filter on issue metadata so unrelated robot proposals are never fetched.
	return ghapi.NewWithPrefilter(repository, safeoutput.ToolProposalFilter)
}

// RawOptions are the unvalidated guard flags.
type RawOptions struct {
	AgentOutput string
	Repository  string

	out io.Writer
	err io.Writer
}

// DefaultOptions returns the raw guard options.
func DefaultOptions() *RawOptions {
	return &RawOptions{}
}

// BindOptions registers the guard flags.
func BindOptions(options *RawOptions, command *cobra.Command) error {
	command.Flags().StringVar(&options.AgentOutput, "agent-output", options.AgentOutput,
		"safe-output artifact written by the agent step")
	command.Flags().StringVar(&options.Repository, "repository", options.Repository, "OWNER/REPO")
	return nil
}

// ValidatedOptions are the guard options that passed validation.
type ValidatedOptions struct {
	*RawOptions
	artifact *safeoutput.Artifact
}

// Validate checks the flags and parses the artifact before any remote read.
func (o *RawOptions) Validate(_ context.Context) (*ValidatedOptions, error) {
	if o.AgentOutput == "" || o.Repository == "" {
		return nil, cli.Invalid(errAgentOutputAndRepositoryRequired)
	}
	content, err := os.ReadFile(o.AgentOutput)
	if err != nil {
		return nil, cli.Invalid(fmt.Errorf("read agent output: %w", err))
	}
	artifact, err := safeoutput.Parse(content)
	if err != nil {
		return nil, cli.Invalid(err)
	}
	return &ValidatedOptions{RawOptions: o, artifact: artifact}, nil
}

// CompletedOptions are the guard options with their reader.
type CompletedOptions struct {
	*ValidatedOptions
	reader forge.Reader
}

// Complete builds the read-only proposal reader.
func (v *ValidatedOptions) Complete(_ context.Context) (*CompletedOptions, error) {
	reader, err := readerFactory(v.Repository)
	if err != nil {
		if errors.Is(err, forge.ErrAuthentication) {
			return nil, cli.Incomplete("read owned proposals: %v", err)
		}
		return nil, cli.Invalid(err)
	}
	return &CompletedOptions{ValidatedOptions: v, reader: reader}, nil
}

// Run applies the guard and rewrites the artifact only after it passes.
func (c *CompletedOptions) Run(ctx context.Context) error {
	result, err := safeoutput.Guard(ctx, c.reader, c.Repository, c.artifact)
	if err != nil {
		return guardError(err)
	}
	if err := safeoutput.WriteArtifact(c.AgentOutput, c.artifact); err != nil {
		return cli.Internal(err)
	}
	switch {
	case result.Deferred:
		fmt.Fprintf(c.err, "deferred proposals while %s is open\n", safeoutput.Numbers(result.Active))
	case len(result.Active) > 0:
		fmt.Fprintf(c.err, "owned proposals are already open: %s\n", safeoutput.Numbers(result.Active))
	default:
		fmt.Fprintln(c.err, "no owned tooling proposal is open")
	}
	return nil
}

// Maps guard failures onto the documented exit codes.
func guardError(err error) error {
	if errors.Is(err, safeoutput.ErrInvalidArtifact) {
		return cli.Invalid(err)
	}
	if errors.Is(err, forge.ErrEvidenceIncomplete) {
		return cli.Incomplete("%v", err)
	}
	return cli.Internal(err)
}

// Errors reported by the guard command.
var errAgentOutputAndRepositoryRequired = errors.New("--agent-output and --repository are required")
