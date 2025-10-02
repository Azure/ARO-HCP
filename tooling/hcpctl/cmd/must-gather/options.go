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
	"time"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/cmd/base"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/common"
)

// RawMustGatherOptions represents the initial, unvalidated configuration for must-gather operations.
type RawMustGatherOptions struct {
	BaseOptions  *base.RawBaseOptions
	KustoName    string        // Azure Data Explorer cluster name
	OutputPath   string        // Path to write the output file
	QueryTimeout time.Duration // Timeout for query execution
	OutputFormat string        // Output format (json, csv, table)
}

// DefaultMustGatherOptions returns a new RawMustGatherOptions struct initialized with sensible defaults.
func DefaultMustGatherOptions() *RawMustGatherOptions {
	return &RawMustGatherOptions{
		BaseOptions:  base.DefaultBaseOptions(),
		QueryTimeout: 5 * time.Minute,
		OutputFormat: "json",
	}
}

// BindMustGatherOptions configures cobra command flags for must-gather specific options.
func BindMustGatherOptions(opts *RawMustGatherOptions, cmd *cobra.Command) error {
	// Bind base options first
	if err := base.BindBaseOptions(opts.BaseOptions, cmd); err != nil {
		return fmt.Errorf("failed to bind base options: %w", err)
	}

	// Add must-gather specific flags
	cmd.Flags().StringVar(&opts.KustoName, "kusto-name", opts.KustoName, "Azure Data Explorer cluster name (required)")
	cmd.Flags().StringVarP(&opts.OutputPath, "output", "o", opts.OutputPath, "path to write the output file")
	cmd.Flags().DurationVar(&opts.QueryTimeout, "query-timeout", opts.QueryTimeout, "timeout for query execution")
	cmd.Flags().StringVar(&opts.OutputFormat, "format", opts.OutputFormat, "output format: json, csv, table")

	// Mark required flags
	if err := cmd.MarkFlagRequired("kusto-name"); err != nil {
		return fmt.Errorf("failed to mark kusto-name as required: %w", err)
	}

	return nil
}

// ValidatedMustGatherOptions represents must-gather configuration that has passed validation.
type ValidatedMustGatherOptions struct {
	*RawMustGatherOptions
	OutputFormat common.OutputFormat
}

// Validate performs comprehensive validation of all must-gather input parameters.
func (o *RawMustGatherOptions) Validate(ctx context.Context) (*ValidatedMustGatherOptions, error) {
	// Validate base options first
	if err := base.ValidateBaseOptions(o.BaseOptions); err != nil {
		return nil, err
	}

	// Validate kusto name
	if o.KustoName == "" {
		return nil, fmt.Errorf("kusto-name is required")
	}

	// Validate output format
	outputFormat, err := common.ValidateOutputFormat(o.OutputFormat)
	if err != nil {
		return nil, fmt.Errorf("invalid output format '%s': %w", o.OutputFormat, err)
	}

	// Validate query timeout
	if o.QueryTimeout < 30*time.Second {
		return nil, fmt.Errorf("query timeout must be at least 30 seconds")
	}

	if o.QueryTimeout > 30*time.Minute {
		return nil, fmt.Errorf("query timeout cannot exceed 30 minutes")
	}

	return &ValidatedMustGatherOptions{
		RawMustGatherOptions: o,
		OutputFormat:         outputFormat,
	}, nil
}

// Complete performs final initialization to create fully usable MustGatherOptions.
func (o *ValidatedMustGatherOptions) Complete(ctx context.Context) (*MustGatherOptions, error) {
	// Set default output path if not specified
	if o.OutputPath == "" {
		o.OutputPath = fmt.Sprintf("must-gather-%s-%s.%s",
			o.KustoName,
			time.Now().Format("20060102-150405"),
			o.OutputFormat)
	}

	return &MustGatherOptions{
		ValidatedMustGatherOptions: o,
	}, nil
}

// MustGatherOptions represents the final, fully validated and initialized configuration for must-gather operations.
type MustGatherOptions struct {
	*ValidatedMustGatherOptions
}
