package server

import (
	"fmt"

	"github.com/spf13/cobra"
)

func NewCommand() (*cobra.Command, error) {
	cmd := &cobra.Command{
		Use:           "serve",
		Short:         "Serve the ARO HCP Admin API",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	opts := DefaultOptions()
	if err := opts.BindOptions(cmd); err != nil {
		return nil, fmt.Errorf("failed to bind options: %w", err)
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		validated, err := opts.Validate()
		if err != nil {
			return err
		}
		completed, err := validated.Complete(cmd.Context())
		if err != nil {
			return err
		}
		return completed.Run(cmd.Context())
	}

	return cmd, nil
}
