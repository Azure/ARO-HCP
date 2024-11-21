package run

import (
	"context"

	"github.com/spf13/cobra"
)

func NewCommand() (*cobra.Command, error) {
	opts := DefaultOptions()
	cmd := &cobra.Command{
		Use:   "run",
		Short: "run a pipeline.yaml file towards an Azure Resourcegroup / AKS cluster",
		Long:  "run a pipeline.yaml file towards an Azure Resourcegroup / AKS cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPipeline(cmd.Context(), opts)
		},
	}
	if err := BindOptions(opts, cmd); err != nil {
		return nil, err
	}
	return cmd, nil
}

func runPipeline(ctx context.Context, opts *RawRunOptions) error {
	validated, err := opts.Validate()
	if err != nil {
		return err
	}
	completed, err := validated.Complete()
	if err != nil {
		return err
	}
	return completed.RunPipeline(ctx)
}
