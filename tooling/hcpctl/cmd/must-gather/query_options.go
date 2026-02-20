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
	"os"
	"path"
	"time"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/mustgather"
)

// RawMustGatherOptions represents the initial, unvalidated configuration for must-gather operations.
type RawMustGatherOptions struct {
	Kusto                      string        // Name of the Azure Data Explorer cluster
	Region                     string        // Region of the Azure Data Explorer cluster
	OutputPath                 string        // Path to write the output file
	QueryTimeout               time.Duration // Timeout for query execution
	SubscriptionID             string        // Subscription ID
	ResourceGroup              string        // Resource group
	ResourceId                 string        // Resource ID
	SkipHostedControlPlaneLogs bool          // Skip hosted control plane logs
	TimestampMin               time.Time     // Timestamp minimum
	TimestampMax               time.Time     // Timestamp maximum
	Limit                      int           // Limit the number of results
	GatherInfraLogs            bool          // Gather all logs from the infrastructure, does NOT gather HCP logs
	InfraClusterName           string        // Name of the infrastructure cluster
}

// DefaultMustGatherOptions returns a new RawMustGatherOptions struct initialized with sensible defaults.
func DefaultMustGatherOptions() *RawMustGatherOptions {
	return &RawMustGatherOptions{
		QueryTimeout: 5 * time.Minute,
		TimestampMin: time.Now().Add(-24 * time.Hour),
		TimestampMax: time.Now(),
		Limit:        -1, // defaults to no limit
		OutputPath:   fmt.Sprintf("must-gather-%s", time.Now().Format("20060102-150405")),
	}
}

func (opts *RawMustGatherOptions) Run(ctx context.Context, runLegacy bool) error {
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

	return completed.Run(ctx)
}

// BindMustGatherOptions configures cobra command flags for must-gather specific options.
func BindMustGatherOptions(opts *RawMustGatherOptions, cmd *cobra.Command) error {
	// Add must-gather specific flags
	cmd.Flags().StringVar(&opts.Kusto, "kusto", opts.Kusto, "Azure Data Explorer cluster name (required)")
	cmd.Flags().StringVar(&opts.Region, "region", opts.Region, "Azure Data Explorer cluster region (required)")
	cmd.Flags().DurationVar(&opts.QueryTimeout, "query-timeout", opts.QueryTimeout, "timeout for query execution")
	cmd.Flags().StringVar(&opts.OutputPath, "output-path", opts.OutputPath, "path to write the output file")
	cmd.Flags().BoolVar(&opts.GatherInfraLogs, "gather-infra-logs", false, "gather all logs from the infrastructure, does NOT gather HCP logs")
	cmd.Flags().StringVar(&opts.InfraClusterName, "infra-cluster-name", opts.InfraClusterName, "name of the infrastructure cluster")
	cmd.Flags().StringVar(&opts.SubscriptionID, "subscription-id", opts.SubscriptionID, "subscription ID")
	cmd.Flags().StringVar(&opts.ResourceGroup, "resource-group", opts.ResourceGroup, "resource group")
	cmd.Flags().StringVar(&opts.ResourceId, "resource-id", opts.ResourceId, "resource ID")
	cmd.Flags().BoolVar(&opts.SkipHostedControlPlaneLogs, "skip-hcp-logs", opts.SkipHostedControlPlaneLogs, "Do not gather customer (ocm namespaces) logs")
	cmd.Flags().TimeVar(&opts.TimestampMin, "timestamp-min", opts.TimestampMin, []string{time.DateTime}, "timestamp minimum")
	cmd.Flags().TimeVar(&opts.TimestampMax, "timestamp-max", opts.TimestampMax, []string{time.DateTime}, "timestamp maximum")
	cmd.Flags().IntVar(&opts.Limit, "limit", opts.Limit, "limit the number of results")

	// Mark required flags
	requiredFlags := []string{"kusto", "region"}
	for _, flag := range requiredFlags {
		if err := cmd.MarkFlagRequired(flag); err != nil {
			return fmt.Errorf("failed to mark %s as required: %w", flag, err)
		}
	}

	cmd.MarkFlagsMutuallyExclusive("subscription-id", "resource-id")
	cmd.MarkFlagsMutuallyExclusive("resource-group", "resource-id")

	cmd.MarkFlagsRequiredTogether("subscription-id", "resource-group")

	return nil
}

// ValidatedMustGatherOptions represents must-gather configuration that has passed validation.
type ValidatedMustGatherOptions struct {
	*RawMustGatherOptions

	KustoEndpoint *url.URL
	QueryOptions  mustgather.QueryOptions
}

// Validate performs comprehensive validation of all must-gather input parameters.
func (o *RawMustGatherOptions) Validate(ctx context.Context) (*ValidatedMustGatherOptions, error) {
	logger := logr.FromContextOrDiscard(ctx)

	// Validate kusto name
	if o.Kusto == "" {
		return nil, fmt.Errorf("kusto is required")
	}
	// Validate region
	if o.Region == "" {
		return nil, fmt.Errorf("region is required")
	}

	// form kusto URL
	kustoEndpoint, err := kusto.KustoEndpoint(o.Kusto, o.Region)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kusto endpoint: %w", err)
	}

	// Validate query timeout
	if o.QueryTimeout < 30*time.Second {
		return nil, fmt.Errorf("query timeout must be at least 30 seconds")
	}

	if o.QueryTimeout > 30*time.Minute {
		return nil, fmt.Errorf("query timeout cannot exceed 30 minutes")
	}

	// Validate subscription ID
	if o.SubscriptionID == "" && o.ResourceId == "" && o.InfraClusterName == "" {
		return nil, fmt.Errorf("subscription-id is required")
	}

	// Validate resource group
	if o.ResourceGroup == "" && o.ResourceId == "" && o.InfraClusterName == "" {
		return nil, fmt.Errorf("resource-group is required")
	}

	if o.ResourceId != "" && (o.ResourceGroup != "" || o.SubscriptionID != "" || o.InfraClusterName != "") {
		logger.Info("warning: both resource-id and resource-group/subscription-id/infra-cluster-name are provided, will use resource-id to gather cluster ID")
	}

	if o.TimestampMin.After(o.TimestampMax) {
		return nil, fmt.Errorf("timestamp-min cannot be after timestamp-max")
	}

	return &ValidatedMustGatherOptions{
		RawMustGatherOptions: o,
		KustoEndpoint:        kustoEndpoint,
		QueryOptions: mustgather.QueryOptions{
			SubscriptionId:    o.SubscriptionID,
			ResourceGroupName: o.ResourceGroup,
			TimestampMin:      o.TimestampMin,
			TimestampMax:      o.TimestampMax,
			Limit:             o.Limit,
		},
	}, nil
}

// Complete performs final initialization to create fully usable MustGatherOptions.
func (o *ValidatedMustGatherOptions) Complete(ctx context.Context) (*MustGatherOptions, error) {
	client, err := kusto.NewClient(o.KustoEndpoint, o.QueryTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kusto client: %w", err)
	}

	err = os.MkdirAll(path.Join(o.OutputPath, ServicesLogDirectory), 0755)
	if err != nil {
		return nil, fmt.Errorf("failed to create service logs directory: %w", err)
	}

	if !o.SkipHostedControlPlaneLogs {
		err = os.MkdirAll(path.Join(o.OutputPath, HostedControlPlaneLogDirectory), 0755)
		if err != nil {
			return nil, fmt.Errorf("failed to create customer logs directory: %w", err)
		}
	}

	return &MustGatherOptions{
		ValidatedMustGatherOptions: o,
		QueryClient:                mustgather.NewQueryClient(client, o.QueryTimeout, o.OutputPath),
	}, nil
}

// MustGatherOptions represents the final, fully validated and initialized configuration for must-gather operations.
type MustGatherOptions struct {
	*ValidatedMustGatherOptions
	QueryClient mustgather.QueryClientInterface
}
