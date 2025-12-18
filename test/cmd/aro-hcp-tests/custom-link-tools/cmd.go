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

package customlinktools

import (
	"context"
	"log/slog"

	"github.com/dusted-go/logging/prettylog"
	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
)

func NewCommand() (*cobra.Command, error) {
	var logVerbosity int

	opts := DefaultOptions()
	cmd := &cobra.Command{
		Use:           "custom-link-tools",
		Short:         "Generate HTML file that OCP's spyglass recognizes for display.",
		SilenceErrors: true,
		SilenceUsage:  true,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			ctx := logr.NewContext(cmd.Context(), createLogger(logVerbosity))
			cmd.SetContext(ctx)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return Visualize(cmd.Context(), opts)
		},
	}
	cmd.PersistentFlags().IntVarP(&logVerbosity, "verbosity", "v", 0, "set the verbosity level")
	if err := BindOptions(opts, cmd); err != nil {
		return nil, err
	}
	return cmd, nil
}

func Visualize(ctx context.Context, opts *RawOptions) error {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return err
	}

	validated, err := opts.Validate()
	if err != nil {
		return err
	}
	completed, err := validated.Complete(logger)
	if err != nil {
		return err
	}
	return completed.Run(ctx)
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
