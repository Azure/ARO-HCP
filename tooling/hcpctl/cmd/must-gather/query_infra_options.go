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

package mustgather

import (
	"context"
	"fmt"
	"net/url"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/mustgather"
)

// RawInfraQueryOptions represents the initial, unvalidated configuration for infrastructure query operations.
type RawInfraQueryOptions struct {
	BaseGatherOptions
	ServiceClusters []string // Service cluster names
	MgmtClusters    []string // Management cluster names
}

// DefaultInfraQueryOptions returns a new RawInfraQueryOptions struct initialized with sensible defaults.
func DefaultInfraQueryOptions() *RawInfraQueryOptions {
	return &RawInfraQueryOptions{
		BaseGatherOptions: DefaultBaseGatherOptions(),
	}
}

// BindInfraQueryOptions configures cobra command flags for infrastructure query options.
func BindInfraQueryOptions(opts *RawInfraQueryOptions, cmd *cobra.Command) error {
	if err := BindBaseGatherOptions(&opts.BaseGatherOptions, cmd); err != nil {
		return err
	}

	cmd.Flags().StringArrayVar(&opts.ServiceClusters, "service-cluster", opts.ServiceClusters, "service cluster name (can be specified multiple times)")
	cmd.Flags().StringArrayVar(&opts.MgmtClusters, "mgmt-cluster", opts.MgmtClusters, "management cluster name (can be specified multiple times)")

	return nil
}

// ValidatedInfraQueryOptions represents infrastructure query configuration that has passed validation.
type ValidatedInfraQueryOptions struct {
	*RawInfraQueryOptions

	KustoEndpoint *url.URL
}

// Validate performs validation of all infrastructure query input parameters.
func (o *RawInfraQueryOptions) Validate(ctx context.Context) (*ValidatedInfraQueryOptions, error) {
	kustoEndpoint, err := validateBaseGatherOptions(&o.BaseGatherOptions)
	if err != nil {
		return nil, err
	}

	if len(o.ServiceClusters) == 0 && len(o.MgmtClusters) == 0 {
		return nil, fmt.Errorf("at least one --service-cluster or --mgmt-cluster is required")
	}

	return &ValidatedInfraQueryOptions{
		RawInfraQueryOptions: o,
		KustoEndpoint:        kustoEndpoint,
	}, nil
}

// CompletedInfraQueryOptions represents the final, fully validated and initialized configuration for infrastructure query operations.
type CompletedInfraQueryOptions struct {
	*ValidatedInfraQueryOptions
	QueryClient mustgather.QueryClientInterface
}

// Complete performs final initialization to create fully usable CompletedInfraQueryOptions.
func (o *ValidatedInfraQueryOptions) Complete(ctx context.Context) (*CompletedInfraQueryOptions, error) {
	queryClient, err := completeBaseGatherOptions(o.KustoEndpoint, o.QueryTimeout, o.OutputPath)
	if err != nil {
		return nil, err
	}

	if err := createOutputDirectories(o.OutputPath, true); err != nil {
		return nil, err
	}

	return &CompletedInfraQueryOptions{
		ValidatedInfraQueryOptions: o,
		QueryClient:                queryClient,
	}, nil
}

func (opts *RawInfraQueryOptions) Run(ctx context.Context) error {
	validated, err := opts.Validate(ctx)
	if err != nil {
		return err
	}

	completed, err := validated.Complete(ctx)
	if err != nil {
		return err
	}

	return completed.RunInfra(ctx)
}
