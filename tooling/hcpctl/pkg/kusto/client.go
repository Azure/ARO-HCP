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

	"github.com/Azure/azure-kusto-go/azkustodata"
	azkquery "github.com/Azure/azure-kusto-go/azkustodata/query"
)

type KustoClient interface {
	ExecutePreconfiguredQuery(ctx context.Context, query *ConfigurableQuery, outputChannel chan<- azkquery.Row) (*QueryResult, error)
	Close() error
}

// Client represents an Azure Data Explorer client for executing queries
type Client struct {
	ClusterName  string
	QueryTimeout time.Duration
	kustoClient  *azkustodata.Client
}

var _ KustoClient = &Client{}

// QueryResult represents the result of a Kusto query execution
type QueryResult struct {
	Columns    azkquery.Columns
	QueryStats QueryStats
}

// QueryStats represents statistics about the query execution
type QueryStats struct {
	ExecutionTime time.Duration
	TotalRows     int
	DataSize      int64
}

func KustoEndpoint(clusterName, region string) (*url.URL, error) {
	url, err := url.Parse(fmt.Sprintf("https://%s.%s.kusto.windows.net", clusterName, region))
	if err != nil {
		return nil, fmt.Errorf("failed to parse Kusto endpoint URL: %w", err)
	}
	return url, nil
}

// NewClient creates a new Azure Data Explorer client
func NewClient(endpoint *url.URL, queryTimeout time.Duration) (*Client, error) {
	if endpoint == nil {
		return nil, fmt.Errorf("cluster endpoint is required")
	}

	// Create connection string builder
	kcsb := azkustodata.NewConnectionStringBuilder(endpoint.String())

	// Use Azure default credential chain for authentication
	kcsb = kcsb.WithDefaultAzureCredential()

	// Create Kusto client with authentication
	kustoClient, err := azkustodata.New(kcsb)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kusto client: %w", err)
	}

	return &Client{
		kustoClient:  kustoClient,
		QueryTimeout: queryTimeout,
	}, nil
}

// ExecutePreconfiguredQuery executes a KQL query against the Azure Data Explorer cluster
func (c *Client) ExecutePreconfiguredQuery(ctx context.Context, query *ConfigurableQuery, outputChannel chan<- azkquery.Row) (*QueryResult, error) {
	queryCtx, cancel := context.WithTimeout(ctx, c.QueryTimeout)
	defer cancel()

	logger := logr.FromContextOrDiscard(ctx)

	logger.V(1).Info("Executing query on database", "queryName", query.Name, "database", query.Database)

	logger.V(2).Info("Query", "query", query.Query.String())
	logger.V(2).Info("Parameters", "parameters", query.Parameters.ToParameterCollection())

	dataset, err := c.kustoClient.IterativeQuery(queryCtx, query.Database, query.Query, azkustodata.QueryParameters(query.Parameters))
	if err != nil {
		return nil, fmt.Errorf("failed to execute query: %w", err)
	}

	// Process results
	var columns azkquery.Columns
	var totalRows int
	var dataSize int64
	startTime := time.Now()

	// Process the first table (primary result)
	primaryResult := <-dataset.Tables()

	err = primaryResult.Err()
	if err != nil {
		return nil, fmt.Errorf("failed to get primary result: %w", err)
	}

	if primaryResult.Table() == nil {
		return nil, fmt.Errorf("primary result is nil")
	}

	columsSet := false
	for row := range primaryResult.Table().Rows() {
		row := row.Row()
		if row == nil {
			if query.Unlimited {
				logger.Error(fmt.Errorf("query is unlimited and result is nil, most likely a serverside error occured. Try rerunning the query with limits"), "error while getting result")
			}
			continue
		}
		if !columsSet && row.Columns() != nil {
			columns = row.Columns()
			columsSet = true
		}
		outputChannel <- row
		totalRows++
		dataSize += int64(len(fmt.Sprintf("%v", row)))
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
