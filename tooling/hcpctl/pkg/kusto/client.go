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
	"time"

	"github.com/Azure/azure-kusto-go/kusto"
	kustoErrors "github.com/Azure/azure-kusto-go/kusto/data/errors"
	"github.com/Azure/azure-kusto-go/kusto/data/table"
	"github.com/Azure/azure-kusto-go/kusto/kql"
)

// Client represents an Azure Data Explorer client for executing queries
type Client struct {
	ClusterName string
	Endpoint    string
	kustoClient *kusto.Client
}

// QueryResult represents the result of a Kusto query execution
type QueryResult struct {
	Data       []map[string]interface{} `json:"data"`
	Columns    []Column                 `json:"columns"`
	QueryStats QueryStats               `json:"queryStats"`
}

// Column represents a column in the query result
type Column struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// QueryStats represents statistics about the query execution
type QueryStats struct {
	ExecutionTime time.Duration `json:"executionTime"`
	TotalRows     int           `json:"totalRows"`
	DataSize      int64         `json:"dataSize"`
}

// NewClient creates a new Azure Data Explorer client
func NewClient(clusterName string) (*Client, error) {
	if clusterName == "" {
		return nil, fmt.Errorf("cluster name is required")
	}

	// Construct the Kusto endpoint
	endpoint := fmt.Sprintf("https://%s.eastus.kusto.windows.net", clusterName)

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
		ClusterName: clusterName,
		Endpoint:    endpoint,
		kustoClient: kustoClient,
	}, nil
}

// ExecuteQuery executes a KQL query against the Azure Data Explorer cluster
func (c *Client) ExecuteQuery(ctx context.Context, query string, timeout time.Duration) (*QueryResult, error) {
	// Create a context with timeout
	queryCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Execute the query using kql.Builder with AddUnsafe for dynamic queries
	// This allows us to use dynamic query strings
	queryBuilder := kql.New("").AddUnsafe(query)
	iter, err := c.kustoClient.Query(queryCtx, "HCPCustomerLogs", queryBuilder)
	if err != nil {
		return nil, fmt.Errorf("failed to execute query: %w", err)
	}
	defer iter.Stop()

	// Process results
	var data []map[string]any
	var columns []Column
	var totalRows int
	var dataSize int64
	startTime := time.Now()

	// Process rows using DoOnRowOrError
	err = iter.DoOnRowOrError(func(row *table.Row, e *kustoErrors.Error) error {
		if e != nil {
			return e
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

		// Convert row to map
		rowData := make(map[string]interface{})
		colNames := row.ColumnNames()
		for i, value := range row.Values {
			if i < len(colNames) {
				rowData[colNames[i]] = value
			}
		}
		data = append(data, rowData)
		totalRows++
		dataSize += int64(len(fmt.Sprintf("%v", row.Values)))

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("error during query iteration: %w", err)
	}

	executionTime := time.Since(startTime)

	return &QueryResult{
		Data:    data,
		Columns: columns,
		QueryStats: QueryStats{
			ExecutionTime: executionTime,
			TotalRows:     totalRows,
			DataSize:      dataSize,
		},
	}, nil
}

// GetPreconfiguredQueries returns a list of preconfigured diagnostic queries
func GetPreconfiguredQueries() []string {
	return []string{
		"database('HCPCustomerLogs').table('containerLogs')",
	}
}

// Close closes the Kusto client connection
func (c *Client) Close() error {
	if c.kustoClient != nil {
		return c.kustoClient.Close()
	}
	return nil
}
