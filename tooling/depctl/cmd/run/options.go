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

package run

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/cli"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/consumers"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/discovery"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/model"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/orchestration"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/testkit"
)

// RawOptions are the unvalidated run flags.
type RawOptions struct {
	RepositoryPath string
	Profile        string
	Offline        bool
	Mode           string
	WorkDir        string
	StateDir       string
	Output         string
	Scenario       string
	DryRun         bool
	KeepCandidate  bool
	Targets        cli.StringMapFlag
	BaselineTools  cli.StringMapFlag
	CandidateTools cli.StringMapFlag

	out io.Writer
	err io.Writer
}

// DefaultOptions returns the raw run options.
func DefaultOptions() *RawOptions {
	return &RawOptions{
		Profile:        "tools",
		Offline:        true,
		Mode:           "prepare",
		Targets:        cli.StringMapFlag{},
		BaselineTools:  cli.StringMapFlag{},
		CandidateTools: cli.StringMapFlag{},
	}
}

// BindOptions registers the run flags.
func BindOptions(options *RawOptions, command *cobra.Command) error {
	command.Flags().StringVar(&options.RepositoryPath, "repo", options.RepositoryPath, "source repository working tree")
	command.Flags().StringVar(&options.Profile, "profile", options.Profile, "policy profile")
	command.Flags().BoolVar(&options.Offline, "offline", options.Offline, "restrict discovery to tracked pins")
	command.Flags().StringVar(&options.Mode, "mode", options.Mode, "prepare or simulate")
	command.Flags().StringVar(&options.WorkDir, "work-dir", options.WorkDir,
		"working directory for candidates, logs and records (default: a temporary directory)")
	command.Flags().StringVar(&options.StateDir, "state-dir", options.StateDir, "journal and durable budget directory")
	command.Flags().StringVar(&options.Output, "output", options.Output, "write the run result here")
	command.Flags().StringVar(&options.Scenario, "scenario", options.Scenario,
		fmt.Sprintf("simulation scenario: %v", testkit.Scenarios()))
	command.Flags().BoolVar(&options.DryRun, "dry-run", options.DryRun, "declare operations without creating a candidate")
	command.Flags().BoolVar(&options.KeepCandidate, "keep-candidate", options.KeepCandidate,
		"leave the candidate worktree in place")
	command.Flags().Var(options.Targets, "target", "explicit FAMILY=VERSION target (repeatable)")
	command.Flags().Var(options.BaselineTools, "baseline-tool", "phase-specific baseline executable NAME=PATH (repeatable)")
	command.Flags().Var(options.CandidateTools, "candidate-tool", "phase-specific candidate executable NAME=PATH (repeatable)")
	return nil
}

// ValidatedOptions are the run options that passed validation.
type ValidatedOptions struct {
	*RawOptions
}

// Validate checks the run mode and its required inputs.
func (o *RawOptions) Validate(_ context.Context) (*ValidatedOptions, error) {
	switch o.Mode {
	case "prepare":
		if o.RepositoryPath == "" {
			return nil, cli.Invalid(errRepositoryRequired)
		}
	case "simulate":
		if o.RepositoryPath != "" || len(o.Targets) > 0 {
			return nil, cli.Invalid(errSimulateFlags)
		}
		if o.Scenario == "" {
			o.Scenario = testkit.ScenarioLifecycle
		}
	default:
		return nil, cli.Invalid(fmt.Errorf("unsupported mode %q (use prepare or simulate)", o.Mode))
	}
	return &ValidatedOptions{RawOptions: o}, nil
}

// CompletedOptions are the run options with their engine.
type CompletedOptions struct {
	*ValidatedOptions
	engine *orchestration.Engine
}

// Complete builds the engine for a prepare run.
func (v *ValidatedOptions) Complete(_ context.Context) (*CompletedOptions, error) {
	if v.Mode == "simulate" {
		return &CompletedOptions{ValidatedOptions: v}, nil
	}
	deps := orchestration.Deps{
		Clock:     time.Now,
		Registry:  consumers.Default(),
		ToolPaths: cli.ToolPaths(v.BaselineTools, v.CandidateTools),
	}
	if !v.Offline {
		deps.Resolver = discovery.NewCLIResolver(v.RepositoryPath)
	}
	return &CompletedOptions{ValidatedOptions: v, engine: orchestration.New(deps)}, nil
}

// Run prepares a candidate or runs a simulation.
func (c *CompletedOptions) Run(ctx context.Context) error {
	if c.Mode == "simulate" {
		report, err := testkit.RunScenario(ctx, c.Scenario, c.WorkDir)
		if err != nil {
			return err
		}
		fmt.Fprint(c.out, testkit.FormatReport(report))
		if c.Output != "" {
			if err := orchestration.WriteRecord(c.Output, report.Run); err != nil {
				return err
			}
		}
		return runOutcome(report.Run)
	}
	result, err := c.engine.Run(ctx, orchestration.RunOptions{
		RepositoryPath: c.RepositoryPath,
		Profile:        c.Profile,
		Offline:        c.Offline,
		Targets:        c.Targets,
		WorkDir:        c.WorkDir,
		StateDir:       c.StateDir,
		DryRun:         c.DryRun,
		KeepCandidate:  c.KeepCandidate,
		OutputPath:     c.Output,
	})
	if err != nil {
		return cli.MapEngineError(err)
	}
	if err := cli.WriteJSON(c.out, result); err != nil {
		return err
	}
	return runOutcome(result)
}

// Maps the lifecycle outcome onto the documented exit contract.
func runOutcome(result model.RunResult) error {
	switch result.Outcome {
	case model.OutcomeNoChange, model.OutcomePrepared, model.OutcomeAwaitingAuthorization, model.OutcomeCompleted:
		return nil
	case model.OutcomeValidationFailed:
		return cli.Validation(fmt.Errorf(
			"run ended with outcome %q in phase %q: %v", result.Outcome, result.Phase, result.Blockers))
	case model.OutcomeBlocked, model.OutcomeWaiting:
		return cli.Incomplete("run ended with outcome %q in phase %q: %v", result.Outcome, result.Phase, result.Blockers)
	case model.OutcomeStale:
		return cli.Stale(fmt.Errorf("run is stale in phase %q", result.Phase))
	case model.OutcomeInterrupted:
		return cli.Internal(fmt.Errorf("run was interrupted in phase %q", result.Phase))
	default:
		return cli.Internal(fmt.Errorf("run failed in phase %q: %v", result.Phase, result.Errors))
	}
}

// Errors reported by the run command.
var (
	errRepositoryRequired = errors.New("--repo is required for prepare mode")
	errSimulateFlags      = errors.New("simulate mode does not accept --repo or --target")
)
