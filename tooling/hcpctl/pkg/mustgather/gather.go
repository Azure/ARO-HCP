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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"reflect"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"golang.org/x/sync/errgroup"

	"github.com/Azure/azure-kusto-go/azkustodata/types"

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
type RowOutputFunc func(ctx context.Context, logLineChan chan *NormalizedLogLine, options RowOutputOptions) error

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
	// Log           []byte          `kusto:"log"`
	Cluster       string    `kusto:"cluster"`
	Namespace     string    `kusto:"namespace_name"`
	ContainerName string    `kusto:"container_name"`
	Timestamp     time.Time `kusto:"timestamp"`

	Log       map[string]any  `kusto:"-"` // Log content, set by the gatherer pipeline
	QueryName string          `kusto:"-"` // Query name, set by the gatherer pipeline
	QueryType kusto.QueryType `kusto:"-"` // Query type, set by the gatherer pipeline
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
		infraLogsOnly: opts.GatherInfraLogs,
	}
}

// NewCliGatherer creates a new Gatherer with file-based output for CLI usage.
// This is a convenience constructor that configures the Gatherer for the must-gather CLI command
// with the default file-based output function.
//
// For custom output handling, use NewGatherer() instead.
func NewCliGatherer(queryClient QueryClientInterface, outputPath, serviceLogsDirectory, hostedControlPlaneLogsDirectory, customLogsDirectory string, opts GathererOptions, infraLogsOnly bool) *Gatherer {
	outputOptions := map[string]any{
		"outputPath":                              outputPath,
		string(kusto.QueryTypeServices):           serviceLogsDirectory,
		string(kusto.QueryTypeHostedControlPlane): hostedControlPlaneLogsDirectory,
		string(kusto.QueryTypeCustomLogs):         customLogsDirectory,
	}

	return &Gatherer{
		QueryClient:   queryClient,
		outputFunc:    cliOutputFunc,
		outputOptions: outputOptions,
		opts:          opts,
		infraLogsOnly: infraLogsOnly,
	}
}

func cliOutputFunc(ctx context.Context, logLineChan chan *NormalizedLogLine, options RowOutputOptions) error {
	outputPath := options["outputPath"].(string)

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
			directory, ok := options[string(logLine.QueryType)].(string)
			if !ok {
				directory = "cluster"
			}
			var fileName string
			switch logLine.QueryType {
			case kusto.QueryTypeKubernetesEvents, kusto.QueryTypeSystemdLogs:
				fileName = fmt.Sprintf("%s-%s.jsonl", logLine.Cluster, logLine.QueryType)
			case kusto.QueryTypeCustomLogs:
				fileName = fmt.Sprintf("custom-query-%s.jsonl", logLine.QueryName)
			default:
				fileName = fmt.Sprintf("%s-%s-%s.jsonl", logLine.Cluster, logLine.Namespace, logLine.ContainerName)
			}

			file, ok := openedFiles[fileName]
			if !ok {
				filePath := path.Join(outputPath, directory, fileName)
				newFile, err := os.Create(filePath)
				if err != nil {
					allErrors = errors.Join(allErrors, fmt.Errorf("failed to create output file %s: %w", filePath, err))
					continue
				}
				openedFiles[fileName] = newFile
				file = newFile
			}
			thisLog, err := json.Marshal(logLine.Log)
			if err != nil {
				allErrors = errors.Join(allErrors, fmt.Errorf("failed to marshal log line: %w", err))
			} else if _, err := fmt.Fprintf(file, "%s\n", string(thisLog)); err != nil {
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

	queryFactory, err := kusto.NewQueryFactory()
	if err != nil {
		return fmt.Errorf("failed to create query factory: %w", err)
	}

	logger.V(1).Info("Query options", "queryOptions", g.GetQueryOptions())

	// First, get all cluster IDs
	clusterIds := make([]string, 0)
	clusterIdDef, err := queryFactory.GetBuiltinQueryDefinition("clusterId")
	if err != nil {
		return fmt.Errorf("failed to get cluster id query definition: %w", err)
	}
	clusterIdQueries, err := queryFactory.Build(*clusterIdDef, kusto.NewTemplateDataFromOptions(g.GetQueryOptions()))
	if err != nil {
		return fmt.Errorf("failed to build cluster id query: %w", err)
	}
	allClusterIds, err := executeQueryAndConvert[ClusterIdRow](ctx, g, clusterIdQueries[0])
	if err != nil {
		return fmt.Errorf("failed to execute cluster id query: %w", err)
	}
	for _, row := range allClusterIds {
		clusterIds = append(clusterIds, row.ClusterId)
	}
	logger.V(1).Info("Obtained following clusterIDs", "clusterIds", strings.Join(clusterIds, ", "))

	// Gather service logs
	servicesQueries, err := serviceLogs(queryFactory, "serviceLogs", g.GetQueryOptions(), clusterIds)
	if err != nil {
		return fmt.Errorf("failed to build services queries: %w", err)
	}
	if err := g.queryAndWriteToFile(ctx, servicesQueries); err != nil {
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
		if err := g.queryAndWriteToFile(ctx, hcpQueries); err != nil {
			gatherErrors = errors.Join(gatherErrors, fmt.Errorf("failed to execute hosted control plane logs query: %w", err))
		}
	}

	// Gather cluster names

	// clusterNamesQueries, err := clusterNamesQueries(queryFactory, g.GetQueryOptions())
	var clusterNamesQueries []kusto.Query
	for _, queryName := range []string{"clusterNamesSvc", "clusterNamesHcp"} {
		queryDef, err := queryFactory.GetBuiltinQueryDefinition(queryName)
		if err != nil {
			return fmt.Errorf("failed to get cluster names query definition: %w", err)
		}
		queries, err := queryFactory.Build(*queryDef, kusto.NewTemplateDataFromOptions(g.GetQueryOptions()))
		if err != nil {
			return fmt.Errorf("failed to build cluster names query: %w", err)
		}
		clusterNamesQueries = append(clusterNamesQueries, queries...)
	}
	clusterNames := make([]string, 0)
	for _, nameQuery := range clusterNamesQueries {
		allClusterNames, err := executeQueryAndConvert[ClusterNameRow](ctx, g, nameQuery)
		if err != nil {
			gatherErrors = errors.Join(gatherErrors, fmt.Errorf("failed to execute cluster names query: %w", err))
		}
		for _, row := range allClusterNames {
			clusterNames = append(clusterNames, row.ClusterName)
		}
	}
	logger.V(1).Info("Obtained following clusterNames", "clusterNames", strings.Join(clusterNames, ", "))

	var customQueries []kusto.Query
	customQueryDefinitions := queryFactory.GetAllCustomQueryDefinitions()
	for _, def := range customQueryDefinitions {
		if def.IncludeInMustGather {
			q, err := queryFactory.Build(def, kusto.NewTemplateDataFromOptions(g.GetQueryOptions(), kusto.WithClusterNames(clusterNames)))
			if err != nil {
				return fmt.Errorf("failed to build custom query %q: %w", def.Name, err)
			}
			customQueries = append(customQueries, q...)
		}
	}

	if err := g.queryAndWriteToFile(ctx, customQueries); err != nil {
		gatherErrors = errors.Join(gatherErrors, fmt.Errorf("failed to execute custom logs query: %w", err))
	}

	if g.opts.SkipKubernetesEventsLogs && !g.opts.CollectSystemdLogs {
		logger.V(1).Info("Skipping Kubernetes events and Systemd logs")
		return nil
	}

	if !g.opts.SkipKubernetesEventsLogs {
		k8sEventsMgmtDef, err := queryFactory.GetBuiltinQueryDefinition("kubernetesEventsMgmt")
		if err != nil {
			return fmt.Errorf("failed to get kubernetes events mgmt query definition: %w", err)
		}
		k8sEventsSvcDef, err := queryFactory.GetBuiltinQueryDefinition("kubernetesEventsSvc")
		if err != nil {
			return fmt.Errorf("failed to get kubernetes events svc query definition: %w", err)
		}
		allKubernetesEventsQueries := make([]kusto.Query, 0)
		for _, clusterName := range clusterNames {
			if strings.Contains(clusterName, "mgmt") {
				queries, err := queryFactory.Build(*k8sEventsMgmtDef, kusto.NewTemplateDataFromOptions(g.GetQueryOptions(), kusto.WithHCPNamespacePrefix(HCPNamespacePrefix), kusto.WithClusterIds(clusterIds), kusto.WithClusterName(clusterName)))
				if err != nil {
					return fmt.Errorf("failed to build kubernetes events mgmt query: %w", err)
				}
				allKubernetesEventsQueries = append(allKubernetesEventsQueries, queries...)
			} else {
				queries, err := queryFactory.Build(*k8sEventsSvcDef, kusto.NewTemplateDataFromOptions(g.GetQueryOptions(), kusto.WithClusterIds(clusterIds), kusto.WithClusterName(clusterName)))
				if err != nil {
					return fmt.Errorf("failed to build kubernetes events svc query: %w", err)
				}
				allKubernetesEventsQueries = append(allKubernetesEventsQueries, queries...)
			}
		}
		if err := g.queryAndWriteToFile(ctx, allKubernetesEventsQueries); err != nil {
			gatherErrors = errors.Join(gatherErrors, fmt.Errorf("failed to execute kubernetes events query: %w", err))
		}
	}

	if g.opts.CollectSystemdLogs {
		systemdLogsDef, err := queryFactory.GetBuiltinQueryDefinition("systemdLogs")
		if err != nil {
			return fmt.Errorf("failed to get systemd logs query definition: %w", err)
		}
		allSystemdLogsQueries := make([]kusto.Query, 0)
		for _, clusterName := range clusterNames {
			queries, err := queryFactory.Build(*systemdLogsDef, kusto.NewTemplateDataFromOptions(g.GetQueryOptions(), kusto.WithClusterName(clusterName)))
			if err != nil {
				return fmt.Errorf("failed to build systemd logs query: %w", err)
			}
			allSystemdLogsQueries = append(allSystemdLogsQueries, queries...)
		}
		if err := g.queryAndWriteToFile(ctx, allSystemdLogsQueries); err != nil {
			gatherErrors = errors.Join(gatherErrors, fmt.Errorf("failed to execute systemd logs query: %w", err))
		}
	}

	return gatherErrors
}

func (g *Gatherer) gatherInfraLogs(ctx context.Context) error {
	queryFactory, err := kusto.NewQueryFactory()
	if err != nil {
		return fmt.Errorf("failed to create query factory: %w", err)
	}

	k8sEventsDef, err := queryFactory.GetBuiltinQueryDefinition("kubernetesEvents")
	if err != nil {
		return fmt.Errorf("failed to get kubernetes events query definition: %w", err)
	}
	queries, err := queryFactory.Build(*k8sEventsDef, kusto.NewTemplateDataFromOptions(g.GetQueryOptions()))
	if err != nil {
		return fmt.Errorf("failed to build kubernetes events query: %w", err)
	}
	if err := g.queryAndWriteToFile(ctx, queries); err != nil {
		return fmt.Errorf("failed to execute kubernetes events query: %w", err)
	}

	systemdLogsDef, err := queryFactory.GetBuiltinQueryDefinition("systemdLogs")
	if err != nil {
		return fmt.Errorf("failed to get systemd logs query definition: %w", err)
	}
	queries, err = queryFactory.Build(*systemdLogsDef, kusto.NewTemplateDataFromOptions(g.GetQueryOptions()))
	if err != nil {
		return fmt.Errorf("failed to build systemd logs query: %w", err)
	}
	if err := g.queryAndWriteToFile(ctx, queries); err != nil {
		return fmt.Errorf("failed to execute systemd logs query: %w", err)
	}

	queries, err = serviceLogs(queryFactory, "infraServiceLogs", g.GetQueryOptions(), []string{})
	if err != nil {
		return fmt.Errorf("failed to build services queries: %w", err)
	}
	if err := g.queryAndWriteToFile(ctx, queries); err != nil {
		return fmt.Errorf("failed to execute services query: %w", err)
	}
	return nil
}

func executeQueryAndConvert[T any](ctx context.Context, g *Gatherer, query kusto.Query) ([]T, error) {
	outputChannel := make(chan kusto.TaggedRow)
	var allRows []T

	group := new(errgroup.Group)
	group.Go(func() error {
		for row := range outputChannel {
			var target T
			if err := row.Row.ToStruct(&target); err != nil {
				return fmt.Errorf("failed to convert row to struct: %w", err)
			}
			allRows = append(allRows, target)
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

func (g *Gatherer) queryAndWriteToFile(ctx context.Context, queries []kusto.Query) error {
	logger := logr.FromContextOrDiscard(ctx)
	queryOutputChannel := make(chan kusto.TaggedRow)
	logLineChan := make(chan *NormalizedLogLine)

	logger.V(6).Info("Executing query", "queryCount", len(queries))

	queryGroup, queryCtx := errgroup.WithContext(ctx)
	queryGroup.Go(func() error {
		defer close(queryOutputChannel)
		return g.QueryClient.ConcurrentQueries(queryCtx, queries, queryOutputChannel)
	})

	queryGroup.Go(func() error {
		return g.outputFunc(queryCtx, logLineChan, g.outputOptions)
	})

	queryGroup.Go(func() error {
		defer close(logLineChan)
		return g.convertRows(queryCtx, queryOutputChannel, logLineChan)
	})

	logger.V(6).Info("Waiting for query to complete")
	if err := queryGroup.Wait(); err != nil {
		return fmt.Errorf("error during query execution: %w", err)
	}

	return nil
}

func (g *Gatherer) convertRows(ctx context.Context, rowChannel <-chan kusto.TaggedRow, outPutChannel chan<- *NormalizedLogLine) error {
	// knownColumns are columns used by must-gather and should not be written to the output
	knownColumns := make(map[string]struct{})
	t := reflect.TypeOf(NormalizedLogLine{})
	for i := range t.NumField() {
		tag := t.Field(i).Tag.Get("kusto")
		if tag != "" && tag != "-" {
			knownColumns[tag] = struct{}{}
		}
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case tagged, ok := <-rowChannel:
			if !ok {
				return nil
			}
			normalizedLogLine := &NormalizedLogLine{}
			if err := tagged.Row.ToStruct(normalizedLogLine); err != nil {
				return fmt.Errorf("failed to convert row to struct: %w", err)
			}
			normalizedLogLine.QueryName = tagged.QueryName
			normalizedLogLine.QueryType = tagged.QueryType

			columns := tagged.Row.Columns()
			values := tagged.Row.Values()
			// the actual log line
			log := make(map[string]any, len(columns))
			for i, col := range columns {
				if _, ok := knownColumns[col.Name()]; !ok {
					if values[i].GetType() == types.Dynamic {
						var logAsInterface = values[i].GetValue()
						var logMap map[string]any
						if logAsBytes, ok := logAsInterface.([]byte); ok {
							err := json.Unmarshal(logAsBytes, &logMap)
							if err == nil {
								log[col.Name()] = logMap
								continue
							}
						}
					}
					log[col.Name()] = values[i].String()
				}
			}
			if len(log) > 0 {
				normalizedLogLine.Log = log
			}

			select {
			case <-ctx.Done():
				return ctx.Err()
			case outPutChannel <- normalizedLogLine:
			}
		}
	}
}
