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
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"golang.org/x/sync/errgroup"

	azkquery "github.com/Azure/azure-kusto-go/azkustodata/query"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
)

// QueryClientInterface defines the interface for querying data
type QueryClientInterface interface {
	ConcurrentQueries(ctx context.Context, queries []*kusto.ConfigurableQuery, outputChannel chan<- azkquery.Row) error
	Close() error
	ExecutePreconfiguredQuery(ctx context.Context, query *kusto.ConfigurableQuery, outputChannel chan<- azkquery.Row) (*kusto.QueryResult, error)
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

func (q *QueryClient) ConcurrentQueries(ctx context.Context, queries []*kusto.ConfigurableQuery, outputChannel chan<- azkquery.Row) error {
	logger := logr.FromContextOrDiscard(ctx)

	queryGroup, queryCtx := errgroup.WithContext(ctx)
	for _, query := range queries {
		queryGroup.Go(func() error {
			result, err := q.Client.ExecutePreconfiguredQuery(queryCtx, query, outputChannel)
			if err != nil {
				logger.Error(err, "Query failed", "name", query.Name)
				return fmt.Errorf("failed to execute query: %w", err)
			}
			if q.FileWriter != nil {
				err = q.FileWriter.WriteFile(q.OutputPath, fmt.Sprintf("%s.json", query.Name), result)
				if err != nil {
					return fmt.Errorf("failed to write query result to file: %w", err)
				}
			}
			return nil
		})
	}

	return queryGroup.Wait()
}

func (q *QueryClient) Close() error {
	return q.Client.Close()
}

func (q *QueryClient) ExecutePreconfiguredQuery(ctx context.Context, query *kusto.ConfigurableQuery, outputChannel chan<- azkquery.Row) (*kusto.QueryResult, error) {
	return q.Client.ExecutePreconfiguredQuery(ctx, query, outputChannel)
}
