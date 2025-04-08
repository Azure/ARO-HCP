package inspect

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-Tools/pkg/config"

	"github.com/Azure/ARO-HCP/tooling/templatize/cmd/pipeline/options"
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
	cmd.Flags().StringVar(&opts.Scope, "scope", opts.Scope, "scope of the pipeline to inspect")
	cmd.Flags().StringVar(&opts.Format, "format", opts.Format, "output format")
	cmd.Flags().StringVar(&opts.Output, "output", opts.Output, "output file")

	if err := cmd.MarkFlagFilename("output"); err != nil {
		return fmt.Errorf("failed to mark flag %q as a file: %w", "output", err)
	}

	return nil
}

type RawInspectOptions struct {
	PipelineOptions *options.RawPipelineOptions
	Scope           string
	Format          string
	Output          string
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
	Scope           string
	Format          string
	OutputFile      io.Writer
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

	inspectScopes := pipeline.NewStepInspectScopes()
	if _, ok := inspectScopes[o.Scope]; !ok {
		scopes := make([]string, 0, len(inspectScopes))
		for scope := range inspectScopes {
			scopes = append(scopes, scope)
		}
		availableScopes := strings.Join(scopes, ", ")
		return nil, fmt.Errorf("unknown inspect scope %q, valid scopes are: (%v)", o.Scope, availableScopes)
	}

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

	outputFile, err := os.Create(o.Output)
	if err != nil {
		return nil, fmt.Errorf("failed to create output file %s: %w", o.Output, err)
	}

	return &InspectOptions{
		completedInspectOptions: &completedInspectOptions{
			PipelineOptions: completed,
			Scope:           o.Scope,
			Format:          o.Format,
			OutputFile:      outputFile,
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
	inspectOptions := pipeline.NewInspectOptions(variables, rolloutOptions.Region, o.PipelineOptions.Step, o.Scope, o.Format, o.OutputFile)
	return o.PipelineOptions.Pipeline.Inspect(ctx, inspectOptions)
}
