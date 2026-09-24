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
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDatabaseReportContextInterruptsQuery(t *testing.T) {
	s, _ := newScanTestStore(t)
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	reader := databaseContextReader{ctx: ctx, tx: tx}
	var total int64
	err = reader.QueryRow(`WITH RECURSIVE numbers(n) AS (VALUES(1) UNION ALL SELECT n+1 FROM numbers WHERE n<1000000000) SELECT sum(n) FROM numbers`).Scan(&total)
	if err == nil || ctx.Err() == nil {
		t.Fatalf("report query ignored deadline: %v", err)
	}
	w := databaseContextWriter{ctx: ctx, Writer: io.Discard}
	if _, err := w.Write([]byte("report")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("report writer ignored deadline: %v", err)
	}
}

func TestDatabaseReportLimitsPreserveEvidence(t *testing.T) {
	s, path := newScanTestStore(t)
	if _, err := s.db.Exec(`WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<10001)
 INSERT INTO metric(workspace_id,name,display_name) SELECT 1,'limit_'||i,'limit_'||i FROM n`); err != nil {
		t.Fatal(err)
	}
	var report bytes.Buffer
	if err := RenderDatabaseContext(t.Context(), &report, path); err == nil || !strings.Contains(err.Error(), "metric has more than 10000") {
		t.Fatalf("oversized catalog was not refused: %v", err)
	}
	if report.Len() != 0 {
		t.Fatal("oversized catalog emitted a truncated report")
	}
	var count int
	if err := s.db.QueryRow(`SELECT count(*) FROM metric`).Scan(&count); err != nil || count != 10003 {
		t.Fatalf("render limit modified evidence: %d, %v", count, err)
	}
	w := &databaseContextWriter{ctx: t.Context(), Writer: &report, limit: 4}
	if _, err := w.Write([]byte("1234")); err != nil {
		t.Fatal(err)
	}
	if n, err := w.Write([]byte("5")); err == nil || n != 0 || report.String() != "1234" {
		t.Fatalf("output byte cap failed: %d, %v, %q", n, err, report.String())
	}
}

type databaseCancelWriter struct{ cancel context.CancelFunc }

func (w databaseCancelWriter) Write(p []byte) (int, error) {
	w.cancel()
	return len(p), nil
}

func TestDatabaseReportCancellationDuringRendering(t *testing.T) {
	_, path := newScanTestStore(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := time.Now()
	err := RenderDatabaseContext(ctx, databaseCancelWriter{cancel: cancel}, path)
	if !errors.Is(err, context.Canceled) || time.Since(started) > time.Second {
		t.Fatalf("rendering did not stop promptly on cancellation: %v (%s)", err, time.Since(started))
	}
}

type databaseCancelJSON struct{ cancel context.CancelFunc }

func (v databaseCancelJSON) MarshalJSON() ([]byte, error) {
	v.cancel()
	return []byte(`{"complete":true}`), nil
}

func TestDatabaseModelCancellationDiscardsJSON(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	data, err := databaseMarshal(databaseContextReader{ctx: ctx}, databaseCancelJSON{cancel: cancel})
	if !errors.Is(err, context.Canceled) || len(data) != 0 {
		t.Fatalf("model serialization published after cancellation: %q, %v", data, err)
	}
}

func databaseTestAccept(t *testing.T, s *ScanStore, metric, kind, result string) {
	t.Helper()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	var id string
	if err := tx.QueryRow(`SELECT q.id FROM query q JOIN metric m ON m.id=q.metric_id WHERE m.name=? AND q.kind=? AND q.ranking=1`, metric, kind).Scan(&id); err != nil {
		t.Fatal(err)
	}
	err = importScanResponse(context.Background(), tx, id, artifactRecord{OK: true, Body: `{"status":"success","data":{"resultType":"vector","result":` + result + `}}`}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func databaseTestReport(t *testing.T, s *ScanStore) *databaseReport {
	t.Helper()
	tx, err := s.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	r, err := readDatabaseReport(tx)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func databaseTestMetric(t *testing.T, r *databaseReport, name string) *databaseMetric {
	t.Helper()
	for _, m := range r.Metrics {
		if m.Name == name {
			return m
		}
	}
	t.Fatalf("missing metric %q", name)
	return nil
}

func TestRenderDatabaseAcceptedParentsAndUnknownPeriods(t *testing.T) {
	s, path := newScanTestStore(t)
	if _, err := s.db.Exec(`INSERT INTO evidence(id,workspace_id,kind,url,ok,started_at,finished_at,duration_ms) VALUES('reconciliation',1,'platform','cached',1,'','',0);
 INSERT INTO platform_observation(evidence_id,metric,labelset_id,timestamp,value) SELECT 'reconciliation','ActiveTimeSeries',id,99960,100 FROM labelset LIMIT 1;
 INSERT INTO platform_observation(evidence_id,metric,labelset_id,timestamp,value) SELECT 'reconciliation','ActiveTimeSeries',id,100560,200 FROM labelset LIMIT 1`); err != nil {
		t.Fatal(err)
	}
	databaseTestAccept(t, s, "up", "before12h", `[{"metric":{"cluster":"a","job":"j"},"value":[100000,"7"]},{"metric":{"cluster":"b","job":"j"},"value":[100000,"3"]}]`)
	databaseTestAccept(t, s, "up", "end12h", `[{"metric":{"cluster":"a","job":"j"},"value":[100600,"15"]},{"metric":{"cluster":"","job":"","namespace":"","hostedcontrolplane":""},"value":[100600,"0"]}]`)
	databaseTestAccept(t, s, "other", "before12h", `[]`)
	if _, err := s.db.Exec(`UPDATE query SET state='blocked',classification='hard_limit' WHERE metric_id=(SELECT id FROM metric WHERE name='other') AND kind='end12h'`); err != nil {
		t.Fatal(err)
	}
	// A huge failed/staging attempt must never enter totals or source breakdowns.
	if _, err := s.db.Exec(`INSERT INTO attempt(query_id,owner,started_at,state) SELECT id,'staging',0,'failed' FROM query WHERE kind='run' AND metric_id=(SELECT id FROM metric WHERE name='up');
 INSERT INTO observation(attempt_id,labelset_id,timestamp,value,integer_value) SELECT max(a.id),l.id,100600,999999,999999 FROM attempt a,labelset l LIMIT 1`); err != nil {
		t.Fatal(err)
	}
	r := databaseTestReport(t, s)
	m := databaseTestMetric(t, r, "up")
	if m.AMWShare == nil || *m.AMWShare != 7.5 {
		t.Fatalf("metric share must use workspace platform, not selected metrics: %v", m.AMWShare)
	}
	if rc := r.Workspaces[0].Reconciliation; len(rc) != 2 || *rc[0].Residual != 90 || *rc[1].Residual != 185 {
		t.Fatalf("wrong snapshot reconciliation: %+v", rc)
	}
	if m.Before.Value == nil || *m.Before.Value != 10 || m.End.Value == nil || *m.End.Value != 15 || m.Delta == nil || *m.Delta != 5 || m.Samples.Value == nil || *m.Samples.Value != 27 || m.Rate == nil || *m.Rate != 2.7 {
		t.Fatalf("wrong accepted sums: %+v", m)
	}
	other := databaseTestMetric(t, r, "other")
	if other.Before.Value == nil || *other.Before.Value != 0 || other.End.Value != nil || other.Samples.Value != nil || other.Delta != nil || other.End.State != "blocked: hard_limit" {
		t.Fatalf("empty/missing/blocked conflated: %+v", other)
	}
	if r.Valid != 4 || r.Expected != 6 || r.Workspaces[0].SampleCount != 1 {
		t.Fatalf("wrong coverage: %+v", r)
	}
	var before, end, samples float64
	for _, g := range m.Sources {
		before += *g.Before
		end += *g.End
		samples += *g.Samples
	}
	if before != 10 || end != 15 || samples != 27 {
		t.Fatalf("source rollup totals %v/%v/%v", before, end, samples)
	}
	var html bytes.Buffer
	if err := RenderDatabase(&html, path); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Metrics usage", "Samples/min", "Explore", "Unknown", "Includes measured partial partitions", "Gap includes measurement differences"} {
		if !strings.Contains(html.String(), want) {
			t.Errorf("HTML missing %q", want)
		}
	}
	if strings.Contains(html.String(), "999999") {
		t.Fatal("staging observation leaked into report")
	}
	if strings.Contains(html.String(), strings.ToLower(collectionTestID)) {
		t.Fatal("compact explorer should not embed the ARM inventory")
	}
	if os.Getenv("AMW_DATABASE_BROWSER") != "" {
		report := filepath.Join(t.TempDir(), "report.html")
		if err := os.WriteFile(report, html.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("node", "sqlite_budget_browser_test.mjs", report)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("database browser regression: %v\n%s", err, output)
		} else {
			t.Log(string(output))
		}
	}
}

func TestDatabaseReconciliation(t *testing.T) {
	n := func(v float64) *float64 { return &v }
	for _, tc := range []struct {
		name                                     string
		reference                                *float64
		measures                                 []databaseMeasure
		complete, partial, residual, over, share *float64
		scale                                    bool
	}{
		{"normal", n(100), []databaseMeasure{{Value: n(30)}, {Value: n(20)}}, n(50), nil, n(50), nil, n(50), true},
		{"disjoint partial", n(100), []databaseMeasure{{Value: n(30), LowerBound: n(90)}, {LowerBound: n(20)}, {}}, n(30), n(20), n(50), nil, n(30), true},
		{"missing reference", nil, []databaseMeasure{{Value: n(30)}, {LowerBound: n(20)}}, n(30), n(20), nil, nil, nil, false},
		{"zero reference", n(0), []databaseMeasure{{Value: n(0)}, {LowerBound: n(0)}}, n(0), n(0), n(0), nil, nil, false},
		{"no query success", n(100), []databaseMeasure{{}, {}}, nil, nil, n(100), nil, nil, true},
		{"over accounted", n(100), []databaseMeasure{{Value: n(120)}, {LowerBound: n(30)}}, n(120), n(30), n(-50), n(50), n(120), true},
		{"positive against zero", n(0), []databaseMeasure{{Value: n(10)}}, n(10), nil, n(-10), n(10), nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := reconcileDatabasePeriod("Before", tc.reference, tc.measures)
			for _, pair := range [][2]*float64{{r.Complete, tc.complete}, {r.Partial, tc.partial}, {r.Residual, tc.residual}, {r.Over, tc.over}, {r.CompleteShare, tc.share}} {
				if (pair[0] == nil) != (pair[1] == nil) || (pair[0] != nil && *pair[0] != *pair[1]) {
					t.Fatalf("unexpected reconciliation: %+v", r)
				}
			}
			if r.HasScale != tc.scale {
				t.Fatal("incorrect reference scale availability")
			}
			if r.HasScale && math.Abs(r.CompleteWidth+r.PartialWidth+r.GapWidth-600) > 1e-9 {
				t.Fatal("stack does not cover reference/over-accounted scale")
			}
			if r.Over != nil && (r.GapWidth != 0 || r.ReferenceX >= 600) {
				t.Fatal("over-accounted stack falsely fits within AMW")
			}
		})
	}
}

func TestRenderDatabaseQueryRoles(t *testing.T) {
	for _, partitions := range []bool{false, true} {
		t.Run(fmt.Sprint(partitions), func(t *testing.T) {
			s, _ := newScanTestStore(t)
			if _, err := s.db.Exec(`INSERT INTO query(id,workspace_id,metric_id,kind,ranking,path,params,expression,state,accepted_attempt_id)
 SELECT 'cached-query',workspace_id,metric_id,kind,0,path,'cached','cached',state,accepted_attempt_id FROM query WHERE ranking=1 AND kind='run' AND state='succeeded'`); err != nil {
				t.Fatal(err)
			}
			if partitions {
				if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS query_partition(parent_id TEXT,child_id TEXT,key TEXT,parent_predicate TEXT,child_predicate TEXT);
 INSERT INTO query(id,workspace_id,metric_id,kind,ranking,path,params,expression,state,accepted_attempt_id)
 SELECT 'partition-child',workspace_id,metric_id,kind,0,path,'partition','partition',state,accepted_attempt_id FROM query WHERE ranking=1 AND kind='run' AND state='succeeded';
 INSERT INTO query_partition SELECT id,'partition-child','cluster','','' FROM query WHERE ranking=1 AND kind='run' AND state='succeeded'`); err != nil {
					t.Fatal(err)
				}
			}
			r := databaseTestReport(t, s)
			counts := map[string]int{}
			for _, state := range r.States {
				counts[state.Role] += state.Count
			}
			if counts["ranking"] != 6 || counts["cached"] != 1 || (partitions && counts["partition"] != 1) || (!partitions && counts["partition"] != 0) {
				t.Fatalf("wrong diagnostic roles: %v", counts)
			}
			m := databaseTestMetric(t, r, "up")
			if r.Succeeded != 1 || r.SampleCount != 1 || *m.Samples.Value != 27 || len(m.Sources) != 1 || *m.Sources[0].Samples != 27 {
				t.Fatal("cached/partition results double-counted in ranking totals")
			}
		})
	}
}

func TestRenderDatabaseAbsentAndEmptyLabels(t *testing.T) {
	s, _ := newScanTestStore(t)
	// Distinguish missing and explicit empty labels in every source dimension.
	labels := []string{`{}`, `{"cluster":""}`, `{"job":""}`, `{"namespace":""}`, `{"hostedcontrolplane":""}`, `{"cluster":"(absent)"}`}
	var values []string
	for i, label := range labels {
		values = append(values, fmt.Sprintf(`{"metric":%s,"value":[100600,"%d"]}`, label, i+1))
	}
	databaseTestAccept(t, s, "other", "end12h", "["+strings.Join(values, ",")+"]")
	r := databaseTestReport(t, s)
	m := databaseTestMetric(t, r, "other")
	if len(m.Sources) != 6 || m.End.Value == nil || *m.End.Value != 21 {
		t.Fatalf("source identities merged: %+v", m)
	}
	for _, source := range m.Sources {
		key := -1
		for i, label := range []*string{source.Cluster, source.Job, source.Namespace, source.HCP} {
			if label != nil {
				key = i
				if *label == "(absent)" {
					key = 4
				} else if *label != "" {
					t.Fatalf("unexpected label %q", *label)
				}
			}
		}
		if *source.End != float64(key+2) || source.Groups != 1 {
			t.Fatalf("wrong distinct group subtotal: key=%d source=%+v", key, source)
		}
	}
	var data struct {
		Strings []string
		Metrics []struct {
			Name    int
			Sources [][]json.RawMessage
		}
	}
	if err := json.Unmarshal([]byte(r.Data), &data); err != nil {
		t.Fatal(err)
	}
	for _, metric := range data.Metrics {
		if data.Strings[metric.Name] != "other" {
			continue
		}
		var absent, empty int
		for _, row := range metric.Sources {
			if len(row) != 11 {
				t.Fatal("source tuple contract changed")
			}
			for _, raw := range row[:4] {
				var index int
				if err := json.Unmarshal(raw, &index); err != nil {
					t.Fatal(err)
				}
				if index == -1 {
					absent++
				} else if data.Strings[index] == "" {
					empty++
				}
			}
		}
		if absent != 19 || empty != 4 {
			t.Fatalf("dictionary lost missing/empty identity: absent=%d empty=%d", absent, empty)
		}
	}
}

func TestRenderDatabaseInvalidAndMalformedPeriods(t *testing.T) {
	s, _ := newScanTestStore(t)
	databaseTestAccept(t, s, "up", "before12h", `[{"metric":{"cluster":"a"},"value":[100000,"5"]},{"metric":{"cluster":"b"},"value":[100000,"NaN"]}]`)
	databaseTestAccept(t, s, "up", "end12h", `[{"metric":{},"value":[100600,"9"]}]`)
	// A syntactically malformed response is rejected by the importer, leaving the
	// parent pending. Its parseable prefix must not be reported as a partial total.
	databaseTestAccept(t, s, "other", "end12h", `[{"metric":{},"value":[100600,"800"]}`)
	r := databaseTestReport(t, s)
	m := databaseTestMetric(t, r, "up")
	if m.Before.Value != nil || m.Before.State != "invalid accepted values" || m.Delta != nil || m.End.Value == nil || *m.End.Value != 9 {
		t.Fatalf("invalid group did not poison only its period: %+v", m)
	}
	for _, g := range m.Sources {
		if g.Before != nil || g.Delta != nil {
			t.Fatal("invalid parent leaked partial source values")
		}
	}
	if m := databaseTestMetric(t, r, "other"); m.End.Value != nil {
		t.Fatal("malformed response became numeric")
	}
}

func TestRenderDatabaseBoundedSourcesAndExactRemainder(t *testing.T) {
	s, _ := newScanTestStore(t)
	var groups []string
	for i := 0; i < 72; i++ {
		groups = append(groups, fmt.Sprintf(`{"metric":{"cluster":"cluster-%03d","job":"j","namespace":"ns"},"value":[100600,"%d"]}`, i, i+1))
	}
	databaseTestAccept(t, s, "up", "end12h", "["+strings.Join(groups, ",")+"]")
	r := databaseTestReport(t, s)
	m := databaseTestMetric(t, r, "up")
	if len(m.Sources) != 51 {
		t.Fatalf("got %d groups, want 50 plus remainder", len(m.Sources))
	}
	var sum float64
	for _, g := range m.Sources {
		sum += *g.End
		if g.Before != nil || g.Delta != nil {
			t.Fatal("missing before was zero-filled")
		}
	}
	last := m.Sources[50]
	if sum != 72*73/2 || !last.Remainder || last.Groups != 23 || *last.End != 22*23/2 {
		t.Fatalf("inexact remainder: sum=%v remainder=%+v", sum, last)
	}
}

func TestRenderDatabasePlatformBoundariesAndGaps(t *testing.T) {
	s, _ := newScanTestStore(t)
	if _, err := s.db.Exec(`INSERT INTO evidence(id,workspace_id,kind,url,ok,started_at,finished_at,duration_ms) VALUES('platform',1,'platform','cached',1,'','',0);
 INSERT INTO platform_observation(evidence_id,metric,labelset_id,timestamp,value) SELECT 'platform','ActiveTimeSeries',id,99900,999 FROM labelset LIMIT 1;
 INSERT INTO platform_observation(evidence_id,metric,labelset_id,timestamp,value) SELECT 'platform','ActiveTimeSeries',id,99960,100 FROM labelset LIMIT 1;
 INSERT INTO platform_observation(evidence_id,metric,labelset_id,timestamp,value) SELECT 'platform','ActiveTimeSeries',id,100560,130 FROM labelset LIMIT 1;
 INSERT INTO platform_observation(evidence_id,metric,labelset_id,timestamp,value) SELECT 'platform','ActiveTimeSeries',id,100620,500 FROM labelset LIMIT 1`); err != nil {
		t.Fatal(err)
	}
	r := databaseTestReport(t, s)
	if r.Before == nil || *r.Before != 100 || r.After == nil || *r.After != 130 || r.Delta == nil || *r.Delta != 30 {
		t.Fatalf("padded endpoints used instead of run boundaries: %+v", r)
	}
	if len(r.Workspaces[0].Timeline) != 5 || r.Workspaces[0].Timeline[2].Value != nil {
		t.Fatal("missing platform minutes connected across gap")
	}
	if _, err := s.db.Exec(`UPDATE platform_observation SET value=NULL,invalid_value='missing' WHERE timestamp=100560`); err != nil {
		t.Fatal(err)
	}
	r = databaseTestReport(t, s)
	if r.Before == nil || r.After != nil || r.Delta != nil {
		t.Fatal("partial platform boundary reported as complete")
	}
	if _, err := s.db.Exec(`UPDATE platform_observation SET timestamp=100500 WHERE timestamp=100560`); err != nil {
		t.Fatal(err)
	}
	r = databaseTestReport(t, s)
	if r.After != nil {
		t.Fatal("stale boundary accepted")
	}
}

type databaseErrorWriter struct{}

func (databaseErrorWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestRenderDatabaseReadOnlyEscapingAndErrors(t *testing.T) {
	s, path := newScanTestStore(t)
	attack := `</script><script>alert("x")</script>`
	if _, err := s.db.Exec(`UPDATE metric SET display_name=? WHERE name='up'`, attack); err != nil {
		t.Fatal(err)
	}
	before, err := s.Summary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := RenderDatabase(&output, path); err != nil {
		t.Fatal(err)
	}
	after, err := s.Summary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(before) != fmt.Sprint(after) {
		t.Fatal("renderer mutated the scan")
	}
	if strings.Contains(output.String(), attack) || !strings.Contains(output.String(), `\u003c/script\u003e`) {
		t.Fatal("unescaped script terminator in report")
	}
	if err := RenderDatabase(databaseErrorWriter{}, path); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("writer error not returned: %v", err)
	}
	missing := filepath.Join(t.TempDir(), "missing.db")
	if err := RenderDatabase(io.Discard, missing); err == nil {
		t.Fatal("missing DB accepted")
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("read-only renderer created missing DB")
	}
	if err := RenderDatabase(io.Discard, ""); err == nil {
		t.Fatal("empty path accepted")
	}
	if _, err := s.db.Exec(`PRAGMA user_version=77`); err != nil {
		t.Fatal(err)
	}
	if err := RenderDatabase(io.Discard, path); err == nil || !strings.Contains(err.Error(), "unsupported scan schema") {
		t.Fatalf("unknown schema accepted: %v", err)
	}
}

func TestRenderDatabaseExactPartialMinuteRate(t *testing.T) {
	s, _ := newScanTestStore(t)
	if _, err := s.db.Exec(`UPDATE run SET end=start+625`); err != nil {
		t.Fatal(err)
	}
	r := databaseTestReport(t, s)
	m := databaseTestMetric(t, r, "up")
	if m.Rate == nil || *m.Rate != 27.0*60/625 {
		t.Fatalf("rate rounded away trailing partial minute: %v", m.Rate)
	}
}

func TestRenderDatabaseSnapshotDuringScan(t *testing.T) {
	s, path := newScanTestStore(t)
	reader, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	tx, err := reader.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	initial, err := readDatabaseReport(tx)
	if err != nil {
		t.Fatal(err)
	}
	// Publishing a completed query through a writer must neither block on this
	// WAL reader nor become visible halfway through its report snapshot.
	databaseTestAccept(t, s, "up", "end12h", `[{"metric":{},"value":[100600,"99"]}]`)
	snapshot, err := readDatabaseReport(tx)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Valid != initial.Valid || databaseTestMetric(t, snapshot, "up").End.Value != nil {
		t.Fatal("concurrent publication changed existing snapshot")
	}
	fresh := databaseTestReport(t, s)
	if m := databaseTestMetric(t, fresh, "up"); m.End.Value == nil || *m.End.Value != 99 {
		t.Fatal("fresh snapshot did not see published query")
	}
}

func TestRenderDatabaseAmbiguousPlatformAndMissingWorkspace(t *testing.T) {
	s, _ := newScanTestStore(t)
	if _, err := s.db.Exec(`INSERT INTO evidence(id,workspace_id,kind,url,ok,started_at,finished_at,duration_ms)
 VALUES('platform-a',1,'platform','cached',1,'','',0),('platform-b',1,'platform','cached',1,'','',0);
 INSERT INTO platform_observation(evidence_id,metric,labelset_id,timestamp,value)
 SELECT e.id,'ActiveTimeSeries',l.id,99960,100 FROM evidence e,labelset l WHERE e.kind='platform';
 INSERT INTO platform_observation(evidence_id,metric,labelset_id,timestamp,value)
 SELECT 'platform-a','ActiveTimeSeries',id,100560,130 FROM labelset LIMIT 1`); err != nil {
		t.Fatal(err)
	}
	r := databaseTestReport(t, s)
	if r.Before != nil || r.Delta != nil || r.After == nil || *r.After != 130 {
		t.Fatal("ambiguous physical platform observations were summed or arbitrarily selected")
	}
	if _, err := s.db.Exec(`INSERT INTO workspace(run_id,arm_id,name,endpoint) VALUES(1,'another-id','missing-platform','another-endpoint')`); err != nil {
		t.Fatal(err)
	}
	r = databaseTestReport(t, s)
	if r.After != nil {
		t.Fatal("fleet total ignored a workspace with no platform evidence")
	}
}

func TestRenderDatabaseActiveTerminalLowerBounds(t *testing.T) {
	s, path := newScanTestStore(t)
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(partitionSchema); err != nil {
		t.Fatal(err)
	}
	root := func(metric, kind string) string {
		var id string
		if err := tx.QueryRow(`SELECT q.id FROM query q JOIN metric m ON m.id=q.metric_id WHERE m.name=? AND q.kind=? AND q.ranking=1`, metric, kind).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	add := func(parent, id, state, value string, active bool) {
		if _, err := tx.Exec(`INSERT INTO query(id,workspace_id,metric_id,kind,ranking,path,params,expression,evaluation_time,state)
 SELECT ?,workspace_id,metric_id,kind,0,path,?,'partition',evaluation_time,? FROM query WHERE id=?`, id, id, state, parent); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`INSERT INTO query_partition(parent_id,child_id,key,parent_predicate,child_predicate,active) VALUES(?,?,'cluster','','',?)`, parent, id, active); err != nil {
			t.Fatal(err)
		}
		if state == "succeeded" {
			var at float64
			if err := tx.QueryRow(`SELECT evaluation_time FROM query WHERE id=?`, id).Scan(&at); err != nil {
				t.Fatal(err)
			}
			result := `[]`
			if value != "" {
				result = fmt.Sprintf(`[{"metric":{"cluster":%q},"value":[%v,%q]}]`, id, at, value)
			}
			if err := importScanResponse(context.Background(), tx, id, artifactRecord{OK: true, Body: `{"status":"success","data":{"resultType":"vector","result":` + result + `}}`}, false); err != nil {
				t.Fatal(err)
			}
		}
	}
	before, end, run := root("up", "before12h"), root("up", "end12h"), root("other", "run")
	if _, err := tx.Exec(`UPDATE query SET state='blocked',classification='partition_pending' WHERE id IN (?,?,?)`, before, end, run); err != nil {
		t.Fatal(err)
	}
	add(before, "assembled", "succeeded", "999", true)
	add("assembled", "nested-seven", "succeeded", "7", true)
	add("assembled", "nested-three", "succeeded", "3", true)
	add(before, "superseded", "succeeded", "1000", false)
	add("superseded", "old-descendant", "succeeded", "2000", true)
	add(before, "empty", "succeeded", "", true)
	add(before, "invalid", "succeeded", "NaN", true)
	add(before, "blocked", "blocked", "", true)
	add(before, "pending", "pending", "", true)
	add(end, "empty-end", "succeeded", "", true)
	add(run, "samples", "succeeded", "13", true)
	// A complete root remains authoritative even if it has an old child plan.
	add(root("up", "run"), "complete-child", "succeeded", "9000", true)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	r := databaseTestReport(t, s)
	m := databaseTestMetric(t, r, "up")
	if m.Before.Value != nil || m.Before.LowerBound == nil || *m.Before.LowerBound != 10 || m.Before.Leaves != 6 || m.Before.Measured != 3 || m.Before.Invalid != 1 || m.Before.Blocked != 1 || m.Before.Pending != 1 {
		t.Fatalf("wrong active terminal frontier: %+v", m.Before)
	}
	if m.End.Value != nil || m.End.LowerBound == nil || *m.End.LowerBound != 0 || m.End.Leaves != 1 || m.End.Measured != 1 || m.Delta != nil {
		t.Fatalf("empty partial leaf became complete or created delta: %+v", m)
	}
	if *m.Samples.Value != 27 || m.Samples.LowerBound != nil || m.Samples.Leaves != 0 {
		t.Fatalf("complete parent mixed with child totals: %+v", m.Samples)
	}
	other := databaseTestMetric(t, r, "other")
	if other.Samples.Value != nil || other.Samples.LowerBound == nil || *other.Samples.LowerBound != 13 || other.LowerRate == nil || *other.LowerRate != 1.3 {
		t.Fatalf("missing sample bound: %+v", other)
	}
	if r.Succeeded != 1 || r.Valid != 1 || r.UnresolvedMetrics != 2 || r.UnresolvedWindows != 5 || r.Workspaces[0].FullyRanked != 0 || r.Workspaces[0].BeforeCount != 0 {
		t.Fatalf("lower bounds counted as complete: %+v", r)
	}
	for _, source := range m.Sources {
		if source.Before != nil || source.End != nil {
			t.Fatal("partial leaf values replayed as complete source groups")
		}
	}
	var html bytes.Buffer
	if err := RenderDatabase(&html, path); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"LowerBefore":10`, `"LowerEnd":0`, `"LowerSamples":13`, "Measured; remainder unknown", `"IncompleteSeries":2`, "Gap includes measurement differences"} {
		if !strings.Contains(html.String(), want) {
			t.Errorf("missing partial evidence %q", want)
		}
	}
	if os.Getenv("AMW_DATABASE_BROWSER") != "" {
		report := filepath.Join(t.TempDir(), "partial.html")
		if err := os.WriteFile(report, html.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("node", "sqlite_budget_browser_test.mjs", report)
		cmd.Env = append(os.Environ(), "AMW_DATABASE_PARTIAL=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("partial browser: %v\n%s", err, output)
		}
	}
	// Publication switches to the complete parent, without retaining a second bound.
	databaseTestAccept(t, s, "up", "before12h", `[{"metric":{},"value":[100000,"50"]}]`)
	r = databaseTestReport(t, s)
	m = databaseTestMetric(t, r, "up")
	if m.Before.Value == nil || *m.Before.Value != 50 || m.Before.LowerBound != nil || m.Before.Leaves != 0 || m.Delta != nil {
		t.Fatalf("publication did not replace partial evidence: %+v", m)
	}
}
