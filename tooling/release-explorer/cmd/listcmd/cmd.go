package listcmd

import (
	"fmt"

	"github.com/spf13/cobra"
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

	rawOptions := DefaultOptions()
	if err := BindOptions(rawOptions, cmd); err != nil {
		return nil, fmt.Errorf("failed to bind options: %w", err)
	}

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		validatedOptions, err := rawOptions.Validate()
		if err != nil {
			return fmt.Errorf("invalid options: %w", err)
		}

		options, err := validatedOptions.Complete()
		if err != nil {
			return fmt.Errorf("failed to complete options: %w", err)
		}

		return options.Run(cmd.Context())
	}

	return cmd, nil

}
