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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func catalogSeed(t *testing.T, names ...string) collectionData {
	t.Helper()
	var data collectionData
	if err := json.Unmarshal([]byte(scanSeed(t)), &data); err != nil {
		t.Fatal(err)
	}
	w := data.Workspaces[0]
	w.Names = append([]string{}, names...)
	w.Metrics = nil
	w.Discovery = "names"
	body, err := json.Marshal(map[string]any{"status": "success", "data": w.Names})
	if err != nil {
		t.Fatal(err)
	}
	params := url.Values{"start": {time.Unix(data.Run.Start, 0).UTC().Format(time.RFC3339)}, "end": {time.Unix(data.Run.End, 0).UTC().Format(time.RFC3339)}}
	data.Records = map[string]*collectionRecord{
		"names":    {collectionEnvelope: collectionEnvelope{OK: true, Status: 200, Request: collectionRequest{Kind: "discovery", URL: w.Endpoint + "/api/v1/label/__name__/values?" + params.Encode()}}, Body: string(body)},
		"resource": {collectionEnvelope: collectionEnvelope{OK: true, Status: 200, Request: collectionRequest{Kind: "resource", URL: "https://management.azure.com" + w.ID}}, Body: fmt.Sprintf(`{"properties":{"accountId":"incarnation-1","metrics":{"prometheusQueryEndpoint":%q}}}`, w.Endpoint)},
	}
	return data
}

func encodeCatalogSeed(t *testing.T, data collectionData) string {
	t.Helper()
	b, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCatalogColdStartAndRefresh(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "empty.db")
	s, err := OpenScanStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	original := encodeCatalogSeed(t, catalogSeed(t))
	if err := s.Initialize(ctx, strings.NewReader(original)); err != nil {
		t.Fatal(err)
	}
	summary, err := s.Summary(ctx)
	if err != nil || !summary.SchedulingComplete || summary.CoverageComplete || summary.EmptyCatalogs != 1 || summary.RankingTotal != 0 || summary.Observations != 0 {
		t.Fatalf("empty is scheduling complete, not measured zero: %+v %v", summary, err)
	}
	if err := s.Scan(ctx, ScanOptions{Workers: 1, Credential: scanTestCredential{}, Transport: collectionTestTransport(func(*http.Request) (*http.Response, error) {
		t.Error("empty catalog issued a query")
		return nil, fmt.Errorf("unexpected request")
	})}); err != nil {
		t.Fatal(err)
	}
	if err := RenderDatabase(io.Discard, path); err != nil {
		t.Fatalf("empty report: %v", err)
	}
	var evidence, count int
	if err := s.db.QueryRow(`SELECT e.ok,c.metric_count FROM catalog_observation c JOIN evidence e ON e.id=c.evidence_id`).Scan(&evidence, &count); err != nil || evidence != 1 || count != 0 {
		t.Fatalf("empty evidence missing: %d %d %v", evidence, count, err)
	}
	fresh := encodeCatalogSeed(t, catalogSeed(t, "up", "kube_pod_info"))
	if err := s.RefreshCatalog(ctx, strings.NewReader(fresh)); err != nil {
		t.Fatal(err)
	}
	summary, err = s.Summary(ctx)
	if err != nil || summary.Metrics != 2 || summary.RankingTotal != 6 || summary.Queries["pending"] != 6 || summary.State != "active" {
		t.Fatalf("refresh: %+v %v", summary, err)
	}
	if err := s.Initialize(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Initialize(ctx, strings.NewReader(original)); err != nil {
		t.Fatal(err)
	}
	if err := s.Initialize(ctx, strings.NewReader(fresh)); err == nil {
		t.Fatal("ordinary resume accepted a changed fingerprint")
	}
	if err := s.RefreshCatalog(ctx, strings.NewReader(fresh)); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT count(*) FROM query`).Scan(&count); err != nil || count != 6 {
		t.Fatalf("duplicate plans: %d %v", count, err)
	}
	if err := s.db.QueryRow(`SELECT count(*) FROM catalog_refresh WHERE seed_hash=? AND spec_fingerprint=?`, scanHash(fresh), scanHash(original)).Scan(&count); err != nil || count != 2 {
		t.Fatalf("refresh provenance: %d %v", count, err)
	}
}

func TestCatalogRejectUnprovenEmpty(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*collectionData)
	}{
		{"missing", func(d *collectionData) { delete(d.Records, "names") }},
		{"http", func(d *collectionData) { d.Records["names"].Status = 500 }},
		{"null", func(d *collectionData) { d.Records["names"].Body = `{"status":"success","data":null}` }},
		{"error", func(d *collectionData) { d.Records["names"].Body = `{"status":"error","data":[]}` }},
		{"malformed", func(d *collectionData) { d.Records["names"].Body = `{` }},
		{"mismatch", func(d *collectionData) { d.Records["names"].Body = `{"status":"success","data":["up"]}` }},
		{"warnings", func(d *collectionData) {
			d.Records["names"].Body = `{"status":"success","data":[],"warnings":["partial"]}`
		}},
		{"window", func(d *collectionData) { d.Run.Start++ }},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := catalogSeed(t)
			test.change(&d)
			s, err := OpenScanStore(filepath.Join(t.TempDir(), "scan.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err := s.Initialize(context.Background(), strings.NewReader(encodeCatalogSeed(t, d))); err == nil {
				t.Fatal("unproven empty catalog accepted")
			}
			var count int
			if err := s.db.QueryRow(`SELECT count(*) FROM run`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("partial import: %d %v", count, err)
			}
		})
	}
}

func TestFailedWorkspaceDoesNotRollbackHealthyCatalog(t *testing.T) {
	for _, failure := range []string{"discovery", "endpoint", "both endpoints"} {
		t.Run(failure, func(t *testing.T) {
			d := catalogSeed(t, "up")
			bad := *d.Workspaces[0]
			bad.Name, bad.ID = "failed", collectionTestID+"-failed"
			bad.Endpoint = "https://failed.westus3.prometheus.monitor.azure.com"
			bad.Names, bad.Discovery = nil, "failed-names"
			d.Workspaces = append(d.Workspaces, &bad)
			record := *d.Records["names"]
			record.OK, record.Status, record.Body = false, 503, `{"error":"unavailable"}`
			record.Request.URL = strings.Replace(record.Request.URL, d.Workspaces[0].Endpoint, bad.Endpoint, 1)
			d.Records[bad.Discovery] = &record
			if failure != "discovery" {
				bad.Endpoint, bad.Discovery = "", ""
				record.Request.URL = "https://management.azure.com" + bad.ID
				if failure == "both endpoints" {
					d.Workspaces[0].Names = nil
					d.Workspaces[0].Endpoint, d.Workspaces[0].Discovery = "", ""
					d.Records["resource"].OK = false
				}
			}
			path := filepath.Join(t.TempDir(), "scan.db")
			s, err := OpenScanStore(path)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err := s.Initialize(t.Context(), strings.NewReader(encodeCatalogSeed(t, d))); err != nil {
				t.Fatalf("failed workspace rolled back healthy import: %v", err)
			}
			var blocked string
			var count int
			if err := s.db.QueryRow(`SELECT blocked_reason FROM workspace WHERE name='failed'`).Scan(&blocked); err != nil || blocked == "" {
				t.Fatalf("failed workspace not retained: %q, %v", blocked, err)
			}
			if err := s.db.QueryRow(`SELECT count(*) FROM query q JOIN workspace w ON w.id=q.workspace_id WHERE w.blocked_reason<>''`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("failed workspace planned queries: %d, %v", count, err)
			}
			requests := 0
			transport := collectionTestTransport(func(req *http.Request) (*http.Response, error) {
				requests++
				if req.URL.Host != "test.westus3.prometheus.monitor.azure.com" {
					t.Errorf("request to failed/untrusted workspace: %s", req.URL)
				}
				return collectionTestResponse(`{"status":"success","data":{"resultType":"vector","result":[]}}`, 200), nil
			})
			if err := s.Scan(t.Context(), ScanOptions{Workers: 1, Credential: scanTestCredential{}, Transport: transport}); err != nil {
				t.Fatal(err)
			}
			want := 3
			if failure == "both endpoints" {
				want = 0
			}
			if requests != want {
				t.Fatalf("healthy scan requests = %d, want %d", requests, want)
			}
			summary, err := s.Summary(t.Context())
			if err != nil || summary.Workspaces != 2 || summary.CoverageComplete || summary.RankingSucceeded != want {
				t.Fatalf("false coverage or missing healthy results: %+v, %v", summary, err)
			}
			var report strings.Builder
			if err := RenderDatabaseContext(t.Context(), &report, path); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(report.String(), "failed: workspace coverage unavailable") || !strings.Contains(report.String(), "unknown, not zero") {
				t.Fatal("report hid failed-workspace coverage")
			}
		})
	}
}

func TestCatalogRefreshGuardsAndRollback(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*collectionData)
	}{
		{"window", func(d *collectionData) { d.Run.End++ }},
		{"platform", func(d *collectionData) { d.Run.PlatformStart++ }},
		{"workspace", func(d *collectionData) { d.Workspaces[0].ID += "new" }},
		{"endpoint", func(d *collectionData) { d.Workspaces[0].Endpoint = "https://new.westus3.prometheus.monitor.azure.com" }},
		{"account", func(d *collectionData) {
			d.Records["resource"].Body = strings.ReplaceAll(d.Records["resource"].Body, "incarnation-1", "incarnation-2")
		}},
		{"missing-account", func(d *collectionData) { delete(d.Records, "resource") }},
		{"failed-discovery", func(d *collectionData) { d.Records["names"].OK = false }},
		{"duplicate", func(d *collectionData) { d.Workspaces = append(d.Workspaces, d.Workspaces[0]) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, err := OpenScanStore(filepath.Join(t.TempDir(), "scan.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err := s.Initialize(context.Background(), strings.NewReader(encodeCatalogSeed(t, catalogSeed(t)))); err != nil {
				t.Fatal(err)
			}
			d := catalogSeed(t, "up")
			test.change(&d)
			if err := s.RefreshCatalog(context.Background(), strings.NewReader(encodeCatalogSeed(t, d))); err == nil {
				t.Fatal("unsafe refresh accepted")
			}
			var count int
			if err := s.db.QueryRow(`SELECT (SELECT count(*) FROM metric)+(SELECT count(*) FROM catalog_refresh)+(SELECT count(*) FROM catalog_observation)-1`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("partial refresh: %d %v", count, err)
			}
		})
	}
}

func TestCatalogRetryEmptyPreservesHistory(t *testing.T) {
	s, _ := newScanTestStore(t)
	ctx := context.Background()
	var empty, nonempty string
	if err := s.db.QueryRow(`SELECT id FROM query WHERE state='pending' LIMIT 1`).Scan(&empty); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT id FROM query WHERE state='succeeded'`).Scan(&nonempty); err != nil {
		t.Fatal(err)
	}
	partitionTestAccept(t, s, empty, -1)
	var oldAttempt, nonemptyAttempt int
	if err := s.db.QueryRow(`SELECT accepted_attempt_id FROM query WHERE id=?`, empty).Scan(&oldAttempt); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT accepted_attempt_id FROM query WHERE id=?`, nonempty).Scan(&nonemptyAttempt); err != nil {
		t.Fatal(err)
	}
	fresh := encodeCatalogSeed(t, catalogSeed(t, "up", "other", "kube_pod_info"))
	if err := s.RefreshCatalog(ctx, strings.NewReader(fresh)); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRow(`SELECT accepted_attempt_id FROM query WHERE id=?`, empty).Scan(&count); err != nil || count != oldAttempt {
		t.Fatalf("implicit retry: %d %v", count, err)
	}
	if err := s.RefreshCatalogRetryEmpty(ctx, strings.NewReader(fresh)); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT count(*) FROM query WHERE id=? AND state='pending' AND accepted_attempt_id IS NULL`, empty).Scan(&count); err != nil || count != 1 {
		t.Fatalf("empty not reset: %d %v", count, err)
	}
	if err := s.db.QueryRow(`SELECT accepted_attempt_id FROM query WHERE id=?`, nonempty).Scan(&count); err != nil || count != nonemptyAttempt {
		t.Fatalf("nonempty reset: %d %v", count, err)
	}
	if err := s.db.QueryRow(`SELECT count(*) FROM attempt WHERE id=? AND state='succeeded'`, oldAttempt).Scan(&count); err != nil || count != 1 {
		t.Fatalf("history lost: %d %v", count, err)
	}
	if _, err := s.db.Exec(`UPDATE query SET state='blocked',classification='test' WHERE state='pending' AND id<>?`, empty); err != nil {
		t.Fatal(err)
	}
	requests := 0
	if err := s.Scan(ctx, ScanOptions{Workers: 1, Credential: scanTestCredential{}, Transport: collectionTestTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		at, err := time.Parse(time.RFC3339, r.URL.Query().Get("time"))
		if err != nil {
			t.Fatal(err)
		}
		return collectionTestResponse(fmt.Sprintf(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{"namespace":"late"},"value":[%d,"9"]}]}}`, at.Unix()), 200), nil
	})}); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("retried nonempty or unrelated queries: %d", requests)
	}
	if err := s.db.QueryRow(`SELECT integer_value FROM accepted_observation WHERE query_id=?`, empty).Scan(&count); err != nil || count != 9 {
		t.Fatalf("late result missing: %d %v", count, err)
	}
}

func TestCollectEmptyThenRefresh(t *testing.T) {
	ctx := context.Background()
	o := collectionTestOptions(t)
	o.End = o.Start.Add(5 * time.Minute)
	empty := true
	o.Transport = collectionTestTransport(func(r *http.Request) (*http.Response, error) {
		if empty && strings.HasSuffix(r.URL.Path, "/values") {
			return collectionTestResponse(`{"status":"success","data":[]}`, 200), nil
		}
		return collectionTestResponse(collectionTestBody(r), 200), nil
	})
	s, err := OpenScanStore(filepath.Join(t.TempDir(), "scan.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for pass := 0; pass < 2; pass++ {
		o.Output = filepath.Join(t.TempDir(), "seed.json")
		summary, err := Collect(ctx, o)
		if err != nil || summary.PromQL != 0 || summary.Failed != 0 {
			t.Fatalf("collection: %+v %v", summary, err)
		}
		seed, err := os.Open(o.Output)
		if err != nil {
			t.Fatal(err)
		}
		if empty {
			err = s.Initialize(ctx, seed)
		} else {
			err = s.RefreshCatalog(ctx, seed)
		}
		seed.Close()
		if err != nil {
			t.Fatal(err)
		}
		empty = false
	}
	summary, err := s.Summary(ctx)
	if err != nil || summary.RankingTotal != 6 || summary.Observations != 0 {
		t.Fatalf("cold collection refresh: %+v %v", summary, err)
	}
}

func TestCatalogRefreshTransactionAndLease(t *testing.T) {
	s, _ := newScanTestStore(t)
	ctx := context.Background()
	fresh := encodeCatalogSeed(t, catalogSeed(t, "up", "other", "new_metric"))
	claim, _, err := s.claimScan(ctx, "active-scanner", time.Now())
	if err != nil || claim == nil {
		t.Fatalf("claim: %v %v", claim, err)
	}
	if err := s.RefreshCatalog(ctx, strings.NewReader(fresh)); err == nil || !strings.Contains(err.Error(), "running leases") {
		t.Fatalf("refresh raced scanner: %v", err)
	}
	if err := s.releaseScanClaims(ctx, "active-scanner"); err != nil {
		t.Fatal(err)
	}
	// Fail after metric/query insertion to verify the entire refresh rolls back.
	if _, err := s.db.Exec(`CREATE TRIGGER reject_catalog BEFORE INSERT ON catalog_observation BEGIN SELECT RAISE(ABORT,'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.RefreshCatalog(ctx, strings.NewReader(fresh)); err == nil || !strings.Contains(err.Error(), "injected failure") {
		t.Fatalf("injection: %v", err)
	}
	var count int
	if err := s.db.QueryRow(`SELECT (SELECT count(*) FROM metric WHERE name='new_metric')+(SELECT count(*) FROM catalog_refresh)+(SELECT count(*) FROM evidence WHERE id LIKE 'catalog-refresh-%')`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial transaction: %d %v", count, err)
	}
	if _, err := s.db.Exec(`DROP TRIGGER reject_catalog`); err != nil {
		t.Fatal(err)
	}
	if err := s.RefreshCatalog(ctx, strings.NewReader(fresh)); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogRetryEmptyDoesNotResetPartitionTrees(t *testing.T) {
	s, _, root := namespaceInventoryFixture(t)
	ctx := context.Background()
	if _, err := s.PlanNamespaceRecovery(ctx); err != nil {
		t.Fatal(err)
	}
	for _, child := range partitionTestChildren(t, s, root) {
		partitionTestAccept(t, s, child, -1)
	}
	partitionTestFinalize(t, s)
	var before, after string
	const snapshot = `SELECT json_group_array(json_object('id',id,'state',state,'accepted',accepted_attempt_id)) FROM query WHERE id IN (SELECT root_id FROM namespace_inventory_plan UNION SELECT child_id FROM namespace_inventory_child)`
	if err := s.db.QueryRow(snapshot).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := s.RefreshCatalogRetryEmpty(ctx, strings.NewReader(encodeCatalogSeed(t, catalogSeed(t, "up", "other")))); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(snapshot).Scan(&after); err != nil || before != after {
		t.Fatalf("partition history reset: %s %s %v", before, after, err)
	}
}

func TestCatalogMixedEmptyWorkspaceCoverage(t *testing.T) {
	d := catalogSeed(t, "up")
	empty := catalogSeed(t)
	w := *empty.Workspaces[0]
	w.Name, w.ID, w.Endpoint, w.Discovery = "empty", collectionTestID+"-empty", "https://empty.westus3.prometheus.monitor.azure.com", "empty-names"
	d.Workspaces = append(d.Workspaces, &w)
	r := *empty.Records["names"]
	r.Request.URL = strings.Replace(r.Request.URL, "https://test.", "https://empty.", 1)
	d.Records["empty-names"] = &r
	s, err := OpenScanStore(filepath.Join(t.TempDir(), "mixed.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Initialize(context.Background(), strings.NewReader(encodeCatalogSeed(t, d))); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"before12h", "end12h", "run"} {
		var id string
		if err := s.db.QueryRow(`SELECT id FROM query WHERE kind=?`, kind).Scan(&id); err != nil {
			t.Fatal(err)
		}
		partitionTestAccept(t, s, id, 3)
	}
	summary, err := s.Summary(context.Background())
	if err != nil || summary.CoverageComplete || summary.EmptyCatalogs != 1 || summary.RankingSucceeded != 3 {
		t.Fatalf("nonempty workspace masked unknown empty workspace: %+v %v", summary, err)
	}
}
