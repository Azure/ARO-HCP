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

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	copilot "github.com/github/copilot-sdk/go"

	"github.com/Azure/azure-kusto-go/azkustodata"
	kustoerrors "github.com/Azure/azure-kusto-go/azkustodata/errors"
	"github.com/Azure/azure-kusto-go/azkustodata/kql"
	"github.com/Azure/azure-kusto-go/azkustodata/query"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/internal/tabular"
)

// KustoClient is the interface for executing KQL queries against Azure Data Explorer.
type KustoClient interface {
	// Query executes a KQL query and returns the result as a tabular.Table.
	Query(ctx context.Context, kql string) (*tabular.Table, error)
}

// kustoQueryParams is the typed parameter struct for the kusto_query tool.
type kustoQueryParams struct {
	KQL string `json:"kql" jsonschema:"required" jsonschema_description:"The KQL query to execute."`
}

// maxToolResultRows is the maximum number of rows returned to the agent in a
// tool call result. The full table is still cached (and used for hydration /
// persistence); only the markdown rendered for the conversation is truncated.
const maxToolResultRows = 100

// kustoToolDescription is the shared description for the kusto_query tool,
// used by both the Copilot-specific and provider-neutral factories.
const kustoToolDescription = `Execute a KQL query against Azure Data Explorer. Use this when the pre-gathered diagnostic data is insufficient and you need to investigate further. The query runs against the ARO-HCP logging cluster. Results are returned as a markdown table. Write queries that are self-contained and tell a story — they will be rendered verbatim in the final analysis.`

// kustoToolParamSchema is the JSON Schema for the kusto_query tool's
// input parameters, shared between all providers.
var kustoToolParamSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"kql": {
			"type": "string",
			"description": "The KQL query to execute."
		}
	},
	"required": ["kql"]
}`)

// NewKustoToolDefinition creates a provider-neutral ToolDefinition for the
// kusto_query tool. This is the preferred factory for use with LLMProvider
// implementations; each provider converts it to its native tool format.
func NewKustoToolDefinition(client KustoClient) ToolDefinition {
	return ToolDefinition{
		Name:        "kusto_query",
		Description: kustoToolDescription,
		ParamSchema: kustoToolParamSchema,
		Handler: func(ctx context.Context, params json.RawMessage) (string, error) {
			var p kustoQueryParams
			if err := json.Unmarshal(params, &p); err != nil {
				return "", fmt.Errorf("invalid kusto_query params: %w", err)
			}
			if p.KQL == "" {
				return "", fmt.Errorf("kql must not be empty")
			}

			table, err := client.Query(ctx, p.KQL)
			if err != nil {
				return "", fmt.Errorf("%s", summarizeKustoError(err))
			}

			totalRows := len(table.Rows)
			if totalRows > maxToolResultRows {
				truncated := &tabular.Table{
					Columns: table.Columns,
					Rows:    table.Rows[:maxToolResultRows],
				}
				return fmt.Sprintf("%s\n\n(showing %d of %d rows — add filters or aggregations to narrow results)",
					TableToMarkdown(truncated), maxToolResultRows, totalRows), nil
			}

			return TableToMarkdown(table), nil
		},
	}
}

// NewKustoTool creates a kusto_query Copilot SDK tool backed by the given client.
func NewKustoTool(client KustoClient) copilot.Tool {
	return copilot.DefineTool(
		"kusto_query",
		`Execute a KQL query against Azure Data Explorer. Use this when the pre-gathered diagnostic data is insufficient and you need to investigate further. The query runs against the ARO-HCP logging cluster. Results are returned as a markdown table. Write queries that are self-contained and tell a story — they will be rendered verbatim in the final analysis.`,
		func(params kustoQueryParams, inv copilot.ToolInvocation) (string, error) {
			if params.KQL == "" {
				return "", fmt.Errorf("kql must not be empty")
			}

			table, err := client.Query(inv.TraceContext, params.KQL)
			if err != nil {
				return "", fmt.Errorf("%s", summarizeKustoError(err))
			}

			totalRows := len(table.Rows)
			if totalRows > maxToolResultRows {
				truncated := &tabular.Table{
					Columns: table.Columns,
					Rows:    table.Rows[:maxToolResultRows],
				}
				return fmt.Sprintf("%s\n\n(showing %d of %d rows — add filters or aggregations to narrow results)",
					TableToMarkdown(truncated), maxToolResultRows, totalRows), nil
			}

			return TableToMarkdown(table), nil
		},
	)
}

// ADXKustoClient wraps an Azure Data Explorer client to implement KustoClient.
// It executes queries against a specific database and formats results as markdown.
type ADXKustoClient struct {
	client   *azkustodata.Client
	database string
}

// NewADXKustoClient creates a KustoClient that queries a specific ADX cluster and database.
// The client is created using the provided credential and cluster URI.
// The caller is responsible for calling Close when done.
func NewADXKustoClient(credential azcore.TokenCredential, clusterURI, database string) (*ADXKustoClient, error) {
	kcsb := azkustodata.NewConnectionStringBuilder(clusterURI).
		WithTokenCredential(credential)
	client, err := azkustodata.New(kcsb)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kusto client for %q: %w", clusterURI, err)
	}
	return &ADXKustoClient{
		client:   client,
		database: database,
	}, nil
}

// Close releases the underlying Kusto client resources.
func (c *ADXKustoClient) Close() error {
	return c.client.Close()
}

// CachingKustoClient wraps a KustoClient and caches successful query results
// in memory, keyed by the KQL query string. This avoids re-running identical
// queries across validation and hydration rounds. Only successful results are
// cached; errors are always retried against the underlying client.
type CachingKustoClient struct {
	delegate KustoClient
	cache    map[string]*tabular.Table
}

// NewCachingKustoClient wraps the given client with an in-memory query cache.
func NewCachingKustoClient(delegate KustoClient) *CachingKustoClient {
	return &CachingKustoClient{
		delegate: delegate,
		cache:    make(map[string]*tabular.Table),
	}
}

// Query returns a cached result for the given KQL if available, otherwise
// delegates to the underlying client and caches a successful result.
func (c *CachingKustoClient) Query(ctx context.Context, kqlQuery string) (*tabular.Table, error) {
	if table, ok := c.cache[kqlQuery]; ok {
		return table, nil
	}
	table, err := c.delegate.Query(ctx, kqlQuery)
	if err != nil {
		return nil, err
	}
	c.cache[kqlQuery] = table
	return table, nil
}

// Query executes a KQL query and returns the result as a tabular.Table.
func (c *ADXKustoClient) Query(ctx context.Context, kqlQuery string) (*tabular.Table, error) {
	dataset, err := c.client.IterativeQuery(ctx, c.database, kql.New("").AddUnsafe(kqlQuery))
	if err != nil {
		return nil, fmt.Errorf("query failed: %w", err)
	}

	return datasetToTable(dataset)
}

// datasetToTable converts a Kusto iterative dataset to a tabular.Table,
// preserving the column order returned by the Kusto API.
func datasetToTable(dataset query.IterativeDataset) (*tabular.Table, error) {
	t := &tabular.Table{}

	for tableResult := range dataset.Tables() {
		table := tableResult.Table()
		if table == nil {
			if tableResult.Err() != nil {
				return nil, tableResult.Err()
			}
			continue
		}

		// Skip non-primary tables (metadata, query stats, etc.)
		if !table.IsPrimaryResult() {
			for range table.Rows() {
			}
			continue
		}

		columns := table.Columns()
		if t.Columns == nil {
			t.Columns = make([]string, len(columns))
			for i, col := range columns {
				t.Columns[i] = col.Name()
			}
		}

		for rowResult := range table.Rows() {
			if rowResult.Err() != nil {
				return t, rowResult.Err()
			}
			row := rowResult.Row()

			cells := make([]string, len(columns))
			for i := range columns {
				val, err := row.Value(i)
				if err != nil {
					cells[i] = ""
				} else {
					cells[i] = fmt.Sprintf("%v", val)
				}
			}
			t.Rows = append(t.Rows, cells)
		}
	}

	return t, nil
}

// TableToMarkdown renders a tabular.Table as a markdown table.
// If the table is nil or has no columns, the string "(no results)" is returned.
func TableToMarkdown(t *tabular.Table) string {
	if t == nil || len(t.Columns) == 0 {
		return "(no results)"
	}

	var sb strings.Builder

	// Header.
	sb.WriteString("| ")
	for i, col := range t.Columns {
		if i > 0 {
			sb.WriteString(" | ")
		}
		sb.WriteString(col)
	}
	sb.WriteString(" |\n")

	// Separator.
	sb.WriteString("| ")
	for i := range t.Columns {
		if i > 0 {
			sb.WriteString(" | ")
		}
		sb.WriteString("---")
	}
	sb.WriteString(" |\n")

	// Rows.
	for _, row := range t.Rows {
		sb.WriteString("| ")
		for i := range t.Columns {
			if i > 0 {
				sb.WriteString(" | ")
			}
			if i < len(row) {
				sb.WriteString(row[i])
			}
		}
		sb.WriteString(" |\n")
	}

	return sb.String()
}

// summarizeKustoError extracts a concise error message from a Kusto SDK error.
func summarizeKustoError(err error) string {
	var kustoErr *kustoerrors.Error
	if errors.As(err, &kustoErr) {
		if rest := kustoErr.UnmarshalREST(); rest != nil {
			if errObj, ok := rest["error"].(map[string]interface{}); ok {
				code, _ := errObj["code"].(string)
				msg, _ := errObj["message"].(string)
				atMsg, _ := errObj["@message"].(string)
				if code != "" {
					summary := fmt.Sprintf("query failed: %s: %s", code, msg)
					if atMsg != "" && atMsg != msg {
						summary += " — " + atMsg
					}
					return summary
				}
			}
		}
	}
	// Fallback: return the raw error but truncated to avoid context bloat.
	s := err.Error()
	const maxLen = 512
	if len(s) > maxLen {
		s = s[:maxLen] + "... (truncated)"
	}
	return s
}
