package lastcmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/Azure/ARO-HCP/tooling/release-explorer/pkg/client/last"
	"github.com/Azure/ARO-HCP/tooling/release-explorer/pkg/client/types"
	"github.com/Azure/ARO-HCP/tooling/release-explorer/pkg/output"
	"github.com/spf13/cobra"
)

var (
	outputFormat string
	useLocalTime bool
)

func NewCommand() (*cobra.Command, error) {

	cmd := &cobra.Command{
		Use:   "last",
		Short: "Get the last deployment to an environment",
		Long: `Get the most recent deployment to an environment.

	Examples:
	release-explorer last
	release-explorer last -e int`,
		SilenceUsage: true,
	}

	rawOptions := last.DefaultOptions()
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

		return runLast(cmd.Context(), options, outputFormat, useLocalTime)
	}

	return cmd, nil

}

func runLast(ctx context.Context, opts *last.Options, format string, localTime bool) error {

	deployment, err := opts.LastReleaseDeployment(ctx)
	if err != nil {
		// Distinguish between "no results" and real errors.
		if errors.Is(err, last.ErrNoDeploymentsFound) {
			lo := opts.ListOptions
			if lo.PipelineRevision != "" || lo.SourceRevision != "" {
				return fmt.Errorf("no deployments found in '%s' matching the specified revision filters", lo.Environment)
			}
			return fmt.Errorf("no deployments found in '%s' between %s and %s.\nMaybe try a longer --max-lookback, e.g.\n  release-explorer last --environment %s --max-lookback 24w",
				lo.Environment, lo.Since, lo.Until, lo.Environment)
		}
		return fmt.Errorf("failed to find last deployment: %w", err)
	}

	out, err := output.FormatOutput([]*types.ReleaseDeployment{deployment}, format, localTime, opts.ListOptions.IncludeComponents)
	if err != nil {
		return fmt.Errorf("failed to format output: %w", err)
	}
	fmt.Println(out)

	return nil
}
