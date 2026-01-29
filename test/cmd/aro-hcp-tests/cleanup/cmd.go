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

package cleanup

import (
	"log/slog"
	"os"
	"os/signal"

	"github.com/dusted-go/logging/prettylog"
	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/test/util/cleanup"
	kustoroleassignments "github.com/Azure/ARO-HCP/test/util/cleanup/kusto-role-assignments"
	"github.com/Azure/ARO-HCP/test/util/cleanup/resourcegroups"
)

func NewCommand() *cobra.Command {

	opt := cleanup.NewBaseOptions()

	cmd := &cobra.Command{
		Use:          "cleanup",
		Short:        "Cleanup resources",
		SilenceUsage: true,
	}

	cmd.PersistentFlags().IntVarP(&opt.Verbosity, "verbosity", "v", opt.Verbosity, "Log verbosity level")

	cmd.AddCommand(newCleanupResourceGroupsCommand())
	cmd.AddCommand(newDeleteKustoRoleAssignmentsCommand())

	cmd.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		ctx := logr.NewContext(cmd.Context(), createLogger(opt.Verbosity))
		cmd.SetContext(ctx)
	}
	return cmd
}

func newDeleteKustoRoleAssignmentsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "kusto-role-assignments",
		Short:        "Delete Kusto role assignments",
		SilenceUsage: true,
	}
	rawOpt := kustoroleassignments.DefaultOptions()

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt)
		defer cancel()

		validatedOpt, err := rawOpt.Validate()
		if err != nil {
			return err
		}

		completedOpt, err := validatedOpt.Complete(ctx)
		if err != nil {
			return err
		}

		return completedOpt.Run(ctx)
	}

	return cmd
}

func newCleanupResourceGroupsCommand() *cobra.Command {

	cmd := &cobra.Command{
		Use:          "resource-groups",
		Short:        "Delete resource groups (explicit list or expired resource groups)",
		SilenceUsage: true,
	}

	rawOpt := resourcegroups.DefaultOptions()
	cmd.Flags().StringArrayVar(&rawOpt.ResourceGroups, "resource-group", rawOpt.ResourceGroups, "Resource group to clean (repeat)")
	cmd.Flags().BoolVar(&rawOpt.DeleteExpired, "expired", rawOpt.DeleteExpired, "Delete all expired e2e resource groups")
	cmd.Flags().StringVar(&rawOpt.EvaluationTime, "evaluation-time", rawOpt.EvaluationTime, "Time at which to evaluate resource group expiration (RFC3339,defaults to current time)")
	cmd.Flags().BoolVar(&rawOpt.DryRun, "dry-run", rawOpt.DryRun, "Print which resource groups would be deleted without deleting")
	cmd.Flags().StringVar(&rawOpt.CleanupWorkflow, "mode", string(rawOpt.CleanupWorkflow), "Cleanup workflow: 'standard' (default, via RP) or 'no-rp' (only to be used when the infra has already been cleaned up)")
	cmd.Flags().BoolVar(&rawOpt.IsDevelopment, "is-development", rawOpt.IsDevelopment, "Use development (local RP) endpoint instead of ARM (only valid with mode=standard)")
	cmd.Flags().DurationVar(&rawOpt.Timeout, "timeout", rawOpt.Timeout, "Timeout for deleting each resource group (e.g. 60m)")
	cmd.Flags().StringArrayVar(&rawOpt.IncludeLocations, "include-location", rawOpt.IncludeLocations, "Only delete resource groups in these Azure locations (repeatable)")
	cmd.Flags().StringArrayVar(&rawOpt.ExcludeLocations, "exclude-location", rawOpt.ExcludeLocations, "Do not delete resource groups in these Azure locations (repeatable)")
	cmd.Flags().BoolVar(&rawOpt.Tracked, "tracked", rawOpt.Tracked, "Use tracked resource groups")
	cmd.Flags().StringVar(&rawOpt.SharedDir, "shared-dir", rawOpt.SharedDir, "Shared directory to use for tracked resource groups")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt)
		defer cancel()

		validatedOpt, err := rawOpt.Validate()
		if err != nil {
			return err
		}

		completedOpt, err := validatedOpt.Complete()
		if err != nil {
			return err
		}

		return completedOpt.Run(ctx)
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
