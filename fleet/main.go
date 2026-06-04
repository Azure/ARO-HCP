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

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/klog/v2"

	"github.com/Azure/ARO-HCP/fleet/cmd/controller"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/version"
)

func main() {
	utilruntime.PanicHandlers = append(utilruntime.PanicHandlers, utils.IncrementPanicMetrics)

	logger := createLogger(0)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var logVerbosity int

	cmd := &cobra.Command{
		Use:           "fleet",
		Short:         "Fleet management CLI for stamp and management cluster registration",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			logger := createLogger(logVerbosity)
			klog.SetLogger(logger)
			ctx = logr.NewContext(ctx, logger)
			cmd.SetContext(ctx)
		},
		CompletionOptions: cobra.CompletionOptions{
			HiddenDefaultCmd: true,
		},
	}
	cmd.Version = version.CommitSHA
	cmd.PersistentFlags().IntVarP(&logVerbosity, "verbosity", "v", 0, "set the verbosity level")

	subcommands := []func() (*cobra.Command, error){
		controller.NewControllerCommand,
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
		logger.Error(err, "command failed")
		os.Exit(1)
	}
}

func createLogger(verbosity int) logr.Logger {
	handlerOptions := &slog.HandlerOptions{
		Level:     slog.Level(verbosity * -1),
		AddSource: true,
	}
	return logr.FromSlogHandler(slog.NewJSONHandler(os.Stdout, handlerOptions))
}
