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
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-Tools/pkg/cmdutils"

	"github.com/Azure/ARO-HCP/tooling/grafanactl/cmd/base"
	"github.com/Azure/ARO-HCP/tooling/grafanactl/internal/azure"
)

// RawAddDatasourceOptions represents the initial, unvalidated configuration for add datasource operations.
type RawAddDatasourceOptions struct {
	*base.BaseOptions
	MonitorWorkspaceIDs []string
}

// validatedAddDatasourceOptions is a private struct that enforces the options validation pattern.
type validatedAddDatasourceOptions struct {
	*RawAddDatasourceOptions
}

// ValidatedAddDatasourceOptions represents add datasource configuration that has passed validation.
type ValidatedAddDatasourceOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package
	*validatedAddDatasourceOptions
}

// CompletedAddDatasourceOptions represents the final, fully validated and initialized configuration
// for add datasource operations.
type CompletedAddDatasourceOptions struct {
	*validatedAddDatasourceOptions
	MonitorWorkspaceClient *azure.MonitorWorkspaceClient
	ManagedGrafanaClient   *azure.ManagedGrafanaClient
}

// DefaultAddDatasourceOptions returns a new RawAddDatasourceOptions with default values
func DefaultAddDatasourceOptions() *RawAddDatasourceOptions {
	return &RawAddDatasourceOptions{
		BaseOptions: base.DefaultBaseOptions(),
	}
}

// BindAddDatasourceOptions binds command-line flags to the options
func BindAddDatasourceOptions(opts *RawAddDatasourceOptions, cmd *cobra.Command) error {
	if err := base.BindBaseOptions(opts.BaseOptions, cmd); err != nil {
		return err
	}

	// Set defaults from environment variables if available
	if envWorkspaceID := os.Getenv("MONITOR_WORKSPACE_IDS"); envWorkspaceID != "" {
		opts.MonitorWorkspaceIDs = strings.Split(envWorkspaceID, ",")
	}
	if envDryRun := os.Getenv("DRY_RUN"); envDryRun != "" {
		if dryRun, err := strconv.ParseBool(envDryRun); err == nil {
			opts.DryRun = dryRun
		}
	}

	flags := cmd.Flags()
	flags.StringSliceVar(&opts.MonitorWorkspaceIDs, "monitor-workspace-ids", opts.MonitorWorkspaceIDs, "Azure Monitor Workspace resource IDs to add as datasource (required) [env: GRAFANACTL_MONITOR_WORKSPACE_IDS]")
	flags.BoolVar(&opts.DryRun, "dry-run", opts.DryRun, "Perform a dry run without making changes [env: GRAFANACTL_DRY_RUN]")

	// Mark flag as required only if not set via environment variable
	if len(opts.MonitorWorkspaceIDs) == 0 {
		_ = cmd.MarkFlagRequired("monitor-workspace-id")
	}

	return nil
}

// Validate performs validation on the raw options
func (o *RawAddDatasourceOptions) Validate(ctx context.Context) (*ValidatedAddDatasourceOptions, error) {
	if err := base.ValidateBaseOptions(o.BaseOptions); err != nil {
		return nil, err
	}

	if len(o.MonitorWorkspaceIDs) == 0 {
		return nil, fmt.Errorf("monitor workspace IDs are required")
	}

	for _, workspaceID := range o.MonitorWorkspaceIDs {
		if !strings.HasPrefix(workspaceID, "/subscriptions/") {
			return nil, fmt.Errorf("monitor workspace ID must be a valid Azure resource ID starting with /subscriptions/")
		}

		// Validate that it's an Azure Monitor Workspace resource
		if !strings.Contains(workspaceID, "/providers/Microsoft.Monitor/accounts/") {
			return nil, fmt.Errorf("monitor workspace ID must be an Azure Monitor Workspace resource ID")
		}
	}

	return &ValidatedAddDatasourceOptions{
		validatedAddDatasourceOptions: &validatedAddDatasourceOptions{
			RawAddDatasourceOptions: o,
		},
	}, nil
}

// Complete performs final initialization to create fully usable add datasource options.
func (o *ValidatedAddDatasourceOptions) Complete(ctx context.Context) (*CompletedAddDatasourceOptions, error) {
	cred, err := cmdutils.GetAzureTokenCredentials()
	if err != nil {
		return nil, fmt.Errorf("failed to obtain Azure credentials: %w", err)
	}

	managedGrafanaClient, err := azure.NewManagedGrafanaClient(o.SubscriptionID, cred)
	if err != nil {
		return nil, fmt.Errorf("failed to create managed Grafana client: %w", err)
	}

	monitorWorkspaceClient, err := azure.NewMonitorWorkspaceClient(o.SubscriptionID, cred)
	if err != nil {
		return nil, fmt.Errorf("failed to create monitor workspace client: %w", err)
	}

	return &CompletedAddDatasourceOptions{
		validatedAddDatasourceOptions: o.validatedAddDatasourceOptions,
		MonitorWorkspaceClient:        monitorWorkspaceClient,
		ManagedGrafanaClient:          managedGrafanaClient,
	}, nil
}
