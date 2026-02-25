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

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/mustgather"
)

// RawQueryOptions represents the initial, unvalidated configuration for query operations.
type RawQueryOptions struct {
	BaseGatherOptions
	SubscriptionID             string // Subscription ID
	ResourceGroup              string // Resource group
	ResourceId                 string // Resource ID
	SkipHostedControlPlaneLogs bool   // Skip hosted control plane logs
	SkipKubernetesEventsLogs   bool   // Skip Kubernetes events logs
	SkipSystemdLogs        bool   // Skip Systemd logs
}

// DefaultQueryOptions returns a new RawQueryOptions struct initialized with sensible defaults.
func DefaultQueryOptions() *RawQueryOptions {
	return &RawQueryOptions{
		BaseGatherOptions: DefaultBaseGatherOptions(),
	}
}

// BindQueryOptions configures cobra command flags for query specific options.
func BindQueryOptions(opts *RawQueryOptions, cmd *cobra.Command) error {
	if err := BindBaseGatherOptions(&opts.BaseGatherOptions, cmd); err != nil {
		return err
	}

	cmd.Flags().StringVar(&opts.SubscriptionID, "subscription-id", opts.SubscriptionID, "subscription ID")
	cmd.Flags().StringVar(&opts.ResourceGroup, "resource-group", opts.ResourceGroup, "resource group")
	cmd.Flags().StringVar(&opts.ResourceId, "resource-id", opts.ResourceId, "resource ID")
	cmd.Flags().BoolVar(&opts.SkipHostedControlPlaneLogs, "skip-hcp-logs", opts.SkipHostedControlPlaneLogs, "Do not gather customer (ocm namespaces) logs")
	cmd.Flags().BoolVar(&opts.SkipKubernetesEventsLogs, "skip-kubernetes-events-logs", opts.SkipKubernetesEventsLogs, "Do not gather Kubernetes events logs")
	cmd.Flags().BoolVar(&opts.SkipSystemdLogs, "skip-systemd-logs", opts.SkipSystemdLogs, "Do not gather Systemd logs")

	cmd.MarkFlagsMutuallyExclusive("subscription-id", "resource-id")
	cmd.MarkFlagsMutuallyExclusive("resource-group", "resource-id")

	cmd.MarkFlagsRequiredTogether("subscription-id", "resource-group")

	return nil
}

// ValidatedQueryOptions represents query configuration that has passed validation.
type ValidatedQueryOptions struct {
	*RawQueryOptions

	KustoEndpoint *url.URL
	QueryOptions  mustgather.QueryOptions
}

// Validate performs comprehensive validation of all query input parameters.
func (o *RawQueryOptions) Validate(ctx context.Context) (*ValidatedQueryOptions, error) {
	logger := logr.FromContextOrDiscard(ctx)

	kustoEndpoint, err := validateBaseGatherOptions(&o.BaseGatherOptions)
	if err != nil {
		return nil, err
	}

	if o.SubscriptionID == "" && o.ResourceId == "" {
		return nil, fmt.Errorf("subscription-id is required")
	}
	if o.ResourceGroup == "" && o.ResourceId == "" {
		return nil, fmt.Errorf("resource-group is required")
	}
	if o.ResourceId != "" && (o.ResourceGroup != "" || o.SubscriptionID != "") {
		logger.Info("warning: both resource-id and resource-group/subscription-id are provided, will use resource-id to gather cluster ID")
	}

	return &ValidatedQueryOptions{
		RawQueryOptions: o,
		KustoEndpoint:   kustoEndpoint,
		QueryOptions: mustgather.QueryOptions{
			SubscriptionId:    o.SubscriptionID,
			ResourceGroupName: o.ResourceGroup,
			TimestampMin:      o.TimestampMin,
			TimestampMax:      o.TimestampMax,
			Limit:             o.Limit,
		},
	}, nil
}

// CompletedQueryOptions represents the final, fully validated and initialized configuration for query operations.
type CompletedQueryOptions struct {
	*ValidatedQueryOptions
	QueryClient mustgather.QueryClientInterface
}

// Complete performs final initialization to create fully usable CompletedQueryOptions.
func (o *ValidatedQueryOptions) Complete(ctx context.Context) (*CompletedQueryOptions, error) {
	queryClient, err := completeBaseGatherOptions(o.KustoEndpoint, o.QueryTimeout, o.OutputPath)
	if err != nil {
		return nil, err
	}

	if err := createOutputDirectories(o.OutputPath, o.SkipHostedControlPlaneLogs, o.SkipKubernetesEventsLogs, o.SkipSystemdLogs); err != nil {
		return nil, err
	}

	return &CompletedQueryOptions{
		ValidatedQueryOptions: o,
		QueryClient:           queryClient,
	}, nil
}

func (opts *RawQueryOptions) Run(ctx context.Context, runLegacy bool) error {
	validated, err := opts.Validate(ctx)
	if err != nil {
		return err
	}

	completed, err := validated.Complete(ctx)
	if err != nil {
		return err
	}

	if runLegacy {
		return completed.RunLegacy(ctx)
	}

	return completed.RunQuery(ctx)
}
