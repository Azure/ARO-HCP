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

	"github.com/Azure/ARO-HCP/tooling/grafanactl/cmd/base"
	"github.com/Azure/ARO-HCP/tooling/grafanactl/internal/azure"
	"github.com/Azure/ARO-HCP/tooling/grafanactl/internal/grafana"
	"github.com/Azure/ARO-Tools/pkg/cmdutils"
)

// RawCleanOptions represents the initial, unvalidated configuration for clean operations.
type RawCleanDatasourcesOptions struct {
	*base.BaseOptions
	DryRun bool
}

// validatedCleanOptions is a private struct that enforces the options validation pattern.
type validatedCleanDatasourcesOptions struct {
	*RawCleanDatasourcesOptions
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
	GrafanaClient          *grafana.Client
	MonitorWorkspaceClient *azure.MonitorWorkspaceClient
	ManagedGrafanaClient   *azure.ManagedGrafanaClient
}

// DefaultCleanOptions returns a new RawCleanOptions with default values
func DefaultCleanDatasourcesOptions() *RawCleanDatasourcesOptions {
	return &RawCleanDatasourcesOptions{
		BaseOptions: base.DefaultBaseOptions(),
		DryRun:      false,
	}
}

// BindCleanOptions binds command-line flags to the options
func BindCleanDatasourcesOptions(opts *RawCleanDatasourcesOptions, cmd *cobra.Command) error {
	if err := base.BindBaseOptions(opts.BaseOptions, cmd); err != nil {
		return err
	}

	flags := cmd.Flags()
	flags.BoolVar(&opts.DryRun, "dry-run", false, "Perform a dry run without making changes")
	return nil
}

// Validate performs validation on the raw options
func (o *RawCleanDatasourcesOptions) Validate(ctx context.Context) (*ValidatedCleanDatasourcesOptions, error) {
	if err := base.ValidateBaseOptions(o.BaseOptions); err != nil {
		return nil, err
	}

	return &ValidatedCleanDatasourcesOptions{
		validatedCleanDatasourcesOptions: &validatedCleanDatasourcesOptions{
			RawCleanDatasourcesOptions: o,
		},
	}, nil
}

// Complete performs final initialization to create fully usable clean options.
func (o *ValidatedCleanDatasourcesOptions) Complete(ctx context.Context) (*CompletedCleanDatasourcesOptions, error) {
	cred, err := cmdutils.GetAzureTokenCredentials()
	if err != nil {
		return nil, fmt.Errorf("failed to obtain Azure credentials: %w", err)
	}

	managedGrafanaClient, err := azure.NewManagedGrafanaClient(o.SubscriptionID, cred)
	if err != nil {
		return nil, fmt.Errorf("failed to create managed Grafana client: %w", err)
	}

	grafanaClient, err := grafana.NewClient(ctx, cred, managedGrafanaClient, o.SubscriptionID, o.ResourceGroup, o.GrafanaName)
	if err != nil {
		return nil, fmt.Errorf("failed to create Grafana client: %w", err)
	}

	monitorWorkspaceClient, err := azure.NewMonitorWorkspaceClient(o.SubscriptionID, cred)
	if err != nil {
		return nil, fmt.Errorf("failed to create managed Prometheus client: %w", err)
	}

	return &CompletedCleanDatasourcesOptions{
		validatedCleanDatasourcesOptions: o.validatedCleanDatasourcesOptions,
		GrafanaClient:                    grafanaClient,
		MonitorWorkspaceClient:           monitorWorkspaceClient,
		ManagedGrafanaClient:             managedGrafanaClient,
	}, nil
}
