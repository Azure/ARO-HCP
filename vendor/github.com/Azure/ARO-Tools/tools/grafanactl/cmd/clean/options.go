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

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-Tools/tools/cmdutils"
	"github.com/Azure/ARO-Tools/tools/grafanactl/cmd/base"
	"github.com/Azure/ARO-Tools/tools/grafanactl/internal/azure"
	"github.com/Azure/ARO-Tools/tools/grafanactl/internal/grafana"
)

// RawCleanOptions represents the initial, unvalidated configuration for clean operations.
type RawCleanDatasourcesOptions struct {
	*base.BaseOptions
}

// validatedCleanOptions is a private struct that enforces the options validation pattern.
type validatedCleanDatasourcesOptions struct {
	*RawCleanDatasourcesOptions
	*base.CompletedBaseOptions
}

// ValidatedCleanOptions represents clean configuration that has passed validation.
type ValidatedCleanDatasourcesOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package
	*validatedCleanDatasourcesOptions
}

// CompletedCleanOptions represents the final, fully validated and initialized configuration
// for clean operations.
type CompletedCleanDatasourcesOptions struct {
	*validatedCleanDatasourcesOptions
	GrafanaClient                *grafana.Client
	MonitorWorkspaceClient       *azure.MonitorWorkspaceClient
	ResourceGraphDiscoveryClient *azure.ResourceGraphDiscoveryClient
	ManagedGrafanaClient         *azure.ManagedGrafanaClient
}

// DefaultCleanOptions returns a new RawCleanOptions with default values
func DefaultCleanDatasourcesOptions() *RawCleanDatasourcesOptions {
	return &RawCleanDatasourcesOptions{
		BaseOptions: base.DefaultBaseOptions(),
	}
}

// BindCleanOptions binds command-line flags to the options
func BindCleanDatasourcesOptions(opts *RawCleanDatasourcesOptions, cmd *cobra.Command) error {
	if err := base.BindBaseOptions(opts.BaseOptions, cmd); err != nil {
		return err
	}

	return nil
}

// Validate performs validation on the raw options
func (o *RawCleanDatasourcesOptions) Validate(ctx context.Context) (*ValidatedCleanDatasourcesOptions, error) {
	completedBase, err := base.ValidateBaseOptions(o.BaseOptions)
	if err != nil {
		return nil, err
	}

	return &ValidatedCleanDatasourcesOptions{
		validatedCleanDatasourcesOptions: &validatedCleanDatasourcesOptions{
			RawCleanDatasourcesOptions: o,
			CompletedBaseOptions:       completedBase,
		},
	}, nil
}

// Complete performs final initialization to create fully usable clean options.
func (o *ValidatedCleanDatasourcesOptions) Complete(ctx context.Context) (*CompletedCleanDatasourcesOptions, error) {
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

	monitorWorkspaceClient, err := azure.NewMonitorWorkspaceClient(o.SubscriptionID, cred, clientOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to create managed Prometheus client: %w", err)
	}

	resourceGraphClient, err := azure.NewResourceGraphDiscoveryClient(cred, clientOpts, o.DiscoveryTagKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create Resource Graph discovery client: %w", err)
	}

	return &CompletedCleanDatasourcesOptions{
		validatedCleanDatasourcesOptions: o.validatedCleanDatasourcesOptions,
		GrafanaClient:                    grafanaClient,
		MonitorWorkspaceClient:           monitorWorkspaceClient,
		ResourceGraphDiscoveryClient:     resourceGraphClient,
		ManagedGrafanaClient:             managedGrafanaClient,
	}, nil
}
