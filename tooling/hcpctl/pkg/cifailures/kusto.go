// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software distributed
// under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
// CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cifailures

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/Azure/azure-kusto-go/azkustodata"
	"github.com/Azure/azure-kusto-go/azkustodata/kql"
	"github.com/Azure/azure-kusto-go/azkustodata/query"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
)

// QueryClient uses the SDK's fully materialized result API. Query consumes the
// entire response and returns late table/dataset failures, unlike a consumer
// that reads only the first primary table from IterativeQuery.
type QueryClient interface {
	Query(context.Context, string, azkustodata.Statement, ...azkustodata.QueryOption) (query.Dataset, error)
}

const schemaQuery = `union
(ciTestResults | getschema | extend TableName="ciTestResults"),
(ciTestNames | getschema | extend TableName="ciTestNames"),
(ciJobOutcomes | getschema | extend TableName="ciJobOutcomes"),
(ciProcessedJobs | getschema | extend TableName="ciProcessedJobs")
| project TableName, ColumnName, ColumnType`

// Parameter declarations are supplied by the SDK QueryParameters option.
const runsQuery = `ciJobOutcomes
| summarize arg_max(finishedAt, jobName, prowUrl, failed) by buildId
| where finishedAt >= windowFrom and finishedAt < windowTo
| where isempty(jobFilter) or jobName contains_cs jobFilter
| join kind=leftouter (ciProcessedJobs | summarize processedCount=count() by buildId) on buildId
| project buildId, jobName, prowUrl, runFinishedAt=finishedAt, runFailed=failed, processed=coalesce(processedCount, 0) > 0`

// Use the captured run IDs rather than independently selecting runs again.
// The failed-test population is never summarized or deduplicated.
const observationsQuery = `let names = ciTestNames
    | summarize name=take_any(name), nameVariants=count_distinct(name) by testId
    | extend name=iff(nameVariants == 1, name, "");
ciTestResults
| where failed
| where buildId in (todynamic(buildIDs))
| where isempty(sourceFilter) or source == sourceFilter
| join kind=leftouter names on testId
| project buildId, source, testId, testName=coalesce(name, ""), nameVariants=coalesce(nameVariants, 0),
    startedAt, finishedAt, setupFinishTime, testStartTime, cleanupStartTime, message`

var requiredSchema = map[string]map[string]string{
	"ciTestResults": {
		"buildId": "string", "testId": "string", "source": "string", "failed": "bool", "message": "string",
		"startedAt": "datetime", "finishedAt": "datetime", "setupFinishTime": "datetime",
		"testStartTime": "datetime", "cleanupStartTime": "datetime",
	},
	"ciTestNames":     {"testId": "string", "name": "string"},
	"ciJobOutcomes":   {"buildId": "string", "jobName": "string", "prowUrl": "string", "finishedAt": "datetime", "failed": "bool"},
	"ciProcessedJobs": {"buildId": "string"},
}

type schemaColumn struct {
	TableName  string
	ColumnName string
	ColumnType string
}

type runRow struct {
	BuildID       string    `kusto:"buildId"`
	JobName       string    `kusto:"jobName"`
	ProwURL       string    `kusto:"prowUrl"`
	RunFinishedAt time.Time `kusto:"runFinishedAt"`
	RunFailed     bool      `kusto:"runFailed"`
	Processed     bool      `kusto:"processed"`
}

type observationRow struct {
	BuildID          string    `kusto:"buildId"`
	Source           string    `kusto:"source"`
	TestID           string    `kusto:"testId"`
	TestName         string    `kusto:"testName"`
	NameVariants     int64     `kusto:"nameVariants"`
	StartedAt        time.Time `kusto:"startedAt"`
	FinishedAt       time.Time `kusto:"finishedAt"`
	SetupFinishTime  time.Time `kusto:"setupFinishTime"`
	TestStartTime    time.Time `kusto:"testStartTime"`
	CleanupStartTime time.Time `kusto:"cleanupStartTime"`
	Message          string    `kusto:"message"`
}

// Collect bounds the entire collection (schema and population) by timeout. No
// report or bundle is returned if a query is partial, truncated, or undecodable.
func Collect(ctx context.Context, client QueryClient, database string, metadata Metadata, timeout time.Duration) (Bundle, error) {
	if err := metadata.Validate(); err != nil {
		return Bundle{}, err
	}
	if database == "" {
		return Bundle{}, fmt.Errorf("kusto database is required")
	}
	if timeout <= 0 || timeout > 30*time.Minute {
		return Bundle{}, fmt.Errorf("timeout must be greater than zero and at most 30m")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	options := []azkustodata.QueryOption{azkustodata.NoTruncation(), azkustodata.ServerTimeout(timeout)}
	dataset, err := client.Query(ctx, database, kql.New(schemaQuery), options...)
	if err != nil {
		return Bundle{}, fmt.Errorf("inspect CI schema: %w", err)
	}
	columns, err := kusto.DecodePrimary[schemaColumn](dataset)
	if err != nil {
		return Bundle{}, fmt.Errorf("decode CI schema: %w", err)
	}
	if err := validateSchema(columns); err != nil {
		return Bundle{}, err
	}
	parameters := kql.NewParameters().AddDateTime("windowFrom", metadata.Window.From.UTC()).AddDateTime("windowTo", metadata.Window.To.UTC()).AddString("jobFilter", metadata.Filters.JobFilter)
	dataset, err = client.Query(ctx, database, kql.New(runsQuery), append(options, azkustodata.QueryParameters(parameters))...)
	if err != nil {
		return Bundle{}, fmt.Errorf("collect CI runs (partial results are not usable): %w", err)
	}
	runs, err := kusto.DecodePrimary[runRow](dataset)
	if err != nil {
		return Bundle{}, fmt.Errorf("decode CI runs: %w", err)
	}
	buildIDs := make([]string, len(runs))
	for i := range runs {
		buildIDs[i] = runs[i].BuildID
	}
	// A JSON array passed as a typed parameter preserves arbitrary build ID text.
	ids, err := json.Marshal(buildIDs)
	if err != nil {
		return Bundle{}, fmt.Errorf("encode selected CI run IDs: %w", err)
	}
	parameters = kql.NewParameters().AddString("buildIDs", string(ids)).AddString("sourceFilter", metadata.Filters.Source)
	dataset, err = client.Query(ctx, database, kql.New(observationsQuery), append(options, azkustodata.QueryParameters(parameters))...)
	if err != nil {
		return Bundle{}, fmt.Errorf("collect CI observations (partial results are not usable): %w", err)
	}
	observations, err := kusto.DecodePrimary[observationRow](dataset)
	if err != nil {
		return Bundle{}, fmt.Errorf("decode CI observations: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return Bundle{}, fmt.Errorf("collect CI results: %w", err)
	}
	return assembleBundle(metadata, runs, observations)
}

func validateSchema(columns []schemaColumn) error {
	actual := map[string]map[string]string{}
	for _, column := range columns {
		if actual[column.TableName] == nil {
			actual[column.TableName] = map[string]string{}
		}
		actual[column.TableName][column.ColumnName] = column.ColumnType
	}
	var missing []string
	for table, columns := range requiredSchema {
		for column, columnType := range columns {
			if actual[table][column] != columnType {
				missing = append(missing, table+"."+column+":"+columnType)
			}
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return fmt.Errorf("CI schema prerequisite missing or incompatible: %v; deploy the source-aware CI ingestion schema (ARO-HCP PR #7875) to this database before running failure-patterns; historical empty source values are supported but an absent source column is not", missing)
}

func assembleBundle(metadata Metadata, runs []runRow, observations []observationRow) (Bundle, error) {
	metadata.Window.From = metadata.Window.From.UTC()
	metadata.Window.To = metadata.Window.To.UTC()
	bundle := Bundle{SchemaVersion: BundleSchemaVersion, Metadata: metadata, Runs: []Run{}, Observations: []Observation{}}
	environments := make(map[string]string, len(runs))
	for _, row := range runs {
		environment := jobEnvironment(row.JobName)
		environments[row.BuildID] = environment
		if metadata.Environment != "" && environment != metadata.Environment {
			continue
		}
		bundle.Runs = append(bundle.Runs, Run{BuildID: row.BuildID, Environment: environment, JobName: row.JobName, ProwURL: row.ProwURL,
			FinishedAt: row.RunFinishedAt.UTC(), Failed: row.RunFailed, Processed: row.Processed})
	}
	for _, row := range observations {
		environment, exists := environments[row.BuildID]
		if !exists {
			return Bundle{}, fmt.Errorf("observation references unselected run %q", row.BuildID)
		}
		if metadata.Environment != "" && environment != metadata.Environment {
			continue
		}
		observation := Observation{Origin: metadata.Origin, Environment: environment, BuildID: row.BuildID,
			Source: row.Source, TestID: row.TestID, TestName: row.TestName, NameVariants: row.NameVariants,
			StartedAt: row.StartedAt.UTC(), FinishedAt: row.FinishedAt.UTC(), SetupFinishTime: row.SetupFinishTime.UTC(),
			TestStartTime: row.TestStartTime.UTC(), CleanupStartTime: row.CleanupStartTime.UTC(), Text: row.Message}
		observation.Fingerprint = Fingerprint(observation)
		bundle.Observations = append(bundle.Observations, observation)
	}
	sort.Slice(bundle.Runs, func(i, j int) bool { return bundle.Runs[i].BuildID < bundle.Runs[j].BuildID })
	sort.Slice(bundle.Observations, func(i, j int) bool { return bundle.Observations[i].Fingerprint < bundle.Observations[j].Fingerprint })
	if err := bundle.Validate(); err != nil {
		return Bundle{}, fmt.Errorf("validate collected CI facts: %w", err)
	}
	return bundle, nil
}
