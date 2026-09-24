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
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLabelsSkipOnlyCompleteZeroAndReserveBytes(t *testing.T) {
	s, _ := newScanTestStore(t)
	ctx := context.Background()
	if _, err := s.db.Exec(`DELETE FROM observation; UPDATE query SET state='blocked' WHERE kind='run' AND metric_id=(SELECT id FROM metric WHERE name='other')`); err != nil {
		t.Fatal(err)
	}
	limits := LabelScanOptions{MaxBytes: labelResponseReservation, MaxRequests: 10}
	if err := s.PlanLabels(ctx, limits); err != nil {
		t.Fatal(err)
	}
	var skipped, planned int
	if err := s.db.QueryRow(`SELECT sum(skipped_zero),count(root_id) FROM label_scan_metric`).Scan(&skipped, &planned); err != nil || skipped != 1 || planned != 1 {
		t.Fatalf("zero/unknown selection %d %d %v", skipped, planned, err)
	}
	claim, done, err := s.claimLabelScan(ctx, "crashed", time.Now())
	if err != nil || done || claim == nil {
		t.Fatalf("first reservation %v %v %v", claim, done, err)
	}
	if _, err := s.db.Exec(`UPDATE query SET lease_until=0 WHERE id=?`, claim.id); err != nil {
		t.Fatal(err)
	}
	claim, done, err = s.claimLabelScan(ctx, "next", time.Now())
	if err != nil || !done || claim != nil {
		t.Fatalf("abandoned bytes not reserved %v %v %v", claim, done, err)
	}
	limits.MaxBytes *= 2
	if err := s.PlanLabels(ctx, limits); err != nil {
		t.Fatal(err)
	}
	claim, done, err = s.claimLabelScan(ctx, "next", time.Now())
	if err != nil || done || claim == nil {
		t.Fatalf("expanded budget did not resume %v %v %v", claim, done, err)
	}
}

func TestLabelsCompactLiteralComplement(t *testing.T) {
	values := []string{"ocm-long-prefix-a", "ocm-long-prefix-b", "ocm-long-prefix-c", "", `a.b`, "quoted\"comma,here", "\u00e9", "\u00ea", "\u00e9-one", "\u00e9-two"}
	var terms []string
	for _, v := range values {
		terms = append(terms, "namespace!="+strconv.Quote(v))
	}
	compact := compactLabelPredicate(`cluster="literal,,comma",` + strings.Join(terms, ","))
	if !strings.HasPrefix(compact, `cluster="literal,,comma",namespace!~`) {
		t.Fatalf("changed literal %s", compact)
	}
	pattern, err := strconv.Unquote(strings.TrimPrefix(compact, `cluster="literal,,comma",namespace!~`))
	if err != nil {
		t.Fatal(err)
	}
	re, err := regexp.Compile("^(?:" + pattern + ")$")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range values {
		if !re.MatchString(v) {
			t.Fatalf("omitted %q: %s", v, pattern)
		}
	}
	for _, v := range []string{"aXb", "ocm-long-prefix-aa", "different"} {
		if re.MatchString(v) {
			t.Fatalf("nonliteral match %q", v)
		}
	}
	for _, predicate := range []string{`cluster="namespace!=\"embedded\"",namespace!="one",namespace!="two"`, `cluster=~"a|b",namespace!="one"`} {
		compact := compactLabelPredicate(predicate)
		if strings.Contains(compact, "embedded|") || strings.Contains(compact, "|embedded") || !strings.HasPrefix(compact, strings.Split(predicate, ",")[0]) {
			t.Fatalf("changed quoted literal or unsupported matcher: %s", compact)
		}
	}
}

func TestLabelsBudgetResumeRawIdentityAndWindow(t *testing.T) {
	s, path := newScanTestStore(t)
	ctx := context.Background()
	if _, err := s.db.Exec(`UPDATE run SET start=1790177405,end=1790185558; UPDATE window SET start=1790177405,end=1790185558 WHERE kind='run'; UPDATE query SET state='blocked' WHERE ranking=1`); err != nil {
		t.Fatal(err)
	}
	limits := LabelScanOptions{MaxBytes: 1 << 30, MaxRequests: 1}
	if err := s.PlanLabels(ctx, limits); err != nil {
		t.Fatal(err)
	}
	var before string
	const ranking = `SELECT json_group_array(json_object('id',id,'state',state,'accepted',accepted_attempt_id,'count',attempt_count)) FROM query WHERE ranking=1`
	if err := s.db.QueryRow(ranking).Scan(&before); err != nil {
		t.Fatal(err)
	}
	calls := 0
	o := ScanOptions{Workers: 1, Credential: scanTestCredential{}, Kinds: []string{"inventory_labels"}, Transport: collectionTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "GET" || !strings.Contains(r.URL.Query().Get("query"), "[8153000ms]") || r.URL.Query().Get("time") != "2026-09-23T17:45:58Z" {
			t.Errorf("wrong full window request %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{"Job":"API","method":"GET"},"value":[1790185558,"27"]}]}}`))}, nil
	})}
	if err := s.Scan(ctx, o); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("budget allowed %d requests", calls)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := OpenScanStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	limits.MaxRequests = 10
	if err := s.PlanLabels(ctx, limits); err != nil {
		t.Fatal(err)
	}
	if err := s.Scan(ctx, o); err != nil {
		t.Fatal(err)
	}
	if err := s.Scan(ctx, o); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("resume repeated requests: %d", calls)
	}
	var after, labels string
	if err := s.db.QueryRow(ranking).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("ranking changed")
	}
	if err := s.db.QueryRow(`SELECT json_group_object(n.name,v.value) FROM labelset_member l JOIN label_name n ON n.id=l.name_id JOIN label_value v ON v.id=l.value_id WHERE l.labelset_id=(SELECT labelset_id FROM label_scan_result LIMIT 1)`).Scan(&labels); err != nil {
		t.Fatal(err)
	}
	if labels != `{"job":"api","method":"get"}` {
		t.Fatalf("incorrect normalized labels: %s", labels)
	}
	summary, err := s.LabelSummary(ctx)
	if err != nil || !summary.CoverageComplete || summary.Unverified != 2 || summary.SourceReconciliationComplete {
		t.Fatalf("summary %+v %v", summary, err)
	}
}

func TestLabelsCancellationAndInvalidPublication(t *testing.T) {
	for _, invalid := range []string{"0", "1.5", "duplicate", "warning", "case-collision", "case-duplicate"} {
		t.Run(invalid, func(t *testing.T) {
			s, _ := newScanTestStore(t)
			ctx := context.Background()
			if err := s.PlanLabels(ctx, LabelScanOptions{MaxBytes: 1 << 30, MaxRequests: 20}); err != nil {
				t.Fatal(err)
			}
			cancelCtx, cancel := context.WithCancel(ctx)
			o := ScanOptions{Workers: 1, Credential: scanTestCredential{}, Kinds: []string{"inventory_labels"}, Transport: collectionTestTransport(func(*http.Request) (*http.Response, error) { cancel(); return nil, context.Canceled })}
			if err := s.Scan(cancelCtx, o); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancel %v", err)
			}
			o.Transport = collectionTestTransport(func(*http.Request) (*http.Response, error) {
				var rows []string
				for i := 0; i < 130; i++ {
					rows = append(rows, fmt.Sprintf(`{"metric":{"instance":"%d"},"value":[100600,"1"]}`, i))
				}
				warning := ""
				if invalid == "duplicate" {
					rows = append(rows, rows[0])
				} else if invalid == "case-collision" {
					rows = append(rows, `{"metric":{"Job":"a","job":"a"},"value":[100600,"1"]}`)
				} else if invalid == "case-duplicate" {
					rows = append(rows, `{"metric":{"Instance":"0"},"value":[100600,"1"]}`)
				} else if invalid == "warning" {
					warning = `,"warnings":["partial"]`
				} else {
					rows = append(rows, fmt.Sprintf(`{"metric":{"instance":"invalid"},"value":[100600,%q]}`, invalid))
				}
				body := `{"status":"success","data":{"resultType":"vector","result":[` + strings.Join(rows, ",") + `]}` + warning + `}`
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			if err := s.Scan(ctx, o); err != nil {
				t.Fatal(err)
			}
			var n int
			if err := s.db.QueryRow(`SELECT count(*) FROM label_scan_observation`).Scan(&n); err != nil || n != 0 {
				t.Fatalf("partial stage leaked %d %v", n, err)
			}
			summary, err := s.LabelSummary(ctx)
			if err != nil || summary.CoverageComplete {
				t.Fatalf("invalid complete %+v %v", summary, err)
			}
		})
	}
}

func TestLabelsPartitionComplementsAndMismatch(t *testing.T) {
	s, _ := newScanTestStore(t)
	ctx := context.Background()
	if err := s.PlanLabels(ctx, LabelScanOptions{MaxBytes: 1 << 30, MaxRequests: 20}); err != nil {
		t.Fatal(err)
	}
	seenComplement := false
	err := s.Scan(ctx, ScanOptions{Workers: 1, Credential: scanTestCredential{}, Kinds: []string{"inventory_labels"}, Transport: collectionTestTransport(func(r *http.Request) (*http.Response, error) {
		q := r.URL.Query().Get("query")
		status := 200
		body := `{"status":"success","data":{"resultType":"vector","result":[]}}`
		if q == "count_over_time(up[600000ms])" {
			status = 422
			body = "timeseries per metric limit"
		}
		if strings.Contains(q, `cluster="abc"`) {
			body = `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"cluster":"abc","job":"test"},"value":[100600,"26"]}]}}`
		}
		if strings.Contains(q, `cluster!="abc"`) {
			seenComplement = true
		}
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	if !seenComplement {
		t.Fatal("missing exhaustive complement")
	}
	summary, err := s.LabelSummary(ctx)
	if err != nil || summary.Mismatched != 1 || summary.CoverageComplete {
		t.Fatalf("mismatch not exposed %+v %v", summary, err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM label_scan_result`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("published mismatch: %d %v", n, err)
	}
}

func labelLegacyFixture(t *testing.T) (*ScanStore, string, int64) {
	t.Helper()
	s, path := newScanTestStore(t)
	if err := s.PlanLabels(context.Background(), LabelScanOptions{MaxBytes: 1 << 30, MaxRequests: 20}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DROP VIEW label_scan_result; DROP VIEW label_scan_coverage; DROP VIEW label_scan_actual_group; DROP VIEW label_scan_leaf_observation; DROP TRIGGER label_scan_discard;
 DROP TABLE label_scan_observation; DROP TABLE label_scan_expected_group; DROP TABLE label_scan_group; DROP TABLE label_scan_http;
 ALTER TABLE label_scan_metric DROP COLUMN source_attempt_id;
 CREATE TABLE label_scan_observation(attempt_id INTEGER NOT NULL REFERENCES attempt(id),labels_json TEXT NOT NULL,timestamp REAL NOT NULL,samples INTEGER NOT NULL,PRIMARY KEY(attempt_id,labels_json,timestamp)) WITHOUT ROWID;
 INSERT INTO attempt(query_id,owner,started_at,finished_at,state) SELECT root_id,'legacy',100600,100601,'succeeded' FROM label_scan_metric WHERE metric_id=(SELECT id FROM metric WHERE name='up');
 UPDATE query SET state='succeeded',accepted_attempt_id=(SELECT max(id) FROM attempt) WHERE id=(SELECT root_id FROM label_scan_metric WHERE metric_id=(SELECT id FROM metric WHERE name='up'));`); err != nil {
		t.Fatal(err)
	}
	var aid int64
	if err := s.db.QueryRow(`SELECT max(id) FROM attempt`).Scan(&aid); err != nil {
		t.Fatal(err)
	}
	return s, path, aid
}

func TestLabelsMigrationResumeAndCanonicalCollision(t *testing.T) {
	for _, collision := range []string{"none", "names", "identity"} {
		t.Run(collision, func(t *testing.T) {
			s, path, aid := labelLegacyFixture(t)
			for i := 0; i < 300; i++ {
				labels := fmt.Sprintf(`{"Cluster":"ABC","job":"Test","Instance":"%03d"}`, i)
				if collision == "names" && i == 299 {
					labels = `{"Job":"Test","job":"test"}`
				}
				if _, err := s.db.Exec(`INSERT INTO label_scan_observation VALUES(?,?,100600,1)`, aid, labels); err != nil {
					t.Fatal(err)
				}
			}
			if collision == "identity" {
				if _, err := s.db.Exec(`INSERT INTO label_scan_observation VALUES(?, '{"cluster":"abc","job":"test","instance":"000"}',100600,1)`, aid); err != nil {
					t.Fatal(err)
				}
			}
			// Commit setup and one bounded batch, then close/reopen as after a crash.
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := s.MigrateLabels(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled migration %v", err)
			}
			err := s.MigrateLabels(context.Background())
			if collision != "none" {
				if err == nil || !strings.Contains(err.Error(), "collid") && !strings.Contains(err.Error(), "duplicate") {
					t.Fatalf("canonical collision accepted: %v", err)
				}
				var raw int
				if err := s.db.QueryRow(`SELECT count(*) FROM label_scan_raw`).Scan(&raw); err != nil || raw < 300 {
					t.Fatalf("raw evidence lost %d %v", raw, err)
				}
				if err := s.Scan(context.Background(), ScanOptions{Credential: scanTestCredential{}, Kinds: []string{"inventory_labels"}}); err == nil {
					t.Fatal("scan allowed incomplete migration")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = OpenScanStore(path)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err := s.MigrateLabels(context.Background()); err != nil {
				t.Fatal(err)
			}
			var count, total, names int
			if err := s.db.QueryRow(`SELECT count(*),sum(samples) FROM label_scan_observation`).Scan(&count, &total); err != nil || count != 300 || total != 300 {
				t.Fatalf("migration counts %d %d %v", count, total, err)
			}
			if err := s.db.QueryRow(`SELECT count(*) FROM label_name WHERE name<>lower(name)`).Scan(&names); err != nil || names != 0 {
				t.Fatalf("noncanonical names %d %v", names, err)
			}
		})
	}
}

func TestLabelsPerGroupReconciliationAndFrozenSource(t *testing.T) {
	s, _ := newScanTestStore(t)
	ctx := context.Background()
	if err := s.PlanLabels(ctx, LabelScanOptions{MaxBytes: 1 << 30, MaxRequests: 20}); err != nil {
		t.Fatal(err)
	}
	err := s.Scan(ctx, ScanOptions{Workers: 1, Credential: scanTestCredential{}, Kinds: []string{"inventory_labels"}, Transport: collectionTestTransport(func(r *http.Request) (*http.Response, error) {
		body := `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"cluster":"wrong","job":"test"},"value":[100600,"27"]}]}}`
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	var reconciled, total, expected, exhaustive int
	if err := s.db.QueryRow(`SELECT source_reconciled,samples,expected_samples,execution_complete FROM label_scan_coverage WHERE expected_samples IS NOT NULL`).Scan(&reconciled, &total, &expected, &exhaustive); err != nil || reconciled != 0 || total != expected || exhaustive != 1 {
		t.Fatalf("offsetting groups accepted %d %d %d %d %v", reconciled, total, expected, exhaustive, err)
	}
	if _, err := s.db.Exec(`UPDATE query SET accepted_attempt_id=NULL WHERE ranking=1`); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT count(*) FROM label_scan_expected_group`).Scan(&total); err != nil || total != 1 {
		t.Fatalf("source changed after freeze %d %v", total, err)
	}
}

func TestLabelsOrdinaryScanRejectedAndLongPOST(t *testing.T) {
	s, _ := newScanTestStore(t)
	ctx := context.Background()
	if err := s.PlanLabels(ctx, LabelScanOptions{MaxBytes: 1 << 30, MaxRequests: 1}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	o := ScanOptions{Workers: 1, Credential: scanTestCredential{}, Transport: collectionTestTransport(func(r *http.Request) (*http.Response, error) { calls++; return nil, errors.New("unexpected") })}
	if err := s.Scan(ctx, o); err == nil || calls != 0 {
		t.Fatal("ordinary scan bypassed label budget")
	}
	o.Kinds = []string{"inventory_labels"}
	o.Workers = 3
	if err := s.Scan(ctx, o); err == nil || calls != 0 {
		t.Fatal("label scan bypassed two-worker limit")
	}
	o.Workers = 1
	var qid string
	if err := s.db.QueryRow(`SELECT root_id FROM label_scan_metric WHERE expected_samples=27`).Scan(&qid); err != nil {
		t.Fatal(err)
	}
	params := url.Values{"query": {`count_over_time(up{instance="` + strings.Repeat("x", 6100) + `"}[600000ms])`}, "time": {"1970-01-02T03:56:40Z"}}.Encode()
	if _, err := s.db.Exec(`UPDATE query SET params=? WHERE id=?`, params, qid); err != nil {
		t.Fatal(err)
	}
	o.Kinds = []string{"inventory_labels"}
	o.Transport = collectionTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if r.Method != "POST" || r.URL.RawQuery != "" || string(body) != params || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Errorf("incorrect read-only query POST %s", r.Method)
		}
		return &http.Response{StatusCode: 414, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("too long"))}, nil
	})
	if err := s.Scan(ctx, o); err != nil {
		t.Fatal(err)
	}
	var methods int
	if err := s.db.QueryRow(`SELECT count(*) FROM label_scan_http WHERE method='POST'`).Scan(&methods); err != nil || methods != 1 {
		t.Fatalf("method provenance %d %v", methods, err)
	}
	var children int
	if err := s.db.QueryRow(`SELECT count(*) FROM label_scan_query WHERE parent_id IS NOT NULL`).Scan(&children); err != nil || children != 0 {
		t.Fatalf("414 recursively partitioned %d %v", children, err)
	}
}

func TestLabelsNormalizedStorageUsesSharedDictionary(t *testing.T) {
	s, _ := newScanTestStore(t)
	if err := s.PlanLabels(context.Background(), LabelScanOptions{MaxBytes: 1 << 30, MaxRequests: 20}); err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	labels := map[string]string{"Cluster": "ABC", "Job": "Test"}
	id, err := internLabels(context.Background(), tx, labels)
	if err != nil {
		t.Fatal(err)
	}
	var aid int64
	if err := tx.QueryRow(`SELECT min(id) FROM attempt`).Scan(&aid); err != nil {
		t.Fatal(err)
	}
	n := int64(27)
	if err := insertNormalizedLabels(context.Background(), tx, aid, []scanObservation{{labels: labels, at: 100600, integer: &n}}); err != nil {
		t.Fatal(err)
	}
	var stored int64
	if err := tx.QueryRow(`SELECT labelset_id FROM label_scan_observation`).Scan(&stored); err != nil || stored != id {
		t.Fatalf("dictionary not shared %d %d %v", stored, id, err)
	}
	var normalized string
	if err := tx.QueryRow(`SELECT json_group_object(n.name,v.value) FROM labelset_member l JOIN label_name n ON n.id=l.name_id JOIN label_value v ON v.id=l.value_id WHERE l.labelset_id=?`, id).Scan(&normalized); err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(normalized), &got); err != nil || got["job"] != "test" {
		t.Fatalf("normalized %s %v", normalized, err)
	}
}

func TestLabelsCompactionKeepsLiveDictionaryAndPrunesFailedStaging(t *testing.T) {
	s, path := newScanTestStore(t)
	ctx := context.Background()
	if err := s.PlanLabels(ctx, LabelScanOptions{MaxBytes: 1 << 30, MaxRequests: 10}); err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	var aid int64
	if err := tx.QueryRow(`SELECT min(id) FROM attempt`).Scan(&aid); err != nil {
		t.Fatal(err)
	}
	n := int64(27)
	if err := insertNormalizedLabels(ctx, tx, aid, []scanObservation{{labels: map[string]string{"newlabel": "live"}, at: 100600, integer: &n}, {labels: map[string]string{"newlabel": "orphan"}, at: 100600, integer: &n}}); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`DELETE FROM label_scan_observation WHERE labelset_id IN (SELECT l.labelset_id FROM labelset_member l JOIN label_value v ON v.id=l.value_id WHERE v.value='orphan'); UPDATE query SET state='blocked' WHERE state='pending'; UPDATE run SET state='complete'`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := PruneScanStore(ctx, path); err != nil {
		t.Fatal(err)
	}
	s, err = OpenScanStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var live, orphan int
	if err := s.db.QueryRow(`SELECT (SELECT count(*) FROM label_value WHERE value='live'),(SELECT count(*) FROM label_value WHERE value='orphan')`).Scan(&live, &orphan); err != nil || live != 1 || orphan != 0 {
		t.Fatalf("unsafe dictionary GC %d %d %v", live, orphan, err)
	}
}

func TestLabelsSQLFailuresStopScanWithoutBlockingLeaf(t *testing.T) {
	for _, table := range []string{"labelset", "label_name", "label_value", "labelset_member", "label_scan_group", "label_scan_observation", "label_scan_hint"} {
		t.Run(table, func(t *testing.T) {
			s, _ := newScanTestStore(t)
			ctx := context.Background()
			if err := s.PlanLabels(ctx, LabelScanOptions{MaxBytes: 1 << 30, MaxRequests: 10}); err != nil {
				t.Fatal(err)
			}
			// The first 128 rows commit successfully; fail inside the next batch.
			// The diagnostic deliberately resembles a uniqueness failure, but the
			// trigger's SQLite code is not an observation identity collision.
			if _, err := s.db.Exec(`CREATE TRIGGER injected_label_failure BEFORE INSERT ON ` + table + `
 WHEN (SELECT count(*) FROM label_scan_observation)>=128
 BEGIN SELECT RAISE(ABORT,'injected SQL failure: UNIQUE constraint'); END`); err != nil {
				t.Fatal(err)
			}
			var rows []string
			for i := 0; i < 130; i++ {
				rows = append(rows, fmt.Sprintf(`{"metric":{"instance":"%d"},"value":[100600,"1"]}`, i))
			}
			body := `{"status":"success","data":{"resultType":"vector","result":[` + strings.Join(rows, ",") + `]}}`
			calls := 0
			err := s.Scan(ctx, ScanOptions{Workers: 1, Credential: scanTestCredential{}, Kinds: []string{"inventory_labels"}, Transport: collectionTestTransport(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})})
			var databaseError scanDBError
			if !errors.As(err, &databaseError) || !strings.Contains(err.Error(), "injected SQL failure") || calls != 1 {
				t.Fatalf("SQL failure did not stop scan: calls=%d err=%v", calls, err)
			}
			var staged, published, pending, blocked, abandoned int
			if err := s.db.QueryRow(`SELECT
 (SELECT count(*) FROM label_scan_observation),
 (SELECT count(*) FROM label_scan_result),
 (SELECT count(*) FROM query WHERE kind='inventory_labels' AND state='pending'),
 (SELECT count(*) FROM query WHERE kind='inventory_labels' AND (state='blocked' OR classification='invalid_response')),
 (SELECT count(*) FROM attempt a JOIN label_scan_query l ON l.query_id=a.query_id WHERE a.state='abandoned')`).Scan(&staged, &published, &pending, &blocked, &abandoned); err != nil {
				t.Fatal(err)
			}
			if staged != 0 || published != 0 || pending != 2 || blocked != 0 || abandoned != 1 {
				t.Fatalf("unsafe failure state: staged=%d published=%d pending=%d blocked=%d abandoned=%d", staged, published, pending, blocked, abandoned)
			}
		})
	}
}

func TestLabelsStoredDictionaryCorruptionIsFatal(t *testing.T) {
	s, _ := newScanTestStore(t)
	ctx := context.Background()
	if err := s.PlanLabels(ctx, LabelScanOptions{MaxBytes: 1 << 30, MaxRequests: 10}); err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	labels := map[string]string{"cluster": "abc", "job": "test"}
	id, err := internLabels(ctx, tx, labels)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`DELETE FROM labelset_member WHERE labelset_id=?`, id); err != nil {
		t.Fatal(err)
	}
	n := int64(27)
	err = insertNormalizedLabels(ctx, tx, 1, []scanObservation{{labels: labels, at: 100600, integer: &n}})
	var databaseError scanDBError
	if !errors.As(err, &databaseError) || !strings.Contains(err.Error(), "incomplete membership") {
		t.Fatalf("corrupt stored dictionary was not operationally fatal: %v", err)
	}
}
