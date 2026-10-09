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

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-kusto-go/azkustodata"

	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/config"
)

func newTestWriter(t *testing.T) *Writer {
	t.Helper()
	cfg := &config.Config{CIJobOutcomes: config.CIJobOutcomesConfig{
		Releases: []string{"Presubmits", "4.21"}, JobFilter: "ARO-HCP", SippyURI: "https://sippy.invalid",
		Outcomes:    config.KustoTableConfig{Table: "ciJobOutcomes", IngestionMapping: "ciJobOutcomesMapping"},
		TestResults: config.KustoTableConfig{Table: "ciTestResults", IngestionMapping: "ciTestResultsMapping"},
		TestNames:   config.KustoTableConfig{Table: "ciTestNames", IngestionMapping: "ciTestNamesMapping"},
	}, Tenants: []config.TenantConfig{{TenantID: "t", ServicePrincipalClientId: "c", KeyVaultSecretName: "s"}}}
	require.NoError(t, cfg.Validate())
	w := NewWriter(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	w.tagExists = func(context.Context, batchKind, BuildID) (bool, error) { return false, nil }
	w.submit = func(context.Context, batchKind, BuildID, *bytes.Buffer) error { return nil }
	w.cursor = func(context.Context, string) (time.Time, error) { return time.Time{}, nil }
	w.jobDetail = func(context.Context, *http.Client, string) (runDetail, error) { return runDetail{}, nil }
	w.e2eRows = func(context.Context, *http.Client, string) ([]ciTestResult, []ciTestName, error) {
		return []ciTestResult{{BuildID: "123", TestID: "e2e"}}, []ciTestName{{TestID: "e2e", Name: "full name"}}, nil
	}
	w.observabilityRows = func(context.Context, *http.Client, string) ([]ciTestResult, []ciTestName, error) {
		return []ciTestResult{{BuildID: "123", TestID: "alert", Message: "firing"}}, []ciTestName{{TestID: "alert", Name: "alert name"}}, nil
	}
	t.Cleanup(w.queue.ShutDown)
	return w
}

func testRun(id string, at time.Time) sippyRun {
	return sippyRun{ProwID: id, Job: "periodic-ARO-HCP", URL: "https://prow.ci.openshift.org/view/gs/test-platform-results/logs/periodic-ARO-HCP/" + id, OverallResult: "S", Succeeded: true, Timestamp: sippyTimestamp{Time: at}}
}

func seedRun(w *Writer, id BuildID) {
	w.metadata.put(id, runMetadata{run: testRun(string(id), time.Now()), release: "Presubmits"})
}

func testContext(t *testing.T, w *Writer) context.Context {
	return utils.ContextWithLogger(t.Context(), w.logger)
}

func TestWriterUsesConfiguredCaches(t *testing.T) {
	cfg := newTestWriter(t).config
	size := 7
	cfg.CIJobOutcomes.CacheSize = &size
	cfg.CIJobOutcomes.CacheTTL = "2m"
	require.NoError(t, cfg.Validate())
	w := NewWriter(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(w.queue.ShutDown)
	require.Equal(t, size, w.batches.capacity)
	require.Equal(t, 2*time.Minute, w.batches.ttl)
	require.Equal(t, size, w.metadata.capacity)
	require.Equal(t, 2*time.Minute, w.metadata.ttl)
}

func TestMappingsGuardNewFields(t *testing.T) {
	for _, field := range []string{"adoBuildId", "message"} {
		for _, tc := range []struct{ name, mapping, wantErr string }{
			{"flat", `[{"column":"%s","datatype":"string","path":"$['%s']"}]`, ""},
			{"properties", `[{"Column":"%s","DataType":"string","Properties":{"Path":"$['%s']"}}]`, ""},
			{"old", `[]`, "deploy the Kusto schema migration first"},
			{"wrong path", `[{"Column":"%s","DataType":"string","Properties":{"Path":"$.wrong"}}]`, "deploy the Kusto schema migration first"},
			{"wrong type", `[{"Column":"%s","DataType":"long","Properties":{"Path":"$['%s']"}}]`, "deploy the Kusto schema migration first"},
			{"malformed", `{`, "failed to decode"},
		} {
			t.Run(field+"/"+tc.name, func(t *testing.T) {
				mapping := strings.ReplaceAll(tc.mapping, "%s", field)
				encoded, err := json.Marshal(mapping)
				require.NoError(t, err)
				w := newTestWriter(t)
				client, err := azkustodata.New(azkustodata.NewConnectionStringBuilder("http://localhost"),
					azkustodata.WithHttpClient(&http.Client{Transport: ingestionTransport(func(r *http.Request) (*http.Response, error) {
						if r.Method == http.MethodGet {
							return &http.Response{StatusCode: http.StatusNotFound, Body: http.NoBody}, nil
						}
						var request struct{ CSL string }
						require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
						require.Contains(t, request.CSL, "ingestion json mapping")
						body := fmt.Sprintf(`{"Tables":[{"TableName":"Table","Columns":[{"ColumnName":"Mapping","ColumnType":"string"}],"Rows":[[%s]]}]}`, encoded)
						return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
					})}))
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, client.Close()) })
				err = w.checkMapping(t.Context(), client, w.config.CIJobOutcomes.TestResults, field)
				if tc.wantErr == "" {
					require.NoError(t, err)
				} else {
					require.ErrorContains(t, err, tc.wantErr)
				}
			})
		}
	}
}

func TestKustoCursorQueryResponse(t *testing.T) {
	for _, tc := range []struct {
		name      string
		rows      string
		column    string
		want      time.Time
		wantError string
	}{
		{name: "cursor with metadata", rows: `[["2026-10-08T12:34:56.1234567Z"]]`, column: "cursor", want: time.Date(2026, 10, 8, 12, 34, 56, 123456700, time.UTC)},
		{name: "null max with metadata", rows: `[[null]]`, column: "cursor"},
		{name: "empty primary with metadata", rows: `[]`, column: "cursor"},
		{name: "no primary table"},
		{name: "missing primary column is an error", rows: `[[null]]`, column: "wrong", wantError: "column cursor not found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Exercise the SDK's v2 decoder, including the metadata tables that
			// Query returns alongside the primary result even for a null max().
			primary := ""
			if tc.rows != "" {
				var rows []json.RawMessage
				require.NoError(t, json.Unmarshal([]byte(tc.rows), &rows))
				primary = fmt.Sprintf(`
,{"FrameType":"TableHeader","TableId":1,"TableKind":"PrimaryResult","TableName":"PrimaryResult","Columns":[{"ColumnName":%q,"ColumnType":"datetime"}]}
,{"FrameType":"TableFragment","TableFragmentType":"DataAppend","TableId":1,"Rows":%s}
,{"FrameType":"TableCompletion","TableId":1,"RowCount":%d}`, tc.column, tc.rows, len(rows))
			}
			body := `[{"FrameType":"DataSetHeader","IsProgressive":false,"Version":"v2.0","IsFragmented":true,"ErrorReportingPlacement":"EndOfTable"}
,{"FrameType":"DataTable","TableId":0,"TableKind":"QueryProperties","TableName":"@ExtendedProperties","Columns":[{"ColumnName":"TableId","ColumnType":"int"},{"ColumnName":"Key","ColumnType":"string"},{"ColumnName":"Value","ColumnType":"dynamic"}],"Rows":[[1,"Visualization","{\"Visualization\":null}"]]}` + primary + `
,{"FrameType":"DataTable","TableId":2,"TableKind":"QueryCompletionInformation","TableName":"QueryCompletionInformation","Columns":[{"ColumnName":"Timestamp","ColumnType":"datetime"},{"ColumnName":"Level","ColumnType":"int"},{"ColumnName":"LevelName","ColumnType":"string"},{"ColumnName":"StatusCode","ColumnType":"int"},{"ColumnName":"StatusCodeName","ColumnType":"string"},{"ColumnName":"EventType","ColumnType":"int"},{"ColumnName":"EventTypeName","ColumnType":"string"},{"ColumnName":"Payload","ColumnType":"string"}],"Rows":[["2026-10-08T12:35:00Z",4,"Info",0,"S_OK (0)",4,"QueryInfo","{\"Count\":1,\"Text\":\"Query completed successfully\"}"]]}
,{"FrameType":"DataSetCompletion","HasErrors":false,"Cancelled":false}
]
`
			w := newTestWriter(t)
			w.config.CIJobOutcomes.ClusterURI = "http://localhost"
			w.config.CIJobOutcomes.Database = "ServiceLogs"
			queries := 0
			w.client.Transport = ingestionTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method == http.MethodGet {
					return &http.Response{StatusCode: http.StatusNotFound, Body: http.NoBody}, nil
				}
				require.Equal(t, "/v2/rest/query", r.URL.Path)
				var request struct{ DB, CSL string }
				require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
				require.Equal(t, "ServiceLogs", request.DB)
				require.Equal(t, cursorQuery(w.config.CIJobOutcomes, "Presubmits").String(), request.CSL)
				queries++
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			require.NoError(t, w.initializeKustoWithCredential(t.Context(), false, nil))
			t.Cleanup(w.closeClients)
			cursor, err := w.cursor(t.Context(), "Presubmits")
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.want, cursor)
			}
			require.Equal(t, 1, queries)
		})
	}
}

func TestReconcileIndependentBatches(t *testing.T) {
	w := newTestWriter(t)
	seedRun(w, "123")
	checked := []batchKind{}
	w.tagExists = func(_ context.Context, kind batchKind, _ BuildID) (bool, error) {
		checked = append(checked, kind)
		return kind == runBatch || kind == testsBatch, nil
	}
	w.jobDetail = func(context.Context, *http.Client, string) (runDetail, error) {
		t.Fatal("existing outcome downloaded")
		return runDetail{}, nil
	}
	var submissions []batchKind
	failed := true
	w.submit = func(_ context.Context, kind batchKind, _ BuildID, payload *bytes.Buffer) error {
		submissions = append(submissions, kind)
		require.GreaterOrEqual(t, len(checked), 5, "all tags must be checked before downloads/submissions")
		if kind == namesBatch {
			require.Contains(t, payload.String(), "full name")
		}
		if kind == observabilityTestsBatch && failed {
			return errors.New("unavailable")
		}
		return nil
	}
	require.ErrorContains(t, w.reconcile(testContext(t, w), "123"), "unavailable")
	require.Equal(t, []batchKind{namesBatch, observabilityTestsBatch, observabilityNamesBatch}, submissions)
	_, cached := w.batches.get(observabilityTestsBatch.tag("123"))
	require.False(t, cached, "failed submission must not enter cache")
	failed = false
	submissions = nil
	// Successful batches are suppressed even while extent tags lag visibility.
	require.NoError(t, w.reconcile(testContext(t, w), "123"))
	require.Equal(t, []batchKind{observabilityTestsBatch}, submissions)
	require.Equal(t, float64(1), testutil.ToFloat64(w.metrics.submissions.WithLabelValues(string(observabilityTestsBatch), "error")))
	registry := prometheus.NewRegistry()
	w.RegisterMetrics(registry)
	families, err := registry.Gather()
	require.NoError(t, err)
	require.NotEmpty(t, families)
}

func TestPendingOutcomeRefreshesWithinTTLWithoutBlockingIndependentBatches(t *testing.T) {
	for _, result := range []string{"R", "", "f", "unknown"} {
		for _, injectCache := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/cached=%t", result, injectCache), func(t *testing.T) {
				w := newTestWriter(t)
				now := time.Now().UTC()
				w.metadata.now = func() time.Time { return now }
				w.batches.now = func() time.Time { return now }
				run := testRun("123", now)
				run.OverallResult = result
				// Neither boolean is allowed to override a nonterminal verdict.
				run.Failed, run.Succeeded = true, true
				if injectCache {
					w.metadata.put("123", runMetadata{run: run, release: "Presubmits"})
				}
				reads, detailReads := 0, 0
				w.client.Transport = ingestionTransport(func(*http.Request) (*http.Response, error) {
					reads++
					return respondRuns(t, run), nil
				})
				w.jobDetail = func(context.Context, *http.Client, string) (runDetail, error) {
					detailReads++
					return runDetail{FinishedAt: now}, nil
				}
				var submitted []batchKind
				var outcome ciJobOutcome
				w.submit = func(_ context.Context, kind batchKind, _ BuildID, payload *bytes.Buffer) error {
					submitted = append(submitted, kind)
					if kind == runBatch {
						require.NoError(t, json.NewDecoder(payload).Decode(&outcome))
					}
					return nil
				}
				var reports []BatchReport
				w.report = func(report BatchReport) { reports = append(reports, report) }
				for range 2 {
					reports = nil
					require.ErrorContains(t, w.reconcile(testContext(t, w), "123"), "job outcome pending")
					var runReports []BatchReport
					for _, report := range reports {
						if report.Tag == runTag("123") {
							runReports = append(runReports, report)
						}
					}
					require.Len(t, runReports, 1)
					require.Equal(t, "error", runReports[0].Status)
					require.Contains(t, runReports[0].Error, "job outcome pending")
					_, cached := w.batches.get(runTag("123"))
					require.False(t, cached, "pending outcomes must not consume the immutable run tag")
				}
				require.Equal(t, 2, reads, "pending metadata must be refreshed without cache expiry")
				require.Zero(t, detailReads, "a nonzero finishedAt must not override pending Sippy metadata")
				require.Equal(t, []batchKind{testsBatch, namesBatch, observabilityTestsBatch, observabilityNamesBatch}, submitted)
				run.OverallResult, run.Failed, run.Succeeded = "F", false, false
				run.FailedTestNames = []string{"final failure"}
				require.NoError(t, w.reconcile(testContext(t, w), "123"))
				require.Equal(t, 3, reads)
				require.Equal(t, 1, detailReads)
				require.Equal(t, []batchKind{testsBatch, namesBatch, observabilityTestsBatch, observabilityNamesBatch, runBatch}, submitted)
				require.Equal(t, "F", outcome.OverallResult)
				require.Equal(t, 1, outcome.TestFailures)
				require.True(t, outcome.Failed)
				require.Equal(t, now, outcome.FinishedAt)
				require.NoError(t, w.reconcile(testContext(t, w), "123"))
				require.Len(t, submitted, 5, "accepted batches must not be resubmitted")
			})
		}
	}
}

func TestTerminalOutcomeWithMissingFinishedRecord(t *testing.T) {
	for _, result := range []string{"F", "A"} {
		t.Run(result, func(t *testing.T) {
			w := newTestWriter(t)
			// Exercise the API JSON fixture, not just an injected metadata value.
			body := fmt.Sprintf(`{"rows":[{"prow_id":"123","job":"periodic-ARO-HCP","url":%q,"overall_result":%q,"failed":false,"succeeded":false}]}`, artifactTestURL, result)
			artifacts := artifactClient(t, nil, map[string]int{
				artifactTestPrefix + "/prowjob.json":  http.StatusNotFound,
				artifactTestPrefix + "/finished.json": http.StatusNotFound,
			})
			var requested []string
			w.client.Transport = ingestionTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "sippy.invalid" {
					return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
				}
				requested = append(requested, r.URL.Path)
				return artifacts.Transport.RoundTrip(r)
			})
			w.jobDetail = fetchJobOutcomeDetail
			var submitted []batchKind
			w.submit = func(_ context.Context, kind batchKind, _ BuildID, payload *bytes.Buffer) error {
				submitted = append(submitted, kind)
				if kind == runBatch {
					var outcome ciJobOutcome
					require.NoError(t, json.NewDecoder(payload).Decode(&outcome))
					require.Equal(t, result, outcome.OverallResult)
					require.True(t, outcome.Failed)
					require.Zero(t, outcome.FinishedAt)
					require.Empty(t, outcome.ADOBuildID)
					require.Empty(t, outcome.SvcCluster)
					require.Empty(t, outcome.MgmtCluster)
				}
				return nil
			}
			require.NoError(t, w.reconcile(testContext(t, w), "123"))
			require.Equal(t, batchKinds[:], submitted)
			require.Contains(t, requested, "/test-platform-results/"+artifactTestPrefix+"/finished.json")
			require.Equal(t, float64(2), testutil.ToFloat64(w.metrics.artifacts.WithLabelValues("job", "absent")))
			_, cached := w.batches.get(runTag("123"))
			require.True(t, cached, "terminal Sippy metadata is sufficient without a completion record")
		})
	}
}

func TestCompleteTagsNeedNoMetadataOrDownloads(t *testing.T) {
	w := newTestWriter(t)
	var checked []batchKind
	w.tagExists = func(_ context.Context, kind batchKind, _ BuildID) (bool, error) {
		checked = append(checked, kind)
		return true, nil
	}
	w.client.Transport = ingestionTransport(func(*http.Request) (*http.Response, error) { t.Fatal("metadata downloaded"); return nil, nil })
	require.NoError(t, w.reconcile(testContext(t, w), "123"))
	require.Equal(t, batchKinds[:], checked)
}

func TestEvictedAcceptanceRechecksLiveTagsAndRepairsMissingBatch(t *testing.T) {
	w := newTestWriter(t)
	seedRun(w, "123")
	require.NoError(t, w.reconcile(testContext(t, w), "123"))
	// Simulate restart/eviction: queued acceptance is not proof of durable
	// ingestion, so a missing extent must cause a new submission.
	w.batches = newTTLCache[string, struct{}](20000, 15*time.Minute)
	w.tagExists = func(_ context.Context, kind batchKind, _ BuildID) (bool, error) {
		return kind != namesBatch, nil
	}
	w.jobDetail = func(context.Context, *http.Client, string) (runDetail, error) {
		t.Fatal("existing outcome downloaded")
		return runDetail{}, nil
	}
	w.observabilityRows = func(context.Context, *http.Client, string) ([]ciTestResult, []ciTestName, error) {
		t.Fatal("existing observability batches downloaded")
		return nil, nil, nil
	}
	var submitted []batchKind
	w.submit = func(_ context.Context, kind batchKind, _ BuildID, _ *bytes.Buffer) error {
		submitted = append(submitted, kind)
		return nil
	}
	require.NoError(t, w.reconcile(testContext(t, w), "123"))
	require.Equal(t, []batchKind{namesBatch}, submitted)
}

func TestSourceFailuresDoNotBlockObservability(t *testing.T) {
	for _, source := range []string{"job", "e2e", "tag"} {
		t.Run(source, func(t *testing.T) {
			w := newTestWriter(t)
			seedRun(w, "123")
			failure := errors.New("transient")
			switch source {
			case "job":
				w.jobDetail = func(context.Context, *http.Client, string) (runDetail, error) { return runDetail{}, failure }
			case "e2e":
				w.e2eRows = func(context.Context, *http.Client, string) ([]ciTestResult, []ciTestName, error) {
					return nil, nil, failure
				}
			case "tag":
				w.tagExists = func(_ context.Context, kind batchKind, _ BuildID) (bool, error) {
					if kind == runBatch {
						return false, failure
					}
					return false, nil
				}
			}
			var submitted []batchKind
			w.submit = func(_ context.Context, kind batchKind, _ BuildID, _ *bytes.Buffer) error {
				submitted = append(submitted, kind)
				return nil
			}
			require.ErrorContains(t, w.reconcile(testContext(t, w), "123"), "transient")
			require.Contains(t, submitted, observabilityTestsBatch)
			require.Contains(t, submitted, observabilityNamesBatch)
		})
	}
}

func TestEmptySourcesAreOnlyCachedTemporarily(t *testing.T) {
	w := newTestWriter(t)
	seedRun(w, "123")
	now := time.Now()
	w.batches.now = func() time.Time { return now }
	reads, submitted := 0, 0
	w.e2eRows = func(context.Context, *http.Client, string) ([]ciTestResult, []ciTestName, error) {
		reads++
		return []ciTestResult{}, []ciTestName{}, nil
	}
	w.observabilityRows = w.e2eRows
	w.submit = func(context.Context, batchKind, BuildID, *bytes.Buffer) error { submitted++; return nil }
	require.NoError(t, w.reconcile(testContext(t, w), "123"))
	require.NoError(t, w.reconcile(testContext(t, w), "123"))
	require.Equal(t, 2, reads)
	require.Equal(t, 1, submitted, "empty sources must not produce persistent markers")
	now = now.Add(w.config.CIJobOutcomes.GetCacheTTL())
	require.NoError(t, w.reconcile(testContext(t, w), "123"))
	require.Equal(t, 4, reads)
}

func TestMismatchedMetadataCannotEmit(t *testing.T) {
	w := newTestWriter(t)
	w.metadata.put("123", runMetadata{run: testRun("456", time.Now())})
	w.submit = func(context.Context, batchKind, BuildID, *bytes.Buffer) error {
		t.Fatal("mismatched ID submitted")
		return nil
	}
	w.jobDetail = func(context.Context, *http.Client, string) (runDetail, error) {
		t.Fatal("mismatched URL downloaded")
		return runDetail{}, nil
	}
	require.NoError(t, w.reconcile(testContext(t, w), "123"))
	require.Zero(t, w.batches.size(), "malformed metadata must not populate the batch cache")
}

func TestSkippedSourcesAreNotCached(t *testing.T) {
	w := newTestWriter(t)
	seedRun(w, "123")
	fetchRows := w.e2eRows
	reads := 0
	w.e2eRows = func(context.Context, *http.Client, string) ([]ciTestResult, []ciTestName, error) {
		reads++
		return nil, nil, nil
	}
	w.observabilityRows = w.e2eRows
	var submitted []batchKind
	w.submit = func(_ context.Context, kind batchKind, _ BuildID, _ *bytes.Buffer) error {
		submitted = append(submitted, kind)
		return nil
	}
	for range 2 {
		require.NoError(t, w.reconcile(testContext(t, w), "123"))
		require.Equal(t, 1, w.batches.size(), "only the submitted outcome may be cached")
	}
	require.Equal(t, 4, reads, "nil sources must be rechecked without waiting for cache expiry")
	require.Equal(t, []batchKind{runBatch}, submitted)
	w.e2eRows, w.observabilityRows = fetchRows, fetchRows
	require.NoError(t, w.reconcile(testContext(t, w), "123"))
	require.Equal(t, batchKinds[:], submitted, "later valid artifacts must be submitted immediately")
}

func TestEnrichmentFailureStillSubmitsNames(t *testing.T) {
	for _, testsKind := range []batchKind{testsBatch, observabilityTestsBatch} {
		t.Run(string(testsKind), func(t *testing.T) {
			w := newTestWriter(t)
			seedRun(w, "123")
			failure := errors.New("enrichment unavailable")
			failed := true
			fetch := func(context.Context, *http.Client, string) ([]ciTestResult, []ciTestName, error) {
				names := []ciTestName{{TestID: "test", Name: "valid name"}}
				if failed {
					return nil, names, failure
				}
				return []ciTestResult{{BuildID: "123", TestID: "test"}}, names, nil
			}
			if testsKind == testsBatch {
				w.e2eRows = fetch
			} else {
				w.observabilityRows = fetch
			}
			var submitted []batchKind
			w.submit = func(_ context.Context, kind batchKind, _ BuildID, _ *bytes.Buffer) error {
				submitted = append(submitted, kind)
				return nil
			}
			require.ErrorIs(t, w.reconcile(testContext(t, w), "123"), failure)
			for _, kind := range batchKinds {
				_, cached := w.batches.get(kind.tag("123"))
				if kind == testsKind {
					require.NotContains(t, submitted, kind)
					require.False(t, cached, "nil tests must not be cached")
				} else {
					require.Contains(t, submitted, kind, "names and independent sources must be submitted")
					require.True(t, cached)
				}
			}
			failed = false
			submitted = nil
			require.NoError(t, w.reconcile(testContext(t, w), "123"))
			require.Equal(t, []batchKind{testsKind}, submitted, "retry only the failed enrichment batch")
		})
	}
}

func TestMalformedMetadataIsNotCached(t *testing.T) {
	w := newTestWriter(t)
	run := testRun("123", time.Now())
	run.URL = "malformed"
	reads := 0
	w.client.Transport = ingestionTransport(func(*http.Request) (*http.Response, error) {
		reads++
		return respondRuns(t, run), nil
	})
	var submitted []batchKind
	w.submit = func(_ context.Context, kind batchKind, _ BuildID, _ *bytes.Buffer) error {
		submitted = append(submitted, kind)
		return nil
	}
	require.NoError(t, w.reconcile(testContext(t, w), "123"))
	require.Empty(t, submitted)
	require.Zero(t, w.metadata.size())
	require.Zero(t, w.batches.size())
	run = testRun("123", time.Now())
	require.NoError(t, w.reconcile(testContext(t, w), "123"))
	require.Equal(t, 2, reads, "corrected metadata must be fetched immediately")
	require.Equal(t, batchKinds[:], submitted)
}
