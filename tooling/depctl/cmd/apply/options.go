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

package apply

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/cli"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/model"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/orchestration"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/repository"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/workspace"
)

// RawOptions are the unvalidated apply flags.
type RawOptions struct {
	RepositoryPath string
	PlanPath       string
	CandidatePath  string
	Output         string
	LogDir         string
	DryRun         bool

	out io.Writer
	err io.Writer
}

// DefaultOptions returns the raw apply options.
func DefaultOptions() *RawOptions {
	return &RawOptions{RepositoryPath: "."}
}

// BindOptions registers the apply flags.
func BindOptions(options *RawOptions, command *cobra.Command) error {
	command.Flags().StringVar(&options.RepositoryPath, "repo", options.RepositoryPath, "source repository working tree")
	command.Flags().StringVar(&options.PlanPath, "plan", options.PlanPath, "plan record")
	command.Flags().StringVar(&options.CandidatePath, "candidate", options.CandidatePath, "candidate worktree path to create")
	command.Flags().StringVar(&options.Output, "output", options.Output, "write the candidate record here")
	command.Flags().StringVar(&options.LogDir, "log-dir", options.LogDir, "directory for consumer logs")
	command.Flags().BoolVar(&options.DryRun, "dry-run", options.DryRun, "declare operations without creating a candidate")
	return nil
}

// ValidatedOptions are the apply options that passed validation.
type ValidatedOptions struct {
	*RawOptions
	plan model.Plan
}

// Validate checks the apply inputs.
func (o *RawOptions) Validate(_ context.Context) (*ValidatedOptions, error) {
	if o.PlanPath == "" {
		return nil, cli.Invalid(errPlanRequired)
	}
	if !o.DryRun && o.CandidatePath == "" {
		return nil, cli.Invalid(errCandidatePathRequired)
	}
	var plan model.Plan
	if err := cli.ReadRecord(o.PlanPath, model.KindPlan, &plan); err != nil {
		return nil, cli.Invalid(err)
	}
	return &ValidatedOptions{RawOptions: o, plan: plan}, nil
}

// CompletedOptions are the apply options with their checkout and log directory.
type CompletedOptions struct {
	*ValidatedOptions
	checkout  *repository.Repo
	candidate *workspace.Candidate
	engine    *orchestration.Engine
}

// Complete opens the checkout and allocates the log directory.
func (v *ValidatedOptions) Complete(_ context.Context) (*CompletedOptions, error) {
	if v.DryRun {
		return &CompletedOptions{ValidatedOptions: v}, nil
	}
	checkout, err := repository.Open(v.RepositoryPath)
	if err != nil {
		return nil, cli.Invalid(err)
	}
	if v.LogDir == "" {
		// Logs must never land inside the source checkout.
		v.LogDir, err = os.MkdirTemp("", "depctl-apply-")
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(v.err, "consumer logs: %s\n", v.LogDir)
	}
	return &CompletedOptions{ValidatedOptions: v, checkout: checkout, engine: orchestration.New(orchestration.Deps{})}, nil
}

// Run declares the operations or prepares the candidate.
func (c *CompletedOptions) Run(ctx context.Context) error {
	if c.DryRun {
		return cli.EmitRecord(c.out, c.Output, orchestration.Declare(c.plan))
	}
	record, candidate, err := c.engine.ApplyWithCheckout(ctx, c.checkout, c.plan, c.CandidatePath, c.LogDir)
	if err != nil {
		return cli.MapEngineError(err)
	}
	if candidate == nil {
		return cli.Invalid(errNoCandidateWorktree)
	}
	c.candidate = candidate
	if err := cli.EmitRecord(c.out, c.Output, record); err != nil {
		// A candidate whose record was never published is scratch work.
		_ = candidate.Close(ctx, c.checkout)
		return err
	}
	// The documented apply -> verify -> proposal flow needs the worktree.
	fmt.Fprintf(c.err, "candidate worktree: %s\n", candidate.Path())
	return nil
}

// Errors reported by the apply command.
var (
	errPlanRequired          = errors.New("--plan is required")
	errCandidatePathRequired = errors.New("--candidate is required unless --dry-run is set")
	errNoCandidateWorktree   = errors.New("apply returned no candidate worktree")
)
