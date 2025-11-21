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
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/admin/server/cmd/server"
	"github.com/Azure/ARO-HCP/admin/server/interrupts"
)

func main() {
	logger := createLogger(0)
	logger.Info(fmt.Sprintf("aro-hcp-admin (%s) starting...", version()))

	var logVerbosity int
	// Create a root context with the logger and signal handling
	ctx := interrupts.Context()

	cmd := &cobra.Command{
		Use:           "aro-hcp-admin",
		Short:         "Operate on ARO release artifacts.",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			ctx = logr.NewContext(ctx, createLogger(logVerbosity))
			cmd.SetContext(ctx)
		},
	}

	cmd.PersistentFlags().IntVarP(&logVerbosity, "verbosity", "v", 0, "set the verbosity level")

	commands := []func() (*cobra.Command, error){
		server.NewCommand,
	}
	for _, newCmd := range commands {
		c, err := newCmd()
		if err != nil {
			logger.Error(err, "Failed to create subcommand.")
			os.Exit(1)
		}
		cmd.AddCommand(c)
	}

	if err := cmd.Execute(); err != nil {
		logger.Error(err, "Command failed.")
		os.Exit(1)
	}
}

func createLogger(verbosity int) logr.Logger {
	handlerOptions := slog.HandlerOptions{
		Level: slog.Level(verbosity * -1),
	}
	handler := slog.NewJSONHandler(os.Stdout, &handlerOptions)
	return logr.FromSlogHandler(handler)
}

func version() string {
	version := "unknown"
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				version = setting.Value
				break
			}
		}
	}

	return version
}
