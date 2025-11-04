package listcmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/release-explorer/cmd"
	"github.com/Azure/ARO-Tools/pkg/release/client/list"
	"github.com/Azure/ARO-Tools/pkg/release/output"
)

func DefaultOptions() *RawOptions {
	return &RawOptions{
		Base: cmd.DefaultOptions(),
		List: list.DefaultOptions(),
	}
}

func BindOptions(o *RawOptions, command *cobra.Command) error {
	if err := o.List.BindOptions(command); err != nil {
		return fmt.Errorf("failed to bind base options: %w", err)
	}
	if err := cmd.BindOptions(o.Base, command); err != nil {
		return fmt.Errorf("failed to bind base options: %w", err)
	}

	return nil
}

type RawOptions struct {
	List *list.RawOptions
	Base *cmd.RawOptions
}

type validatedOptions struct {
	List *list.ValidatedOptions
	Base *cmd.ValidatedOptions
}

type ValidatedOptions struct {
	*validatedOptions
}

type Options struct {
	List *list.Options
	Base *cmd.Options
}

func (o *RawOptions) Validate() (*ValidatedOptions, error) {
	baseValidated, err := o.Base.Validate()
	if err != nil {
		return nil, fmt.Errorf("failed to validate base options: %w", err)
	}
	listValidated, err := o.List.Validate()
	if err != nil {
		return nil, fmt.Errorf("failed to validate list options: %w", err)
	}

	return &ValidatedOptions{
		validatedOptions: &validatedOptions{
			Base: baseValidated,
			List: listValidated,
		},
	}, nil
}

func (o *ValidatedOptions) Complete() (*Options, error) {
	baseOpts, err := o.Base.Complete()
	if err != nil {
		return nil, fmt.Errorf("failed to complete base options: %w", err)
	}
	listOpts, err := o.List.Complete()
	if err != nil {
		return nil, fmt.Errorf("failed to complete list options: %w", err)
	}

	return &Options{
		Base: baseOpts,
		List: listOpts,
	}, nil
}

func (o *Options) Run(ctx context.Context) error {
	deployments, err := o.List.ListReleaseDeployments(ctx)
	if err != nil {
		return fmt.Errorf("failed to list deployments: %w", err)
	}

	if len(deployments) == 0 {
		if o.List.PipelineRevision != "" || o.List.SourceRevision != "" {
			return fmt.Errorf("no deployments found in '%s' matching the specified revision filters", o.List.Environment)
		}
		return fmt.Errorf("no deployments found in '%s' between %s and %s.\nMaybe try a longer --since, e.g.\n  release-explorer list -e %s --since 'last month'",
			o.List.Environment, o.List.Since, o.List.Until, o.List.Environment)
	}

	out, err := output.FormatOutput(
		deployments,
		o.Base.OutputFormat,
		o.Base.Locale,
		o.List.IncludeComponents,
	)
	if err != nil {
		return fmt.Errorf("failed to format output: %w", err)
	}

	fmt.Println(out)
	return nil
}
