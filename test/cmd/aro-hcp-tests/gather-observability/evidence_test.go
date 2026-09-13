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
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"

	"github.com/Azure/ARO-HCP/test/util/timing"
)

type evidenceTestTransport func(*http.Request) (*http.Response, error)

func (f evidenceTestTransport) Do(req *http.Request) (*http.Response, error) { return f(req) }

type evidenceTestCredential struct{}

func (evidenceTestCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "secret-bearer-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type evidenceTestBody struct {
	io.Reader
	closed   bool
	closeErr error
}

func (b *evidenceTestBody) Close() error {
	b.closed = true
	return b.closeErr
}

func readEvidenceEntries(t *testing.T, collector *evidenceCollector) []evidenceEntry {
	t.Helper()
	if err := collector.writeManifest(); err != nil {
		t.Fatalf("write evidence manifest: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(collector.dir, "manifest.json"))
	if err != nil {
		t.Fatalf("read evidence manifest: %v", err)
	}
	var manifest struct {
		Version int             `json:"version"`
		Entries []evidenceEntry `json:"entries"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode evidence manifest: %v", err)
	}
	if manifest.Version != 1 {
		t.Fatalf("unsupported manifest version: %d", manifest.Version)
	}
	return manifest.Entries
}

func readEvidencePayload(t *testing.T, outputDir, path string) string {
	t.Helper()
	if !filepath.IsLocal(path) {
		t.Fatalf("artifact path escapes the output directory: %q", path)
	}
	f, err := os.Open(filepath.Join(outputDir, path))
	if err != nil {
		t.Fatalf("open native response: %v", err)
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("open gzip response: %v", err)
	}
	defer z.Close()
	data, err := io.ReadAll(z)
	if err != nil {
		t.Fatalf("decompress native response: %v", err)
	}
	return string(data)
}

func TestEvidencePreservesNativeResponseAndPrometheusResult(t *testing.T) {
	t.Parallel()
	const payload = "{\n" + `"status":"success","warnings":["partial data"],"infos":["extra info"],"future":{"opaque":true},"data":{"resultType":"matrix","result":[{"metric":{"__name__":"requests"},"values":[[1700000000.125,"NaN"]],"histograms":[[1700000000.125,{"count":"2","sum":"+Inf","buckets":[[0,"1","2","2"]]}]]}]}}` + "\n"
	dir := t.TempDir()
	at := time.Unix(1700000000, 125000000).UTC()
	collector, err := newEvidenceCollector(dir, timing.TimeWindow{Start: at, End: at.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	body := &evidenceTestBody{Reader: strings.NewReader(payload)}
	base := evidenceTestTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: body}, nil
	})
	client := collector.client(base, evidenceRequest{Kind: "query-result", Source: "prometheus", Title: "../../outside", Start: at, End: at})
	result, err := queryRange(context.Background(), client, evidenceTestCredential{}, "https://example.test", "requests", at, at, "60s")
	if err != nil {
		t.Fatalf("chart query failed during evidence capture: %v", err)
	}
	if !body.closed {
		t.Fatal("query did not close the underlying HTTP body")
	}
	if len(result.Data.Result) != 1 || result.Data.Result[0].Values[0][0] != json.Number("1700000000.125") || result.Data.Result[0].Values[0][1] != "NaN" {
		t.Fatalf("capture changed the chart result: %#v", result.Data.Result)
	}
	if len(result.Warnings) != 1 || result.Warnings[0] != "partial data" || len(result.Infos) != 1 || result.Infos[0] != "extra info" {
		t.Fatalf("query lost API annotations: %#v", result)
	}
	entries := readEvidenceEntries(t, collector)
	if len(entries) != 1 || entries[0].Status != "complete" {
		t.Fatalf("native response not reported complete: %#v", entries)
	}
	if got := readEvidencePayload(t, dir, entries[0].Path); got != payload {
		t.Fatalf("native payload changed:\ngot %s\nwant %s", got, payload)
	}
}

func TestEvidenceOmitsCredentialsAndPreservesNativeHTTPErrors(t *testing.T) {
	t.Parallel()
	const payload = `{"error":{"code":"Forbidden","unknown":{"reason":"denied"}}}`
	dir := t.TempDir()
	collector, err := newEvidenceCollector(dir, timing.TimeWindow{})
	if err != nil {
		t.Fatal(err)
	}
	closeErr := errors.New("underlying close error")
	body := &evidenceTestBody{Reader: strings.NewReader(payload), closeErr: closeErr}
	base := evidenceTestTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusForbidden, Body: body, Header: http.Header{"Set-Cookie": {"secret-cookie"}}}, nil
	})
	req, err := http.NewRequest(http.MethodGet, "https://secret-user:secret-password@example.test/metrics?metricnames=Requests&api-version=2024-01-01&sig=secret-signature&access_token=secret-query-token#secret-fragment", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer secret-authorization")
	req.Header.Set("X-Api-Key", "secret-api-key")
	resp, err := collector.client(base, evidenceRequest{Kind: "query-result", Source: "azureMonitor"}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	got, readErr := io.ReadAll(resp.Body)
	if readErr != nil || string(got) != payload {
		t.Fatalf("capture changed the caller's error response: %s, %v", got, readErr)
	}
	if err := resp.Body.Close(); err != closeErr || !body.closed {
		t.Fatalf("capture changed underlying close behavior: closed=%v error=%v", body.closed, err)
	}
	entries := readEvidenceEntries(t, collector)
	if len(entries) != 1 || entries[0].Status != "failed" || entries[0].Failure != "http" || entries[0].HTTPStatus != http.StatusForbidden {
		t.Fatalf("HTTP error coverage missing: %#v", entries)
	}
	if got := readEvidencePayload(t, dir, entries[0].Path); got != payload {
		t.Fatalf("native HTTP error changed: %s", got)
	}
	manifest, err := os.ReadFile(filepath.Join(collector.dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(manifest), "secret-") || strings.Contains(string(manifest), "Authorization") || strings.Contains(string(manifest), "Set-Cookie") {
		t.Fatalf("manifest leaked request/response credentials: %s", manifest)
	}
	if entries[0].Endpoint != "https://example.test/metrics" || entries[0].Parameters.Get("metricnames") != "Requests" || entries[0].Parameters.Get("api-version") != "2024-01-01" {
		t.Fatalf("manifest lost safe request semantics: %#v", entries[0])
	}
}

func TestEvidenceLimitsAndWriteFailureKeepChartResults(t *testing.T) {
	const small = `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"job":"frontend"},"values":[[1700000000,"2"]]}]}}`
	for _, mode := range []string{"response-limit", "total-limit", "write"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			collector, err := newEvidenceCollector(dir, timing.TimeWindow{})
			if err != nil {
				t.Fatal(err)
			}
			payload := small
			switch mode {
			case "response-limit":
				payload = small[:len(small)-1] + `,"future":"` + strings.Repeat("x", int(maxEvidenceResponseBytes)) + `"}`
			case "total-limit":
				collector.usedBytes = maxEvidenceBytes - 16
			case "write":
				// A regular file where the artifact directory was makes writes
				// fail deterministically, including when tests run as root.
				if err := os.Remove(collector.dir); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(collector.dir, []byte("not a directory"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			body := &evidenceTestBody{Reader: strings.NewReader(payload)}
			base := evidenceTestTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Body: body}, nil
			})
			result, err := queryRange(context.Background(), collector.client(base, evidenceRequest{Kind: "query-result"}), evidenceTestCredential{}, "https://example.test", "requests", time.Time{}, time.Time{}, "60s")
			if err != nil {
				t.Fatalf("capture failure broke chart query: %v", err)
			}
			if len(result.Data.Result) != 1 || result.Data.Result[0].Values[0][1] != "2" || !body.closed {
				t.Fatalf("capture failure changed query result or close: %#v, closed=%v", result, body.closed)
			}
			if mode == "write" {
				if err := os.Remove(collector.dir); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(collector.dir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			entries := readEvidenceEntries(t, collector)
			wantStatus := "limited"
			if mode == "write" {
				wantStatus = "failed"
			}
			if len(entries) != 1 || entries[0].Status != wantStatus || entries[0].Failure != mode || entries[0].Error == "" || entries[0].Path != "" {
				t.Fatalf("missing capture was not explicit: %#v", entries)
			}
			files, err := filepath.Glob(filepath.Join(collector.dir, "*.json.gz"))
			if err != nil || len(files) != 0 {
				t.Fatalf("incomplete evidence published as native JSON: %v, %v", files, err)
			}
		})
	}
}

func TestEvidenceDistinguishesMalformedUnreadAndNetworkFailures(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"malformed-json", "incomplete-body", "network", "read"} {
		t.Run(mode, func(t *testing.T) {
			collector, err := newEvidenceCollector(t.TempDir(), timing.TimeWindow{})
			if err != nil {
				t.Fatal(err)
			}
			originalErr := errors.New("https://secret-user:secret-password@example.test: failed")
			body := &evidenceTestBody{Reader: strings.NewReader(`{"incomplete":`)}
			if mode == "read" {
				body.Reader = evidenceTestReaderError{err: originalErr}
			}
			base := evidenceTestTransport(func(*http.Request) (*http.Response, error) {
				if mode == "network" {
					return nil, originalErr
				}
				return &http.Response{StatusCode: http.StatusOK, Body: body}, nil
			})
			req, err := http.NewRequest(http.MethodGet, "https://example.test", nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := collector.client(base, evidenceRequest{Kind: "query-result"}).Do(req)
			if mode == "network" {
				if err != originalErr {
					t.Fatalf("capture changed transport error: %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if mode != "incomplete-body" {
					_, readErr := io.ReadAll(resp.Body)
					if mode == "read" && readErr != originalErr {
						t.Fatalf("capture changed body read error: %v", readErr)
					}
					if mode == "malformed-json" && readErr != nil {
						t.Fatalf("capture introduced a body read error: %v", readErr)
					}
				}
				if err := resp.Body.Close(); err != nil || !body.closed {
					t.Fatalf("underlying body was not closed: %v", err)
				}
			}
			entries := readEvidenceEntries(t, collector)
			if len(entries) != 1 || entries[0].Failure != mode || entries[0].Path != "" || strings.Contains(entries[0].Error, "secret-") {
				t.Fatalf("wrong missing-evidence classification or leaked credentials: %#v", entries)
			}
		})
	}
}

type evidenceTestReaderError struct{ err error }

func (r evidenceTestReaderError) Read([]byte) (int, error) { return 0, r.err }

func TestInstantQueryStopsReadingAtEvidenceLimit(t *testing.T) {
	collector, err := newEvidenceCollector(t.TempDir(), timing.TimeWindow{})
	if err != nil {
		t.Fatal(err)
	}
	reader := &io.LimitedReader{R: evidenceTestPaddingReader{}, N: 2 * maxEvidenceResponseBytes}
	body := &evidenceTestBody{Reader: reader}
	base := evidenceTestTransport(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: body}, nil
	})
	_, err = queryInstant(context.Background(), collector.client(base, evidenceRequest{Kind: "stored-samples"}), evidenceTestCredential{}, "https://example.test", "requests[1h]", time.Now())
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized instant query did not report the response limit: %v", err)
	}
	if reader.N != maxEvidenceResponseBytes-1 || !body.closed {
		t.Fatalf("instant query drained beyond the limit or left its body open: unread=%d closed=%v", reader.N, body.closed)
	}
	entries := readEvidenceEntries(t, collector)
	if len(entries) != 1 || entries[0].Status != "limited" || entries[0].Failure != "response-limit" || entries[0].Path != "" {
		t.Fatalf("oversized instant query evidence was not explicitly limited: %#v", entries)
	}
}

type evidenceTestPaddingReader struct{}

func (evidenceTestPaddingReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = ' '
	}
	return len(p), nil
}
