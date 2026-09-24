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
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBudgetBands(t *testing.T) {
	baseline := func(v float64) sql.NullFloat64 { return sql.NullFloat64{Float64: v, Valid: true} }
	bands := budgetBands(40, 90, baseline(0), baseline(20), 10, 100)
	if len(bands) != 2 || bands[0].Name != "Previous baseline" || bands[0].Start != 0 || bands[0].End != 10.0/90 || bands[1].Start != 30.0/90 || bands[1].End != 80.0/90 {
		t.Fatalf("incorrect clipped timeline bands: %+v", bands)
	}
	if got := budgetBands(40, 90, baseline(20), baseline(50), 10, 100); len(got) != 1 || got[0].Name != "Test run" {
		t.Fatalf("overlapping baseline should not be plotted: %+v", got)
	}
	if got := budgetBands(40, 90, sql.NullFloat64{}, sql.NullFloat64{}, 10, 100); len(got) != 1 {
		t.Fatalf("missing baseline must not be fabricated: %+v", got)
	}
}

func TestBudgetCompactExplorerContract(t *testing.T) {
	s, path := newScanTestStore(t)
	databaseTestAccept(t, s, "up", "end12h", `[{"metric":{"namespace":"a","prometheus":"one"},"value":[100600,"5"]},{"metric":{"namespace":"b","prometheus":"two"},"value":[100600,"5"]}]`)
	b := databaseTestReport(t, s).Budget
	var decoded databaseBudget
	if err := json.Unmarshal([]byte(b.Data), &decoded); err != nil {
		t.Fatal(err)
	}
	indexes := map[int]bool{}
	for _, f := range decoded.Workspaces[0].Facts {
		if f[1] >= 0 && (decoded.Strings[f[1]] == "a" || decoded.Strings[f[1]] == "b") {
			indexes[f[6]] = true
		}
	}
	if len(indexes) != 1 {
		t.Fatal("identical count tuples were not interned")
	}
	var output bytes.Buffer
	if err := RenderDatabase(&output, path); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"Start here", "Advanced evidence", "Exact query provenance", "Query role", "SeriesLeads", "Other (exact remainder)", "Show more"} {
		if strings.Contains(output.String(), forbidden) {
			t.Errorf("removed report content returned: %s", forbidden)
		}
	}
	if !strings.Contains(output.String(), "Enable JavaScript to filter metrics") || strings.Count(output.String(), "<tbody>") != 1 {
		t.Fatal("fallback should contain only workspace summary rows")
	}
}

func TestBudgetFactsNoCapsAndExactSources(t *testing.T) {
	s, _ := newScanTestStore(t)
	var groups []string
	for i := 0; i < 240; i++ {
		groups = append(groups, fmt.Sprintf(`{"metric":{"namespace":"ns-%03d","cluster":"c"},"value":[100600,"1000"]}`, i))
	}
	for i := 0; i < 72; i++ {
		groups = append(groups, fmt.Sprintf(`{"metric":{"namespace":"velero","cluster":"c-%03d","job":"j","hostedcontrolplane":"hcp"},"value":[100600,"%d"]}`, i, i+1))
	}
	databaseTestAccept(t, s, "up", "end12h", "["+strings.Join(groups, ",")+"]")
	r := databaseTestReport(t, s)
	w := r.Budget.Workspaces[0]
	var total float64
	var velero float64
	var sources int
	for _, f := range w.Facts {
		v := r.Budget.Counts[f[6]]
		total += v[1]
		if f[1] >= 0 && r.Budget.Strings[f[1]] == "velero" {
			velero += v[1]
			sources++
			if r.Budget.Strings[f[4]] != "hcp" {
				t.Fatal("lost HCP identity")
			}
		}
	}
	if total != 240000+72*73/2 || total != r.Workspaces[0].End {
		t.Fatalf("namespace sum before cap: %v", total)
	}
	if velero != 72*73/2 || sources != 72 {
		t.Fatal("cross-cluster namespace lost before ranking")
	}
}

func TestBudgetInvalidWholePeriodAndMatchedGrowth(t *testing.T) {
	s, _ := newScanTestStore(t)
	databaseTestAccept(t, s, "up", "before12h", `[{"metric":{"namespace":"ns"},"value":[100000,"8"]}]`)
	databaseTestAccept(t, s, "up", "end12h", `[{"metric":{"namespace":"ns"},"value":[100600,"10"]}]`)
	databaseTestAccept(t, s, "other", "end12h", `[{"metric":{"namespace":"ns"},"value":[100600,"50"]}]`)
	r := databaseTestReport(t, s)
	var end float64
	for _, f := range r.Budget.Workspaces[0].Facts {
		end += r.Budget.Counts[f[6]][1]
	}
	if end != 60 {
		t.Fatal("wrong source totals")
	}
	for _, m := range r.Budget.Metrics {
		if r.Budget.Strings[m.Name] == "other" && m.Before != nil {
			t.Fatal("missing before zero-filled")
		}
	}
	databaseTestAccept(t, s, "other", "before12h", `[]`)
	r = databaseTestReport(t, s)
	for _, m := range r.Budget.Metrics {
		if r.Budget.Strings[m.Name] == "other" && (m.Before == nil || *m.Before != 0) {
			t.Fatal("accepted empty parent not zero")
		}
	}
	for _, invalid := range []string{"NaN", "+Inf", "-1"} {
		databaseTestAccept(t, s, "other", "end12h", fmt.Sprintf(`[{"metric":{"namespace":"ns"},"value":[100600,"99999"]},{"metric":{"namespace":"bad"},"value":[100600,%q]}]`, invalid))
		r := databaseTestReport(t, s)
		var end float64
		for _, f := range r.Budget.Workspaces[0].Facts {
			end += r.Budget.Counts[f[6]][1]
			if f[1] >= 0 && r.Budget.Strings[f[1]] == "bad" {
				t.Fatal("invalid query leaked source")
			}
		}
		if end != 10 {
			t.Fatal("invalid whole period contributed")
		}
	}
}

func TestBudgetActiveLimitPairing(t *testing.T) {
	for _, mode := range []string{"valid", "stale", "off grain", "physical switch", "duplicate", "zero", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			s, _ := newScanTestStore(t)
			_, err := s.db.Exec(`INSERT INTO evidence(id,workspace_id,kind,url,ok,started_at,finished_at,duration_ms) VALUES('active',1,'platform','cached',1,'','',0),('duplicate',1,'platform','cached',1,'','',0);
 INSERT INTO platform_observation(evidence_id,metric,labelset_id,timestamp,value) SELECT 'active','ActiveTimeSeries',min(id),100560,200 FROM labelset;
 INSERT INTO platform_observation(evidence_id,metric,labelset_id,timestamp,value) SELECT 'active','ActiveTimeSeriesLimit',min(id),100560,1000 FROM labelset;
 INSERT INTO platform_observation(evidence_id,metric,labelset_id,timestamp,value) SELECT 'active','ActiveTimeSeriesLimit',min(id),100620,9000 FROM labelset;`)
			if err != nil {
				t.Fatal(err)
			}
			mutation := map[string]string{
				"stale":           `UPDATE platform_observation SET timestamp=100500 WHERE metric='ActiveTimeSeriesLimit' AND timestamp=100560`,
				"off grain":       `UPDATE platform_observation SET timestamp=100561 WHERE metric='ActiveTimeSeriesLimit' AND timestamp=100560`,
				"physical switch": `INSERT INTO labelset(hash,canonical) VALUES('extra',''); UPDATE platform_observation SET labelset_id=(SELECT max(id) FROM labelset) WHERE timestamp=100620`,
				"duplicate":       `INSERT INTO platform_observation SELECT 'duplicate',metric,labelset_id,timestamp,value,invalid_value FROM platform_observation WHERE metric='ActiveTimeSeriesLimit' AND timestamp=100560`,
				"zero":            `UPDATE platform_observation SET value=0 WHERE metric='ActiveTimeSeriesLimit' AND timestamp=100560`,
				"invalid":         `UPDATE platform_observation SET invalid_value='NaN' WHERE metric='ActiveTimeSeriesLimit' AND timestamp=100560`,
			}[mode]
			if mutation != "" {
				if _, err := s.db.Exec(mutation); err != nil {
					t.Fatal(err)
				}
			}
			w := databaseTestReport(t, s).Workspaces[0]
			if mode == "valid" {
				if w.ActiveLimitEnd == nil || *w.ActiveLimitEnd != 1000 || *w.ActiveUtilization != 20 {
					t.Fatal("quota not paired to end minute")
				}
			} else if w.ActiveLimitEnd != nil || w.ActiveUtilization != nil {
				t.Fatalf("accepted %s quota", mode)
			}
		})
	}
}

func TestBudgetRateShortWindowAndLabelIdentity(t *testing.T) {
	s, _ := newScanTestStore(t)
	if _, err := s.db.Exec(`UPDATE run SET end=start+0.25`); err != nil {
		t.Fatal(err)
	}
	b := databaseTestReport(t, s).Budget
	var rate float64
	for _, f := range b.Workspaces[0].Facts {
		rate += b.Counts[f[6]][2] / b.Minutes
	}
	if rate != 27*60/0.25 {
		t.Fatalf("short duration rounded: %v", rate)
	}
	databaseTestAccept(t, s, "other", "end12h", `[{"metric":{},"value":[100600,"1"]},{"metric":{"namespace":"","prometheus":""},"value":[100600,"2"]},{"metric":{"namespace":"(absent)","prometheus":"source"},"value":[100600,"3"]}]`)
	b = databaseTestReport(t, s).Budget
	seen := map[int]float64{}
	for _, f := range b.Workspaces[0].Facts {
		if b.Strings[b.Metrics[f[0]].Name] == "other" {
			seen[f[1]] = b.Counts[f[6]][1]
			if b.Counts[f[6]][1] == 3 && b.Strings[f[5]] != "source" {
				t.Fatal("Prometheus source lost")
			}
		}
	}
	if len(seen) != 3 || seen[-1] != 1 {
		t.Fatal("absent, empty and literal sentinel merged")
	}
}

// Every source rollup must reconcile to the same complete/partial measurement
// used by workspace totals, including measured zero without any source rows.
func assertBudgetFactTotals(t *testing.T, b *databaseBudget) {
	t.Helper()
	for _, w := range b.Workspaces {
		totals := map[int][3]float64{}
		for _, f := range w.Facts {
			total := totals[f[0]]
			m := b.Metrics[f[0]]
			for i, value := range b.Counts[f[6]] {
				total[i] += value
				complete := []*float64{m.Before, m.End, m.Samples}[i]
				lower := []*float64{m.LowerBefore, m.LowerEnd, m.LowerSamples}[i]
				if f[7]&(1<<i) != 0 && (complete != nil || lower == nil) {
					t.Fatalf("workspace %s metric %s: partial mask on nonpartial period %d", w.Name, b.Strings[m.Name], i)
				}
				if value != 0 && complete == nil && f[7]&(1<<i) == 0 {
					t.Fatalf("workspace %s metric %s: missing partial bit %d", w.Name, b.Strings[m.Name], i)
				}
			}
			totals[f[0]] = total
		}
		for _, index := range w.Metrics {
			m := b.Metrics[index]
			for i, complete := range []*float64{m.Before, m.End, m.Samples} {
				value := complete
				if value == nil {
					value = []*float64{m.LowerBefore, m.LowerEnd, m.LowerSamples}[i]
				}
				var want float64
				if value != nil {
					want = *value
				}
				if totals[index][i] != want {
					t.Errorf("workspace %s metric %s period %d: source sum %v, measurement %v", w.Name, b.Strings[m.Name], i, totals[index][i], want)
				}
			}
		}
	}
}

func TestBudgetPartialTerminalFacts(t *testing.T) {
	s, _ := newScanTestStore(t)
	databaseTestAccept(t, s, "up", "run", `[{"metric":{"namespace":"ns","cluster":"c","job":"j","hostedcontrolplane":"hcp","prometheus":"p"},"value":[100600,"27"]}]`)
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
		if err := tx.QueryRow(`SELECT q.id FROM query q JOIN metric m ON m.id=q.metric_id WHERE m.name=? AND q.kind=? AND ranking=1`, metric, kind).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	add := func(parent, id, result string, active bool) {
		if _, err := tx.Exec(`INSERT INTO query(id,workspace_id,metric_id,kind,ranking,path,params,expression,evaluation_time,state)
 SELECT ?,workspace_id,metric_id,kind,0,path,?,'partition',evaluation_time,'pending' FROM query WHERE id=?`, id, id, parent); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`INSERT INTO query_partition(parent_id,child_id,key,parent_predicate,child_predicate,active) VALUES(?,?,'namespace','','',?)`, parent, id, active); err != nil {
			t.Fatal(err)
		}
		if err := importScanResponse(context.Background(), tx, id, artifactRecord{OK: true, Body: `{"status":"success","data":{"resultType":"vector","result":` + result + `}}`}, false); err != nil {
			t.Fatal(err)
		}
	}
	vector := func(at int, value string) string {
		return fmt.Sprintf(`[{"metric":{"namespace":"ns","cluster":"c","job":"j","hostedcontrolplane":"hcp","prometheus":"p"},"value":[%d,%q]}]`, at, value)
	}
	before, end, samples := root("up", "before12h"), root("up", "end12h"), root("other", "run")
	add(before, "before", vector(100000, "2"), true)
	add(end, "assembled", vector(100600, "999"), true)
	add("assembled", "seven", vector(100600, "7"), true)
	add("assembled", "three", vector(100600, "3"), true)
	add(end, "superseded", vector(100600, "1000"), false)
	add("superseded", "old-descendant", vector(100600, "2000"), true)
	add(end, "absent", `[{"metric":{},"value":[100600,"4"]}]`, true)
	add(end, "empty-label", `[{"metric":{"namespace":""},"value":[100600,"5"]}]`, true)
	add(end, "zero", `[{"metric":{"namespace":"zero"},"value":[100600,"0"]}]`, true)
	add(end, "empty", `[]`, true)
	add(root("up", "run"), "complete-child", vector(100600, "9000"), true)
	add(samples, "samples", vector(100600, "13"), true)
	add(root("other", "before12h"), "empty-before", `[]`, true)
	add(root("other", "end12h"), "other-end", vector(100600, "6"), true)
	// The lower-bound reader accepts numeric counts without integer storage.
	if _, err := tx.Exec(`UPDATE observation SET integer_value=NULL WHERE attempt_id=(SELECT accepted_attempt_id FROM query WHERE id='three')`); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{"NaN", "+Inf", "-Inf", "-1"} {
		add(end, "invalid-"+invalid, `[{"metric":{"namespace":"invalid-good-row"},"value":[100600,"99999"]},{"metric":{"namespace":"invalid"},"value":[100600,"`+invalid+`"]}]`, true)
	}
	for id, mutation := range map[string]string{
		"wrong-time": `UPDATE observation SET timestamp=100601 WHERE attempt_id=(SELECT accepted_attempt_id FROM query WHERE id=?)`,
		"no-time":    `UPDATE query SET evaluation_time=NULL WHERE id=?`,
		"no-value":   `UPDATE observation SET value=NULL WHERE attempt_id=(SELECT accepted_attempt_id FROM query WHERE id=?)`,
		"overflow":   `UPDATE observation SET value=1e999 WHERE attempt_id=(SELECT accepted_attempt_id FROM query WHERE id=?)`,
		"failed":     `UPDATE query SET state='blocked' WHERE id=?`,
		"pending":    `UPDATE query SET state='pending' WHERE id=?`,
		"no-attempt": `UPDATE query SET accepted_attempt_id=NULL WHERE id=?`,
		"duplicate-time": `INSERT INTO observation SELECT attempt_id,labelset_id,100601,value,integer_value,invalid_value
 FROM observation WHERE attempt_id=(SELECT accepted_attempt_id FROM query WHERE id=?)`,
	} {
		add(end, id, vector(100600, "99999"), true)
		if _, err := tx.Exec(mutation, id); err != nil {
			t.Fatal(err)
		}
	}
	add(end, "sum-overflow", `[{"metric":{"namespace":"overflow-a"},"value":[100600,"1e308"]},{"metric":{"namespace":"overflow-b"},"value":[100600,"1e308"]}]`, true)
	// A failed attempt on an accepted leaf is not part of its accepted data.
	if _, err := tx.Exec(`INSERT INTO attempt(query_id,owner,started_at,state) VALUES('seven','failed',0,'failed');
 INSERT INTO observation SELECT (SELECT max(id) FROM attempt),labelset_id,timestamp,99999,99999,NULL
 FROM accepted_observation WHERE query_id='seven'`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	r := databaseTestReport(t, s)
	b := r.Budget
	assertBudgetFactTotals(t, b)
	m := databaseTestMetric(t, r, "up")
	if m.End.LowerBound == nil || *m.End.LowerBound != 19 {
		t.Fatalf("expected 19 from valid active leaves: %+v", m.End)
	}
	other := databaseTestMetric(t, r, "other")
	if other.Before.Value != nil || other.Before.LowerBound == nil || *other.Before.LowerBound != 0 || *other.End.LowerBound != 6 || other.Delta != nil {
		t.Fatalf("partial zero before/positive end must not imply matched growth: %+v", other)
	}
	var known, absent, empty, zero, otherFacts int
	for _, f := range b.Workspaces[0].Facts {
		values := b.Counts[f[6]]
		if b.Strings[b.Metrics[f[0]].Name] == "other" {
			otherFacts++
			if values != [3]float64{0, 6, 13} || f[7] != 6 {
				t.Fatalf("empty leaf should not invent a source; end/sample mask should merge: %v %v", f, values)
			}
			continue
		}
		if f[1] == -1 {
			absent++
			if values != [3]float64{0, 4, 0} || f[7] != 2 {
				t.Fatalf("only legitimately absent namespace should remain unknown: %v %v", f, values)
			}
			continue
		}
		switch b.Strings[f[1]] {
		case "ns":
			known++
			if values != [3]float64{2, 10, 27} || f[7] != 3 {
				t.Fatalf("complete and partial counts/mask not combined: %v %v", f, values)
			}
			for i, want := range []string{"ns", "c", "j", "hcp", "p"} {
				if f[i+1] < 0 || b.Strings[f[i+1]] != want {
					t.Fatalf("lost source identity %s: %v", want, f)
				}
			}
		case "":
			empty++
			if values != [3]float64{0, 5, 0} || f[7] != 2 {
				t.Fatalf("empty namespace identity lost: %v %v", f, values)
			}
		case "zero":
			zero++
			if values != [3]float64{} || f[7] != 2 {
				t.Fatalf("observed zero must retain partial mask: %v %v", f, values)
			}
		default:
			t.Fatalf("invalid source leaked: %v", f)
		}
	}
	if known != 1 || absent != 1 || empty != 1 || zero != 1 || otherFacts != 1 {
		t.Fatalf("unexpected source counts: %d/%d/%d/%d/%d", known, absent, empty, zero, otherFacts)
	}
	// Once published, only original parent observations contribute.
	databaseTestAccept(t, s, "up", "end12h", vector(100600, "50"))
	b = databaseTestReport(t, s).Budget
	assertBudgetFactTotals(t, b)
	for _, f := range b.Workspaces[0].Facts {
		if b.Strings[b.Metrics[f[0]].Name] == "up" && (b.Counts[f[6]] != [3]float64{2, 50, 27} || f[7] != 1) {
			t.Fatalf("complete parent retained child sources: %v", f)
		}
	}
}

func TestBudgetOlderPartitionArchives(t *testing.T) {
	for _, partitions := range []bool{false, true} {
		t.Run(fmt.Sprint(partitions), func(t *testing.T) {
			s, _ := newScanTestStore(t)
			if partitions {
				if _, err := s.db.Exec(`CREATE TABLE query_partition(parent_id TEXT,child_id TEXT,key TEXT,parent_predicate TEXT,child_predicate TEXT)`); err != nil {
					t.Fatal(err)
				}
			}
			b := databaseTestReport(t, s).Budget
			assertBudgetFactTotals(t, b)
			if len(b.Workspaces[0].Facts) != 1 || b.Workspaces[0].Facts[0][7] != 0 {
				t.Fatal("legacy complete observations lost or marked partial")
			}
		})
	}
}

func TestBudgetNamespaceRecovery(t *testing.T) {
	path := os.Getenv("AMW_BUDGET_RECOVERY")
	if path == "" {
		t.Skip("set AMW_BUDGET_RECOVERY to namespace-recovery.db for the read-only regression")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	u := url.URL{Scheme: "file", Path: abs}
	u.RawQuery = url.Values{"mode": {"ro"}, "_pragma": {"query_only(1)", "busy_timeout(10000)"}}.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := databaseTestReport(t, &ScanStore{db: db})
	assertBudgetFactTotals(t, r.Budget)
	for _, w := range r.Budget.Workspaces {
		if w.Label != "HCP" {
			continue
		}
		var complete, partial float64
		for _, f := range w.Facts {
			if f[7]&2 != 0 {
				partial += r.Budget.Counts[f[6]][1]
				if f[1] < 0 {
					t.Fatal("recovered namespace leaf was attributed to unknown namespace")
				}
			} else {
				complete += r.Budget.Counts[f[6]][1]
			}
		}
		if complete != 2629489 || partial != 4394719 || complete+partial != 7024208 {
			t.Fatalf("HCP source end counts: complete=%v partial=%v total=%v", complete, partial, complete+partial)
		}
		t.Logf("HCP source end counts: complete=%v partial=%v total=%v", complete, partial, complete+partial)
		return
	}
	t.Fatal("recovery archive has no HCP workspace")
}
