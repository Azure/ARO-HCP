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

package base

import (
	"fmt"

	"github.com/spf13/cobra"
)

// BaseOptions represents common options used across multiple commands.
type BaseOptions struct {
	SubscriptionID string
	ResourceGroup  string
	GrafanaName    string
	OutputFormat   string
}

// DefaultBaseOptions returns a new BaseOptions with default values
func DefaultBaseOptions() *BaseOptions {
	return &BaseOptions{
		OutputFormat: "table",
	}
}

// BindBaseOptions binds common command-line flags to the base options
func BindBaseOptions(opts *BaseOptions, cmd *cobra.Command) error {
	flags := cmd.Flags()
	flags.StringVar(&opts.SubscriptionID, "subscription", "", "Azure subscription ID (required)")
	flags.StringVar(&opts.ResourceGroup, "resource-group", "", "Azure resource group name (required)")
	flags.StringVar(&opts.GrafanaName, "grafana-name", "", "Azure Managed Grafana instance name (required)")
	flags.StringVar(&opts.OutputFormat, "output", opts.OutputFormat, "Output format: table or json")

	_ = cmd.MarkFlagRequired("subscription")
	_ = cmd.MarkFlagRequired("resource-group")
	_ = cmd.MarkFlagRequired("grafana-name")

	return nil
}

// ValidateBaseOptions performs validation on the base options
func ValidateBaseOptions(opts *BaseOptions) error {
	// Validate required fields
	if opts.SubscriptionID == "" {
		return fmt.Errorf("subscription ID is required")
	}
	if opts.ResourceGroup == "" {
		return fmt.Errorf("resource group is required")
	}
	if opts.GrafanaName == "" {
		return fmt.Errorf("grafana name is required")
	}

	// Validate output format
	if opts.OutputFormat != "table" && opts.OutputFormat != "json" {
		return fmt.Errorf("output format must be 'table' or 'json', got: %s", opts.OutputFormat)
	}

	return nil
}
