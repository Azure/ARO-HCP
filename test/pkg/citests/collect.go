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

package citests

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/Azure/azure-kusto-go/azkustodata"
	"github.com/Azure/azure-kusto-go/azkustodata/kql"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/ciruns"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/citables"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
)

// observationBatchSize bounds each observation query response without sampling
// or truncating the captured run population.
const observationBatchSize = 128

// Parameter declarations are supplied by the SDK QueryParameters option.
const runsQuery = `let jobFilter = tostring(filters.job_filter);
ciJobOutcomes
| summarize arg_max(finishedAt, jobName, prowUrl, overallResult, failed, startedAt, runtimeRegion) by buildId
| where finishedAt >= windowFrom and finishedAt < windowTo
| where isempty(jobFilter) or jobName contains_cs jobFilter
| join kind=leftouter (ciProcessedJobs | summarize processedCount=count() by buildId) on buildId
| project buildId, jobName, prowUrl, overallResult, runtimeRegion, runFailed=failed,
    runStartedAt=startedAt, runFinishedAt=finishedAt, processed=coalesce(processedCount, 0) > 0
| order by buildId asc`

// Use the captured run IDs rather than independently selecting runs again.
// E2E results are the only producer whose durations describe test execution.
const observationsQuery = `let names = ciTestNames
    | summarize name=take_any(name), nameVariants=count_distinct(name) by testId
    | extend name=iff(nameVariants == 1, name, "");
ciTestResults
| where buildId in (buildIDs)
| where source == "e2e"
| join kind=leftouter names on testId
| project buildId, source, testId, testName=coalesce(name, ""), nameVariants=coalesce(nameVariants, 0),
    result, failed, startedAt, finishedAt, setupFinishTime, testStartTime, cleanupStartTime
| order by buildId asc, testId asc, startedAt asc`

type runRow struct {
	BuildID       string    `kusto:"buildId"`
	JobName       string    `kusto:"jobName"`
	ProwURL       string    `kusto:"prowUrl"`
	OverallResult string    `kusto:"overallResult"`
	RuntimeRegion string    `kusto:"runtimeRegion"`
	RunFailed     bool      `kusto:"runFailed"`
	RunStartedAt  time.Time `kusto:"runStartedAt"`
	RunFinishedAt time.Time `kusto:"runFinishedAt"`
	Processed     bool      `kusto:"processed"`
}

type observationRow struct {
	BuildID          string    `kusto:"buildId"`
	Source           string    `kusto:"source"`
	TestID           string    `kusto:"testId"`
	TestName         string    `kusto:"testName"`
	NameVariants     int64     `kusto:"nameVariants"`
	Result           string    `kusto:"result"`
	Failed           bool      `kusto:"failed"`
	StartedAt        time.Time `kusto:"startedAt"`
	FinishedAt       time.Time `kusto:"finishedAt"`
	SetupFinishTime  time.Time `kusto:"setupFinishTime"`
	TestStartTime    time.Time `kusto:"testStartTime"`
	CleanupStartTime time.Time `kusto:"cleanupStartTime"`
}

// Collect bounds the entire collection (schema and population) by timeout. No
// bundle is returned if a query is partial, truncated or undecodable.
func Collect(ctx context.Context, client citables.QueryClient, database string, selection Selection, timeout time.Duration) (Bundle, error) {
	if err := selection.Validate(); err != nil {
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
	if err := citables.Ensure(ctx, client, database, options...); err != nil {
		return Bundle{}, err
	}
	// SDK v1.2.3 AddString emits invalid KQL escapes for non-BMP text.
	// Serialized dynamic JSON preserves arbitrary filter strings.
	filters, err := json.Marshal(struct {
		JobFilter string `json:"job_filter"`
	}{JobFilter: selection.JobFilter})
	if err != nil {
		return Bundle{}, fmt.Errorf("encode CI query filters: %w", err)
	}
	parameters := kql.NewParameters().
		AddDateTime("windowFrom", selection.From.UTC()).
		AddDateTime("windowTo", selection.To.UTC()).
		AddSerializedDynamic("filters", filters)
	dataset, err := client.Query(ctx, database, kql.New(runsQuery), append(options, azkustodata.QueryParameters(parameters))...)
	if err != nil {
		return Bundle{}, fmt.Errorf("collect CI runs (partial results are not usable): %w", err)
	}
	runRows, err := kusto.DecodePrimary[runRow](dataset)
	if err != nil {
		return Bundle{}, fmt.Errorf("decode CI runs: %w", err)
	}
	runs, buildIDs, err := buildRuns(selection, runRows)
	if err != nil {
		return Bundle{}, err
	}
	observations := make([]Observation, 0, len(buildIDs))
	for start := 0; start < len(buildIDs); start += observationBatchSize {
		end := start + observationBatchSize
		if end > len(buildIDs) {
			end = len(buildIDs)
		}
		// A JSON array passed as a typed parameter preserves arbitrary build ID text.
		ids, err := json.Marshal(buildIDs[start:end])
		if err != nil {
			return Bundle{}, fmt.Errorf("encode selected CI run IDs: %w", err)
		}
		batchParameters := kql.NewParameters().AddSerializedDynamic("buildIDs", ids)
		dataset, err := client.Query(ctx, database, kql.New(observationsQuery), append(options, azkustodata.QueryParameters(batchParameters))...)
		if err != nil {
			return Bundle{}, fmt.Errorf("collect CI observations (partial results are not usable): %w", err)
		}
		rows, err := kusto.DecodePrimary[observationRow](dataset)
		if err != nil {
			return Bundle{}, fmt.Errorf("decode CI observations: %w", err)
		}
		batch, err := buildObservations(rows)
		if err != nil {
			return Bundle{}, err
		}
		observations = append(observations, batch...)
	}
	if err := ctx.Err(); err != nil {
		return Bundle{}, fmt.Errorf("collect CI results: %w", err)
	}
	selected := make(map[string]struct{}, len(runs))
	for _, run := range runs {
		selected[run.BuildID] = struct{}{}
	}
	observations = collapseObservations(observations)
	for i, observation := range observations {
		if _, ok := selected[observation.BuildID]; !ok {
			return Bundle{}, fmt.Errorf("observation %d references unselected run %q", i, observation.BuildID)
		}
	}
	bundle := Bundle{SchemaVersion: BundleSchemaVersion, Selection: selection, Runs: runs, Observations: observations}
	if err := bundle.Validate(); err != nil {
		return Bundle{}, fmt.Errorf("validate collected CI facts: %w", err)
	}
	return bundle, nil
}

// validateSchema requires every column and type this feature reads to exist, so
// a database that has not deployed the CI ingestion schema is an error rather
// than empty history.
// buildRuns classifies runs, applies the environment filter and returns the
// sorted captured build-ID set that later queries must use instead of reselecting.
func buildRuns(selection Selection, rows []runRow) ([]Run, []string, error) {
	seen := make(map[string]struct{}, len(rows))
	runs := make([]Run, 0, len(rows))
	for _, row := range rows {
		if row.BuildID == "" {
			return nil, nil, fmt.Errorf("CI run row has an empty buildId")
		}
		if _, ok := seen[row.BuildID]; ok {
			return nil, nil, fmt.Errorf("CI run %q was returned more than once", row.BuildID)
		}
		seen[row.BuildID] = struct{}{}
		environment := ciruns.JobEnvironment(row.JobName)
		if selection.Environment != "" && environment != selection.Environment {
			continue
		}
		runs = append(runs, Run{
			BuildID:       row.BuildID,
			JobName:       row.JobName,
			Environment:   environment,
			RuntimeRegion: row.RuntimeRegion,
			OverallResult: row.OverallResult,
			Failed:        row.RunFailed,
			Processed:     row.Processed,
			StartedAt:     row.RunStartedAt.UTC(),
			FinishedAt:    row.RunFinishedAt.UTC(),
			ProwURL:       row.ProwURL,
		})
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].BuildID < runs[j].BuildID })
	buildIDs := make([]string, len(runs))
	for i := range runs {
		buildIDs[i] = runs[i].BuildID
	}
	return runs, buildIDs, nil
}

// buildObservations keeps only facts that identify a test execution, so no
// diagnostic text or resource group name is retained for this feature.
func buildObservations(rows []observationRow) ([]Observation, error) {
	observations := make([]Observation, 0, len(rows))
	for _, row := range rows {
		if row.BuildID == "" || row.TestID == "" {
			return nil, fmt.Errorf("CI observation row has an empty buildId or testId")
		}
		if row.NameVariants < 0 {
			return nil, fmt.Errorf("CI observation %s/%s reports a negative name variant count", row.BuildID, row.TestID)
		}
		observations = append(observations, Observation{
			BuildID:          row.BuildID,
			TestID:           row.TestID,
			Name:             row.TestName,
			NameVariants:     int(row.NameVariants),
			Source:           row.Source,
			Result:           row.Result,
			Failed:           row.Failed,
			StartedAt:        row.StartedAt.UTC(),
			FinishedAt:       row.FinishedAt.UTC(),
			SetupFinishTime:  row.SetupFinishTime.UTC(),
			TestStartTime:    row.TestStartTime.UTC(),
			CleanupStartTime: row.CleanupStartTime.UTC(),
		})
	}
	return observations, nil
}

// collapseObservations removes byte-equivalent duplicates while retaining
// differing executions of the same test in the same build for later exclusion.
func collapseObservations(observations []Observation) []Observation {
	collapsed := make([]Observation, 0, len(observations))
	seen := make(map[Observation]struct{}, len(observations))
	for _, observation := range observations {
		if _, ok := seen[observation]; ok {
			continue
		}
		seen[observation] = struct{}{}
		collapsed = append(collapsed, observation)
	}
	sortObservations(collapsed)
	return collapsed
}

// sortObservations orders observations by build, test and timestamps so output
// is deterministic regardless of query response order.
func sortObservations(observations []Observation) {
	sort.Slice(observations, func(i, j int) bool {
		a, b := observations[i], observations[j]
		if a.BuildID != b.BuildID {
			return a.BuildID < b.BuildID
		}
		if a.TestID != b.TestID {
			return a.TestID < b.TestID
		}
		if !a.StartedAt.Equal(b.StartedAt) {
			return a.StartedAt.Before(b.StartedAt)
		}
		if !a.FinishedAt.Equal(b.FinishedAt) {
			return a.FinishedAt.Before(b.FinishedAt)
		}
		return a.Result < b.Result
	})
}
