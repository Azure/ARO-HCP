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

	"github.com/Azure/azure-kusto-go/kusto/data/table"
	"github.com/go-logr/logr"
	"golang.org/x/sync/errgroup"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
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
	ClusterIds                 []string  // Cluster IDs
	SubscriptionID             string    // Subscription ID
	ResourceGroup              string    // Resource group
	SkipHostedControlPlaneLogs bool      // Skip hosted control plane logs
	TimestampMin               time.Time // Timestamp minimum
	TimestampMax               time.Time // Timestamp maximum
	Limit                      int       // Limit the number of results
}

// Gatherer represents the Gatherer
type Gatherer struct {
	opts          GathererOptions
	QueryClient   QueryClientInterface
	outputFunc    RowOutputFunc
	outputOptions RowOutputOptions
}

// NewCliGatherer creates a new Gatherer
// It configures a Gatherer configured for using in the must-gather cli command
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
	clusterIds, err := g.executeClusterIdQuery(ctx, GetClusterIdQuery(g.opts.SubscriptionID, g.opts.ResourceGroup))
	if err != nil {
		return fmt.Errorf("failed to execute cluster id query: %w", err)
	}
	logger.V(1).Info("Obtained following clusterIDs", "clusterIds", strings.Join(clusterIds, ", "))
	g.opts.ClusterIds = clusterIds

	// Gather service logs
	if err := g.queryAndWriteToFile(ctx, QueryTypeServices, GetServicesQueries(g.opts)); err != nil {
		return fmt.Errorf("failed to execute services query: %w", err)
	}

	// Gather hosted control plane logs if not skipped
	if g.opts.SkipHostedControlPlaneLogs {
		logger.V(2).Info("Skipping hosted control plane logs")
	} else {
		logger.V(1).Info("Executing hosted control plane logs")
		if err := g.queryAndWriteToFile(ctx, QueryTypeHostedControlPlane, GetHostedControlPlaneLogsQuery(g.opts)); err != nil {
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
	queryOutputChannel := make(chan *table.Row)

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

func (g *Gatherer) convertRowsAndOutput(outputChannel chan *table.Row, queryType QueryType) error {
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
