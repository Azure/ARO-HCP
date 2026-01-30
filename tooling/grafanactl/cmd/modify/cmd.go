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

package modify

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
)

const datasourceGroupID = "datasource"

func NewModifyCommand(group string) (*cobra.Command, error) {
	opts := DefaultAddDatasourceOptions()

	modifyCmd := &cobra.Command{
		Use:     "modify",
		Short:   "Modify Grafana resources",
		Long:    "Modify Grafana dashboards, data sources, or other resources.",
		GroupID: group,
	}

	modifyCmd.AddGroup(&cobra.Group{
		ID:    datasourceGroupID,
		Title: "Datasource Commands:",
	})

	datasourceCmd := &cobra.Command{
		Use:     "datasource",
		Short:   "Manage Grafana datasources",
		Long:    "Add, update, or manage Grafana datasources.",
		GroupID: datasourceGroupID,
	}

	addDatasourceCmd := &cobra.Command{
		Use:   "add",
		Short: "Add Azure Monitor Workspace datasource to Grafana",
		Long:  "Add an Azure Monitor Workspace as a datasource to the Azure Managed Grafana instance. This integrates the workspace with Grafana and creates the necessary datasource configuration.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.Run(cmd.Context())
		},
	}

	if err := BindAddDatasourceOptions(opts, addDatasourceCmd); err != nil {
		return nil, err
	}

	datasourceCmd.AddCommand(addDatasourceCmd)
	modifyCmd.AddCommand(datasourceCmd)

	return modifyCmd, nil
}

func (opts *RawAddDatasourceOptions) Run(ctx context.Context) error {
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

func (o *CompletedAddDatasourceOptions) Run(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)

	logger.Info("add datasource command executed", "monitor-workspace-ids", o.MonitorWorkspaceIDs, "dry-run", o.DryRun)

	grafana, err := o.ManagedGrafanaClient.GetGrafanaInstance(ctx, o.ResourceGroup, o.GrafanaName)
	if err != nil {
		return fmt.Errorf("failed to get Grafana instance: %w", err)
	}

	monitorWorkspaces, err := o.MonitorWorkspaceClient.GetAllMonitorWorkspaces(ctx)
	if err != nil {
		return fmt.Errorf("failed to list Azure Monitor Workspaces: %w", err)
	}

	validWorkspaceIDs := make(map[string]bool)
	for _, workspace := range monitorWorkspaces {
		validWorkspaceIDs[strings.ToLower(*workspace.ID)] = true
	}

	currentIntegrations := make(map[string]bool)
	for _, workspaceID := range o.MonitorWorkspaceIDs {
		if !validWorkspaceIDs[strings.ToLower(workspaceID)] {
			return fmt.Errorf("provided Azure Monitor Workspace not found: %s", workspaceID)
		}

		for _, integration := range grafana.Properties.GrafanaIntegrations.AzureMonitorWorkspaceIntegrations {
			workspaceID := *integration.AzureMonitorWorkspaceResourceID
			if validWorkspaceIDs[workspaceID] {
				currentIntegrations[workspaceID] = true
			}
		}

		if currentIntegrations[workspaceID] {
			logger.Info("Azure Monitor Workspace is already integrated", "workspace-id", workspaceID)
			return nil
		}

		// This effectively adds the new provided workspace to the list of integrations
		currentIntegrations[workspaceID] = true
	}

	integrationList := make([]string, 0, len(currentIntegrations))
	for workspaceID := range currentIntegrations {
		integrationList = append(integrationList, workspaceID)
	}

	if o.DryRun {
		logger.Info("Dry run - would add Azure Monitor Workspace integration", "workspace-ids", o.MonitorWorkspaceIDs, "total-integrations", len(integrationList))
		return nil
	}

	logger.Info("Adding Azure Monitor Workspace integration", "workspace-ids", o.MonitorWorkspaceIDs, "total-integrations", len(integrationList))

	err = o.ManagedGrafanaClient.UpdataGrafanaIntegrations(ctx, o.ResourceGroup, o.GrafanaName, integrationList)
	if err != nil {
		return fmt.Errorf("failed to update Grafana integrations: %w", err)
	}

	logger.Info("Successfully added Azure Monitor Workspace integration", "workspace-ids", o.MonitorWorkspaceIDs)
	return nil
}
