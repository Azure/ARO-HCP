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

package mustgather

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"golang.org/x/sync/errgroup"

	azkquery "github.com/Azure/azure-kusto-go/azkustodata/query"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
)

// RowOutputOptions provides configuration options to RowOutputFunc implementations.
// Common options include:
//   - "outputPath": base directory for output files
//   - string(QueryTypeServices): subdirectory for service logs
//   - string(QueryTypeHostedControlPlane): subdirectory for HCP logs
type RowOutputOptions map[string]any

// RowOutputFunc defines how normalized log lines should be processed and output.
// This function receives log data through logLineChan and should process it according
// to the queryType and configuration in options.
//
// The function should:
//   - Consume all data from logLineChan until it's closed
//   - Handle the queryType to determine output format/location
//   - Use options for configuration (paths, formats, etc.)
//   - Return an error if processing fails
//
// Custom implementations can output to files, databases, APIs, or any other destination.
// The channel will be closed by the caller when all data has been sent.
type RowOutputFunc func(ctx context.Context, logLineChan chan *NormalizedLogLine, queryType QueryType, options RowOutputOptions) error

// NormalizedLogLine represents a single log entry with standardized fields.
// This structure is passed to RowOutputFunc implementations for processing.
//
// Fields available for custom output functions:
//   - Log: The actual log message content as bytes
//   - Cluster: The cluster ID where this log originated
//   - Namespace: The Kubernetes namespace (for HCP logs) or service name
//   - ContainerName: The container that generated this log
//   - Timestamp: When the log entry was created
//
// Example usage in a custom output function:
//
//	for logLine := range logLineChan {
//		fmt.Printf("[%s] %s/%s/%s: %s\n",
//			logLine.Timestamp.Format(time.RFC3339),
//			logLine.Cluster,
//			logLine.Namespace,
//			logLine.ContainerName,
//			string(logLine.Log))
//	}
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
	GatherInfraLogs            bool                // Gather all logs from the infrastructure, does NOT gather HCP logs
	SkipHostedControlPlaneLogs bool                // Skip hosted control plane logs
	SkipKubernetesEventsLogs   bool                // Skip Kubernetes events logs
	CollectSystemdLogs         bool                // Collect Systemd logs
	QueryOptions               *kusto.QueryOptions // Query options
}

// Gatherer coordinates the collection and processing of log data from Azure resources.
// It executes queries to gather logs from services and hosted control planes, then
// processes the results through a configurable output function.
//
// The Gatherer follows this workflow:
//  1. Discovers cluster IDs from the specified subscription and resource group
//  2. Gathers service logs from all discovered clusters
//  3. Optionally gathers hosted control plane logs (unless skipped)
//  4. Processes all log data through the configured outputFunc
//
// # Creating Custom Output Functions
//
// You can create custom output functions to handle log data differently. Your function
// must implement the RowOutputFunc signature:
//
//	func myCustomOutput(logLineChan chan *NormalizedLogLine, queryType QueryType, options RowOutputOptions) error {
//		for logLine := range logLineChan {
//			// Process each log line
//			switch queryType {
//			case QueryTypeServices:
//				// Handle service logs
//				fmt.Printf("[SVC] %s: %s\n", logLine.Cluster, string(logLine.Log))
//			case QueryTypeHostedControlPlane:
//				// Handle HCP logs
//				fmt.Printf("[HCP] %s: %s\n", logLine.Cluster, string(logLine.Log))
//			}
//		}
//		return nil
//	}
//
// Then create a Gatherer with your custom function:
//
//	gatherer := NewGatherer(
//		myQueryClient,
//		myCustomOutput,
//		RowOutputOptions{"format": "json"},
//		GathererOptions{...},
//	)
//
// # Example Custom Output Functions
//
// JSON output to a single file:
//
//	func jsonOutput(logLineChan chan *NormalizedLogLine, queryType QueryType, options RowOutputOptions) error {
//		outputPath := options["outputPath"].(string)
//		file, err := os.Create(filepath.Join(outputPath, fmt.Sprintf("%s.json", queryType)))
//		if err != nil {
//			return err
//		}
//		defer file.Close()
//
//		encoder := json.NewEncoder(file)
//		for logLine := range logLineChan {
//			if err := encoder.Encode(logLine); err != nil {
//				return err
//			}
//		}
//		return nil
//	}
//
// Send logs to an external API:
//
//	func apiOutput(logLineChan chan *NormalizedLogLine, queryType QueryType, options RowOutputOptions) error {
//		apiURL := options["apiURL"].(string)
//		client := &http.Client{}
//
//		for logLine := range logLineChan {
//			jsonData, _ := json.Marshal(logLine)
//			resp, err := client.Post(apiURL, "application/json", bytes.NewBuffer(jsonData))
//			if err != nil {
//				return err
//			}
//			resp.Body.Close()
//		}
//		return nil
//	}
//
// # Constructors
//
// Use NewGatherer() for full control with custom output functions.
// Use NewCliGatherer() for file-based output suitable for must-gather CLI usage.
type Gatherer struct {
	opts          GathererOptions
	QueryClient   QueryClientInterface
	outputFunc    RowOutputFunc
	outputOptions RowOutputOptions
	infraLogsOnly bool
}

func (g *Gatherer) GetQueryOptions() kusto.QueryOptions {
	return *g.opts.QueryOptions
}

// NewGatherer creates a new Gatherer with custom output function and options.
// This constructor provides full control over how log data is processed and output.
//
// Parameters:
//   - queryClient: Interface for executing database queries
//   - outputFunc: Custom function to process and output log data
//   - outputOptions: Configuration options passed to the output function
//   - opts: Gatherer configuration (clusters, timeframes, etc.)
//
// Example usage:
//
//	// Create custom JSON output function
//	jsonOutput := func(logLineChan chan *NormalizedLogLine, queryType QueryType, options RowOutputOptions) error {
//		outputPath := options["outputPath"].(string)
//		file, err := os.Create(filepath.Join(outputPath, fmt.Sprintf("%s.json", queryType)))
//		if err != nil {
//			return err
//		}
//		defer file.Close()
//
//		encoder := json.NewEncoder(file)
//		for logLine := range logLineChan {
//			if err := encoder.Encode(logLine); err != nil {
//				return err
//			}
//		}
//		return nil
//	}
//
//	// Create gatherer with custom output
//	gatherer := NewGatherer(
//		queryClient,
//		jsonOutput,
//		RowOutputOptions{"outputPath": "/tmp/logs", "format": "json"},
//		GathererOptions{SubscriptionID: "sub-123", ResourceGroup: "rg-test"},
//	)
func NewGatherer(queryClient QueryClientInterface, outputFunc RowOutputFunc, outputOptions map[string]any, opts GathererOptions) *Gatherer {
	return &Gatherer{
		QueryClient:   queryClient,
		outputFunc:    outputFunc,
		outputOptions: outputOptions,
		opts:          opts,
		infraLogsOnly: false,
	}
}

// NewCliGatherer creates a new Gatherer with file-based output for CLI usage.
// This is a convenience constructor that configures the Gatherer for the must-gather CLI command
// with the default file-based output function.
//
// For custom output handling, use NewGatherer() instead.
func NewCliGatherer(queryClient QueryClientInterface, outputPath, serviceLogsDirectory, hostedControlPlaneLogsDirectory string, opts GathererOptions, infraLogsOnly bool) *Gatherer {
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
		infraLogsOnly: infraLogsOnly,
	}
}

func cliOutputFunc(ctx context.Context, logLineChan chan *NormalizedLogLine, queryType QueryType, options RowOutputOptions) error {
	outputPath := options["outputPath"].(string)
	var directory string
	var ok bool
	if directory, ok = options[string(queryType)].(string); !ok {
		directory = "cluster"
	}

	openedFiles := make(map[string]*os.File)

	var allErrors error

	// Ensure all files are properly closed when the function exits
	defer func() {
		for _, file := range openedFiles {
			if closeErr := file.Close(); closeErr != nil {
				allErrors = errors.Join(allErrors, fmt.Errorf("failed to close file: %w", closeErr))
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case logLine, ok := <-logLineChan:
			if !ok {
				return allErrors
			}
			var fileName string
			if queryType == QueryTypeKubernetesEvents || queryType == QueryTypeSystemdLogs {
				fileName = fmt.Sprintf("%s-%s.log", logLine.Cluster, queryType)
			} else {
				fileName = fmt.Sprintf("%s-%s-%s.log", logLine.Cluster, logLine.Namespace, logLine.ContainerName)
			}

			file, ok := openedFiles[fileName]
			if !ok {
				newFile, err := os.Create(path.Join(outputPath, directory, fileName))
				if err != nil {
					return errors.Join(allErrors, fmt.Errorf("failed to create output file: %w", err))
				}
				openedFiles[fileName] = newFile
				file = newFile
			}
			if _, err := fmt.Fprintf(file, "%s\n", string(logLine.Log)); err != nil {
				allErrors = errors.Join(allErrors, fmt.Errorf("failed to write to file %s: %w", fileName, err))
			}
		}
	}
}

func (g *Gatherer) GatherLogs(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)
	if g.infraLogsOnly {
		logger.V(1).Info("Gathering infrastructure logs only")
		return g.gatherInfraLogs(ctx)
	}

	var gatherErrors error

	queryFactory := kusto.NewQueryFactory(false)

	logger.V(1).Info("Query options", "queryOptions", g.GetQueryOptions())

	// First, get all cluster IDs
	clusterIds := make([]string, 0)
	clusterIdQuery, err := queryFactory.Build(kusto.ClusterIdQueryDef, kusto.NewTemplateDataFromOptions(g.GetQueryOptions()))
	if err != nil {
		return fmt.Errorf("failed to build cluster id query: %w", err)
	}
	allClusterIds, err := g.executeQueryAndConvert(ctx, clusterIdQuery, ClusterIdRow{})
	if err != nil {
		return fmt.Errorf("failed to execute cluster id query: %w", err)
	}
	for _, row := range allClusterIds {
		clusterIds = append(clusterIds, row.(ClusterIdRow).ClusterId)
	}
	logger.V(1).Info("Obtained following clusterIDs", "clusterIds", strings.Join(clusterIds, ", "))

	// Gather service logs
	servicesQueries, err := serviceLogs(queryFactory, g.GetQueryOptions(), clusterIds)
	if err != nil {
		return fmt.Errorf("failed to build services queries: %w", err)
	}
	if err := g.queryAndWriteToFile(ctx, QueryTypeServices, servicesQueries); err != nil {
		gatherErrors = errors.Join(gatherErrors, fmt.Errorf("failed to execute services query: %w", err))
	}

	// Gather hosted control plane logs if not skipped
	if g.opts.SkipHostedControlPlaneLogs {
		logger.V(2).Info("Skipping hosted control plane logs")
	} else {
		logger.V(1).Info("Executing hosted control plane logs")
		hcpQueries, err := hostedControlPlaneLogs(queryFactory, g.GetQueryOptions(), clusterIds)
		if err != nil {
			return fmt.Errorf("failed to build hosted control plane logs query: %w", err)
		}
		if err := g.queryAndWriteToFile(ctx, QueryTypeHostedControlPlane, hcpQueries); err != nil {
			gatherErrors = errors.Join(gatherErrors, fmt.Errorf("failed to execute hosted control plane logs query: %w", err))
		}
	}

	// Gather cluster names
	if g.opts.SkipKubernetesEventsLogs && !g.opts.CollectSystemdLogs {
		logger.V(1).Info("Skipping Kubernetes events and Systemd logs")
		return nil
	}

	clusterNamesQueries, err := clusterNamesQueries(queryFactory, g.GetQueryOptions())
	if err != nil {
		return fmt.Errorf("failed to build cluster names queries: %w", err)
	}
	clusterNames := make([]string, 0)
	for _, nameQuery := range clusterNamesQueries {
		allClusterNames, err := g.executeQueryAndConvert(ctx, nameQuery, ClusterNameRow{})
		if err != nil {
			gatherErrors = errors.Join(gatherErrors, fmt.Errorf("failed to execute cluster names query: %w", err))
		}
		for _, row := range allClusterNames {
			clusterNames = append(clusterNames, row.(ClusterNameRow).ClusterName)
		}
	}
	logger.V(1).Info("Obtained following clusterNames", "clusterNames", strings.Join(clusterNames, ", "))

	if !g.opts.SkipKubernetesEventsLogs {
		allKubernetesEventsQueries := make([]kusto.Query, 0)
		for _, clusterName := range clusterNames {
			g.opts.QueryOptions.InfraClusterName = clusterName
			if strings.Contains(clusterName, "mgmt") {
				query, err := queryFactory.Build(kusto.KubernetesEventsMgmtQuery, kusto.NewTemplateDataFromOptions(g.GetQueryOptions(), kusto.WithHCPNamespacePrefix(HCPNamespacePrefix), kusto.WithClusterIds(clusterIds)))
				if err != nil {
					return fmt.Errorf("failed to build kubernetes events mgmt query: %w", err)
				}
				allKubernetesEventsQueries = append(allKubernetesEventsQueries, query)
			} else {
				query, err := queryFactory.Build(kusto.KubernetesEventsSvcQuery, kusto.NewTemplateDataFromOptions(g.GetQueryOptions(), kusto.WithClusterIds(clusterIds)))
				if err != nil {
					return fmt.Errorf("failed to build kubernetes events svc query: %w", err)
				}
				allKubernetesEventsQueries = append(allKubernetesEventsQueries, query)
			}
		}
		if err := g.queryAndWriteToFile(ctx, QueryTypeKubernetesEvents, allKubernetesEventsQueries); err != nil {
			gatherErrors = errors.Join(gatherErrors, fmt.Errorf("failed to execute kubernetes events query: %w", err))
		}
	}

	if g.opts.CollectSystemdLogs {
		allSystemdLogsQueries := make([]kusto.Query, 0)
		for _, clusterName := range clusterNames {
			g.opts.QueryOptions.InfraClusterName = clusterName
			query, err := queryFactory.Build(kusto.InfraSystemdLogsQuery, kusto.NewTemplateDataFromOptions(g.GetQueryOptions()))
			if err != nil {
				return fmt.Errorf("failed to build systemd logs query: %w", err)
			}
			allSystemdLogsQueries = append(allSystemdLogsQueries, query)
		}
		if err := g.queryAndWriteToFile(ctx, QueryTypeSystemdLogs, allSystemdLogsQueries); err != nil {
			gatherErrors = errors.Join(gatherErrors, fmt.Errorf("failed to execute systemd logs query: %w", err))
		}
	}

	return gatherErrors
}

func (g *Gatherer) gatherInfraLogs(ctx context.Context) error {
	queryFactory := kusto.NewQueryFactory(false)
	query, err := queryFactory.Build(kusto.InfraKubernetesEventsQuery, kusto.NewTemplateDataFromOptions(g.GetQueryOptions()))
	if err != nil {
		return fmt.Errorf("failed to build kubernetes events query: %w", err)
	}
	if err := g.queryAndWriteToFile(ctx, QueryTypeKubernetesEvents, []kusto.Query{query}); err != nil {
		return fmt.Errorf("failed to execute kubernetes events query: %w", err)
	}
	query, err = queryFactory.Build(kusto.InfraSystemdLogsQuery, kusto.NewTemplateDataFromOptions(g.GetQueryOptions()))
	if err != nil {
		return fmt.Errorf("failed to build systemd logs query: %w", err)
	}
	if err := g.queryAndWriteToFile(ctx, QueryTypeSystemdLogs, []kusto.Query{query}); err != nil {
		return fmt.Errorf("failed to execute systemd logs query: %w", err)
	}
	query, err = queryFactory.Build(kusto.InfraServiceLogsQuery, kusto.NewTemplateDataFromOptions(g.GetQueryOptions()))
	if err != nil {
		return fmt.Errorf("failed to build services queries: %w", err)
	}
	if err := g.queryAndWriteToFile(ctx, QueryTypeServices, []kusto.Query{query}); err != nil {
		return fmt.Errorf("failed to execute services query: %w", err)
	}
	return nil
}

func (g *Gatherer) executeQueryAndConvert(ctx context.Context, query kusto.Query, targetRow any) ([]any, error) {
	outputChannel := make(chan azkquery.Row)
	allRows := make([]any, 0)

	group := new(errgroup.Group)
	group.Go(func() error {
		for row := range outputChannel {
			switch targetRow := targetRow.(type) {
			case ClusterIdRow:
				cidRow := targetRow
				if err := row.ToStruct(&cidRow); err != nil {
					return fmt.Errorf("failed to convert row to struct: %w", err)
				}
				allRows = append(allRows, cidRow)
			case ClusterNameRow:
				clusterNameRow := targetRow
				if err := row.ToStruct(&clusterNameRow); err != nil {
					return fmt.Errorf("failed to convert row to struct: %w", err)
				}
				allRows = append(allRows, clusterNameRow)
			default:
				return fmt.Errorf("unsupported target row type: %T", targetRow)
			}
		}
		return nil
	})

	_, queryErr := g.QueryClient.ExecutePreconfiguredQuery(ctx, query, outputChannel)
	close(outputChannel)

	if err := group.Wait(); err != nil {
		return nil, fmt.Errorf("failed to process query results: %w", err)
	}

	if queryErr != nil {
		return nil, fmt.Errorf("failed to execute query: %w", queryErr)
	}

	return allRows, nil
}

func (g *Gatherer) queryAndWriteToFile(ctx context.Context, queryType QueryType, queries []kusto.Query) error {
	logger := logr.FromContextOrDiscard(ctx)
	queryOutputChannel := make(chan azkquery.Row)
	logLineChan := make(chan *NormalizedLogLine)

	logger.V(6).Info("Executing query", "queryType", queryType, "queries", len(queries), "queries", queries)

	queryGroup, queryCtx := errgroup.WithContext(ctx)
	queryGroup.Go(func() error {
		defer close(queryOutputChannel)
		return g.QueryClient.ConcurrentQueries(queryCtx, queries, queryOutputChannel)
	})

	queryGroup.Go(func() error {
		return g.outputFunc(queryCtx, logLineChan, queryType, g.outputOptions)
	})

	queryGroup.Go(func() error {
		defer close(logLineChan)
		return g.convertRows(queryCtx, queryOutputChannel, logLineChan)
	})

	logger.V(6).Info("Waiting for query to complete", "queryType", queryType)
	if err := queryGroup.Wait(); err != nil {
		return fmt.Errorf("error during query execution: %w", err)
	}

	return nil
}

func (g *Gatherer) convertRows(ctx context.Context, rowChannel <-chan azkquery.Row, outPutChannel chan<- *NormalizedLogLine) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case row, ok := <-rowChannel:
			if !ok {
				return nil
			}
			normalizedLogLine := &NormalizedLogLine{}
			if err := row.ToStruct(normalizedLogLine); err != nil {
				return fmt.Errorf("failed to convert row to struct: %w", err)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case outPutChannel <- normalizedLogLine: // now interruptible
			}
		}
	}
}
