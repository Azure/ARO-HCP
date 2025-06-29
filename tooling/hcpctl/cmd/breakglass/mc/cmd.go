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
	"strings"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/cluster"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/shell"
)

// NewCommand creates the mc subcommand for breakglass
func NewCommand() (*cobra.Command, error) {
	opts := DefaultMCOptions()
	listOpts := DefaultListMCOptions()

	cmd := &cobra.Command{
		Use:   "mc AKS_NAME",
		Short: "Build a kubeconfig for Management Cluster (MC) access",
		Long: `mc builds a kubeconfig for convenient access to AKS management clusters.

This command creates a convenient kubeconfig workflow for management cluster access:
  • Searches for AKS clusters across all subscriptions
  • Retrieves kubeconfig using Azure SDK
  • Converts kubeconfig using kubelogin for Azure CLI authentication
  • Launches an interactive shell with the kubeconfig environment configured

AKS_NAME is the name of the AKS management cluster to access.
Use 'hcpctl breakglass mc list' to see available clusters.

Note: This requires having appropriate JIT permissions to access the target cluster.

This command is designed to work seamlessly with HCP breakglass, allowing you to:
  1. First access an MC using this command
  2. Then use HCP breakglass to access hosted control planes within that MC`,
		Args:             cobra.ExactArgs(1),
		SilenceUsage:     true,
		SilenceErrors:    true,
		TraverseChildren: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.ClusterName = args[0]
			return runBreakglass(cmd.Context(), opts)
		},
		CompletionOptions: cobra.CompletionOptions{
			HiddenDefaultCmd: true,
		},
	}

	// Add list subcommand
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List available Management Clusters for access",
		Long: `List all AKS management clusters available for access.

This command searches across all Azure subscriptions for AKS clusters and displays them in a formatted table.

Use the --region flag to filter clusters by Azure region (e.g., eastus, westus2).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runListClusters(cmd.Context(), listOpts)
		},
	}
	cmd.AddCommand(listCmd)

	// Bind additional flags to both main command and list subcommand
	if err := BindMCOptions(opts, cmd); err != nil {
		return nil, err
	}
	if err := BindListMCOptions(listOpts, listCmd); err != nil {
		return nil, err
	}

	return cmd, nil
}

func runBreakglass(ctx context.Context, opts *RawMCOptions) error {
	formatter := &ErrorFormatter{}

	validated, err := opts.Validate(ctx)
	if err != nil {
		fmt.Fprint(os.Stderr, formatter.FormatError(err))
		return err
	}

	completed, err := validated.Complete(ctx)
	if err != nil {
		fmt.Fprint(os.Stderr, formatter.FormatError(err))
		return err
	}

	// AKS name is now mandatory, so no interactive selection needed

	// Get kubeconfig from Azure
	kubeconfigPath, err := GetAKSKubeconfig(ctx, completed)
	if err != nil {
		fmt.Fprint(os.Stderr, formatter.FormatError(err))
		return err
	}

	// If output path is specified, copy kubeconfig there
	if completed.OutputPath != "" {
		if err := copyKubeconfig(kubeconfigPath, completed.OutputPath); err != nil {
			fmt.Fprint(os.Stderr, formatter.FormatError(err))
			return err
		}
		fmt.Printf("Kubeconfig written to: %s\n", completed.OutputPath)

		if completed.NoShell {
			return nil
		}
	}

	// Spawn shell with KUBECONFIG environment
	fmt.Printf("\nStarting shell for management cluster: %s\n", completed.ClusterName)
	fmt.Println("You can now use kubectl commands, or run 'hcpctl breakglass hcp' to access hosted control planes.")
	fmt.Println()

	return shell.Spawn(ctx, &shell.Config{
		KubeconfigPath: kubeconfigPath,
		ClusterName:    completed.ClusterName,
		PromptInfo:     fmt.Sprintf("[MC: %s]", completed.ClusterName),
	})
}

func runListClusters(ctx context.Context, opts *ListMCOptions) error {
	// Validate options
	validated, err := opts.Validate(ctx)
	if err != nil {
		return err
	}

	// Discover all management clusters
	discovery := cluster.NewManagementClusterDiscovery(validated.AzureCredential)
	filter := &cluster.ClusterTypeFilter{
		TagKey:   "clusterType",
		TagValue: "mgmt-cluster",
	}
	clusters, err := discovery.DiscoverClusters(ctx, filter)
	if err != nil {
		return fmt.Errorf("failed to discover management clusters: %w", err)
	}

	// Apply region filtering if specified
	if validated.Region != "" {
		filteredClusters := make([]cluster.AKSCluster, 0)
		regionFilter := strings.ToLower(validated.Region)

		for _, cluster := range clusters {
			if strings.ToLower(cluster.Location) == regionFilter {
				filteredClusters = append(filteredClusters, cluster)
			}
		}
		clusters = filteredClusters
	}

	if len(clusters) == 0 {
		if validated.Region != "" {
			fmt.Printf("No management clusters found in region '%s'\n", validated.Region)
		} else {
			fmt.Println("No management clusters found")
		}
		return nil
	}

	// Display clusters in a table
	fmt.Printf("\nFound %d management cluster(s):\n\n", len(clusters))
	DisplayClustersTable(clusters)

	return nil
}
