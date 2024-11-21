package pipeline

import (
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/templatize/cmd/pipeline/inspect"
	"github.com/Azure/ARO-HCP/tooling/templatize/cmd/pipeline/run"
)

func NewCommand() (*cobra.Command, error) {
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

	commands := []func() (*cobra.Command, error){
		run.NewCommand,
		inspect.NewCommand,
	}
	for _, newCmd := range commands {
		c, err := newCmd()
		if err != nil {
			return nil, err
		}
		cmd.AddCommand(c)
	}

	return cmd, nil
}
