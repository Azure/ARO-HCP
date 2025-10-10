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

package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/dusted-go/logging/prettylog"
	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/image-updater/cmd"
)

func main() {
	logger := createLogger()

	// Create a root context with the logger and signal handling
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	rootCmd := &cobra.Command{
		Use:   "image-updater",
		Short: "Updates container image digests in ARO-HCP configuration files",
		Long: `image-updater automatically fetches the latest image digests from container registries
and updates the corresponding digests in ARO-HCP configuration files.

This tool helps maintain up-to-date container images by automating the process of
checking for new image versions and updating configuration files accordingly.`,
		SilenceUsage:     true,
		SilenceErrors:    true,
		TraverseChildren: true,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			ctx = logr.NewContext(ctx, logger)
			cmd.SetContext(ctx)
		},
	}

	rootCmd.AddCommand(cmd.NewUpdateCommand())

	if err := rootCmd.Execute(); err != nil {
		logger.Error(err, "command failed")
		os.Exit(1)
	}
}

func createLogger() logr.Logger {
	prettyHandler := prettylog.New(&slog.HandlerOptions{
		Level:       slog.LevelInfo,
		AddSource:   false,
		ReplaceAttr: nil,
	}, prettylog.WithDestinationWriter(os.Stderr))
	return logr.FromSlogHandler(prettyHandler)
}
