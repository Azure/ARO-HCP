package lastcmd

import (
	"github.com/spf13/cobra"
)

func NewCommand() (*cobra.Command, error) {
	opts := DefaultOptions()
	cmd := &cobra.Command{
		Use:           "last",
		Short:         "Get the last deployment to an environment",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	if err := BindOptions(opts, cmd); err != nil {
		return nil, err
	}

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		validated, err := opts.Validate()
		if err != nil {
			return err
		}
		completed, err := validated.Complete()
		if err != nil {
			return err
		}

		return completed.Run(cmd.Context())
	}

	return cmd, nil
}
