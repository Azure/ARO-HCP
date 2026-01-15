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

package kusto

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/go-logr/logr"

	"github.com/Azure/azure-kusto-go/kusto"
	kustoErrors "github.com/Azure/azure-kusto-go/kusto/data/errors"
	"github.com/Azure/azure-kusto-go/kusto/data/table"
)

type KustoClient interface {
	ExecutePreconfiguredQuery(ctx context.Context, query *ConfigurableQuery, outputChannel chan<- *table.Row) (*QueryResult, error)
	Close() error
}

// Client represents an Azure Data Explorer client for executing queries
type Client struct {
	ClusterName  string
	Endpoint     string
	QueryTimeout time.Duration
	kustoClient  *kusto.Client
}

var _ KustoClient = &Client{}

// QueryResult represents the result of a Kusto query execution
type QueryResult struct {
	Columns    []Column
	QueryStats QueryStats
}

// Column represents a column in the query result
type Column struct {
	Name string
	Type string
}

// QueryStats represents statistics about the query execution
type QueryStats struct {
	ExecutionTime time.Duration
	TotalRows     int
	DataSize      int64
}

func KustoEndpoint(clusterName, region string) (string, error) {
	url, err := url.Parse(fmt.Sprintf("https://%s.%s.kusto.windows.net", clusterName, region))
	if err != nil {
		return "", fmt.Errorf("failed to parse Kusto endpoint URL: %w", err)
	}
	return url.String(), nil
}

// NewClient creates a new Azure Data Explorer client
func NewClient(endpoint string, queryTimeout time.Duration) (*Client, error) {
	if endpoint == "" {
		return nil, fmt.Errorf("cluster endpoint is required")
	}

	// Create connection string builder
	kcsb := kusto.NewConnectionStringBuilder(endpoint)

	// Use Azure default credential chain for authentication
	kcsb = kcsb.WithDefaultAzureCredential()

	// Create Kusto client with authentication
	kustoClient, err := kusto.New(kcsb)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kusto client: %w", err)
	}

	return &Client{
		Endpoint:     endpoint,
		kustoClient:  kustoClient,
		QueryTimeout: queryTimeout,
	}, nil
}

// ExecutePreconfiguredQuery executes a KQL query against the Azure Data Explorer cluster
func (c *Client) ExecutePreconfiguredQuery(ctx context.Context, query *ConfigurableQuery, outputChannel chan<- *table.Row) (*QueryResult, error) {
	queryCtx, cancel := context.WithTimeout(ctx, c.QueryTimeout)
	defer cancel()

	logger := logr.FromContextOrDiscard(ctx)

	logger.V(1).Info("Executing query on database", "queryName", query.Name, "database", query.Database)

	logger.V(2).Info("Query", "query", query.Query.String())
	logger.V(2).Info("Parameters", "parameters", query.Parameters.ToParameterCollection())

	iter, err := c.kustoClient.Query(queryCtx, query.Database, query.Query, kusto.QueryParameters(query.Parameters))
	if err != nil {
		return nil, fmt.Errorf("failed to execute query: %w", err)
	}
	defer iter.Stop()

	// Process results
	var columns []Column
	var totalRows int
	var dataSize int64
	startTime := time.Now()

	// Process rows using DoOnRowOrError
	err = iter.DoOnRowOrError(func(row *table.Row, e *kustoErrors.Error) error {
		logger.V(8).Info("Processing row", "row", row)
		if e != nil {
			return fmt.Errorf("failed to process row: %w", e)
		}

		// Extract column information from the first row
		if totalRows == 0 {
			// Get column information from the row
			colNames := row.ColumnNames()
			colTypes := row.ColumnTypes
			for i, name := range colNames {
				columns = append(columns, Column{
					Name: name,
					Type: string(colTypes[i].Type),
				})
			}
		}

		// Convert row to struct
		outputChannel <- row
		totalRows++
		dataSize += int64(len(fmt.Sprintf("%v", row.Values)))

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("error during query iteration: %w", err)
	}

	executionTime := time.Since(startTime)

	logger.V(1).Info("Query competed", "query", query.Name, "rows", totalRows, "KiloBytes", dataSize/1024, "executionTime", executionTime)

	return &QueryResult{
		Columns: columns,
		QueryStats: QueryStats{
			ExecutionTime: executionTime,
			TotalRows:     totalRows,
			DataSize:      dataSize,
		},
	}, nil
}

// Close closes the Kusto client connection
func (c *Client) Close() error {
	if c.kustoClient != nil {
		return c.kustoClient.Close()
	}
	return nil
}
