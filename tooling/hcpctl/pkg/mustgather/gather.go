package mustgather

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
	"github.com/Azure/azure-kusto-go/kusto/data/table"
	"github.com/go-logr/logr"
	"golang.org/x/sync/errgroup"
)

type RowOutputOptions map[string]any
type RowOutputFunc func(logLineChan chan *NormalizedLogLine, queryType QueryType, options RowOutputOptions) error

// NormalizedLogLine represents a as expected for output
type NormalizedLogLine struct {
	Log           []byte    `kusto:"log"`
	Cluster       string    `kusto:"cluster"`
	Namespace     string    `kusto:"namespace_name"`
	ContainerName string    `kusto:"container_name"`
	Timestamp     time.Time `kusto:"timestamp"`
}

// GathererOptions represents the options for the Gatherer
// These options are used to configure the Gatherer and are passed to the Gatherer constructor
// They are used to generate the queries as well
type GathererOptions struct {
	ClusterIds                  []string  // Cluster IDs
	SubscriptionID              string    // Subscription ID
	ResourceGroup               string    // Resource group
	SkipHostedControlePlaneLogs bool      // Skip hosted control plane logs
	TimestampMin                time.Time // Timestamp minimum
	TimestampMax                time.Time // Timestamp maximum
	Limit                       int       // Limit the number of results
}

// Gatherer represents the Gatherer
type Gatherer struct {
	opts          GathererOptions
	QueryClient   QueryClient
	outputFunc    RowOutputFunc
	outputOptions RowOutputOptions
}

// NewCliGatherer creates a new Gatherer
// It configures a Gatherer configured for using in the must-gather cli command
func NewCliGatherer(queryClient QueryClient, outputPath, serviceLogsDirectory, hostedControlPlaneLogsDirectory string, opts GathererOptions) *Gatherer {
	outputOptions := map[string]any{
		"outputPath":                        outputPath,
		string(QueryTypeServices):           serviceLogsDirectory,
		string(QueryTypeHostedControlPlane): hostedControlPlaneLogsDirectory,
	}

	return &Gatherer{
		QueryClient:   queryClient,
		outputFunc:    cliOutputFunc,
		outputOptions: outputOptions,
		opts:          opts,
	}
}

func cliOutputFunc(logLineChan chan *NormalizedLogLine, queryType QueryType, options RowOutputOptions) error {
	outputPath := options["outputPath"].(string)
	directory := options[string(queryType)].(string)
	openedFiles := make(map[string]*os.File)

	var allErrors error

	for logLine := range logLineChan {
		fileName := fmt.Sprintf("%s-%s-%s.log", logLine.Cluster, logLine.Namespace, logLine.ContainerName)

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
		fmt.Fprintf(openedFiles[fileName], "%s\n", string(logLine.Log))
	}
	return allErrors
}

func (g *Gatherer) GatherLogs(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)
	clusterIds, err := g.executeClusterIdQuery(ctx, GetClusterIdQuery(g.opts.SubscriptionID, g.opts.ResourceGroup))
	if err != nil {
		return fmt.Errorf("failed to execute cluster id query: %w", err)
	}
	logger.V(1).Info("Obtained following clusterIDs", "clusterIds", strings.Join(clusterIds, ", "))
	g.opts.ClusterIds = clusterIds
	// err = serializeOutputToFile(g.cliRunOpts.OutputPath, OptionsOutputFile, g.cliRunOpts.QueryOptions)
	// if err != nil {
	// 	return fmt.Errorf("failed to write query options to file: %w", err)
	// }

	err = g.queryAndWriteToFile(ctx, QueryTypeServices, GetServicesQueries(g.opts))
	if err != nil {
		return fmt.Errorf("failed to execute query: %w", err)
	}

	if g.opts.SkipHostedControlePlaneLogs {
		logger.V(2).Info("Skipping hosted control plane logs")
	} else {
		logger.V(1).Info("Executing hosted control plane logs")
		err := g.queryAndWriteToFile(ctx, QueryTypeHostedControlPlane, GetHostedControlPlaneLogsQuery(g.opts))
		if err != nil {
			return fmt.Errorf("failed to execute hosted control plane logs query: %w", err)
		}
	}
	return nil
}

func (g *Gatherer) executeClusterIdQuery(ctx context.Context, query *kusto.ConfigurableQuery) ([]string, error) {
	outputChannel := make(chan *table.Row)
	allClusterIds := make([]string, 0)

	group := new(errgroup.Group)
	group.Go(func() error {
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

	_, err := g.QueryClient.Client.ExecutePreconfiguredQuery(ctx, query, outputChannel)
	if err != nil {
		return nil, fmt.Errorf("failed to execute query: %w", err)
	}
	close(outputChannel)

	if err := group.Wait(); err != nil {
		return nil, fmt.Errorf("failed to execute query: %w", err)
	}

	return allClusterIds, nil
}

func (g *Gatherer) queryAndWriteToFile(ctx context.Context, queryType QueryType, queries []*kusto.ConfigurableQuery) error {
	// logger := logr.FromContextOrDiscard(ctx)
	queryOutputChannel := make(chan *table.Row)

	queryGroup := new(errgroup.Group)
	queryGroup.Go(func() error {
		return g.QueryClient.concurrentQueries(ctx, queries, queryOutputChannel)
	})

	consumerGroup := new(errgroup.Group)
	consumerGroup.Go(func() error {
		return g.convertRowsAndOutput(queryOutputChannel, queryType)
	})

	if err := queryGroup.Wait(); err != nil {
		return fmt.Errorf("error during query execution: %w", err)
	}
	close(queryOutputChannel)
	if err := consumerGroup.Wait(); err != nil {
		return fmt.Errorf("error during query data transformation: %w", err)
	}
	return nil
}

func (g *Gatherer) convertRowsAndOutput(outputChannel chan *table.Row, queryType QueryType) error {
	logLineChan := make(chan *NormalizedLogLine)
	defer close(logLineChan)
	go g.outputFunc(logLineChan, queryType, g.outputOptions)

	for row := range outputChannel {
		normalizedLogLine := &NormalizedLogLine{}
		if err := row.ToStruct(normalizedLogLine); err != nil {
			return fmt.Errorf("failed to convert row to struct: %w", err)
		}
		logLineChan <- normalizedLogLine
	}
	return nil
}
