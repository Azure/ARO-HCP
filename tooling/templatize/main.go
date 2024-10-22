package main

import (
	"log"
	"os"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/templatize/cmd/generate"
	"github.com/Azure/ARO-HCP/tooling/templatize/cmd/inspect"
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
		RunE: func(cmd *cobra.Command, args []string) error {
			err := cmd.Help()
			if err != nil {
				return err
			}
			os.Exit(1)
			return nil
		},
	}
	cmd.AddCommand(generate.NewCommand())
	cmd.AddCommand(inspect.NewCommand())

	if err := cmd.Execute(); err != nil {
		log.Fatal(err)
	}
}
