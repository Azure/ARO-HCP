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
type RowOutputFunc func(logLineChan chan *NormalizedLogLine, queryType QueryType, options RowOutputOptions) error

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
	SkipHostedControlPlaneLogs bool          // Skip hosted control plane logs
	QueryOptions               *QueryOptions // Query options
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
func NewGatherer(queryClient QueryClientInterface, outputFunc RowOutputFunc, outputOptions RowOutputOptions, opts GathererOptions) *Gatherer {
	return &Gatherer{
		QueryClient:   queryClient,
		outputFunc:    outputFunc,
		outputOptions: outputOptions,
		opts:          opts,
	}
}

// NewCliGatherer creates a new Gatherer with file-based output for CLI usage.
// This is a convenience constructor that configures the Gatherer for the must-gather CLI command
// with the default file-based output function.
//
// For custom output handling, use NewGatherer() instead.
func NewCliGatherer(queryClient QueryClientInterface, outputPath, serviceLogsDirectory, hostedControlPlaneLogsDirectory string, opts GathererOptions) *Gatherer {
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

	// Ensure all files are properly closed when the function exits
	defer func() {
		for _, file := range openedFiles {
			if closeErr := file.Close(); closeErr != nil {
				allErrors = errors.Join(allErrors, fmt.Errorf("failed to close file: %w", closeErr))
			}
		}
	}()

	for logLine := range logLineChan {
		fileName := fmt.Sprintf("%s-%s-%s.log", logLine.Cluster, logLine.Namespace, logLine.ContainerName)

		file, ok := openedFiles[fileName]
		if !ok {
			newFile, err := os.Create(path.Join(outputPath, directory, fileName))
			if err != nil {
				allErrors = errors.Join(allErrors, fmt.Errorf("failed to create output file: %w", err))
				return allErrors
			}
			openedFiles[fileName] = newFile
			file = newFile
		}

		if _, err := fmt.Fprintf(file, "%s\n", string(logLine.Log)); err != nil {
			allErrors = errors.Join(allErrors, fmt.Errorf("failed to write to file %s: %w", fileName, err))
			continue
		}
	}
	return allErrors
}

func (g *Gatherer) GatherLogs(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)

	// First, get all cluster IDs
	clusterIds, err := g.executeClusterIdQuery(ctx, g.opts.QueryOptions.GetClusterIdQuery())
	if err != nil {
		return fmt.Errorf("failed to execute cluster id query: %w", err)
	}
	logger.V(1).Info("Obtained following clusterIDs", "clusterIds", strings.Join(clusterIds, ", "))
	g.opts.QueryOptions.ClusterIds = clusterIds

	// Gather service logs
	if err := g.queryAndWriteToFile(ctx, QueryTypeServices, g.opts.QueryOptions.GetServicesQueries()); err != nil {
		return fmt.Errorf("failed to execute services query: %w", err)
	}

	// Gather hosted control plane logs if not skipped
	if g.opts.SkipHostedControlPlaneLogs {
		logger.V(2).Info("Skipping hosted control plane logs")
	} else {
		logger.V(1).Info("Executing hosted control plane logs")
		if err := g.queryAndWriteToFile(ctx, QueryTypeHostedControlPlane, g.opts.QueryOptions.GetHostedControlPlaneLogsQuery()); err != nil {
			return fmt.Errorf("failed to execute hosted control plane logs query: %w", err)
		}
	}

	return nil
}

func (g *Gatherer) executeClusterIdQuery(ctx context.Context, query *kusto.ConfigurableQuery) ([]string, error) {
	outputChannel := make(chan azkquery.Row)
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

	_, err := g.QueryClient.ExecutePreconfiguredQuery(ctx, query, outputChannel)
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
	queryOutputChannel := make(chan azkquery.Row)

	queryGroup := new(errgroup.Group)
	queryGroup.Go(func() error {
		return g.QueryClient.ConcurrentQueries(ctx, queries, queryOutputChannel)
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

func (g *Gatherer) convertRowsAndOutput(outputChannel <-chan azkquery.Row, queryType QueryType) error {
	logLineChan := make(chan *NormalizedLogLine)

	// Start output processing in background
	outputErrChan := make(chan error, 1)
	go func() {
		outputErrChan <- g.outputFunc(logLineChan, queryType, g.outputOptions)
	}()

	// Process rows and send to output
	for row := range outputChannel {
		normalizedLogLine := &NormalizedLogLine{}
		if err := row.ToStruct(normalizedLogLine); err != nil {
			close(logLineChan)
			return fmt.Errorf("failed to convert row to struct: %w", err)
		}
		logLineChan <- normalizedLogLine
	}

	// Close the channel to signal completion to the output function
	close(logLineChan)

	// Wait for output processing to complete and check for errors
	if outputErr := <-outputErrChan; outputErr != nil {
		return fmt.Errorf("failed to output data: %w", outputErr)
	}

	return nil
}
