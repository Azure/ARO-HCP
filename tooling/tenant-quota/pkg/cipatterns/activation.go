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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Azure/azure-kusto-go/azkustodata"
	"github.com/Azure/azure-kusto-go/azkustodata/kql"
	"github.com/Azure/azure-kusto-go/azkustodata/query"

	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/config"
)

const (
	// coverageIngestionMargin excludes raw rows ingested less than this long before a
	// check, so builds still being ingested cannot keep the residual above zero.
	coverageIngestionMargin = 15 * time.Minute
	// activationToleranceDivisor makes the activation threshold floor(total/100): up
	// to 1% of the raw failed inputs may lack a result when a version is first recorded.
	activationToleranceDivisor = 100
	// coverageFallbackInterval bounds the time between checks of an active version
	// when no sweep completes (for example while failing builds hold the sweep gate),
	// so heartbeats and metrics continue.
	coverageFallbackInterval = 24 * time.Hour
)

var controlColumns = map[string]string{
	"extractorVersion": "string", "resultSchemaVersion": "long", "coverageCheckedAt": "datetime",
	"rawFailedInputsConsidered": "long", "rawFailedInputsWithoutResult": "long",
}

// controlRow is one recorded coverage check. The row with the greatest
// CoverageCheckedAt names the active extractor version and result schema.
type controlRow struct {
	ExtractorVersion             string    `json:"extractorVersion"`
	ResultSchemaVersion          int64     `json:"resultSchemaVersion"`
	CoverageCheckedAt            time.Time `json:"coverageCheckedAt"`
	RawFailedInputsConsidered    int64     `json:"rawFailedInputsConsidered"`
	RawFailedInputsWithoutResult int64     `json:"rawFailedInputsWithoutResult"`
}

// sourceCoverage counts the distinct raw failed inputs of one source and how many of
// them have no result row of the checked version.
type sourceCoverage struct {
	source    string
	total     int64
	uncovered int64
}

// coverageQuery anti-joins the raw failed inputs ingested before request.cutoff
// against the results of request.version and request.schema. Only per-source counts
// are returned. The join ignores testName, so a result computed under an outdated
// name still counts as covered.
func coverageQuery(settings config.CIJobOutcomesConfig, resultsTable string) *kql.Builder {
	return kql.New("let rawIn = ").AddTable(settings.TestResults.Table).AddLiteral(`
| where failed
| where isnotempty(buildId) and ingestion_time() < todatetime(request.cutoff)
| extend messageHash = hash_sha256(message)
| summarize by buildId, source, testId, messageHash;
let resIn = `).AddTable(resultsTable).AddLiteral(`
| where extractorVersion == tostring(request.version) and resultSchemaVersion == tolong(request.schema)
| summarize covered = count() by buildId, source, testId, messageHash;
rawIn
| join kind=leftouter resIn on buildId, source, testId, messageHash
| summarize totalInputs = count(), uncoveredInputs = countif(isnull(covered)) by source`)
}

// newestControlQuery returns the control row naming the active version, if any.
func newestControlQuery(controlTable string) *kql.Builder {
	return kql.New("").AddTable(controlTable).AddLiteral(`
| order by coverageCheckedAt desc
| take 1
| project extractorVersion, resultSchemaVersion, coverageCheckedAt`)
}

func (c *Controller) loadCoverage(ctx context.Context, client *azkustodata.Client, version failurePatternVersion, cutoff time.Time) ([]sourceCoverage, error) {
	rows, err := c.query(ctx, client, coverageQuery(c.config.CIJobOutcomes, c.config.CIFailurePatterns.Results.Table),
		map[string]any{"version": version.extractorVersion, "schema": version.resultSchemaVersion, "cutoff": cutoff.UTC().Format(time.RFC3339Nano)},
		map[string]string{"source": "string", "totalInputs": "long", "uncoveredInputs": "long"})
	if err != nil {
		return nil, err
	}
	return decodeCoverage(rows)
}

func decodeCoverage(rows []query.Row) ([]sourceCoverage, error) {
	seen := map[string]bool{}
	coverage := make([]sourceCoverage, 0, len(rows))
	for index, row := range rows {
		source, err := row.StringByName("source")
		if err != nil {
			return nil, fmt.Errorf("coverage row %d: %w", index, err)
		}
		total, err := row.LongByName("totalInputs")
		if err != nil || total == nil {
			return nil, fmt.Errorf("coverage row %d invalid totalInputs: %v", index, err)
		}
		uncovered, err := row.LongByName("uncoveredInputs")
		if err != nil || uncovered == nil {
			return nil, fmt.Errorf("coverage row %d invalid uncoveredInputs: %v", index, err)
		}
		if seen[source] || *uncovered < 0 || *uncovered > *total {
			return nil, fmt.Errorf("coverage row %d for source %q is duplicated or inconsistent", index, source)
		}
		seen[source] = true
		coverage = append(coverage, sourceCoverage{source: source, total: *total, uncovered: *uncovered})
	}
	return coverage, nil
}

// loadNewestControl reads the pair named by the newest control row. found is false
// only for a successful, complete read of an empty table.
func (c *Controller) loadNewestControl(ctx context.Context, client *azkustodata.Client) (failurePatternVersion, bool, error) {
	rows, err := c.query(ctx, client, newestControlQuery(c.config.CIFailurePatterns.Control.Table), map[string]any{},
		map[string]string{"extractorVersion": "string", "resultSchemaVersion": "long", "coverageCheckedAt": "datetime"})
	if err != nil {
		return failurePatternVersion{}, false, err
	}
	if len(rows) == 0 {
		return failurePatternVersion{}, false, nil
	}
	if len(rows) != 1 {
		return failurePatternVersion{}, false, fmt.Errorf("expected at most one newest control row, got %d", len(rows))
	}
	extractor, err := rows[0].StringByName("extractorVersion")
	if err != nil {
		return failurePatternVersion{}, false, fmt.Errorf("read control extractorVersion: %w", err)
	}
	schema, err := rows[0].LongByName("resultSchemaVersion")
	if err != nil || schema == nil {
		return failurePatternVersion{}, false, fmt.Errorf("read control resultSchemaVersion: %v", err)
	}
	return failurePatternVersion{extractorVersion: extractor, resultSchemaVersion: *schema}, true, nil
}

// coverageCheck measures how many raw failed inputs lack a result of the compiled
// version and records a control row when the write rule allows it:
//   - the newest row already names the compiled pair: always write (heartbeat), so a
//     transient outage cannot let the active record expire;
//   - otherwise write (activation) only when the uncovered inputs are at most
//     floor(total/100).
//
// No row is written when any read fails. It reports whether the compiled pair is the
// active one after the check.
func (c *Controller) coverageCheck(ctx context.Context) (bool, error) {
	version := compiledFailurePatternVersion()
	checkedAt := c.now().UTC()
	newest, found, err := c.readControl(ctx)
	if err != nil {
		return false, fmt.Errorf("read newest control row: %w", err)
	}
	coverage, err := c.readCoverage(ctx, version, checkedAt.Add(-coverageIngestionMargin))
	if err != nil {
		return false, fmt.Errorf("read coverage: %w", err)
	}
	var total, uncovered int64
	for _, source := range coverage {
		total += source.total
		uncovered += source.uncovered
	}
	// The per-source counts and the check timestamp describe the same successful check,
	// so they are published together, only once the check has completed.
	publish := func() {
		c.metrics.uncoveredInputs.Reset()
		c.metrics.consideredInputs.Reset()
		for _, source := range coverage {
			c.metrics.consideredInputs.WithLabelValues(source.source).Set(float64(source.total))
			c.metrics.uncoveredInputs.WithLabelValues(source.source).Set(float64(source.uncovered))
		}
		c.metrics.lastCoverageCheck.Set(float64(checkedAt.Unix()))
	}
	named := found && newest == version
	if !named && uncovered > total/activationToleranceDivisor {
		c.logger.Info("Extractor version not active yet", "extractorVersion", version.extractorVersion,
			"resultSchemaVersion", version.resultSchemaVersion, "uncoveredInputs", uncovered, "consideredInputs", total)
		publish()
		return false, nil
	}
	payload := &bytes.Buffer{}
	if err := json.NewEncoder(payload).Encode(controlRow{
		ExtractorVersion: version.extractorVersion, ResultSchemaVersion: version.resultSchemaVersion, CoverageCheckedAt: checkedAt,
		RawFailedInputsConsidered: total, RawFailedInputsWithoutResult: uncovered,
	}); err != nil {
		return false, fmt.Errorf("encode control row: %w", err)
	}
	if err := c.submitControl(ctx, payload); err != nil {
		return false, fmt.Errorf("submit control row: %w", err)
	}
	c.metrics.controlRows.Inc()
	publish()
	kind := "activation"
	if named {
		kind = "heartbeat"
	}
	c.logger.Info("Control row submitted", "kind", kind, "extractorVersion", version.extractorVersion,
		"resultSchemaVersion", version.resultSchemaVersion, "uncoveredInputs", uncovered, "consideredInputs", total)
	// A queued row is not yet visible to the newest-row read, so the compiled pair is
	// active only once that read has seen it; until then the next check repeats at
	// interval, and a lost ingestion cannot stall activation.
	return named, nil
}

// runCoverage checks every interval while the control table does not yet show the
// compiled version as the newest (or after a failed check), and while it does, once
// per completed history sweep or after coverageFallbackInterval, whichever is first,
// so a stalled sweep cannot let the active record expire.
func (c *Controller) runCoverage(ctx context.Context) {
	for {
		// Discard completions that predate this check. One that arrives during the
		// check stays queued and triggers the next check, which sees its results.
		select {
		case <-c.sweepDone:
		default:
		}
		active, err := c.coverageCheckRecovered(ctx)
		if err != nil && ctx.Err() == nil {
			c.metrics.failures.WithLabelValues("coverage").Inc()
			c.logger.Error("Coverage check failed; retrying", "error", err)
		}
		wait := c.config.CIFailurePatterns.GetInterval()
		var sweep <-chan struct{}
		if err == nil && active {
			wait, sweep = c.coverageFallback, c.sweepDone
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-sweep:
			timer.Stop()
		case <-timer.C:
		}
	}
}

func (c *Controller) coverageCheckRecovered(ctx context.Context) (active bool, err error) {
	err = recoverError(ctx, func() (err error) { active, err = c.coverageCheck(ctx); return err })
	return active, err
}
