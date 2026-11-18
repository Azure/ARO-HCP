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

package manage

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"k8s.io/utils/set"

	"github.com/Azure/ARO-Tools/tools/grafanactl/internal/grafana"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dashboard/armdashboard/v2"
)

func NewManageCommand(group string) (*cobra.Command, error) {
	opts := DefaultReconcileOptions()

	manageCmd := &cobra.Command{
		Use:     "manage",
		Short:   "Manage Grafana infrastructure",
		Long:    "Manage the lifecycle of Azure Managed Grafana resources.",
		GroupID: group,
	}

	reconcileCmd := &cobra.Command{
		Use:   "reconcile",
		Short: "Create or update the Grafana instance and reconcile datasources",
		Long: `Reconcile the Azure Managed Grafana instance. This creates the instance
if it does not exist, updates its configuration, and discovers all Azure Monitor
Workspaces across accessible subscriptions to add them as datasource integrations.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.Run(cmd.Context())
		},
	}

	if err := BindReconcileOptions(opts, reconcileCmd); err != nil {
		return nil, err
	}

	manageCmd.AddCommand(reconcileCmd)

	return manageCmd, nil
}

func (opts *RawReconcileOptions) Run(ctx context.Context) error {
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

func (o *CompletedReconcileOptions) Run(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx).WithValues("resource-group", o.ResourceGroup, "grafana-name", o.GrafanaName)

	logger.Info("reconcile command executed", "dry-run", o.DryRun)

	zoneRedundancy := armdashboard.ZoneRedundancy(o.ZoneRedundancy)
	publicNetworkAccess := armdashboard.PublicNetworkAccess(o.PublicNetworkAccess)

	tags := map[string]*string{}
	if o.CrossTenantSecurityGroup != "" {
		tags["AMG.CrossTenant.SecurityGroup"] = &o.CrossTenantSecurityGroup
	}

	discoveredIDs, err := o.ResourceGraphDiscoveryClient.DiscoverMonitorWorkspaceIDs(ctx)
	if err != nil {
		return fmt.Errorf("failed to discover Azure Monitor Workspaces via Resource Graph: %w", err)
	}

	workspaceIDs := set.New[string]()
	for _, id := range discoveredIDs {
		workspaceIDs.Insert(strings.ToLower(id))
	}
	logger.Info("discovered Azure Monitor Workspaces via Resource Graph", "count", workspaceIDs.Len())

	existingIDs, err := o.getExistingIntegrations(ctx, logger)
	if err != nil {
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) && respErr.StatusCode == 404 {
			logger.Info("Grafana instance does not exist yet, will create")
		} else {
			return fmt.Errorf("failed to get existing Grafana integrations: %w", err)
		}
	} else {
		logger.Info("Grafana instance exists; reconciling integrations to discovered HCP workspaces", "existing-count", len(existingIDs))
	}

	integrations := make([]*armdashboard.AzureMonitorWorkspaceIntegration, 0, workspaceIDs.Len())
	for _, id := range workspaceIDs.SortedList() {
		integrations = append(integrations, &armdashboard.AzureMonitorWorkspaceIntegration{
			AzureMonitorWorkspaceResourceID: &id,
		})
	}

	identityType := armdashboard.ManagedServiceIdentityTypeSystemAssigned
	grafanaResource := armdashboard.ManagedGrafana{
		Location: &o.Location,
		SKU: &armdashboard.ResourceSKU{
			Name: &o.SKU,
		},
		Identity: &armdashboard.ManagedServiceIdentity{
			Type: &identityType,
		},
		Tags: tags,
		Properties: &armdashboard.ManagedGrafanaProperties{
			PublicNetworkAccess: &publicNetworkAccess,
			ZoneRedundancy:      &zoneRedundancy,
			GrafanaConfigurations: &armdashboard.GrafanaConfigurations{
				Users: &armdashboard.Users{
					ViewersCanEdit: to.Ptr(true),
				},
			},
			GrafanaIntegrations: &armdashboard.GrafanaIntegrations{
				AzureMonitorWorkspaceIntegrations: integrations,
			},
		},
	}
	if o.MajorVersion != "" {
		grafanaResource.Properties.GrafanaMajorVersion = &o.MajorVersion
	}

	location := o.Location
	if o.DryRun {
		logger.Info("dry run - would create/update Grafana instance",
			"location", o.Location,
			"major-version", o.MajorVersion,
			"zone-redundancy", o.ZoneRedundancy,
			"public-network-access", o.PublicNetworkAccess,
			"integrations", workspaceIDs.Len(),
		)
	} else {
		result, err := o.ManagedGrafanaClient.CreateOrUpdateGrafanaInstance(ctx, o.ResourceGroup, o.GrafanaName, grafanaResource)
		if err != nil {
			return fmt.Errorf("failed to create/update Grafana instance: %w", err)
		}

		principalID := ""
		if result.Identity != nil && result.Identity.PrincipalID != nil {
			principalID = *result.Identity.PrincipalID
		}

		resultID := ""
		if result.ID != nil {
			resultID = *result.ID
		}

		if result.Location != nil {
			location = *result.Location
		}

		logger.Info("Grafana instance reconciled",
			"id", resultID,
			"principal-id", principalID,
			"integrations", workspaceIDs.Len(),
		)
	}

	validWorkspaceNames := grafana.WorkspaceNamesFromResourceIDs(workspaceIDs)
	logger.Info("Reconciling datasources", "valid-workspaces", validWorkspaceNames.Len())
	if err := o.GrafanaClient.DeleteStaleDatasources(ctx, logger, validWorkspaceNames, o.DryRun); err != nil {
		return fmt.Errorf("failed to delete stale datasources: %w", err)
	}

	if err := o.reconcileADXIntegrations(ctx, location, logger); err != nil {
		return fmt.Errorf("failed to reconcile ADX integration fabrics: %w", err)
	}

	return nil
}

func (o *CompletedReconcileOptions) getExistingIntegrations(ctx context.Context, logger logr.Logger) ([]string, error) {
	grafana, err := o.ManagedGrafanaClient.GetGrafanaInstance(ctx, o.ResourceGroup, o.GrafanaName)
	if err != nil {
		return nil, err
	}

	var ids []string
	if grafana.Properties != nil &&
		grafana.Properties.GrafanaIntegrations != nil {
		for _, integration := range grafana.Properties.GrafanaIntegrations.AzureMonitorWorkspaceIntegrations {
			if integration == nil {
				continue
			}
			if integration.AzureMonitorWorkspaceResourceID != nil {
				logger.Info("found existing integration", "workspace-id", *integration.AzureMonitorWorkspaceResourceID)
				ids = append(ids, *integration.AzureMonitorWorkspaceResourceID)
			}
		}
	}
	return ids, nil
}
