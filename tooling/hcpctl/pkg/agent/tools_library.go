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

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/snapshot"
)

// maxLibraryResultRows is the number of result rows run_query returns to the
// agent; the complete result is written to the run's output file.
const maxLibraryResultRows = 50

// LibraryQueryExecutor adapts a KustoClient to execute library queries.
func LibraryQueryExecutor(client KustoClient) snapshot.QueryExecutor {
	return func(ctx context.Context, kql string) ([]string, [][]string, error) {
		table, err := client.Query(ctx, kql)
		if err != nil {
			return nil, nil, errors.New(summarizeKustoError(err))
		}
		return table.Columns, table.Rows, nil
	}
}

// NewQueryLibraryToolDefinitions creates the tools that let the agent find,
// inspect, and run the query library's queries through a session that
// remembers parameter values across calls:
//
//   - list_queries: find queries, with what each needs and whether it can run now
//   - describe_query: a query's README, parameters, provided values, and preview
//   - describe_param: a parameter's meaning, current value, and its providers and users
//   - run_query: render, execute, and record a query
func NewQueryLibraryToolDefinitions(session *snapshot.LibrarySession) []ToolDefinition {
	return []ToolDefinition{
		{
			Name: "list_queries",
			Description: "List the query library's queries: name, database, one-line description, the parameters each needs, " +
				"and whether the parameters known so far suffice to run it. Optionally filter by a substring of the name, tags, or description " +
				"(for example a component such as \"maestro\" or a topic such as \"conditions\").",
			ParamSchema: json.RawMessage(`{
	"type": "object",
	"properties": {
		"filter": {"type": "string", "description": "Optional case-insensitive substring of the query name, tags, or description."}
	}
}`),
			Handler: func(_ context.Context, raw json.RawMessage) (string, error) {
				var p struct {
					Filter string `json:"filter"`
				}
				if err := unmarshalToolParams(raw, &p); err != nil {
					return "", fmt.Errorf("invalid list_queries params: %w", err)
				}
				return session.ListMarkdown(p.Filter), nil
			},
		},
		{
			Name: "describe_query",
			Description: "Describe one library query: its README (what it shows, what to look for, where to go next), every parameter with its current value " +
				"or how to obtain it (including which queries provide it), the values its results provide to other queries, and the KQL it would run now.",
			ParamSchema: json.RawMessage(`{
	"type": "object",
	"properties": {
		"name": {"type": "string", "description": "Query name, <component>/<queryName>, as listed by list_queries."}
	},
	"required": ["name"]
}`),
			Handler: func(_ context.Context, raw json.RawMessage) (string, error) {
				var p struct {
					Name string `json:"name"`
				}
				if err := unmarshalToolParams(raw, &p); err != nil {
					return "", fmt.Errorf("invalid describe_query params: %w", err)
				}
				return session.DescribeQueryMarkdown(p.Name)
			},
		},
		{
			Name: "describe_param",
			Description: "Describe one query parameter (for example ResourceID, ClusterID, or PhaseStartTime): what it is, its current value and where that came from, " +
				"how it is derived or defaulted, which queries provide it and which use it, and the guide that explains it.",
			ParamSchema: json.RawMessage(`{
	"type": "object",
	"properties": {
		"name": {"type": "string", "description": "Parameter name."}
	},
	"required": ["name"]
}`),
			Handler: func(_ context.Context, raw json.RawMessage) (string, error) {
				var p struct {
					Name string `json:"name"`
				}
				if err := unmarshalToolParams(raw, &p); err != nil {
					return "", fmt.Errorf("invalid describe_param params: %w", err)
				}
				return session.DescribeParamMarkdown(p.Name)
			},
		},
		{
			Name: "run_query",
			Description: "Render and execute a library query. Parameters not passed are filled from earlier arguments, values provided by earlier queries, " +
				"derivation (for example SubscriptionID and ClusterResourceID from ResourceID), and defaults; the Kusto cluster and databases are configured. " +
				"Arguments are remembered for later queries. When the result identifies a value other queries need (for example clustersService/cid provides ClusterID), " +
				"it is remembered too. If a parameter is missing, the error says which query provides it. Returns the rendered KQL, the first rows, " +
				"and the path of a file holding the README, query, and full results.",
			ParamSchema: json.RawMessage(`{
	"type": "object",
	"properties": {
		"name": {"type": "string", "description": "Query name, <component>/<queryName>, as listed by list_queries."},
		"params": {
			"type": "object",
			"description": "Parameter values by name, for example {\"ResourceID\": \"/subscriptions/...\", \"FullStartTime\": \"2025-01-02T15:04:05Z\"}. Datetimes use RFC3339.",
			"additionalProperties": {"type": "string"}
		}
	},
	"required": ["name"]
}`),
			Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
				var p struct {
					Name   string         `json:"name"`
					Params map[string]any `json:"params"`
				}
				if err := unmarshalToolParams(raw, &p); err != nil {
					return "", fmt.Errorf("invalid run_query params: %w", err)
				}
				args := make(map[string]string, len(p.Params))
				for k, v := range p.Params {
					if v == nil {
						continue
					}
					args[k] = fmt.Sprint(v)
				}
				result, err := session.Run(ctx, p.Name, args)
				if err != nil {
					return "", err
				}
				return result.Markdown(maxLibraryResultRows), nil
			},
		},
	}
}

// unmarshalToolParams decodes tool parameters, treating an empty payload as
// an empty object.
func unmarshalToolParams(raw json.RawMessage, v any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return json.Unmarshal(raw, v)
}
