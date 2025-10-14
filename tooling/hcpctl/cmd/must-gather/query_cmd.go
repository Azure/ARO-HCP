package mustgather

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
	"github.com/Azure/azure-kusto-go/kusto/data/table"
	"golang.org/x/sync/errgroup"

	"github.com/go-logr/logr"
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
	logger := logr.FromContextOrDiscard(ctx)
	defer func() {
		if closeErr := opts.QueryClient.Close(); closeErr != nil {
			logger.Error(closeErr, "Warning: failed to close Kusto client")
		}
	}()

	clusterIds, err := executeClusterIdQuery(ctx, opts, getClusterIdQuery(opts.SubscriptionID, opts.ResourceGroup))
	if err != nil {
		return fmt.Errorf("failed to execute cluster id query: %w", err)
	}
	logger.V(1).Info("Obtained following clusterIDs", "clusterIds", strings.Join(clusterIds, ", "))
	opts.QueryOptions.ClusterIds = clusterIds
	err = serializeOutputToFile(opts.OutputPath, OptionsOutputFile, opts.QueryOptions)
	if err != nil {
		return fmt.Errorf("failed to write query options to file: %w", err)
	}

	err = executeServicesQueries(ctx, opts, opts.QueryOptions)
	if err != nil {
		return fmt.Errorf("failed to execute query: %w", err)
	}

	if opts.SkipHostedControlePlaneLogs {
		logger.V(2).Info("Skipping hosted control plane logs")
	} else {
		logger.V(1).Info("Executing hosted control plane logs")
		err := executeHostedControlPlaneLogsQuery(ctx, opts, opts.QueryOptions)
		if err != nil {
			return fmt.Errorf("failed to execute hosted control plane logs query: %w", err)
		}
	}

	return nil
}

func writeContainerLogsToFile(outputChannel chan *table.Row, castFunction func(input *table.Row) (*NormalizedLogLine, error), outputPath string, directory string) error {
	openedFiles := make(map[string]*os.File)
	var allErrors error
	for row := range outputChannel {
		normalizedRow, err := castFunction(row)
		if err != nil {
			return fmt.Errorf("failed to cast row: %w", err)
		}
		fileName := fmt.Sprintf("%s-%s-%s.log", normalizedRow.Cluster, normalizedRow.Namespace, normalizedRow.ContainerName)

		file, ok := openedFiles[fileName]
		if !ok {
			file, err := os.Create(path.Join(outputPath, directory, fileName))
			if err != nil {
				allErrors = errors.Join(allErrors, fmt.Errorf("failed to create output file: %w", err))
				return allErrors
			}
			openedFiles[fileName] = file
		}
		defer file.Close()
		fmt.Fprintf(openedFiles[fileName], "%s\n", string(normalizedRow.Log))
	}
	return allErrors
}

func executeClusterIdQuery(ctx context.Context, opts *MustGatherOptions, query *kusto.ConfigurableQuery) ([]string, error) {
	outputChannel := make(chan *table.Row)
	allClusterIds := make([]string, 0)

	g := new(errgroup.Group)
	g.Go(func() error {
		for row := range outputChannel {
			cidRow := &ClusterIdRow{}
			if err := row.ToStruct(cidRow); err != nil {
				return fmt.Errorf("failed to convert row to struct: %w", err)
			}
			if cidRow.ClusterId != "" {
				allClusterIds = append(allClusterIds, cidRow.ClusterId)
			}
		}
		return nil
	})

	_, err := opts.QueryClient.Client.ExecutePreconfiguredQuery(ctx, query, outputChannel, opts.QueryTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to execute query: %w", err)
	}
	close(outputChannel)

	if err := g.Wait(); err != nil {
		return nil, fmt.Errorf("failed to execute query: %w", err)
	}

	return allClusterIds, nil
}

// executeQuery performs the actual query execution against Azure Data Explorer
func executeServicesQueries(ctx context.Context, opts *MustGatherOptions, queryOpts QueryOptions) error {
	queries := getServicesQueries(queryOpts)
	return queryCastContainerLogAndWriteToFile(ctx, opts, queries)
}

func executeHostedControlPlaneLogsQuery(ctx context.Context, opts *MustGatherOptions, queryOpts QueryOptions) error {
	queries := getHostedControlPlaneLogsQuery(queryOpts)
	return queryCastContainerLogAndWriteToFile(ctx, opts, queries)
}

func queryCastContainerLogAndWriteToFile(ctx context.Context, opts *MustGatherOptions, queries []*kusto.ConfigurableQuery) error {
	castFunction := func(input *table.Row) (*NormalizedLogLine, error) {
		// can directly cast, cause the row is already normalized
		normalizedLogLine := &NormalizedLogLine{}
		if err := input.ToStruct(normalizedLogLine); err != nil {
			return nil, fmt.Errorf("failed to convert row to struct: %w", err)
		}
		return normalizedLogLine, nil
	}

	return queryAndWriteToFile(ctx, opts, castFunction, queries)
}

func queryAndWriteToFile(ctx context.Context, opts *MustGatherOptions, castFunction func(input *table.Row) (*NormalizedLogLine, error), queries []*kusto.ConfigurableQuery) error {
	// logger := logr.FromContextOrDiscard(ctx)
	queryOutputChannel := make(chan *table.Row)

	queryGroup := new(errgroup.Group)
	queryGroup.Go(func() error {
		return opts.QueryClient.concurrentQueries(ctx, queries, queryOutputChannel)
	})

	consumerGroup := new(errgroup.Group)
	consumerGroup.Go(func() error {
		return writeContainerLogsToFile(queryOutputChannel, castFunction, opts.OutputPath, ServicesLogDirectory)
	})

	if err := queryGroup.Wait(); err != nil {
		return fmt.Errorf("Error during query execution: %w", err)
	}
	close(queryOutputChannel)
	if err := consumerGroup.Wait(); err != nil {
		return fmt.Errorf("Error during query data transformation: %w", err)
	}
	return nil
}
