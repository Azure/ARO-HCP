package generate

import (
	"log"

	"github.com/spf13/cobra"
)

func NewCommand() *cobra.Command {
	opts := DefaultGenerationOptions()
	cmd := &cobra.Command{
		Use:   "generate",
		Short: "generate",
		Long:  "generate",
		RunE: func(cmd *cobra.Command, args []string) error {
			return generate(opts)
		},
	}
	if err := BindGenerationOptions(opts, cmd); err != nil {
		log.Fatal(err)
	}
	return cmd
}

func generate(opts *RawGenerationOptions) error {
	validated, err := opts.Validate()
	if err != nil {
		return err
	}
	completed, err := validated.Complete()
	if err != nil {
		return err
	}
	return completed.ExecuteTemplate()
}
