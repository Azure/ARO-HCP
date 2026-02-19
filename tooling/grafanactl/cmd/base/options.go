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
	"strings"

	"github.com/spf13/cobra"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

// BaseOptions represents common options used across multiple commands.
type BaseOptions struct {
	SubscriptionID    string
	ResourceGroup     string
	GrafanaName       string
	GrafanaResourceID string
	OutputFormat      string
	DryRun            bool
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
	flags.StringVar(&opts.SubscriptionID, "subscription", opts.SubscriptionID, "Azure subscription ID ")
	flags.StringVar(&opts.ResourceGroup, "resource-group", opts.ResourceGroup, "Azure resource group name ")
	flags.StringVar(&opts.GrafanaName, "grafana-name", opts.GrafanaName, "Azure Managed Grafana instance name ")
	flags.StringVar(&opts.GrafanaResourceID, "grafana-resource-id", opts.GrafanaResourceID, "Azure Managed Grafana instance resource ID")
	flags.StringVar(&opts.OutputFormat, "output", opts.OutputFormat, "Output format: table or json")

	return nil
}

// ValidateBaseOptions performs validation on the base options
func ValidateBaseOptions(opts *BaseOptions) error {
	// Validate required fields

	if opts.GrafanaResourceID == "" {
		if opts.SubscriptionID == "" || opts.ResourceGroup == "" || opts.GrafanaName == "" {
			return fmt.Errorf("subscription ID, resource group, and grafana name are required if grafana resource ID is not provided")
		}
	} else {
		resourceID, err := ValidateAzureResourceID(opts.GrafanaResourceID, "Microsoft.Dashboard/grafana")
		if err != nil {
			return fmt.Errorf("failed to validate grafana resource ID: %w", err)
		}
		opts.SubscriptionID = resourceID.SubscriptionID
		opts.ResourceGroup = resourceID.ResourceGroupName
		opts.GrafanaName = resourceID.Name
	}

	// Validate output format
	if opts.OutputFormat != "table" && opts.OutputFormat != "json" {
		return fmt.Errorf("output format must be 'table' or 'json', got: %s", opts.OutputFormat)
	}

	return nil
}

// ValidateAzureResourceID validates an Azure resource ID and ensures it's an Azure Managed Grafana resource
func ValidateAzureResourceID(resourceID string, expectedFullType string) (*azcorearm.ResourceID, error) {
	if resourceID == "" {
		return nil, fmt.Errorf("resourceID cannot be empty")
	}

	parsedID, err := azcorearm.ParseResourceID(resourceID)
	if err != nil {
		return nil, fmt.Errorf("invalid Azure resource ID format: %w", err)
	}

	if !strings.EqualFold(parsedID.ResourceType.String(), expectedFullType) {
		return nil, fmt.Errorf("invalid Azure resource type: expected '%s', got '%s'", expectedFullType, parsedID.ResourceType.String())
	}

	if parsedID.Name == "" {
		return nil, fmt.Errorf("resource name cannot be empty in resource ID")
	}

	return parsedID, nil
}
