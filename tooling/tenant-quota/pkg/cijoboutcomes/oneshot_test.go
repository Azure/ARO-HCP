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
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOneShotInspectCanonicalURIDeduplicated(t *testing.T) {
	w := newTestWriter(t)
	w.initializeReadOnly = func(context.Context) error { t.Fatal("inspection initialized query client"); return nil }
	w.initialize = func(context.Context) error { t.Fatal("inspection initialized ingestion client"); return nil }
	w.tagExists = func(context.Context, batchKind, BuildID) (bool, error) { t.Fatal("tag query"); return false, nil }
	w.processedExists = func(context.Context, BuildID) (bool, error) { t.Fatal("processed query"); return false, nil }
	w.submit = func(context.Context, batchKind, BuildID, *bytes.Buffer) error {
		t.Fatal("dry-run submitted")
		return nil
	}
	var lookups atomic.Int32
	w.completion = func(_ context.Context, _ *http.Client, uri string) (*prowCompletion, error) {
		require.Equal(t, string(testJobURI), uri)
		lookups.Add(1)
		return &prowCompletion{Result: "SUCCESS", FinishedAt: w.now().Add(-time.Hour)}, nil
	}
	var output bytes.Buffer
	require.NoError(t, w.RunOnce(t.Context(), OneShotOptions{JobURIs: []JobURI{testJobURI, testJobURI + "/"}, InspectArtifacts: true, Workers: 3}, &output))
	require.Equal(t, int32(1), lookups.Load())
	decoder := json.NewDecoder(&output)
	for _, kind := range batchKinds {
		var report BatchReport
		require.NoError(t, decoder.Decode(&report))
		require.Equal(t, kind.tag(testBuildID), report.Tag)
		require.Equal(t, "would-submit", report.Status)
		require.Equal(t, testJobURI, report.JobURI)
		require.Equal(t, testBuildID, report.BuildID)
		require.Equal(t, w.target(kind).Table, report.Table)
		require.Equal(t, 1, report.Rows)
		require.Len(t, report.Payload, 1)
	}
	var waiting BatchReport
	require.NoError(t, decoder.Decode(&waiting))
	require.Equal(t, "waiting", waiting.Status)
	require.ErrorIs(t, decoder.Decode(&BatchReport{}), io.EOF)
	require.True(t, w.queue.ShuttingDown())
	require.True(t, w.discoveryQueue.ShuttingDown())
}

func TestPartialSourceReportsEachBatchOnce(t *testing.T) {
	for _, dryRun := range []bool{true, false} {
		for _, namesAvailable := range []bool{true, false} {
			w := newTestWriter(t)
			w.dryRun = dryRun
			w.e2eRows = func(context.Context, *http.Client, string) ([]ciTestResult, []ciTestName, error) {
				if namesAvailable {
					return nil, []ciTestName{{TestID: "test", Name: "test"}}, errors.New("partial source")
				}
				return []ciTestResult{{BuildID: string(testBuildID), TestID: "test"}}, nil, errors.New("partial source")
			}
			reports := map[string][]string{}
			w.report = func(report BatchReport) { reports[report.Tag] = append(reports[report.Tag], report.Status) }
			_, err := w.reconcile(testContext(t, w), testJobURI)
			require.ErrorContains(t, err, "partial source")
			available, failed := namesBatch, testsBatch
			if !namesAvailable {
				available, failed = testsBatch, namesBatch
			}
			status := "submitted"
			if dryRun {
				status = "would-submit"
			}
			require.Equal(t, []string{status}, reports[available.tag(testBuildID)])
			require.Equal(t, []string{"error"}, reports[failed.tag(testBuildID)])
		}
	}
}

func TestOneShotDefaultChecksLiveTagsWithoutIngestors(t *testing.T) {
	for _, exists := range []bool{true, false} {
		w := newTestWriter(t)
		w.initialize = func(context.Context) error { t.Fatal("dry-run initialized ingestors"); return nil }
		w.initializeReadOnly = func(ctx context.Context) error { return w.initializeKustoWithCredential(ctx, false, nil) }
		w.config.CIJobOutcomes.ClusterURI = "http://localhost"
		w.config.CIJobOutcomes.IngestionURI = ":invalid-ingestion-endpoint"
		w.config.CIJobOutcomes.Database = "ServiceLogs"
		for _, kind := range batchKinds {
			w.batches.put(kind.tag(testBuildID), struct{}{})
		}
		w.submit = func(context.Context, batchKind, BuildID, *bytes.Buffer) error {
			t.Fatal("dry-run submitted")
			return nil
		}
		w.submitProcessed = func(context.Context, BuildID) error { t.Fatal("dry-run acknowledged"); return nil }
		var queries atomic.Int32
		w.client.Transport = ingestionTransport(func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodGet {
				return &http.Response{StatusCode: http.StatusNotFound, Body: http.NoBody}, nil
			}
			var request struct{ CSL string }
			require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
			require.NotContains(t, request.CSL, "mapping")
			if strings.Contains(request.CSL, "ciProcessedJobs") {
				body := kustoResult("found", `[]`)
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			}
			require.Contains(t, request.CSL, "extents where tags has")
			queries.Add(1)
			rows := `[]`
			if exists {
				rows = `[["extent"]]`
			}
			body := `{"Tables":[{"TableName":"Table","Columns":[{"ColumnName":"ExtentId","ColumnType":"string"}],"Rows":` + rows + `}]}`
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
		})
		var output bytes.Buffer
		require.NoError(t, w.RunOnce(t.Context(), OneShotOptions{JobURIs: []JobURI{testJobURI}}, &output))
		require.Equal(t, int32(5), queries.Load(), "fresh commands check live tags regardless of cached batches")
		if exists {
			require.Equal(t, 5, strings.Count(output.String(), `"status":"existing"`))
		} else {
			require.Equal(t, 5, strings.Count(output.String(), `"status":"would-submit"`))
		}
	}
}

func TestOneShotIngestAndPendingReturnWithoutRetry(t *testing.T) {
	for _, pending := range []bool{true, false} {
		w := newTestWriter(t)
		initialized, closed, submitted, completed := 0, 0, 0, 0
		w.initialize = func(context.Context) error { initialized++; return nil }
		w.closeClients = func() { closed++ }
		w.initializeReadOnly = func(context.Context) error { t.Fatal("wrong initializer"); return nil }
		w.submit = func(context.Context, batchKind, BuildID, *bytes.Buffer) error { submitted++; return nil }
		w.completion = func(context.Context, *http.Client, string) (*prowCompletion, error) {
			completed++
			if pending {
				return nil, nil
			}
			return &prowCompletion{Result: "SUCCESS", FinishedAt: w.now().Add(-time.Hour)}, nil
		}
		var output bytes.Buffer
		require.NoError(t, w.RunOnce(t.Context(), OneShotOptions{JobURIs: []JobURI{testJobURI}, Ingest: true}, &output))
		require.Equal(t, 1, initialized)
		require.Equal(t, 1, closed)
		require.Equal(t, 1, completed)
		if pending {
			require.Zero(t, submitted)
			require.Contains(t, output.String(), `"status":"pending"`)
		} else {
			require.Equal(t, 5, submitted)
			require.Equal(t, 5, strings.Count(output.String(), `"status":"submitted"`))
		}
		require.Contains(t, output.String(), `"status":"waiting"`)
		require.NotContains(t, output.String(), `"payload"`)
		require.Zero(t, w.queue.Len())
	}
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
		require.ErrorContains(t, w.RunOnce(t.Context(), OneShotOptions{JobURIs: []JobURI{testJobURI}}, io.Discard), "initialization failed")
		require.Equal(t, 1, calls)
	}
}

func TestOneShotOutputErrors(t *testing.T) {
	w := newTestWriter(t)
	require.ErrorContains(t, w.RunOnce(t.Context(), OneShotOptions{JobURIs: []JobURI{testJobURI}, InspectArtifacts: true}, failingOutput{}), "write report")
}

type failingOutput struct{}

func (failingOutput) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

func TestOneShotErrorsAndPanicsAreFinite(t *testing.T) {
	recoverPanics(t)
	for _, panicking := range []bool{false, true} {
		w := newTestWriter(t)
		other := JobURI(strings.ReplaceAll(string(testJobURI), string(testBuildID), "1976270000000000456"))
		var calls atomic.Int32
		w.jobDetail = func(_ context.Context, _ *http.Client, url string) (runDetail, error) {
			calls.Add(1)
			if strings.HasSuffix(url, string(testBuildID)) {
				if panicking {
					panic("broken artifact")
				}
				return runDetail{}, errors.New("broken artifact")
			}
			return runDetail{}, nil
		}
		var output bytes.Buffer
		err := w.RunOnce(t.Context(), OneShotOptions{JobURIs: []JobURI{testJobURI, other, testJobURI}, InspectArtifacts: true, Workers: 2}, &output)
		require.ErrorContains(t, err, "broken artifact")
		require.Equal(t, int32(2), calls.Load(), "each unique key attempted once, including after panic")
		require.Contains(t, output.String(), `"tag":"run-1976270000000000456"`)
		require.Contains(t, output.String(), `"status":"error"`)
		require.Zero(t, w.queue.NumRequeues(testJobURI))
	}
}

func TestOneShotCancellation(t *testing.T) {
	for _, alreadyCancelled := range []bool{true, false} {
		w := newTestWriter(t)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		if alreadyCancelled {
			cancel()
		}
		w.jobDetail = func(ctx context.Context, _ *http.Client, _ string) (runDetail, error) {
			cancel()
			return runDetail{}, ctx.Err()
		}
		require.ErrorIs(t, w.RunOnce(ctx, OneShotOptions{JobURIs: []JobURI{testJobURI}, InspectArtifacts: true}, io.Discard), context.Canceled)
	}
}

// Exercise the real GCS enumerator, including root/run pagination and PR aliases.
func discoveryFixture(t *testing.T, w *Writer, allHistory, failScope bool) []JobURI {
	t.Helper()
	pull := "pull-ci-Azure-ARO-HCP-test"
	periodic := "periodic-ci-Azure-ARO-HCP-test"
	branch := "branch-ci-Azure-ARO-HCP-test"
	aliasURI := JobURI("gs://test-platform-results-public/pr-logs/pull/Azure_ARO-HCP/123/" + pull + "/" + string(testBuildID))
	batchURI := JobURI("gs://test-platform-results-public/pr-logs/pull/batch/" + pull + "/" + string(testBuildID))
	branchURI := JobURI("gs://test-platform-results-public/logs/" + branch + "/" + string(testBuildID))
	w.jobCursor = func(context.Context, GCSJob) (string, error) { t.Fatal("live cursor read"); return "", nil }
	w.pendingJobs = func(context.Context) ([]JobURI, error) { t.Fatal("pending jobs read"); return nil, nil }
	w.client.Transport = ingestionTransport(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, "storage.googleapis.com", r.URL.Host, "discovery must not contact Sippy, artifacts, or Kusto")
		require.Equal(t, http.MethodGet, r.Method)
		q := r.URL.Query()
		require.Equal(t, "/", q.Get("delimiter"))
		prefix := q.Get("prefix")
		page := gcsListPage{}
		switch prefix {
		case "pr-logs/directory/pull-ci-Azure-ARO-HCP-":
			page.Prefixes = []string{"pr-logs/directory/" + pull + "/"}
			if q.Get("pageToken") == "" {
				page.NextPageToken = "root-next"
			}
		case "pr-logs/pull/batch/pull-ci-Azure-ARO-HCP-":
			page.Prefixes = []string{"pr-logs/pull/batch/" + pull + "/"}
		case "logs/branch-ci-Azure-ARO-HCP-":
			page.Prefixes = []string{"logs/" + branch + "/"}
		case "logs/periodic-ci-Azure-ARO-HCP-":
			page.Prefixes = []string{"logs/" + periodic + "/"}
		default:
			if allHistory {
				require.Empty(t, q.Get("startOffset"))
			} else {
				require.NotEmpty(t, q.Get("startOffset"))
			}
			require.NotEmpty(t, q.Get("endOffset"))
			if failScope && strings.Contains(prefix, branch) {
				return nil, errors.New("scope failed")
			}
			if prefix == "pr-logs/directory/"+pull+"/" {
				page.Items = []gcsObject{{Name: prefix + string(testBuildID) + ".txt", Metadata: map[string]string{"x-goog-meta-link": string(aliasURI) + "/"}}}
			} else {
				page.Prefixes = []string{prefix + string(testBuildID) + "/"}
			}
			if q.Get("pageToken") == "" {
				page.NextPageToken = "run-next"
			}
		}
		body, err := json.Marshal(page)
		require.NoError(t, err)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(body))}, nil
	})
	w.initializeReadOnly = func(context.Context) error { t.Fatal("discovery initialized query client"); return nil }
	w.initialize = func(context.Context) error { return nil }
	ids := []JobURI{testJobURI, aliasURI, batchURI, branchURI}
	slices.Sort(ids)
	return ids
}

func TestDiscoveryCanonicalSortBeforeLimitAndSelection(t *testing.T) {
	w := newTestWriter(t)
	ids := discoveryFixture(t, w, false, false)
	stamp, err := BuildIDTime(string(testBuildID))
	require.NoError(t, err)
	options := OneShotOptions{Discovery: true, Since: stamp.Add(-time.Hour), Until: stamp, Limit: 2}
	actual, err := w.selectOnce(t.Context(), options)
	require.NoError(t, err)
	require.Equal(t, ids[:2], actual)
	options.JobNames = []string{"pull-ci-Azure-ARO-HCP-test", "pull-ci-Azure-ARO-HCP-test"}
	actual, err = w.selectOnce(t.Context(), options)
	require.NoError(t, err)
	require.Equal(t, ids[2:], actual, "select the same name in both PR and batch scopes, deduplicating aliases")
	options.JobNames = []string{"unknown"}
	_, err = w.selectOnce(t.Context(), options)
	require.ErrorContains(t, err, "did not match")
	require.Empty(t, w.lastRepair)
	require.Zero(t, w.queue.Len())
	require.Zero(t, w.discoveryQueue.Len())
}

func TestDiscoveryBuildIDOrderBeforeLimitAndBulk(t *testing.T) {
	for _, bulk := range []bool{false, true} {
		w := newTestWriter(t)
		ids := discoveryFixture(t, w, true, false)
		// Lexical URI order would put the newer logs first and PR 999 last.
		older := JobURI(strings.ReplaceAll(strings.ReplaceAll(string(ids[2]), "/123/", "/999/"), string(testBuildID), "1976270000000000001"))
		newerBranch := JobURI(strings.ReplaceAll(string(ids[0]), string(testBuildID), "1976270000000000456"))
		newerPeriodic := JobURI(strings.ReplaceAll(string(ids[1]), string(testBuildID), "1976270000000000789"))
		want := []JobURI{older, ids[2], ids[3], newerBranch, newerPeriodic}
		transport := w.client.Transport
		w.client.Transport = ingestionTransport(func(r *http.Request) (*http.Response, error) {
			response, err := transport.RoundTrip(r)
			if err != nil {
				return nil, err
			}
			defer response.Body.Close()
			var page gcsListPage
			require.NoError(t, json.NewDecoder(response.Body).Decode(&page))
			for i, prefix := range page.Prefixes {
				if strings.HasSuffix(prefix, string(testBuildID)+"/") {
					id := "1976270000000000456"
					if strings.Contains(prefix, "periodic-") {
						id = "1976270000000000789"
					}
					if strings.HasPrefix(prefix, "logs/") {
						page.Prefixes[i] = strings.ReplaceAll(prefix, string(testBuildID), id)
					}
				}
			}
			if len(page.Items) > 0 {
				page.Items = append(page.Items, gcsObject{
					Name:     strings.ReplaceAll(page.Items[0].Name, string(testBuildID), "1976270000000000001"),
					Metadata: map[string]string{"x-goog-meta-link": string(older) + "/"},
				})
			}
			body, err := json.Marshal(page)
			require.NoError(t, err)
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(body))}, nil
		})
		// All IDs have the same millisecond timestamp: the inclusive upper bound
		// includes every sequence value, and sorting must use the full build ID.
		until, err := BuildIDTime(string(testBuildID))
		require.NoError(t, err)
		options := OneShotOptions{Discovery: true, AllHistory: true, Until: until, Bulk: bulk, Workers: 1}
		if !bulk {
			options.Limit = 3
			want = want[:3]
		}
		var output bytes.Buffer
		require.NoError(t, w.RunOnce(t.Context(), options, &output))
		var actual []JobURI
		decoder := json.NewDecoder(&output)
		for decoder.More() {
			var report BatchReport
			require.NoError(t, decoder.Decode(&report))
			for _, payload := range report.Payload {
				var row DiscoveredJob
				require.NoError(t, json.Unmarshal(payload, &row))
				actual = append(actual, row.JobURI)
			}
		}
		require.Equal(t, want, actual, "deduplicate all pages, sort full IDs then URIs, and only then limit")
	}
}

func TestDiscoveryBulkSingleStableBatch(t *testing.T) {
	var dryTag string
	for _, ingest := range []bool{false, true} {
		w := newTestWriter(t)
		ids := discoveryFixture(t, w, true, false)
		calls := 0
		w.discoveredExists = func(context.Context, JobURI) (bool, error) { t.Fatal("bulk queried individual row"); return false, nil }
		w.submitDiscovery = func(_ context.Context, rows []DiscoveredJob, tag string) error {
			calls++
			require.True(t, ingest)
			require.Equal(t, dryTag, tag, "tag derives only from canonical sorted content")
			require.Len(t, rows, len(ids))
			for i, row := range rows {
				require.Equal(t, ids[i], row.JobURI)
				require.Equal(t, string(testBuildID), row.BuildID, "do not deduplicate different URIs sharing an ID")
			}
			return nil
		}
		var output bytes.Buffer
		require.NoError(t, w.RunOnce(t.Context(), OneShotOptions{Discovery: true, AllHistory: true, Bulk: true, Until: time.Now(), Ingest: ingest}, &output))
		var report BatchReport
		require.NoError(t, json.Unmarshal(bytes.TrimSpace(output.Bytes()), &report), "exactly one report")
		require.Equal(t, len(ids), report.Rows)
		if ingest {
			require.Equal(t, 1, calls)
			require.Equal(t, "submitted", report.Status)
		} else {
			require.Zero(t, calls)
			require.Len(t, report.Payload, len(ids))
			require.Equal(t, "would-submit", report.Status)
			dryTag = report.Tag
		}
	}
}

func TestDiscoveryErrorsIndependentExceptAtomicBulk(t *testing.T) {
	for _, bulk := range []bool{false, true} {
		w := newTestWriter(t)
		discoveryFixture(t, w, true, true)
		calls := 0
		w.submitDiscovery = func(context.Context, []DiscoveredJob, string) error { calls++; return nil }
		options := OneShotOptions{Discovery: true, AllHistory: true, Until: time.Now(), Ingest: true, Bulk: bulk, Workers: 1}
		if !bulk {
			options.Limit = 100
		}
		var output bytes.Buffer
		require.ErrorContains(t, w.RunOnce(t.Context(), options, &output), "scope failed")
		if bulk {
			require.Zero(t, calls)
		} else {
			require.Equal(t, 3, calls)
		}
	}
}

func TestDiscoveryDryRunNoKustoOrWrites(t *testing.T) {
	w := newTestWriter(t)
	ids := discoveryFixture(t, w, true, false)
	w.initialize = func(context.Context) error { t.Fatal("initialized ingestors"); return nil }
	w.submitDiscovery = func(context.Context, []DiscoveredJob, string) error { t.Fatal("submitted"); return nil }
	var output bytes.Buffer
	require.NoError(t, w.RunOnce(t.Context(), OneShotOptions{Discovery: true, AllHistory: true, Until: time.Now(), Limit: 100, Workers: 2}, &output))
	require.Equal(t, len(ids), strings.Count(output.String(), `"status":"would-submit"`))
	for _, uri := range ids {
		require.Contains(t, output.String(), fmt.Sprintf(`"jobUri":%q`, uri))
	}
}

func TestDiscoverySubmissionFailureDoesNotStopOtherKeys(t *testing.T) {
	w := newTestWriter(t)
	ids := discoveryFixture(t, w, true, false)
	var calls atomic.Int32
	w.submitDiscovery = func(_ context.Context, rows []DiscoveredJob, _ string) error {
		calls.Add(1)
		if rows[0].JobURI == ids[0] {
			return errors.New("submission failed")
		}
		return nil
	}
	var output bytes.Buffer
	err := w.RunOnce(t.Context(), OneShotOptions{Discovery: true, AllHistory: true, Until: time.Now(), Limit: 100, Workers: 2, Ingest: true}, &output)
	require.ErrorContains(t, err, "submission failed")
	require.Equal(t, int32(len(ids)), calls.Load())
	require.Equal(t, len(ids)-1, strings.Count(output.String(), `"status":"submitted"`))
}

func TestDiscoveryBulkPropagatesOutputAndSubmissionErrors(t *testing.T) {
	for _, ingest := range []bool{false, true} {
		w := newTestWriter(t)
		discoveryFixture(t, w, true, false)
		w.submitDiscovery = func(context.Context, []DiscoveredJob, string) error { return errors.New("submission failed") }
		err := w.RunOnce(t.Context(), OneShotOptions{Discovery: true, AllHistory: true, Bulk: true, Until: time.Now(), Ingest: ingest}, failingOutput{})
		require.ErrorContains(t, err, "write report")
		if ingest {
			require.ErrorContains(t, err, "submission failed")
		}
	}
}

func TestOneShotAlreadyProcessedReportsExisting(t *testing.T) {
	w := newTestWriter(t)
	w.initializeReadOnly = func(context.Context) error { return nil }
	w.processedExists = func(context.Context, BuildID) (bool, error) { return true, nil }
	w.completion = func(context.Context, *http.Client, string) (*prowCompletion, error) {
		t.Fatal("processed artifacts read")
		return nil, nil
	}
	var output bytes.Buffer
	require.NoError(t, w.RunOnce(t.Context(), OneShotOptions{JobURIs: []JobURI{testJobURI}}, &output))
	var report BatchReport
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(output.Bytes()), &report))
	require.Equal(t, "existing", report.Status)
	require.Equal(t, testJobURI, report.JobURI)
	require.Equal(t, processedBatch.tag(testBuildID), report.Tag)
}
