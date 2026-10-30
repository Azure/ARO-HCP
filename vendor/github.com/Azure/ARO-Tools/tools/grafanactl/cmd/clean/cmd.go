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

package clean

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"k8s.io/utils/set"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dashboard/armdashboard/v2"
)

const datasourcesGroupID = "datasources"

func NewCleanCommand(group string) (*cobra.Command, error) {
	opts := DefaultCleanDatasourcesOptions()

	cleanCmd := &cobra.Command{
		Use:     "clean",
		Short:   "Clean Grafana resources",
		Long:    "Clean Grafana dashboards, data sources, or other resources.",
		GroupID: group,
	}

	cleanCmd.AddGroup(&cobra.Group{
		ID:    datasourcesGroupID,
		Title: "Clean Commands:",
	})

	cleanDatasourcesCmd := &cobra.Command{
		Use:     "datasources",
		Short:   "Remove orphaned Azure Monitor Workspace integrations from the Grafana resource",
		Long:    "Clean Azure Monitor Workspace integrations references from the Grafana resource (usually you want to run this first). This will remove any references to Azure Monitor Workspace integrations that don't exist anymore.",
		GroupID: datasourcesGroupID,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.Run(cmd.Context())
		},
	}

	fixupCmd := &cobra.Command{
		Use:     "fixup-datasources",
		Short:   "Delete orphaned datasources in the Grafana instance",
		Long:    "Delete orphaned datasources in the Grafana instance. This will remove any Managed Prometheus datasources that don't exist anymore.",
		GroupID: datasourcesGroupID,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.RunFixup(cmd.Context())
		},
	}

	if err := BindCleanDatasourcesOptions(opts, cleanDatasourcesCmd); err != nil {
		return nil, err
	}

	if err := BindCleanDatasourcesOptions(opts, fixupCmd); err != nil {
		return nil, err
	}

	cleanCmd.AddCommand(cleanDatasourcesCmd)
	cleanCmd.AddCommand(fixupCmd)

	return cleanCmd, nil
}

func (opts *RawCleanDatasourcesOptions) Run(ctx context.Context) error {
	validated, err := opts.Validate(ctx)
	if err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	completed, err := validated.Complete(ctx)
	if err != nil {
		return fmt.Errorf("completion failed: %w", err)
	}

	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	return completed.Run(ctx)
}

func (o *CompletedCleanDatasourcesOptions) Run(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)

	logger.Info("clean command executed", "dry-run", o.DryRun)

	grafana, err := o.ManagedGrafanaClient.GetGrafanaInstance(ctx, o.ResourceGroup, o.GrafanaName)
	if err != nil {
		return fmt.Errorf("failed to get Azure Monitor Workspace integrations: %w", err)
	}

	var integrations []*armdashboard.AzureMonitorWorkspaceIntegration
	if grafana.Properties != nil && grafana.Properties.GrafanaIntegrations != nil {
		integrations = grafana.Properties.GrafanaIntegrations.AzureMonitorWorkspaceIntegrations
	}

	logger.Info("Found Azure Monitor Workspace integrations", "count", len(integrations))

	discoveredIDs, err := o.ResourceGraphDiscoveryClient.DiscoverMonitorWorkspaceIDs(ctx)
	if err != nil {
		return fmt.Errorf("failed to discover Azure Monitor Workspaces via Resource Graph: %w", err)
	}

	activePrometheusResourceIds := make(map[string]bool)
	for _, id := range discoveredIDs {
		activePrometheusResourceIds[strings.ToLower(id)] = true
	}

	keptIntegrations := make([]string, 0)
	removedCount := 0

	for _, integration := range integrations {
		if integration == nil || integration.AzureMonitorWorkspaceResourceID == nil {
			continue
		}
		lowerIntegrationID := strings.ToLower(*integration.AzureMonitorWorkspaceResourceID)
		if _, ok := activePrometheusResourceIds[lowerIntegrationID]; ok {
			logger.Info("Keeping Azure Monitor Workspace integration", "resourceId", lowerIntegrationID)
			keptIntegrations = append(keptIntegrations, *integration.AzureMonitorWorkspaceResourceID)
		} else {
			logger.Info("Removing Azure Monitor Workspace integration", "resourceId", lowerIntegrationID)
			removedCount++
		}
	}

	if removedCount > 0 {
		if o.DryRun {
			logger.Info("Dry run - would remove integrations", "count", removedCount, "remaining", len(keptIntegrations))
		} else {
			logger.Info("Updating Grafana resource", "removingCount", removedCount, "keepingCount", len(keptIntegrations))
			err := o.ManagedGrafanaClient.UpdateGrafanaIntegrations(ctx, o.ResourceGroup, o.GrafanaName, keptIntegrations)
			if err != nil {
				return fmt.Errorf("failed to update Azure Monitor Workspace integrations: %w", err)
			}
			logger.Info("Successfully updated Grafana resource")
		}
	} else {
		logger.Info("No orphaned Azure Monitor Workspace integrations found")
	}

	return nil
}

func (opts *RawCleanDatasourcesOptions) RunFixup(ctx context.Context) error {
	validated, err := opts.Validate(ctx)
	if err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	completed, err := validated.Complete(ctx)
	if err != nil {
		return fmt.Errorf("completion failed: %w", err)
	}

	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	return completed.RunFixup(ctx)
}

func (o *CompletedCleanDatasourcesOptions) RunFixup(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)

	logger.Info("fixup-datasources command executed", "dry-run", o.DryRun)

	monitorWorkspaces, err := o.MonitorWorkspaceClient.GetAllMonitorWorkspaces(ctx)
	if err != nil {
		return fmt.Errorf("failed to list Azure Monitor Workspaces: %w", err)
	}

	validWorkspaceNames := set.New[string]()
	for _, ws := range monitorWorkspaces {
		if ws.Name != nil {
			validWorkspaceNames.Insert(strings.ToLower(*ws.Name))
		}
	}

	logger.Info("Reconciling datasources", "valid-workspaces", validWorkspaceNames.Len())
	if err := o.GrafanaClient.DeleteStaleDatasources(ctx, logger, validWorkspaceNames, o.DryRun); err != nil {
		return fmt.Errorf("failed to delete stale datasources: %w", err)
	}

	return nil
}
