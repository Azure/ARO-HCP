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

package mc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/cluster"
	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/azauth"
)

// ListMCOptions represents options specific to listing MC clusters
type ListMCOptions struct {
	Region string // Filter clusters by Azure region
}

// ValidatedListMCOptions represents validated configuration for list operations
type ValidatedListMCOptions struct {
	Region          string
	AzureCredential azcore.TokenCredential
	Logger          logr.Logger
}

// DefaultListMCOptions returns a new ListMCOptions with default values
func DefaultListMCOptions() *ListMCOptions {
	return &ListMCOptions{}
}

// BindListMCOptions binds command-line flags for list operations
func BindListMCOptions(opts *ListMCOptions, cmd *cobra.Command) error {
	flags := cmd.Flags()
	flags.StringVar(&opts.Region, "region", "", "Filter clusters by Azure region (e.g., eastus, westus2)")
	return nil
}

// Validate performs validation on the list options
func (o *ListMCOptions) Validate(ctx context.Context) (*ValidatedListMCOptions, error) {
	// Get Azure credentials
	cred, err := azauth.GetAzureTokenCredentials()
	if err != nil {
		return nil, fmt.Errorf("failed to obtain Azure credentials: %w", err)
	}

	// Create logger
	logger := logr.Discard()

	return &ValidatedListMCOptions{
		Region:          o.Region,
		AzureCredential: cred,
		Logger:          logger,
	}, nil
}

// RawMCOptions represents the initial, unvalidated configuration for MC breakglass operations.
type RawMCOptions struct {
	ClusterName string // Target AKS cluster name
	OutputPath  string // Path to write generated kubeconfig
	NoShell     bool   // Disable shell mode
}

// ValidatedMCOptions represents validated configuration with initialized clients
type ValidatedMCOptions struct {
	ClusterName     string
	OutputPath      string
	NoShell         bool
	AzureCredential azcore.TokenCredential
	Logger          logr.Logger
}

// CompletedMCOptions represents fully initialized options ready for execution
type CompletedMCOptions struct {
	*ValidatedMCOptions
	ResourceGroup  string // Discovered resource group
	SubscriptionID string // Discovered subscription ID
}

// DefaultMCOptions returns a new RawMCOptions with default values
func DefaultMCOptions() *RawMCOptions {
	return &RawMCOptions{
		NoShell: false,
	}
}

// BindMCOptions binds command-line flags to the options
func BindMCOptions(opts *RawMCOptions, cmd *cobra.Command) error {
	// Add MC-specific flags
	flags := cmd.Flags()
	flags.StringVar(&opts.OutputPath, "output", "", "Path to write the generated kubeconfig file")
	flags.BoolVar(&opts.NoShell, "no-shell", false, "Disable interactive shell mode after kubeconfig generation")

	return nil
}

// Validate performs validation on the raw options
func (o *RawMCOptions) Validate(ctx context.Context) (*ValidatedMCOptions, error) {
	// Note: MC commands don't require Kubernetes access, so we skip base options validation
	// Only Azure credentials are needed for AKS cluster discovery

	// Validate output path if provided
	if o.OutputPath != "" {
		dir := filepath.Dir(o.OutputPath)
		if dir != "." && dir != "/" {
			if _, err := os.Stat(dir); os.IsNotExist(err) {
				return nil, fmt.Errorf("output directory does not exist: %s", dir)
			}
		}
	}

	// Get Azure credentials
	cred, err := azauth.GetAzureTokenCredentials()
	if err != nil {
		return nil, fmt.Errorf("failed to obtain Azure credentials: %w", err)
	}

	// Create logger
	logger := logr.Discard()

	return &ValidatedMCOptions{
		ClusterName:     o.ClusterName,
		OutputPath:      o.OutputPath,
		NoShell:         o.NoShell,
		AzureCredential: cred,
		Logger:          logger,
	}, nil
}

// Complete performs discovery and initialization
func (o *ValidatedMCOptions) Complete(ctx context.Context) (*CompletedMCOptions, error) {
	completed := &CompletedMCOptions{
		ValidatedMCOptions: o,
	}

	// If cluster name is provided, try to discover its details
	if o.ClusterName != "" {
		discovery := cluster.NewManagementClusterDiscovery(o.AzureCredential)
		filter := &cluster.ClusterTypeFilter{
			TagKey:   "clusterType",
			TagValue: "mgmt-cluster",
		}
		clusters, err := discovery.DiscoverClusters(ctx, filter)
		if err != nil {
			return nil, fmt.Errorf("failed to discover management clusters: %w", err)
		}

		// Find the matching cluster
		var found bool
		for _, cluster := range clusters {
			if cluster.Name == o.ClusterName {
				completed.ResourceGroup = cluster.ResourceGroup
				completed.SubscriptionID = cluster.SubscriptionID
				found = true
				break
			}
		}

		if !found {
			return nil, fmt.Errorf("management cluster '%s' not found", o.ClusterName)
		}
	}

	return completed, nil
}
