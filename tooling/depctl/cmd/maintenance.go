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
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/consumers"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/evidence"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/forge/ghcli"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/model"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/orchestration"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/repository"
)

// Builds the `depctl maintenance inspect` command.
func newMaintenanceCommand() *cobra.Command {
	command := &cobra.Command{
		Use:     "maintenance",
		Short:   "Assess owned proposals without mutating them",
		GroupID: groupWorkflow,
		Args:    cobra.NoArgs,
	}
	command.AddCommand(newMaintenanceInspectCommand())
	return command
}

// Builds the `depctl maintenance inspect` command.
func newMaintenanceInspectCommand() *cobra.Command {
	var (
		repositoryName string
		number         int
		sourcePath     string
		base           string
		investigate    bool
		stateDir       string
		evidenceDir    string
		output         string
		comparisons    []string
	)
	command := &cobra.Command{
		Use:   "inspect",
		Short: "Read ownership, holds, current-head CI and collect failure evidence",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if repositoryName == "" || number <= 0 {
				return invalid(fmt.Errorf("--repository OWNER/REPO and a positive --pr are required"))
			}
			options := orchestration.MaintenanceOptions{
				Investigate:    investigate,
				BaseSHA:        base,
				ComparisonURLs: comparisons,
				EvidenceDir:    evidenceDir,
				StateDir:       stateDir,
			}
			if sourcePath != "" {
				checkout, err := repository.Open(sourcePath)
				if err != nil {
					return invalid(err)
				}
				options.ChangedPaths = checkout.ChangedPathsBetween
			}
			deps := orchestration.Deps{
				Clock:    time.Now,
				Registry: consumers.Default(),
				Forge:    ghcli.New(repositoryName),
				Evidence: evidence.NewHTTPFetcher(),
			}
			decision, err := orchestration.New(deps).MaintenanceInspect(cmd.Context(), repositoryName, number, options)
			if err != nil {
				return err
			}
			if err := orchestration.WriteRecord(output, decision); err != nil {
				return err
			}
			cmd.Printf("pull request #%d: action=%s reason=%s release=%s recoverRetry=%t\n",
				decision.PullRequest, decision.Action, decision.ActionReason, decision.ReleaseStatus, decision.CanRecoverRetry)
			if decision.Investigation != nil {
				cmd.Printf("diagnosis=%s missing=%v\n", decision.Investigation.Diagnosis, decision.Investigation.Missing)
				if decision.Investigation.Diagnosis == model.DiagnosisUnknownIncomplete {
					return incomplete("investigation is incomplete; the hold is preserved and evidence collection resumes")
				}
			}
			return nil
		},
	}
	command.Flags().StringVar(&repositoryName, "repository", "", "OWNER/REPO")
	command.Flags().IntVar(&number, "pr", 0, "pull request number")
	command.Flags().StringVar(&sourcePath, "repo", "", "local checkout used for changed-file evidence")
	command.Flags().StringVar(&base, "base", "", "merge base revision for changed-file evidence")
	command.Flags().BoolVar(&investigate, "investigate", true, "collect failure evidence when the decision requires diagnosis")
	command.Flags().StringVar(&stateDir, "state-dir", "", "journal and durable investigation budget directory")
	command.Flags().StringVar(&evidenceDir, "evidence-dir", "", "directory for retrieved run artifacts")
	command.Flags().StringVar(&output, "output", "", "write the maintenance decision here")
	command.Flags().StringSliceVar(&comparisons, "comparison-url", nil, "candidate trusted comparison run URL (repeatable)")
	return command
}

// Builds the `depctl evidence prow` command.
func newEvidenceCommand() *cobra.Command {
	command := &cobra.Command{
		Use:     "evidence",
		Short:   "Retrieve bounded public run evidence",
		GroupID: groupEvidence,
		Args:    cobra.NoArgs,
	}
	command.AddCommand(newEvidenceProwCommand())
	return command
}

// Builds the `depctl evidence prow` command.
func newEvidenceProwCommand() *cobra.Command {
	var (
		prowURL   string
		outputDir string
		output    string
	)
	command := &cobra.Command{
		Use:   "prow",
		Short: "Fetch finished.json and build-log.txt from a canonical Prow run URL",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if prowURL == "" || outputDir == "" {
				return invalid(fmt.Errorf("--url and --output-dir are required"))
			}
			manifest, err := evidence.NewHTTPFetcher().Fetch(cmd.Context(), prowURL, outputDir)
			if err != nil {
				return incomplete("evidence retrieval failed: %v (missing: %v)", err, manifest.Missing)
			}
			if err := orchestration.WriteRecord(output, manifest); err != nil {
				return err
			}
			cmd.Printf("retrieved %d artifacts for %s (result %s)\n", len(manifest.Files), prowURL, manifest.Result)
			return nil
		},
	}
	command.Flags().StringVar(&prowURL, "url", "", "canonical prow.ci.openshift.org run URL")
	command.Flags().StringVar(&outputDir, "output-dir", "", "empty directory for the artifacts")
	command.Flags().StringVar(&output, "output", "", "write the evidence manifest here")
	return command
}
