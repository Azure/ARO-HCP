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

package amwusage

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

type scanTestCredential struct{}

func (scanTestCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "never-store-this", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func scanSeed(t *testing.T) string {
	t.Helper()
	d := collectionData{SchemaVersion: 1, Run: collectionRun{Start: 100000, End: 100600}, Records: map[string]*collectionRecord{}, Workspaces: []*collectionWorkspace{{ID: collectionTestID, Name: "test", Endpoint: "https://test.westus3.prometheus.monitor.azure.com", Names: []string{"up", "other"}}}}
	expression := fmt.Sprintf("sum by(%s)(count_over_time(up[600000ms]))", scanGrouping)
	params := url.Values{"query": {expression}, "time": {time.Unix(100600, 0).UTC().Format(time.RFC3339)}, "timeout": {"90s"}}
	d.Workspaces[0].Metrics = []collectionMetric{{Name: "up", Samples: "samples", SamplesQuery: expression}}
	d.Records["samples"] = &collectionRecord{collectionEnvelope: collectionEnvelope{Request: collectionRequest{Kind: "promql", URL: d.Workspaces[0].Endpoint + "/api/v1/query?" + params.Encode()}, OK: true, Status: 200}, Body: `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"Cluster":"ABC","job":"Test"},"value":[100600,"27"]}]}}`}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func newScanTestStore(t *testing.T) (*ScanStore, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "scan.db")
	s, err := OpenScanStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Initialize(context.Background(), strings.NewReader(scanSeed(t))); err != nil {
		t.Fatal(err)
	}
	return s, path
}

func TestSQLiteImportAndPlan(t *testing.T) {
	s, _ := newScanTestStore(t)
	ctx := context.Background()
	r, err := s.Summary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if r.RankingTotal != 6 || r.RankingSucceeded != 1 || r.Metrics != 2 || r.Observations != 1 {
		t.Fatalf("unexpected import summary: %+v", r)
	}
	var canonical string
	var count int64
	if err := s.db.QueryRow(`SELECT l.canonical,o.integer_value FROM accepted_observation o JOIN labelset l ON l.id=o.labelset_id`).Scan(&canonical, &count); err != nil {
		t.Fatal(err)
	}
	if canonical != "" || count != 27 {
		t.Fatalf("normalized labels/count: %s %d", canonical, count)
	}
	if err := s.Initialize(ctx, strings.NewReader(scanSeed(t))); err != nil {
		t.Fatal(err)
	}
	if err := s.Initialize(ctx, strings.NewReader(scanSeed(t)+" ")); err == nil {
		t.Fatal("changed seed accepted")
	}
	var before int
	if err := s.db.QueryRow(`SELECT count(*) FROM query WHERE kind='before12h' AND lookback_seconds=43200 AND evaluation_time=100000`).Scan(&before); err != nil || before != 2 {
		t.Fatalf("before plan: %d %v", before, err)
	}
}

func TestSQLiteNormalizedMembershipCompatibility(t *testing.T) {
	s, _ := newScanTestStore(t)
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	labels := map[string]string{"cluster": "abc", "job": "test"}
	id, err := internLabels(context.Background(), tx, labels)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate the already shipped schema-1 writer, without migrating its rows.
	legacy := `{"cluster":"abc","job":"test"}`
	if _, err := tx.Exec(`UPDATE labelset SET canonical=? WHERE id=?`, legacy, id); err != nil {
		t.Fatal(err)
	}
	again, err := internLabels(context.Background(), tx, map[string]string{"Cluster": "ABC", "JOB": "TEST"})
	if err != nil || again != id {
		t.Fatalf("legacy membership reuse: %d %v", again, err)
	}
	var canonical string
	if err := tx.QueryRow(`SELECT canonical FROM labelset WHERE id=?`, id).Scan(&canonical); err != nil || canonical != legacy {
		t.Fatalf("legacy canonical changed: %s %v", canonical, err)
	}
	newID, err := internLabels(context.Background(), tx, map[string]string{"job": "other"})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(`SELECT canonical FROM labelset WHERE id=?`, newID).Scan(&canonical); err != nil || canonical != "" {
		t.Fatalf("new row repeats labels: %s %v", canonical, err)
	}
	if _, err := tx.Exec(`DELETE FROM labelset_member WHERE labelset_id=?`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := internLabels(context.Background(), tx, labels); err == nil {
		t.Fatal("corrupt membership accepted using legacy canonical")
	}
}

// Explicitly opt-in and exclusively create the requested benchmark path. This
// never opens the running fullscan database and never makes network requests.
func TestSQLiteStorageBenchmark(t *testing.T) {
	path := os.Getenv("AMW_SCAN_BENCHMARK_DB")
	seedPath := os.Getenv("AMW_SCAN_TEST_SEED")
	if path == "" || seedPath == "" {
		t.Skip("set AMW_SCAN_BENCHMARK_DB and AMW_SCAN_TEST_SEED")
	}
	reservation, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err := reservation.Close(); err != nil {
		t.Fatal(err)
	}
	seed, err := os.Open(seedPath)
	if err != nil {
		t.Fatal(err)
	}
	defer seed.Close()
	store, err := OpenScanStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	start := time.Now()
	if err := store.Initialize(context.Background(), seed); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	summary, err := store.Summary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var sets, canonicalBytes, members int64
	if err := store.db.QueryRow(`SELECT count(*),coalesce(sum(length(canonical)),0),(SELECT count(*) FROM labelset_member) FROM labelset`).Scan(&sets, &canonicalBytes, &members); err != nil {
		t.Fatal(err)
	}
	if canonicalBytes != 0 {
		t.Fatalf("duplicated canonical strings remain: %d bytes", canonicalBytes)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	dbInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seed.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	counter := &scanByteCounter{}
	zipped := gzip.NewWriter(counter)
	seedBytes, err := io.Copy(zipped, seed)
	if err != nil {
		t.Fatal(err)
	}
	if err := zipped.Close(); err != nil {
		t.Fatal(err)
	}
	t.Logf("database=%s import=%s seed_bytes=%d gzip_bytes=%d sqlite_bytes=%d sqlite/gzip=%.2f labelsets=%d members=%d canonical_bytes=%d summary=%+v", path, elapsed, seedBytes, counter.n, dbInfo.Size(), float64(dbInfo.Size())/float64(counter.n), sets, members, canonicalBytes, summary)
}

type scanByteCounter struct{ n int64 }

func (c *scanByteCounter) Write(p []byte) (int, error) { c.n += int64(len(p)); return len(p), nil }

func TestSQLiteConcurrentClaimsAndFence(t *testing.T) {
	s, path := newScanTestStore(t)
	other, err := OpenScanStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	now := time.Now()
	ctx := context.Background()
	var wg sync.WaitGroup
	claims := make(chan *scanClaim, 2)
	errs := make(chan error, 2)
	for i, store := range []*ScanStore{s, other} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, _, err := store.claimScan(ctx, fmt.Sprint(i), now)
			claims <- c
			errs <- err
		}()
	}
	wg.Wait()
	close(claims)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var first *scanClaim
	for c := range claims {
		if c != nil {
			if first != nil {
				t.Fatal("workspace concurrency exceeded")
			}
			first = c
		}
	}
	if first == nil {
		t.Fatal("no claim")
	}
	if _, _, err := s.claimScan(ctx, "second", now.Add(59*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := s.finishScan(ctx, *first, scanOutcome{scanResponseMeta: scanResponseMeta{warnings: "[]", stats: "{}"}}, now.Add(61*time.Second)); !errors.Is(err, errScanLease) {
		t.Fatalf("expired publisher accepted: %v", err)
	}
	reclaimed, _, err := other.claimScan(ctx, "replacement", now.Add(61*time.Second))
	if err != nil || reclaimed == nil {
		t.Fatalf("reclaim: %v %v", reclaimed, err)
	}
	if err := s.finishScan(ctx, *first, scanOutcome{}, now); !errors.Is(err, errScanLease) {
		t.Fatalf("stale owner accepted: %v", err)
	}
}

func TestSQLiteRetryAndHardLimit(t *testing.T) {
	s, _ := newScanTestStore(t)
	ctx := context.Background()
	now := time.Now()
	c, _, err := s.claimScan(ctx, "owner", now)
	if err != nil {
		t.Fatal(err)
	}
	after := now.Add(90 * time.Second)
	o := scanOutcome{scanResponseMeta: scanResponseMeta{warnings: "[]", stats: "{}"}, classification: "throttled", retry: true, retryAt: scanTime(after), status: 429, body: "too many concurrent requests"}
	if err := s.finishScan(ctx, *c, o, now); err != nil {
		t.Fatal(err)
	}
	if c, _, err := s.claimScan(ctx, "early", after.Add(-time.Second)); err != nil || c != nil {
		t.Fatalf("retried before Retry-After: %v %v", c, err)
	}
	if c, _, err := s.claimScan(ctx, "later", after); err != nil || c == nil {
		t.Fatalf("did not wake after cooldown: %v %v", c, err)
	}
	for _, body := range []string{"Query has exceeded the timeseries per metric limit", "estimated query cost 19000 >15000", "Query exceeded the series limit"} {
		kind, retry, _ := classifyScanFailure(429, body)
		if kind != "hard_limit" || retry {
			t.Fatalf("hard cap retried: %s", body)
		}
	}
	if got := scanRetryAfter("90", now); !got.Equal(after) {
		t.Fatalf("seconds Retry-After: %s", got)
	}
	if got := scanRetryAfter(after.UTC().Format(http.TimeFormat), now); got.Unix() != after.Unix() {
		t.Fatalf("HTTP date Retry-After: %s", got)
	}
}

func TestSQLitePublicationAndResume(t *testing.T) {
	s, path := newScanTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int32
	transport := collectionTestTransport(func(r *http.Request) (*http.Response, error) { calls.Add(1); cancel(); return nil, context.Canceled })
	err := s.Scan(ctx, ScanOptions{Credential: scanTestCredential{}, Transport: transport})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	var running int
	if err := s.db.QueryRow(`SELECT count(*) FROM query WHERE state='running'`).Scan(&running); err != nil || running != 0 {
		t.Fatalf("claims not released: %d %v", running, err)
	}
	other, err := OpenScanStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	transport = collectionTestTransport(func(r *http.Request) (*http.Response, error) {
		at, err := time.Parse(time.RFC3339, r.URL.Query().Get("time"))
		if err != nil {
			t.Error(err)
		}
		return collectionTestResponse(fmt.Sprintf(`{"data":{"resultType":"vector","result":[{"metric":{"cluster":"ABC","job":"TEST"},"value":[%d,"3"]}]},"status":"success"}`, at.Unix()), 200), nil
	})
	if err := other.Scan(context.Background(), ScanOptions{Credential: scanTestCredential{}, Transport: transport}); err != nil {
		t.Fatal(err)
	}
	r, err := other.Summary(context.Background())
	if err != nil || !r.CoverageComplete || r.State != "complete" {
		t.Fatalf("resume summary: %+v %v", r, err)
	}
	var sets int
	if err := other.db.QueryRow(`SELECT count(*) FROM labelset`).Scan(&sets); err != nil || sets != 1 {
		t.Fatalf("labelsets not interned: %d %v", sets, err)
	}
}

func TestSQLiteLateFailureNeverPublishes(t *testing.T) {
	for _, suffix := range []string{`,"warnings":["partial"]}`, `,"status":"error"}`, `}garbage`} {
		t.Run(suffix, func(t *testing.T) {
			s, _ := newScanTestStore(t)
			var rows strings.Builder
			for i := range 130 {
				if i > 0 {
					rows.WriteByte(',')
				}
				fmt.Fprintf(&rows, `{"metric":{"cluster":"%d"},"value":[100000,"1"]}`, i)
			}
			body := `{"status":"success","data":{"resultType":"vector","result":[` + rows.String() + `]}` + suffix
			transport := collectionTestTransport(func(*http.Request) (*http.Response, error) { return collectionTestResponse(body, 200), nil })
			if err := s.Scan(context.Background(), ScanOptions{Credential: scanTestCredential{}, Transport: transport}); err != nil {
				t.Fatal(err)
			}
			var accepted, staged int
			if err := s.db.QueryRow(`SELECT count(*) FROM accepted_observation`).Scan(&accepted); err != nil {
				t.Fatal(err)
			}
			if err := s.db.QueryRow(`SELECT count(*) FROM observation`).Scan(&staged); err != nil {
				t.Fatal(err)
			}
			if accepted != 1 || staged != 1 {
				t.Fatalf("failed prefix published or retained: %d %d", accepted, staged)
			}
		})
	}
}

func TestSQLiteRealSeedOffline(t *testing.T) {
	path := os.Getenv("AMW_SCAN_TEST_SEED")
	if path == "" {
		t.Skip("set AMW_SCAN_TEST_SEED for local archive import parity")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	database := filepath.Join(t.TempDir(), "real.db")
	s, err := OpenScanStore(database)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Initialize(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	r, err := s.Summary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("offline seed: %+v", r)
	if r.Metrics != 3138 || r.RankingTotal != 9414 {
		t.Fatalf("unexpected full catalog size: %+v", r)
	}
	if r.Observations == 0 {
		t.Fatal("no imported observations")
	}
}

func TestSQLiteParserBounds(t *testing.T) {
	_, err := parseScanResponse(io.MultiReader(strings.NewReader(`{"status":"success","data":{"resultType":"vector","result":[]},"padding":"`), strings.NewReader(strings.Repeat("x", 8<<20)), strings.NewReader(`"}`)), false, func([]scanObservation) error { return nil })
	if err == nil {
		t.Fatal("oversized response accepted")
	}
}

func TestSQLiteHardLimitAndAuthorization(t *testing.T) {
	for _, status := range []int{429, 403} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			s, _ := newScanTestStore(t)
			var requests atomic.Int32
			transport := collectionTestTransport(func(r *http.Request) (*http.Response, error) {
				requests.Add(1)
				if strings.Contains(r.URL.Query().Get("query"), "instance") {
					return collectionTestResponse(`{"error":"bad request"}`, 400), nil
				}
				if strings.Contains(r.URL.Query().Get("query"), "namespace=~") {
					return collectionTestResponse(`{"status":"success","data":{"resultType":"vector","result":[]}}`, 200), nil
				}
				return collectionTestResponse(`{"error":"Query has exceeded the timeseries per metric limit"}`, status), nil
			})
			if status == 403 {
				transport = collectionTestTransport(func(*http.Request) (*http.Response, error) {
					requests.Add(1)
					return collectionTestResponse(`{"error":"permission denied"}`, 403), nil
				})
			}
			if err := s.Scan(context.Background(), ScanOptions{Credential: scanTestCredential{}, Transport: transport}); err != nil {
				t.Fatal(err)
			}
			r, err := s.Summary(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if r.State != "complete" || r.CoverageComplete || r.Queries["blocked"] < 5 {
				t.Fatalf("terminal coverage: %+v", r)
			}
			if status == 403 && (requests.Load() != 1 || r.Queries["blocked"] != 5) {
				t.Fatalf("authorization retried: %+v requests=%d", r, requests.Load())
			}
			if status == 429 && (requests.Load() > 115 || r.Blocked["partition_inactive"] == 0) {
				t.Fatalf("terminal roots did not stop descendants: %+v requests=%d", r, requests.Load())
			}
		})
	}
}

func TestSQLitePersistedRetryBudget(t *testing.T) {
	s, _ := newScanTestStore(t)
	ctx := context.Background()
	now := time.Now()
	var id string
	for i := range 6 {
		c, _, err := s.claimScan(ctx, "owner", now)
		if err != nil || c == nil {
			t.Fatalf("claim %d: %v %v", i, c, err)
		}
		if i == 0 {
			id = c.id
		} else if c.id != id {
			t.Fatal("retry was starved by new work")
		}
		if err := s.finishScan(ctx, *c, scanOutcome{scanResponseMeta: scanResponseMeta{warnings: "[]", stats: "{}"}, retry: true, classification: "unavailable"}, now); err != nil {
			t.Fatal(err)
		}
		now = now.Add(time.Minute)
	}
	var state string
	var attempts int
	if err := s.db.QueryRow(`SELECT state,attempt_count FROM query WHERE id=?`, id).Scan(&state, &attempts); err != nil || state != "blocked" || attempts != 6 {
		t.Fatalf("budget: %s %d %v", state, attempts, err)
	}
	c, _, err := s.claimScan(ctx, "owner", now)
	if err != nil || c == nil {
		t.Fatalf("next claim: %v", err)
	}
	if err := s.finishScan(ctx, *c, scanOutcome{scanResponseMeta: scanResponseMeta{warnings: "[]", stats: "{}"}, retry: true, classification: "throttled", retryAt: scanTime(now.Add(time.Hour))}, now); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT state FROM query WHERE id=?`, c.id).Scan(&state); err != nil || state != "retry_wait" {
		t.Fatalf("long Retry-After consumed execution budget: %s %v", state, err)
	}
}

func TestSQLiteCanceledClaimsAndDowntimeDoNotConsumeBudget(t *testing.T) {
	s, _ := newScanTestStore(t)
	ctx := context.Background()
	now := time.Now()
	var id string
	for range 8 {
		c, _, err := s.claimScan(ctx, "cancel", now)
		if err != nil || c == nil {
			t.Fatalf("claim: %v %v", c, err)
		}
		if id != "" && c.id != id {
			t.Fatal("canceled query no longer claimable")
		}
		id = c.id
		if err := s.releaseScanClaims(ctx, "cancel"); err != nil {
			t.Fatal(err)
		}
		now = now.Add(24 * time.Hour)
	}
	c, _, err := s.claimScan(ctx, "retry", now)
	if err != nil || c == nil || c.id != id {
		t.Fatalf("resume: %v %v", c, err)
	}
	if err := s.finishScan(ctx, *c, scanOutcome{scanResponseMeta: scanResponseMeta{warnings: "[]", stats: "{}"}, retry: true, classification: "transport", duration: 900000}, now); err != nil {
		t.Fatal(err)
	}
	var state, classification string
	if err := s.db.QueryRow(`SELECT state,classification FROM query WHERE id=?`, id).Scan(&state, &classification); err != nil || state != "blocked" || classification != "retry_budget" {
		t.Fatalf("execution budget: %s %s %v", state, classification, err)
	}
}

type scanFailureReader struct{ err error }

func (r scanFailureReader) Read([]byte) (int, error) { return 0, r.err }

func TestSQLiteTruncatedStreamRetriesWithoutPublication(t *testing.T) {
	for _, failure := range []error{io.EOF, io.ErrUnexpectedEOF, errors.New("connection reset by peer")} {
		t.Run(failure.Error(), func(t *testing.T) {
			s, _ := newScanTestStore(t)
			ctx := context.Background()
			now := time.Now()
			c, _, err := s.claimScan(ctx, "reader", now)
			if err != nil {
				t.Fatal(err)
			}
			var rows strings.Builder
			for i := range 130 {
				if i > 0 {
					rows.WriteByte(',')
				}
				fmt.Fprintf(&rows, `{"metric":{"cluster":"%d"},"value":[100000,"1"]}`, i)
			}
			body := `{"status":"success","data":{"resultType":"vector","result":[` + rows.String() + `]}`
			client := &http.Client{Transport: collectionTestTransport(func(*http.Request) (*http.Response, error) {
				r := collectionTestResponse("", 200)
				r.Body = io.NopCloser(io.MultiReader(strings.NewReader(body), scanFailureReader{failure}))
				return r, nil
			})}
			o, err := s.requestScan(ctx, *c, scanTestCredential{}, client)
			if err != nil || !o.retry || o.classification != "transport" {
				t.Fatalf("truncation not retried: %+v %v", o, err)
			}
			var staged int
			if err := s.db.QueryRow(`SELECT count(*) FROM observation WHERE attempt_id=?`, c.attempt).Scan(&staged); err != nil || staged != 128 {
				t.Fatalf("late failure did not stage prefix: %d %v", staged, err)
			}
			if err := s.finishScan(ctx, *c, o, now); err != nil {
				t.Fatal(err)
			}
			if err := s.db.QueryRow(`SELECT count(*) FROM observation WHERE attempt_id=?`, c.attempt).Scan(&staged); err != nil || staged != 0 {
				t.Fatalf("retry retained prefix: %d %v", staged, err)
			}
		})
	}
	_, err := parseScanResponse(strings.NewReader(`{"status":"success","data":BROKEN}`), false, func([]scanObservation) error { return nil })
	var transient scanBodyReadError
	if err == nil || errors.As(err, &transient) {
		t.Fatalf("malformed complete JSON not terminal: %v", err)
	}
}

func TestSQLiteDuplicateKeysIsolateBadQuery(t *testing.T) {
	for _, row := range []string{
		`{"metric":{"job":"a","job":"b"},"value":[100000,"1"]}`,
		`{"metric":{"Job":"a","job":"b"},"value":[100000,"1"]}`,
		`{"metric":{},"metric":{},"value":[100000,"1"]}`,
		`{"metric":{},"value":[100000,"1"],"value":[100000,"2"]}`,
	} {
		t.Run(row, func(t *testing.T) {
			s, _ := newScanTestStore(t)
			transport := collectionTestTransport(func(r *http.Request) (*http.Response, error) {
				if strings.Contains(r.URL.Query().Get("query"), "other[") {
					return collectionTestResponse(`{"status":"success","data":{"resultType":"vector","result":[`+row+`]}}`, 200), nil
				}
				return collectionTestResponse(`{"status":"success","data":{"resultType":"vector","result":[]}}`, 200), nil
			})
			if err := s.Scan(context.Background(), ScanOptions{Credential: scanTestCredential{}, Transport: transport}); err != nil {
				t.Fatal(err)
			}
			r, err := s.Summary(context.Background())
			if err != nil || r.RankingSucceeded != 3 || r.Queries["blocked"] != 3 {
				t.Fatalf("bad query stopped unrelated work: %+v %v", r, err)
			}
		})
	}
}

func TestSQLiteImportedRankingCountValidation(t *testing.T) {
	for _, value := range []string{"-1", "1.5", "1.00000000000000001", "9223372036854775808", "NaN"} {
		t.Run(value, func(t *testing.T) {
			s, err := OpenScanStore(filepath.Join(t.TempDir(), "invalid.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			var seed collectionData
			if err := json.Unmarshal([]byte(scanSeed(t)), &seed); err != nil {
				t.Fatal(err)
			}
			seed.Records["samples"].Body = strings.ReplaceAll(seed.Records["samples"].Body, `"27"`, `"`+value+`"`)
			encoded, err := json.Marshal(seed)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Initialize(context.Background(), strings.NewReader(string(encoded))); err != nil {
				t.Fatal(err)
			}
			var invalid int
			if err := s.db.QueryRow(`SELECT count(*) FROM accepted_observation WHERE ranking=1 AND value IS NULL AND integer_value IS NULL AND invalid_value IS NOT NULL`).Scan(&invalid); err != nil || invalid != 1 {
				t.Fatalf("invalid imported rank accepted: %d %v", invalid, err)
			}
		})
	}
}

func TestSQLitePublicationChecksTimeAfterLockWait(t *testing.T) {
	s, _ := newScanTestStore(t)
	ctx := context.Background()
	now := time.Now()
	c, _, err := s.claimScan(ctx, "owner", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE query SET lease_until=? WHERE id=?`, scanTime(now.Add(100*time.Millisecond)), c.id); err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.finishScan(ctx, *c, scanOutcome{}, now) }()
	time.Sleep(200 * time.Millisecond)
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, errScanLease) {
		t.Fatalf("expired lease published after writer wait: %v", err)
	}
}

func TestSQLiteClaimLeaseStartsAfterLockWait(t *testing.T) {
	s, _ := newScanTestStore(t)
	ctx := context.Background()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		claim *scanClaim
		err   error
	}
	done := make(chan result, 1)
	started := time.Now()
	go func() { c, _, err := s.claimScan(ctx, "waiting", started); done <- result{c, err} }()
	time.Sleep(200 * time.Millisecond)
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	r := <-done
	if r.err != nil || r.claim == nil {
		t.Fatalf("claim: %v %v", r.claim, r.err)
	}
	var lease float64
	if err := s.db.QueryRow(`SELECT lease_until FROM query WHERE id=?`, r.claim.id).Scan(&lease); err != nil {
		t.Fatal(err)
	}
	if lease < scanTime(started.Add(scanLease+150*time.Millisecond)) {
		t.Fatal("writer lock wait shortened new lease")
	}
}

func TestSQLiteExactIntegerParsing(t *testing.T) {
	for _, text := range []string{"9223372036854775807", "9007199254740993", "1e3", "1.0"} {
		var observed *int64
		_, err := parseScanResponse(strings.NewReader(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1,"`+text+`"]}]}}`), false, func(batch []scanObservation) error { observed = batch[0].integer; return nil })
		if err != nil || observed == nil {
			t.Fatalf("exact integer %s rejected: %v", text, err)
		}
		if text == "9223372036854775807" && *observed != 9223372036854775807 {
			t.Fatal("int64 max was rounded")
		}
	}
}

func TestSQLiteEndpointRampAndRedirect(t *testing.T) {
	s, _ := newScanTestStore(t)
	if _, err := s.db.Exec(`UPDATE workspace SET success_streak=9`); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	c, _, err := s.claimScan(context.Background(), "owner", now)
	if err != nil || c == nil {
		t.Fatalf("claim: %v", err)
	}
	if err := s.finishScan(context.Background(), *c, scanOutcome{scanResponseMeta: scanResponseMeta{warnings: "[]", stats: "{}"}}, now); err != nil {
		t.Fatal(err)
	}
	var concurrency int
	if err := s.db.QueryRow(`SELECT concurrency FROM workspace`).Scan(&concurrency); err != nil || concurrency != 2 {
		t.Fatalf("endpoint did not ramp: %d %v", concurrency, err)
	}
	var requests atomic.Int32
	transport := collectionTestTransport(func(r *http.Request) (*http.Response, error) {
		requests.Add(1)
		if r.URL.Host != "test.westus3.prometheus.monitor.azure.com" {
			t.Errorf("followed unsafe redirect: %s", r.URL.Host)
		}
		response := collectionTestResponse("redirect", 302)
		response.Header.Set("Location", "https://attacker.example/token")
		return response, nil
	})
	if err := s.Scan(context.Background(), ScanOptions{Credential: scanTestCredential{}, Transport: transport}); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 4 {
		t.Fatalf("redirect requests: %d", requests.Load())
	}
	for _, endpoint := range []string{"https://attacker.example", "http://test.westus3.prometheus.monitor.azure.com", "https://user@test.westus3.prometheus.monitor.azure.com", "https://test.westus3.prometheus.monitor.azure.com:443"} {
		if err := validateScanEndpoint(endpoint); err == nil {
			t.Fatalf("unsafe endpoint accepted: %s", endpoint)
		}
	}
}

func TestSQLiteCrashRecovery(t *testing.T) {
	if path := os.Getenv("AMW_SCAN_CRASH_CHILD"); path != "" {
		s, err := OpenScanStore(path)
		if err != nil {
			t.Fatal(err)
		}
		c, _, err := s.claimScan(context.Background(), "crashed", time.Now())
		if err != nil || c == nil {
			t.Fatalf("child claim: %v", err)
		}
		tx, err := s.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		value := float64(8)
		if err := insertObservations(context.Background(), tx, c.attempt, []scanObservation{{labels: map[string]string{"staged": "only"}, at: 100000, value: &value}}); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		os.Exit(23)
	}
	s, path := newScanTestStore(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestSQLiteCrashRecovery$")
	cmd.Env = append(os.Environ(), "AMW_SCAN_CRASH_CHILD="+path)
	output, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 23 {
		t.Fatalf("child did not crash as intended: %v %s", err, output)
	}
	var accepted, all int
	if err := s.db.QueryRow(`SELECT count(*) FROM accepted_observation`).Scan(&accepted); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT count(*) FROM observation`).Scan(&all); err != nil {
		t.Fatal(err)
	}
	if accepted != 1 || all != 2 {
		t.Fatalf("staging publication: accepted=%d all=%d", accepted, all)
	}
	if c, _, err := s.claimScan(context.Background(), "too-early", time.Now()); err != nil || c != nil {
		t.Fatalf("live lease stolen: %v %v", c, err)
	}
	if c, _, err := s.claimScan(context.Background(), "recovered", time.Now().Add(61*time.Second)); err != nil || c == nil {
		t.Fatalf("crash not recovered: %v %v", c, err)
	}
	if err := s.db.QueryRow(`SELECT count(*) FROM observation`).Scan(&all); err != nil || all != 1 {
		t.Fatalf("crashed staging not cleaned: %d %v", all, err)
	}
}
