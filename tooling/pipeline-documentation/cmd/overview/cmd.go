package overview

import (
	"context"

	"github.com/spf13/cobra"
)

func NewCommand() (*cobra.Command, error) {
	opts := DefaultGenerationOptions()
	cmd := &cobra.Command{
		Use:   "overview",
		Short: "overview",
		Long:  "overview",
		RunE: func(cmd *cobra.Command, args []string) error {
			return generate(cmd.Context(), opts)
		},
	}
	if err := BindGenerationOptions(opts, cmd); err != nil {
		return nil, err
	}
	return cmd, nil
}

func generate(ctx context.Context, opts *RawGenerationOptions) error {
	validated, err := opts.Validate()
	if err != nil {
		return err
	}
	completed, err := validated.Complete()
	if err != nil {
		return err
	}
	return completed.GenerateOverview()
}
