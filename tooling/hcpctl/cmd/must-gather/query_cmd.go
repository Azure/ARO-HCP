package mustgather

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
	"github.com/spf13/cobra"
)

func newQueryCommand() (*cobra.Command, error) {
	opts := DefaultMustGatherOptions()

	cmd := &cobra.Command{
		Use:              "query",
		Short:            "Execute default queries against Azure Data Explorer",
		Long:             `Execute preconfigured queries against Azure Data Explorer clusters.`,
		Args:             cobra.NoArgs,
		SilenceUsage:     true,
		SilenceErrors:    true,
		TraverseChildren: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.Run(cmd.Context(), false)
		},
		CompletionOptions: cobra.CompletionOptions{
			HiddenDefaultCmd: true,
		},
	}

	if err := BindMustGatherOptions(opts, cmd); err != nil {
		return nil, err
	}

	return cmd, nil
}

func (opts *MustGatherOptions) Run(ctx context.Context) error {
	defer func() {
		if closeErr := opts.QueryClient.Close(); closeErr != nil {
			fmt.Printf("Warning: failed to close Kusto client: %v\n", closeErr)
		}
	}()

	clusterIds, err := executeClusterIdQuery(ctx, opts, getClusterIdQuery(opts.SubscriptionID, opts.ResourceGroup))
	if err != nil {
		return fmt.Errorf("failed to execute cluster id query: %w", err)
	}
	fmt.Printf("Cluster IDs: %s\n", strings.Join(clusterIds, ", "))
	opts.QueryOptions.ClusterIds = clusterIds
	err = writeQueryOptionsToFile(opts.OutputPath, opts.QueryOptions)
	if err != nil {
		return fmt.Errorf("failed to write query options to file: %w", err)
	}

	err = executeServicesQueries(ctx, opts, opts.QueryOptions)
	if err != nil {
		return fmt.Errorf("failed to execute query: %w", err)
	}

	if opts.SkipHostedControlePlaneLogs {
		fmt.Println("Skipping hosted control plane logs")
	} else {
		fmt.Println("Executing hosted control plane logs")
		err := executeHostedControlPlaneLogsQuery(ctx, opts, opts.QueryOptions)
		if err != nil {
			return fmt.Errorf("failed to execute hosted control plane logs query: %w", err)
		}
	}

	return nil
}

func writeContainerLogsToFile(outputChannel chan any, outputPath string, directory string) error {
	openedFiles := make(map[string]*os.File)
	for row := range outputChannel {
		fileName := fmt.Sprintf("%s-%s-%s.log", row.(*ContainerLogsRow).Cluster, row.(*ContainerLogsRow).Namespace, row.(*ContainerLogsRow).ContainerName)

		file, ok := openedFiles[fileName]
		if !ok {
			file, err := os.Create(path.Join(outputPath, directory, fileName))
			if err != nil {
				return fmt.Errorf("failed to create output file: %w", err)
			}
			openedFiles[fileName] = file
		}
		defer file.Close()
		fmt.Fprintf(openedFiles[fileName], "%s\n", string(row.(*ContainerLogsRow).Log))
	}
	return nil
}

func executeClusterIdQuery(ctx context.Context, opts *MustGatherOptions, query *kusto.ConfigurableQuery) ([]string, error) {
	outputChannel := make(chan any)
	allClusterIds := make([]string, 0)

	go func() {
		for row := range outputChannel {
			clusterId := row.(*ClusterIdRow).ClusterId
			if clusterId != "" {
				allClusterIds = append(allClusterIds, clusterId)
			}
		}
	}()

	_, err := opts.QueryClient.Client.ExecutePreconfiguredQuery(ctx, query, outputChannel, ClusterIdRow{}, opts.QueryTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to execute query: %w", err)
	}
	close(outputChannel)

	return allClusterIds, nil
}

// executeQuery performs the actual query execution against Azure Data Explorer
func executeServicesQueries(ctx context.Context, opts *MustGatherOptions, queryOpts QueryOptions) error {
	queries := getServicesQueries(queryOpts)

	outputChannel := make(chan any)
	defer close(outputChannel)

	var err error
	go func() {
		err = writeContainerLogsToFile(outputChannel, opts.OutputPath, ServicesLogDirectory)
	}()
	if err != nil {
		return fmt.Errorf("failed to write container logs to file: %w", err)
	}

	return opts.QueryClient.concurrentQueries(ctx, queries, ContainerLogsRow{}, outputChannel)
}

func executeHostedControlPlaneLogsQuery(ctx context.Context, opts *MustGatherOptions, queryOpts QueryOptions) error {
	query := getHostedControlPlaneLogsQuery(queryOpts)

	outputChannel := make(chan any)
	defer close(outputChannel)

	var err error
	go func() {
		err = writeContainerLogsToFile(outputChannel, opts.OutputPath, HostedControlPlaneLogDirectory)
	}()
	if err != nil {
		return fmt.Errorf("failed to write container logs to file: %w", err)
	}

	return opts.QueryClient.concurrentQueries(ctx, query, ContainerLogsRow{}, outputChannel)
}

func writeQueryOptionsToFile(outputPath string, queryOptions QueryOptions) error {
	file, err := os.Create(path.Join(outputPath, "query-options.json"))
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer file.Close()
	return json.NewEncoder(file).Encode(queryOptions)
}
