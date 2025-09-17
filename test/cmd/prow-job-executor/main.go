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

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/test/util/log"
)

func main() {
	logger := log.GetLogger()

	// Create a root context with signal handling
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd := &cobra.Command{
		Use:           "prow-job-executor",
		Short:         "Execute and monitor Prow jobs for service validation",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// Add subcommands
	subcommands := []func() (*cobra.Command, error){
		NewExecuteCommand,
		NewMonitorCommand,
	}

	for _, newCmd := range subcommands {
		subCmd, err := newCmd()
		if err != nil {
			logger.WithError(err).Error("failed to create subcommand")
			os.Exit(1)
		}
		cmd.AddCommand(subCmd)
	}

	if err := cmd.ExecuteContext(ctx); err != nil {
		logger.WithError(err).Error("command failed")
		os.Exit(1)
	}
}

func NewExecuteCommand() (*cobra.Command, error) {
	opts := DefaultExecuteOptions()
	cmd := &cobra.Command{
		Use:           "execute",
		Short:         "Execute a Prow job and monitor its completion",
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExecute(cmd.Context(), opts)
		},
	}

	if err := opts.BindFlags(cmd); err != nil {
		return nil, err
	}

	return cmd, nil
}

func runExecute(ctx context.Context, opts *RawExecuteOptions) error {
	logger := log.GetLogger()

	validated, err := opts.Validate(ctx)
	if err != nil {
		return err
	}

	completed, err := validated.Complete(ctx)
	if err != nil {
		return err
	}

	logger.WithFields(map[string]interface{}{
		"jobName": completed.ProwJobName,
		"region":  completed.Region,
	}).Info("Starting Prow job execution")

	return completed.Execute(ctx)
}

func NewMonitorCommand() (*cobra.Command, error) {
	opts := DefaultMonitorOptions()
	cmd := &cobra.Command{
		Use:           "monitor",
		Short:         "Monitor an existing Prow job by execution ID",
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMonitor(cmd.Context(), opts)
		},
	}

	if err := opts.BindFlags(cmd); err != nil {
		return nil, err
	}

	return cmd, nil
}

func runMonitor(ctx context.Context, opts *RawMonitorOptions) error {
	logger := log.GetLogger()

	validated, err := opts.Validate(ctx)
	if err != nil {
		return err
	}

	completed, err := validated.Complete(ctx)
	if err != nil {
		return err
	}

	logger.WithField("jobExecutionID", completed.JobExecutionID).Info("Starting Prow job monitoring")

	return completed.Monitor(ctx)
}
