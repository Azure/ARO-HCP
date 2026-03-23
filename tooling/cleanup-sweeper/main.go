// Copyright 2026 Microsoft Corporation
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

	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/cmd/root"
)

func main() {
	logger := createLogger(0)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var logVerbosity int

	cmd, err := root.NewCommand()
	if err != nil {
		logger.Error(err, "failed to create command")
		os.Exit(1)
	}

	cmd.TraverseChildren = true
	cmd.PersistentFlags().IntVarP(&logVerbosity, "verbosity", "v", 0, "set the verbosity level")
	cmd.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		ctx = logr.NewContext(ctx, createLogger(logVerbosity))
		cmd.SetContext(ctx)
	}

	if err := cmd.ExecuteContext(ctx); err != nil {
		logger.Error(err, "command failed")
		os.Exit(1)
	}
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
