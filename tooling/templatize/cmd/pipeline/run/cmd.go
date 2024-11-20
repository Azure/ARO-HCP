package run

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

func NewCommand() *cobra.Command {
	opts := DefaultOptions()
	cmd := &cobra.Command{
		Use:   "run",
		Short: "run a pipeline.yaml file towards an Azure Resourcegroup / AKS cluster",
		Long:  "run a pipeline.yaml file towards an Azure Resourcegroup / AKS cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			return runPipeline(ctx, opts)
		},
	}
	if err := BindOptions(opts, cmd); err != nil {
		log.Fatal(err)
	}
	return cmd
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