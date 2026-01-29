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

package list

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-Tools/pkg/cmdutils"

	"github.com/Azure/ARO-HCP/tooling/grafanactl/cmd/base"
	"github.com/Azure/ARO-HCP/tooling/grafanactl/internal/azure"
	"github.com/Azure/ARO-HCP/tooling/grafanactl/internal/grafana"
)

// RawListDataSourcesOptions represents the initial, unvalidated configuration for list-datasources operations.
type RawListDataSourcesOptions struct {
	*base.BaseOptions
}

// validatedListDataSourcesOptions is a private struct that enforces the options validation pattern.
type validatedListDataSourcesOptions struct {
	*RawListDataSourcesOptions
}

// ValidatedListDataSourcesOptions represents list-datasources configuration that has passed validation.
type ValidatedListDataSourcesOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package
	*validatedListDataSourcesOptions
}

// CompletedListDataSourcesOptions represents the final, fully validated and initialized configuration
// for list-datasources operations.
type CompletedListDataSourcesOptions struct {
	*validatedListDataSourcesOptions
	GrafanaClient *grafana.Client
}

// DefaultListDataSourcesOptions returns a new RawListDataSourcesOptions with default values
func DefaultListDataSourcesOptions() *RawListDataSourcesOptions {
	return &RawListDataSourcesOptions{
		BaseOptions: base.DefaultBaseOptions(),
	}
}

// BindListDataSourcesOptions binds command-line flags to the options
func BindListDataSourcesOptions(opts *RawListDataSourcesOptions, cmd *cobra.Command) error {
	return base.BindBaseOptions(opts.BaseOptions, cmd)
}

// Validate performs validation on the raw options
func (o *RawListDataSourcesOptions) Validate(ctx context.Context) (*ValidatedListDataSourcesOptions, error) {
	if err := base.ValidateBaseOptions(o.BaseOptions); err != nil {
		return nil, err
	}

	return &ValidatedListDataSourcesOptions{
		validatedListDataSourcesOptions: &validatedListDataSourcesOptions{
			RawListDataSourcesOptions: o,
		},
	}, nil
}

// Complete performs final initialization to create fully usable list-datasources options.
func (o *ValidatedListDataSourcesOptions) Complete(ctx context.Context) (*CompletedListDataSourcesOptions, error) {
	cred, err := cmdutils.GetAzureTokenCredentials()
	if err != nil {
		return nil, fmt.Errorf("failed to obtain Azure credentials: %w", err)
	}
	managedGrafanaClient, err := azure.NewManagedGrafanaClient(o.SubscriptionID, cred)
	if err != nil {
		return nil, fmt.Errorf("failed to create Managed Grafana client: %w", err)
	}

	grafanaClient, err := grafana.NewClient(ctx, cred, managedGrafanaClient, o.SubscriptionID, o.ResourceGroup, o.GrafanaName)
	if err != nil {
		return nil, fmt.Errorf("failed to create Grafana client: %w", err)
	}

	return &CompletedListDataSourcesOptions{
		validatedListDataSourcesOptions: o.validatedListDataSourcesOptions,
		GrafanaClient:                   grafanaClient,
	}, nil
}
