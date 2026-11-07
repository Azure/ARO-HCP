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

package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/model"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/orchestration"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/repository"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/workspace"
	"github.com/Azure/ARO-HCP/tooling/depctl/pkg/depctl"
)

// Wraps an error with an explicit exit code.
func exitWith(code int, err error) error {
	return &depctl.ExitError{Code: code, Message: err.Error()}
}

// Builds the `depctl apply` command.
func newApplyCommand() *cobra.Command {
	var (
		repositoryPath string
		planPath       string
		candidatePath  string
		output         string
		logDir         string
		dryRun         bool
	)
	command := &cobra.Command{
		Use:     "apply",
		Short:   "Prepare an isolated candidate, or declare the operations with --dry-run",
		GroupID: groupWorkflow,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var plan model.Plan
			if err := readRecord(planPath, model.KindPlan, &plan); err != nil {
				return invalid(err)
			}
			if dryRun {
				preview := orchestration.Declare(plan)
				return orchestration.WriteRecord(output, preview)
			}
			checkout, err := repository.Open(repositoryPath)
			if err != nil {
				return invalid(err)
			}
			engine := orchestration.New(orchestration.Deps{})
			record, candidate, err := engine.ApplyWithCheckout(cmd.Context(), checkout, plan, candidatePath, logDir)
			if err != nil {
				return mapApplyError(err)
			}
			defer func() {
				if candidate != nil {
					_ = candidate.Close(cmd.Context(), checkout)
				}
			}()
			return orchestration.WriteRecord(output, record)
		},
	}
	command.Flags().StringVar(&repositoryPath, "repo", ".", "source repository working tree")
	command.Flags().StringVar(&planPath, "plan", "", "plan record")
	command.Flags().StringVar(&candidatePath, "candidate", "", "candidate worktree path to create")
	command.Flags().StringVar(&output, "output", "", "write the candidate record here")
	command.Flags().StringVar(&logDir, "log-dir", "", "directory for consumer logs")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "declare operations without creating a candidate")
	return command
}

// Maps a moved base or head onto the stale exit code.
func errStale(err error) error {
	return exitWith(5, err)
}

// Maps failed validation onto its documented exit code.
func errValidation(err error) error {
	return exitWith(4, err)
}

// Maps an apply failure onto the documented exit contract.
func mapApplyError(err error) error {
	switch {
	case errors.Is(err, orchestration.ErrStale):
		return errStale(err)
	case errors.Is(err, orchestration.ErrGeneration):
		return errValidation(err)
	default:
		return invalid(err)
	}
}

// Builds the `depctl verify` command.
func newVerifyCommand() *cobra.Command {
	var (
		repositoryPath string
		planPath       string
		candidatePath  string
		output         string
		logDir         string
	)
	command := &cobra.Command{
		Use:     "verify",
		Short:   "Run every required consumer on the baseline and the candidate",
		GroupID: groupWorkflow,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var plan model.Plan
			if err := readRecord(planPath, model.KindPlan, &plan); err != nil {
				return invalid(err)
			}
			var record model.Candidate
			if err := readRecord(candidatePath, model.KindCandidate, &record); err != nil {
				return invalid(err)
			}
			checkout, err := repository.Open(repositoryPath)
			if err != nil {
				return invalid(err)
			}
			candidate, err := workspace.OpenExisting(cmd.Context(), record.Workspace, record.BaseSHA)
			if err != nil {
				return invalid(err)
			}
			verdict, err := orchestration.New(orchestration.Deps{}).Verify(cmd.Context(), checkout, plan, record, candidate, logDir)
			if err != nil {
				return err
			}
			if err := orchestration.WriteRecord(output, verdict); err != nil {
				return err
			}
			if verdict.Verdict != model.VerdictValidated {
				return errValidation(fmt.Errorf("validation verdict %q", verdict.Verdict))
			}
			return nil
		},
	}
	command.Flags().StringVar(&repositoryPath, "repo", ".", "source repository working tree")
	command.Flags().StringVar(&planPath, "plan", "", "plan record")
	command.Flags().StringVar(&candidatePath, "candidate", "", "candidate record")
	command.Flags().StringVar(&output, "output", "", "write the validation record here")
	command.Flags().StringVar(&logDir, "log-dir", "", "directory for consumer logs")
	return command
}

// Builds the `depctl proposal` command.
func newProposalCommand() *cobra.Command {
	var (
		repositoryPath string
		planPath       string
		candidatePath  string
		validationPath string
		output         string
		artifactDir    string
	)
	command := &cobra.Command{
		Use:     "proposal",
		Short:   "Package a validated candidate; this confers no authorization",
		GroupID: groupWorkflow,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var plan model.Plan
			if err := readRecord(planPath, model.KindPlan, &plan); err != nil {
				return invalid(err)
			}
			var record model.Candidate
			if err := readRecord(candidatePath, model.KindCandidate, &record); err != nil {
				return invalid(err)
			}
			var verdict model.Validation
			if err := readRecord(validationPath, model.KindValidation, &verdict); err != nil {
				return invalid(err)
			}
			if verdict.CandidateID != record.CandidateID || verdict.PlanID != plan.PlanID {
				return invalid(errors.New("validation does not belong to this plan and candidate"))
			}
			checkout, err := repository.Open(repositoryPath)
			if err != nil {
				return invalid(err)
			}
			candidate, err := workspace.OpenExisting(cmd.Context(), record.Workspace, record.BaseSHA)
			if err != nil {
				return invalid(err)
			}
			proposal, err := orchestration.New(orchestration.Deps{}).BuildProposal(
				cmd.Context(), checkout, plan, candidate, record, verdict, artifactDir)
			if err != nil {
				return errValidation(err)
			}
			return orchestration.WriteRecord(output, proposal)
		},
	}
	command.Flags().StringVar(&repositoryPath, "repo", ".", "source repository working tree")
	command.Flags().StringVar(&planPath, "plan", "", "plan record")
	command.Flags().StringVar(&candidatePath, "candidate", "", "candidate record")
	command.Flags().StringVar(&validationPath, "validation", "", "validation record")
	command.Flags().StringVar(&output, "output", "", "write the proposal record here")
	command.Flags().StringVar(&artifactDir, "artifact-dir", ".", "directory for the candidate patch")
	return command
}
