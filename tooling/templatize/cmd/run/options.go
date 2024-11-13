package run

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	options "github.com/Azure/ARO-HCP/tooling/templatize/cmd"
	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/config"
	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/pipeline"
)

func DefaultOptions() *RawRunOptions {
	return &RawRunOptions{
		RolloutOptions: options.DefaultRolloutOptions(),
	}
}

func BindOptions(opts *RawRunOptions, cmd *cobra.Command) error {
	err := options.BindRolloutOptions(opts.RolloutOptions, cmd)
	if err != nil {
		return fmt.Errorf("failed to bind options: %w", err)
	}
	cmd.Flags().StringVar(&opts.PipelineFile, "pipeline-file", opts.PipelineFile, "pipeline file path")

	for _, flag := range []string{"pipeline-file"} {
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
type RawRunOptions struct {
	RolloutOptions *options.RawRolloutOptions
	PipelineFile   string
}

// validatedRunOptions is a private wrapper that enforces a call of Validate() before Complete() can be invoked.
type validatedRunOptions struct {
	*RawRunOptions
	*options.ValidatedRolloutOptions
}

type ValidatedRunOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*validatedRunOptions
}

// completedRunOptions is a private wrapper that enforces a call of Complete() before config generation can be invoked.
type completedRunOptions struct {
	RolloutOptions *options.RolloutOptions
	Pipeline       *pipeline.Pipeline
}

type RunOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedRunOptions
}

func (o *RawRunOptions) Validate() (*ValidatedRunOptions, error) {
	validatedRolloutOptions, err := o.RolloutOptions.Validate()
	if err != nil {
		return nil, err
	}

	if _, err := os.Stat(o.PipelineFile); os.IsNotExist(err) {
		return nil, fmt.Errorf("pipeline file %s does not exist", o.PipelineFile)
	}

	return &ValidatedRunOptions{
		validatedRunOptions: &validatedRunOptions{
			RawRunOptions:           o,
			ValidatedRolloutOptions: validatedRolloutOptions,
		},
	}, nil
}

func (o *ValidatedRunOptions) Complete() (*RunOptions, error) {
	completed, err := o.ValidatedRolloutOptions.Complete()
	if err != nil {
		return nil, err
	}

	pipeline, err := pipeline.NewPipelineFromFile(o.PipelineFile, completed.Config)
	if err != nil {
		return nil, fmt.Errorf("failed to load pipeline file %s: %w", o.PipelineFile, err)
	}

	return &RunOptions{
		completedRunOptions: &completedRunOptions{
			RolloutOptions: completed,
			Pipeline:       pipeline,
		},
	}, nil
}

func (o *RunOptions) RunPipeline(ctx context.Context) error {
	variables, err := o.RolloutOptions.Options.ConfigProvider.GetVariables(
		o.RolloutOptions.Cloud,
		o.RolloutOptions.DeployEnv,
		o.RolloutOptions.Region,
		config.NewConfigReplacements(
			o.RolloutOptions.Region,
			o.RolloutOptions.RegionShort,
			o.RolloutOptions.Stamp,
		),
	)
	if err != nil {
		return err
	}
	return o.Pipeline.Run(ctx, variables)
}
