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
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/snapshot"
)

// A skipped test is stamped with start and end times just as a passing one is,
// so an implementation that inferred "ran" from the timestamps would record the
// whole suite. Only the verdict separates them.
func TestTestRowsForKeepsOnlyExecutedTests(t *testing.T) {
	ran := time.Date(2026, 8, 25, 21, 13, 49, 0, time.UTC)

	results := []snapshot.TestResult{
		{Name: "installs a cluster", Result: "passed", StartTime: ran, EndTime: ran.Add(time.Minute)},
		{Name: "skipped on this platform", Result: "skipped", StartTime: ran, EndTime: ran},
		{Name: "deletes a node pool", Result: "failed", Failed: true, StartTime: ran, EndTime: ran.Add(time.Minute)},
		{Name: "[sig-sippy] infrastructure should work", Result: "failed", Failed: true, StartTime: ran, EndTime: ran},
	}

	rows, names := testRowsFor("2092349199822622720", results)

	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2 (skipped and Sippy's own tests excluded): %+v", len(rows), rows)
	}
	if len(names) != 2 {
		t.Fatalf("got %d names, want 2", len(names))
	}
	for _, row := range rows {
		if row.BuildID != "2092349199822622720" {
			t.Errorf("row is not keyed by the run: %q", row.BuildID)
		}
	}
	if rows[0].Result != "passed" || rows[0].Failed {
		t.Errorf("passing test recorded as %q failed=%v", rows[0].Result, rows[0].Failed)
	}
	if rows[1].Result != "failed" || !rows[1].Failed {
		t.Errorf("failing test recorded as %q failed=%v", rows[1].Result, rows[1].Failed)
	}
}

// Names are the dimension the join table points at, so a run that exercises the
// same test twice must not produce two rows claiming the same id.
func TestTestRowsForEmitsEachNameOnce(t *testing.T) {
	ran := time.Date(2026, 8, 25, 21, 13, 49, 0, time.UTC)
	results := []snapshot.TestResult{
		{Name: "installs a cluster", Result: "passed", StartTime: ran, EndTime: ran},
		{Name: "installs a cluster", Result: "failed", Failed: true, StartTime: ran, EndTime: ran},
	}

	rows, names := testRowsFor("2092349199822622720", results)

	if len(rows) != 2 {
		t.Fatalf("got %d rows, want both attempts recorded", len(rows))
	}
	if len(names) != 1 {
		t.Fatalf("got %d names, want 1", len(names))
	}
	if rows[0].TestID != rows[1].TestID || rows[0].TestID != names[0].TestID {
		t.Error("both rows and the name must share one id")
	}
}

// The id has to be reproducible from the name alone: it is computed
// independently on every pass and after every restart, and rows written weeks
// apart must still join.
func TestTestIDForIsStableAndDistinct(t *testing.T) {
	first := testIDFor("installs a cluster")
	if first != testIDFor("installs a cluster") {
		t.Error("the same name must always yield the same id")
	}
	if first == testIDFor("installs a cluster ") {
		t.Error("names differing only in trailing space must not collide")
	}
	if first == "" {
		t.Error("id must not be empty")
	}
}

// A run still in progress has no finished.json. That is the normal case for the
// newest runs in every window, so it must not be reported as a failure.
func TestFetchFinishedAtToleratesAMissingRecord(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()

	finishedAt, err := fetchFinishedAtFrom(context.Background(), server.Client(), server.URL)
	if err != nil {
		t.Fatalf("a missing finished.json must not be an error, got %v", err)
	}
	if !finishedAt.IsZero() {
		t.Errorf("got %v, want the zero time", finishedAt)
	}
}

func TestFetchFinishedAtReadsTheCompletionTime(t *testing.T) {
	for _, bucket := range []string{"test-platform-results", "test-platform-results-public"} {
		t.Run(bucket, func(t *testing.T) {
			const prefix = "logs/periodic-ci-Azure-ARO-HCP-main-e2e-parallel/1234567890"
			info, err := snapshot.ParseProwURL("https://prow.ci.openshift.org/view/gs/" + bucket + "/" + prefix)
			if err != nil {
				t.Fatal(err)
			}
			client := &http.Client{Transport: ingestionTransport(func(r *http.Request) (*http.Response, error) {
				wantURL := "https://storage.googleapis.com/" + bucket + "/" + prefix + "/finished.json"
				if r.Method != http.MethodGet || r.URL.String() != wantURL {
					return nil, fmt.Errorf("unexpected completion request: %s %s", r.Method, r.URL)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"timestamp":1787696844,"passed":false,"result":"ABORTED"}`)),
				}, nil
			})}
			finishedAt, err := fetchFinishedAt(t.Context(), client, info.GCSBucket, info.GCSPrefix)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if want := time.Unix(1787696844, 0).UTC(); !finishedAt.Equal(want) {
				t.Errorf("got %v, want %v", finishedAt, want)
			}
		})
	}
}

func TestFetchADOBuildID(t *testing.T) {
	for _, bucket := range []string{"test-platform-results", "test-platform-results-public"} {
		for _, tc := range []struct {
			name, body, want, wantErr string
			status                    int
		}{
			{name: "rollout", status: 200, body: `{"metadata":{"annotations":{"ev2.rollout/build":"181589814"}},"status":{"build_id":"2100631679885381632"}}`, want: "181589814"},
			{name: "identifier preserved", status: 200, body: `{"metadata":{"annotations":{"ev2.rollout/build":"00123"}}}`, want: "00123"},
			{name: "no rollout annotation", status: 200, body: `{"metadata":{"annotations":{"ev2.rollout/environment":"int"}},"status":{"build_id":"2100631679885381632"}}`},
			{name: "no annotations", status: 200, body: `{"metadata":{}}`},
			{name: "empty annotation", status: 200, body: `{"metadata":{"annotations":{"ev2.rollout/build":""}}}`},
			{name: "missing artifact", status: 404, wantErr: "artifact not found"},
			{name: "forbidden", status: 403, wantErr: "unexpected status 403"},
			{name: "server error", status: 503, wantErr: "unexpected status 503"},
			{name: "malformed JSON", status: 200, body: `{`, wantErr: "failed to parse"},
			{name: "invalid annotation type", status: 200, body: `{"metadata":{"annotations":{"ev2.rollout/build":123}}}`, wantErr: "failed to parse"},
		} {
			t.Run(bucket+"/"+tc.name, func(t *testing.T) {
				const prefix = "logs/branch-ci-Azure-ARO-HCP-main-e2e-integration-e2e-parallel/2100631679885381632"
				client := &http.Client{Transport: ingestionTransport(func(r *http.Request) (*http.Response, error) {
					wantURL := "https://storage.googleapis.com/" + bucket + "/" + prefix + "/prowjob.json"
					if r.Method != http.MethodGet || r.URL.String() != wantURL {
						return nil, fmt.Errorf("unexpected metadata request: %s %s", r.Method, r.URL)
					}
					return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
				})}
				got, err := fetchADOBuildID(t.Context(), client, bucket, prefix)
				if tc.wantErr != "" {
					if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
						t.Fatalf("got error %v, want %q", err, tc.wantErr)
					}
				} else if err != nil {
					t.Fatal(err)
				}
				if got != tc.want {
					t.Errorf("ADO build ID = %q, want %q", got, tc.want)
				}
			})
		}
	}
}

func TestFetchADOBuildIDPreservesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	client := &http.Client{Transport: ingestionTransport(func(r *http.Request) (*http.Response, error) {
		return nil, r.Context().Err()
	})}
	_, err := fetchADOBuildID(ctx, client, "test-platform-results-public", "logs/job/123")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
}
