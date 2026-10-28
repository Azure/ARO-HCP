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
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func namespaceInventoryFixture(t *testing.T) (*ScanStore, string, string) {
	t.Helper()
	s, path := newScanTestStore(t)
	var root, before, end string
	for _, v := range []struct {
		kind string
		dest *string
	}{{"before12h", &before}, {"end12h", &end}} {
		if err := s.db.QueryRow(`SELECT q.id FROM query q JOIN metric m ON m.id=q.metric_id WHERE m.name='up' AND q.kind=?`, v.kind).Scan(v.dest); err != nil {
			t.Fatal(err)
		}
		partitionTestAccept(t, s, *v.dest, -1)
	}
	if err := s.db.QueryRow(`SELECT q.id FROM query q JOIN metric m ON m.id=q.metric_id WHERE m.name='other' AND q.kind='run'`).Scan(&root); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE query SET state='blocked',classification='test_unrelated' WHERE state='pending'; UPDATE query SET classification='hard_limit' WHERE id=?`, root); err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	for i, pair := range []map[string]string{{"Cluster": "ABC", "Namespace": "NS.A"}, {"cluster": "xyz", "namespace": "ns|b"}, {"cluster": "abc", "namespace": ""}, {"cluster": "abc"}} {
		labels, err := internLabels(context.Background(), tx, pair)
		if err != nil {
			t.Fatal(err)
		}
		id := before
		if i == 1 {
			id = end
		}
		if _, err := tx.Exec(`INSERT INTO observation SELECT accepted_attempt_id,?,evaluation_time,1,1,NULL FROM query WHERE id=?`, labels, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return s, path, root
}

func TestNamespaceInventoryImmutableResumeAndCoverage(t *testing.T) {
	s, path, root := namespaceInventoryFixture(t)
	ctx := context.Background()
	if n, err := s.PlanNamespaceRecovery(ctx); err != nil || n != 5 {
		t.Fatalf("plan %d: %v", n, err)
	}
	var manifest, hash string
	if err := s.db.QueryRow(`SELECT candidates_json,candidate_snapshot_hash FROM namespace_inventory_plan`).Scan(&manifest, &hash); err != nil {
		t.Fatal(err)
	}
	var candidates []namespaceCandidate
	if err := json.Unmarshal([]byte(manifest), &candidates); err != nil || len(candidates) != 2 || scanHash(manifest) != hash {
		t.Fatalf("candidates: %s %v", manifest, err)
	}
	for _, c := range candidates {
		if c.SourceQuery == "" || c.AttemptID == 0 || c.End-c.Start != 43200 || c.Namespace == "" {
			t.Fatalf("missing provenance %+v", c)
		}
	}
	if _, err := s.db.Exec(`UPDATE query SET ranking=0 WHERE kind='before12h'`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := OpenScanStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if n, err := s.PlanNamespaceRecovery(ctx); err != nil || n != 0 {
		t.Fatalf("resume mutated plan %d %v", n, err)
	}
	var after string
	if err := s.db.QueryRow(`SELECT candidates_json FROM namespace_inventory_plan`).Scan(&after); err != nil || after != manifest {
		t.Fatal("manifest changed", err)
	}
	children := partitionTestChildren(t, s, root)
	// A failed complement must not suppress unexecuted exact literals.
	var complement string
	if err := s.db.QueryRow(`SELECT child_id FROM namespace_inventory_child WHERE child_role='cluster_complement'`).Scan(&complement); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE query SET state='blocked',classification='hard_limit' WHERE id=?`, complement); err != nil {
		t.Fatal(err)
	}
	if planned, err := s.planHardLimitPartitions(ctx); err != nil || planned {
		t.Fatalf("inventory autoexpanded: %v %v", planned, err)
	}
	var pending int
	if err := s.db.QueryRow(`SELECT count(*) FROM query WHERE state='pending' AND id IN (SELECT child_id FROM namespace_inventory_child)`).Scan(&pending); err != nil || pending != 4 {
		t.Fatalf("siblings suppressed %d %v", pending, err)
	}
	var class string
	if err := s.db.QueryRow(`SELECT classification FROM query WHERE id=?`, root).Scan(&class); err != nil || class != "namespace_inventory_pending" {
		t.Fatalf("early terminal %s %v", class, err)
	}
	for _, id := range children {
		if id != complement {
			partitionTestAccept(t, s, id, 3)
		}
	}
	partitionTestFinalize(t, s)
	if err := s.db.QueryRow(`SELECT classification FROM query WHERE id=? AND accepted_attempt_id IS NULL`, root).Scan(&class); err != nil || class != "namespace_inventory_incomplete" {
		t.Fatalf("false complete %s %v", class, err)
	}
	var lower int
	if err := s.db.QueryRow(`SELECT sum(o.integer_value) FROM query_partition l JOIN query c ON c.id=l.child_id JOIN observation o ON o.attempt_id=c.accepted_attempt_id WHERE l.parent_id=? AND l.active=1`, root).Scan(&lower); err != nil || lower != 12 {
		t.Fatalf("lost lower bound %d %v", lower, err)
	}
}

func TestNamespaceInventoryDisjointAMW(t *testing.T) {
	s, _, _ := namespaceInventoryFixture(t)
	if _, err := s.PlanNamespaceRecovery(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows, err := s.db.Query(`SELECT predicate FROM namespace_inventory_child`)
	if err != nil {
		t.Fatal(err)
	}
	var parts []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			t.Fatal(err)
		}
		parts = append(parts, p)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	matcher := regexp.MustCompile(`(cluster|namespace)(=|!~)("(?:[^"\\]|\\.)*")`)
	for _, cluster := range []string{"ABC", "xyz", "unknown", "", "\n"} {
		for _, namespace := range []string{"NS.A", "ns|b", "nsxa", "ns", "unknown", "", "\n"} {
			matches := 0
			for _, part := range parts {
				ok := true
				for _, m := range matcher.FindAllStringSubmatch(part, -1) {
					v := cluster
					if m[1] == "namespace" {
						v = namespace
					}
					v = strings.ToLower(v)
					want, err := strconv.Unquote(m[3])
					if err != nil {
						t.Fatal(err)
					}
					if m[2] == "=" {
						ok = ok && strings.EqualFold(v, want)
					} else {
						ok = ok && !regexp.MustCompile("(?i)"+want).MatchString(v)
					}
				}
				if ok {
					matches++
				}
			}
			if matches != 1 {
				t.Fatalf("%q/%q matched %d: %v", cluster, namespace, matches, parts)
			}
		}
	}
	for _, part := range parts {
		if strings.Contains(part, `cluster="abc",namespace="ns|b"`) {
			t.Fatal("cross-product candidate")
		}
	}
}

func TestNamespaceInventoryReuseHistoricalEdgeAndComplete(t *testing.T) {
	s, _, root := namespaceInventoryFixture(t)
	ctx := context.Background()
	if _, err := s.RepairPartition(ctx, root); err != nil {
		t.Fatal(err)
	}
	var oldParent string
	if err := s.db.QueryRow(`SELECT child_id FROM query_partition WHERE parent_id=? AND child_predicate='cluster="abc"'`, root).Scan(&oldParent); err != nil {
		t.Fatal(err)
	}
	var expression, params string
	if err := s.db.QueryRow(`SELECT expression,params FROM query WHERE id=?`, root).Scan(&expression, &params); err != nil {
		t.Fatal(err)
	}
	expression = strings.Replace(expression, "other[", `other{cluster="abc",namespace="ns.a"}[`, 1)
	v, _ := url.ParseQuery(params)
	v.Set("query", expression)
	if _, err := s.db.Exec(`INSERT INTO query(id,workspace_id,metric_id,window_id,kind,ranking,path,params,expression,evaluation_time,lookback_seconds,grouping,state)
 SELECT 'legacy-alias',workspace_id,metric_id,window_id,kind,0,path,?,?,evaluation_time,lookback_seconds,grouping,'pending' FROM query WHERE id=?`, v.Encode(), expression, root); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO query_partition VALUES(?,'legacy-alias','namespace','cluster="abc"','cluster="abc",namespace="ns.a"',1)`, oldParent); err != nil {
		t.Fatal(err)
	}
	partitionTestAccept(t, s, "legacy-alias", 7)
	if n, err := s.PlanNamespaceRecovery(ctx); err != nil || n != 5 {
		t.Fatalf("reuse %d %v", n, err)
	}
	var history int
	if err := s.db.QueryRow(`SELECT count(*) FROM namespace_inventory_edge_history WHERE child_id='legacy-alias' AND parent_id=?`, oldParent).Scan(&history); err != nil || history != 1 {
		t.Fatalf("history %d %v", history, err)
	}
	for _, id := range partitionTestChildren(t, s, root) {
		if id != "legacy-alias" {
			partitionTestAccept(t, s, id, 2)
		}
	}
	partitionTestFinalize(t, s)
	partitionTestFinalize(t, s)
	var total, attempts int
	if err := s.db.QueryRow(`SELECT sum(integer_value) FROM accepted_observation WHERE query_id=?`, root).Scan(&total); err != nil || total != 15 {
		t.Fatalf("total %d %v", total, err)
	}
	if err := s.db.QueryRow(`SELECT count(*) FROM attempt WHERE query_id='legacy-alias'`).Scan(&attempts); err != nil || attempts != 1 {
		t.Fatalf("duplicate accepted %d %v", attempts, err)
	}
}

func TestNamespaceInventoryScanCrashScopeAndBudget(t *testing.T) {
	s, _, root := namespaceInventoryFixture(t)
	ctx := context.Background()
	if _, err := s.PlanNamespaceRecovery(ctx); err != nil {
		t.Fatal(err)
	}
	claim, _, err := s.claimScan(ctx, "crashed", time.Now().Add(-2*time.Minute), true)
	if err != nil || claim == nil {
		t.Fatalf("claim %v %v", claim, err)
	}
	if _, err := s.db.Exec(`UPDATE query SET state='pending' WHERE classification='test_unrelated'`); err != nil {
		t.Fatal(err)
	}
	requests := 0
	opts := ScanOptions{Workers: 1, NamespaceOnly: true, Credential: scanTestCredential{}, Transport: collectionTestTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.Method != "GET" || !strings.Contains(r.URL.Query().Get("query"), "other{") {
			t.Errorf("outside scope: %s", r.URL)
		}
		body := `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"job":"same"},"value":[100600,"2"]}]}}`
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	if err := s.Scan(ctx, opts); err != nil {
		t.Fatal(err)
	}
	if requests != 5 {
		t.Fatalf("requests %d", requests)
	}
	if err := s.Scan(ctx, opts); err != nil || requests != 5 {
		t.Fatalf("resume reran requests %d %v", requests, err)
	}
	var value int
	if err := s.db.QueryRow(`SELECT sum(integer_value) FROM accepted_observation WHERE query_id=?`, root).Scan(&value); err != nil || value != 10 {
		t.Fatalf("aggregate %d %v", value, err)
	}
	// A separate plan at its durable budget may not issue even one extra GET.
	s2, _, _ := namespaceInventoryFixture(t)
	if _, err := s2.PlanNamespaceRecovery(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.db.Exec(`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<1500)
 INSERT INTO attempt(query_id,owner,started_at,state,classification) SELECT (SELECT child_id FROM namespace_inventory_child LIMIT 1),'crash',1,'abandoned','lease_expired' FROM n`); err != nil {
		t.Fatal(err)
	}
	if err := s2.Scan(ctx, opts); err != nil || requests != 5 {
		t.Fatalf("budget exceeded %d %v", requests, err)
	}
}

func TestNamespaceInventoryDeferEmptyCandidates(t *testing.T) {
	s, _, root := namespaceInventoryFixture(t)
	if _, err := s.db.Exec(`UPDATE query SET ranking=0 WHERE kind IN ('before12h','end12h')`); err != nil {
		t.Fatal(err)
	}
	if n, err := s.PlanNamespaceRecovery(context.Background()); err != nil || n != 0 {
		t.Fatalf("empty inventory must defer: %d %v", n, err)
	}
	var class string
	var plans int
	if err := s.db.QueryRow(`SELECT classification,(SELECT count(*) FROM namespace_inventory_plan) FROM query WHERE id=? AND accepted_attempt_id IS NULL`, root).Scan(&class, &plans); err != nil || class != "waiting_inventory" || plans != 0 {
		t.Fatalf("deferred root: %s %d %v", class, plans, err)
	}
	if _, err := s.db.Exec(`UPDATE query SET ranking=1 WHERE kind IN ('before12h','end12h')`); err != nil {
		t.Fatal(err)
	}
	if n, err := s.PlanNamespaceRecovery(context.Background()); err != nil || n != 5 {
		t.Fatalf("late inventory did not plan: %d %v", n, err)
	}
	// A previously planned root must not prevent a newly eligible root.
	if _, err := s.db.Exec(`UPDATE query SET classification='hard_limit' WHERE kind='end12h' AND state='blocked'`); err != nil {
		t.Fatal(err)
	}
	if n, err := s.PlanNamespaceRecovery(context.Background()); err != nil || n != 5 {
		t.Fatalf("per-root planning: %d %v", n, err)
	}
	if n, err := s.PlanNamespaceRecovery(context.Background()); err != nil || n != 0 {
		t.Fatalf("repeat appended overlapping plans: %d %v", n, err)
	}
}

func TestNamespaceInventoryCandidatesRejectWrongWindow(t *testing.T) {
	for _, statement := range []string{
		`UPDATE query SET evaluation_time=evaluation_time+1 WHERE kind='before12h'`,
		`UPDATE window SET end=end+1,start=start+1 WHERE kind='before12h'`,
		`UPDATE query SET lookback_seconds=60 WHERE kind='before12h'`,
		`UPDATE query SET kind='run' WHERE kind='before12h'`,
		`UPDATE attempt SET warnings_json='["partial"]' WHERE id IN (SELECT accepted_attempt_id FROM query WHERE kind='before12h')`,
	} {
		t.Run(statement, func(t *testing.T) {
			s, _, _ := namespaceInventoryFixture(t)
			if _, err := s.db.Exec(statement); err != nil {
				t.Fatal(err)
			}
			if n, err := s.PlanNamespaceRecovery(context.Background()); err != nil || n != 3 {
				t.Fatalf("wrong-window source included: %d %v", n, err)
			}
			var manifest string
			if err := s.db.QueryRow(`SELECT candidates_json FROM namespace_inventory_plan`).Scan(&manifest); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(manifest, `"Cluster":"abc"`) {
				t.Fatalf("out-of-window candidate: %s", manifest)
			}
		})
	}
}
