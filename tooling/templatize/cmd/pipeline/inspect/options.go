package inspect

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/templatize/cmd/pipeline/options"
	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/config"
	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/pipeline"
)

func DefaultOptions() *RawInspectOptions {
	return &RawInspectOptions{
		PipelineOptions: options.DefaultOptions(),
	}
}

func BindOptions(opts *RawInspectOptions, cmd *cobra.Command) error {
	err := options.BindOptions(opts.PipelineOptions, cmd)
	if err != nil {
		return fmt.Errorf("failed to bind options: %w", err)
	}
	cmd.Flags().StringVar(&opts.Aspect, "aspect", opts.Aspect, "aspect of the pipeline to inspect")
	cmd.Flags().StringVar(&opts.Format, "format", opts.Format, "output format")
	return nil
}

type RawInspectOptions struct {
	PipelineOptions *options.RawPipelineOptions
	Aspect          string
	Format          string
}

// validatedInspectOptions is a private wrapper that enforces a call of Validate() before Complete() can be invoked.
type validatedInspectOptions struct {
	*RawInspectOptions
	*options.ValidatedPipelineOptions
}

type ValidatedInspectOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*validatedInspectOptions
}

// completedRunOptions is a private wrapper that enforces a call of Complete() before config generation can be invoked.
type completedInspectOptions struct {
	PipelineOptions *options.PipelineOptions
	Aspect          string
	Format          string
}

type InspectOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedInspectOptions
}

func (o *RawInspectOptions) Validate() (*ValidatedInspectOptions, error) {
	validatedPipelineOptions, err := o.PipelineOptions.Validate()
	if err != nil {
		return nil, err
	}
	// todo validate aspect
	return &ValidatedInspectOptions{
		validatedInspectOptions: &validatedInspectOptions{
			RawInspectOptions:        o,
			ValidatedPipelineOptions: validatedPipelineOptions,
		},
	}, nil
}

func (o *ValidatedInspectOptions) Complete() (*InspectOptions, error) {
	completed, err := o.ValidatedPipelineOptions.Complete()
	if err != nil {
		return nil, err
	}

	return &InspectOptions{
		completedInspectOptions: &completedInspectOptions{
			PipelineOptions: completed,
			Aspect:          o.Aspect,
			Format:          o.Format,
		},
	}, nil
}

func (o *InspectOptions) RunInspect(ctx context.Context) error {
	rolloutOptions := o.PipelineOptions.RolloutOptions
	variables, err := rolloutOptions.Options.ConfigProvider.GetVariables(
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
	return o.PipelineOptions.Pipeline.Inspect(ctx, &pipeline.PipelineInspectOptions{
		Vars:   variables,
		Region: rolloutOptions.Region,
		Step:   o.PipelineOptions.Step,
		Aspect: o.Aspect,
		Format: o.Format,
	})
}
