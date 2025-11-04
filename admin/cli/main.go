package main

// Copyright 2025 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/dusted-go/logging/prettylog"
	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/admin/cli/cmd/base"
)

func main() {
	logger := createLogger(0)
	// Create a root context with signal handling
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var logVerbosity int

	cmd := &cobra.Command{
		Use:           "aro-hcp-admin-cli",
		Short:         "Execute Admin API endpoints",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			ctx = logr.NewContext(ctx, createLogger(logVerbosity))
			cmd.SetContext(ctx)
		},
		CompletionOptions: cobra.CompletionOptions{
			HiddenDefaultCmd: true,
		},
	}
	cmd.PersistentFlags().IntVarP(&logVerbosity, "verbosity", "v", 0, "set the verbosity level")

	// Add subcommands
	subcommands := []func() (*cobra.Command, error){
		base.NewAuthTestCommand,
	}

	for _, newCmd := range subcommands {
		subCmd, err := newCmd()
		if err != nil {
			logger.Error(err, "failed to create command")
			os.Exit(1)
		}
		cmd.AddCommand(subCmd)
	}

	cmd.SetHelpCommand(&cobra.Command{Hidden: true})

	if err := cmd.ExecuteContext(ctx); err != nil {
		logger.Error(err, "failed to create command")
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
