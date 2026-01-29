package base

import (
	"context"
	"fmt"
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

	completed, err := config.CompleteBreakglass(ctx, validated)
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
	crsList, err := crLister.ListCRs(ctx, opts.HostedClusterNamespace)
	if err != nil {
		return fmt.Errorf("failed to list CRs: %w", err)
	}

	if err := crdump.WriteCRsToDisk(opts.HostedClusterNamespace, crsList, opts.OutputPath); err != nil {
		return fmt.Errorf("failed to write CRs to disk: %w", err)
	}
	return nil
}
