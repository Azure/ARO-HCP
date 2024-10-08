package main

import (
	"log"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func DefaultGenerationOptions() *GenerationOptions {
	return &GenerationOptions{}
}

type GenerationOptions struct {
	Input  string
	Region string
	User   string
}

func BindGenerationOptions(opts *GenerationOptions, flags *pflag.FlagSet) {
	flags.StringVar(&opts.Input, "input", opts.Input, "input file path")
	flags.StringVar(&opts.Region, "region", opts.Region, "resources location")
	flags.StringVar(&opts.User, "user", opts.User, "unique user name")
}

func main() {
	cmd := &cobra.Command{}

	opts := DefaultGenerationOptions()
	BindGenerationOptions(opts, cmd.Flags())
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		println("Input:", opts.Input)
		println("Region:", opts.Region)
		println("User:", opts.User)

		// TODO: implement templatize tooling

		return nil
	}

	if err := cmd.Execute(); err != nil {
		log.Fatal(err)
	}
}
