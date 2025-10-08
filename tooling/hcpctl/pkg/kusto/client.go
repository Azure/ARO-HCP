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
	"reflect"
	"time"

	"github.com/Azure/azure-kusto-go/kusto"
	kustoErrors "github.com/Azure/azure-kusto-go/kusto/data/errors"
	"github.com/Azure/azure-kusto-go/kusto/data/table"
)

type KustoClient interface {
	ExecutePreconfiguredQuery(ctx context.Context, query *ConfigurableQuery, outputChannel chan<- any, rowType any, timeout time.Duration) (*QueryResult, error)
	Close() error
}

// Client represents an Azure Data Explorer client for executing queries
type Client struct {
	Debug       bool
	ClusterName string
	Endpoint    string
	kustoClient *kusto.Client
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

// NewClient creates a new Azure Data Explorer client
func NewClient(endpoint string, debug bool) (*Client, error) {
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
		Debug:       debug,
		Endpoint:    endpoint,
		kustoClient: kustoClient,
	}, nil
}

// ExecutePreconfiguredQuery executes a KQL query against the Azure Data Explorer cluster
func (c *Client) ExecutePreconfiguredQuery(ctx context.Context, query *ConfigurableQuery, outputChannel chan<- any, rowType any, timeout time.Duration) (*QueryResult, error) {
	queryCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	fmt.Printf("Executing query: %s on database %s\n", query.Name, query.Database)

	if c.Debug {
		fmt.Printf("Query: %s\n", query.Query.String())
		fmt.Printf("Parameters: %v\n", query.Parameters.ToParameterCollection())
	}
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
		rowData := reflect.New(reflect.TypeOf(rowType)).Interface()
		err := row.ToStruct(rowData)
		if err != nil {
			return fmt.Errorf("failed to convert row to struct: %w", err)
		}
		outputChannel <- rowData
		totalRows++
		dataSize += int64(len(fmt.Sprintf("%v", row.Values)))

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("error during query iteration: %w", err)
	}

	executionTime := time.Since(startTime)

	fmt.Printf("Query '%s' completed: %d rows with %d KB in %v\n", query.Name, totalRows, dataSize/1024, executionTime)

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
