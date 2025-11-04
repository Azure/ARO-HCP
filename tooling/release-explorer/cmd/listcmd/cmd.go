package listcmd

import (
	"context"
	"fmt"

	"github.com/Azure/ARO-HCP/tooling/release-explorer/pkg/client/list"
	"github.com/Azure/ARO-HCP/tooling/release-explorer/pkg/output"
	"github.com/spf13/cobra"
)

var (
	outputFormat string
	useLocalTime bool
)

func NewCommand() (*cobra.Command, error) {

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List all deployments to an environment (alias: ls)",
		Long: `List all deployments to an environment in a given period.
	
	Examples:
	  release-explorer list
	  release-explorer ls -e int --since 1w
	  release-explorer list --since 2025-11-01 --until 2025-11-15
	  release-explorer list --since 30d -o json --limit 5
	  release-explorer list --count`,
		SilenceUsage: true,
	}

	rawOptions := list.DefaultOptions()
	if err := rawOptions.BindOptions(cmd); err != nil {
		return nil, fmt.Errorf("failed to bind options: %w", err)
	}

	//extra flags
	cmd.Flags().StringVarP(&outputFormat, "output", "o", "", "Output format (json, yaml)")
	cmd.Flags().BoolVar(&useLocalTime, "local-time", false, "Use local time for output")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		// Validate extra flags
		switch outputFormat {
		case "", "json", "yaml":
		default:
			return fmt.Errorf("invalid output format: %s", outputFormat)
		}

		rawOptions.IncludeComponents = rawOptions.IncludeComponents || outputFormat == "json" || outputFormat == "yaml"

		validatedOptions, err := rawOptions.Validate()
		if err != nil {
			return fmt.Errorf("invalid options: %w", err)
		}

		options, err := validatedOptions.Complete()
		if err != nil {
			return fmt.Errorf("failed to complete options: %w", err)
		}

		return runList(cmd.Context(), options, outputFormat, useLocalTime)
	}

	return cmd, nil

}

func runList(ctx context.Context, opts *list.Options, format string, localTime bool) error {

	// List deployments
	deployments, err := opts.ListReleaseDeployments(ctx)
	if err != nil {
		return fmt.Errorf("failed to list deployments: %w", err)
	}

	if len(deployments) == 0 {
		if opts.PipelineRevision != "" || opts.SourceRevision != "" {
			return fmt.Errorf("no deployments found in '%s' matching the specified revision filters", opts.Environment)
		}
		return fmt.Errorf("no deployments found in '%s' between %s and %s.\nMaybe try a longer --since, e.g.\n  release-explorer list -e %s --since 'last month'",
			opts.Environment, opts.Since, opts.Until, opts.Environment)
	}

	output, err := output.FormatOutput(deployments, format, localTime, opts.IncludeComponents)
	if err != nil {
		return fmt.Errorf("failed to format output: %w", err)
	}
	fmt.Println(output)

	return nil
}
