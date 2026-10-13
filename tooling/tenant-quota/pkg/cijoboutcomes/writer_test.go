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

package cijoboutcomes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-kusto-go/azkustodata"

	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/config"
)

const testBuildID BuildID = "1976270000000000123"
const testJobURI JobURI = "gs://test-platform-results-public/logs/periodic-ci-Azure-ARO-HCP-test/1976270000000000123"

func newTestWriter(t *testing.T) *Writer {
	t.Helper()
	cfg := &config.Config{CIJobOutcomes: config.CIJobOutcomesConfig{
		JobFilter:   "ARO-HCP",
		Outcomes:    config.KustoTableConfig{Table: "ciJobOutcomes", IngestionMapping: "ciJobOutcomesMapping"},
		TestResults: config.KustoTableConfig{Table: "ciTestResults", IngestionMapping: "ciTestResultsMapping"},
		TestNames:   config.KustoTableConfig{Table: "ciTestNames", IngestionMapping: "ciTestNamesMapping"},
		Discovered:  config.KustoTableConfig{Table: "ciDiscoveredJobs", IngestionMapping: "ciDiscoveredJobsMapping"},
		Processed:   config.KustoTableConfig{Table: "ciProcessedJobs", IngestionMapping: "ciProcessedJobsMapping"},
	}, Tenants: []config.TenantConfig{{TenantID: "t", ServicePrincipalClientId: "c", KeyVaultSecretName: "s"}}}
	require.NoError(t, cfg.Validate())
	w := NewWriter(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	w.tagExists = func(context.Context, batchKind, BuildID) (bool, error) { return false, nil }
	w.submit = func(context.Context, batchKind, BuildID, *bytes.Buffer) error { return nil }
	w.jobCursor = func(context.Context, GCSJob) (string, error) { return "", nil }
	w.pendingJobs = func(context.Context) ([]JobURI, error) { return nil, nil }
	w.discoveredExists = func(context.Context, JobURI) (bool, error) { return false, nil }
	w.processedExists = func(context.Context, BuildID) (bool, error) { return false, nil }
	w.submitDiscovery = func(context.Context, []DiscoveredJob, string) error { return nil }
	w.submitProcessed = func(context.Context, BuildID) error { return nil }
	w.completion = func(context.Context, *http.Client, string) (*prowCompletion, error) {
		return &prowCompletion{Result: "FAILURE", FinishedAt: w.now().Add(-time.Hour)}, nil
	}
	w.jobDetail = func(context.Context, *http.Client, string) (runDetail, error) { return runDetail{}, nil }
	w.e2eRows = func(context.Context, *http.Client, string) ([]ciTestResult, []ciTestName, error) {
		return []ciTestResult{{BuildID: string(testBuildID), TestID: "e2e"}}, []ciTestName{{TestID: "e2e", Name: "full name"}}, nil
	}
	w.observabilityRows = w.e2eRows
	t.Cleanup(w.queue.ShutDown)
	t.Cleanup(w.discoveryQueue.ShutDown)
	return w
}

func testContext(t *testing.T, w *Writer) context.Context {
	return utils.ContextWithLogger(t.Context(), w.logger)
}

func TestReconcileRequiresLiveCompletionNotAcceptedCache(t *testing.T) {
	w := newTestWriter(t)
	checked, submitted, acknowledgments := 0, 0, 0
	visible := map[batchKind]bool{}
	w.tagExists = func(_ context.Context, kind batchKind, _ BuildID) (bool, error) { checked++; return visible[kind], nil }
	w.submit = func(context.Context, batchKind, BuildID, *bytes.Buffer) error { submitted++; return nil }
	w.submitProcessed = func(context.Context, BuildID) error { acknowledgments++; return nil }
	ctx := testContext(t, w)
	for range 2 {
		delay, err := w.reconcile(ctx, testJobURI)
		require.NoError(t, err)
		require.Equal(t, 15*time.Minute, delay)
		require.Zero(t, acknowledgments, "accepted cache cannot acknowledge invisible ingestion")
	}
	require.Equal(t, 10, checked, "all five tags must be checked on every reconcile")
	require.Equal(t, 5, submitted, "accepted cache suppresses only resubmission")
	for _, kind := range batchKinds {
		visible[kind] = true
	}
	delay, err := w.reconcile(ctx, testJobURI)
	require.NoError(t, err)
	require.Equal(t, 15*time.Minute, delay, "ack remains pending until its row is visible")
	require.Equal(t, 1, acknowledgments)
	w.processedExists = func(context.Context, BuildID) (bool, error) { return true, nil }
	w.completion = func(context.Context, *http.Client, string) (*prowCompletion, error) {
		t.Fatal("processed job downloaded")
		return nil, nil
	}
	delay, err = w.reconcile(ctx, testJobURI)
	require.NoError(t, err)
	require.Zero(t, delay)
	require.Equal(t, 15, checked)
}

func TestAcceptedCacheExpiryAllowsRepair(t *testing.T) {
	w := newTestWriter(t)
	require.Equal(t, 20000, w.batches.capacity)
	require.Equal(t, 15*time.Minute, w.batches.ttl)
	now := time.Now()
	w.batches.now = func() time.Time { return now }
	submissions := 0
	w.submit = func(context.Context, batchKind, BuildID, *bytes.Buffer) error { submissions++; return nil }
	for range 2 {
		_, err := w.reconcile(testContext(t, w), testJobURI)
		require.NoError(t, err)
	}
	require.Equal(t, 5, submissions)
	now = now.Add(15 * time.Minute)
	_, err := w.reconcile(testContext(t, w), testJobURI)
	require.NoError(t, err)
	require.Equal(t, 10, submissions)
	require.Equal(t, "run-123", runTag("123"))
	require.Equal(t, "tests-123", testsTag("123"))
	require.NotEqual(t, discoveryTag(testJobURI), discoveryTag(testJobURI+"1"))
	require.Equal(t, "processed-"+string(testBuildID), processedBatch.tag(testBuildID))
}

func TestRootCompletionGateAndNativeOutcome(t *testing.T) {
	for _, body := range []string{"", `{}`, `{"result":"R","timestamp":1}`, `{"result":"FAILURE","timestamp":0}`} {
		t.Run(body, func(t *testing.T) {
			w := newTestWriter(t)
			w.completion = fetchProwCompletion
			w.client.Transport = ingestionTransport(func(r *http.Request) (*http.Response, error) {
				require.Equal(t, "/test-platform-results-public/logs/periodic-ci-Azure-ARO-HCP-test/1976270000000000123/finished.json", r.URL.Path)
				status := http.StatusOK
				if body == "" {
					status = http.StatusNotFound
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			w.tagExists = func(context.Context, batchKind, BuildID) (bool, error) {
				t.Fatal("pending root passed gate")
				return false, nil
			}
			delay, err := w.reconcile(testContext(t, w), testJobURI)
			require.NoError(t, err)
			require.Equal(t, 15*time.Minute, delay)
		})
	}
	w := newTestWriter(t)
	now := time.Now().UTC().Truncate(time.Second)
	w.now = func() time.Time { return now }
	w.completion = fetchProwCompletion
	w.client.Transport = ingestionTransport(func(r *http.Request) (*http.Response, error) {
		body := fmt.Sprintf(`{"result":"FAILURE","timestamp":%d}`, now.Add(-14*time.Minute).Unix())
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	submitted := 0
	w.submit = func(_ context.Context, kind batchKind, _ BuildID, payload *bytes.Buffer) error {
		submitted++
		if kind == runBatch {
			var outcome ciJobOutcome
			require.NoError(t, json.NewDecoder(payload).Decode(&outcome))
			require.Equal(t, "FAILURE", outcome.OverallResult)
			require.True(t, outcome.Failed)
			require.Equal(t, string(testBuildID), outcome.BuildID)
			require.Equal(t, now.Add(-15*time.Minute), outcome.FinishedAt)
		}
		return nil
	}
	_, err := w.reconcile(testContext(t, w), testJobURI)
	require.NoError(t, err)
	require.Zero(t, submitted, "settlement delay applies to all sources")
	w.client.Transport = ingestionTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"result":"FAILURE","timestamp":%d}`, now.Add(-15*time.Minute).Unix())))}, nil
	})
	_, err = w.reconcile(testContext(t, w), testJobURI)
	require.NoError(t, err)
	require.Equal(t, 5, submitted)
}

func TestRestartRepairsFailedIngestion(t *testing.T) {
	w := newTestWriter(t)
	_, err := w.reconcile(testContext(t, w), testJobURI)
	require.NoError(t, err)
	w.batches = newTTLCache[string, struct{}](20000, 15*time.Minute)
	w.tagExists = func(_ context.Context, kind batchKind, _ BuildID) (bool, error) { return kind != testsBatch, nil }
	var submitted []batchKind
	w.submit = func(_ context.Context, kind batchKind, _ BuildID, _ *bytes.Buffer) error {
		submitted = append(submitted, kind)
		return nil
	}
	w.submitProcessed = func(context.Context, BuildID) error { t.Fatal("missing tests acknowledged"); return nil }
	delay, err := w.reconcile(testContext(t, w), testJobURI)
	require.NoError(t, err)
	require.Equal(t, 15*time.Minute, delay)
	require.Equal(t, []batchKind{testsBatch}, submitted)
}

func TestIndependentSourceFailuresAndOptionalEmpty(t *testing.T) {
	for _, source := range []string{"job", "e2e", "tag", "submission"} {
		t.Run(source, func(t *testing.T) {
			w := newTestWriter(t)
			failure := errors.New("transient")
			var submitted []batchKind
			w.submit = func(_ context.Context, kind batchKind, _ BuildID, _ *bytes.Buffer) error {
				if source == "submission" && kind == testsBatch {
					return failure
				}
				submitted = append(submitted, kind)
				return nil
			}
			switch source {
			case "job":
				w.jobDetail = func(context.Context, *http.Client, string) (runDetail, error) { return runDetail{}, failure }
			case "e2e":
				w.e2eRows = func(context.Context, *http.Client, string) ([]ciTestResult, []ciTestName, error) {
					return nil, []ciTestName{{Name: "still usable"}}, failure
				}
			case "tag":
				w.tagExists = func(_ context.Context, kind batchKind, _ BuildID) (bool, error) {
					if kind == runBatch {
						return false, failure
					}
					return false, nil
				}
			}
			w.submitProcessed = func(context.Context, BuildID) error { t.Fatal("transient failure acknowledged"); return nil }
			_, err := w.reconcile(testContext(t, w), testJobURI)
			require.ErrorIs(t, err, failure)
			require.Contains(t, submitted, observabilityTestsBatch)
			require.Contains(t, submitted, observabilityNamesBatch)
			if source == "e2e" {
				require.Contains(t, submitted, namesBatch)
			}
		})
	}
	for _, empty := range []bool{false, true} {
		w := newTestWriter(t)
		w.tagExists = func(_ context.Context, kind batchKind, _ BuildID) (bool, error) { return kind == runBatch, nil }
		w.e2eRows = func(context.Context, *http.Client, string) ([]ciTestResult, []ciTestName, error) {
			if empty {
				return []ciTestResult{}, []ciTestName{}, nil
			}
			return nil, nil, nil
		}
		w.observabilityRows = w.e2eRows
		acks := 0
		w.submitProcessed = func(context.Context, BuildID) error { acks++; return nil }
		_, err := w.reconcile(testContext(t, w), testJobURI)
		require.NoError(t, err)
		require.Equal(t, 1, acks, "settled optional absent/malformed/empty sources are terminally handled")
		require.Zero(t, w.batches.size(), "only accepted writes may enter the cache")
	}
}

func TestDiscoveryUsesLiveRowsNotTags(t *testing.T) {
	w := newTestWriter(t)
	checks, submissions := 0, 0
	w.tagExists = func(context.Context, batchKind, BuildID) (bool, error) {
		t.Fatal("discovery checked tag instead of row")
		return false, nil
	}
	w.discoveredExists = func(_ context.Context, uri JobURI) (bool, error) {
		checks++
		require.Equal(t, testJobURI, uri)
		return checks > 1, nil
	}
	w.submitDiscovery = func(_ context.Context, rows []DiscoveredJob, tag string) error {
		submissions++
		require.Equal(t, []DiscoveredJob{{JobURI: testJobURI, BuildID: string(testBuildID), JobName: "periodic-ci-Azure-ARO-HCP-test"}}, rows)
		require.Equal(t, discoveryTag(testJobURI), tag)
		return nil
	}
	for range 2 {
		require.NoError(t, w.reconcileDiscovery(testContext(t, w), testJobURI))
	}
	require.Equal(t, 1, checks)
	require.Equal(t, 1, submissions)
	w.batches = newTTLCache[string, struct{}](20000, 15*time.Minute)
	require.NoError(t, w.reconcileDiscovery(testContext(t, w), testJobURI))
	require.Equal(t, 2, checks)
	require.Equal(t, 1, submissions, "bulk bootstrap rows must suppress a per-URI submission without a matching tag")
}

func TestRejectNoncanonicalKeys(t *testing.T) {
	w := newTestWriter(t)
	w.processedExists = func(context.Context, BuildID) (bool, error) { t.Fatal("invalid URI queried"); return false, nil }
	w.discoveredExists = func(context.Context, JobURI) (bool, error) { t.Fatal("invalid URI queried"); return false, nil }
	for _, uri := range []JobURI{"bad", testJobURI + "/", testJobURI + "/artifacts"} {
		_, err := w.reconcile(testContext(t, w), uri)
		require.Error(t, err)
		require.Error(t, w.reconcileDiscovery(testContext(t, w), uri))
	}
}

func TestKustoQueriesUseExactScopedIntegerCursor(t *testing.T) {
	w := newTestWriter(t)
	for _, job := range []GCSJob{
		{Name: "pull-ci-Azure-ARO-HCP-test", Prefix: "pr-logs/directory/pull-ci-Azure-ARO-HCP-test/", Aliases: true},
		{Name: "pull-ci-Azure-ARO-HCP-test", Prefix: "pr-logs/pull/batch/pull-ci-Azure-ARO-HCP-test/"},
		{Name: "periodic-ci-Azure-ARO-HCP-test", Prefix: "logs/periodic-ci-Azure-ARO-HCP-test/"},
	} {
		query := cursorQuery(w.config.CIJobOutcomes, job).String()
		require.Contains(t, query, "jobName == ")
		require.Contains(t, query, "max(tolong(buildId))")
		require.Contains(t, query, "toscalar(")
		require.Contains(t, query, "tostring(maximum)")
		if job.Aliases {
			require.Contains(t, query, `jobUri startswith_cs "gs://test-platform-results-public/pr-logs/pull/"`)
			require.Contains(t, query, `not(jobUri startswith_cs "gs://test-platform-results-public/pr-logs/pull/batch/")`)
			require.NotContains(t, query, "directory/")
		} else {
			require.Contains(t, query, "gs://test-platform-results-public/"+job.Prefix)
		}
	}
	query := pendingJobsQuery(w.config.CIJobOutcomes).String()
	require.Contains(t, query, "join kind=leftanti")
	require.Contains(t, query, "distinct jobUri")
	require.NotContains(t, query, "ago(")
	require.NotContains(t, query, "take ")
	for _, field := range []string{"adoBuildId", "message", "jobUri", "buildId", "jobName"} {
		require.NoError(t, validateMapping(fmt.Sprintf(`[{"Column":%q,"DataType":"string","Properties":{"Path":"$['%s']"}}]`, field, field), field))
		require.Error(t, validateMapping("[]", field))
	}
}

func kustoResult(column, rows string) string {
	var decoded []json.RawMessage
	_ = json.Unmarshal([]byte(rows), &decoded)
	return fmt.Sprintf(`[{"FrameType":"DataSetHeader","IsProgressive":false,"Version":"v2.0","IsFragmented":true,"ErrorReportingPlacement":"EndOfTable"}
,{"FrameType":"DataTable","TableId":0,"TableKind":"QueryProperties","TableName":"metadata","Columns":[{"ColumnName":"ignore","ColumnType":"string"}],"Rows":[["not a primary row"]]}
,{"FrameType":"TableHeader","TableId":1,"TableKind":"PrimaryResult","TableName":"PrimaryResult","Columns":[{"ColumnName":%q,"ColumnType":"string"}]}
,{"FrameType":"TableFragment","TableFragmentType":"DataAppend","TableId":1,"Rows":%s}
,{"FrameType":"TableCompletion","TableId":1,"RowCount":%d}
,{"FrameType":"DataSetCompletion","HasErrors":false,"Cancelled":false}
]`, column, rows, len(decoded))
}

func TestKustoProductionReadHooks(t *testing.T) {
	w := newTestWriter(t)
	w.config.CIJobOutcomes.ClusterURI = "http://localhost"
	w.config.CIJobOutcomes.Database = "ServiceLogs"
	column, rows := "cursor", `[["1976270000000000123"]]`
	w.client.Transport = ingestionTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet {
			return &http.Response{StatusCode: http.StatusNotFound, Body: http.NoBody}, nil
		}
		require.Equal(t, "/v2/rest/query", r.URL.Path, "read-only initialization must never construct ingestors or mutate schemas")
		var request struct {
			CSL        string
			Properties json.RawMessage
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		require.Contains(t, string(request.Properties), `"notruncation":true`)
		require.Contains(t, string(request.Properties), "servertimeout")
		_, deadline := r.Context().Deadline()
		require.True(t, deadline)
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(kustoResult(column, rows)))}, nil
	})
	require.NoError(t, w.initializeKustoWithCredential(t.Context(), false, nil))
	t.Cleanup(w.closeClients)
	job := GCSJob{Name: "periodic-ci-Azure-ARO-HCP-test", Prefix: "logs/periodic-ci-Azure-ARO-HCP-test/"}
	cursor, err := w.jobCursor(t.Context(), job)
	require.NoError(t, err)
	require.Equal(t, string(testBuildID), cursor, "snowflake must survive without float precision loss")
	rows = `[[""]]`
	cursor, err = w.jobCursor(t.Context(), job)
	require.NoError(t, err)
	require.Empty(t, cursor)
	column, rows = "jobUri", fmt.Sprintf(`[[%q],[%q]]`, testJobURI, testJobURI+"1")
	jobs, err := w.pendingJobs(t.Context())
	require.NoError(t, err)
	require.Equal(t, []JobURI{testJobURI, testJobURI + "1"}, jobs)
	column, rows = "found", `[]`
	exists, err := w.discoveredExists(t.Context(), testJobURI)
	require.NoError(t, err)
	require.False(t, exists, "metadata rows are not discovery rows")
	rows = `[["yes"]]`
	exists, err = w.processedExists(t.Context(), testBuildID)
	require.NoError(t, err)
	require.True(t, exists)
	column = "wrong"
	_, err = w.pendingJobs(t.Context())
	require.ErrorContains(t, err, "column jobUri not found")
}

func TestPendingQueryRejectsPartialAndMissingPrimaryResults(t *testing.T) {
	for _, partial := range []bool{false, true} {
		w := newTestWriter(t)
		w.config.CIJobOutcomes.ClusterURI = "http://localhost"
		body := kustoResult("jobUri", fmt.Sprintf(`[[%q]]`, testJobURI))
		if partial {
			body = strings.Replace(body, `"HasErrors":false`, `"HasErrors":true,"OneApiErrors":[{"error":{"code":"LimitsExceeded","message":"partial results","@permanent":false}}]`, 1)
		} else {
			start := strings.Index(body, `,{"FrameType":"TableHeader"`)
			end := strings.Index(body, `,{"FrameType":"DataSetCompletion"`)
			body = body[:start] + body[end:]
		}
		w.client.Transport = ingestionTransport(func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodGet {
				return &http.Response{StatusCode: http.StatusNotFound, Body: http.NoBody}, nil
			}
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
		})
		require.NoError(t, w.initializeKustoWithCredential(t.Context(), false, nil))
		t.Cleanup(w.closeClients)
		jobs, err := w.pendingJobs(t.Context())
		require.Error(t, err)
		require.Empty(t, jobs, "never enqueue an incomplete result set")
	}
}

func TestMappingQueriesAndGuards(t *testing.T) {
	w := newTestWriter(t)
	for _, target := range []config.KustoTableConfig{w.target(discoveredBatch), w.target(processedBatch), w.target(testsBatch)} {
		for _, field := range []string{"buildId", "message"} {
			mapping, err := json.Marshal(fmt.Sprintf(`[{"Column":%q,"DataType":"string","Path":"$['%s']"}]`, field, field))
			require.NoError(t, err)
			client, err := azkustodata.New(azkustodata.NewConnectionStringBuilder("http://localhost"), azkustodata.WithHttpClient(&http.Client{
				Transport: ingestionTransport(func(r *http.Request) (*http.Response, error) {
					if r.Method == http.MethodGet {
						return &http.Response{StatusCode: http.StatusNotFound, Body: http.NoBody}, nil
					}
					var request struct{ CSL string }
					require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
					require.Contains(t, request.CSL, target.Table)
					require.Contains(t, request.CSL, target.IngestionMapping)
					body := fmt.Sprintf(`{"Tables":[{"TableName":"Table","Columns":[{"ColumnName":"Mapping","ColumnType":"string"}],"Rows":[[%s]]}]}`, mapping)
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
				}),
			}))
			require.NoError(t, err)
			require.NoError(t, w.checkMapping(t.Context(), client, target, field))
			require.NoError(t, client.Close())
		}
	}
}
