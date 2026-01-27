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
		Long:    "Delete orphaned datasources in the Grafana instance. This will remove any Managed Prometheus datasources that are not exsisting anymore.",
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

	return completed.Run(ctx)
}

func (o *CompletedCleanDatasourcesOptions) Run(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)

	logger.Info("clean command executed", "dry-run", o.DryRun)

	grafana, err := o.ManagedGrafanaClient.GetGrafanaInstance(ctx, o.ResourceGroup, o.GrafanaName)
	if err != nil {
		return fmt.Errorf("failed to get Azure Monitor Workspace integrations: %w", err)
	}

	integrations := grafana.Properties.GrafanaIntegrations.AzureMonitorWorkspaceIntegrations

	logger.Info("Found Azure Monitor Workspace integrations", "count", len(integrations))

	prometheusInstances, err := o.ManagedPrometheusClient.ListPrometheusInstances(ctx)
	if err != nil {
		return fmt.Errorf("failed to list Prometheus instances: %w", err)
	}

	activePrometheusResourceIds := make(map[string]bool)
	for _, prometheusInstance := range prometheusInstances {
		activePrometheusResourceIds[strings.ToLower(prometheusInstance.ID)] = true
	}

	keptIntegrations := make([]string, 0)
	removedCount := 0

	for _, integration := range integrations {
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
			err := o.ManagedGrafanaClient.UpdataGrafanaIntegrations(ctx, o.ResourceGroup, o.GrafanaName, keptIntegrations)
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

	return completed.RunFixup(ctx)
}

func (o *CompletedCleanDatasourcesOptions) RunFixup(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)

	logger.Info("clean command executed", "dry-run", o.DryRun)

	datasources, err := o.GrafanaClient.ListDataSources(ctx)
	if err != nil {
		return fmt.Errorf("failed to list datasources: %w", err)
	}

	prometheusInstances, err := o.ManagedPrometheusClient.ListPrometheusInstances(ctx)
	if err != nil {
		return fmt.Errorf("failed to list Prometheus instances: %w", err)
	}

	activePrometheusResourceNames := make(map[string]bool)
	for _, prometheusInstance := range prometheusInstances {
		activePrometheusResourceNames[strings.ToLower(prometheusInstance.Name)] = true
	}

	for _, datasource := range datasources {
		if datasource.Type == "prometheus" {
			nameSuffix := strings.TrimPrefix(datasource.Name, "Managed_Prometheus_")
			if activePrometheusResourceNames[strings.ToLower(nameSuffix)] {
				logger.Info("Keeping datasource", "name", datasource.Name)
			} else {
				logger.Info("Deleting datasource", "name", datasource.Name)
				if o.DryRun {
					logger.Info("Dry run - would delete datasource", "name", datasource.Name)
					continue
				}
				err := o.GrafanaClient.DeleteDataSource(ctx, datasource.Name)
				if err != nil {
					return fmt.Errorf("failed to delete datasource: %w", err)
				}
			}
		}
	}

	return nil
}
