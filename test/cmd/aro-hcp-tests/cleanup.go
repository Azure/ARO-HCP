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

	"github.com/dusted-go/logging/prettylog"
	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/test/util/cleanup"
	"github.com/Azure/ARO-HCP/test/util/cleanup/resourcegroups"
)

func newCleanupCommand() *cobra.Command {

	opt := cleanup.NewBaseOptions()

	cmd := &cobra.Command{
		Use:          "cleanup",
		Short:        "Cleanup resources",
		SilenceUsage: true,
	}

	cmd.PersistentFlags().IntVarP(&opt.Verbosity, "verbosity", "v", opt.Verbosity, "Log verbosity level")

	cmd.AddCommand(newCleanupResourceGroupsCommand())

	cmd.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		ctx := logr.NewContext(cmd.Context(), createLogger(opt.Verbosity))
		cmd.SetContext(ctx)
	}
	return cmd
}

func newCleanupResourceGroupsCommand() *cobra.Command {

	cmd := &cobra.Command{
		Use:          "resource-groups",
		Short:        "Delete resource groups (explicit list or expired resource groups)",
		SilenceUsage: true,
	}

	opt := resourcegroups.NewOptions()
	cmd.Flags().StringArrayVar(&opt.ResourceGroups, "resource-group", opt.ResourceGroups, "Resource group to clean (repeat)")
	cmd.Flags().BoolVar(&opt.DeleteExpired, "expired", opt.DeleteExpired, "Delete all expired e2e resource groups")
	cmd.Flags().StringVar(&opt.EvaluationTime, "evaluation-time", opt.EvaluationTime, "Time at which to evaluate resource group expiration (RFC3339,defaults to current time)")
	cmd.Flags().BoolVar(&opt.DryRun, "dry-run", opt.DryRun, "Print which resource groups would be deleted without deleting")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt)
		defer cancel()
		return opt.Run(ctx)
	}

	return cmd
}

// TODO: delete when scripts have been migrated to use the new command
func newDeleteExpiredResourceGroupsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "delete-expired-resource-groups",
		Short:        "Delete expired e2e resource groups",
		SilenceUsage: true,
	}

	opt := resourcegroups.NewOptions()
	opt.DeleteExpired = true
	cmd.Flags().StringVar(&opt.EvaluationTime, "now", opt.EvaluationTime, "Current time when evaluating expiration (RFC3339)")
	cmd.Flags().BoolVar(&opt.DryRun, "dry-run", opt.DryRun, "Print which resource groups would be deleted without deleting")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()
		return opt.Run(logr.NewContext(ctx, createLogger(0)))
	}

	return cmd
}

func createLogger(verbosity int) logr.Logger {
	prettyHandler := prettylog.NewHandler(&slog.HandlerOptions{
		Level:       slog.Level(verbosity * -1),
		AddSource:   false,
		ReplaceAttr: nil,
	})
	return logr.FromSlogHandler(prettyHandler)
}
