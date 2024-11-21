package inspect

import (
	"context"

	"github.com/spf13/cobra"
)

func NewCommand() (*cobra.Command, error) {
	opts := DefaultOptions()
	cmd := &cobra.Command{
		Use:   "inspect",
		Short: "inspect aspects of a pipeline.yaml file",
		Long:  "inspect aspects of a pipeline.yaml file",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInspect(cmd.Context(), opts)
		},
	}
	if err := BindOptions(opts, cmd); err != nil {
		return nil, err
	}
	return cmd, nil
}

func runInspect(ctx context.Context, opts *RawInspectOptions) error {
	validated, err := opts.Validate()
	if err != nil {
		return err
	}
	completed, err := validated.Complete()
	if err != nil {
		return err
	}
	return completed.RunInspect(ctx)
}
