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

package maintenance

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/cli"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/consumers"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/evidence"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/forge/ghapi"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/model"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/orchestration"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/repository"
)

// RawOptions are the unvalidated maintenance inspection flags.
type RawOptions struct {
	RepositoryName string
	Number         int
	SourcePath     string
	BaseSHA        string
	Investigate    bool
	StateDir       string
	EvidenceDir    string
	Output         string
	Comparisons    []string

	out io.Writer
	err io.Writer
}

// DefaultOptions returns the raw maintenance inspection options.
func DefaultOptions() *RawOptions {
	return &RawOptions{Investigate: true}
}

// BindOptions registers the maintenance inspection flags.
func BindOptions(options *RawOptions, command *cobra.Command) error {
	command.Flags().StringVar(&options.RepositoryName, "repository", options.RepositoryName, "OWNER/REPO")
	command.Flags().IntVar(&options.Number, "pr", options.Number, "pull request number")
	command.Flags().StringVar(&options.SourcePath, "repo", options.SourcePath, "local checkout used for changed-file evidence")
	command.Flags().StringVar(&options.BaseSHA, "base", options.BaseSHA, "merge base revision for changed-file evidence")
	command.Flags().BoolVar(&options.Investigate, "investigate", options.Investigate,
		"collect failure evidence when the decision requires diagnosis")
	command.Flags().StringVar(&options.StateDir, "state-dir", options.StateDir,
		"journal and durable investigation budget directory")
	command.Flags().StringVar(&options.EvidenceDir, "evidence-dir", options.EvidenceDir,
		"directory for retrieved run artifacts")
	command.Flags().StringVar(&options.Output, "output", options.Output, "write the maintenance decision here")
	command.Flags().StringSliceVar(&options.Comparisons, "comparison-url", options.Comparisons,
		"candidate trusted comparison run URL (repeatable)")
	return nil
}

// ValidatedOptions are the maintenance inspection options that passed validation.
type ValidatedOptions struct {
	*RawOptions
}

// Validate checks the maintenance inspection inputs.
func (o *RawOptions) Validate(_ context.Context) (*ValidatedOptions, error) {
	if o.RepositoryName == "" || o.Number <= 0 {
		return nil, cli.Invalid(errRepositoryAndPullRequestRequired)
	}
	return &ValidatedOptions{RawOptions: o}, nil
}

// CompletedOptions are the maintenance inspection options with their engine.
type CompletedOptions struct {
	*ValidatedOptions
	engine  *orchestration.Engine
	options orchestration.MaintenanceOptions
}

// Complete builds the read-only engine and the assessment options.
func (v *ValidatedOptions) Complete(_ context.Context) (*CompletedOptions, error) {
	options := orchestration.MaintenanceOptions{
		Investigate:    v.Investigate,
		BaseSHA:        v.BaseSHA,
		ComparisonURLs: v.Comparisons,
		EvidenceDir:    v.EvidenceDir,
		StateDir:       v.StateDir,
	}
	if v.SourcePath != "" {
		checkout, err := repository.Open(v.SourcePath)
		if err != nil {
			return nil, cli.Invalid(err)
		}
		options.ChangedPaths = checkout.ChangedPathsBetween
	}
	reader, err := ghapi.New(v.RepositoryName)
	if err != nil {
		return nil, cli.Incomplete("read pull-request evidence: %v", err)
	}
	deps := orchestration.Deps{
		Clock:    time.Now,
		Registry: consumers.Default(),
		Forge:    reader,
		Evidence: evidence.NewHTTPFetcher(),
	}
	return &CompletedOptions{ValidatedOptions: v, engine: orchestration.New(deps), options: options}, nil
}

// Run assesses the pull request and emits the decision.
func (c *CompletedOptions) Run(ctx context.Context) error {
	decision, err := c.engine.MaintenanceInspect(ctx, c.RepositoryName, c.Number, c.options)
	if err != nil {
		return cli.MapEngineError(err)
	}
	if err := cli.EmitRecord(c.out, c.Output, decision); err != nil {
		return err
	}
	fmt.Fprintf(c.err, "pull request #%d: action=%s reason=%s release=%s recoverRetry=%t\n",
		decision.PullRequest, decision.Action, decision.ActionReason, decision.ReleaseStatus, decision.CanRecoverRetry)
	if decision.Investigation != nil {
		fmt.Fprintf(c.err, "diagnosis=%s missing=%v\n",
			decision.Investigation.Diagnosis, decision.Investigation.Missing)
		if decision.Investigation.Diagnosis == model.DiagnosisUnknownIncomplete {
			return cli.Incomplete("investigation is incomplete; the hold is preserved and evidence collection resumes")
		}
	}
	return nil
}

// Errors reported by the maintenance command.
var errRepositoryAndPullRequestRequired = errors.New("--repository OWNER/REPO and a positive --pr are required")
