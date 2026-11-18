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

package cipatterns

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Azure/azure-kusto-go/azkustodata"
	"github.com/Azure/azure-kusto-go/azkustodata/kql"
	"github.com/Azure/azure-kusto-go/azkustodata/query"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/config"
)

var inputColumns = map[string]string{
	"origin": "string", "buildId": "string", "source": "string", "testId": "string",
	"startedAt": "datetime", "finishedAt": "datetime", "setupFinishTime": "datetime",
	"testStartTime": "datetime", "cleanupStartTime": "datetime", "message": "string", "testName": "string",
	"multiplicity": "long", "nameStatus": "string", "nameVariantCount": "long",
}

func buildIDQuery(settings config.CIJobOutcomesConfig, limit int) *kql.Builder {
	return kql.New("").AddTable(settings.TestResults.Table).AddLiteral(`
| where failed
| where isnotempty(buildId) and (isempty(tostring(request.beforeBuildID)) or strcmp(buildId, tostring(request.beforeBuildID)) < 0)
| distinct buildId
| order by buildId desc
| take `).AddInt(int32(limit))
}

func inputQuery(settings config.CIJobOutcomesConfig) *kql.Builder {
	// Distinct name pairs give an exact variant count; approximate dcount and
	// direct dimension joins would silently change enrichment or multiplicity.
	return kql.New("let names = ").AddTable(settings.TestNames.Table).AddLiteral(`
| distinct testId, name
| summarize name=take_any(name), nameVariantCount=count() by testId;
`).AddTable(settings.TestResults.Table).AddLiteral(`
| where failed
| where buildId == tostring(request.buildId)
| extend startedAt=iff(isnull(startedAt) or startedAt == datetime(0001-01-01), datetime(null), startedAt),
    finishedAt=iff(isnull(finishedAt) or finishedAt == datetime(0001-01-01), datetime(null), finishedAt),
    setupFinishTime=iff(isnull(setupFinishTime) or setupFinishTime == datetime(0001-01-01), datetime(null), setupFinishTime),
    testStartTime=iff(isnull(testStartTime) or testStartTime == datetime(0001-01-01), datetime(null), testStartTime),
    cleanupStartTime=iff(isnull(cleanupStartTime) or cleanupStartTime == datetime(0001-01-01), datetime(null), cleanupStartTime)
| summarize multiplicity=count() by buildId, source, testId, startedAt, finishedAt, setupFinishTime, testStartTime, cleanupStartTime, message
| join kind=leftouter names on testId
| project origin=tostring(request.origin), buildId, source, testId, startedAt, finishedAt,
    setupFinishTime, testStartTime, cleanupStartTime, message,
    testName=iff(nameVariantCount == 1, name, ""),
    nameStatus=case(isnull(nameVariantCount), "missing", nameVariantCount == 1, "unique", "conflicting"),
    nameVariantCount=coalesce(nameVariantCount, 0), multiplicity`)
}

func resultQuery(table string) *kql.Builder {
	return kql.New("").AddTable(table).AddLiteral(`
| where extractorVersion == tostring(request.version) and resultSchemaVersion == tolong(request.schema)
| where resultKey in (request.keys)
| project resultKey, rawFingerprint, messageHash, extractorVersion, resultSchemaVersion, origin, buildId, source, testId,
    testName, findings, computedAt, producerCommit`)
}

func (c *Controller) query(ctx context.Context, client *azkustodata.Client, statement *kql.Builder, payload any, required ...map[string]string) ([]query.Row, error) {
	ctx, cancel := context.WithTimeout(ctx, c.operationTimeout())
	defer cancel()
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode query parameter: %w", err)
	}
	if len(encoded) > maxReadBytes {
		return nil, fmt.Errorf("query parameters exceed %d bytes", maxReadBytes)
	}
	params := kql.NewParameters().AddSerializedDynamic("request", encoded)
	dataset, err := kusto.CompleteQuery(ctx, client, c.config.CIJobOutcomes.Database, statement, azkustodata.QueryParameters(params))
	if err != nil {
		return nil, err
	}
	return primaryRows(dataset, required...)
}

func primaryRows(dataset query.Dataset, required ...map[string]string) ([]query.Row, error) {
	table, err := kusto.PrimaryTable(dataset)
	if err != nil {
		return nil, err
	}
	columns := make(map[string]string, len(table.Columns()))
	for _, column := range table.Columns() {
		columns[column.Name()] = string(column.Type())
	}
	for _, schema := range required {
		for name, kind := range schema {
			if columns[name] != kind {
				return nil, fmt.Errorf("response requires %s:%s", name, kind)
			}
		}
	}
	if len(table.Rows()) > maxReadRows {
		return nil, fmt.Errorf("query exceeds %d rows", maxReadRows)
	}
	return table.Rows(), nil
}

func (c *Controller) operationTimeout() time.Duration {
	if timeout := c.config.GetTimeout(); timeout > 0 {
		return timeout
	}
	return defaultOperationTimeout
}

func (c *Controller) origin() string {
	return strings.TrimRight(c.config.CIJobOutcomes.ClusterURI, "/") + "/" + c.config.CIJobOutcomes.Database
}

// loadBuildIDs returns at most limit distinct failed build IDs strictly older than
// before (all builds when empty), newest first.
func (c *Controller) loadBuildIDs(ctx context.Context, client *azkustodata.Client, before string, limit int) ([]string, error) {
	rows, err := c.query(ctx, client, buildIDQuery(c.config.CIJobOutcomes, limit), map[string]any{"beforeBuildID": before}, map[string]string{"buildId": "string"})
	if err != nil {
		return nil, err
	}
	if len(rows) > limit {
		return nil, fmt.Errorf("build ID page exceeds %d rows", limit)
	}
	builds := make([]string, 0, len(rows))
	previous := before
	for index, row := range rows {
		build, err := row.StringByName("buildId")
		if err != nil {
			return nil, fmt.Errorf("build ID row %d: %w", index, err)
		}
		if build == "" || (previous != "" && build >= previous) {
			return nil, fmt.Errorf("build ID row %d is empty or not strictly before the previous ID", index)
		}
		builds = append(builds, build)
		previous = build
	}
	return builds, nil
}

func (c *Controller) loadInputs(ctx context.Context, client *azkustodata.Client, build string) ([]input, error) {
	rows, err := c.query(ctx, client, inputQuery(c.config.CIJobOutcomes), map[string]any{"origin": c.origin(), "buildId": build}, inputColumns)
	if err != nil {
		return nil, err
	}
	inputs, err := decodeInputs(rows)
	if err != nil {
		return nil, err
	}
	for index, i := range inputs {
		if err := validateInput(i, c.origin(), build); err != nil {
			return nil, fmt.Errorf("input row %d: %w", index, err)
		}
		inputs[index] = prepareInput(i, compiledFailurePatternVersion())
	}
	if err := validateInputPopulation(inputs); err != nil {
		return nil, err
	}
	return inputs, nil
}

func (c *Controller) loadResults(ctx context.Context, client *azkustodata.Client, inputs []input, version failurePatternVersion) ([]resultRow, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	keys := make([]string, len(inputs))
	for index, i := range inputs {
		keys[index] = i.keyFor(version)
	}
	rows, err := c.query(ctx, client, resultQuery(c.config.CIFailurePatterns.Results.Table), map[string]any{"version": version.extractorVersion, "schema": version.resultSchemaVersion, "keys": keys}, resultColumns)
	if err != nil {
		return nil, err
	}
	return decodeResults(rows)
}
