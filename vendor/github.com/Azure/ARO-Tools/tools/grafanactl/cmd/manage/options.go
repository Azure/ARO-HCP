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
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-Tools/tools/cmdutils"
	"github.com/Azure/ARO-Tools/tools/grafanactl/cmd/base"
	"github.com/Azure/ARO-Tools/tools/grafanactl/internal/azure"
	"github.com/Azure/ARO-Tools/tools/grafanactl/internal/grafana"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dashboard/armdashboard/v2"
)

type kustoDiscoveryClient interface {
	DiscoverKustoClusters(context.Context, string, string) ([]azure.KustoCluster, error)
}

type integrationFabricsClient interface {
	List(context.Context, string, string) ([]armdashboard.IntegrationFabric, error)
	Create(context.Context, string, string, string, armdashboard.IntegrationFabric) (*armdashboard.IntegrationFabric, error)
	Update(context.Context, string, string, string, armdashboard.IntegrationFabricUpdateParameters) (*armdashboard.IntegrationFabric, error)
	Delete(context.Context, string, string, string) error
}

// RawReconcileOptions represents the initial, unvalidated configuration for reconcile operations.
type RawReconcileOptions struct {
	*base.BaseOptions
	Location                 string
	SKU                      string
	MajorVersion             string
	ZoneRedundancy           string
	PublicNetworkAccess      string
	CrossTenantSecurityGroup string
	ADXIntegrationsEnabled   bool
	ADXEnvironment           string
	ADXGeographies           string
	ADXScenario              string
	ADXTargetResourceID      string
}

type validatedReconcileOptions struct {
	*RawReconcileOptions
	*base.CompletedBaseOptions
}

// ValidatedReconcileOptions represents reconcile configuration that has passed validation.
type ValidatedReconcileOptions struct {
	*validatedReconcileOptions
}

// CompletedReconcileOptions represents the final, fully validated and initialized configuration
// for reconcile operations.
type CompletedReconcileOptions struct {
	*validatedReconcileOptions
	GrafanaClient                *grafana.Client
	ManagedGrafanaClient         *azure.ManagedGrafanaClient
	ResourceGraphDiscoveryClient *azure.ResourceGraphDiscoveryClient
	KustoDiscoveryClient         kustoDiscoveryClient
	IntegrationFabricsClient     integrationFabricsClient
}

// DefaultReconcileOptions returns a new RawReconcileOptions with default values
func DefaultReconcileOptions() *RawReconcileOptions {
	return &RawReconcileOptions{
		BaseOptions:         base.DefaultBaseOptions(),
		SKU:                 "Standard",
		ZoneRedundancy:      "Disabled",
		PublicNetworkAccess: "Enabled",
	}
}

// BindReconcileOptions binds command-line flags to the options
func BindReconcileOptions(opts *RawReconcileOptions, cmd *cobra.Command) error {
	if err := base.BindBaseOptions(opts.BaseOptions, cmd); err != nil {
		return err
	}

	flags := cmd.Flags()
	flags.StringVar(&opts.Location, "location", opts.Location, "Azure region for the Grafana instance")
	flags.StringVar(&opts.SKU, "sku", opts.SKU, "Grafana SKU name (e.g. Standard)")
	flags.StringVar(&opts.MajorVersion, "major-version", opts.MajorVersion, "Grafana major version (e.g. 11)")
	flags.StringVar(&opts.ZoneRedundancy, "zone-redundancy", opts.ZoneRedundancy, "Zone redundancy mode: Enabled or Disabled")
	flags.StringVar(&opts.PublicNetworkAccess, "public-network-access", opts.PublicNetworkAccess, "Public network access mode: Enabled or Disabled")
	flags.StringVar(&opts.CrossTenantSecurityGroup, "cross-tenant-security-group", opts.CrossTenantSecurityGroup, "Cross-tenant security group (format: GroupObjectId;TenantId)")
	flags.BoolVar(&opts.ADXIntegrationsEnabled, "adx-integrations-enabled", opts.ADXIntegrationsEnabled, "Reconcile ADX Kusto integration fabric child resources")
	flags.StringVar(&opts.ADXEnvironment, "adx-integrations-environment", opts.ADXEnvironment, "Required aroHCPEnvironment tag value when ADX integrations are enabled")
	flags.StringVar(&opts.ADXGeographies, "adx-integrations-geographies", opts.ADXGeographies, "Required comma-separated complete aroHCPGeoShortId set when ADX integrations are enabled")
	flags.StringVar(&opts.ADXScenario, "adx-integrations-scenario", opts.ADXScenario, "Optional ADX integration fabric scenario supplied by the RP contract")
	flags.StringVar(&opts.ADXTargetResourceID, "adx-integrations-target-resource-id", opts.ADXTargetResourceID, "Optional ADX integration fabric target resource ID supplied by the RP contract")

	return nil
}

// Validate performs validation on the raw options
func (o *RawReconcileOptions) Validate(ctx context.Context) (*ValidatedReconcileOptions, error) {
	completedBase, err := base.ValidateBaseOptions(o.BaseOptions)
	if err != nil {
		return nil, err
	}

	if o.Location == "" {
		return nil, fmt.Errorf("--location is required")
	}

	if o.SKU == "" {
		return nil, fmt.Errorf("--sku is required")
	}

	if o.ZoneRedundancy != "Enabled" && o.ZoneRedundancy != "Disabled" {
		return nil, fmt.Errorf("--zone-redundancy must be 'Enabled' or 'Disabled', got: %s", o.ZoneRedundancy)
	}

	if o.PublicNetworkAccess != "Enabled" && o.PublicNetworkAccess != "Disabled" {
		return nil, fmt.Errorf("--public-network-access must be 'Enabled' or 'Disabled', got: %s", o.PublicNetworkAccess)
	}

	if o.ADXIntegrationsEnabled {
		if o.ADXEnvironment == "" {
			return nil, fmt.Errorf("--adx-integrations-environment is required when ADX integrations are enabled")
		}
		if err := azure.ValidateKustoEnvironment(o.ADXEnvironment); err != nil {
			return nil, fmt.Errorf("invalid --adx-integrations-environment: %w", err)
		}
		if len(parseGeographyAllowlist(o.ADXGeographies)) == 0 {
			return nil, fmt.Errorf("--adx-integrations-geographies is required when ADX integrations are enabled")
		}
	}

	return &ValidatedReconcileOptions{
		validatedReconcileOptions: &validatedReconcileOptions{
			RawReconcileOptions:  o,
			CompletedBaseOptions: completedBase,
		},
	}, nil
}

// Complete performs final initialization to create fully usable reconcile options.
func (o *ValidatedReconcileOptions) Complete(ctx context.Context) (*CompletedReconcileOptions, error) {
	cred, err := cmdutils.GetAzureTokenCredentialsForCloud(o.CloudConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to obtain Azure credentials: %w", err)
	}

	clientOpts := o.ARMClientOptions()

	managedGrafanaClient, err := azure.NewManagedGrafanaClient(o.SubscriptionID, cred, clientOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to create managed Grafana client: %w", err)
	}

	grafanaClient, err := grafana.NewClient(ctx, cred, managedGrafanaClient, o.SubscriptionID, o.ResourceGroup, o.GrafanaName)
	if err != nil {
		return nil, fmt.Errorf("failed to create Grafana client: %w", err)
	}

	resourceGraphClient, err := azure.NewResourceGraphDiscoveryClient(cred, clientOpts, o.DiscoveryTagKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create Resource Graph discovery client: %w", err)
	}

	completed := &CompletedReconcileOptions{
		validatedReconcileOptions:    o.validatedReconcileOptions,
		GrafanaClient:                grafanaClient,
		ManagedGrafanaClient:         managedGrafanaClient,
		ResourceGraphDiscoveryClient: resourceGraphClient,
	}

	if o.ADXIntegrationsEnabled {
		integrationFabricsClient, err := azure.NewIntegrationFabricsClient(o.SubscriptionID, cred, clientOpts)
		if err != nil {
			return nil, fmt.Errorf("failed to create integration fabrics client: %w", err)
		}
		completed.KustoDiscoveryClient = resourceGraphClient
		completed.IntegrationFabricsClient = integrationFabricsClient
	}

	return completed, nil
}
