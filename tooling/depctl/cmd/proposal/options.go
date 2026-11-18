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

package proposal

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

// RawOptions are the unvalidated proposal flags.
type RawOptions struct {
	RepositoryPath string
	PlanPath       string
	CandidatePath  string
	ValidationPath string
	Output         string
	ArtifactDir    string

	out io.Writer
	err io.Writer
}

// DefaultOptions returns the raw proposal options.
func DefaultOptions() *RawOptions {
	return &RawOptions{RepositoryPath: "."}
}

// BindOptions registers the proposal flags.
func BindOptions(options *RawOptions, command *cobra.Command) error {
	command.Flags().StringVar(&options.RepositoryPath, "repo", options.RepositoryPath, "source repository working tree")
	command.Flags().StringVar(&options.PlanPath, "plan", options.PlanPath, "plan record")
	command.Flags().StringVar(&options.CandidatePath, "candidate", options.CandidatePath, "candidate record")
	command.Flags().StringVar(&options.ValidationPath, "validation", options.ValidationPath, "validation record")
	command.Flags().StringVar(&options.Output, "output", options.Output, "write the proposal record here")
	command.Flags().StringVar(&options.ArtifactDir, "artifact-dir", options.ArtifactDir,
		"directory for the candidate patch (default: a temporary directory)")
	return nil
}

// ValidatedOptions are the proposal options that passed validation.
type ValidatedOptions struct {
	*RawOptions
	plan       model.Plan
	candidate  model.Candidate
	validation model.Validation
}

// Validate checks the proposal inputs and loads its records.
func (o *RawOptions) Validate(_ context.Context) (*ValidatedOptions, error) {
	if o.PlanPath == "" {
		return nil, cli.Invalid(errPlanRequired)
	}
	if o.CandidatePath == "" {
		return nil, cli.Invalid(errCandidateRequired)
	}
	if o.ValidationPath == "" {
		return nil, cli.Invalid(errValidationRequired)
	}
	var plan model.Plan
	if err := cli.ReadRecord(o.PlanPath, model.KindPlan, &plan); err != nil {
		return nil, cli.Invalid(err)
	}
	var candidate model.Candidate
	if err := cli.ReadRecord(o.CandidatePath, model.KindCandidate, &candidate); err != nil {
		return nil, cli.Invalid(err)
	}
	var validation model.Validation
	if err := cli.ReadRecord(o.ValidationPath, model.KindValidation, &validation); err != nil {
		return nil, cli.Invalid(err)
	}
	if validation.CandidateID != candidate.CandidateID || validation.PlanID != plan.PlanID {
		return nil, cli.Invalid(errBindingMismatch)
	}
	return &ValidatedOptions{RawOptions: o, plan: plan, candidate: candidate, validation: validation}, nil
}

// CompletedOptions are the proposal options with their checkout and worktree.
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
	if v.ArtifactDir == "" {
		// An omitted artifact directory must not drop a patch inside the
		// source checkout.
		v.ArtifactDir, err = os.MkdirTemp("", "depctl-proposal-")
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(v.err, "candidate patch: %s\n", v.ArtifactDir)
	}
	return &CompletedOptions{
		ValidatedOptions: v,
		checkout:         checkout,
		workspace:        candidate,
		engine:           orchestration.New(orchestration.Deps{}),
	}, nil
}

// Run builds and emits the proposal.
func (c *CompletedOptions) Run(ctx context.Context) error {
	packaged, err := c.engine.BuildProposal(ctx, c.checkout, c.plan, c.workspace, c.candidate, c.validation, c.ArtifactDir)
	if err != nil {
		return cli.MapEngineError(err)
	}
	return cli.EmitRecord(c.out, c.Output, packaged)
}

// Errors reported by the proposal command.
var (
	errPlanRequired       = errors.New("--plan is required")
	errCandidateRequired  = errors.New("--candidate is required")
	errValidationRequired = errors.New("--validation is required")
	errBindingMismatch    = errors.New("validation does not belong to this plan and candidate")
)
