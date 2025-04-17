package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/dusted-go/logging/prettylog"

	"github.com/Azure/ARO-HCP/tooling/pipeline-documentation/cmd/overview"
)

func main() {
	logger := createLogger(0)

	// Create a root context with the logger and signal handling
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var logVerbosity int

	cmd := &cobra.Command{
		Use:              "document",
		Short:            "document",
		Long:             "document",
		SilenceUsage:     true,
		TraverseChildren: true,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			ctx = logr.NewContext(ctx, createLogger(logVerbosity))
			cmd.SetContext(ctx)
		},
		CompletionOptions: cobra.CompletionOptions{
			HiddenDefaultCmd: true,
		},
	}

	cmd.PersistentFlags().IntVarP(&logVerbosity, "verbosity", "v", 0, "set the verbosity level")

	commands := []func() (*cobra.Command, error){
		overview.NewCommand,
	}
	for _, newCmd := range commands {
		c, err := newCmd()
		if err != nil {
			logger.Error(err, "failed to create command", "command", newCmd)
		}
		cmd.AddCommand(c)
	}

	cmd.SetHelpCommand(&cobra.Command{Hidden: true})

	if err := cmd.Execute(); err != nil {
		logger.Error(err, "command failed")
		os.Exit(1)
	}
}

func createLogger(verbosity int) logr.Logger {
	prettyHandler := prettylog.NewHandler(&slog.HandlerOptions{
		Level:       slog.Level(verbosity * -1),
		AddSource:   false,
		ReplaceAttr: nil,
	})
	return logr.FromSlogHandler(prettyHandler)
}
