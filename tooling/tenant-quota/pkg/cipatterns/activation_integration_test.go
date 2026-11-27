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
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-kusto-go/azkustodata"
	"github.com/Azure/azure-kusto-go/azkustodata/kql"

	"github.com/Azure/ARO-HCP/tooling/cifailurepatterns"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
)

// KUSTO_TEST_ENDPOINT=http://127.0.0.1:18094 go test ./pkg/cipatterns -run TestCoverageAndActiveHelperKustoIntegration -v
// Like TestControllerKustoIntegration it refuses non-loopback endpoints and uses its
// own disposable database, the real table scripts and the in-code queries.
func TestCoverageAndActiveHelperKustoIntegration(t *testing.T) {
	endpoint := os.Getenv("KUSTO_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("set KUSTO_TEST_ENDPOINT to a disposable local Kusto emulator")
	}
	u, err := url.Parse(endpoint)
	require.NoError(t, err)
	require.Equal(t, "http", u.Scheme)
	require.Contains(t, []string{"localhost", "127.0.0.1", "::1"}, u.Hostname())
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	t.Cleanup(transport.CloseIdleConnections)
	httpClient := &http.Client{Timeout: 30 * time.Second, Transport: kusto.CompleteTransport{Base: fixtureTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "http" || r.URL.Host != u.Host {
			return nil, fmt.Errorf("refusing request outside local emulator: %s", r.URL)
		}
		return transport.RoundTrip(r)
	})}}
	client, err := azkustodata.New(azkustodata.NewConnectionStringBuilder(endpoint), azkustodata.WithHttpClient(httpClient))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	database := fmt.Sprintf("patterns_coverage_%d", time.Now().UnixNano())
	_, err = client.Mgmt(ctx, "", (&kql.Builder{}).AddUnsafe(fmt.Sprintf(`.create database %s persist (@"/kustodata/dbs/%s/md", @"/kustodata/dbs/%s/data")`, database, database, database)))
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, err := client.Mgmt(cleanupCtx, "", (&kql.Builder{}).AddUnsafe(".drop database "+database))
		require.NoError(t, err)
	})
	// The control script is deployed before the results table exists on purpose: the
	// helper must not need it at creation time. Its table scripts run independently.
	for _, script := range []string{"tables/ciTestResults.kql", "tables/ciFailurePatternControl.kql", "tables/ciFailurePatterns.kql"} {
		content, err := os.ReadFile(filepath.Join("../../../../dev-infrastructure/modules/logs/kusto", script))
		require.NoError(t, err)
		dataset, err := client.Mgmt(ctx, database, (&kql.Builder{}).AddUnsafe(".execute database script <|\n"+string(content)))
		require.NoError(t, err)
		for _, table := range dataset.Tables() {
			for _, row := range table.Rows() {
				result, err := row.StringByName("Result")
				require.NoError(t, err)
				require.Equal(t, "Completed", result, "%s: %s", script, row.String())
			}
		}
	}
	c := newTestController(t)
	c.config.CIJobOutcomes.ClusterURI = endpoint
	c.config.CIJobOutcomes.Database = database
	ingest := func(table, mapping string, rows ...any) {
		t.Helper()
		var payload bytes.Buffer
		encoder := json.NewEncoder(&payload)
		for _, row := range rows {
			require.NoError(t, encoder.Encode(row))
		}
		_, err := client.Mgmt(ctx, database, kql.New(".ingest inline into table ").AddTable(table).AddLiteral(" with (format='multijson', ingestionMappingReference=").AddString(mapping).AddLiteral(") <| ").AddUnsafe(payload.String()))
		require.NoError(t, err)
	}
	raw := func(build, id, source, message string, failed bool) map[string]any {
		return map[string]any{"buildId": build, "testId": id, "source": source, "message": message, "failed": failed, "resourceGroup": "fixture", "result": "Failed"}
	}
	compiled := compiledFailurePatternVersion()
	result := func(build, id, source, message, name string, version failurePatternVersion, computedAt time.Time) resultRow {
		return resultRow{ResultKey: fmt.Sprintf("%s|%s|%s|%s|%d", build, id, version.extractorVersion, name, version.resultSchemaVersion),
			RawFingerprint: "fp", MessageHash: messageHash(message), ExtractorVersion: version.extractorVersion, ResultSchemaVersion: version.resultSchemaVersion,
			Origin: "o", BuildID: build, Source: source, TestID: id, TestName: name, Findings: []cifailurepatterns.Finding{}, ComputedAt: computedAt}
	}
	coverage := func(cutoff time.Time) map[string][2]int64 {
		t.Helper()
		rows, err := c.loadCoverage(ctx, client, compiled, cutoff)
		require.NoError(t, err)
		out := map[string][2]int64{}
		for _, row := range rows {
			out[row.source] = [2]int64{row.total, row.uncovered}
		}
		return out
	}
	far := time.Now().Add(24 * time.Hour)

	// No raw rows: complete and empty.
	require.Empty(t, coverage(far))

	// Raw failed inputs: two e2e, one rp-provision. A repeated tuple counts once, and
	// non-failed rows and rows without a build ID are not inputs.
	ingest("ciTestResults", "ciTestResultsMapping",
		raw("b1", "x1", "e2e", "message one", true), raw("b1", "x1", "e2e", "message one", true),
		raw("b1", "x2", "e2e", "message two", true), raw("b2", "y1", "rp-provision", "message three", true),
		raw("b1", "ok", "e2e", "fine", false), raw("", "nobuild", "e2e", "no build", true))
	require.Equal(t, map[string][2]int64{"e2e": {2, 2}, "rp-provision": {1, 1}}, coverage(far), "absent coverage")

	now := time.Now().UTC()
	ingest("ciFailurePatterns", "ciFailurePatternsMapping", result("b1", "x1", "e2e", "message one", "n", compiled, now))
	require.Equal(t, map[string][2]int64{"e2e": {2, 1}, "rp-provision": {1, 1}}, coverage(far), "partial coverage")

	other := failurePatternVersion{extractorVersion: "other", resultSchemaVersion: compiled.resultSchemaVersion}
	otherSchema := failurePatternVersion{extractorVersion: compiled.extractorVersion, resultSchemaVersion: compiled.resultSchemaVersion + 1}
	ingest("ciFailurePatterns", "ciFailurePatternsMapping", result("b1", "x2", "e2e", "message two", "n", other, now), result("b2", "y1", "rp-provision", "message three", "n", otherSchema, now),
		result("b1", "x1", "e2e", "different message", "n", compiled, now))
	require.Equal(t, map[string][2]int64{"e2e": {2, 1}, "rp-provision": {1, 1}}, coverage(far), "other version, other schema and other message do not count")

	// Results under an outdated name and physical retry copies still cover.
	ingest("ciFailurePatterns", "ciFailurePatternsMapping", result("b1", "x2", "e2e", "message two", "outdated name", compiled, now),
		result("b2", "y1", "rp-provision", "message three", "", compiled, now), result("b2", "y1", "rp-provision", "message three", "", compiled, now.Add(time.Second)))
	require.Equal(t, map[string][2]int64{"e2e": {2, 0}, "rp-provision": {1, 0}}, coverage(far), "outdated name counts as covered; copies do not inflate")

	// Rows ingested at or after the cutoff are excluded from the population.
	rows, err := c.query(ctx, client, kql.New("").AddTable("ciTestResults").AddLiteral(" | summarize latest=max(ingestion_time())"), map[string]any{})
	require.NoError(t, err)
	latest, err := rows[0].DateTimeByName("latest")
	require.NoError(t, err)
	time.Sleep(2 * time.Second)
	ingest("ciTestResults", "ciTestResultsMapping", raw("b3", "late", "e2e", "arrived after the cutoff", true))
	cutoff := latest.Add(time.Second)
	require.Equal(t, map[string][2]int64{"e2e": {2, 0}, "rp-provision": {1, 0}}, coverage(cutoff), "post-cutoff rows excluded")
	require.Equal(t, map[string][2]int64{"e2e": {3, 1}, "rp-provision": {1, 0}}, coverage(far))

	// Control reads and the helper.
	_, found, err := c.loadNewestControl(ctx, client)
	require.NoError(t, err)
	require.False(t, found)
	active := func() []resultRow {
		t.Helper()
		rows, err := c.query(ctx, client, kql.New("ciFailurePatternActive()"), map[string]any{}, resultColumns)
		require.NoError(t, err)
		decoded, err := decodeResults(rows)
		require.NoError(t, err)
		return decoded
	}
	require.Empty(t, active(), "no control rows: no result rows")

	vA := failurePatternVersion{extractorVersion: "vA", resultSchemaVersion: 1}
	vB := failurePatternVersion{extractorVersion: "vB", resultSchemaVersion: 1}
	vA2 := failurePatternVersion{extractorVersion: "vA", resultSchemaVersion: 2}
	t0 := time.Date(2026, 12, 1, 9, 0, 0, 0, time.UTC)
	ingest("ciFailurePatterns", "ciFailurePatternsMapping",
		result("h", "a", "e2e", "m", "n", vA, t0), result("h", "b", "e2e", "m", "n", vB, t0), result("h", "b", "e2e", "m", "n", vB, t0.Add(time.Hour)),
		result("h", "c", "e2e", "m", "n", vA2, t0))
	control := func(version failurePatternVersion, checkedAt time.Time) controlRow {
		return controlRow{ExtractorVersion: version.extractorVersion, ResultSchemaVersion: version.resultSchemaVersion, CoverageCheckedAt: checkedAt, RawFailedInputsConsidered: 10}
	}
	// Ingestion order differs from check-time order: the greatest coverageCheckedAt wins.
	ingest("ciFailurePatternControl", "ciFailurePatternControlMapping", control(vB, t0.Add(2*time.Hour)), control(vA, t0.Add(time.Hour)), control(vB, t0.Add(30*time.Minute)))
	newest, found, err := c.loadNewestControl(ctx, client)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, vB, newest)
	got := active()
	require.Len(t, got, 1, "one row per resultKey for the newest pair only")
	require.Equal(t, "b", got[0].TestID)
	require.Equal(t, vB.extractorVersion, got[0].ExtractorVersion)
	require.True(t, got[0].ComputedAt.Equal(t0.Add(time.Hour)), "collapse keeps the newest computedAt")

	ingest("ciFailurePatternControl", "ciFailurePatternControlMapping", control(vA2, t0.Add(3*time.Hour)))
	got = active()
	require.Len(t, got, 1)
	require.Equal(t, "c", got[0].TestID)
	require.EqualValues(t, 2, got[0].ResultSchemaVersion)
}
