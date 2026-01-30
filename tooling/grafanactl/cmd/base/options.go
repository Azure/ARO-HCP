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
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// BaseOptions represents common options used across multiple commands.
type BaseOptions struct {
	SubscriptionID string
	ResourceGroup  string
	GrafanaName    string
	OutputFormat   string
	DryRun         bool
}

// DefaultBaseOptions returns a new BaseOptions with default values
func DefaultBaseOptions() *BaseOptions {
	return &BaseOptions{
		OutputFormat: "table",
	}
}

// BindBaseOptions binds common command-line flags to the base options
func BindBaseOptions(opts *BaseOptions, cmd *cobra.Command) error {
	// Set defaults from environment variables if available
	if envSub := os.Getenv("GRAFANA_SUBSCRIPTION"); envSub != "" {
		opts.SubscriptionID = envSub
	}
	if envRG := os.Getenv("GRAFANA_RESOURCE_GROUP"); envRG != "" {
		opts.ResourceGroup = envRG
	}
	if envGrafana := os.Getenv("GRAFANA_NAME"); envGrafana != "" {
		opts.GrafanaName = envGrafana
	}
	if envResourceId := os.Getenv("GRAFANA_RESOURCE_ID"); envResourceId != "" {
		resourceID := strings.Split(envResourceId, "/")
		opts.SubscriptionID = resourceID[2]
		opts.ResourceGroup = resourceID[4]
		opts.GrafanaName = resourceID[8]
	}
	if envOutput := os.Getenv("GRAFANA_OUTPUT"); envOutput != "" {
		opts.OutputFormat = envOutput
	}
	if envDryRun := os.Getenv("DRY_RUN"); envDryRun != "" {
		if dryRun, err := strconv.ParseBool(envDryRun); err == nil {
			opts.DryRun = dryRun
		}
	}

	flags := cmd.Flags()
	flags.StringVar(&opts.SubscriptionID, "subscription", opts.SubscriptionID, "Azure subscription ID (required) [env: GRAFANACTL_SUBSCRIPTION]")
	flags.StringVar(&opts.ResourceGroup, "resource-group", opts.ResourceGroup, "Azure resource group name (required) [env: GRAFANACTL_RESOURCE_GROUP]")
	flags.StringVar(&opts.GrafanaName, "grafana-name", opts.GrafanaName, "Azure Managed Grafana instance name (required) [env: GRAFANACTL_GRAFANA_NAME]")
	flags.StringVar(&opts.OutputFormat, "output", opts.OutputFormat, "Output format: table or json [env: GRAFANACTL_OUTPUT]")

	// Mark flags as required only if not set via environment variables
	if opts.SubscriptionID == "" {
		_ = cmd.MarkFlagRequired("subscription")
	}
	if opts.ResourceGroup == "" {
		_ = cmd.MarkFlagRequired("resource-group")
	}
	if opts.GrafanaName == "" {
		_ = cmd.MarkFlagRequired("grafana-name")
	}

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
