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
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-Tools/tools/cmdutils"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/aks"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/utils"
)

// RawListAKSOptions represents the initial, unvalidated configuration for AKS list operations.
type RawListAKSOptions struct {
	Region string // Filter clusters by Azure region
	Output string // Output format (table, yaml, json)
}

// validatedListAKSOptions is a private struct that enforces the options validation pattern.
type validatedListAKSOptions struct {
	*RawListAKSOptions
	AzureCredential azcore.TokenCredential
	OutputFormat    aks.OutputFormat
}

// ValidatedListAKSOptions represents AKS list configuration that has passed validation
type ValidatedListAKSOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package
	*validatedListAKSOptions
}

// DefaultListAKSOptions returns a new RawListAKSOptions with default values
func DefaultListAKSOptions() *RawListAKSOptions {
	return &RawListAKSOptions{}
}

// BindListAKSOptions binds command-line flags for list operations
func BindListAKSOptions(opts *RawListAKSOptions, cmd *cobra.Command) error {
	flags := cmd.Flags()
	flags.StringVar(&opts.Region, "region", "", "Filter clusters by Azure region (e.g., eastus, westus2)")
	flags.StringVarP(&opts.Output, "output", "o", "table", "Output format: table, yaml, json")
	return nil
}

// Validate performs validation on the list options
func (o *RawListAKSOptions) Validate(ctx context.Context) (*ValidatedListAKSOptions, error) {
	// Validate output format
	outputFormat, err := aks.ValidateOutputFormat(o.Output)
	if err != nil {
		return nil, fmt.Errorf("invalid output format '%s': %w", o.Output, err)
	}

	// Get Azure credentials
	cred, err := cmdutils.GetAzureTokenCredentials()
	if err != nil {
		return nil, fmt.Errorf("failed to obtain Azure credentials: %w", err)
	}

	return &ValidatedListAKSOptions{
		validatedListAKSOptions: &validatedListAKSOptions{
			RawListAKSOptions: o,
			AzureCredential:   cred,
			OutputFormat:      outputFormat,
		},
	}, nil
}

// CompleteListAKSOptions represents the final, fully validated and initialized configuration
// for AKS list operations.
type CompleteListAKSOptions struct {
	*validatedListAKSOptions
	Discovery     *aks.AKSDiscovery
	ClusterFilter *aks.AKSFilter
}

// CompleteWithFilter performs discovery client initialization to create fully usable list options.
func (o *ValidatedListAKSOptions) CompleteWithFilter(ctx context.Context, filter *aks.AKSFilter) (*CompleteListAKSOptions, error) {
	// Initialize AKS discovery client
	discovery := aks.NewAKSDiscovery(o.AzureCredential)

	return &CompleteListAKSOptions{
		validatedListAKSOptions: o.validatedListAKSOptions,
		Discovery:               discovery,
		ClusterFilter:           filter,
	}, nil
}

// RawBreakglassAKSOptions represents the initial, unvalidated configuration for AKS breakglass operations.
type RawBreakglassAKSOptions struct {
	ClusterName string // Target AKS cluster name
	OutputPath  string // Path to write generated kubeconfig
	NoShell     bool   // Disable shell mode
	ExecCommand string // Command to execute directly instead of spawning interactive shell
}

// ValidatedBreakglassAKSOptions represents validated configuration with initialized clients
type ValidatedBreakglassAKSOptions struct {
	ClusterName     string
	OutputPath      string
	NoShell         bool
	ExecCommand     string
	AzureCredential azcore.TokenCredential
}

// CompletedBreakglassAKSOptions represents fully initialized options ready for execution
type CompletedBreakglassAKSOptions struct {
	*ValidatedBreakglassAKSOptions
	ResourceGroup  string // Discovered resource group
	SubscriptionID string // Discovered subscription ID
}

// DefaultBreakglassAKSOptions returns a new RawBreakglassAKSOptions with default values
func DefaultBreakglassAKSOptions() *RawBreakglassAKSOptions {
	return &RawBreakglassAKSOptions{
		NoShell: false,
	}
}

// BindBreakglassAKSOptions binds command-line flags to the options
func BindBreakglassAKSOptions(opts *RawBreakglassAKSOptions, cmd *cobra.Command) error {
	// Add AKS-specific flags
	flags := cmd.Flags()
	flags.StringVar(&opts.OutputPath, "output", "", "Path to write the generated kubeconfig file")
	flags.BoolVar(&opts.NoShell, "no-shell", false, "Disable interactive shell mode after kubeconfig generation")
	flags.StringVar(&opts.ExecCommand, "exec", "", "Execute command directly instead of spawning interactive shell")

	return nil
}

// Validate performs validation on the raw options
func (o *RawBreakglassAKSOptions) Validate(ctx context.Context) (*ValidatedBreakglassAKSOptions, error) {
	// Validate that cluster name is provided
	if o.ClusterName == "" {
		return nil, fmt.Errorf("cluster name is required for breakglass operations")
	}

	// Provide an output path if none is given
	outputPath := o.OutputPath
	var err error
	if outputPath == "" {
		outputPath, err = utils.GetTempFilename("aks-kubeconfig-*.yaml")
		if err != nil {
			return nil, fmt.Errorf("failed to get temporary filename for the kubeconfig: %w", err)
		}
	}

	// Get Azure credentials
	cred, err := cmdutils.GetAzureTokenCredentials()
	if err != nil {
		return nil, fmt.Errorf("failed to obtain Azure credentials: %w", err)
	}

	return &ValidatedBreakglassAKSOptions{
		ClusterName:     o.ClusterName,
		OutputPath:      outputPath,
		NoShell:         o.NoShell,
		ExecCommand:     o.ExecCommand,
		AzureCredential: cred,
	}, nil
}

// CompleteWithFilter performs discovery and initialization
func (o *ValidatedBreakglassAKSOptions) Complete(ctx context.Context, clusterType string) (*CompletedBreakglassAKSOptions, error) {
	completed := &CompletedBreakglassAKSOptions{
		ValidatedBreakglassAKSOptions: o,
	}

	// Discover cluster details using the validated cluster name
	discovery := aks.NewAKSDiscovery(o.AzureCredential)
	filter := aks.NewAKSFilter(clusterType, "", o.ClusterName)
	cluster, err := discovery.FindSingleCluster(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("failed to discover AKS cluster '%s': %w", o.ClusterName, err)
	}

	completed.ResourceGroup = cluster.ResourceGroup
	completed.SubscriptionID = cluster.SubscriptionID

	return completed, nil
}
