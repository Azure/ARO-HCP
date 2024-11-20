package inspect

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
		Use:   "inspect",
		Short: "inspect aspects of a pipeline.yaml file",
		Long:  "inspect aspects of a pipeline.yaml file",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			return runInspect(ctx, opts)
		},
	}
	if err := BindOptions(opts, cmd); err != nil {
		log.Fatal(err)
	}
	return cmd
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
