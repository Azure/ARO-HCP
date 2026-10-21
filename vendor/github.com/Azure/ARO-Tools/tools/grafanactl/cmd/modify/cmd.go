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

	"k8s.io/utils/set"

	"github.com/Azure/ARO-Tools/tools/grafanactl/internal/grafana"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dashboard/armdashboard/v2"
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
		Use:   "reconcile",
		Short: "Reconcile Azure Monitor Workspace datasources in Grafana",
		Long:  "Reconcile Azure Monitor Workspace datasources in the Azure Managed Grafana instance. This integrates the workspaces with Grafana and creates the necessary datasource configuration.",
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

	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	return completed.Run(ctx)
}

func (o *CompletedAddDatasourceOptions) getMatchingWorkspaceIDs(ctx context.Context, logger logr.Logger) (set.Set[string], error) {
	discoveredIDs, err := o.ResourceGraphDiscoveryClient.DiscoverMonitorWorkspaceIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to discover Azure Monitor Workspaces via Resource Graph: %w", err)
	}

	workspaceIDs := set.New[string]()
	for _, id := range discoveredIDs {
		workspaceIDs.Insert(strings.ToLower(id))
	}
	logger.Info("discovered Azure Monitor Workspaces via Resource Graph", "count", workspaceIDs.Len())

	return workspaceIDs, nil
}

func (o *CompletedAddDatasourceOptions) Run(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx).WithValues("resource-group", o.ResourceGroup, "grafana-name", o.GrafanaName)

	logger.Info("add datasource command executed")

	grafanaInstance, err := o.ManagedGrafanaClient.GetGrafanaInstance(ctx, o.ResourceGroup, o.GrafanaName)
	if err != nil {
		return fmt.Errorf("failed to get Grafana instance: %w", err)
	}

	validWorkspaceIDs, err := o.getMatchingWorkspaceIDs(ctx, logger)
	if err != nil {
		return fmt.Errorf("failed to get valid workspace IDs: %w", err)
	}

	integrationList := set.New[string]()
	var existingIntegrations []*armdashboard.AzureMonitorWorkspaceIntegration
	if grafanaInstance.Properties != nil && grafanaInstance.Properties.GrafanaIntegrations != nil {
		existingIntegrations = grafanaInstance.Properties.GrafanaIntegrations.AzureMonitorWorkspaceIntegrations
	}
	for _, integration := range existingIntegrations {
		if integration == nil || integration.AzureMonitorWorkspaceResourceID == nil {
			continue
		}
		integrationID := strings.ToLower(*integration.AzureMonitorWorkspaceResourceID)
		if validWorkspaceIDs.Has(integrationID) {
			integrationList.Insert(integrationID)
		} else {
			logger.Info("Removing", "workspace-id", integrationID)
		}
	}

	for _, workspaceID := range validWorkspaceIDs.UnsortedList() {
		if !integrationList.Has(workspaceID) {
			logger.Info("Adding", "workspace-id", workspaceID)
			integrationList.Insert(workspaceID)
		}
	}

	if o.DryRun {
		logger.Info("Dry run - would reconcile Azure Monitor Workspace integrations", "total-integrations", integrationList.Len())
	} else {
		logger.Info("Reconciling Azure Monitor Workspace integrations", "total-integrations", integrationList.Len())

		err = o.ManagedGrafanaClient.UpdateGrafanaIntegrations(ctx, o.ResourceGroup, o.GrafanaName, integrationList.UnsortedList())
		if err != nil {
			return fmt.Errorf("failed to update Grafana integrations: %w", err)
		}
	}

	validWorkspaceNames := grafana.WorkspaceNamesFromResourceIDs(integrationList)
	logger.Info("Reconciling datasources", "valid-workspaces", validWorkspaceNames.Len())
	if err := o.GrafanaClient.DeleteStaleDatasources(ctx, logger, validWorkspaceNames, o.DryRun); err != nil {
		return fmt.Errorf("failed to delete stale datasources: %w", err)
	}

	return nil
}
