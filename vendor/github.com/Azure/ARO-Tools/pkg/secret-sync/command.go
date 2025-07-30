package secretsync

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-Tools/pkg/secret-sync/populate"
	"github.com/Azure/ARO-Tools/pkg/secret-sync/register"
)

func NewCommand() (*cobra.Command, error) {
	cmd := &cobra.Command{
		Use:           "secrets",
		Short:         "Manage encrypted content.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	commands := []func() (*cobra.Command, error){
		populate.NewCommand,
		register.NewCommand,
	}
	for _, newCmd := range commands {
		c, err := newCmd()
		if err != nil {
			return nil, fmt.Errorf("failed to create subcommand: %w", err)
		}
		cmd.AddCommand(c)
	}

	return cmd, nil
}
