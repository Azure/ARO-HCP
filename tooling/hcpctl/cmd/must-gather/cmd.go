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
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/azure-kusto-go/kusto/data/table"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
)

var ServicesLogDirectory = "service"
var HostedControlPlaneLogDirectory = "hosted-control-plane"

var OptionsOutputFile = "options.json"

func NewCommand(group string) (*cobra.Command, error) {
	cmd := &cobra.Command{
		Use:     "must-gather",
		Short:   "Azure Data Explorer must-gather operations",
		GroupID: group,
		Long: `must-gather provides data collection operations for Azure Data Explorer clusters.

This command group includes subcommands for querying Azure Data Explorer instances
and collecting diagnostic data for troubleshooting and analysis.`,
		Example: `  hcpctl must-gather query --kusto-endpoint https://my-kusto-cluster.eastus.kusto.windows.net
  hcpctl must-gather query --kusto-endpoint https://my-kusto-cluster.eastus.kusto.windows.net --output results.json`,
		CompletionOptions: cobra.CompletionOptions{
			HiddenDefaultCmd: true,
		},
	}

	// Add query subcommand
	queryCmd, err := newQueryCommand()
	if err != nil {
		return nil, err
	}
	cmd.AddCommand(queryCmd)

	// Add query subcommand
	queryCmdLegacy, err := newQueryCommandLegacy()
	if err != nil {
		return nil, err
	}
	cmd.AddCommand(queryCmdLegacy)

	cleanCommand, err := newCleanCommand()
	if err != nil {
		return nil, err
	}
	cmd.AddCommand(cleanCommand)

	return cmd, nil
}

// NormalizedLogLine represents a as expected for output
type NormalizedLogLine struct {
	Log           []byte    `kusto:"log"`
	Cluster       string    `kusto:"cluster"`
	Namespace     string    `kusto:"namespace_name"`
	ContainerName string    `kusto:"container_name"`
	Timestamp     time.Time `kusto:"timestamp"`
}

type QueryClient struct {
	Client       kusto.KustoClient
	QueryTimeout time.Duration
	OutputPath   string
}

func (q *QueryClient) concurrentQueries(ctx context.Context, queries []*kusto.ConfigurableQuery, outputChannel chan *table.Row) error {
	logger := logr.FromContextOrDiscard(ctx)
	wg := sync.WaitGroup{}
	wg.Add(len(queries))

	errorCh := make(chan error, len(queries))

	for i, query := range queries {
		go func(query *kusto.ConfigurableQuery, queryIndex int) {
			defer wg.Done()
			result, err := q.Client.ExecutePreconfiguredQuery(ctx, query, outputChannel, q.QueryTimeout)
			if err != nil {
				logger.Error(err, "Query failed", "name", query.Name)
				errorCh <- fmt.Errorf("failed to execute query: %w", err)
				return
			}
			err = serializeOutputToFile(q.OutputPath, fmt.Sprintf("%s.json", query.Name), result)
			if err != nil {
				errorCh <- fmt.Errorf("failed to write query result to file: %w", err)
			}
		}(query, i)
	}

	wg.Wait()
	close(errorCh)

	if allErrors := errors.Join(<-errorCh); allErrors != nil {
		return fmt.Errorf("failed to execute queries: %v", allErrors)
	}

	return nil
}

func (q *QueryClient) Close() error {
	return q.Client.Close()
}

func serializeOutputToFile(outputPath string, outputFile string, output any) error {
	file, err := os.Create(path.Join(outputPath, outputFile))
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer file.Close()
	return json.NewEncoder(file).Encode(output)
}
