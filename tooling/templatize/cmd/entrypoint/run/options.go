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
	"os"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/templatize/cmd/entrypoint/entrypointutils"
	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/pipeline"
)

func DefaultOptions() *RawOptions {
	return &RawOptions{
		RawOptions:               entrypointutils.DefaultOptions(),
		DeploymentTimeoutSeconds: pipeline.DefaultDeploymentTimeoutSeconds,
	}
}

func BindOptions(opts *RawOptions, cmd *cobra.Command) error {
	if err := entrypointutils.BindOptions(opts.RawOptions, cmd); err != nil {
		return err
	}

	cmd.Flags().StringVar(&opts.TimingOutputFile, "timing-output", opts.TimingOutputFile, "Path to the file where timing outputs will be written.")
	cmd.Flags().StringVar(&opts.JUnitOutputFile, "junit-output", opts.JUnitOutputFile, "If provided, jUnit outputs for pipeline steps will be written to this file.")

	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", opts.DryRun, "validate the pipeline without executing it")
	cmd.Flags().BoolVar(&opts.Persist, "persist-tag", opts.Persist, "toggle if persist tag should be set")
	cmd.Flags().IntVar(&opts.DeploymentTimeoutSeconds, "deployment-timeout-seconds", opts.DeploymentTimeoutSeconds, "Timeout in Seconds to wait for previous deployments of the pipeline to finish")

	return nil
}

type RawOptions struct {
	*entrypointutils.RawOptions

	DryRun                   bool
	Persist                  bool
	DeploymentTimeoutSeconds int

	TimingOutputFile string
	JUnitOutputFile  string
}

// validatedOptions is a private wrapper that enforces a call of Validate() before Complete() can be invoked.
type validatedOptions struct {
	*RawOptions
	*entrypointutils.ValidatedOptions
}

type ValidatedOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*validatedOptions
}

// completedOptions is a private wrapper that enforces a call of Complete() before config generation can be invoked.
type completedOptions struct {
	*entrypointutils.Options

	DryRun                   bool
	NoPersist                bool
	DeploymentTimeoutSeconds int

	TimingOutputFile string
	JUnitOutputFile  string
}

type Options struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedOptions
}

func (o *RawOptions) Validate(ctx context.Context) (*ValidatedOptions, error) {
	validated, err := o.RawOptions.Validate(ctx)
	if err != nil {
		return nil, err
	}

	return &ValidatedOptions{
		validatedOptions: &validatedOptions{
			RawOptions:       o,
			ValidatedOptions: validated,
		},
	}, nil
}

func (o *ValidatedOptions) Complete(ctx context.Context) (*Options, error) {
	completed, err := o.ValidatedOptions.Complete(ctx)
	if err != nil {
		return nil, err
	}

	return &Options{
		completedOptions: &completedOptions{
			Options: completed,

			DryRun:                   o.DryRun,
			NoPersist:                !o.Persist,
			DeploymentTimeoutSeconds: o.DeploymentTimeoutSeconds,

			TimingOutputFile: o.TimingOutputFile,
			JUnitOutputFile:  o.JUnitOutputFile,
		},
	}, nil
}

func (o *Options) Run(ctx context.Context) error {
	subscriptionIdToAzureConfigDirectory, err := pipeline.GetAllRequiredAzureClients(ctx, o.Pipelines, o.Subscriptions)
	if err != nil {
		return fmt.Errorf("failed to get all required Azure clients: %w", err)
	}
	defer func() {
		for _, azureConfigDir := range subscriptionIdToAzureConfigDirectory {
			os.RemoveAll(azureConfigDir)
		}
	}()

	runOpts := &pipeline.PipelineRunOptions{
		BaseRunOptions: pipeline.BaseRunOptions{
			DryRun:                               o.DryRun,
			Cloud:                                o.Cloud,
			Configuration:                        o.Config,
			NoPersist:                            o.NoPersist,
			DeploymentTimeoutSeconds:             o.DeploymentTimeoutSeconds,
			StepCacheDir:                         o.StepCacheDir,
			BicepClient:                          o.BicepClient,
			SubscriptionIdToAzureConfigDirectory: subscriptionIdToAzureConfigDirectory,
		},
		TopologyDir:           o.TopoDir,
		Region:                o.Region,
		SubsciptionLookupFunc: pipeline.LookupSubscriptionID(o.Subscriptions),
		Concurrency:           o.Concurrency,
		TimingOutputFile:      o.TimingOutputFile,
		JUnitOutputFile:       o.JUnitOutputFile,
	}

	if o.Entrypoint != nil {
		_, err := pipeline.RunEntrypoint(o.Topo, o.Entrypoint, o.Pipelines, ctx, runOpts, pipeline.RunStep)
		return err
	}

	_, err = pipeline.RunPipeline(o.Service, o.Pipelines[o.Service.ServiceGroup], ctx, runOpts, pipeline.RunStep)
	return err
}
