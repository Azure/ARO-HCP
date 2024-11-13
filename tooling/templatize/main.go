package main

import (
	"log"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/templatize/cmd/generate"
	"github.com/Azure/ARO-HCP/tooling/templatize/cmd/inspect"
	"github.com/Azure/ARO-HCP/tooling/templatize/cmd/run"
)

func main() {
	cmd := &cobra.Command{
		Use:              "templatize",
		Short:            "templatize",
		Long:             "templatize",
		SilenceUsage:     true,
		TraverseChildren: true,
		CompletionOptions: cobra.CompletionOptions{
			HiddenDefaultCmd: true,
		},
	}
	cmd.AddCommand(generate.NewCommand())
	cmd.AddCommand(inspect.NewCommand())
	cmd.AddCommand(run.NewCommand())
	cmd.SetHelpCommand(&cobra.Command{Hidden: true})

	if err := cmd.Execute(); err != nil {
		log.Fatal(err)
	}
}
