package gatherlogs

import (
	"context"
	"log/slog"

	"github.com/dusted-go/logging/prettylog"
	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
)

func NewCommand() (*cobra.Command, error) {
	var logVerbosity int

	opts := DefaultOptions()
	cmd := &cobra.Command{
		Use:           "gatherlogs",
		Short:         "Gather logs for HCP created during test.",
		SilenceErrors: true,
		SilenceUsage:  true,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			ctx := logr.NewContext(cmd.Context(), createLogger(logVerbosity))
			cmd.SetContext(ctx)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return GatherLogs(cmd.Context(), opts)
		},
	}
	cmd.PersistentFlags().IntVarP(&logVerbosity, "verbosity", "v", 0, "set the verbosity level")
	if err := BindOptions(opts, cmd); err != nil {
		return nil, err
	}
	return cmd, nil
}

func GatherLogs(ctx context.Context, opts *RawOptions) error {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return err
	}

	validated, err := opts.Validate()
	if err != nil {
		return err
	}
	completed, err := validated.Complete(logger)
	if err != nil {
		return err
	}
	return completed.Gather(ctx)
}

func createLogger(verbosity int) logr.Logger {
	level := slog.Level(verbosity * -1)
	prettyHandler := prettylog.NewHandler(&slog.HandlerOptions{
		Level:       level,
		AddSource:   false,
		ReplaceAttr: nil,
	})
	slog.SetDefault(slog.New(prettyHandler))
	slog.SetLogLoggerLevel(level)
	return logr.FromSlogHandler(prettyHandler)
}
