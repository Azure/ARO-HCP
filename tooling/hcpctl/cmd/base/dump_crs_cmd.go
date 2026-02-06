// Copyright 2026 Microsoft Corporation
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
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/aks"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/crdump"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/utils/kubeclient"
)

type DumpCRsCmdOptions struct {
	RawBreakglassAKSOptions
	HostedClusterNamespace string
	OutputPath             string
}

type ValidatedDumpCRsCmdOptions struct {
	*ValidatedBreakglassAKSOptions
	HostedClusterNamespace string
	OutputPath             string
}

func (o *DumpCRsCmdOptions) Validate(ctx context.Context) (*ValidatedDumpCRsCmdOptions, error) {
	if o.HostedClusterNamespace == "" {
		return nil, fmt.Errorf("hosted-cluster-namespace is required")
	}

	if o.OutputPath == "" {
		return nil, fmt.Errorf("output-path is required")
	}

	info, err := os.Stat(o.OutputPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if err := os.MkdirAll(o.OutputPath, 0755); err != nil {
				return nil, fmt.Errorf("failed to create output-path '%s': %w", o.OutputPath, err)
			}
		} else {
			return nil, err
		}
	} else {
		if !info.IsDir() {
			return nil, fmt.Errorf("output-path %s is not a directory", o.OutputPath)
		}
	}

	validated, err := o.RawBreakglassAKSOptions.Validate(ctx)
	if err != nil {
		return nil, err
	}

	return &ValidatedDumpCRsCmdOptions{
		ValidatedBreakglassAKSOptions: validated,
		HostedClusterNamespace:        o.HostedClusterNamespace,
		OutputPath:                    o.OutputPath,
	}, nil
}

func newDumpCrsCommand(config ClusterConfig) (*cobra.Command, error) {
	dumpOpts := DumpCRsCmdOptions{}

	cmd := &cobra.Command{
		Use:     "dump-crs AKS_NAME",
		Aliases: []string{"dc"},
		Short:   fmt.Sprintf("Dump Custom Resources from a %s", config.DisplayName),
		Long: fmt.Sprintf(`Dump Custom Resources (CRs) from an AKS %s for analysis.

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
			dumpOpts.ClusterName = args[0]
			return runDumpCrs(cmd.Context(), dumpOpts, config)
		},
		CompletionOptions: cobra.CompletionOptions{
			HiddenDefaultCmd: true,
		},
	}
	cmd.Flags().StringVar(&dumpOpts.HostedClusterNamespace, "hosted-cluster-namespace", "", "Namespace of the hosted cluster to filter CRs")
	if err := cmd.MarkFlagRequired("hosted-cluster-namespace"); err != nil {
		return nil, fmt.Errorf("failed to mark flag 'hosted-cluster-namespace' as required: %w", err)
	}
	cmd.Flags().StringVarP(&dumpOpts.OutputPath, "output-path", "o", ".", "Path to output the dumped CRs from the mgmt cluster")
	return cmd, nil
}

func runDumpCrs(ctx context.Context, opts DumpCRsCmdOptions, config ClusterConfig) error {
	validated, err := opts.Validate(ctx)
	if err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	completed, err := config.CompleteBreakglass(ctx, validated.ValidatedBreakglassAKSOptions)
	if err != nil {
		return err
	}

	kubeconfigBytes, err := aks.GetAKSKubeConfigContent(ctx, completed.SubscriptionID, completed.ResourceGroup, completed.ClusterName, completed.AzureCredential)
	if err != nil {
		return fmt.Errorf("failed to get AKS kubeconfig: %w", err)
	}

	k8sClient, err := kubeclient.NewK8sClientFromKubeConfig(
		ctx,
		kubeconfigBytes,
		completed.AzureCredential,
		apiextensionsv1.AddToScheme,
		corev1.AddToScheme,
	)
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	crLister := crdump.NewCustomResourceLister(k8sClient)
	dumper := crdump.NewCliDumper(crLister, opts.OutputPath, opts.HostedClusterNamespace)

	if err := dumper.DumpCRs(ctx, opts.HostedClusterNamespace); err != nil {
		return fmt.Errorf("failed to dump CRs: %w", err)
	}
	return nil
}
