package registration

import (
	"fmt"
	"os"
	"os/signal"

	"github.com/spf13/cobra"
)

func NewCommand() (*cobra.Command, error) {
	cmd := &cobra.Command{
		Use:           "register",
		Short:         "Ensure providers and features are registered for a subscription.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	opts := DefaultOptions()
	if err := BindOptions(opts, cmd); err != nil {
		return nil, fmt.Errorf("failed to bind options: %w", err)
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt)
		defer cancel()

		validated, err := opts.Validate()
		if err != nil {
			return err
		}
		completed, err := validated.Complete()
		if err != nil {
			return err
		}
		return completed.Register(ctx)
	}

	return cmd, nil
}
