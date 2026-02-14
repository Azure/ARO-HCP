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

package sync

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/grafanactl/internal/grafana"
)

const dashboardsGroupID = "dashboards"

func NewSyncCommand(group string) (*cobra.Command, error) {
	opts := DefaultSyncDashboardsOptions()

	syncCmd := &cobra.Command{
		Use:     "sync",
		Short:   "Sync Grafana resources",
		Long:    "Sync Grafana resources as per git",
		GroupID: group,
	}

	syncCmd.AddGroup(&cobra.Group{
		ID:    dashboardsGroupID,
		Title: "Sync Commands:",
	})

	syncDashboardsCmd := &cobra.Command{
		Use:     "dashboards",
		Short:   "Sync Grafana dashboards",
		Long:    "Sync Grafana dashboards and folders present in git, remove stale dashboards",
		GroupID: dashboardsGroupID,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.Run(cmd.Context())
		},
	}

	if err := BindSyncDashboardsOptions(opts, syncDashboardsCmd); err != nil {
		return nil, err
	}

	syncCmd.AddCommand(syncDashboardsCmd)

	return syncCmd, nil
}

func (opts *RawSyncDashboardsOptions) Run(ctx context.Context) error {
	validated, err := opts.Validate(ctx)
	if err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	completed, err := validated.Complete(ctx)
	if err != nil {
		return fmt.Errorf("completion failed: %w", err)
	}

	return completed.Run(ctx)
}

func (o *CompletedSyncDashboardsOptions) Run(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)

	logger.Info("Starting dashboard sync", "dry-run", o.DryRun)

	syncer := grafana.NewDashboardSyncer(
		o.GrafanaClient,
		o.Config,
		o.ConfigFilePath,
		o.DryRun,
	)

	if err := syncer.Sync(ctx); err != nil {
		return fmt.Errorf("sync failed: %w", err)
	}

	logger.Info("Dashboard sync completed successfully")
	return nil
}
