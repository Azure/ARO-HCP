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
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func enrichmentTestPlan(t *testing.T) string {
	t.Helper()
	p := EnrichmentPlan{Windows: []EnrichmentWindow{{Name: "minute", Start: time.Unix(100000, 0).UTC(), End: time.Unix(100060, 0).UTC()}}, Metrics: []EnrichmentMetric{{Workspace: "test", Name: "up", Labels: true, MatchLabels: map[string]string{"namespace": "velero", "cluster": "mgmt-1"}}}}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestEnrichmentPlanDurableAndAtomic(t *testing.T) {
	s, path := newScanTestStore(t)
	ctx := context.Background()
	if err := s.PlanEnrichment(ctx, nil); err == nil {
		t.Fatal("resume without plan accepted")
	}
	plan := enrichmentTestPlan(t)
	if err := s.PlanEnrichment(ctx, strings.NewReader(plan)); err != nil {
		t.Fatal(err)
	}
	if err := s.PlanEnrichment(ctx, strings.NewReader(plan)); err != nil {
		t.Fatal(err)
	}
	var expression, role, window string
	var count int
	if err := s.db.QueryRow(`SELECT expression,kind,(SELECT kind FROM window WHERE id=query.window_id) FROM query WHERE kind='samples_window'`).Scan(&expression, &role, &window); err != nil {
		t.Fatal(err)
	}
	if expression != `sum by(cluster,job,namespace,hostedcontrolplane,prometheus)(count_over_time(up{cluster="mgmt-1",namespace="velero"}[60000ms]))` || window != "samples:100000:100060" {
		t.Fatalf("wrong bounded expression/window: %s %s", expression, window)
	}
	if err := s.PlanEnrichment(ctx, strings.NewReader(strings.Replace(plan, "velero", "other", 1))); err == nil {
		t.Fatal("conflicting plan accepted")
	}
	if err := s.db.QueryRow(`SELECT count(*) FROM query WHERE kind IN ('samples_window','inventory_samples')`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("plan duplicated or partially changed: %d %v", count, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := OpenScanStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.PlanEnrichment(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE query SET id='changed' WHERE kind='samples_window'`); err != nil {
		// Durable links also fence accidental request-ID changes at the FK.
		if !strings.Contains(err.Error(), "FOREIGN KEY") {
			t.Fatal(err)
		}
		return
	}
	if err := s.PlanEnrichment(ctx, nil); err == nil {
		t.Fatal("changed query ID accepted or duplicated")
	}
}

func TestEnrichmentValidation(t *testing.T) {
	for _, change := range []struct {
		name   string
		mutate func(*EnrichmentPlan)
	}{
		{"fraction", func(p *EnrichmentPlan) { p.Windows[0].Start = p.Windows[0].Start.Add(time.Nanosecond) }},
		{"short", func(p *EnrichmentPlan) { p.Windows[0].End = p.Windows[0].Start.Add(59 * time.Second) }},
		{"long", func(p *EnrichmentPlan) { p.Windows[0].End = p.Windows[0].Start.Add(31 * time.Minute) }},
		{"outside", func(p *EnrichmentPlan) { p.Windows[0].Start = time.Unix(1, 0); p.Windows[0].End = time.Unix(61, 0) }},
		{"catalog", func(p *EnrichmentPlan) { p.Metrics[0].Name = "missing" }},
		{"endpoint", func(p *EnrichmentPlan) { p.Metrics[0].Workspace = "https://evil.invalid" }},
		{"injection", func(p *EnrichmentPlan) { p.Metrics[0].Name = "up{job=~\".*\"}" }},
		{"label", func(p *EnrichmentPlan) { p.Metrics[0].MatchLabels["pod"] = "pod" }},
		{"duplicate", func(p *EnrichmentPlan) { p.Metrics = append(p.Metrics, p.Metrics[0]) }},
		{"windows", func(p *EnrichmentPlan) { p.Windows = append(p.Windows, p.Windows[0], p.Windows[0], p.Windows[0]) }},
		{"metrics", func(p *EnrichmentPlan) {
			p.Metrics = append(p.Metrics, p.Metrics[0], p.Metrics[0], p.Metrics[0], p.Metrics[0])
		}},
		{"unknown_window", func(p *EnrichmentPlan) { p.Metrics[0].Windows = []string{"absent"} }},
	} {
		t.Run(change.name, func(t *testing.T) {
			s, _ := newScanTestStore(t)
			var p EnrichmentPlan
			if err := json.Unmarshal([]byte(enrichmentTestPlan(t)), &p); err != nil {
				t.Fatal(err)
			}
			change.mutate(&p)
			b, _ := json.Marshal(p)
			if err := s.PlanEnrichment(context.Background(), strings.NewReader(string(b))); err == nil {
				t.Fatal("invalid plan accepted")
			}
			var n int
			if err := s.db.QueryRow(`SELECT count(*) FROM query WHERE kind IN ('samples_window','inventory_samples')`).Scan(&n); err != nil || n != 0 {
				t.Fatalf("partial plan: %d %v", n, err)
			}
		})
	}
}

func TestEnrichmentFilteredResumeAndCancellation(t *testing.T) {
	s, path := newScanTestStore(t)
	ctx := context.Background()
	if err := s.PlanEnrichment(ctx, strings.NewReader(enrichmentTestPlan(t))); err != nil {
		t.Fatal(err)
	}
	// A copied expired ranking lease and pending ranking work must remain byte-for-byte unchanged.
	old, _, err := s.claimScan(ctx, "old-scanner", time.Now().Add(-2*time.Minute))
	if err != nil || old == nil {
		t.Fatalf("old claim: %v", err)
	}
	var before string
	const rankingState = `SELECT json_group_array(json_object('id',id,'state',state,'owner',owner,'lease',lease_until,'attempt',current_attempt_id,'count',attempt_count,'accepted',accepted_attempt_id)) FROM query WHERE ranking=1 ORDER BY id`
	if err := s.db.QueryRow(rankingState).Scan(&before); err != nil {
		t.Fatal(err)
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	options := ScanOptions{Workers: 1, Credential: scanTestCredential{}, Kinds: []string{"samples_window", "inventory_samples"}, Transport: collectionTestTransport(func(r *http.Request) (*http.Response, error) { cancel(); return nil, context.Canceled })}
	if err := s.Scan(cancelCtx, options); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenScanStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.PlanEnrichment(ctx, nil); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	options.Transport = collectionTestTransport(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		q := r.URL.Query().Get("query")
		if !strings.Contains(q, `up{cluster="mgmt-1",namespace="velero"}[60000ms]`) {
			t.Errorf("old/unscoped query executed: %s", q)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{"cluster":"mgmt-1","namespace":"velero"},"value":[100060,"7"]}]}}`))}, nil
	})
	if err := s.Scan(ctx, options); err != nil {
		t.Fatal(err)
	}
	if err := s.Scan(ctx, options); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("resumed unrelated or repeated work: %d", calls.Load())
	}
	var after string
	if err := s.db.QueryRow(rankingState).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatal("ranking state changed during enrichment")
	}
	var observations, abandoned int
	if err := s.db.QueryRow(`SELECT count(*) FROM accepted_observation WHERE kind IN ('samples_window','inventory_samples')`).Scan(&observations); err != nil || observations != 2 {
		t.Fatalf("duplicate/missing publication: %d %v", observations, err)
	}
	if err := s.db.QueryRow(`SELECT count(*) FROM attempt WHERE classification='canceled'`).Scan(&abandoned); err != nil || abandoned != 1 {
		t.Fatalf("cancellation not durable: %d %v", abandoned, err)
	}
	summary, err := s.EnrichmentSummary(ctx)
	if err != nil || !summary.CoverageComplete {
		t.Fatalf("summary: %+v %v", summary, err)
	}
}

func TestEnrichmentCountsAndHardLimits(t *testing.T) {
	for _, value := range []string{"0", "-1", "1.5", "NaN", "9223372036854775808", "2.00000000000000001", "3"} {
		t.Run(value, func(t *testing.T) {
			s, _ := newScanTestStore(t)
			ctx := context.Background()
			if err := s.PlanEnrichment(ctx, strings.NewReader(enrichmentTestPlan(t))); err != nil {
				t.Fatal(err)
			}
			err := s.Scan(ctx, ScanOptions{Workers: 1, Credential: scanTestCredential{}, Kinds: []string{"samples_window", "inventory_samples"}, Transport: collectionTestTransport(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[100060,%q]}]}}`, value)))}, nil
			})})
			if err != nil {
				t.Fatal(err)
			}
			summary, err := s.EnrichmentSummary(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if summary.CoverageComplete != (value == "3") {
				t.Fatalf("count validation: %+v", summary)
			}
			if value != "3" && summary.Queries["blocked"] != 2 {
				t.Fatalf("invalid counts accepted: %+v", summary)
			}
		})
	}
	s, _ := newScanTestStore(t)
	ctx := context.Background()
	if err := s.PlanEnrichment(ctx, strings.NewReader(enrichmentTestPlan(t))); err != nil {
		t.Fatal(err)
	}
	err := s.Scan(ctx, ScanOptions{Workers: 1, Credential: scanTestCredential{}, Kinds: []string{"samples_window", "inventory_samples"}, Transport: collectionTestTransport(func(r *http.Request) (*http.Response, error) {
		status, body := 200, `{"status":"success","data":{"resultType":"vector","result":[]}}`
		if strings.HasPrefix(r.URL.Query().Get("query"), "count_over_time") {
			status = 422
			body = "estimated query cost exceeded"
		}
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := s.EnrichmentSummary(ctx)
	if err != nil || summary.Queries["blocked"] != 1 || summary.Queries["succeeded"] != 1 || !summary.SchedulingComplete {
		t.Fatalf("partial hard limit: %+v %v", summary, err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM query WHERE ranking=0`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("automatic fanout: %d %v", n, err)
	}
}

func TestEnrichmentLeaseRecoveryAndRetry(t *testing.T) {
	s, _ := newScanTestStore(t)
	ctx := context.Background()
	if err := s.PlanEnrichment(ctx, strings.NewReader(enrichmentTestPlan(t))); err != nil {
		t.Fatal(err)
	}
	kinds := []string{"samples_window", "inventory_samples"}
	old, _, err := s.claimEnrichment(ctx, "dead", time.Now().Add(-2*time.Minute), kinds)
	if err != nil || old == nil {
		t.Fatalf("crash claim: %v", err)
	}
	fresh, _, err := s.claimEnrichment(ctx, "new", time.Now(), kinds)
	if err != nil || fresh == nil || fresh.id != old.id || fresh.attempt == old.attempt {
		t.Fatalf("recovery: %+v %v", fresh, err)
	}
	if err := s.finishScan(ctx, *old, scanOutcome{}, time.Now()); !errors.Is(err, errScanLease) {
		t.Fatalf("stale publication accepted: %v", err)
	}
	if err := s.finishScan(ctx, *fresh, scanOutcome{classification: "throttled", retry: true, status: 429, retryAt: scanTime(time.Now().Add(time.Minute))}, time.Now()); err != nil {
		t.Fatal(err)
	}
	claim, done, err := s.claimEnrichment(ctx, "waiting", time.Now(), kinds)
	if err != nil || done || claim != nil {
		t.Fatalf("workspace cooldown ignored: %+v %v %v", claim, done, err)
	}
	claim, done, err = s.claimEnrichment(ctx, "later", time.Now().Add(2*time.Minute), kinds)
	if err != nil || done || claim == nil || claim.id != fresh.id {
		t.Fatalf("durable retry not resumed: %+v %v %v", claim, done, err)
	}
	if err := s.finishScan(ctx, *claim, scanOutcome{scanResponseMeta: scanResponseMeta{warnings: "[]", stats: "{}"}}, time.Now().Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.finishScan(ctx, *claim, scanOutcome{}, time.Now().Add(2*time.Minute)); !errors.Is(err, errScanLease) {
		t.Fatalf("double publication accepted: %v", err)
	}
	if err := s.Scan(ctx, ScanOptions{Credential: scanTestCredential{}, Kinds: []string{"run"}}); err == nil {
		t.Fatal("ranking kind allowed in whitelist")
	}
}

func TestEnrichmentBaselineAndWindowSelection(t *testing.T) {
	s, _ := newScanTestStore(t)
	ctx := context.Background()
	contextJSON := `{"schemaVersion":1,"run":{"start":"1970-01-02T03:46:40Z","end":"1970-01-02T03:56:40Z","prow":"https://example.com/run"},"baseline":{"start":"1970-01-02T03:00:00Z","end":"1970-01-02T03:30:00Z","reason":"supplied comparator","sources":["https://example.com/evidence"]}}`
	if _, err := s.db.Exec(`UPDATE run SET context_json=?`, contextJSON); err != nil {
		t.Fatal(err)
	}
	var p EnrichmentPlan
	if err := json.Unmarshal([]byte(enrichmentTestPlan(t)), &p); err != nil {
		t.Fatal(err)
	}
	p.Windows = append(p.Windows, EnrichmentWindow{Name: "baseline", Start: time.Unix(97200, 0).UTC(), End: time.Unix(97500, 0).UTC()})
	p.Metrics[0].Windows = []string{"baseline"}
	p.Metrics[0].Labels = false
	b, _ := json.Marshal(p)
	if err := s.PlanEnrichment(ctx, strings.NewReader(string(b))); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM query WHERE kind IN ('samples_window','inventory_samples')`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("window/labels selection: %d %v", n, err)
	}
	if _, err := s.db.Exec(`UPDATE enrichment_plan SET spec_hash='corrupt'`); err != nil {
		t.Fatal(err)
	}
	if err := s.PlanEnrichment(ctx, nil); err == nil {
		t.Fatal("corrupt plan fingerprint accepted")
	}
}

func TestEnrichmentReusesExactRequests(t *testing.T) {
	for _, tc := range []struct {
		name              string
		baseline, pending bool
	}{
		{"accepted_run", false, false}, {"pending_run", false, true}, {"accepted_baseline", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newScanTestStore(t)
			ctx := context.Background()
			start, end := int64(100000), int64(100600)
			var existing, kind string
			var ranking int
			if tc.baseline {
				start, end = 97200, 99000
				contextJSON := `{"schemaVersion":1,"run":{"start":"1970-01-02T03:46:40Z","end":"1970-01-02T03:56:40Z","prow":"https://example.com/run"},"baseline":{"start":"1970-01-02T03:00:00Z","end":"1970-01-02T03:30:00Z","reason":"supplied comparator","sources":["https://example.com/evidence"]}}`
				if _, err := s.db.Exec(`UPDATE run SET context_json=?`, contextJSON); err != nil {
					t.Fatal(err)
				}
				expression := fmt.Sprintf("sum by(%s)(count_over_time(up[1800000ms]))", scanGrouping)
				params := url.Values{"query": {expression}, "time": {time.Unix(end, 0).UTC().Format(time.RFC3339)}, "timeout": {"90s"}}.Encode()
				// Imported cached requests have no window, grouping or lookback metadata.
				if _, err := s.db.Exec(`INSERT INTO query(id,workspace_id,metric_id,kind,path,params,expression,evaluation_time,state) SELECT 'cached-baseline',workspace_id,metric_id,'baseline_samples',path,?,?,?,'pending' FROM query WHERE kind='run' AND state='succeeded'`, params, expression, end); err != nil {
					t.Fatal(err)
				}
				tx, err := s.db.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				if err := importScanResponse(ctx, tx, "cached-baseline", artifactRecord{OK: true, Body: fmt.Sprintf(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[%d,"27"]}]}}`, end)}, false); err != nil {
					_ = tx.Rollback()
					t.Fatal(err)
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				existing, kind = "cached-baseline", "baseline_samples"
			} else {
				if err := s.db.QueryRow(`SELECT id,kind,ranking FROM query WHERE kind='run' AND state='succeeded'`).Scan(&existing, &kind, &ranking); err != nil {
					t.Fatal(err)
				}
				if tc.pending {
					if _, err := s.db.Exec(`UPDATE query SET state='pending',accepted_attempt_id=NULL WHERE id=?`, existing); err != nil {
						t.Fatal(err)
					}
				}
			}
			var before, attempts int
			if err := s.db.QueryRow(`SELECT count(*),(SELECT count(*) FROM attempt) FROM query`).Scan(&before, &attempts); err != nil {
				t.Fatal(err)
			}
			plan := EnrichmentPlan{Windows: []EnrichmentWindow{{Name: "whole", Start: time.Unix(start, 0).UTC(), End: time.Unix(end, 0).UTC()}}, Metrics: []EnrichmentMetric{{Workspace: strings.ToUpper(collectionTestID), Name: "up"}}}
			data, _ := json.Marshal(plan)
			if err := s.PlanEnrichment(ctx, strings.NewReader(string(data))); err != nil {
				t.Fatal(err)
			}
			var linked string
			if err := s.db.QueryRow(`SELECT query_id FROM enrichment_query`).Scan(&linked); err != nil || linked != existing {
				t.Fatalf("request not reused: %s %v", linked, err)
			}
			plan.Metrics[0].Workspace = "TEST"
			data, _ = json.Marshal(plan)
			if err := s.PlanEnrichment(ctx, strings.NewReader(string(data))); err != nil {
				t.Fatalf("case-insensitive workspace alias conflicts: %v", err)
			}
			var calls atomic.Int32
			opts := ScanOptions{Workers: 1, Credential: scanTestCredential{}, Kinds: []string{"samples_window", "inventory_samples"}, Transport: collectionTestTransport(func(r *http.Request) (*http.Response, error) {
				calls.Add(1)
				if r.URL.Query().Get("time") != time.Unix(end, 0).UTC().Format(time.RFC3339) {
					t.Errorf("unrelated request: %s", r.URL)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[%d,"27"]}]}}`, end)))}, nil
			})}
			if err := s.Scan(ctx, opts); err != nil {
				t.Fatal(err)
			}
			if err := s.PlanEnrichment(ctx, nil); err != nil {
				t.Fatal(err)
			}
			if err := s.Scan(ctx, opts); err != nil {
				t.Fatal(err)
			}
			want := 0
			if tc.pending {
				want = 1
			}
			if int(calls.Load()) != want {
				t.Fatalf("requests=%d want=%d", calls.Load(), want)
			}
			var after, afterAttempts, actualRanking int
			var actualKind string
			if err := s.db.QueryRow(`SELECT count(*),(SELECT count(*) FROM attempt) FROM query`).Scan(&after, &afterAttempts); err != nil {
				t.Fatal(err)
			}
			if after != before || afterAttempts != attempts+want {
				t.Fatalf("duplicate requests/attempts: %d/%d %d/%d", before, after, attempts, afterAttempts)
			}
			if err := s.db.QueryRow(`SELECT kind,ranking FROM query WHERE id=?`, existing).Scan(&actualKind, &actualRanking); err != nil {
				t.Fatal(err)
			}
			if actualKind != kind || actualRanking != ranking {
				t.Fatal("reused request reclassified")
			}
			summary, err := s.EnrichmentSummary(ctx)
			if err != nil || !summary.CoverageComplete || summary.Queries["succeeded"] != 1 {
				t.Fatalf("logical coverage: %+v %v", summary, err)
			}
		})
	}
}

func TestEnrichmentBackfillsLinks(t *testing.T) {
	s, _ := newScanTestStore(t)
	ctx := context.Background()
	if err := s.PlanEnrichment(ctx, strings.NewReader(enrichmentTestPlan(t))); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DROP TABLE enrichment_query`); err != nil {
		t.Fatal(err)
	}
	if err := s.PlanEnrichment(ctx, nil); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM enrichment_query`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("backfill: %d %v", n, err)
	}
	if err := s.PlanEnrichment(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE enrichment_query SET query_id=(SELECT id FROM query WHERE kind='run' LIMIT 1) WHERE role='samples_window'`); err != nil {
		t.Fatal(err)
	}
	if err := s.PlanEnrichment(ctx, nil); err == nil {
		t.Fatal("conflicting durable role link accepted")
	}
}

func TestEnrichmentLateInvalidDiagnostics(t *testing.T) {
	for _, value := range []string{"NaN", "2.00000000000000001", "never-store-this"} {
		t.Run(value, func(t *testing.T) {
			s, _ := newScanTestStore(t)
			ctx := context.Background()
			if err := s.PlanEnrichment(ctx, strings.NewReader(enrichmentTestPlan(t))); err != nil {
				t.Fatal(err)
			}
			var body strings.Builder
			body.WriteString(`{"status":"success","data":{"resultType":"vector","result":[`)
			for i := 0; i < 130; i++ {
				fmt.Fprintf(&body, `{"metric":{"row":"%d","padding":%q},"value":[100060,"1"]},`, i, strings.Repeat("x", 600))
			}
			fmt.Fprintf(&body, `{"metric":{"row":"bad"},"value":[100060,%q]}]}}`, value)
			if body.Len() <= 65537 {
				t.Fatal("fixture does not exceed prefix cap")
			}
			err := s.Scan(ctx, ScanOptions{Workers: 1, Credential: scanTestCredential{}, Kinds: []string{"samples_window", "inventory_samples"}, Transport: collectionTestTransport(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body.String()))}, nil
			})})
			if err != nil {
				t.Fatal(err)
			}
			rows, err := s.db.Query(`SELECT a.error_gzip FROM enrichment_query e JOIN query q ON q.id=e.query_id JOIN attempt a ON a.query_id=q.id WHERE q.state='blocked' AND q.accepted_attempt_id IS NULL AND a.classification='invalid_response'`)
			if err != nil {
				t.Fatal(err)
			}
			n := 0
			for rows.Next() {
				var zipped []byte
				if err := rows.Scan(&zipped); err != nil {
					t.Fatal(err)
				}
				r, err := gzip.NewReader(bytes.NewReader(zipped))
				if err != nil {
					t.Fatal(err)
				}
				diagnostic, err := io.ReadAll(r)
				r.Close()
				if err != nil {
					t.Fatal(err)
				}
				want := value
				if value == "never-store-this" {
					want = "[REDACTED]"
				}
				if !strings.Contains(string(diagnostic), "sample counts must be positive integers") || !strings.Contains(string(diagnostic), want) || strings.Contains(string(diagnostic), "never-store-this") {
					t.Fatalf("late reason/value lost or token leaked: %.1100s", diagnostic)
				}
				n++
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
			if n != 2 {
				t.Fatalf("missing failure diagnostics: %d", n)
			}
			var staged int
			if err := s.db.QueryRow(`SELECT count(*) FROM observation o JOIN attempt a ON a.id=o.attempt_id JOIN enrichment_query e ON e.query_id=a.query_id`).Scan(&staged); err != nil || staged != 0 {
				t.Fatalf("partial observations published/retained: %d %v", staged, err)
			}
		})
	}
}
