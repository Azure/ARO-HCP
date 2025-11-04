package lastcmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/Azure/ARO-HCP/tooling/release-explorer/cmd"
	"github.com/Azure/ARO-Tools/pkg/release/client/last"
	"github.com/Azure/ARO-Tools/pkg/release/client/types"
	"github.com/Azure/ARO-Tools/pkg/release/output"
	"github.com/spf13/cobra"
)

func DefaultOptions() *RawOptions {
	return &RawOptions{
		Last: last.DefaultOptions(),
		Base: cmd.DefaultOptions(),
	}
}

func BindOptions(o *RawOptions, command *cobra.Command) error {
	if err := o.Last.BindOptions(command); err != nil {
		return fmt.Errorf("failed to bind base options: %w", err)
	}
	if err := cmd.BindOptions(o.Base, command); err != nil {
		return fmt.Errorf("failed to bind base options: %w", err)
	}
	return nil
}

type RawOptions struct {
	Base *cmd.RawOptions
	Last *last.RawOptions
}

type validatedOptions struct {
	Base *cmd.ValidatedOptions
	Last *last.ValidatedOptions
}

type ValidatedOptions struct {
	*validatedOptions
}

type Options struct {
	Base *cmd.Options
	Last *last.Options
}

func (o *RawOptions) Validate() (*ValidatedOptions, error) {
	validatedBaseOptions, err := o.Base.Validate()
	if err != nil {
		return nil, fmt.Errorf("failed to validate last options: %w", err)
	}

	validatedLastOptions, err := o.Last.Validate()
	if err != nil {
		return nil, fmt.Errorf("failed to validate last options: %w", err)
	}

	return &ValidatedOptions{
		validatedOptions: &validatedOptions{
			Last: validatedLastOptions,
			Base: validatedBaseOptions,
		},
	}, nil
}

func (o *ValidatedOptions) Complete() (*Options, error) {
	baseOptions, err := o.Base.Complete()
	if err != nil {
		return nil, fmt.Errorf("failed to complete last options: %w", err)
	}
	lastOptions, err := o.Last.Complete()
	if err != nil {
		return nil, fmt.Errorf("failed to complete last options: %w", err)
	}

	return &Options{
		Base: baseOptions,
		Last: lastOptions,
	}, nil
}

func (o *Options) Run(ctx context.Context) error {

	deployment, err := o.Last.LastReleaseDeployment(ctx)
	if err != nil {
		// Distinguish between "no results" and real errors.
		if errors.Is(err, last.ErrNoDeploymentsFound) {
			lo := o.Last.ListOptions
			if lo.PipelineRevision != "" || lo.SourceRevision != "" {
				return fmt.Errorf("no deployments found in '%s' matching the specified revision filters", lo.Environment)
			}
			return fmt.Errorf("no deployments found in '%s' between %s and %s.\nMaybe try a longer --max-lookback, e.g.\n  release-explorer last --environment %s --max-lookback 24w",
				lo.Environment, lo.Since, lo.Until, lo.Environment)
		}
		return fmt.Errorf("failed to find last deployment: %w", err)
	}

	out, err := output.FormatOutput(
		[]*types.ReleaseDeployment{deployment},
		o.Base.OutputFormat, o.Base.Locale,
		o.Last.ListOptions.IncludeComponents,
	)
	if err != nil {
		return fmt.Errorf("failed to format output: %w", err)
	}
	fmt.Println(out)

	return nil
}
