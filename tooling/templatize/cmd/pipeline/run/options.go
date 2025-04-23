// Copyright 2025 Microsoft Corporation
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
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-Tools/pkg/config"

	"github.com/Azure/ARO-HCP/tooling/templatize/cmd/pipeline/options"
	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/pipeline"
)

func DefaultOptions() *RawRunOptions {
	return &RawRunOptions{
		PipelineOptions: options.DefaultOptions(),
	}
}

func BindOptions(opts *RawRunOptions, cmd *cobra.Command) error {
	err := options.BindOptions(opts.PipelineOptions, cmd)
	if err != nil {
		return fmt.Errorf("failed to bind options: %w", err)
	}
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", opts.DryRun, "validate the pipeline without executing it")
	cmd.Flags().BoolVar(&opts.NoPersist, "no-persist-tag", opts.NoPersist, "toggle if persist tag should not be set")
	cmd.Flags().IntVar(&opts.DeploymentTimeoutSeconds, "deployment-timeout-seconds", pipeline.DefaultDeploymentTimeoutSeconds, "Timeout in Seconds to wait for previous deployments of the pipeline to finish")
	return nil
}

type RawRunOptions struct {
	PipelineOptions          *options.RawPipelineOptions
	DryRun                   bool
	NoPersist                bool
	DeploymentTimeoutSeconds int
}

// validatedRunOptions is a private wrapper that enforces a call of Validate() before Complete() can be invoked.
type validatedRunOptions struct {
	*RawRunOptions
	*options.ValidatedPipelineOptions
}

type ValidatedRunOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*validatedRunOptions
}

// completedRunOptions is a private wrapper that enforces a call of Complete() before config generation can be invoked.
type completedRunOptions struct {
	PipelineOptions          *options.PipelineOptions
	DryRun                   bool
	NoPersist                bool
	DeploymentTimeoutSeconds int
}

type RunOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedRunOptions
}

func (o *RawRunOptions) Validate() (*ValidatedRunOptions, error) {
	validatedPipelineOptions, err := o.PipelineOptions.Validate()
	if err != nil {
		return nil, err
	}

	return &ValidatedRunOptions{
		validatedRunOptions: &validatedRunOptions{
			RawRunOptions:            o,
			ValidatedPipelineOptions: validatedPipelineOptions,
		},
	}, nil
}

func (o *ValidatedRunOptions) Complete() (*RunOptions, error) {
	completed, err := o.ValidatedPipelineOptions.Complete()
	if err != nil {
		return nil, err
	}

	return &RunOptions{
		completedRunOptions: &completedRunOptions{
			PipelineOptions:          completed,
			DryRun:                   o.DryRun,
			NoPersist:                o.NoPersist,
			DeploymentTimeoutSeconds: o.DeploymentTimeoutSeconds,
		},
	}, nil
}

func (o *RunOptions) RunPipeline(ctx context.Context) error {
	rolloutOptions := o.PipelineOptions.RolloutOptions
	variables, err := rolloutOptions.Options.ConfigProvider.GetDeployEnvRegionConfiguration(
		rolloutOptions.Cloud,
		rolloutOptions.DeployEnv,
		rolloutOptions.Region,
		config.NewConfigReplacements(
			rolloutOptions.Region,
			rolloutOptions.RegionShort,
			rolloutOptions.Stamp,
		),
	)
	if err != nil {
		return err
	}
	_, err = pipeline.RunPipeline(o.PipelineOptions.Pipeline, ctx, &pipeline.PipelineRunOptions{
		DryRun:                   o.DryRun,
		Configuration:            variables,
		Region:                   rolloutOptions.Region,
		Step:                     o.PipelineOptions.Step,
		SubsciptionLookupFunc:    pipeline.LookupSubscriptionID,
		NoPersist:                o.NoPersist,
		DeploymentTimeoutSeconds: o.DeploymentTimeoutSeconds,
	})
	return err
}
