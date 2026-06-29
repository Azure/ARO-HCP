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

package reconcile

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-Tools/tools/cmdutils"
	"github.com/Azure/ARO-Tools/tools/grafanactl/cmd/base"
	"github.com/Azure/ARO-Tools/tools/grafanactl/cmd/modify"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dashboard/armdashboard/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
)

type RawGrafanaOptions struct {
	*base.BaseOptions
	Location                 string
	GrafanaMajorVersion      string
	ZoneRedundancyMode       string
	CrossTenantSecurityGroup string
}

type validatedGrafanaOptions struct {
	*RawGrafanaOptions
	*base.CompletedBaseOptions
}

type ValidatedGrafanaOptions struct {
	*validatedGrafanaOptions
}

type CompletedGrafanaOptions struct {
	*validatedGrafanaOptions
	grafanaClient   *armdashboard.GrafanaClient
	workspaceClient *armmonitor.AzureMonitorWorkspacesClient
	zoneRedundancy  armdashboard.ZoneRedundancy
}

func DefaultGrafanaOptions() *RawGrafanaOptions {
	return &RawGrafanaOptions{
		BaseOptions:        base.DefaultBaseOptions(),
		ZoneRedundancyMode: "Disabled",
	}
}

func BindGrafanaOptions(opts *RawGrafanaOptions, cmd *cobra.Command) error {
	if err := base.BindBaseOptions(opts.BaseOptions, cmd); err != nil {
		return err
	}
	flags := cmd.Flags()
	flags.StringVar(&opts.Location, "location", opts.Location, "Azure location for the Managed Grafana instance")
	flags.StringVar(&opts.GrafanaMajorVersion, "grafana-major-version", opts.GrafanaMajorVersion, "Grafana major version to target")
	flags.StringVar(&opts.ZoneRedundancyMode, "zone-redundancy-mode", opts.ZoneRedundancyMode, "Grafana zone redundancy mode: Enabled, Disabled, or Auto")
	flags.StringVar(&opts.CrossTenantSecurityGroup, "cross-tenant-security-group", opts.CrossTenantSecurityGroup, "optional AMG cross-tenant security group tag value")
	return nil
}

func (o *RawGrafanaOptions) Validate(ctx context.Context) (*ValidatedGrafanaOptions, error) {
	completedBase, err := base.ValidateBaseOptions(o.BaseOptions)
	if err != nil {
		return nil, err
	}
	if o.Location == "" {
		return nil, fmt.Errorf("location is required")
	}
	if o.GrafanaMajorVersion == "" {
		return nil, fmt.Errorf("grafana major version is required")
	}

	if err := validateZoneRedundancyMode(o.ZoneRedundancyMode); err != nil {
		return nil, err
	}

	return &ValidatedGrafanaOptions{
		validatedGrafanaOptions: &validatedGrafanaOptions{
			RawGrafanaOptions: &RawGrafanaOptions{
				BaseOptions:              o.BaseOptions,
				Location:                 o.Location,
				GrafanaMajorVersion:      o.GrafanaMajorVersion,
				ZoneRedundancyMode:       o.ZoneRedundancyMode,
				CrossTenantSecurityGroup: o.CrossTenantSecurityGroup,
			},
			CompletedBaseOptions: completedBase,
		},
	}, nil
}

func (o *ValidatedGrafanaOptions) Complete(ctx context.Context) (*CompletedGrafanaOptions, error) {
	cred, err := cmdutils.GetAzureTokenCredentialsForCloud(o.CloudConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to obtain Azure credentials: %w", err)
	}

	clientOptions := o.ARMClientOptions()
	grafanaClient, err := armdashboard.NewGrafanaClient(o.SubscriptionID, cred, clientOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to create Managed Grafana client: %w", err)
	}
	workspaceClient, err := armmonitor.NewAzureMonitorWorkspacesClient(o.SubscriptionID, cred, clientOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure Monitor Workspaces client: %w", err)
	}

	subscriptionsClient, err := armsubscriptions.NewClient(cred, clientOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to create Subscriptions client: %w", err)
	}
	locationHasAZs, err := locationHasAvailabilityZones(ctx, subscriptionsClient, o.SubscriptionID, o.Location)
	if err != nil {
		return nil, err
	}

	return &CompletedGrafanaOptions{
		validatedGrafanaOptions: o.validatedGrafanaOptions,
		grafanaClient:           grafanaClient,
		workspaceClient:         workspaceClient,
		zoneRedundancy:          resolveZoneRedundancy(o.ZoneRedundancyMode, locationHasAZs),
	}, nil
}

func (o *RawGrafanaOptions) Run(ctx context.Context) error {
	validated, err := o.Validate(ctx)
	if err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}
	completed, err := validated.Complete(ctx)
	if err != nil {
		return fmt.Errorf("completion failed: %w", err)
	}
	return completed.Run(ctx)
}

func (o *CompletedGrafanaOptions) Run(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx).WithValues("resource-group", o.ResourceGroup, "grafana-name", o.GrafanaName)

	workspaceIDs, err := o.discoverWorkspaceIDs(ctx, logger)
	if err != nil {
		return err
	}
	if o.DryRun {
		logger.Info("Dry run - would create or update Managed Grafana", "workspace-count", len(workspaceIDs))
		return nil
	}

	logger.Info("Creating or updating Managed Grafana", "workspace-count", len(workspaceIDs))
	poller, err := o.grafanaClient.BeginCreate(ctx, o.ResourceGroup, o.GrafanaName, o.managedGrafana(workspaceIDs), nil)
	if err != nil {
		return fmt.Errorf("failed to start Managed Grafana create/update: %w", err)
	}
	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		return fmt.Errorf("failed to create/update Managed Grafana: %w", err)
	}

	addDatasourceOpts := modify.DefaultAddDatasourceOptions()
	addDatasourceOpts.SubscriptionID = o.SubscriptionID
	addDatasourceOpts.ResourceGroup = o.ResourceGroup
	addDatasourceOpts.GrafanaName = o.GrafanaName
	addDatasourceOpts.DryRun = o.DryRun
	addDatasourceOpts.ARMEndpoint = o.ARMEndpoint
	addDatasourceOpts.AADAuthority = o.AADAuthority
	if err := addDatasourceOpts.Run(ctx); err != nil {
		return fmt.Errorf("failed to reconcile Grafana datasources: %w", err)
	}

	return nil
}

func (o *CompletedGrafanaOptions) discoverWorkspaceIDs(ctx context.Context, logger logr.Logger) ([]string, error) {
	workspaceIDsByLowercase := map[string]string{}
	pager := o.workspaceClient.NewListBySubscriptionPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list Azure Monitor Workspaces: %w", err)
		}
		for _, workspace := range page.Value {
			if workspace == nil || workspace.ID == nil || workspace.Properties == nil || workspace.Properties.ProvisioningState == nil {
				continue
			}
			if *workspace.Properties.ProvisioningState != armmonitor.ProvisioningStateSucceeded {
				continue
			}
			workspaceID := *workspace.ID
			logger.Info("Found Azure Monitor Workspace", "workspace-id", workspaceID)
			workspaceIDsByLowercase[strings.ToLower(workspaceID)] = workspaceID
		}
	}

	workspaceIDs := make([]string, 0, len(workspaceIDsByLowercase))
	for _, workspaceID := range workspaceIDsByLowercase {
		workspaceIDs = append(workspaceIDs, workspaceID)
	}
	sort.Slice(workspaceIDs, func(i, j int) bool {
		return strings.ToLower(workspaceIDs[i]) < strings.ToLower(workspaceIDs[j])
	})
	return workspaceIDs, nil
}

func (o *CompletedGrafanaOptions) managedGrafana(workspaceIDs []string) armdashboard.ManagedGrafana {
	identityType := armdashboard.ManagedServiceIdentityTypeSystemAssigned
	zoneRedundancy := o.zoneRedundancy
	integrations := make([]*armdashboard.AzureMonitorWorkspaceIntegration, 0, len(workspaceIDs))
	for _, workspaceID := range workspaceIDs {
		workspaceID := workspaceID
		integrations = append(integrations, &armdashboard.AzureMonitorWorkspaceIntegration{
			AzureMonitorWorkspaceResourceID: &workspaceID,
		})
	}

	tags := map[string]*string{}
	if o.CrossTenantSecurityGroup != "" {
		crossTenantSecurityGroup := o.CrossTenantSecurityGroup
		tags["AMG.CrossTenant.SecurityGroup"] = &crossTenantSecurityGroup
	}

	return armdashboard.ManagedGrafana{
		Location: &o.Location,
		SKU: &armdashboard.ResourceSKU{
			Name: ptr("Standard"),
		},
		Identity: &armdashboard.ManagedServiceIdentity{
			Type: &identityType,
		},
		Tags: tags,
		Properties: &armdashboard.ManagedGrafanaProperties{
			GrafanaMajorVersion: &o.GrafanaMajorVersion,
			ZoneRedundancy:      &zoneRedundancy,
			GrafanaIntegrations: &armdashboard.GrafanaIntegrations{
				AzureMonitorWorkspaceIntegrations: integrations,
			},
		},
	}
}

func validateZoneRedundancyMode(mode string) error {
	switch strings.ToLower(mode) {
	case "enabled", "disabled", "auto", "":
		return nil
	default:
		return fmt.Errorf("unsupported zone redundancy mode %q (want Enabled, Disabled, or Auto)", mode)
	}
}

// resolveZoneRedundancy mirrors determineZoneRedundancy() in
// dev-infrastructure/modules/common.bicep: both Auto and Enabled only enable
// zone redundancy when the target region actually exposes availability zones.
// Enabling it in a non-zonal region makes the Managed Grafana create/update fail.
func resolveZoneRedundancy(mode string, locationHasAvailabilityZones bool) armdashboard.ZoneRedundancy {
	switch strings.ToLower(mode) {
	case "auto", "enabled":
		if locationHasAvailabilityZones {
			return armdashboard.ZoneRedundancyEnabled
		}
		return armdashboard.ZoneRedundancyDisabled
	default: // "disabled", ""
		return armdashboard.ZoneRedundancyDisabled
	}
}

// locationHasAvailabilityZones reports whether the given Azure region exposes
// availability zones for the subscription, used to gate zone redundancy.
func locationHasAvailabilityZones(ctx context.Context, client *armsubscriptions.Client, subscriptionID, location string) (bool, error) {
	target := strings.ToLower(location)
	pager := client.NewListLocationsPager(subscriptionID, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return false, fmt.Errorf("failed to list locations for subscription %s: %w", subscriptionID, err)
		}
		for _, loc := range page.Value {
			if loc == nil || loc.Name == nil {
				continue
			}
			if strings.ToLower(*loc.Name) == target {
				return len(loc.AvailabilityZoneMappings) > 0, nil
			}
		}
	}
	return false, fmt.Errorf("location %q not found in subscription %s", location, subscriptionID)
}

func ptr[T any](v T) *T {
	return &v
}
