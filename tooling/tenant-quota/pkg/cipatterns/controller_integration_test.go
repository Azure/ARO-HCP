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
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-kusto-go/azkustodata"
	"github.com/Azure/azure-kusto-go/azkustodata/kql"
	"github.com/Azure/azure-kusto-go/azkustodata/query"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
)

// KUSTO_TEST_ENDPOINT=http://127.0.0.1:18094 go test ./pkg/cipatterns -run TestControllerKustoIntegration -v
// This test refuses non-loopback endpoints and uses its own disposable database.
// Real queued FromReader acceptance is covered by the HTTP wire test because
// the emulator does not implement the managed ingestion queue.
func TestControllerKustoIntegration(t *testing.T) {
	endpoint := os.Getenv("KUSTO_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("set KUSTO_TEST_ENDPOINT to a disposable local Kusto emulator")
	}
	u, err := url.Parse(endpoint)
	require.NoError(t, err)
	require.Equal(t, "http", u.Scheme)
	require.Contains(t, []string{"localhost", "127.0.0.1", "::1"}, u.Hostname())
	require.Nil(t, u.User)
	require.Empty(t, u.RawQuery)
	require.Empty(t, u.Fragment)
	require.Contains(t, []string{"", "/"}, u.Path)
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
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	database := fmt.Sprintf("patterns_integration_%d", time.Now().UnixNano())
	_, err = client.Mgmt(ctx, "", (&kql.Builder{}).AddUnsafe(fmt.Sprintf(`.create database %s persist (@"/kustodata/dbs/%s/md", @"/kustodata/dbs/%s/data")`, database, database, database)))
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, err := client.Mgmt(cleanupCtx, "", (&kql.Builder{}).AddUnsafe(".drop database "+database))
		require.NoError(t, err)
	})
	exec := func(statement *kql.Builder) {
		t.Helper()
		_, err := client.Mgmt(ctx, database, statement)
		require.NoError(t, err)
	}
	for _, script := range []string{"tables/ciTestResults.kql", "tables/ciTestNames.kql", "tables/ciFailurePatterns.kql"} {
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
	ingest := func(t *testing.T, table, mapping string, rows any) {
		t.Helper()
		var payload bytes.Buffer
		encoder := json.NewEncoder(&payload)
		switch rows := rows.(type) {
		case []map[string]any:
			for _, row := range rows {
				require.NoError(t, encoder.Encode(row))
			}
		case []resultRow:
			for _, row := range rows {
				require.NoError(t, encoder.Encode(row))
			}
		default:
			t.Fatalf("unsupported fixture %T", rows)
		}
		_, err := client.Mgmt(ctx, database, kql.New(".ingest inline into table ").AddTable(table).AddLiteral(" with (format='multijson', ingestionMappingReference=").AddString(mapping).AddLiteral(") <| ").AddUnsafe(payload.String()))
		require.NoError(t, err)
	}
	const build = "controller-fixture 🧪'; .drop table x; //"
	const exactText = "中文🙂𐐷\r\ncontext deadline exceeded\x00\n"
	raw := func(id, source, message string) map[string]any {
		return map[string]any{"buildId": build, "testId": id, "source": source, "message": message, "failed": true, "resourceGroup": "fixture", "result": "Failed"}
	}
	repeated := raw("unicode", "e2e", exactText)
	zeroTime := raw("unicode", "e2e", exactText)
	zeroTime["startedAt"] = "0001-01-01T00:00:00Z"
	success := raw("success", "e2e", "not failed")
	success["failed"] = false
	start := time.Date(2026, 11, 4, 1, 2, 3, 0, time.UTC)
	timed := raw("alert", "observability", multiSubjectText)
	timed["startedAt"] = start
	timed["setupFinishTime"] = start.Add(time.Minute)
	timed["testStartTime"] = start.Add(2 * time.Minute)
	timed["cleanupStartTime"] = start.Add(3 * time.Minute)
	timed["finishedAt"] = start.Add(4 * time.Minute)
	ingest(t, "ciTestResults", "ciTestResultsMapping", []map[string]any{repeated, zeroTime, raw("", "rp-upgrade", ""), timed, raw("ambiguous", "rp-provision", "context deadline exceeded"), success})
	ingest(t, "ciTestNames", "ciTestNamesMapping", []map[string]any{{"testId": "unicode", "name": "name 🧪"}, {"testId": "unicode", "name": "name 🧪"}, {"testId": "alert", "name": alertName}, {"testId": "ambiguous", "name": "one"}, {"testId": "ambiguous", "name": "two"}})
	require.NoError(t, c.checkSchema(ctx, client, "ciFailurePatterns", resultColumns))
	require.NoError(t, c.checkMapping(ctx, client))
	inputs, err := c.loadInputs(ctx, client, build)
	require.NoError(t, err)
	require.Len(t, inputs, 4)
	var occurrences int64
	for _, i := range inputs {
		occurrences += i.multiplicity
		switch i.observation.TestID {
		case "unicode":
			require.EqualValues(t, 2, i.multiplicity)
			require.True(t, i.observation.StartedAt.IsZero())
			require.Equal(t, "unique", i.nameStatus)
			require.Equal(t, exactText, i.observation.Text)
		case "alert":
			require.Equal(t, start, i.observation.StartedAt)
			require.Equal(t, start.Add(time.Minute), i.observation.SetupFinishTime)
			require.Equal(t, start.Add(2*time.Minute), i.observation.TestStartTime)
			require.Equal(t, start.Add(3*time.Minute), i.observation.CleanupStartTime)
			require.Equal(t, start.Add(4*time.Minute), i.observation.FinishedAt)
		case "":
			require.Equal(t, "missing", i.nameStatus)
			require.Empty(t, i.observation.TestName)
		case "ambiguous":
			require.Equal(t, "conflicting", i.nameStatus)
			require.Empty(t, i.observation.TestName)
		}
	}
	require.EqualValues(t, 5, occurrences)
	c.readBuildIDs = func(ctx context.Context, before string, limit int) ([]string, error) {
		return c.loadBuildIDs(ctx, client, before, limit)
	}
	c.readInputs = func(ctx context.Context, build string) ([]input, error) { return c.loadInputs(ctx, client, build) }
	c.readResults = func(ctx context.Context, inputs []input, version failurePatternVersion) ([]resultRow, error) {
		return c.loadResults(ctx, client, inputs, version)
	}
	var submitted []resultRow
	c.submit = func(ctx context.Context, payload *bytes.Buffer) error {
		if err := decodePayload(payload.Bytes(), &submitted); err != nil {
			return err
		}
		exec(kql.New(".ingest inline into table ciFailurePatterns with (format='multijson', ingestionMappingReference='ciFailurePatternsMapping') <| ").AddUnsafe(payload.String()))
		return nil
	}
	checkCoverage := func(wantComplete, wantPending int64) {
		t.Helper()
		current, err := c.loadInputs(ctx, client, build)
		require.NoError(t, err)
		require.Len(t, current, 4)
		rows, err := c.loadResults(ctx, client, current, compiledFailurePatternVersion())
		require.NoError(t, err)
		visible, err := validatedVisibleResults(current, compiledFailurePatternVersion(), rows)
		require.NoError(t, err)
		var complete, pending int64
		for _, i := range current {
			result, ok := visible[i.keyFor(compiledFailurePatternVersion())]
			if !ok {
				pending += i.multiplicity
				continue
			}
			complete += i.multiplicity
			found := false
			for _, expected := range submitted {
				if expected.ResultKey == result.ResultKey {
					require.Equal(t, expected.Findings, result.Findings)
					require.Equal(t, expected.ProducerCommit, result.ProducerCommit)
					found = true
					break
				}
			}
			require.True(t, found, "unexpected persisted computation")
		}
		require.Equal(t, wantComplete, complete)
		require.Equal(t, wantPending, pending)
	}
	checkCoverage(0, 5)
	require.NoError(t, c.allowNormalReconciliation(compiledFailurePatternVersion()))
	require.NoError(t, c.scanHead(ctx))
	require.Equal(t, 1, c.queue.Len())
	require.True(t, c.processNext(ctx))
	require.Len(t, submitted, 4)
	require.True(t, c.processNext(ctx))
	checkCoverage(5, 0)
	// Stored message hashes equal Kusto's hash_sha256 of the raw message, so the
	// documented join finds the raw row of every result.
	joined := func(buildFilter, join string) []query.Row {
		t.Helper()
		dataset, err := kusto.CompleteQuery(ctx, client, database, kql.New("ciFailurePatterns | where ").AddUnsafe(buildFilter+`
| join kind=inner (ciTestResults | where failed | extend messageHash = hash_sha256(message)) on `+join+`
| summarize raw = count() by resultKey`))
		require.NoError(t, err)
		rows, err := primaryRows(dataset)
		require.NoError(t, err)
		return rows
	}
	rawMatches := joined(`buildId != "retry-fixture"`, "buildId, source, testId, messageHash")
	require.Len(t, rawMatches, len(submitted), "every result must match a raw row")
	var matched int64
	for _, row := range rawMatches {
		count, err := row.LongByName("raw")
		require.NoError(t, err)
		matched += *count
	}
	require.EqualValues(t, occurrences, matched, "each raw occurrence matches exactly its own result")
	// Physical retry duplicates do not change the logical population or findings.
	ingest(t, "ciFailurePatterns", "ciFailurePatternsMapping", submitted)
	orphan := submitted[0]
	orphan.ResultKey = "orphan"
	historical := submitted[0]
	historical.ExtractorVersion = "historical-version"
	ingest(t, "ciFailurePatterns", "ciFailurePatternsMapping", []resultRow{orphan, historical})
	checkCoverage(5, 0)
	// Conflicting enrichment changes the exact selected name to empty, causing
	// only that new context to become pending, not mixing obsolete contexts.
	ingest(t, "ciTestNames", "ciTestNamesMapping", []map[string]any{{"testId": "unicode", "name": "another name"}})
	checkCoverage(3, 2)
	require.NoError(t, c.scanHead(ctx))
	require.True(t, c.processNext(ctx))
	require.Len(t, submitted, 5)
	require.True(t, c.processNext(ctx))
	checkCoverage(5, 0)
	for _, tt := range []struct {
		name    string
		mutate  func(*resultRow)
		wantErr string
	}{
		{
			name:    "mismatched-envelope",
			mutate:  func(row *resultRow) { row.TestID = "not the raw test" },
			wantErr: "envelope",
		},
		{
			name: "conflicting-findings",
			mutate: func(row *resultRow) {
				row.Findings = append(row.Findings[:0:0], row.Findings...)
				row.Findings[0].Canonical += " conflicting"
			},
			wantErr: "conflicting findings",
		},
		{
			name:    "missing-computed-at",
			mutate:  func(row *resultRow) { row.ComputedAt = time.Time{} },
			wantErr: "computedAt",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ingest(t, "ciTestResults", "ciTestResultsMapping", []map[string]any{raw(tt.name, "e2e", "context deadline exceeded")})
			current, err := c.loadInputs(ctx, client, build)
			require.NoError(t, err)
			var selected []input
			for _, i := range current {
				if i.observation.TestID == tt.name {
					selected = append(selected, i)
				}
			}
			require.Len(t, selected, 1)
			valid := computedRow(selected[0])
			invalid := valid
			tt.mutate(&invalid)
			ingest(t, "ciFailurePatterns", "ciFailurePatternsMapping", []resultRow{valid, invalid})
			rows, err := c.loadResults(ctx, client, selected, compiledFailurePatternVersion())
			if err == nil {
				_, err = validatedVisibleResults(selected, compiledFailurePatternVersion(), rows)
			}
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
	t.Run("paged discovery exceeds aggregate evidence limit", func(t *testing.T) {
		const largeBuilds = 2*discoveryPageSize + 4
		const messageBytes = 300 * 1024
		// Generate the large evidence entirely in Kusto: the client sends only
		// this small statement and receives only build IDs or aggregate metadata.
		_, err := client.Mgmt(ctx, database, kql.New("").AddUnsafe(fmt.Sprintf(`.set-or-append ciTestResults <|
range n from 0 to %d step 1
| project buildId=strcat("paged-", tostring(100000+n)), testId="large-evidence", source="e2e",
    result="Failed", failed=true, resourceGroup="fixture",
    startedAt=datetime(2000-01-01), finishedAt=datetime(null), setupFinishTime=datetime(null),
    testStartTime=datetime(null), cleanupStartTime=datetime(null), message=strrep("x", %d)`, largeBuilds-1, messageBytes)))
		require.NoError(t, err)
		ingest(t, "ciTestResults", "ciTestResultsMapping", []map[string]any{
			{"buildId": "paged-100000", "testId": "duplicate-build", "failed": true, "message": "another failed input"},
			{"buildId": "", "testId": "empty-build", "failed": true},
			{"buildId": "not-failed", "testId": "success-only", "failed": false},
		})
		metadata, err := c.query(ctx, client, kql.New(`ciTestResults
| where failed and buildId startswith "paged-"
| summarize buildBytes=sum(strlen(message)) by buildId
| summarize evidenceBytes=sum(buildBytes), largestBuildBytes=max(buildBytes), failedBuilds=count()`),
			map[string]any{}, map[string]string{"evidenceBytes": "long", "largestBuildBytes": "long", "failedBuilds": "long"})
		require.NoError(t, err)
		require.Len(t, metadata, 1)
		totalBytes, err := metadata[0].LongByName("evidenceBytes")
		require.NoError(t, err)
		require.NotNil(t, totalBytes)
		require.Greater(t, *totalBytes, int64(maxReadBytes), "actual retained evidence exceeds one whole-population response")
		largestBuildBytes, err := metadata[0].LongByName("largestBuildBytes")
		require.NoError(t, err)
		require.NotNil(t, largestBuildBytes)
		require.Less(t, *largestBuildBytes, int64(maxReadBytes), "individual builds still fit the existing evidence bound")
		failedBuilds, err := metadata[0].LongByName("failedBuilds")
		require.NoError(t, err)
		require.NotNil(t, failedBuilds)
		require.EqualValues(t, largeBuilds, *failedBuilds)

		discovery := newTestController(t)
		discovery.config = c.config
		queries := 0
		discovery.readBuildIDs = func(ctx context.Context, before string, limit int) ([]string, error) {
			queries++
			return discovery.loadBuildIDs(ctx, client, before, limit)
		}
		discovery.readInputs = func(context.Context, string) ([]input, error) {
			t.Error("metadata discovery attempted to load large evidence")
			return nil, fmt.Errorf("evidence access unavailable")
		}
		discovery.readResults = func(context.Context, []input, failurePatternVersion) ([]resultRow, error) {
			t.Error("metadata discovery attempted to load derived results")
			return nil, fmt.Errorf("results access unavailable")
		}
		require.NoError(t, discovery.allowNormalReconciliation(compiledFailurePatternVersion()))
		want := []string{build}
		for index := range largeBuilds {
			want = append(want, fmt.Sprintf("paged-%d", 100000+index))
		}
		sort.Sort(sort.Reverse(sort.StringSlice(want)))
		require.NoError(t, discovery.scanHead(ctx))
		require.Equal(t, want[:headPageSize], drainDiscoveredBuildIDs(t, discovery), "newest page needs no cursor")
		queries = 0
		steps := 0
		for offset := 0; offset < len(want); offset += discoveryPageSize {
			require.NoError(t, discovery.sweepStep(ctx))
			steps++
			end := min(offset+discoveryPageSize, len(want))
			require.Equal(t, want[offset:end], drainDiscoveredBuildIDs(t, discovery))
			require.Equal(t, steps, queries, "one real build-ID query per sweep step")
			if end-offset == discoveryPageSize {
				require.Equal(t, want[end-1], discovery.sweepBefore)
				continue
			}
			require.Empty(t, discovery.sweepBefore)
		}
		require.NoError(t, discovery.sweepStep(ctx))
		require.Equal(t, steps, queries, "sweep rests after reaching the oldest build")
	})
	t.Run("startup reconciles without manual admission", func(t *testing.T) {
		const startupBuild = "zzz-startup" // newest: the head poll reaches it before the sweep drains older history
		ingest(t, "ciTestResults", "ciTestResultsMapping", []map[string]any{
			{"buildId": startupBuild, "testId": "startup", "source": "e2e", "failed": true, "message": "context deadline exceeded"},
		})
		owner := newTestController(t)
		owner.config.CIJobOutcomes = c.config.CIJobOutcomes
		owner.config.CIJobOutcomes.IngestionURI = endpoint
		owner.client = httpClient
		owner.initialize = func(ctx context.Context) error {
			if err := owner.initializeKustoWithCredential(ctx, nil); err != nil {
				return err
			}
			// The emulator has no managed queue; persist using the real JSON mapping.
			owner.submit = func(ctx context.Context, payload *bytes.Buffer) error {
				_, err := client.Mgmt(ctx, database, kql.New(".ingest inline into table ciFailurePatterns with (format='multijson', ingestionMappingReference='ciFailurePatternsMapping') <| ").AddUnsafe(payload.String()))
				return err
			}
			return nil
		}
		runCtx, stop := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() { defer close(done); owner.Start(runCtx) }()
		t.Cleanup(func() { stop(); <-done })
		current, err := c.loadInputs(ctx, client, startupBuild)
		require.NoError(t, err)
		require.Len(t, current, 1)
		require.Eventually(t, func() bool {
			rows, err := c.loadResults(ctx, client, current, compiledFailurePatternVersion())
			if err != nil {
				return false
			}
			visible, err := validatedVisibleResults(current, compiledFailurePatternVersion(), rows)
			return err == nil && len(visible) == 1
		}, 10*time.Second, 50*time.Millisecond)
	}) // A test that fails twice in one run shares (buildId, source, testId) between
	// two raw rows with different messages. Only the hash tells them apart.
	const retryBuild = "retry-fixture"
	flaky := func(message string) map[string]any {
		return map[string]any{"buildId": retryBuild, "testId": "flaky", "source": "e2e", "message": message, "failed": true, "resourceGroup": "fixture", "result": "Failed"}
	}
	ingest(t, "ciTestResults", "ciTestResultsMapping", []map[string]any{flaky("first failure: context deadline exceeded"), flaky(exactText)})
	attempts, err := c.loadInputs(ctx, client, retryBuild)
	require.NoError(t, err)
	require.Len(t, attempts, 2)
	var attemptResults []resultRow
	for _, attempt := range attempts {
		attemptResults = append(attemptResults, computedRow(attempt))
	}
	ingest(t, "ciFailurePatterns", "ciFailurePatternsMapping", attemptResults)
	require.Len(t, joined(`buildId == "retry-fixture"`, "buildId, source, testId"), 2)
	for _, row := range joined(`buildId == "retry-fixture"`, "buildId, source, testId") {
		count, err := row.LongByName("raw")
		require.NoError(t, err)
		require.EqualValues(t, 2, *count, "joining on the triple alone matches both attempts")
	}
	for _, row := range joined(`buildId == "retry-fixture"`, "buildId, source, testId, messageHash") {
		count, err := row.LongByName("raw")
		require.NoError(t, err)
		require.EqualValues(t, 1, *count, "the hash selects the attempt's own raw row")
	}
}
