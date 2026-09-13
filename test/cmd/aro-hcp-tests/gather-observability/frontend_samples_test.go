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

package gatherobservability

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"

	"github.com/Azure/ARO-HCP/test/util/timing"
)

type frontendSampleCredential struct{}

func (frontendSampleCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "test-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type frontendSampleTransport func(*http.Request) (*http.Response, error)

func (f frontendSampleTransport) Do(r *http.Request) (*http.Response, error) { return f(r) }

const frontendEmptySamples = `{"status":"success","data":{"resultType":"matrix","result":[]}}`

type frontendSampleManifest struct {
	Entries []struct {
		Title  string    `json:"title"`
		Start  time.Time `json:"start"`
		End    time.Time `json:"end"`
		Status string    `json:"status"`
		Path   string    `json:"path"`
	} `json:"entries"`
}

func frontendManifest(t *testing.T, dir string, evidence *evidenceCollector) frontendSampleManifest {
	t.Helper()
	if err := evidence.writeManifest(); err != nil {
		t.Fatalf("write profile manifest: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "evidence", "manifest.json"))
	if err != nil {
		t.Fatalf("read profile manifest: %v", err)
	}
	var manifest frontendSampleManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode profile manifest: %v", err)
	}
	return manifest
}

func TestFrontendSamplesNativeResponseAndChunkBoundaries(t *testing.T) {
	window := timing.TimeWindow{
		Start: time.Date(2025, 1, 2, 3, 4, 5, 123000000, time.UTC),
		End:   time.Date(2025, 1, 2, 3, 19, 5, 123000000, time.UTC),
	}
	// The run start is exactly an adjacent-chunk boundary. Three exact 15m
	// intervals must include that sample without adding a fourth 1ms chunk.
	// Scrape timestamps, bucket/replica labels, special values, warnings, infos,
	// and unfamiliar native histogram data must survive without reconstruction.
	payload := fmt.Sprintf(`{"status":"success","warnings":["partial upstream"],"infos":["native info"],"futureField":{"retained":true},"data":{"resultType":"matrix","result":[{"metric":{"__name__":"frontend_http_requests_duration_seconds_bucket","le":"+Inf","pod":"frontend-a","instance":"10.0.0.1:9090"},"values":[[%s,"+Inf"]]},{"metric":{"__name__":"frontend_http_requests_duration_seconds_bucket","le":"0.5","pod":"frontend-b","instance":"10.0.0.2:9090"},"values":[[%s,"NaN"]],"histograms":[[%s,{"count":"2","sum":"1","buckets":[[0,"0","1","2"]]}]]}]}}`,
		fmt.Sprintf("%d.123", window.Start.Unix()), fmt.Sprintf("%d.123", window.Start.Unix()), fmt.Sprintf("%d.123", window.Start.Unix()))
	type request struct {
		selector   string
		start, end time.Time
	}
	var requests []request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query" {
			t.Errorf("stored samples must use instant query endpoint, got %q", r.URL.Path)
		}
		query := r.URL.Query().Get("query")
		index := strings.LastIndex(query, "[")
		if index < 0 || !strings.HasSuffix(query, "]") {
			t.Errorf("expected a bare range selector, got %q", query)
			http.Error(w, "invalid selector", http.StatusBadRequest)
			return
		}
		duration, err := time.ParseDuration(query[index+1 : len(query)-1])
		if err != nil {
			t.Errorf("invalid range duration: %v", err)
		}
		end, err := time.Parse(time.RFC3339Nano, r.URL.Query().Get("time"))
		if err != nil {
			t.Errorf("invalid fractional instant timestamp: %v", err)
		}
		requests = append(requests, request{query[:index], end.Add(-duration), end})
		w.Header().Set("Content-Type", "application/json")
		if query[:index] == "frontend_http_requests_duration_seconds_bucket" && window.Start.After(end.Add(-duration)) && !window.Start.After(end) {
			_, _ = io.WriteString(w, payload)
		} else {
			_, _ = io.WriteString(w, frontendEmptySamples)
		}
	}))
	defer server.Close()
	dir := t.TempDir()
	evidence, err := newEvidenceCollector(dir, window)
	if err != nil {
		t.Fatal(err)
	}
	collectFrontendSamples(context.Background(), server.Client(), frontendSampleCredential{}, server.URL, window, evidence)
	manifest := frontendManifest(t, dir, evidence)
	if got := manifest.Entries[len(manifest.Entries)-1].Status; got != "complete" {
		t.Fatalf("empty successful families must not make coverage partial, got %q", got)
	}
	wantSelectors := []string{
		"frontend_http_requests_duration_seconds_bucket", "frontend_http_requests_duration_seconds_count",
		"frontend_http_requests_duration_seconds_sum", "frontend_http_requests_total",
		"sli:frontend_http:latency_p99:rate5m", "sli:frontend_http:latency_p95:rate5m",
		`up{service="aro-hcp-frontend-metrics"}`, `scrape_duration_seconds{service="aro-hcp-frontend-metrics"}`,
		`scrape_samples_scraped{service="aro-hcp-frontend-metrics"}`,
	}
	if len(requests) != 3*len(wantSelectors) {
		t.Fatalf("expected three chunks for run plus lookback, got %d requests", len(requests))
	}
	for i, request := range requests {
		if request.selector != wantSelectors[i%len(wantSelectors)] {
			t.Errorf("request %d aggregates, drops a family, or changes replica/bucket labels: %q", i, request.selector)
		}
		if request.end.Nanosecond() != window.End.Nanosecond() {
			t.Errorf("request %d lost fractional timestamp: %s", i, request.end)
		}
		if i >= len(wantSelectors) && !request.end.Equal(requests[i-len(wantSelectors)].start) {
			t.Errorf("adjacent chunk bounds must touch without overlap: %+v and %+v", requests[i-len(wantSelectors)], request)
		}
	}
	if !requests[0].end.Equal(window.End) || !requests[len(requests)-1].start.Equal(window.Start.Add(-30*time.Minute-time.Millisecond)) {
		t.Fatalf("incorrect profile bounds: first=%+v last=%+v", requests[0], requests[len(requests)-1])
	}
	paths, err := filepath.Glob(filepath.Join(dir, "evidence", "*.json.gz"))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		reader, err := gzip.NewReader(file)
		if err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		_ = reader.Close()
		_ = file.Close()
		if err != nil {
			t.Fatal(err)
		}
		if string(data) == payload {
			found = true
		}
	}
	if !found {
		t.Fatal("gzip evidence did not preserve native scrape timestamps, replica/bucket labels, warnings, infos, and histogram fields byte-for-byte")
	}
}

func TestFrontendSamplesFutureCapAndHistoryLimit(t *testing.T) {
	before := time.Now()
	window := timing.TimeWindow{Start: before.Add(-8 * time.Hour), End: before.Add(time.Hour)}
	dir := t.TempDir()
	evidence, err := newEvidenceCollector(dir, window)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	var newest, oldest time.Time
	transport := frontendSampleTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		end, err := time.Parse(time.RFC3339Nano, r.URL.Query().Get("time"))
		if err != nil {
			t.Fatal(err)
		}
		if calls == 1 {
			newest = end
		}
		oldest = end.Add(-15 * time.Minute)
		if !strings.HasSuffix(r.URL.Query().Get("query"), "[900000ms]") {
			t.Errorf("capped history must use bounded 15m chunks, got %q", r.URL.RawQuery)
		}
		deadline, ok := r.Context().Deadline()
		if !ok || deadline.Sub(before) > 2*time.Minute+time.Second {
			t.Errorf("extra profile lacks a hard two-minute deadline: %s", deadline)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(frontendEmptySamples))}, nil
	})
	collectFrontendSamples(context.Background(), transport, frontendSampleCredential{}, "https://svc.example", window, evidence)
	after := time.Now()
	if newest.Before(before) || newest.After(after) || newest.After(window.End) {
		t.Fatalf("future end was not capped to collection start: %s not in [%s,%s]", newest, before, after)
	}
	if calls != 24*9 || newest.Sub(oldest) != 6*time.Hour {
		t.Fatalf("profile exceeded or omitted capped six-hour coverage: calls=%d range=%s", calls, newest.Sub(oldest))
	}
	manifest := frontendManifest(t, dir, evidence)
	var original, future, history bool
	for _, entry := range manifest.Entries {
		original = original || entry.Start.Equal(window.Start) && entry.End.Equal(window.End)
		future = future || entry.Status == "skipped" && entry.Start.Equal(newest) && entry.End.Equal(window.End)
		history = history || entry.Status == "limited" && entry.End.Equal(oldest)
	}
	if !original || !future || !history || manifest.Entries[len(manifest.Entries)-1].Status != "limited" {
		t.Fatalf("manifest must distinguish original window, future skip, omitted history and partial summary: %+v", manifest)
	}
}

func TestFrontendSamplesStopsRemainingCoverage(t *testing.T) {
	for _, reason := range []string{"canceled", "budget", "in-flight cancellation"} {
		t.Run(reason, func(t *testing.T) {
			window := timing.TimeWindow{Start: time.Now().Add(-time.Hour), End: time.Now().Add(-time.Minute)}
			dir := t.TempDir()
			evidence, err := newEvidenceCollector(dir, window)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if reason == "canceled" {
				cancel()
			}
			if reason == "budget" {
				evidence.usedBytes = maxEvidenceBytes
			}
			calls := 0
			transport := frontendSampleTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				cancel()
				return nil, r.Context().Err()
			})
			collectFrontendSamples(ctx, transport, frontendSampleCredential{}, "https://svc.example", window, evidence)
			wantCalls := 0
			if reason == "in-flight cancellation" {
				wantCalls = 1
			}
			if calls != wantCalls {
				t.Fatalf("sent requests after %s: got %d, want %d", reason, calls, wantCalls)
			}
			manifest := frontendManifest(t, dir, evidence)
			if got := manifest.Entries[len(manifest.Entries)-1].Status; got != "limited" {
				t.Fatalf("%s must leave partial rather than successful coverage, got %s", reason, got)
			}
			if len(manifest.Entries) > 4 {
				t.Fatalf("remaining chunks should use one coverage note, not an entry per unissued query: %d", len(manifest.Entries))
			}
		})
	}
}

func TestFrontendSamplesContinuesAfterQueryFailure(t *testing.T) {
	window := timing.TimeWindow{Start: time.Now().Add(-time.Hour), End: time.Now().Add(-59 * time.Minute)}
	dir := t.TempDir()
	evidence, err := newEvidenceCollector(dir, window)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	transport := frontendSampleTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		status, body := http.StatusOK, frontendEmptySamples
		if calls == 1 {
			status, body = http.StatusUnprocessableEntity, `{"status":"error","errorType":"execution","error":"history unavailable"}`
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	collectFrontendSamples(context.Background(), transport, frontendSampleCredential{}, "https://svc.example", window, evidence)
	manifest := frontendManifest(t, dir, evidence)
	if calls != 27 || manifest.Entries[len(manifest.Entries)-1].Status != "limited" {
		t.Fatalf("individual failure must retain other families/chunks without claiming complete coverage: calls=%d entries=%+v", calls, manifest.Entries)
	}
}
