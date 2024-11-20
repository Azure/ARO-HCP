package options

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	options "github.com/Azure/ARO-HCP/tooling/templatize/cmd"
	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/pipeline"
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
	cmd.Flags().StringVar(&opts.PipelineFile, "pipeline-file", opts.PipelineFile, "pipeline file path")
	cmd.Flags().StringVar(&opts.Step, "step", opts.Step, "run only a specific step in the pipeline")

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
type RawPipelineOptions struct {
	RolloutOptions *options.RawRolloutOptions
	PipelineFile   string
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
	Pipeline       *pipeline.Pipeline
	Step           string
}

type PipelineOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedPipelineOptions
}

func (o *RawPipelineOptions) Validate() (*ValidatedPipelineOptions, error) {
	validatedRolloutOptions, err := o.RolloutOptions.Validate()
	if err != nil {
		return nil, err
	}

	if _, err := os.Stat(o.PipelineFile); os.IsNotExist(err) {
		return nil, fmt.Errorf("pipeline file %s does not exist", o.PipelineFile)
	}

	return &ValidatedPipelineOptions{
		validatedPipelineOptions: &validatedPipelineOptions{
			RawPipelineOptions:      o,
			ValidatedRolloutOptions: validatedRolloutOptions,
		},
	}, nil
}

func (o *ValidatedPipelineOptions) Complete() (*PipelineOptions, error) {
	completed, err := o.ValidatedRolloutOptions.Complete()
	if err != nil {
		return nil, err
	}

	pipeline, err := pipeline.NewPipelineFromFile(o.PipelineFile, completed.Config)
	if err != nil {
		return nil, fmt.Errorf("failed to load pipeline file %s: %w", o.PipelineFile, err)
	}

	return &PipelineOptions{
		completedPipelineOptions: &completedPipelineOptions{
			RolloutOptions: completed,
			Pipeline:       pipeline,
			Step:           o.Step,
		},
	}, nil
}
