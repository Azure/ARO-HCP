package mustgather

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
)

func newQueryCommandLegacy() (*cobra.Command, error) {
	opts := DefaultMustGatherOptions()

	cmd := &cobra.Command{
		Use:              "legacy-query",
		Short:            "Execute default queries against Azure Data Explorer",
		Long:             `Execute preconfigured queries against Azure Data Explorer clusters. This command relies on the akskubesystem table.`,
		Args:             cobra.NoArgs,
		SilenceUsage:     true,
		SilenceErrors:    true,
		TraverseChildren: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.Run(cmd.Context(), true)
		},
	}
	if err := BindMustGatherOptions(opts, cmd); err != nil {
		return nil, err
	}
	return cmd, nil
}

func (opts *MustGatherOptions) RunLegacy(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)
	clusterIds, err := executeClusterIdQuery(ctx, opts, getKubeSystemClusterIdQuery(opts.SubscriptionID, opts.ResourceGroup))
	if err != nil {
		return fmt.Errorf("failed to execute cluster id query: %w", err)
	}
	logger.V(1).Info("Obtained following clusterIDs", "clusterIds", strings.Join(clusterIds, ", "))
	opts.QueryOptions.ClusterIds = clusterIds
	err = writeQueryOptionsToFile(opts.OutputPath, opts.QueryOptions)
	if err != nil {
		return fmt.Errorf("failed to write query options to file: %w", err)
	}

	err = executeKubeSystemQueries(ctx, opts, opts.QueryOptions)
	if err != nil {
		return fmt.Errorf("failed to execute query: %w", err)
	}

	if opts.SkipHostedControlePlaneLogs {
		logger.V(2).Info("Skipping hosted control plane logs")
	} else {
		err = executeKubeSystemHostedControlPlaneLogsQuery(ctx, opts, opts.QueryOptions)
		if err != nil {
			return fmt.Errorf("failed to execute hosted control plane logs query: %w", err)
		}
	}

	return nil
}

type kubernetesCol struct {
	ContainerName string `json:"container_name"`
	Namespace     string `json:"namespace_name"`
}

func processKubesystemLogsRow(row *KubesystemLogsRow) error {
	// read containername/namespace from the row
	// handle inconsitent columns

	if row.ContainerName == "" {
		kubernetesCol := kubernetesCol{}
		err := json.Unmarshal([]byte(row.Kubernetes), &kubernetesCol)
		if err != nil {
			return fmt.Errorf("failed to unmarshal kubernetes column: %w", err)
		}
		row.ContainerName = kubernetesCol.ContainerName
		row.Namespace = kubernetesCol.Namespace
	}

	return nil
}

func writeKubesystemLogsToFile(outputChannel chan any, outputPath string, directory string) error {
	openedFiles := make(map[string]*os.File)
	for rowAsAny := range outputChannel {
		row := rowAsAny.(*KubesystemLogsRow)
		err := processKubesystemLogsRow(row)
		if err != nil {
			return fmt.Errorf("failed to process kubesystem logs row: %w", err)
		}
		fileName := fmt.Sprintf("%s-%s-%s.log", row.Cluster, row.Namespace, row.ContainerName)

		file, ok := openedFiles[fileName]
		if !ok {
			file, err := os.Create(path.Join(outputPath, directory, fileName))
			if err != nil {
				return fmt.Errorf("failed to create output file: %w", err)
			}
			openedFiles[fileName] = file
		}
		defer file.Close()
		fmt.Fprintf(openedFiles[fileName], "%s\n", string(row.Log))
	}
	return nil
}

func executeKubeSystemQueries(ctx context.Context, opts *MustGatherOptions, queryOpts QueryOptions) error {
	query := getKubeSystemQuery(opts.SubscriptionID, opts.ResourceGroup, queryOpts.ClusterIds)

	outputChannel := make(chan any)
	defer close(outputChannel)

	var err error

	go func() {
		err = writeKubesystemLogsToFile(outputChannel, opts.OutputPath, ServicesLogDirectory)
	}()
	if err != nil {
		return fmt.Errorf("failed to write kubesystem logs to file: %w", err)
	}

	return opts.QueryClient.concurrentQueries(ctx, []*kusto.ConfigurableQuery{query}, KubesystemLogsRow{}, outputChannel)
}

func executeKubeSystemHostedControlPlaneLogsQuery(ctx context.Context, opts *MustGatherOptions, queryOpts QueryOptions) error {
	query := getKubeSystemHostedControlPlaneLogsQuery(queryOpts)

	outputChannel := make(chan any)
	defer close(outputChannel)

	var err error
	go func() {
		err = writeKubesystemLogsToFile(outputChannel, opts.OutputPath, HostedControlPlaneLogDirectory)
	}()
	if err != nil {
		return fmt.Errorf("failed to write container logs to file: %w", err)
	}

	return opts.QueryClient.concurrentQueries(ctx, query, KubesystemLogsRow{}, outputChannel)
}
