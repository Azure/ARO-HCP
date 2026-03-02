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
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/dusted-go/logging/prettylog"
	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	datadumptogit "github.com/Azure/ARO-HCP/tooling/hcpctl/cmd/datadump-to-git"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/cmd/hcp"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/cmd/kubelogin"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/cmd/mc"
	mustgather "github.com/Azure/ARO-HCP/tooling/hcpctl/cmd/must-gather"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/cmd/sc"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/cmd/version"
)

// Command group IDs
const (
	mainGroupID   = "main"
	helperGroupID = "helper"
)

func main() {
	logger := createLogger(0)

	// Create a root context with the logger and signal handling
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var logVerbosity int

	cmd := &cobra.Command{
		Use:   "hcpctl",
		Short: "ARO-HCP operations CLI",
		Long: `hcpctl provides operational tools for ARO-HCP management.

This tool includes commands for accessing ARO-HCP infrastructure components
and hosted control plane services for operational and emergency scenarios.`,
		SilenceUsage:     true,
		SilenceErrors:    true,
		TraverseChildren: true,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			ctx = logr.NewContext(ctx, createLogger(logVerbosity))
			cmd.SetContext(ctx)
		},
		CompletionOptions: cobra.CompletionOptions{
			HiddenDefaultCmd: true,
		},
	}

	cmd.PersistentFlags().IntVarP(&logVerbosity, "verbosity", "v", 0, "set the verbosity level")

	// Define command groups
	cmd.AddGroup(&cobra.Group{
		ID:    mainGroupID,
		Title: "Main Commands:",
	})
	cmd.AddGroup(&cobra.Group{
		ID:    helperGroupID,
		Title: "Helper Commands:",
	})

	// Add main subcommands
	mainCommands := []func(string) (*cobra.Command, error){
		mc.NewCommand,
		sc.NewCommand,
		hcp.NewCommand,
		mustgather.NewCommand,
		datadumptogit.NewCommand,
	}
	for _, newCmd := range mainCommands {
		c, err := newCmd(mainGroupID)
		if err != nil {
			logger.Error(err, "failed to create command")
			os.Exit(1)
		}
		cmd.AddCommand(c)
	}

	// Add helper subcommands
	helperCommands := []func(string) (*cobra.Command, error){
		kubelogin.NewCommand,
		version.NewCommand,
	}
	for _, newCmd := range helperCommands {
		c, err := newCmd(helperGroupID)
		if err != nil {
			logger.Error(err, "failed to create command")
			os.Exit(1)
		}
		cmd.AddCommand(c)
	}

	cmd.SetHelpCommand(&cobra.Command{Hidden: true})

	if err := cmd.ExecuteContext(ctx); err != nil {
		logger.Error(err, "command failed")
		os.Exit(1)
	}
}

func createLogger(verbosity int) logr.Logger {
	prettyHandler := prettylog.NewHandler(&slog.HandlerOptions{
		Level:       slog.Level(verbosity * -1),
		AddSource:   false,
		ReplaceAttr: nil,
	})
	return logr.FromSlogHandler(prettyHandler)
}
