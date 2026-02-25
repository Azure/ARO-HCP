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

package options

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-Tools/pipelines/topology"
	"github.com/Azure/ARO-Tools/pipelines/types"

	options "github.com/Azure/ARO-HCP/tooling/templatize/cmd"
)

func DefaultOptions() *RawPipelineOptions {
	return &RawPipelineOptions{
		RolloutOptions: options.DefaultRolloutOptions(),
	}
}

func BindOptions(opts *RawPipelineOptions, cmd *cobra.Command) error {
	err := options.BindRolloutOptions(opts.RolloutOptions, cmd)
	if err != nil {
		return fmt.Errorf("failed to bind options: %w", err)
	}
	cmd.Flags().StringVar(&opts.ServiceGroup, "service-group", opts.ServiceGroup, "Service group identifying the pipeline.")
	cmd.Flags().StringVar(&opts.TopologyFile, "topology-file", opts.TopologyFile, "Path to the topology configuration.")
	cmd.Flags().StringVar(&opts.Step, "step", opts.Step, "run only a specific step in the pipeline")

	for _, flag := range []string{"topology-file"} {
		if err := cmd.MarkFlagFilename(flag); err != nil {
			return fmt.Errorf("failed to mark flag %q as a file: %w", flag, err)
		}
		if err := cmd.MarkFlagRequired(flag); err != nil {
			return fmt.Errorf("failed to mark flag %q as required: %w", flag, err)
		}
	}
	return nil
}

// RawRunOptions holds input values.
type RawPipelineOptions struct {
	RolloutOptions *options.RawRolloutOptions
	ServiceGroup   string
	TopologyFile   string
	Step           string
}

// validatedPipelineOptions is a private wrapper that enforces a call of Validate() before Complete() can be invoked.
type validatedPipelineOptions struct {
	*RawPipelineOptions
	*options.ValidatedRolloutOptions
}

type ValidatedPipelineOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*validatedPipelineOptions
}

// completedPipelineOptions is a private wrapper that enforces a call of Complete() before config generation can be invoked.
type completedPipelineOptions struct {
	RolloutOptions *options.RolloutOptions
	Service        *topology.Service
	Pipeline       *types.Pipeline
	Step           string
	TopologyDir    string
}

type PipelineOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedPipelineOptions
}

func (o *RawPipelineOptions) Validate(ctx context.Context) (*ValidatedPipelineOptions, error) {
	validatedRolloutOptions, err := o.RolloutOptions.Validate(ctx)
	if err != nil {
		return nil, err
	}

	return &ValidatedPipelineOptions{
		validatedPipelineOptions: &validatedPipelineOptions{
			RawPipelineOptions:      o,
			ValidatedRolloutOptions: validatedRolloutOptions,
		},
	}, nil
}

func (o *ValidatedPipelineOptions) Complete(ctx context.Context) (*PipelineOptions, error) {
	completed, err := o.ValidatedRolloutOptions.Complete(ctx)
	if err != nil {
		return nil, err
	}

	t, err := topology.Load(o.TopologyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load topology file: %w", err)
	}
	if err := t.Validate(); err != nil {
		return nil, fmt.Errorf("failed to validate topology: %w", err)
	}

	service, err := t.Lookup(o.ServiceGroup)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve service group: %w", err)
	}
	pipelinePath := filepath.Join(filepath.Dir(o.TopologyFile), service.PipelinePath)

	pipeline, err := types.NewPipelineFromFile(pipelinePath, completed.Config)
	if err != nil {
		return nil, fmt.Errorf("failed to load pipeline %s: %w", pipelinePath, err)
	}

	return &PipelineOptions{
		completedPipelineOptions: &completedPipelineOptions{
			RolloutOptions: completed,
			Pipeline:       pipeline,
			Service:        service,
			Step:           o.Step,
			TopologyDir:    filepath.Dir(o.TopologyFile),
		},
	}, nil
}
