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
	"os"
	"path"
	"time"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/cmd/base"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
)

// RawMustGatherOptions represents the initial, unvalidated configuration for must-gather operations.
type RawMustGatherOptions struct {
	BaseOptions      *base.RawBaseOptions
	KustoDebug       bool          // Print debug information
	Kusto            string        // Name of the Azure Data Explorer cluster
	Region           string        // Region of the Azure Data Explorer cluster
	OutputPath       string        // Path to write the output file
	QueryTimeout     time.Duration // Timeout for query execution
	SubscriptionID   string        // Subscription ID
	ResourceGroup    string        // Resource group
	SkipCustomerLogs bool          // Skip customer logs
	TimestampMin     time.Time     // Timestamp minimum
	TimestampMax     time.Time     // Timestamp maximum
	Limit            int           // Limit the number of results
}

// DefaultMustGatherOptions returns a new RawMustGatherOptions struct initialized with sensible defaults.
func DefaultMustGatherOptions() *RawMustGatherOptions {
	return &RawMustGatherOptions{
		BaseOptions:  base.DefaultBaseOptions(),
		QueryTimeout: 5 * time.Minute,
	}
}

// BindMustGatherOptions configures cobra command flags for must-gather specific options.
func BindMustGatherOptions(opts *RawMustGatherOptions, cmd *cobra.Command) error {
	// Bind base options first
	if opts.BaseOptions == nil {
		return fmt.Errorf("base options cannot be nil")
	}
	if err := base.BindBaseOptions(opts.BaseOptions, cmd); err != nil {
		return fmt.Errorf("failed to bind base options: %w", err)
	}

	// Add must-gather specific flags
	cmd.Flags().StringVar(&opts.Kusto, "kusto", opts.Kusto, "Azure Data Explorer cluster name (required)")
	cmd.Flags().StringVar(&opts.Region, "region", opts.Region, "Azure Data Explorer cluster region (required)")
	cmd.Flags().DurationVar(&opts.QueryTimeout, "query-timeout", opts.QueryTimeout, "timeout for query execution")
	cmd.Flags().StringVar(&opts.OutputPath, "output-path", opts.OutputPath, "path to write the output file")
	cmd.Flags().StringVar(&opts.SubscriptionID, "subscription-id", opts.SubscriptionID, "subscription ID")
	cmd.Flags().StringVar(&opts.ResourceGroup, "resource-group", opts.ResourceGroup, "resource group")
	cmd.Flags().BoolVar(&opts.SkipCustomerLogs, "skip-customer-logs", opts.SkipCustomerLogs, "Do not gather customer (ocm namespaces) logs")
	cmd.Flags().TimeVar(&opts.TimestampMin, "timestamp-min", opts.TimestampMin, []string{time.DateTime}, "timestamp minimum")
	cmd.Flags().TimeVar(&opts.TimestampMax, "timestamp-max", opts.TimestampMax, []string{time.DateTime}, "timestamp maximum")
	cmd.Flags().BoolVar(&opts.KustoDebug, "kusto-debug", opts.KustoDebug, "print debug information")
	cmd.Flags().IntVar(&opts.Limit, "limit", opts.Limit, "limit the number of results")

	// Mark required flags
	if err := cmd.MarkFlagRequired("kusto"); err != nil {
		return fmt.Errorf("failed to mark kusto as required: %w", err)
	}
	if err := cmd.MarkFlagRequired("region"); err != nil {
		return fmt.Errorf("failed to mark region as required: %w", err)
	}
	if err := cmd.MarkFlagRequired("subscription-id"); err != nil {
		return fmt.Errorf("failed to mark subscription-id as required: %w", err)
	}
	if err := cmd.MarkFlagRequired("resource-group"); err != nil {
		return fmt.Errorf("failed to mark resource-group as required: %w", err)
	}

	return nil
}

// ValidatedMustGatherOptions represents must-gather configuration that has passed validation.
type ValidatedMustGatherOptions struct {
	*RawMustGatherOptions
	QueryOptions QueryOptions
}

// Validate performs comprehensive validation of all must-gather input parameters.
func (o *RawMustGatherOptions) Validate(ctx context.Context) (*ValidatedMustGatherOptions, error) {
	// Validate base options first
	if err := base.ValidateBaseOptions(o.BaseOptions); err != nil {
		return nil, err
	}

	// Validate kusto name
	if o.Kusto == "" {
		return nil, fmt.Errorf("kusto is required")
	}
	// Validate region
	if o.Region == "" {
		return nil, fmt.Errorf("region is required")
	}

	// Validate query timeout
	if o.QueryTimeout < 30*time.Second {
		return nil, fmt.Errorf("query timeout must be at least 30 seconds")
	}

	if o.QueryTimeout > 30*time.Minute {
		return nil, fmt.Errorf("query timeout cannot exceed 30 minutes")
	}

	// Validate subscription ID
	if o.SubscriptionID == "" {
		return nil, fmt.Errorf("subscription-id is required")
	}

	// Validate resource group
	if o.ResourceGroup == "" {
		return nil, fmt.Errorf("resource-group is required")
	}

	return &ValidatedMustGatherOptions{
		RawMustGatherOptions: o,
		QueryOptions: QueryOptions{
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
	// Set default output path if not specified
	if o.OutputPath == "" {
		o.OutputPath = fmt.Sprintf("must-gather-%s", time.Now().Format("20060102-150405"))
	}

	endpoint := fmt.Sprintf("https://%s.%s.kusto.windows.net", o.Kusto, o.Region)
	client, err := kusto.NewClient(endpoint, o.KustoDebug)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kusto client: %w", err)
	}

	err = os.MkdirAll(path.Join(o.OutputPath, "serviceLogs"), 0755)
	if err != nil {
		return nil, fmt.Errorf("failed to create service logs directory: %w", err)
	}

	if !o.SkipCustomerLogs {
		err = os.MkdirAll(path.Join(o.OutputPath, "customerLogs"), 0755)
		if err != nil {
			return nil, fmt.Errorf("failed to create customer logs directory: %w", err)
		}
	}

	return &MustGatherOptions{
		ValidatedMustGatherOptions: o,
		Client:                     client,
	}, nil
}

// MustGatherOptions represents the final, fully validated and initialized configuration for must-gather operations.
type MustGatherOptions struct {
	*ValidatedMustGatherOptions
	Client *kusto.Client
}
