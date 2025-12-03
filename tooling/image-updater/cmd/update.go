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

package cmd

import (
	"log/slog"
	"os"

	"github.com/dusted-go/logging/prettylog"
	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/image-updater/internal/options"
)

func NewUpdateCommand() *cobra.Command {
	opts := options.DefaultUpdateOptions()

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update image digests from their source registries",
		Long: `Update reads the configuration file and fetches the latest image digests
from their source registries, then updates the target configuration files
with the new digests.

Use --dry-run to see what changes would be made without actually updating files.

Verbosity levels:
  (default)  Show only summary and errors
  -v         Show image updates and important operations
  -vv        Show detailed debug information including tag fetching`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUpdate(cmd, opts)
		},
	}

	if err := options.BindUpdateOptions(opts, cmd); err != nil {
		return nil
	}

	return cmd
}

func runUpdate(cmd *cobra.Command, opts *options.RawUpdateOptions) error {
	ctx := cmd.Context()

	// Adjust logger level based on verbosity
	var level slog.Level
	if opts.Verbose >= 2 {
		level = slog.LevelDebug
	} else if opts.Verbose == 1 {
		level = slog.LevelInfo // Keep detailed INFO logs
	} else {
		level = slog.LevelWarn // Only warnings and errors for normal mode
	}

	prettyHandler := prettylog.New(&slog.HandlerOptions{
		Level:       level,
		AddSource:   false,
		ReplaceAttr: nil,
	}, prettylog.WithDestinationWriter(os.Stderr))
	logger := logr.FromSlogHandler(prettyHandler)

	// Update context with the configured logger
	ctx = logr.NewContext(ctx, logger)

	validated, err := opts.Validate(ctx)
	if err != nil {
		return err
	}

	completed, err := validated.Complete(ctx)
	if err != nil {
		return err
	}

	return completed.UpdateImages(ctx)
}
