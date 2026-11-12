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

// Command depctl runs the ARO-HCP dependency automation lifecycle locally.
//
// The commands are thin adapters over reusable workflow code. Only commands
// whose guards exist are registered: no live publication, remote mutation or
// scheduled execution path is reachable from this binary.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/dusted-go/logging/prettylog"
	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/depctl/cmd/apply"
	"github.com/Azure/ARO-HCP/tooling/depctl/cmd/evidence"
	"github.com/Azure/ARO-HCP/tooling/depctl/cmd/guard"
	"github.com/Azure/ARO-HCP/tooling/depctl/cmd/inventory"
	"github.com/Azure/ARO-HCP/tooling/depctl/cmd/maintenance"
	"github.com/Azure/ARO-HCP/tooling/depctl/cmd/plan"
	"github.com/Azure/ARO-HCP/tooling/depctl/cmd/proposal"
	"github.com/Azure/ARO-HCP/tooling/depctl/cmd/run"
	"github.com/Azure/ARO-HCP/tooling/depctl/cmd/verify"
	"github.com/Azure/ARO-HCP/tooling/depctl/cmd/version"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/cli"
	"github.com/Azure/ARO-HCP/tooling/depctl/pkg/depctl"
)

// Command group IDs.
const (
	mainGroupID   = "main"
	helperGroupID = "helper"
)

// Runs the CLI and exits with its documented code.
func main() {
	logger := createLogger(0)

	// Create a root context with the logger and signal handling.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var logVerbosity int

	cmd := &cobra.Command{
		Use:   "depctl",
		Short: "ARO-HCP dependency automation lifecycle",
		Long: `depctl runs the ARO-HCP dependency automation lifecycle locally.

It discovers pinned dependencies, plans explicit targets, prepares and validates
an isolated candidate against the repository's own generators and consumers, and
investigates failing owned proposals with public evidence. Publication, hold
release and scheduling require an authorized execution integration and are not
reachable from this binary.`,
		SilenceUsage:     true,
		SilenceErrors:    true,
		TraverseChildren: true,
		Args:             cli.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
		PersistentPreRun: func(cmd *cobra.Command, _ []string) {
			ctx = logr.NewContext(ctx, createLogger(logVerbosity))
			cmd.SetContext(ctx)
		},
		CompletionOptions: cobra.CompletionOptions{
			HiddenDefaultCmd: true,
		},
	}

	cmd.PersistentFlags().IntVarP(&logVerbosity, "verbosity", "v", 0, "set the verbosity level")

	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return depctl.Errorf(depctl.ExitInvalid, "%v", err)
	})

	cmd.AddGroup(&cobra.Group{ID: mainGroupID, Title: "Main Commands:"})
	cmd.AddGroup(&cobra.Group{ID: helperGroupID, Title: "Helper Commands:"})

	mainCommands := []func(string) (*cobra.Command, error){
		inventory.NewCommand,
		plan.NewCommand,
		apply.NewCommand,
		verify.NewCommand,
		proposal.NewCommand,
		run.NewCommand,
		guard.NewCommand,
		maintenance.NewCommand,
		evidence.NewCommand,
	}
	for _, newCommand := range mainCommands {
		command, err := newCommand(mainGroupID)
		if err != nil {
			logger.Error(err, "failed to create command")
			os.Exit(1)
		}
		cmd.AddCommand(command)
	}

	helperCommands := []func(string) (*cobra.Command, error){
		version.NewCommand,
	}
	for _, newCommand := range helperCommands {
		command, err := newCommand(helperGroupID)
		if err != nil {
			logger.Error(err, "failed to create command")
			os.Exit(1)
		}
		cmd.AddCommand(command)
	}

	cmd.SetHelpCommand(&cobra.Command{Hidden: true})

	if err := cmd.ExecuteContext(ctx); err != nil {
		var exit *depctl.ExitError
		if errors.As(err, &exit) {
			fmt.Fprintln(os.Stderr, exit.Message)
			os.Exit(exit.Code)
		}
		// Command resolution failures are invalid input, not internal errors.
		message := err.Error()
		if strings.HasPrefix(message, "unknown command") ||
			strings.HasPrefix(message, "unknown flag") ||
			strings.HasPrefix(message, "unknown shorthand flag") {
			fmt.Fprintln(os.Stderr, message)
			os.Exit(depctl.ExitInvalid)
		}
		logger.Error(err, "command failed")
		os.Exit(depctl.ExitInternal)
	}
}

// Creates the structured logger used by every command.
func createLogger(verbosity int) logr.Logger {
	prettyHandler := prettylog.New(&slog.HandlerOptions{
		Level:       slog.Level(verbosity * -1),
		AddSource:   false,
		ReplaceAttr: nil,
	}, prettylog.WithDestinationWriter(os.Stderr), prettylog.WithColor(), prettylog.WithOutputEmptyAttrs())
	return logr.FromSlogHandler(prettyHandler)
}
