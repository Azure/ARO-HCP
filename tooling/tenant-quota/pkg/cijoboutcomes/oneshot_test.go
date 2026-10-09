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
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOneShotBoundedSelection(t *testing.T) {
	w := newTestWriter(t)
	since := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	until := since.Add(time.Hour)
	w.cursor = func(context.Context, string) (time.Time, error) { t.Fatal("cursor read"); return time.Time{}, nil }
	var releases []string
	w.client.Transport = ingestionTransport(func(r *http.Request) (*http.Response, error) {
		releases = append(releases, r.URL.Query().Get("release"))
		var filter struct{ Items []map[string]string }
		require.NoError(t, json.Unmarshal([]byte(r.URL.Query().Get("filter")), &filter))
		require.Equal(t, []map[string]string{
			{"columnField": "name", "operatorValue": "contains", "value": "ARO-HCP"},
			{"columnField": "timestamp", "operatorValue": ">=", "value": since.Format(time.RFC3339Nano)},
			{"columnField": "timestamp", "operatorValue": "<", "value": until.Format(time.RFC3339Nano)},
		}, filter.Items)
		if r.URL.Query().Get("release") == "4.21" {
			return respondRuns(t, testRun("30", since.Add(time.Minute)), testRun("20", since), testRun("20", since)), nil
		}
		return respondRuns(t, testRun("40", until), testRun("10", since), testRun("20", since), testRun("1", since.Add(-time.Nanosecond))), nil
	})
	options := OneShotOptions{Releases: []string{"Presubmits", "4.21", "4.21"}, Since: since, Until: until, Limit: 2}
	ids, err := w.selectOnce(testContext(t, w), options)
	require.NoError(t, err)
	require.Equal(t, []BuildID{"10", "20"}, ids, "sort all releases and deduplicate before applying a global cap")
	require.Equal(t, []string{"4.21", "Presubmits"}, releases)
	require.Empty(t, w.lastRepair)
	require.Zero(t, w.queue.Len())
	options.Limit = 10
	ids, err = w.selectOnce(testContext(t, w), options)
	require.NoError(t, err)
	require.Equal(t, []BuildID{"10", "20", "30"}, ids, "job start interval is inclusive/exclusive, even if server ignores filters")
}

func TestOneShotInspectNoKustoAndExactIDDeduplicated(t *testing.T) {
	w := newTestWriter(t)
	w.initializeReadOnly = func(context.Context) error { t.Fatal("inspection initialized query client"); return nil }
	w.initialize = func(context.Context) error { t.Fatal("inspection initialized ingestion client"); return nil }
	w.tagExists = func(context.Context, batchKind, BuildID) (bool, error) { t.Fatal("tag query"); return false, nil }
	w.submit = func(context.Context, batchKind, BuildID, *bytes.Buffer) error {
		t.Fatal("submission in dry mode")
		return nil
	}
	var lookups atomic.Int32
	w.client.Transport = ingestionTransport(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, "sippy.invalid", r.URL.Host, "inspection must not contact Kusto")
		var filter struct{ Items []map[string]string }
		require.NoError(t, json.Unmarshal([]byte(r.URL.Query().Get("filter")), &filter))
		require.Equal(t, []map[string]string{{"columnField": "prow_id", "operatorValue": "=", "value": "123"}}, filter.Items)
		lookups.Add(1)
		return respondRuns(t, testRun("123", time.Now())), nil
	})
	var output bytes.Buffer
	require.NoError(t, w.RunOnce(t.Context(), OneShotOptions{BuildIDs: []BuildID{"123", "123"}, InspectArtifacts: true, Workers: 3}, &output))
	require.Equal(t, int32(1), lookups.Load())
	decoder := json.NewDecoder(&output)
	for _, kind := range batchKinds {
		var report BatchReport
		require.NoError(t, decoder.Decode(&report))
		require.Equal(t, kind.tag("123"), report.Tag)
		require.Equal(t, "would-submit", report.Status)
		require.Equal(t, "123", string(report.BuildID))
		require.Equal(t, w.target(kind).Table, report.Table)
		require.Equal(t, 1, report.Rows)
		require.Len(t, report.Payload, 1)
	}
	require.ErrorIs(t, decoder.Decode(&BatchReport{}), io.EOF)
}

func TestOneShotJobFilterCaseInsensitive(t *testing.T) {
	w := newTestWriter(t)
	w.config.CIJobOutcomes.JobFilter = "ArO-HcP"
	now := time.Now().UTC()
	w.client.Transport = ingestionTransport(func(*http.Request) (*http.Response, error) {
		var runs []sippyRun
		for i, name := range []string{"periodic-aro-hcp", "periodic-ARO-HCP", "periodic-ArO-HcP", "unrelated"} {
			run := testRun(string(rune('1'+i)), now)
			run.Job = name
			runs = append(runs, run)
		}
		return respondRuns(t, runs...), nil
	})
	ids, err := w.selectOnce(testContext(t, w), OneShotOptions{Releases: []string{"Presubmits"}, Since: now, Until: now.Add(time.Hour), Limit: 10})
	require.NoError(t, err)
	require.Equal(t, []BuildID{"1", "2", "3"}, ids, "local defensive filter must match Sippy's case-insensitive contains")
}

func TestPartialSourceReportsEachBatchOnce(t *testing.T) {
	for _, dryRun := range []bool{true, false} {
		for _, namesAvailable := range []bool{true, false} {
			w := newTestWriter(t)
			seedRun(w, "123")
			w.dryRun = dryRun
			w.e2eRows = func(context.Context, *http.Client, string) ([]ciTestResult, []ciTestName, error) {
				if namesAvailable {
					return nil, []ciTestName{{TestID: "test", Name: "test"}}, errors.New("partial source")
				}
				return []ciTestResult{{BuildID: "123", TestID: "test"}}, nil, errors.New("partial source")
			}
			reports := map[string][]string{}
			w.report = func(report BatchReport) { reports[report.Tag] = append(reports[report.Tag], report.Status) }
			require.ErrorContains(t, w.reconcile(testContext(t, w), "123"), "partial source")
			available, failed := namesBatch, testsBatch
			if !namesAvailable {
				available, failed = testsBatch, namesBatch
			}
			status := "submitted"
			if dryRun {
				status = "would-submit"
			}
			require.Equal(t, []string{status}, reports[available.tag("123")], "usable batch must not also report a source error")
			require.Equal(t, []string{"error"}, reports[failed.tag("123")])
		}
	}
}

func TestOneShotDefaultChecksLiveTagsWithoutIngestors(t *testing.T) {
	for _, exists := range []bool{true, false} {
		w := newTestWriter(t)
		w.initialize = func(context.Context) error { t.Fatal("dry-run initialized ingestors"); return nil }
		w.initializeReadOnly = func(ctx context.Context) error { return w.initializeKustoWithCredential(ctx, false, nil) }
		// Local Kusto endpoints skip authentication, using the real query client
		// and initializer with a transport that rejects all non-read requests.
		w.config.CIJobOutcomes.ClusterURI = "http://localhost"
		w.config.CIJobOutcomes.IngestionURI = ":invalid-ingestion-endpoint"
		w.config.CIJobOutcomes.Database = "ServiceLogs"
		seedRun(w, "123")
		for _, kind := range batchKinds {
			w.batches.put(kind.tag("123"), struct{}{})
		}
		w.submit = func(context.Context, batchKind, BuildID, *bytes.Buffer) error {
			t.Fatal("dry-run submitted")
			return nil
		}
		var queries atomic.Int32
		w.client.Transport = ingestionTransport(func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodGet {
				return &http.Response{StatusCode: http.StatusNotFound, Body: http.NoBody}, nil
			}
			var request struct{ CSL string }
			require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
			require.Contains(t, request.CSL, "extents where tags has")
			require.NotContains(t, request.CSL, "mapping")
			queries.Add(1)
			rows := `[]`
			if exists {
				rows = `[["extent"]]`
			}
			body := `{"Tables":[{"TableName":"Table","Columns":[{"ColumnName":"ExtentId","ColumnType":"string"}],"Rows":` + rows + `}]}`
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
		})
		var output bytes.Buffer
		require.NoError(t, w.RunOnce(t.Context(), OneShotOptions{BuildIDs: []BuildID{"123"}}, &output))
		require.Equal(t, int32(5), queries.Load(), "fresh commands must check live tags regardless of cached batches")
		status := "would-submit"
		if exists {
			status = "existing"
		}
		require.Equal(t, 5, strings.Count(output.String(), `"status":"`+status+`"`))
	}
}

func TestOneShotIngestOptIn(t *testing.T) {
	w := newTestWriter(t)
	seedRun(w, "123")
	initialized, closed, submitted := 0, 0, 0
	w.initialize = func(context.Context) error { initialized++; return nil }
	w.closeClients = func() { closed++ }
	w.initializeReadOnly = func(context.Context) error { t.Fatal("wrong initializer"); return nil }
	w.submit = func(context.Context, batchKind, BuildID, *bytes.Buffer) error { submitted++; return nil }
	var output bytes.Buffer
	require.NoError(t, w.RunOnce(t.Context(), OneShotOptions{BuildIDs: []BuildID{"123"}, Ingest: true}, &output))
	require.Equal(t, 1, initialized)
	require.Equal(t, 1, closed)
	require.Equal(t, 5, submitted)
	require.Equal(t, 5, strings.Count(output.String(), `"status":"submitted"`))
	require.NotContains(t, output.String(), `"payload"`)
}

func TestOneShotInitializationFailsWithoutRetry(t *testing.T) {
	recoverPanics(t)
	for _, panicking := range []bool{true, false} {
		w := newTestWriter(t)
		calls := 0
		w.initializeReadOnly = func(context.Context) error {
			calls++
			if panicking {
				panic("initialization failed")
			}
			return errors.New("initialization failed")
		}
		require.ErrorContains(t, w.RunOnce(t.Context(), OneShotOptions{BuildIDs: []BuildID{"123"}}, io.Discard), "initialization failed")
		require.Equal(t, 1, calls)
	}
}

func TestOneShotOutputErrors(t *testing.T) {
	w := newTestWriter(t)
	seedRun(w, "123")
	require.ErrorContains(t, w.RunOnce(t.Context(), OneShotOptions{BuildIDs: []BuildID{"123"}, InspectArtifacts: true}, failingOutput{}), "write report")
}

type failingOutput struct{}

func (failingOutput) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

func TestOneShotErrorsAndPanicsAreFinite(t *testing.T) {
	recoverPanics(t)
	for _, panicking := range []bool{false, true} {
		w := newTestWriter(t)
		seedRun(w, "123")
		seedRun(w, "456")
		var calls atomic.Int32
		w.jobDetail = func(_ context.Context, _ *http.Client, url string) (runDetail, error) {
			calls.Add(1)
			if strings.HasSuffix(url, "/123") {
				if panicking {
					panic("broken artifact")
				}
				return runDetail{}, errors.New("broken artifact")
			}
			return runDetail{}, nil
		}
		var output bytes.Buffer
		err := w.RunOnce(t.Context(), OneShotOptions{BuildIDs: []BuildID{"123", "456", "123"}, InspectArtifacts: true, Workers: 2}, &output)
		require.ErrorContains(t, err, "broken artifact")
		require.Equal(t, int32(2), calls.Load(), "each unique key attempted once, including after panic")
		require.Contains(t, output.String(), `"tag":"run-456"`)
		require.Contains(t, output.String(), `"status":"error"`)
		require.Zero(t, w.queue.NumRequeues("123"))
	}
}

func TestOneShotCancellation(t *testing.T) {
	for _, alreadyCancelled := range []bool{true, false} {
		w := newTestWriter(t)
		seedRun(w, "123")
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		if alreadyCancelled {
			cancel()
		}
		w.jobDetail = func(ctx context.Context, _ *http.Client, _ string) (runDetail, error) {
			cancel()
			return runDetail{}, ctx.Err()
		}
		require.ErrorIs(t, w.RunOnce(ctx, OneShotOptions{BuildIDs: []BuildID{"123"}, InspectArtifacts: true}, io.Discard), context.Canceled)
	}
}

func TestOneShotReportsEmptyUnavailableAndPartialSelection(t *testing.T) {
	w := newTestWriter(t)
	w.e2eRows = func(context.Context, *http.Client, string) ([]ciTestResult, []ciTestName, error) {
		return []ciTestResult{}, []ciTestName{}, nil
	}
	w.observabilityRows = func(context.Context, *http.Client, string) ([]ciTestResult, []ciTestName, error) {
		return nil, nil, nil
	}
	now := time.Now().UTC()
	w.client.Transport = ingestionTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("release") == "4.21" {
			return nil, errors.New("selection failed")
		}
		return respondRuns(t, testRun("123", now)), nil
	})
	var output bytes.Buffer
	err := w.RunOnce(t.Context(), OneShotOptions{Releases: []string{"4.21", "Presubmits"}, Since: now, Until: now.Add(time.Hour), Limit: 1, InspectArtifacts: true}, &output)
	require.ErrorContains(t, err, "selection failed")
	require.Equal(t, 2, strings.Count(output.String(), `"status":"empty"`))
	require.Equal(t, 2, strings.Count(output.String(), `"status":"unavailable"`))
	require.Equal(t, 1, strings.Count(output.String(), `"status":"would-submit"`))
}
