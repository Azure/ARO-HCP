package pipeline

import (
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/templatize/cmd/pipeline/inspect"
	"github.com/Azure/ARO-HCP/tooling/templatize/cmd/pipeline/run"
)

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:              "pipeline",
		Short:            "pipeline",
		Long:             "pipeline",
		SilenceUsage:     true,
		TraverseChildren: true,
		CompletionOptions: cobra.CompletionOptions{
			HiddenDefaultCmd: true,
		},
	}
	cmd.AddCommand(run.NewCommand())
	cmd.AddCommand(inspect.NewCommand())

	return cmd
}
