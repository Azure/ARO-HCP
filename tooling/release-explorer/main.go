package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/Azure/ARO-HCP/tooling/release-explorer/cmd/lastcmd"
	"github.com/Azure/ARO-HCP/tooling/release-explorer/cmd/listcmd"
	"github.com/dusted-go/logging/prettylog"
	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
)

func main() {
	logger := createLogger(0)
	logger.Info(fmt.Sprintf("release-explorer starting..."))

	cmd := &cobra.Command{
		Use:           "release-explorer",
		Short:         "Explore ARO release artifacts.",
		SilenceUsage:  true,
		SilenceErrors: true,
		CompletionOptions: cobra.CompletionOptions{
			DisableDefaultCmd: true,
		},
	}

	cmd.SetContext(logr.NewContext(context.Background(), logger))

	listCmd, err := listcmd.NewCommand()
	if err != nil {
		logger.Error(err, "failed to create list command")
		os.Exit(1)
	}
	cmd.AddCommand(listCmd)

	lastCmd, err := lastcmd.NewCommand()
	if err != nil {
		logger.Error(err, "failed to create last command")
		os.Exit(1)
	}
	cmd.AddCommand(lastCmd)

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
