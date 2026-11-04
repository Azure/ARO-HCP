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

package prowjobexecutor

import (
	"context"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
)

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
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return err
	}

	validated, err := opts.Validate(ctx)
	if err != nil {
		return err
	}

	completed, err := validated.Complete(ctx)
	if err != nil {
		return err
	}

	logger.Info("Starting Prow job execution", "jobName", completed.ProwJobName, "cloud", completed.Cloud, "environment", completed.Environment, "region", completed.Region)

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
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return err
	}

	validated, err := opts.Validate(ctx)
	if err != nil {
		return err
	}

	completed, err := validated.Complete(ctx)
	if err != nil {
		return err
	}

	logger.Info("Starting Prow job monitoring", "jobExecutionID", completed.JobExecutionID)

	return completed.Monitor(ctx, logger)
}
