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

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/breakglass"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/common"
	cluster "github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/hcp"
)

func NewCommand(group string) (*cobra.Command, error) {
	cmd := &cobra.Command{
		Use:     "hcp",
		Aliases: []string{"hc", "h"},
		Short:   "Hosted Control Plane (HCP) operations",
		GroupID: group,
		Long: `hcp provides emergency access operations for hosted control planes.

This command group includes subcommands for emergency break-glass access
and investigation of hosted control plane clusters.`,
		Example: `  hcpctl hcp list
  hcpctl hcp breakglass <cluster-id>`,
		CompletionOptions: cobra.CompletionOptions{
			HiddenDefaultCmd: true,
		},
	}

	// Add breakglass subcommand
	breakglassCmd, err := newBreakglassCommand()
	if err != nil {
		return nil, err
	}
	cmd.AddCommand(breakglassCmd)

	// Add list subcommand
	listCmd, err := newListCommand()
	if err != nil {
		return nil, err
	}
	cmd.AddCommand(listCmd)

	return cmd, nil
}

func newBreakglassCommand() (*cobra.Command, error) {
	opts := DefaultBreakglassHCPOptions()

	cmd := &cobra.Command{
		Use:   "breakglass CLUSTER",
		Short: "Get emergency access to HCP clusters",
		Long: `Get emergency break-glass access to hosted control plane clusters.

This command provides temporary emergency access to HCP clusters.

CLUSTER can be either a clusters-service cluster ID or an Azure resource ID.
Use 'hcpctl hcp list' to see available clusters.`,
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

	if err := BindBreakglassHCPOptions(opts, cmd); err != nil {
		return nil, err
	}

	return cmd, nil
}

func newListCommand() (*cobra.Command, error) {
	listOpts := DefaultListHCPOptions()

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List available HCP clusters",
		Long: `List all hosted control plane clusters available on the current management cluster.

This command displays clusters with their identifiers and resource information
to help identify target clusters for emergency access operations.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runListClusters(cmd.Context(), listOpts)
		},
	}

	if err := BindListHCPOptions(listOpts, cmd); err != nil {
		return nil, err
	}

	return cmd, nil
}

func runBreakglass(ctx context.Context, opts *RawBreakglassHCPOptions) error {
	validated, err := opts.Validate(ctx)
	if err != nil {
		return err
	}

	completed, err := validated.Complete(ctx)
	if err != nil {
		return err
	}

	// Convert Options to ExecutionParams
	params := &breakglass.ExecutionParams{
		ClusterID:         completed.ClusterID,
		ClusterName:       completed.ClusterName,
		User:              completed.User,
		Namespace:         completed.Namespace,
		Privileged:        completed.Privileged,
		OutputPath:        completed.OutputPath,
		Timeout:           completed.SessionTimeout,
		EnablePortForward: !completed.NoPortForward,
		EnableShell:       !completed.NoShell && !completed.NoPortForward && completed.ExecCommand == "",
		ExecCommand:       completed.ExecCommand,
		RestConfig:        completed.RestConfig,
	}

	if err := breakglass.Execute(ctx, params); err != nil {
		return fmt.Errorf("failed to execute breakglass operation: %w", err)
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
	discovery := cluster.NewDiscovery(validated.CtrlClient)

	// List all clusters
	clusters, err := discovery.ListAllClusters(ctx)
	if err != nil {
		return fmt.Errorf("failed to list clusters: %w", err)
	}

	// Display clusters using generic formatter
	formatter := getHCPFormatter()
	return formatter.Display(clusters, validated.OutputFormat)
}

// getHCPFormatter returns a formatter configured for HCP clusters
func getHCPFormatter() *common.Formatter[cluster.HCPInfo] {
	tableOptions := common.TableOptions[cluster.HCPInfo]{
		Title:        "Found %d hosted control plane(s)",
		EmptyMessage: "No hosted control planes found",
		Columns: []common.TableColumn[cluster.HCPInfo]{
			{Header: "CLUSTER NAME", Field: func(item cluster.HCPInfo) string { return item.Name }},
			{Header: "CLUSTER ID", Field: func(item cluster.HCPInfo) string { return item.ID }},
			{Header: "SUBSCRIPTION ID", Field: func(item cluster.HCPInfo) string { return item.SubscriptionID }},
		},
	}
	return common.NewFormatter("HostedControlPlanes", tableOptions)
}
