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
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/model"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/orchestration"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/testkit"
)

// Builds the `depctl run` command.
func newRunCommand() *cobra.Command {
	var (
		repositoryPath string
		profile        string
		offline        bool
		mode           string
		workDir        string
		stateDir       string
		output         string
		scenario       string
		dryRun         bool
		keepCandidate  bool
		targets        = stringMapFlag{}
	)
	command := &cobra.Command{
		Use:     "run",
		Short:   "Run the lifecycle locally: prepare, or simulate with fake services",
		GroupID: groupWorkflow,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			switch mode {
			case "prepare":
				if repositoryPath == "" {
					return invalid(fmt.Errorf("--repo is required for prepare mode"))
				}
				deps, err := adapters(!offline, repositoryPath)
				if err != nil {
					return invalid(err)
				}
				engine := orchestration.New(deps)
				result, err := engine.Run(cmd.Context(), orchestration.RunOptions{
					RepositoryPath: repositoryPath,
					Profile:        profile,
					Offline:        offline,
					Targets:        targets,
					WorkDir:        workDir,
					StateDir:       stateDir,
					DryRun:         dryRun,
					KeepCandidate:  keepCandidate,
					OutputPath:     output,
				})
				if err != nil {
					return err
				}
				if err := reportRun(cmd, result); err != nil {
					return err
				}
				return runOutcome(result)
			case "simulate":
				if scenario == "" {
					scenario = testkit.ScenarioLifecycle
				}
				report, err := testkit.RunScenario(cmd.Context(), scenario, workDir)
				if err != nil {
					return err
				}
				cmd.Print(testkit.FormatReport(report))
				if output != "" {
					if err := writeJSON(output, report); err != nil {
						return err
					}
				}
				return runOutcome(report.Run)
			default:
				return invalid(fmt.Errorf("unsupported mode %q (use prepare or simulate)", mode))
			}
		},
	}
	command.Flags().StringVar(&repositoryPath, "repo", "", "source repository working tree")
	command.Flags().StringVar(&profile, "profile", "tools", "policy profile")
	command.Flags().BoolVar(&offline, "offline", true, "restrict discovery to tracked pins")
	command.Flags().StringVar(&mode, "mode", "prepare", "prepare or simulate")
	command.Flags().StringVar(&workDir, "work-dir", "", "working directory for candidates, logs and records")
	command.Flags().StringVar(&stateDir, "state-dir", "", "journal and durable budget directory")
	command.Flags().StringVar(&output, "output", "", "write the run result here")
	command.Flags().StringVar(&scenario, "scenario", "", "simulation scenario: "+fmt.Sprint(testkit.Scenarios()))
	command.Flags().BoolVar(&dryRun, "dry-run", false, "declare operations without creating a candidate")
	command.Flags().BoolVar(&keepCandidate, "keep-candidate", false, "leave the candidate worktree in place")
	command.Flags().Var(targets, "target", "explicit FAMILY=VERSION target (repeatable)")
	return command
}

// Renders a summary derived from the typed result.
func reportRun(cmd *cobra.Command, result model.RunResult) error {
	encoded, err := json.MarshalIndent(orchestration.NormalizeRecord(result), "", "  ")
	if err != nil {
		return err
	}
	cmd.Print(string(encoded) + "\n")
	return nil
}

// Maps the lifecycle outcome onto the documented exit contract.
func runOutcome(result model.RunResult) error {
	switch result.Outcome {
	case model.OutcomeNoChange, model.OutcomePrepared, model.OutcomeAwaitingAuthorization, model.OutcomeCompleted:
		return nil
	case model.OutcomeBlocked, model.OutcomeWaiting:
		return incomplete("run ended with outcome %q in phase %q: %v", result.Outcome, result.Phase, result.Blockers)
	case model.OutcomeStale:
		return errStale(fmt.Errorf("run is stale in phase %q", result.Phase))
	case model.OutcomeInterrupted:
		return exitWith(6, fmt.Errorf("run was interrupted in phase %q", result.Phase))
	default:
		return exitWith(6, fmt.Errorf("run failed in phase %q: %v", result.Phase, result.Errors))
	}
}

// Writes one indented record, creating parent directories.
func writeJSON(path string, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(encoded, '\n'), 0o644)
}
