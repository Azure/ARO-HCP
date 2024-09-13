package maestro

import (
	"os"

	"github.com/Azure/ARO-HCP/tooling/generate-config/cmd/maestro/infrastructure"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/generate-config/cmd/common"
)

func NewCommand(opts *common.RawPrimitiveOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "maestro",
		Short:        "Generates configuration for Maestro deployments.",
		SilenceUsage: true,
		Run: func(cmd *cobra.Command, args []string) {
			_ = cmd.Help()
			os.Exit(1)
		},
	}

	cmd.AddCommand(infrastructure.NewCommand(opts))

	return cmd
}
