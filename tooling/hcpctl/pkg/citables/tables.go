// Copyright 2026 Microsoft Corporation
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

// Package citables declares the CI telemetry tables this repository queries so
// every consumer validates the same deployed schema instead of keeping a copy.
package citables

import (
	"context"
	"fmt"
	"sort"

	"github.com/Azure/azure-kusto-go/azkustodata"
	"github.com/Azure/azure-kusto-go/azkustodata/kql"
	"github.com/Azure/azure-kusto-go/azkustodata/query"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
)

// CI table names, written once so a consumer cannot drift from the deployed set.
const (
	JobOutcomes   = "ciJobOutcomes"
	ProcessedJobs = "ciProcessedJobs"
	TestResults   = "ciTestResults"
	TestNames     = "ciTestNames"
)

// SchemaQuery reads the declared type of every column in the CI tables.
const SchemaQuery = `union
(` + TestResults + ` | getschema | extend TableName="` + TestResults + `"),
(` + TestNames + ` | getschema | extend TableName="` + TestNames + `"),
(` + JobOutcomes + ` | getschema | extend TableName="` + JobOutcomes + `"),
(` + ProcessedJobs + ` | getschema | extend TableName="` + ProcessedJobs + `")
| project TableName, ColumnName, ColumnType`

// Column is one declared column returned by SchemaQuery.
type Column struct {
	TableName  string
	ColumnName string
	ColumnType string
}

// Required is the union of the columns consumers may rely on, so one check
// covers every query in this repository. A column that is absent or typed
// differently is a schema prerequisite failure, never empty history.
var Required = map[string]map[string]string{
	TestResults: {
		"buildId": "string", "testId": "string", "source": "string", "result": "string", "failed": "bool",
		"message": "string", "startedAt": "datetime", "finishedAt": "datetime",
		"setupFinishTime": "datetime", "testStartTime": "datetime", "cleanupStartTime": "datetime",
	},
	TestNames: {"testId": "string", "name": "string"},
	JobOutcomes: {
		"buildId": "string", "jobName": "string", "prowUrl": "string", "overallResult": "string",
		"failed": "bool", "startedAt": "datetime", "finishedAt": "datetime", "runtimeRegion": "string",
	},
	ProcessedJobs: {"buildId": "string"},
}

// QueryClient must return only complete datasets, including late failures.
// kusto.Client implements this contract; bare SDK v1.2.3 clients require a
// kusto.CompleteTransport because materialized SDK Query alone is insufficient.
type QueryClient interface {
	Query(context.Context, string, azkustodata.Statement, ...azkustodata.QueryOption) (query.Dataset, error)
}

// Ensure reads the declared schema and reports every required column that is
// absent or typed differently, so callers fail before querying data.
func Ensure(ctx context.Context, client QueryClient, database string, options ...azkustodata.QueryOption) error {
	dataset, err := client.Query(ctx, database, kql.New(SchemaQuery), options...)
	if err != nil {
		return fmt.Errorf("inspect CI schema: %w", err)
	}
	columns, err := kusto.DecodePrimary[Column](dataset)
	if err != nil {
		return fmt.Errorf("decode CI schema: %w", err)
	}
	return Validate(columns)
}

// Validate checks declared columns against every required table and column.
func Validate(columns []Column) error {
	actual := make(map[string]map[string]string, len(Required))
	for _, column := range columns {
		if actual[column.TableName] == nil {
			actual[column.TableName] = map[string]string{}
		}
		actual[column.TableName][column.ColumnName] = column.ColumnType
	}
	var missing []string
	for table, required := range Required {
		for column, columnType := range required {
			if actual[table][column] != columnType {
				missing = append(missing, table+"."+column+":"+columnType)
			}
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return fmt.Errorf("CI schema prerequisite missing or incompatible: %v; deploy the source-aware CI ingestion schema to this database before querying CI telemetry", missing)
}
