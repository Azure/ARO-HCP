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
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/go-logr/logr"

	"github.com/Azure/azure-kusto-go/azkustodata"
	azkquery "github.com/Azure/azure-kusto-go/azkustodata/query"
	queryv2 "github.com/Azure/azure-kusto-go/azkustodata/query/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

const (
	queryPageDuration  = 5 * time.Minute
	kustoTimePrecision = 100 * time.Nanosecond
)

type KustoClient interface {
	ExecutePreconfiguredQuery(ctx context.Context, query Query, outputChannel chan<- TaggedRow) (*QueryResult, error)
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

type TaggedRow struct {
	Row       azkquery.Row
	QueryName string
	QueryType QueryType
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

	// Use Azure default credential chain for authentication, which respects AZURE_CONFIG_DIR
	cred, err := azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{
		AdditionallyAllowedTenants:   []string{"*"},
		RequireAzureTokenCredentials: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure credential: %w", err)
	}
	kcsb = kcsb.WithTokenCredential(cred)

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
func (c *Client) ExecutePreconfiguredQuery(ctx context.Context, query Query, outputChannel chan<- TaggedRow) (*QueryResult, error) {
	if timeRangeQuery, ok := query.(TimeRangeQuery); ok && query.IsUnlimited() && timeRangeQuery.IsPageable() {
		return c.executeTimeRangeQuery(ctx, timeRangeQuery, outputChannel)
	}

	return c.executeQuery(ctx, query, outputChannel, true)
}

func (c *Client) executeQuery(ctx context.Context, query Query, outputChannel chan<- TaggedRow, logCompletion bool) (*QueryResult, error) {
	queryCtx, cancel := context.WithTimeout(ctx, c.QueryTimeout)
	defer cancel()
	startTime := time.Now()

	logger := logr.FromContextOrDiscard(ctx)

	logger.V(1).Info("Executing query on database", "queryName", query.GetName(), "database", query.GetDatabase())

	logger.V(2).Info("Query", "query", query.GetQuery().String())

	var queryOptions []azkustodata.QueryOption
	if query.IsUnlimited() {
		// Set notruncation as a client request property in addition to any KQL
		// statement emitted by the query template. This applies the setting to
		// every unlimited query and allows IterativeQuery to stream results larger
		// than Kusto's default result limits.
		queryOptions = append(queryOptions, azkustodata.NoTruncation())
	}

	dataset, err := c.kustoClient.IterativeQuery(queryCtx, query.GetDatabase(), query.GetQuery(), queryOptions...)
	if err != nil {
		return nil, fmt.Errorf("failed to execute query: %w", err)
	}
	defer dataset.Close()

	// Process results
	var columns azkquery.Columns
	var totalRows int
	var dataSize int64
	// Process the first table (primary result)
	logger.V(6).Info("Processing primary result")
	primaryResult, ok := <-dataset.Tables()
	if !ok {
		return nil, fmt.Errorf("query result contained no tables")
	}

	err = primaryResult.Err()
	if err != nil {
		return nil, fmt.Errorf("failed to get primary result: %w", err)
	}

	if primaryResult.Table() == nil {
		return nil, fmt.Errorf("primary result is nil")
	}

	columnsSet := false
	for rowResult := range primaryResult.Table().Rows() {
		logger.V(8).Info("Processing row", "rowNumber", totalRows)
		if err := rowResult.Err(); err != nil {
			return nil, fmt.Errorf("failed while streaming query results: %w", err)
		}

		row := rowResult.Row()
		if row == nil {
			return nil, fmt.Errorf("query result contained a nil row")
		}
		if !columnsSet && row.Columns() != nil {
			columns = row.Columns()
			columnsSet = true
		}
		select {
		case <-queryCtx.Done():
			return nil, queryCtx.Err()
		case outputChannel <- TaggedRow{Row: row, QueryName: query.GetName(), QueryType: query.GetQueryType()}:
		}
		totalRows++
		dataSize += int64(len(fmt.Sprintf("%v", row)))
	}

	executionTime := time.Since(startTime)

	if logCompletion {
		logger.Info("Query completed", "query", query.GetName(), "rows", totalRows, "KiloBytes", dataSize/1024, "executionTime", executionTime)
	}

	return &QueryResult{
		Columns: columns,
		QueryStats: QueryStats{
			ExecutionTime: executionTime,
			TotalRows:     totalRows,
			DataSize:      dataSize,
		},
	}, nil
}

type queryTimeRange struct {
	start time.Time
	end   time.Time
}

func (c *Client) executeTimeRangeQuery(ctx context.Context, query TimeRangeQuery, outputChannel chan<- TaggedRow) (*QueryResult, error) {
	logger := logr.FromContextOrDiscard(ctx)
	start, end := query.GetTimeRange()
	if start.IsZero() || end.IsZero() || end.Before(start) {
		return nil, fmt.Errorf("query %q has an invalid timestamp range", query.GetName())
	}

	ranges := splitTimeRange(start, end, queryPageDuration)
	if query.GetOrderBy() == OrderByDesc {
		for i, j := 0, len(ranges)-1; i < j; i, j = i+1, j-1 {
			ranges[i], ranges[j] = ranges[j], ranges[i]
		}
	}

	combined := &QueryResult{}
	for _, queryRange := range ranges {
		result, err := c.executeTimeRangePage(ctx, query, queryRange, outputChannel)
		if err != nil {
			return nil, err
		}
		mergeQueryResult(combined, result)
	}
	logger.Info("Query completed", "query", query.GetName(), "rows", combined.QueryStats.TotalRows, "KiloBytes", combined.QueryStats.DataSize/1024, "executionTime", combined.QueryStats.ExecutionTime)
	return combined, nil
}

func (c *Client) executeTimeRangePage(ctx context.Context, query TimeRangeQuery, queryRange queryTimeRange, outputChannel chan<- TaggedRow) (*QueryResult, error) {
	pageQuery, err := query.WithTimeRange(queryRange.start, queryRange.end)
	if err != nil {
		return nil, err
	}

	result, rows, err := c.executeBufferedQuery(ctx, pageQuery)
	if err == nil {
		for _, row := range rows {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case outputChannel <- row:
			}
		}
		return result, nil
	}
	if !isLimitsExceeded(err) {
		return nil, err
	}

	left, right, ok := bisectTimeRange(queryRange)
	if !ok {
		return nil, fmt.Errorf("query %q exceeded Kusto limits for the smallest timestamp range %s through %s: %w", query.GetName(), queryRange.start, queryRange.end, err)
	}

	orderedRanges := []queryTimeRange{left, right}
	if query.GetOrderBy() == OrderByDesc {
		orderedRanges[0], orderedRanges[1] = orderedRanges[1], orderedRanges[0]
	}

	combined := &QueryResult{}
	for _, childRange := range orderedRanges {
		childResult, childErr := c.executeTimeRangePage(ctx, query, childRange, outputChannel)
		if childErr != nil {
			return nil, childErr
		}
		mergeQueryResult(combined, childResult)
	}
	return combined, nil
}

func (c *Client) executeBufferedQuery(ctx context.Context, query Query) (*QueryResult, []TaggedRow, error) {
	rowChannel := make(chan TaggedRow)
	done := make(chan struct{})
	var rows []TaggedRow
	go func() {
		defer close(done)
		for row := range rowChannel {
			rows = append(rows, row)
		}
	}()

	result, err := c.executeQuery(ctx, query, rowChannel, false)
	close(rowChannel)
	<-done
	return result, rows, err
}

func splitTimeRange(start, end time.Time, pageDuration time.Duration) []queryTimeRange {
	var ranges []queryTimeRange
	for pageStart := start; !pageStart.After(end); {
		pageEnd := pageStart.Add(pageDuration - kustoTimePrecision)
		if pageEnd.After(end) {
			pageEnd = end
		}
		ranges = append(ranges, queryTimeRange{start: pageStart, end: pageEnd})
		if pageEnd.Equal(end) {
			break
		}
		pageStart = pageEnd.Add(kustoTimePrecision)
	}
	return ranges
}

func bisectTimeRange(queryRange queryTimeRange) (queryTimeRange, queryTimeRange, bool) {
	if queryRange.end.Sub(queryRange.start) < kustoTimePrecision {
		return queryTimeRange{}, queryTimeRange{}, false
	}

	leftEnd := queryRange.start.Add(queryRange.end.Sub(queryRange.start) / 2).Truncate(kustoTimePrecision)
	rightStart := leftEnd.Add(kustoTimePrecision)
	if rightStart.After(queryRange.end) {
		return queryTimeRange{}, queryTimeRange{}, false
	}
	return queryTimeRange{start: queryRange.start, end: leftEnd}, queryTimeRange{start: rightStart, end: queryRange.end}, true
}

func isLimitsExceeded(err error) bool {
	var oneAPIError *queryv2.OneApiError
	return errors.As(err, &oneAPIError) && oneAPIError.ErrorMessage.Code == "LimitsExceeded"
}

func mergeQueryResult(destination, source *QueryResult) {
	if len(destination.Columns) == 0 {
		destination.Columns = source.Columns
	}
	destination.QueryStats.ExecutionTime += source.QueryStats.ExecutionTime
	destination.QueryStats.TotalRows += source.QueryStats.TotalRows
	destination.QueryStats.DataSize += source.QueryStats.DataSize
}

// Close closes the Kusto client connection
func (c *Client) Close() error {
	if c.kustoClient != nil {
		return c.kustoClient.Close()
	}
	return nil
}
