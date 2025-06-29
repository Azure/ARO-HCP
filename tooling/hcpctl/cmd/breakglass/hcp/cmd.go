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

package hcp

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/breakglass"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/cluster"
)

func NewCommand() (*cobra.Command, error) {
	opts := DefaultHCPOptions()
	listOpts := DefaultListHCPOptions()

	cmd := &cobra.Command{
		Use:   "hcp CLUSTER",
		Short: "Generate temporary emergency access to HCP clusters",
		Long: `hcp provides emergency break-glass access to hosted control plane clusters.

This command creates a complete emergency access workflow:
  • Generates RSA client certificates signed by the cluster's sre-break-glass CA
  • Creates a temporary kubeconfig with the cluster's CA and client certificate
  • Establishes secure port forwarding to the cluster's API server
  • Launches an interactive shell with the kubeconfig environment configured

The generated certificates have a configurable expiration (default 24h).
All Kubernetes resources (CSRs, approvals) are removed to ensure no persistent
access artifacts remain.

CLUSTER can be either:
  • A clusters-service cluster ID (e.g., "2jesjug41iavg27inj078ssjidn20clk")
  • An Azure resource ID (e.g., "/subscriptions/.../providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/my-cluster")

Use 'hcpctl breakglass hcp list' to see available clusters.`,
		Args:             cobra.ExactArgs(1),
		SilenceUsage:     true,
		SilenceErrors:    true,
		TraverseChildren: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.ClusterIdentifier = args[0]
			return runBreakglass(cmd.Context(), opts)
		},
		CompletionOptions: cobra.CompletionOptions{
			HiddenDefaultCmd: true,
		},
	}

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List available HCP clusters for emergency access",
		Long: `List all hosted control plane clusters available for break-glass emergency access.

This command displays clusters with their cluster IDs, namespaces, and Azure resource 
identifiers to help identify the target cluster for emergency access operations.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runListClusters(cmd.Context(), listOpts)
		},
	}
	cmd.AddCommand(listCmd)

	if err := BindHCPOptions(opts, cmd); err != nil {
		return nil, err
	}
	if err := BindListHCPOptions(listOpts, listCmd); err != nil {
		return nil, err
	}

	return cmd, nil
}

func runBreakglass(ctx context.Context, opts *RawHCPOptions) error {
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

	// Cluster ID is now mandatory, so no interactive selection needed

	// Convert Options to ExecutionParams
	params := &breakglass.ExecutionParams{
		ClusterID:         completed.ClusterID,
		ClusterName:       completed.ClusterName,
		User:              completed.User,
		Namespace:         completed.Namespace,
		OutputPath:        completed.OutputPath,
		Timeout:           completed.SessionTimeout,
		EnablePortForward: !completed.NoPortForward,
		EnableShell:       !completed.NoShell && !completed.NoPortForward, // Shell is default when port-forwarding is enabled
		RestConfig:        completed.RestConfig,
		Config:            completed.Config,
	}

	if err := breakglass.Execute(ctx, params); err != nil {
		fmt.Fprint(os.Stderr, formatter.FormatError(err))
		return err
	}

	return nil
}

func runListClusters(ctx context.Context, opts *ListHCPOptions) error {
	// Validate options to get dynamic client
	validated, err := opts.Validate(ctx)
	if err != nil {
		return err
	}

	// Create cluster discovery client
	discovery := cluster.NewDiscovery(validated.DynamicClient)

	// List all clusters
	clusters, err := discovery.ListAllClusters(ctx)
	if err != nil {
		return fmt.Errorf("failed to list clusters: %w", err)
	}

	if len(clusters) == 0 {
		fmt.Println("No hosted control planes found")
		return nil
	}

	// Display clusters in a simple list
	fmt.Printf("\nFound %d hosted control plane(s):\n\n", len(clusters))
	for _, cluster := range clusters {
		fmt.Printf("  %s (ID: %s, Namespace: %s)\n", cluster.Name, cluster.ID, cluster.Namespace)
	}

	return nil
}
