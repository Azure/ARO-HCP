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

package server

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/internal/signal"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func NewCommand() (*cobra.Command, error) {
	cmd := &cobra.Command{
		Use:           "serve",
		Short:         "Serve the ARO HCP Admin API",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	opts := DefaultOptions()
	if err := opts.BindOptions(cmd); err != nil {
		return nil, fmt.Errorf("failed to bind options: %w", err)
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		validated, err := opts.Validate()
		if err != nil {
			return err
		}

		// Create a logr.Logger and add it to context for use throughout the application
		handlerOptions := &slog.HandlerOptions{Level: slog.Level(validated.LogVerbosity * -1)}
		logrLogger := logr.FromSlogHandler(slog.NewJSONHandler(os.Stdout, handlerOptions))
		ctx := signal.SetupSignalContext()
		ctx = utils.ContextWithLogger(ctx, logrLogger)

		completed, err := validated.Complete(ctx)
		if err != nil {
			return err
		}
		return completed.Run(ctx)
	}

	return cmd, nil
}
