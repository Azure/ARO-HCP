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

package sync

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-Tools/pkg/cmdutils"

	"github.com/Azure/ARO-HCP/tooling/grafanactl/cmd/base"
	"github.com/Azure/ARO-HCP/tooling/grafanactl/internal/azure"
	"github.com/Azure/ARO-HCP/tooling/grafanactl/internal/config"
	"github.com/Azure/ARO-HCP/tooling/grafanactl/internal/grafana"
)

// RawSyncDashboardsOptions represents the initial, unvalidated configuration for sync operations.
type RawSyncDashboardsOptions struct {
	*base.BaseOptions
	DryRun         bool
	ConfigFilePath string
}

// validatedSyncDashboardsOptions is a private struct that enforces the options validation pattern.
type validatedSyncDashboardsOptions struct {
	*RawSyncDashboardsOptions
}

// ValidatedSyncDashboardsOptions represents sync configuration that has passed validation.
type ValidatedSyncDashboardsOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package
	*validatedSyncDashboardsOptions
}

// CompletedSyncDashboardsOptions represents the final, fully validated and initialized configuration
// for sync operations.
type CompletedSyncDashboardsOptions struct {
	*validatedSyncDashboardsOptions
	GrafanaClient *grafana.Client
	Config        *config.ObservabilityConfig
}

// DefaultSyncDashboardsOptions returns a new RawSyncDashboardsOptions with default values
func DefaultSyncDashboardsOptions() *RawSyncDashboardsOptions {
	return &RawSyncDashboardsOptions{
		BaseOptions: base.DefaultBaseOptions(),
		DryRun:      false,
	}
}

// BindSyncDashboardsOptions binds command-line flags to the options
func BindSyncDashboardsOptions(opts *RawSyncDashboardsOptions, cmd *cobra.Command) error {
	if err := base.BindBaseOptions(opts.BaseOptions, cmd); err != nil {
		return err
	}

	flags := cmd.Flags()
	flags.BoolVar(&opts.DryRun, "dry-run", false, "Perform a dry run without making changes")
	flags.StringVar(&opts.ConfigFilePath, "config-file", "", "Path to config file with Grafana dashboard references (absolute or relative path, required)")

	_ = cmd.MarkFlagRequired("config-file")
	return nil
}

// Validate performs validation on the raw options
func (o *RawSyncDashboardsOptions) Validate(ctx context.Context) (*ValidatedSyncDashboardsOptions, error) {
	if err := base.ValidateBaseOptions(o.BaseOptions); err != nil {
		return nil, err
	}

	absPath, err := filepath.Abs(o.ConfigFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve config file path: %w", err)
	}

	if _, err := os.Stat(absPath); err != nil {
		return nil, fmt.Errorf("config file not found: %w", err)
	}

	o.ConfigFilePath = absPath

	return &ValidatedSyncDashboardsOptions{
		validatedSyncDashboardsOptions: &validatedSyncDashboardsOptions{
			RawSyncDashboardsOptions: o,
		},
	}, nil
}

// Complete performs final initialization to create fully usable sync options.
func (o *ValidatedSyncDashboardsOptions) Complete(ctx context.Context) (*CompletedSyncDashboardsOptions, error) {
	cfg, err := config.LoadFromFile(o.ConfigFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

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

	return &CompletedSyncDashboardsOptions{
		validatedSyncDashboardsOptions: o.validatedSyncDashboardsOptions,
		GrafanaClient:                  grafanaClient,
		Config:                         cfg,
	}, nil
}
