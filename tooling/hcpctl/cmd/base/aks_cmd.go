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
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/aks"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/shell"
)

// ClusterConfig defines the configuration for a cluster command type
type ClusterConfig struct {
	CommandName         string
	Aliases             []string
	DisplayName         string
	ShortName           string
	CompleteBreakglass  func(context.Context, *ValidatedBreakglassAKSOptions) (*CompletedBreakglassAKSOptions, error)
	CompleteList        func(context.Context, *ValidatedListAKSOptions) (*CompleteListAKSOptions, error)
	BreakglassUsageHelp string
	ShellMessage        string
}

// NewClusterCommand creates a new cluster command with the given configuration
func NewClusterCommand(config ClusterConfig, group string) (*cobra.Command, error) {
	cmd := &cobra.Command{
		Use:     config.CommandName,
		Aliases: config.Aliases,
		Short:   fmt.Sprintf("%s (%s) operations", config.DisplayName, config.ShortName),
		GroupID: group,
		Long: fmt.Sprintf(`%s provides %s operations for ARO-HCP.

This command group includes subcommands for %s access,
investigation, and operational tasks.`, config.CommandName, strings.ToLower(config.DisplayName), strings.ToLower(config.DisplayName)),
		Example: fmt.Sprintf(`  hcpctl %s list
  hcpctl %s breakglass int-usw3-%s-1`, config.CommandName, config.CommandName, strings.ToLower(config.ShortName)),
		CompletionOptions: cobra.CompletionOptions{
			HiddenDefaultCmd: true,
		},
	}

	// add breakglass subcommand
	breakglassCmd, err := newBreakglassCommand(config)
	if err != nil {
		return nil, err
	}
	cmd.AddCommand(breakglassCmd)

	// add list subcommand
	listCmd, err := newListCommand(config)
	if err != nil {
		return nil, err
	}
	cmd.AddCommand(listCmd)

	// add dump crs subcommand
	dumpCrsCmd, err := newDumpCrsCommand(config)
	if err != nil {
		return nil, err
	}
	cmd.AddCommand(dumpCrsCmd)
	return cmd, nil
}

func newBreakglassCommand(config ClusterConfig) (*cobra.Command, error) {
	opts := DefaultBreakglassAKSOptions()

	cmd := &cobra.Command{
		Use:     "breakglass AKS_NAME",
		Aliases: []string{"br"},
		Short:   fmt.Sprintf("Get access to a %s", config.DisplayName),
		Long: fmt.Sprintf(`Get access to an AKS %s for operational tasks.

%s

AKS_NAME is the name of the AKS %s to access.
Use 'hcpctl %s list' to see available clusters.

Note: Requires appropriate JIT permissions to access the target cluster.`,
			strings.ToLower(config.DisplayName),
			config.BreakglassUsageHelp,
			strings.ToLower(config.DisplayName),
			config.CommandName),
		Args:             cobra.ExactArgs(1),
		SilenceUsage:     true,
		SilenceErrors:    true,
		TraverseChildren: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.ClusterName = args[0]
			return runBreakglass(cmd.Context(), opts, config)
		},
		CompletionOptions: cobra.CompletionOptions{
			HiddenDefaultCmd: true,
		},
	}

	// bind additional flags to main command
	if err := BindBreakglassAKSOptions(opts, cmd); err != nil {
		return nil, err
	}

	return cmd, nil
}

func newListCommand(config ClusterConfig) (*cobra.Command, error) {
	listOpts := DefaultListAKSOptions()

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   fmt.Sprintf("List available %ss", config.DisplayName),
		Long: fmt.Sprintf(`List all AKS %ss available for operational access.

This command searches across all Azure subscriptions for the AKS clusters that
fulfill the role of a %s.`, strings.ToLower(config.DisplayName), strings.ToLower(config.DisplayName)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runListClusters(cmd.Context(), listOpts, config)
		},
	}

	if err := BindListAKSOptions(listOpts, cmd); err != nil {
		return nil, err
	}

	return cmd, nil
}

func runBreakglass(ctx context.Context, opts *RawBreakglassAKSOptions, config ClusterConfig) error {
	validated, err := opts.Validate(ctx)
	if err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	completed, err := config.CompleteBreakglass(ctx, validated)
	if err != nil {
		return err
	}

	// Get kubeconfig from Azure
	err = aks.GetAKSKubeconfig(ctx, completed.SubscriptionID, completed.ResourceGroup, completed.ClusterName, completed.AzureCredential, completed.OutputPath)
	if err != nil {
		return fmt.Errorf("failed to get AKS kubeconfig: %w", err)
	}

	// Handle exec command, shell, or no-shell mode
	if completed.ExecCommand != "" {
		// Execute command directly with the kubeconfig environment
		return shell.ExecCommandString(ctx, &shell.Config{
			KubeconfigPath: completed.OutputPath,
			ClusterName:    completed.ClusterName,
			PromptInfo:     fmt.Sprintf("[%s: %s]", config.ShortName, completed.ClusterName),
			Privileged:     false,
		}, completed.ExecCommand, make(chan struct{}), &sync.Once{})
	} else if !completed.NoShell {
		// spawn shell with KUBECONFIG environment
		fmt.Printf("\nStarting shell for %s: %s\n", strings.ToLower(config.DisplayName), completed.ClusterName)
		fmt.Println(config.ShellMessage)
		fmt.Println()

		return shell.Spawn(ctx, &shell.Config{
			KubeconfigPath: completed.OutputPath,
			ClusterName:    completed.ClusterName,
			PromptInfo:     fmt.Sprintf("[%s: %s]", config.ShortName, completed.ClusterName),
			Privileged:     false,
		})
	}

	// If --no-shell is set, just return success after kubeconfig generation
	return nil
}

func runListClusters(ctx context.Context, opts *RawListAKSOptions, config ClusterConfig) error {
	// validate options
	validated, err := opts.Validate(ctx)
	if err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	// complete initialization
	completed, err := config.CompleteList(ctx, validated)
	if err != nil {
		return err
	}

	// discover all clusters using completed options
	clusters, err := completed.Discovery.DiscoverClusters(ctx, completed.ClusterFilter)
	if err != nil {
		return fmt.Errorf("failed to discover %ss: %w", strings.ToLower(config.DisplayName), err)
	}

	// output clusters
	err = aks.DisplayAKSClusters(clusters, completed.OutputFormat)
	if err != nil {
		return fmt.Errorf("failed to display clusters: %w", err)
	}

	return nil
}
