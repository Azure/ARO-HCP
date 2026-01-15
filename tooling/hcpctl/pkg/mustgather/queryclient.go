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
	"sync"
	"time"

	"github.com/go-logr/logr"

	"github.com/Azure/azure-kusto-go/kusto/data/table"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
)

// QueryClientInterface defines the interface for querying data
type QueryClientInterface interface {
	ConcurrentQueries(ctx context.Context, queries []*kusto.ConfigurableQuery, outputChannel chan *table.Row) error
	Close() error
	ExecutePreconfiguredQuery(ctx context.Context, query *kusto.ConfigurableQuery, outputChannel chan *table.Row) (*kusto.QueryResult, error)
}

type QueryClient struct {
	Client       kusto.KustoClient
	QueryTimeout time.Duration
	OutputPath   string
	FileWriter   FileWriter
}

// NewQueryClient creates a new QueryClient with default dependencies
func NewQueryClient(client kusto.KustoClient, queryTimeout time.Duration, outputPath string) *QueryClient {
	return &QueryClient{
		Client:       client,
		QueryTimeout: queryTimeout,
		OutputPath:   outputPath,
		FileWriter:   &JsonEncoderWriter{},
	}
}

// NewQueryClient creates a new QueryClient with default dependencies
func NewQueryClientWithFileWriter(client kusto.KustoClient, queryTimeout time.Duration, outputPath string, fileWriter FileWriter) *QueryClient {
	return &QueryClient{
		Client:       client,
		QueryTimeout: queryTimeout,
		OutputPath:   outputPath,
		FileWriter:   fileWriter,
	}
}

func (q *QueryClient) ConcurrentQueries(ctx context.Context, queries []*kusto.ConfigurableQuery, outputChannel chan *table.Row) error {
	logger := logr.FromContextOrDiscard(ctx)
	wg := sync.WaitGroup{}
	wg.Add(len(queries))

	errorCh := make(chan error, len(queries))

	for i, query := range queries {
		go func(query *kusto.ConfigurableQuery, queryIndex int) {
			defer wg.Done()
			result, err := q.Client.ExecutePreconfiguredQuery(ctx, query, outputChannel)
			if err != nil {
				logger.Error(err, "Query failed", "name", query.Name)
				errorCh <- fmt.Errorf("failed to execute query: %w", err)
				return
			}
			if q.FileWriter != nil {
				err = q.FileWriter.WriteFile(q.OutputPath, fmt.Sprintf("%s.json", query.Name), result)
				if err != nil {
					errorCh <- fmt.Errorf("failed to write query result to file: %w", err)
				}
			}
		}(query, i)
	}

	wg.Wait()
	close(errorCh)

	var allErrors error
	for err := range errorCh {
		allErrors = errors.Join(allErrors, err)
	}
	if allErrors != nil {
		return fmt.Errorf("failed to execute queries: %w", allErrors)
	}

	return nil
}

func (q *QueryClient) Close() error {
	return q.Client.Close()
}

func (q *QueryClient) ExecutePreconfiguredQuery(ctx context.Context, query *kusto.ConfigurableQuery, outputChannel chan *table.Row) (*kusto.QueryResult, error) {
	return q.Client.ExecutePreconfiguredQuery(ctx, query, outputChannel)
}
