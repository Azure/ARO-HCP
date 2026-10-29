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
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPartitionPredicatesExhaustive(t *testing.T) {
	for _, values := range [][]string{{"a.b", "x|y", "quote\"slash\\\n", "a.b"}, {"", "a"}} {
		parts, err := partitionPredicates("", "cluster", values)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range append(append([]string{}, values...), "unseen", "axb", "x", "y", "", "\n") {
			matches := 0
			for _, part := range parts {
				negative := strings.HasPrefix(part, "cluster!~")
				literal := strings.TrimPrefix(strings.TrimPrefix(part, "cluster!~"), "cluster=")
				decoded, err := strconv.Unquote(literal)
				if err != nil {
					t.Fatal(err)
				}
				if (!negative && value == decoded) || (negative && !regexp.MustCompile(decoded).MatchString(value)) {
					matches++
				}
			}
			if matches != 1 {
				t.Fatalf("value %q (empty also represents absent label) matched %d partitions: %v", value, matches, parts)
			}
		}
	}
	parts, err := partitionPredicates(`cluster="a"`, "namespace", []string{"n"})
	if err != nil || len(parts) != 2 || !strings.HasPrefix(parts[1], `cluster="a",namespace!~`) {
		t.Fatalf("parent predicate lost: %v %v", parts, err)
	}
	if _, err := partitionPredicates("", "__name__", []string{"a"}); err == nil {
		t.Fatal("metric-name partition accepted")
	}
}

func partitionTestParent(t *testing.T, s *ScanStore) string {
	t.Helper()
	var parent string
	if err := s.db.QueryRow(`SELECT id FROM query WHERE ranking=1 AND kind='run' AND state='pending' LIMIT 1`).Scan(&parent); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE query SET state='blocked',classification='hard_limit' WHERE id=?`, parent); err != nil {
		t.Fatal(err)
	}
	return parent
}

func partitionTestChildren(t *testing.T, s *ScanStore, parent string) []string {
	t.Helper()
	rows, err := s.db.Query(`SELECT child_id FROM query_partition WHERE parent_id=? AND active=1 ORDER BY child_id`, parent)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

func partitionTestAccept(t *testing.T, s *ScanStore, id string, count int64) {
	t.Helper()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.Exec(`INSERT INTO attempt(query_id,owner,started_at,finished_at,state,http_status,response_bytes,duration_ms,api_cost,stats_json) VALUES(?,'test',1,2,'succeeded',200,123,456,7,'{"test":true}')`, id)
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if count >= 0 {
		// Same output group across partitions exercises additive regrouping.
		labels, err := internLabels(context.Background(), tx, map[string]string{"job": "same"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`INSERT INTO observation(attempt_id,labelset_id,timestamp,value,integer_value) SELECT ?,?,evaluation_time,?,? FROM query WHERE id=?`, attempt, labels, count, count, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(`UPDATE query SET state='succeeded',accepted_attempt_id=? WHERE id=?`, attempt, id); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func partitionTestFinalize(t *testing.T, s *ScanStore) {
	t.Helper()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := finalizeReadyPartitions(context.Background(), tx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestPartitionResumeAndAggregate(t *testing.T) {
	s, path := newScanTestStore(t)
	ctx := context.Background()
	parent := partitionTestParent(t, s)
	n, err := s.RepairPartition(ctx, parent)
	if err != nil || n != 2 {
		t.Fatalf("plan: %d %v", n, err)
	}
	children := partitionTestChildren(t, s, parent)
	for _, child := range children {
		var expression, params string
		var ranking int
		if err := s.db.QueryRow(`SELECT expression,params,ranking FROM query WHERE id=?`, child).Scan(&expression, &params, &ranking); err != nil {
			t.Fatal(err)
		}
		v, err := url.ParseQuery(params)
		if err != nil || v.Get("query") != expression || !strings.Contains(expression, "[600000ms]") || ranking != 0 {
			t.Fatalf("child changed measurement or ranking: %s %s %d %v", expression, params, ranking, err)
		}
	}
	partitionTestAccept(t, s, children[0], 4)
	partitionTestFinalize(t, s)
	var accepted int
	if err := s.db.QueryRow(`SELECT count(*) FROM query WHERE id=? AND accepted_attempt_id IS NOT NULL`, parent).Scan(&accepted); err != nil || accepted != 0 {
		t.Fatalf("partial parent accepted: %d %v", accepted, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenScanStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if n, err := s.RepairPartition(ctx, parent); err != nil || n != 0 {
		t.Fatalf("resume duplicated plan: %d %v", n, err)
	}
	partitionTestAccept(t, s, children[1], 9)
	// Use the real claim hook, with no other schedulable work.
	if _, err := s.db.Exec(`UPDATE query SET state='blocked' WHERE state='pending'`); err != nil {
		t.Fatal(err)
	}
	claim, done, err := s.claimScan(ctx, "test", time.Now())
	if err != nil || claim != nil || !done {
		t.Fatalf("finalize before done: %v %v %v", claim, done, err)
	}
	var value int64
	if err := s.db.QueryRow(`SELECT integer_value FROM accepted_observation WHERE query_id=? AND ranking=1`, parent).Scan(&value); err != nil || value != 13 {
		t.Fatalf("aggregate: %d %v", value, err)
	}
	partitionTestFinalize(t, s)
	var attempts, bytes, duration int
	var cost float64
	if err := s.db.QueryRow(`SELECT count(*),sum(response_bytes),sum(duration_ms),sum(api_cost) FROM attempt WHERE query_id IN (SELECT child_id FROM query_partition WHERE parent_id=?) OR query_id=?`, parent, parent).Scan(&attempts, &bytes, &duration, &cost); err != nil || attempts != 3 || bytes != 246 || duration != 912 || cost != 14 {
		t.Fatalf("synthetic attempt duplicated actual API stats: %d %d %d %g %v", attempts, bytes, duration, cost, err)
	}
}

func TestPartitionRecursiveAndUnknown(t *testing.T) {
	for _, failure := range []string{"hard_limit", "invalid", "empty"} {
		t.Run(failure, func(t *testing.T) {
			s, _ := newScanTestStore(t)
			ctx := context.Background()
			parent := partitionTestParent(t, s)
			if _, err := s.RepairPartition(ctx, parent); err != nil {
				t.Fatal(err)
			}
			children := partitionTestChildren(t, s, parent)
			partitionTestAccept(t, s, children[0], -1)
			if failure == "hard_limit" {
				if _, err := s.db.Exec(`UPDATE query SET state='blocked',classification='hard_limit' WHERE id=?`, children[1]); err != nil {
					t.Fatal(err)
				}
				partitionTestFinalize(t, s)
				var state string
				if err := s.db.QueryRow(`SELECT state FROM query WHERE id=?`, parent).Scan(&state); err != nil || state != "blocked" {
					t.Fatalf("failed child made parent known: %s %v", state, err)
				}
				// Add observed namespace evidence, then explicitly approve one
				// deeper plan. It must retain the cluster complement predicate.
				tx, err := s.db.Begin()
				if err != nil {
					t.Fatal(err)
				}
				lid, err := internLabels(ctx, tx, map[string]string{"namespace": "ns"})
				if err == nil {
					_, err = tx.Exec(`INSERT INTO observation(attempt_id,labelset_id,timestamp,value,integer_value) SELECT accepted_attempt_id,?,evaluation_time,1,1 FROM query WHERE id=?`, lid, children[0])
				}
				if err != nil {
					_ = tx.Rollback()
					t.Fatal(err)
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				if n, err := s.RepairPartition(ctx, children[1]); err != nil || n != 6 {
					t.Fatalf("namespace plan: %d %v", n, err)
				}
				for _, id := range partitionTestChildren(t, s, children[1]) {
					partitionTestAccept(t, s, id, 2)
				}
			} else {
				count := int64(-1)
				if failure == "invalid" {
					count = 2
				}
				partitionTestAccept(t, s, children[1], count)
				if failure == "invalid" {
					if _, err := s.db.Exec(`UPDATE observation SET integer_value=NULL,invalid_value='bad' WHERE attempt_id=(SELECT accepted_attempt_id FROM query WHERE id=?)`, children[1]); err != nil {
						t.Fatal(err)
					}
				}
			}
			partitionTestFinalize(t, s)
			var state string
			if err := s.db.QueryRow(`SELECT state FROM query WHERE id=?`, parent).Scan(&state); err != nil {
				t.Fatal(err)
			}
			want := "succeeded"
			if failure == "invalid" {
				want = "blocked"
			}
			if state != want {
				t.Fatalf("parent %s, expected %s", state, want)
			}
		})
	}
}

func TestPartitionQuotaAndAtomicity(t *testing.T) {
	values := make([]string, maxPartitionChildren)
	for i := range values {
		values[i] = fmt.Sprint(i)
	}
	if _, err := partitionPredicates("", "cluster", values); err == nil {
		t.Fatal("child quota not enforced")
	}
	s, _ := newScanTestStore(t)
	parent := partitionTestParent(t, s)
	// Force a failure after the first child insertion, and check rollback.
	if _, err := s.db.Exec(`CREATE TRIGGER fail_partition BEFORE INSERT ON query WHEN NEW.ranking=0 AND (SELECT count(*) FROM query WHERE ranking=0)>0 BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RepairPartition(context.Background(), parent); err == nil {
		t.Fatal("injected plan failure ignored")
	}
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM query WHERE ranking=0`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("partial plan persisted: %d %v", n, err)
	}
	var class string
	if err := s.db.QueryRow(`SELECT classification FROM query WHERE id=?`, parent).Scan(&class); err != nil || class != "hard_limit" {
		t.Fatalf("failed plan changed parent: %s %v", class, err)
	}
}

func TestPartitionOverflowRemainsUnknown(t *testing.T) {
	s, _ := newScanTestStore(t)
	parent := partitionTestParent(t, s)
	if _, err := s.RepairPartition(context.Background(), parent); err != nil {
		t.Fatal(err)
	}
	children := partitionTestChildren(t, s, parent)
	partitionTestAccept(t, s, children[0], math.MaxInt64)
	partitionTestAccept(t, s, children[1], 1)
	partitionTestFinalize(t, s)
	var unknown bool
	if err := s.db.QueryRow(`SELECT state='blocked' AND classification='partition_overflow' AND accepted_attempt_id IS NULL FROM query WHERE id=?`, parent).Scan(&unknown); err != nil || !unknown {
		t.Fatalf("overflow published or stopped scheduler: %v %v", unknown, err)
	}
}

func TestAutomaticPartitionNoFilterGate(t *testing.T) {
	for _, helpful := range []bool{false, true} {
		t.Run(fmt.Sprint(helpful), func(t *testing.T) {
			s, _ := newScanTestStore(t)
			ctx := context.Background()
			parent := partitionTestParent(t, s)
			planned, err := s.planHardLimitPartitions(ctx)
			if err != nil || !planned {
				t.Fatalf("automatic root plan: %v %v", planned, err)
			}
			var exact, complement string
			if err := s.db.QueryRow(`SELECT child_id FROM query_partition WHERE parent_id=? AND child_predicate LIKE 'cluster=%'`, parent).Scan(&exact); err != nil {
				t.Fatal(err)
			}
			if err := s.db.QueryRow(`SELECT child_id FROM query_partition WHERE parent_id=? AND child_predicate LIKE 'cluster!~%'`, parent).Scan(&complement); err != nil {
				t.Fatal(err)
			}
			failed, success := exact, complement
			if helpful {
				failed, success = complement, exact
			}
			if _, err := s.db.Exec(`UPDATE query SET state='blocked',classification='hard_limit' WHERE id=?`, failed); err != nil {
				t.Fatal(err)
			}
			if planned, err := s.planHardLimitPartitions(ctx); err != nil || !planned {
				t.Fatalf("namespace plan must not require a cluster success: %v %v", planned, err)
			}
			partitionTestAccept(t, s, success, -1)
			// Namespace choices come from normalized accepted seed evidence.
			tx, err := s.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			lid, err := internLabels(ctx, tx, map[string]string{"namespace": "ns"})
			if err == nil {
				_, err = tx.Exec(`INSERT INTO observation(attempt_id,labelset_id,timestamp,value,integer_value) SELECT accepted_attempt_id,?,evaluation_time,1,1 FROM query WHERE ranking=1 AND state='succeeded' LIMIT 1`, lid)
			}
			if err != nil {
				_ = tx.Rollback()
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if planned, err := s.planHardLimitPartitions(ctx); err != nil || planned {
				t.Fatalf("duplicate namespace plan: %v %v", planned, err)
			}
			{
				children := partitionTestChildren(t, s, failed)
				if len(children) != 6 {
					t.Fatalf("namespace children: %v", children)
				}
				var empty string
				if err := s.db.QueryRow(`SELECT l.child_id FROM query_partition l JOIN namespace_partition n ON n.query_id=l.child_id WHERE l.parent_id=? AND n.terminal=1`, failed).Scan(&empty); err != nil {
					t.Fatal(err)
				}
				if _, err := s.db.Exec(`UPDATE query SET state='blocked',classification='hard_limit' WHERE id=?`, empty); err != nil {
					t.Fatal(err)
				}
				for _, child := range children {
					if child != empty {
						partitionTestAccept(t, s, child, -1)
					}
				}
				if planned, err := s.planHardLimitPartitions(ctx); err != nil || !planned {
					t.Fatalf("empty namespace did not get instance fallback: %v %v", planned, err)
				}
				for _, child := range partitionTestChildren(t, s, empty) {
					if _, err := s.db.Exec(`UPDATE query SET state='blocked',classification='authorization' WHERE id=?`, child); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := s.planHardLimitPartitions(ctx); err != nil {
					t.Fatal(err)
				}
			}
			var exhausted bool
			if err := s.db.QueryRow(`SELECT state='blocked' AND classification='partition_exhausted' AND accepted_attempt_id IS NULL FROM query WHERE id=?`, parent).Scan(&exhausted); err != nil || !exhausted {
				t.Fatalf("root failure not propagated: %v %v", exhausted, err)
			}
			if planned, err := s.planHardLimitPartitions(ctx); err != nil || planned {
				t.Fatalf("terminal root replanned: %v %v", planned, err)
			}
		})
	}
}

func TestAutomaticPartitionScan(t *testing.T) {
	s, _ := newScanTestStore(t)
	ctx := context.Background()
	var calls int
	transport := collectionTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		expression := r.URL.Query().Get("query")
		if !strings.Contains(expression, "{") {
			return &http.Response{StatusCode: 429, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":"Query has exceeded the timeseries per metric limit"}`))}, nil
		}
		at, err := time.Parse(time.RFC3339, r.URL.Query().Get("time"))
		if err != nil {
			t.Fatal(err)
		}
		body := fmt.Sprintf(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{"job":"test"},"value":[%d,"3"]}]}}`, at.Unix())
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	if err := s.Scan(ctx, ScanOptions{Workers: 1, Credential: scanTestCredential{}, Transport: transport}); err != nil {
		t.Fatal(err)
	}
	summary, err := s.Summary(ctx)
	if err != nil || !summary.CoverageComplete || !summary.SchedulingComplete || calls != 15 {
		t.Fatalf("automatic scan incomplete: %+v calls=%d err=%v", summary, calls, err)
	}
}

func TestNamespaceRadixExhaustive(t *testing.T) {
	// Includes absent/empty, mixed case, punctuation, controls, non-ASCII,
	// Unicode case-fold equivalents, and strings shorter than suffix depth.
	values := []string{"", "a", "A", "k", "K", "\u212a", "\u017f", "\u00e9", "\u4e2d", "-", "\n", "\x00", "a\n", "\nA", "qa", "qaA", "qqaa", "qqqaa", "abcde"}
	for _, a := range "0afgpqz-" {
		for _, b := range "1bfhry_" {
			values = append(values, string([]rune{a, b}))
		}
	}
	matches := func(p namespacePartition, value string) bool {
		if literal, ok := namespaceLiteral(p); ok {
			return strings.EqualFold(value, literal)
		}
		pattern, err := strconv.Unquote(strings.TrimPrefix(p.matcher(), "namespace=~"))
		if err != nil {
			t.Fatal(err)
		}
		return regexp.MustCompile(pattern).MatchString(value)
	}
	check := func(parent *namespacePartition) []namespacePartition {
		parts, err := namespaceParts(parent)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range values {
			count := 0
			for _, part := range parts {
				if matches(part, value) {
					count++
				}
			}
			want := 0
			if parent == nil || matches(*parent, value) {
				want = 1
			}
			if count != want {
				t.Fatalf("parent=%+v value=%q matches=%d want=%d parts=%+v", parent, value, count, want, parts)
			}
		}
		return parts
	}
	first := check(nil)
	for _, bin := range first[:5] {
		check(&bin)
	}
	p := namespacePartition{bucket: "a", depth: 1}
	for depth := 2; depth <= maxNamespaceDepth; depth++ {
		bins := check(&p)
		literals := check(&bins[1])
		p = literals[0]
		if p.depth != depth {
			t.Fatalf("depth did not advance: %+v", p)
		}
	}
	if _, err := namespaceParts(&p); err == nil {
		t.Fatal("maximum suffix depth not enforced")
	}
	if _, err := namespaceParts(&first[5]); err == nil {
		t.Fatal("empty/absent leaf can never be split by namespace")
	}
}

func TestNamespaceVersionResume(t *testing.T) {
	s, path := newScanTestStore(t)
	ctx := context.Background()
	parent := partitionTestParent(t, s)
	if _, err := s.RepairPartition(ctx, parent); err != nil {
		t.Fatal(err)
	}
	children := partitionTestChildren(t, s, parent)
	partitionTestAccept(t, s, children[0], 7)
	// Simulate a persisted v1 gate failure without altering successful evidence.
	if _, err := s.db.Exec(`UPDATE query SET state='blocked',classification='partition_filter_ineffective' WHERE id=?`, children[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE query SET classification='partition_exhausted' WHERE id=?`, parent); err != nil {
		t.Fatal(err)
	}
	if planned, err := s.planHardLimitPartitions(ctx); err != nil || !planned {
		t.Fatalf("old gate not reopened: %v %v", planned, err)
	}
	if len(partitionTestChildren(t, s, children[1])) != 6 {
		t.Fatal("old leaf did not gain radix children")
	}
	var count int
	if err := s.db.QueryRow(`SELECT count(*) FROM accepted_observation WHERE query_id=? AND integer_value=7`, children[0]).Scan(&count); err != nil || count != 1 {
		t.Fatalf("old success lost: %d %v", count, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := OpenScanStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if planned, err := s.planHardLimitPartitions(ctx); err != nil || planned {
		t.Fatalf("resume duplicated plans: %v %v", planned, err)
	}
	if _, err := s.db.Exec(`UPDATE query SET classification='partition_exhausted' WHERE id=?`, parent); err != nil {
		t.Fatal(err)
	}
	if _, err := s.planHardLimitPartitions(ctx); err != nil {
		t.Fatal(err)
	}
	var class string
	if err := s.db.QueryRow(`SELECT classification FROM query WHERE id=?`, parent).Scan(&class); err != nil || class != "partition_exhausted" {
		t.Fatalf("version transition repeated: %s %v", class, err)
	}
}

func TestNamespaceRecursiveQuota(t *testing.T) {
	s, _ := newScanTestStore(t)
	ctx := context.Background()
	root := partitionTestParent(t, s)
	if _, err := s.RepairPartition(ctx, root); err != nil {
		t.Fatal(err)
	}
	parent := partitionTestChildren(t, s, root)[0]
	// Walk beyond two levels. Metadata, not predicate parsing, drives each split.
	for _, bucket := range []string{"abcdef", "a", "abcdef"} {
		if _, err := s.db.Exec(`UPDATE query SET state='blocked',classification='hard_limit' WHERE id=?`, parent); err != nil {
			t.Fatal(err)
		}
		if _, err := s.RepairPartition(ctx, parent); err != nil {
			t.Fatal(err)
		}
		if err := s.db.QueryRow(`SELECT l.child_id FROM query_partition l JOIN namespace_partition n ON n.query_id=l.child_id WHERE l.parent_id=? AND n.bucket=?`, parent, bucket).Scan(&parent); err != nil {
			t.Fatal(err)
		}
	}
	var descendants int
	if err := s.db.QueryRow(`SELECT count(*) FROM query_partition`).Scan(&descendants); err != nil {
		t.Fatal(err)
	}
	// Fill the quota with descendants beneath an intermediate node. A shallow
	// root-only count would miss these and incorrectly admit another plan.
	var ancestor string
	if err := s.db.QueryRow(`SELECT parent_id FROM query_partition WHERE child_id=?`, parent).Scan(&ancestor); err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	for i := descendants; i < maxPartitionChildren; i++ {
		id := fmt.Sprintf("quota-%d", i)
		if _, err := tx.Exec(`INSERT INTO query(id,workspace_id,kind,path,params,expression,state) SELECT ?,workspace_id,kind,path,?,'quota','blocked' FROM query WHERE id=?`, id, id, parent); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`INSERT INTO query_partition(parent_id,child_id,key,parent_predicate,child_predicate) VALUES(?,?,'namespace','','')`, ancestor, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE query SET state='blocked',classification='hard_limit' WHERE id=?`, parent); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RepairPartition(ctx, parent); !errors.Is(err, errPartitionExhausted) {
		t.Fatalf("deep descendants bypassed quota: %v", err)
	}
	if _, err := s.db.Exec(`UPDATE query_partition SET active=0 WHERE child_id LIKE 'quota-%'`); err != nil {
		t.Fatal(err)
	}
	if n, err := s.RepairPartition(ctx, parent); err != nil || n != 6 {
		t.Fatalf("inactive historical edges consumed active quota: %d %v", n, err)
	}
}

func TestNamespaceRecursiveScanClosure(t *testing.T) {
	s, _ := newScanTestStore(t)
	ctx := context.Background()
	root := partitionTestParent(t, s)
	if _, err := s.db.Exec(`UPDATE query SET state='blocked',classification='test_unused' WHERE state='pending'`); err != nil {
		t.Fatal(err)
	}
	var requests int
	transport := collectionTestTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		expression := r.URL.Query().Get("query")
		// Every cluster child caps, then one broad bin and one literal cap.
		// A second suffix rune is needed. Other branches succeed empty.
		depth := strings.Count(expression, "namespace=")
		if depth == 0 || (depth == 1 && strings.Contains(expression, "[qrstuvwxyz]")) || (depth == 2 && strings.Contains(expression, ".*[t]")) {
			return collectionTestResponse(`{"error":"Query has exceeded the timeseries per metric limit"}`, 429), nil
		}
		body := `{"status":"success","data":{"resultType":"vector","result":[]}}`
		if depth == 3 && strings.Contains(expression, ".*[abcdef][t]") {
			at, err := time.Parse(time.RFC3339, r.URL.Query().Get("time"))
			if err != nil {
				t.Fatal(err)
			}
			body = fmt.Sprintf(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{"job":"same"},"value":[%d,"7"]}]}}`, at.Unix())
		}
		return collectionTestResponse(body, 200), nil
	})
	if err := s.Scan(ctx, ScanOptions{Workers: 1, Credential: scanTestCredential{}, Transport: transport}); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := s.db.QueryRow(`SELECT integer_value FROM accepted_observation WHERE query_id=? AND ranking=1`, root).Scan(&count); err != nil || count != 14 {
		t.Fatalf("recursive root closure: %d %v requests=%d", count, err, requests)
	}
	if requests != 46 {
		t.Fatalf("unexpected requests, possible redundant retries: %d", requests)
	}
	if err := s.Scan(ctx, ScanOptions{Workers: 1, Credential: scanTestCredential{}, Transport: transport}); err != nil || requests != 46 {
		t.Fatalf("completed tree reexecuted on resume: %d %v", requests, err)
	}
}

func TestInstancePartitionsExhaustive(t *testing.T) {
	values := []string{"", ":443", "443", "host", "host:443", "10.0.0.0:443", "10.0.0.9:443", "[::1]:443", "::1:443", "a::443", "host:port", "host:443\n", "\n:443", "\x00:0", "\u4e2d:1", "\u212a:99", "a:1:23", ":0", "a:\u0661"}
	for _, c := range "0123456789abcdef-" {
		values = append(values, "a"+string(c)+":443", string(c)+":443")
	}
	matches := func(p namespacePartition, value string) bool {
		matcher := instanceMatcher(p)
		negative := strings.HasPrefix(matcher, "instance!~")
		pattern, err := strconv.Unquote(strings.TrimPrefix(strings.TrimPrefix(matcher, "instance!~"), "instance=~"))
		if err != nil {
			t.Fatal(err)
		}
		return regexp.MustCompile(pattern).MatchString(value) != negative
	}
	check := func(parent *namespacePartition, parts []namespacePartition) {
		for _, value := range values {
			want, count := 0, 0
			if parent == nil || matches(*parent, value) {
				want = 1
			}
			for _, part := range parts {
				if matches(part, value) {
					count++
				}
			}
			if count != want {
				t.Fatalf("instance %q parent=%+v matched %d want %d", value, parent, count, want)
			}
		}
	}
	initial := instanceParts()
	check(nil, initial)
	for _, parent := range initial[:3] {
		children, err := namespaceParts(&parent)
		if err != nil {
			t.Fatal(err)
		}
		check(&parent, children)
	}
	p := namespacePartition{bucket: "0", depth: 1}
	children, err := namespaceParts(&p)
	if err != nil {
		t.Fatal(err)
	}
	check(&p, children)
}

func TestInstanceFallbackVersionAndClosure(t *testing.T) {
	s, path := newScanTestStore(t)
	ctx := context.Background()
	root := partitionTestParent(t, s)
	if _, err := s.RepairPartition(ctx, root); err != nil {
		t.Fatal(err)
	}
	clusters := partitionTestChildren(t, s, root)
	partitionTestAccept(t, s, clusters[0], 5)
	if _, err := s.db.Exec(`UPDATE query SET state='blocked',classification='hard_limit' WHERE id=?`, clusters[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RepairPartition(ctx, clusters[1]); err != nil {
		t.Fatal(err)
	}
	var empty string
	if err := s.db.QueryRow(`SELECT l.child_id FROM query_partition l JOIN namespace_partition n ON n.query_id=l.child_id WHERE l.parent_id=? AND n.terminal=1`, clusters[1]).Scan(&empty); err != nil {
		t.Fatal(err)
	}
	for _, child := range partitionTestChildren(t, s, clusters[1]) {
		if child != empty {
			partitionTestAccept(t, s, child, -1)
		}
	}
	// Persist exactly the old v2 exhausted tree and marker before reopening.
	if _, err := s.db.Exec(`INSERT INTO partition_version(version) VALUES(2);
 UPDATE query SET state='blocked',classification='partition_exhausted' WHERE state<>'succeeded';`); err != nil {
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
	// Avoid unrelated ranking roots in this focused closure fixture.
	if _, err := s.db.Exec(`UPDATE query SET classification='test_unused' WHERE ranking=1 AND id<>? AND state='blocked'`, root); err != nil {
		t.Fatal(err)
	}
	if planned, err := s.planHardLimitPartitions(ctx); err != nil || !planned {
		t.Fatalf("v2 exhausted tree was not reopened: %v %v", planned, err)
	}
	if len(partitionTestChildren(t, s, empty)) != 2 {
		t.Fatal("empty namespace did not get metric-specific job plus complement")
	}
	if planned, err := s.planHardLimitPartitions(ctx); err != nil || planned {
		t.Fatalf("repeated planner duplicated fallback: %v %v", planned, err)
	}
	requests := map[string]int{}
	transport := collectionTestTransport(func(r *http.Request) (*http.Response, error) {
		expression := r.URL.Query().Get("query")
		requests[expression]++
		if !strings.Contains(expression, `namespace=""`) {
			t.Fatalf("fallback lost parent predicate: %s", expression)
		}
		// Job branches cap, then all twelve direct instance branches succeed.
		if !strings.Contains(expression, "instance") {
			return collectionTestResponse(`{"error":"Query has exceeded the timeseries per metric limit"}`, 429), nil
		}
		at, err := time.Parse(time.RFC3339, r.URL.Query().Get("time"))
		if err != nil {
			t.Fatal(err)
		}
		return collectionTestResponse(fmt.Sprintf(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{"job":"same"},"value":[%d,"2"]}]}}`, at.Unix()), 200), nil
	})
	if err := s.Scan(ctx, ScanOptions{Workers: 1, Credential: scanTestCredential{}, Transport: transport}); err != nil {
		t.Fatal(err)
	}
	var count int64
	// This metric has job=same (job=test belongs to a different metric).
	// Exact-job and residual each have twelve instance branches, plus saved 5.
	if err := s.db.QueryRow(`SELECT integer_value FROM accepted_observation WHERE query_id=? AND ranking=1`, root).Scan(&count); err != nil || count != 53 {
		t.Fatalf("original root closure after fallback: %d %v", count, err)
	}
	for expression, n := range requests {
		if n != 1 {
			t.Fatalf("repeated request %s: %d", expression, n)
		}
	}
	before := len(requests)
	if err := s.Scan(ctx, ScanOptions{Workers: 1, Credential: scanTestCredential{}, Transport: transport}); err != nil || len(requests) != before {
		t.Fatalf("completed fallback executed again: %v", err)
	}
	var version int
	if err := s.db.QueryRow(`SELECT count(*) FROM partition_version WHERE version=4`).Scan(&version); err != nil || version != 1 {
		t.Fatalf("missing durable v4 marker: %d %v", version, err)
	}
}

func TestTerminalSupersessionPreservesHistory(t *testing.T) {
	s, _ := newScanTestStore(t)
	ctx := context.Background()
	root := partitionTestParent(t, s)
	if _, err := s.RepairPartition(ctx, root); err != nil {
		t.Fatal(err)
	}
	cluster := partitionTestChildren(t, s, root)[0]
	for _, sibling := range partitionTestChildren(t, s, root)[1:] {
		partitionTestAccept(t, s, sibling, -1)
	}
	// Construct a real suffix path, including its legacy regex-only boundary.
	parent := cluster
	for _, bucket := range []string{"abcdef", "a", ""} {
		if _, err := s.db.Exec(`UPDATE query SET state='blocked',classification='hard_limit' WHERE id=?`, parent); err != nil {
			t.Fatal(err)
		}
		if _, err := s.RepairPartition(ctx, parent); err != nil {
			t.Fatal(err)
		}
		var next string
		if err := s.db.QueryRow(`SELECT l.child_id FROM query_partition l JOIN namespace_partition n ON n.query_id=l.child_id WHERE l.parent_id=? AND n.bucket=? AND l.active=1`, parent, bucket).Scan(&next); err != nil {
			t.Fatal(err)
		}
		for _, child := range partitionTestChildren(t, s, parent) {
			if child != next {
				partitionTestAccept(t, s, child, -1)
			}
		}
		parent = next
	}
	var expression, params, predicate string
	if err := s.db.QueryRow(`SELECT q.expression,q.params,l.child_predicate FROM query q JOIN query_partition l ON l.child_id=q.id WHERE q.id=?`, parent).Scan(&expression, &params, &predicate); err != nil {
		t.Fatal(err)
	}
	legacyMatcher := `namespace=~"^(?is:[a])$"`
	expression = strings.Replace(expression, `namespace="a"`, legacyMatcher, 1)
	predicate = strings.Replace(predicate, `namespace="a"`, legacyMatcher, 1)
	v, _ := url.ParseQuery(params)
	v.Set("query", expression)
	if _, err := s.db.Exec(`UPDATE query SET expression=?,params=?,state='blocked',classification='hard_limit' WHERE id=?`, expression, v.Encode(), parent); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE query_partition SET child_predicate=? WHERE child_id=?`, predicate, parent); err != nil {
		t.Fatal(err)
	}
	// Build the old expensive fallback tree while retaining the real boundary.
	if _, err := s.db.Exec(`UPDATE namespace_partition SET suffix='[^0-9a-z]' WHERE query_id=?`, parent); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RepairPartition(ctx, parent); err != nil {
		t.Fatal(err)
	}
	old := partitionTestChildren(t, s, parent)
	if len(old) != 12 {
		t.Fatalf("old instance tree fixture: %d", len(old))
	}
	partitionTestAccept(t, s, old[0], 999)
	if _, err := s.db.Exec(`UPDATE namespace_partition SET suffix='[a]' WHERE query_id=?`, parent); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO partition_version(version) VALUES(3);
 UPDATE query SET classification='partition_exhausted' WHERE classification='partition_pending';
 ALTER TABLE query_partition DROP COLUMN active`); err != nil {
		t.Fatal(err)
	}
	if planned, err := s.planHardLimitPartitions(ctx); err != nil || !planned {
		t.Fatalf("v4 supersession: %v %v", planned, err)
	}
	children := partitionTestChildren(t, s, parent)
	if len(children) != 1 {
		t.Fatalf("expected one literal replacement, got %d", len(children))
	}
	var replacement string
	if err := s.db.QueryRow(`SELECT expression FROM query WHERE id=?`, children[0]).Scan(&replacement); err != nil || !strings.Contains(replacement, `namespace="a"`) || !strings.Contains(replacement, legacyMatcher) {
		t.Fatalf("literal replacement lost original predicate: %s %v", replacement, err)
	}
	var inactive, canceled, saved int
	if err := s.db.QueryRow(`SELECT count(*) FROM query_partition WHERE parent_id=? AND active=0`, parent).Scan(&inactive); err != nil || inactive != 12 {
		t.Fatalf("historical edges lost: %d %v", inactive, err)
	}
	if err := s.db.QueryRow(`SELECT count(*) FROM query WHERE classification='superseded'`).Scan(&canceled); err != nil || canceled != 11 {
		t.Fatalf("unexecuted old requests not canceled: %d %v", canceled, err)
	}
	if err := s.db.QueryRow(`SELECT count(*) FROM accepted_observation WHERE query_id=? AND integer_value=999`, old[0]).Scan(&saved); err != nil || saved != 1 {
		t.Fatalf("historical observation lost: %d %v", saved, err)
	}
	partitionTestAccept(t, s, children[0], 7)
	partitionTestFinalize(t, s)
	var total int
	if err := s.db.QueryRow(`SELECT integer_value FROM accepted_observation WHERE query_id=? AND ranking=1`, root).Scan(&total); err != nil || total != 7 {
		t.Fatalf("inactive plan double counted or blocked closure: %d %v", total, err)
	}
	if planned, err := s.planHardLimitPartitions(ctx); err != nil || planned {
		t.Fatalf("supersession repeated: %v %v", planned, err)
	}
}

func TestMetricJobsBatchedResidual(t *testing.T) {
	s, _ := newScanTestStore(t)
	ctx := context.Background()
	root := partitionTestParent(t, s)
	if _, err := s.RepairPartition(ctx, root); err != nil {
		t.Fatal(err)
	}
	cluster := partitionTestChildren(t, s, root)[0]
	sibling := partitionTestChildren(t, s, root)[1]
	partitionTestAccept(t, s, sibling, -1)
	// Ten jobs for this metric; the unrelated seed's job must not be selected.
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		lid, err := internLabels(ctx, tx, map[string]string{"job": fmt.Sprintf("job-%02d", i)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`INSERT INTO observation(attempt_id,labelset_id,timestamp,value,integer_value) SELECT accepted_attempt_id,?,evaluation_time,1,1 FROM query WHERE id=?`, lid, sibling); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE query SET state='blocked',classification='hard_limit' WHERE id=?`, cluster); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RepairPartition(ctx, cluster); err != nil {
		t.Fatal(err)
	}
	var empty string
	if err := s.db.QueryRow(`SELECT l.child_id FROM query_partition l JOIN namespace_partition n ON n.query_id=l.child_id WHERE l.parent_id=? AND n.terminal=1`, cluster).Scan(&empty); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE query SET state='blocked',classification='hard_limit' WHERE id=?`, empty); err != nil {
		t.Fatal(err)
	}
	if n, err := s.RepairPartition(ctx, empty); err != nil || n != 9 {
		t.Fatalf("first job batch: %d %v", n, err)
	}
	var residual string
	if err := s.db.QueryRow(`SELECT l.child_id FROM query_partition l JOIN job_partition j ON j.query_id=l.child_id WHERE l.parent_id=? AND j.residual=1`, empty).Scan(&residual); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE query SET state='blocked',classification='hard_limit' WHERE id=?`, residual); err != nil {
		t.Fatal(err)
	}
	if n, err := s.RepairPartition(ctx, residual); err != nil || n != 3 {
		t.Fatalf("remaining jobs were not isolated: %d %v", n, err)
	}
	for _, id := range partitionTestChildren(t, s, residual) {
		var expression string
		if err := s.db.QueryRow(`SELECT expression FROM query WHERE id=?`, id).Scan(&expression); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(expression, `job="test"`) || strings.Contains(expression, `job="job-00"`) {
			t.Fatalf("unrelated or excluded job repeated: %s", expression)
		}
	}
}

func TestPartitionUpgradeRefusesRunningLease(t *testing.T) {
	s, _ := newScanTestStore(t)
	claim, _, err := s.claimScan(context.Background(), "older-scanner", time.Now())
	if err != nil || claim == nil {
		t.Fatalf("claim fixture: %v %v", claim, err)
	}
	if _, err := s.planHardLimitPartitions(context.Background()); err == nil || !strings.Contains(err.Error(), "stopped scanners") {
		t.Fatalf("upgraded beneath running scanner: %v", err)
	}
	var running int
	if err := s.db.QueryRow(`SELECT count(*) FROM query WHERE id=? AND state='running'`, claim.id).Scan(&running); err != nil || running != 1 {
		t.Fatalf("refused migration modified lease: %d %v", running, err)
	}
}
