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

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-Tools/tools/cmdutils"
	"github.com/Azure/ARO-Tools/tools/grafanactl/cmd/base"
	"github.com/Azure/ARO-Tools/tools/grafanactl/internal/azure"
	"github.com/Azure/ARO-Tools/tools/grafanactl/internal/grafana"
)

// RawAddDatasourceOptions represents the initial, unvalidated configuration for add datasource operations.
type RawAddDatasourceOptions struct {
	*base.BaseOptions
	TagKey   string
	TagValue string
}

// validatedAddDatasourceOptions is a private struct that enforces the options validation pattern.
type validatedAddDatasourceOptions struct {
	*RawAddDatasourceOptions
	*base.CompletedBaseOptions
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
	GrafanaClient                *grafana.Client
	ManagedGrafanaClient         *azure.ManagedGrafanaClient
	ResourceGraphDiscoveryClient *azure.ResourceGraphDiscoveryClient
}

// DefaultAddDatasourceOptions returns a new RawAddDatasourceOptions with default values
func DefaultAddDatasourceOptions() *RawAddDatasourceOptions {
	return &RawAddDatasourceOptions{
		BaseOptions: base.DefaultBaseOptions(),
		TagKey:      "grafanactl-discovery",
		TagValue:    "true",
	}
}

// BindAddDatasourceOptions binds command-line flags to the options
func BindAddDatasourceOptions(opts *RawAddDatasourceOptions, cmd *cobra.Command) error {
	if err := base.BindBaseOptions(opts.BaseOptions, cmd); err != nil {
		return err
	}

	flags := cmd.Flags()
	flags.StringVar(&opts.TagKey, "tag-key", opts.TagKey, "Azure Monitor Workspace tag key to filter by")
	flags.StringVar(&opts.TagValue, "tag-value", opts.TagValue, "Azure Monitor Workspace tag value to filter by")

	return nil
}

// Validate performs validation on the raw options
func (o *RawAddDatasourceOptions) Validate(ctx context.Context) (*ValidatedAddDatasourceOptions, error) {
	completedBase, err := base.ValidateBaseOptions(o.BaseOptions)
	if err != nil {
		return nil, err
	}

	return &ValidatedAddDatasourceOptions{
		validatedAddDatasourceOptions: &validatedAddDatasourceOptions{
			RawAddDatasourceOptions: &RawAddDatasourceOptions{
				BaseOptions: o.BaseOptions,
				TagKey:      o.TagKey,
				TagValue:    o.TagValue,
			},
			CompletedBaseOptions: completedBase,
		},
	}, nil
}

// Complete performs final initialization to create fully usable add datasource options.
func (o *ValidatedAddDatasourceOptions) Complete(ctx context.Context) (*CompletedAddDatasourceOptions, error) {
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

	return &CompletedAddDatasourceOptions{
		validatedAddDatasourceOptions: o.validatedAddDatasourceOptions,
		GrafanaClient:                 grafanaClient,
		ManagedGrafanaClient:          managedGrafanaClient,
		ResourceGraphDiscoveryClient:  resourceGraphClient,
	}, nil
}
