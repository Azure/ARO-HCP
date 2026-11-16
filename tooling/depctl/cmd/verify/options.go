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

package verify

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

// RawOptions are the unvalidated verify flags.
type RawOptions struct {
	RepositoryPath string
	PlanPath       string
	CandidatePath  string
	Output         string
	LogDir         string
	BaselineTools  cli.StringMapFlag
	CandidateTools cli.StringMapFlag

	out io.Writer
	err io.Writer
}

// DefaultOptions returns the raw verify options.
func DefaultOptions() *RawOptions {
	return &RawOptions{
		RepositoryPath: ".",
		BaselineTools:  cli.StringMapFlag{},
		CandidateTools: cli.StringMapFlag{},
	}
}

// BindOptions registers the verify flags.
func BindOptions(options *RawOptions, command *cobra.Command) error {
	command.Flags().StringVar(&options.RepositoryPath, "repo", options.RepositoryPath, "source repository working tree")
	command.Flags().StringVar(&options.PlanPath, "plan", options.PlanPath, "plan record")
	command.Flags().StringVar(&options.CandidatePath, "candidate", options.CandidatePath, "candidate record")
	command.Flags().StringVar(&options.Output, "output", options.Output, "write the validation record here")
	command.Flags().StringVar(&options.LogDir, "log-dir", options.LogDir, "directory for consumer logs")
	command.Flags().Var(options.BaselineTools, "baseline-tool", "phase-specific baseline executable NAME=PATH (repeatable)")
	command.Flags().Var(options.CandidateTools, "candidate-tool", "phase-specific candidate executable NAME=PATH (repeatable)")
	return nil
}

// ValidatedOptions are the verify options that passed validation.
type ValidatedOptions struct {
	*RawOptions
	plan      model.Plan
	candidate model.Candidate
}

// Validate checks the verify inputs and loads its records.
func (o *RawOptions) Validate(_ context.Context) (*ValidatedOptions, error) {
	if o.PlanPath == "" {
		return nil, cli.Invalid(errPlanRequired)
	}
	if o.CandidatePath == "" {
		return nil, cli.Invalid(errCandidateRequired)
	}
	var plan model.Plan
	if err := cli.ReadRecord(o.PlanPath, model.KindPlan, &plan); err != nil {
		return nil, cli.Invalid(err)
	}
	var candidate model.Candidate
	if err := cli.ReadRecord(o.CandidatePath, model.KindCandidate, &candidate); err != nil {
		return nil, cli.Invalid(err)
	}
	return &ValidatedOptions{RawOptions: o, plan: plan, candidate: candidate}, nil
}

// CompletedOptions are the verify options with their checkout and worktree.
type CompletedOptions struct {
	*ValidatedOptions
	checkout  *repository.Repo
	workspace *workspace.Candidate
	engine    *orchestration.Engine
}

// Complete opens the checkout and the candidate worktree.
func (v *ValidatedOptions) Complete(ctx context.Context) (*CompletedOptions, error) {
	checkout, err := repository.Open(v.RepositoryPath)
	if err != nil {
		return nil, cli.Invalid(err)
	}
	candidate, err := workspace.OpenExisting(ctx, v.candidate.Workspace, v.candidate.CommitSHA, v.candidate.BaseSHA)
	if err != nil {
		return nil, cli.MapEngineError(err)
	}
	if v.LogDir == "" {
		// An omitted log directory must not place a baseline worktree inside
		// the source checkout.
		v.LogDir, err = os.MkdirTemp("", "depctl-verify-")
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(v.err, "consumer logs: %s\n", v.LogDir)
	}
	deps := orchestration.Deps{ToolPaths: cli.ToolPaths(v.BaselineTools, v.CandidateTools)}
	return &CompletedOptions{ValidatedOptions: v, checkout: checkout, workspace: candidate, engine: orchestration.New(deps)}, nil
}

// Run verifies the candidate and emits the validation.
func (c *CompletedOptions) Run(ctx context.Context) error {
	verdict, err := c.engine.Verify(ctx, c.checkout, c.plan, c.candidate, c.workspace, c.LogDir)
	if err != nil {
		return cli.MapEngineError(err)
	}
	if err := cli.EmitRecord(c.out, c.Output, verdict); err != nil {
		return err
	}
	switch verdict.Verdict {
	case model.VerdictValidated:
		return nil
	case model.VerdictRegression:
		return cli.Validation(fmt.Errorf("validation verdict %q", verdict.Verdict))
	default:
		return cli.Incomplete("validation is incomplete with verdict %q: %v", verdict.Verdict, verdict.NotRun)
	}
}

// Errors reported by the verify command.
var (
	errPlanRequired      = errors.New("--plan is required")
	errCandidateRequired = errors.New("--candidate is required")
)
