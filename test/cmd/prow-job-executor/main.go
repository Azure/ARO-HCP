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
	"os"
	"os/signal"
	"syscall"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	prowjobexecutor "github.com/Azure/ARO-Tools/tools/prow-job-executor"

	"github.com/Azure/ARO-HCP/test/pkg/logger"
)

func main() {
	setupLog := logger.NewWithVerbosity(0)

	// Create a root context with signal handling
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var logVerbosity int

	cmd := &cobra.Command{
		Use:           "prow-job-executor",
		Short:         "Execute and monitor Prow jobs for service validation",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			ctx = logr.NewContext(ctx, logger.NewWithVerbosity(logVerbosity))
			cmd.SetContext(ctx)
		},
	}

	cmd.PersistentFlags().IntVarP(&logVerbosity, "verbosity", "v", 0, "set the verbosity level")

	// Add subcommands
	subcommands := []func() (*cobra.Command, error){
		prowjobexecutor.NewExecuteCommand,
		prowjobexecutor.NewMonitorCommand,
	}

	for _, newCmd := range subcommands {
		subCmd, err := newCmd()
		if err != nil {
			setupLog.Error(err, "failed to create subcommand")
			os.Exit(1)
		}
		cmd.AddCommand(subCmd)
	}

	if err := cmd.ExecuteContext(ctx); err != nil {
		setupLog.Error(err, "command failed")
		os.Exit(1)
	}
}
