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
	"net/http"
	"net/http/httptest"
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"timestamp":1787696844,"passed":false,"result":"ABORTED"}`))
	}))
	defer server.Close()

	finishedAt, err := fetchFinishedAtFrom(context.Background(), server.Client(), server.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := time.Unix(1787696844, 0).UTC()
	if !finishedAt.Equal(want) {
		t.Errorf("got %v, want %v", finishedAt, want)
	}
}
