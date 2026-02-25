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
	"fmt"
	"net/url"
	"os"
	"path"
	"time"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/mustgather"
)

// BaseGatherOptions holds configuration shared across all query commands.
type BaseGatherOptions struct {
	Kusto        string        // Name of the Azure Data Explorer cluster
	Region       string        // Region of the Azure Data Explorer cluster
	OutputPath   string        // Path to write the output file
	QueryTimeout time.Duration // Timeout for query execution
	TimestampMin time.Time     // Timestamp minimum
	TimestampMax time.Time     // Timestamp maximum
	Limit        int           // Limit the number of results
}

// DefaultBaseGatherOptions returns BaseGatherOptions initialized with sensible defaults.
func DefaultBaseGatherOptions() BaseGatherOptions {
	return BaseGatherOptions{
		QueryTimeout: 5 * time.Minute,
		TimestampMin: time.Now().Add(-24 * time.Hour),
		TimestampMax: time.Now(),
		Limit:        -1, // defaults to no limit
		OutputPath:   fmt.Sprintf("must-gather-%s", time.Now().Format("20060102-150405")),
	}
}

// BindBaseGatherOptions configures cobra command flags for the shared gather options.
func BindBaseGatherOptions(opts *BaseGatherOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.Kusto, "kusto", opts.Kusto, "Azure Data Explorer cluster name (required)")
	cmd.Flags().StringVar(&opts.Region, "region", opts.Region, "Azure Data Explorer cluster region (required)")
	cmd.Flags().DurationVar(&opts.QueryTimeout, "query-timeout", opts.QueryTimeout, "timeout for query execution")
	cmd.Flags().StringVar(&opts.OutputPath, "output-path", opts.OutputPath, "path to write the output file")
	cmd.Flags().TimeVar(&opts.TimestampMin, "timestamp-min", opts.TimestampMin, []string{time.DateTime}, "timestamp minimum")
	cmd.Flags().TimeVar(&opts.TimestampMax, "timestamp-max", opts.TimestampMax, []string{time.DateTime}, "timestamp maximum")
	cmd.Flags().IntVar(&opts.Limit, "limit", opts.Limit, "limit the number of results")

	requiredFlags := []string{"kusto", "region"}
	for _, flag := range requiredFlags {
		if err := cmd.MarkFlagRequired(flag); err != nil {
			return fmt.Errorf("failed to mark %s as required: %w", flag, err)
		}
	}

	return nil
}

// validateBaseGatherOptions validates the shared gather options and returns a kusto endpoint URL.
func validateBaseGatherOptions(opts *BaseGatherOptions) (*url.URL, error) {
	if opts.Kusto == "" {
		return nil, fmt.Errorf("kusto is required")
	}
	if opts.Region == "" {
		return nil, fmt.Errorf("region is required")
	}

	kustoEndpoint, err := kusto.KustoEndpoint(opts.Kusto, opts.Region)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kusto endpoint: %w", err)
	}

	if opts.QueryTimeout < 30*time.Second {
		return nil, fmt.Errorf("query timeout must be at least 30 seconds")
	}
	if opts.QueryTimeout > 30*time.Minute {
		return nil, fmt.Errorf("query timeout cannot exceed 30 minutes")
	}

	if opts.TimestampMin.After(opts.TimestampMax) {
		return nil, fmt.Errorf("timestamp-min cannot be after timestamp-max")
	}

	return kustoEndpoint, nil
}

// completeBaseGatherOptions creates the kusto client.
func completeBaseGatherOptions(kustoEndpoint *url.URL, queryTimeout time.Duration, outputPath string) (mustgather.QueryClientInterface, error) {
	client, err := kusto.NewClient(kustoEndpoint, queryTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kusto client: %w", err)
	}
	return mustgather.NewQueryClient(client, queryTimeout, outputPath), nil
}

// createOutputDirectories creates the output directory structure.
func createOutputDirectories(outputPath string, skipHCPDir bool) error {
	if err := os.MkdirAll(path.Join(outputPath, ServicesLogDirectory), 0755); err != nil {
		return fmt.Errorf("failed to create service logs directory: %w", err)
	}
	if err := os.MkdirAll(path.Join(outputPath, InfraLogDirectory), 0755); err != nil {
		return fmt.Errorf("failed to create infrastructure logs directory: %w", err)
	}
	if !skipHCPDir {
		if err := os.MkdirAll(path.Join(outputPath, HostedControlPlaneLogDirectory), 0755); err != nil {
			return fmt.Errorf("failed to create customer logs directory: %w", err)
		}
	}
	return nil
}
