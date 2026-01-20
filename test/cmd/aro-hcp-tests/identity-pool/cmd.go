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

package identitypool

import (
	"context"
	"log/slog"
	"os"
	"os/signal"

	"github.com/dusted-go/logging/prettylog"
	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
)

func NewCommand() (*cobra.Command, error) {
	var logVerbosity int

	cmd := &cobra.Command{
		Use:           "identity-pool",
		Short:         "Manage the pooled managed identities used by e2e tests.",
		SilenceErrors: true,
		SilenceUsage:  true,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			ctx := logr.NewContext(cmd.Context(), createLogger(logVerbosity))
			cmd.SetContext(ctx)
		},
	}

	cmd.PersistentFlags().IntVarP(&logVerbosity, "verbosity", "v", 0, "set the verbosity level")
	cmd.AddCommand(newApplyCommand())

	return cmd, nil
}

func newApplyCommand() *cobra.Command {
	opts := DefaultApplyOptions()

	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Apply the managed identity pool ARM deployment stack.",
		Long: `Apply the managed identity pool ARM deployment stack.

This command applies the managed identity pool ARM deployment stack to the Azure subscription for the given environment..

An identity pool is a number of resource groups, determined by the pool size, containing the managed identities required to
create a single HCP cluster.

The identity pool must be kept in sync with the Boskos leasing server configuration 
https://github.com/openshift/release/blob/master/core-services/prow/02_config/generate-boskos.py#L687-L691

The identity pool is applied to the Azure subscription for the given environment.
`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer cancel()

			logger, err := getLogger(ctx)
			if err != nil {
				return err
			}
			ctx = logr.NewContext(ctx, logger)

			validated, err := opts.Validate()
			if err != nil {
				return err
			}

			completed, err := validated.Complete(ctx)
			if err != nil {
				return err
			}

			return completed.Run(ctx)
		},
	}

	BindApplyOptions(opts, cmd)
	return cmd
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

func getLogger(ctx context.Context) (logr.Logger, error) {
	return logr.FromContext(ctx)
}
