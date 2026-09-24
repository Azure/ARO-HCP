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
	"net/url"
	"os"
	"os/exec"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

func labelDetailFixture(t *testing.T, data []databaseSampleRow) (*ScanStore, string, databaseLabelCandidate) {
	t.Helper()
	s, path := newScanTestStore(t)
	var total int64
	for _, row := range data {
		total += row.Samples
	}
	if _, err := s.db.Exec(`UPDATE observation SET value=?,integer_value=?`, total, total); err != nil {
		t.Fatal(err)
	}
	if err := s.PlanLabels(context.Background(), LabelScanOptions{MaxBytes: 1 << 30, MaxRequests: 10}); err != nil {
		t.Fatal(err)
	}
	// Keep a concrete legacy checkpoint fixture independent of the collector's
	// current schema. The normalized reader fixture below converts this table.
	if _, err := s.db.Exec(`DROP VIEW IF EXISTS label_scan_result; DROP VIEW IF EXISTS label_scan_coverage; DROP VIEW IF EXISTS label_scan_actual_group;
 DROP VIEW IF EXISTS label_scan_leaf_observation; DROP TRIGGER IF EXISTS label_scan_discard;
 DROP TABLE label_scan_observation;
 CREATE TABLE label_scan_observation(attempt_id INTEGER NOT NULL,labels_json TEXT NOT NULL,timestamp REAL NOT NULL,
 samples INTEGER NOT NULL CHECK(samples>0),PRIMARY KEY(attempt_id,labels_json,timestamp)) WITHOUT ROWID;`); err != nil {
		t.Fatal(err)
	}
	var c databaseLabelCandidate
	if err := s.db.QueryRow(`SELECT m.id,m.workspace_id,m.display_name,w.name,l.root_id,l.source_query_id,l.expected_samples
 FROM label_scan_metric l JOIN metric m ON m.id=l.metric_id JOIN workspace w ON w.id=m.workspace_id WHERE m.name='up'`).Scan(&c.ID, &c.WorkspaceID, &c.Metric, &c.Workspace, &c.Root, &c.Source, &c.Expected); err != nil {
		t.Fatal(err)
	}
	labelDetailAccept(t, s, c.Root, data)
	return s, path, c
}

func labelDetailAccept(t *testing.T, s *ScanStore, id string, data []databaseSampleRow) {
	t.Helper()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT INTO attempt(query_id,owner,started_at,state,http_status) VALUES(?,'test',0,'succeeded',200)`, id)
	if err != nil {
		t.Fatal(err)
	}
	aid, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range data {
		encoded, err := json.Marshal(row.Labels)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`INSERT INTO label_scan_observation VALUES(?,?,100600,?)`, aid, string(encoded), row.Samples); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(`UPDATE query SET state='succeeded',accepted_attempt_id=? WHERE id=?`, aid, id); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func labelDetailRead(t *testing.T, s *ScanStore) *databaseSampleDetail {
	t.Helper()
	tx, err := s.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	d, err := readDatabaseLabelDetail(context.Background(), tx)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestDatabaseLabelDetailExactSummary(t *testing.T) {
	var data []databaseSampleRow
	var total int64
	for i := 0; i < 250; i++ {
		labels := map[string]string{"cluster": "abc", "job": "test", "pod": fmt.Sprint(i / 2), "prometheus_replica": fmt.Sprint(i % 2)}
		if i > 0 {
			labels["casesensitive"] = ""
			if i > 1 {
				labels["casesensitive"] = fmt.Sprint(i)
			}
		}
		weight := int64(i + 1)
		total += weight
		data = append(data, databaseSampleRow{Labels: labels, Samples: weight})
	}
	s, path, _ := labelDetailFixture(t, data)
	d := labelDetailRead(t, s)
	if len(d.Experiments) != 1 {
		t.Fatalf("expected one completed metric: %+v", d)
	}
	e := d.Experiments[0]
	if !e.Matched || e.Window != "Full normal run" || e.Start != 100000 || e.End != 100600 || e.Minutes != 10 || *e.Full.Series != 250 || *e.Full.Samples != total || *e.Full.Rate != float64(total)/10 {
		t.Fatalf("incorrect full-run accounting: %+v", e)
	}
	want := summarizeDatabaseSampleLabels(data, total, 10)
	if e.LabelCount != len(want) || e.OmittedLabels != 0 || e.SelectionReason != "High series count and sample volume" {
		t.Fatalf("incorrect selection metadata: %+v", e)
	}
	for _, got := range e.Labels {
		for _, expected := range want {
			if got.Name != expected.Name {
				continue
			}
			checkLabelDetailValues(t, got.Values, len(data), total)
			got.Values, expected.Values = nil, nil
			if !reflect.DeepEqual(got, expected) {
				t.Fatalf("compact exact projection differs from reference: %+v / %+v", got, expected)
			}
		}
	}
	if len(e.LabelPairs) != 3 {
		t.Fatalf("pair work unbounded: %d", len(e.LabelPairs))
	}
	for _, pair := range e.LabelPairs {
		found := false
		for _, want := range summarizeDatabaseSampleLabelPairs(data, want, total, 10) {
			if want.Names == pair.Names {
				found = true
				var values []databaseSampleValue
				for _, v := range pair.Values {
					values = append(values, databaseSampleValue{Series: v.Series, Samples: v.Samples, Share: v.Share})
				}
				checkLabelDetailValues(t, values, len(data), total)
				pair.Values, want.Values = nil, nil
				if !reflect.DeepEqual(pair, want) {
					t.Fatalf("pair differs from reference: %+v / %+v", pair, want)
				}
			}
		}
		if !found {
			t.Fatalf("unexpected pair: %+v", pair)
		}
	}
	var html bytes.Buffer
	if err := RenderDatabaseContext(context.Background(), &html, path); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html.String(), `"Window":"Full normal run"`) || !strings.Contains(html.String(), `"LabelCollected":true`) || !strings.Contains(html.String(), `"LabelSummaryIncluded":true`) || !strings.Contains(html.String(), `"SelectionReason":"High series count and sample volume"`) || !strings.Contains(html.String(), `"LabelCount":5`) {
		t.Fatal("full-run details/availability missing from rendered budget")
	}
	if html.Len() > DatabaseReportMaxBytes {
		t.Fatal("bounded report exceeded output limit")
	}
}

func TestDatabaseLabelDetailRejectsInvalidCoverage(t *testing.T) {
	for _, tc := range []struct {
		name, sql         string
		included, matched bool
	}{
		{"complete", "", true, true},
		{"running", `UPDATE query SET state='running' WHERE kind='inventory_labels'`, false, false},
		{"wrong window", `UPDATE query SET evaluation_time=100599 WHERE kind='inventory_labels'`, false, false},
		{"wrong duration", `UPDATE query SET lookback_seconds=43200 WHERE kind='inventory_labels'`, false, false},
		{"wrong expression", `UPDATE query SET expression='up' WHERE kind='inventory_labels'`, false, false},
		{"wrong attempt owner", `UPDATE attempt SET query_id=(SELECT id FROM query WHERE kind='run' LIMIT 1) WHERE owner='test'`, false, false},
		{"wrong status", `UPDATE attempt SET http_status=500 WHERE owner='test'`, false, false},
		{"warning", `UPDATE attempt SET warnings_json='["partial"]' WHERE owner='test'`, false, false},
		{"wrong timestamp", `UPDATE label_scan_observation SET timestamp=100599`, false, false},
		{"noninteger", `UPDATE label_scan_observation SET samples=0.5`, false, false},
		{"total mismatch", `UPDATE label_scan_metric SET expected_samples=99`, false, false},
		{"source mismatch", `UPDATE label_value SET value='different' WHERE value='test'`, true, false},
		{"unknown comparator", `UPDATE label_scan_metric SET expected_samples=NULL`, true, false},
		{"invalid comparator", `UPDATE observation SET integer_value=NULL`, true, false},
		{"comparator wrong duration", `UPDATE query SET lookback_seconds=43200 WHERE kind='run'`, true, false},
		{"synthetic partition", `UPDATE attempt SET http_status=0,classification='partitioned' WHERE id=(SELECT accepted_attempt_id FROM query WHERE kind='run' AND ranking=1 AND state='succeeded'); UPDATE query SET classification='partitioned' WHERE kind='run' AND state='succeeded'`, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _ := labelDetailFixture(t, []databaseSampleRow{{Labels: map[string]string{"cluster": "abc", "job": "test"}, Samples: 27}})
			if tc.sql != "" {
				if _, err := s.db.Exec(tc.sql); err != nil {
					t.Fatal(err)
				}
			}
			tx, err := s.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			d, err := readDatabaseLabelDetail(context.Background(), tx)
			if tc.name == "noninteger" {
				if err == nil {
					t.Fatal("noninteger count accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if (len(d.Experiments) == 1) != tc.included {
				t.Fatalf("unexpected coverage: %+v", d)
			}
			if tc.included && d.Experiments[0].Matched != tc.matched {
				t.Fatalf("false grouped reconciliation: %+v", d.Experiments[0])
			}
		})
	}
}

func TestDatabaseLabelDetailPartitionAndOverlap(t *testing.T) {
	for _, normalized := range []bool{false, true} {
		for _, overlap := range []bool{false, true} {
			t.Run(fmt.Sprintf("normalized=%t/overlap=%t", normalized, overlap), func(t *testing.T) {
				data := []databaseSampleRow{{Labels: map[string]string{"cluster": "abc", "job": "test", "pod": "a"}, Samples: 10}, {Labels: map[string]string{"cluster": "abc", "job": "test", "pod": "b"}, Samples: 17}}
				s, _, c := labelDetailFixture(t, data)
				tx, err := s.db.Begin()
				if err != nil {
					t.Fatal(err)
				}
				if _, err := tx.Exec(`UPDATE label_scan_query SET split=1 WHERE query_id=?`, c.Root); err != nil {
					t.Fatal(err)
				}
				ids := []string{}
				for _, predicate := range []string{`pod="a"`, `pod!="a"`} {
					id, err := planLabelQuery(context.Background(), tx, int64(c.ID), int64(c.WorkspaceID), c.Metric, predicate, c.Root, 1, 100000, 100600)
					if err != nil {
						t.Fatal(err)
					}
					ids = append(ids, id)
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				if overlap {
					data[1].Labels = data[0].Labels
				}
				for i, id := range ids {
					labelDetailAccept(t, s, id, data[i:i+1])
				}
				if normalized {
					normalizeLabelDetailFixture(t, s)
				}
				d := labelDetailRead(t, s)
				if overlap {
					if len(d.Experiments) != 0 {
						t.Fatal("overlapping leaves published")
					}
					return
				}
				if len(d.Experiments) != 1 || !d.Experiments[0].Matched || *d.Experiments[0].Full.Series != 2 || *d.Experiments[0].Full.Samples != 27 {
					t.Fatalf("split root counted or lost leaves: %+v", d)
				}
			})
		}
	}
}

func TestDatabaseLabelDetailBoundsAndCancellation(t *testing.T) {
	s, _, c := labelDetailFixture(t, []databaseSampleRow{{Labels: map[string]string{"cluster": "abc", "job": "test", "Case": "A"}, Samples: 10}, {Labels: map[string]string{"cluster": "abc", "job": "test", "Case": "B"}, Samples: 17}})
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	e, used, err := readDatabaseLabelMetric(context.Background(), tx, c, 100000, 100600, 1)
	if err != nil || e != nil || used != 0 {
		t.Fatalf("oversized inventory sampled: %v %d %v", e, used, err)
	}
	e, used, err = readDatabaseLabelMetric(context.Background(), tx, c, 100000, 100600, 2)
	if err != nil || e == nil || used != 2 || *e.Full.Series != 2 {
		t.Fatalf("distinct canonical identities merged: %v %d %v", e, used, err)
	}
	b := &databaseBudget{Strings: []string{"up"}, Metrics: []budgetMetric{{ID: c.ID, Name: 0}}}
	r := &databaseReport{Metrics: []*databaseMetric{{ID: c.ID, WorkspaceName: c.Workspace}}}
	if err := databaseLabelBudgetFlags(tx, r, b); err != nil {
		t.Fatal(err)
	}
	if !b.Metrics[0].LabelCollected || b.Metrics[0].LabelSummaryIncluded {
		t.Fatal("collected but omitted metric advertised as uncollected/included")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readDatabaseLabelDetail(ctx, tx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation ignored: %v", err)
	}
}

func TestDatabaseLabelDetailAbsent(t *testing.T) {
	s, _ := newScanTestStore(t)
	if detail := labelDetailRead(t, s); detail != nil {
		t.Fatalf("invented label scan: %+v", detail)
	}
}

func TestDatabaseLabelDetailIncompleteMigration(t *testing.T) {
	s, _, _ := labelDetailFixture(t, []databaseSampleRow{{Labels: map[string]string{"job": "test"}, Samples: 27}})
	normalizeLabelDetailFixture(t, s)
	if _, err := s.db.Exec(`CREATE TABLE label_scan_migration(id INTEGER PRIMARY KEY,state TEXT); INSERT INTO label_scan_migration VALUES(1,'migrating'); UPDATE label_scan_metric SET expected_samples=NULL`); err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := readDatabaseLabelDetail(t.Context(), tx); err == nil || !strings.Contains(err.Error(), "migration is incomplete") {
		t.Fatalf("partially migrated labels were readable: %v", err)
	}
}

func normalizeLabelDetailFixture(t *testing.T, s *ScanStore) {
	t.Helper()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`ALTER TABLE label_scan_observation RENAME TO legacy_labels;
 CREATE TABLE label_scan_observation(attempt_id INTEGER NOT NULL,labelset_id INTEGER NOT NULL,timestamp REAL NOT NULL,
 samples INTEGER NOT NULL,PRIMARY KEY(attempt_id,labelset_id,timestamp)) WITHOUT ROWID;`); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.Query(`SELECT attempt_id,labels_json,timestamp,samples FROM legacy_labels`)
	if err != nil {
		t.Fatal(err)
	}
	type observation struct {
		attempt int64
		raw     string
		at      float64
		samples int64
	}
	var observations []observation
	for rows.Next() {
		var o observation
		if err := rows.Scan(&o.attempt, &o.raw, &o.at, &o.samples); err != nil {
			t.Fatal(err)
		}
		observations = append(observations, o)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	for _, o := range observations {
		var labels map[string]string
		if err := json.Unmarshal([]byte(o.raw), &labels); err != nil {
			t.Fatal(err)
		}
		id, err := internLabels(context.Background(), tx, labels)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`INSERT INTO label_scan_observation VALUES(?,?,?,?)`, o.attempt, id, o.at, o.samples); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(`DROP TABLE legacy_labels; UPDATE labelset SET canonical='not JSON: membership is authoritative'`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestDatabaseLabelDetailNormalized(t *testing.T) {
	var data []databaseSampleRow
	for i := 0; i < 250; i++ {
		labels := map[string]string{"Cluster": "ABC", "job": "Test", "Pod": fmt.Sprint(i / 2), "prometheus_replica": fmt.Sprint(i % 2)}
		if i > 0 {
			labels["Optional"] = ""
			if i > 1 {
				labels["Optional"] = fmt.Sprint(i)
			}
		}
		data = append(data, databaseSampleRow{Labels: labels, Samples: int64(i + 1)})
	}
	s, path, _ := labelDetailFixture(t, data)
	legacy := labelDetailRead(t, s)
	normalizeLabelDetailFixture(t, s)
	normalized := labelDetailRead(t, s)
	if len(normalized.Experiments) == 1 {
		if !strings.Contains(normalized.Experiments[0].Status, "matches captured grouped source") {
			t.Fatal("captured comparator not identified")
		}
		legacy.Experiments[0].Status = normalized.Experiments[0].Status
		legacy.Experiments[0].Grouped.State = normalized.Experiments[0].Grouped.State
	}
	if !reflect.DeepEqual(legacy.Experiments, normalized.Experiments) || len(normalized.Experiments) != 1 || !normalized.Experiments[0].Matched {
		t.Fatal("normalized dictionaries changed exact legacy full-run accounting")
	}
	// Inventory-only dictionaries must not trigger base-model guards or appear
	// in ranking/source attribution, even with many names and long values.
	if _, err := s.db.Exec(`INSERT INTO labelset(hash,canonical) VALUES('unrelated','');
 WITH RECURSIVE x(n) AS (VALUES(1) UNION ALL SELECT n+1 FROM x WHERE n<300)
 INSERT INTO label_name(name) SELECT 'unused_'||n FROM x;
 INSERT INTO label_value(value) VALUES(hex(zeroblob(9000000)));
 INSERT INTO labelset_member SELECT (SELECT id FROM labelset WHERE hash='unrelated'),n.id,v.id
 FROM label_name n,label_value v WHERE n.name LIKE 'unused_%' AND length(v.value)=18000000;`); err != nil {
		t.Fatal(err)
	}
	var html bytes.Buffer
	if err := RenderDatabaseContext(context.Background(), &html, path); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html.String(), "unused_") || !strings.Contains(html.String(), `"SourceReconciled":true`) {
		t.Fatal("inventory labels leaked into base model or reconciliation metadata missing")
	}
}

func TestDatabaseLabelDetailNormalizedValidation(t *testing.T) {
	for _, tc := range []struct {
		name, mutation    string
		included, matched bool
	}{
		{"complete", "", true, true},
		{"invalid timestamp", `UPDATE label_scan_observation SET timestamp=100599`, false, false},
		{"overlap", `INSERT INTO label_scan_observation SELECT attempt_id,labelset_id,100599,samples FROM label_scan_observation`, false, false},
		{"wrong total", `UPDATE label_scan_metric SET expected_samples=99`, false, false},
		{"unfinished", `UPDATE query SET state='running' WHERE kind='inventory_labels'`, false, false},
		{"noncanonical dictionary", `UPDATE label_value SET value='ABC' WHERE value='abc'`, false, false},
		{"source attempt changed", `UPDATE label_scan_metric SET source_attempt_id=NULL`, true, false},
		{"current observations changed", `UPDATE observation SET integer_value=NULL`, true, true},
		{"captured samples changed", `UPDATE label_scan_expected_group SET samples=samples+1`, true, false},
		{"captured group removed", `DELETE FROM label_scan_expected_group`, true, false},
		{"captured attempt invalid", `UPDATE attempt SET warnings_json='["partial"]' WHERE id IN (SELECT source_attempt_id FROM label_scan_metric)`, true, false},
		{"captured attempt foreign query", `UPDATE label_scan_metric SET source_attempt_id=(SELECT accepted_attempt_id FROM query WHERE kind='inventory_labels' AND state='succeeded')`, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _ := labelDetailFixture(t, []databaseSampleRow{{Labels: map[string]string{"Cluster": "ABC", "job": "Test"}, Samples: 27}})
			normalizeLabelDetailFixture(t, s)
			if tc.mutation != "" {
				if _, err := s.db.Exec(tc.mutation); err != nil {
					t.Fatal(err)
				}
			}
			d := labelDetailRead(t, s)
			if (len(d.Experiments) == 1) != tc.included || (tc.included && d.Experiments[0].Matched != tc.matched) {
				t.Fatalf("unexpected normalized coverage: %+v", d)
			}
		})
	}
}

func TestDatabaseLabelDetailLegacyCaseCollisions(t *testing.T) {
	for _, data := range [][]databaseSampleRow{
		{{Labels: map[string]string{"Case": "a"}, Samples: 10}, {Labels: map[string]string{"case": "A"}, Samples: 17}},
		{{Labels: map[string]string{"Case": "a", "case": "b"}, Samples: 27}},
	} {
		s, _, _ := labelDetailFixture(t, data)
		if len(labelDetailRead(t, s).Experiments) != 0 {
			t.Fatal("case-folded identity collision published")
		}
	}
}

func TestDatabaseLabelDetailAvailabilityUI(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the label availability UI test")
	}
	script, err := renderTemplates.ReadFile("templates/budget.js")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(script), "const metricExperiments=")
	end := strings.Index(string(script), "function timeline()")
	if start < 0 || end < start {
		t.Fatal("label availability functions not found")
	}
	check := `
 const assert=require('node:assert/strict');
 const data={Metrics:[{}, {LabelCollected:true}, {LabelCollected:true,LabelSummaryIncluded:true}, {LabelCollected:true,LabelSummaryIncluded:true}]};
 const workspace={Name:'test'},label=(_dim,id)=>String(id),metricFilter=()=>1;
 const sampleData=[{Workspace:'test',Metric:'2',Matched:true,SourceReconciled:false},{Workspace:'test',Metric:'3',Matched:true,SourceReconciled:true}];
 ` + string(script[start:end]) + `
 assert.equal(labelAvailability(0).text,'Label details not collected');
 assert.equal(labelAvailability().text,'Label detail not included');
 assert.equal(labelAvailability().available,false);
 assert.match(labelAvailability().title,/collected in SQLite/);
 assert.equal(labelAvailability(2).text,'Label comparison unavailable');
 assert.equal(labelAvailability(2).available,false);
 assert.equal(labelAvailability(3).available,true);
 assert.match(labelAvailability(3).title,/source filters do not change/);
 workspace.Name='other';
 assert.equal(labelAvailability(3).available,false);
 `
	cmd := exec.CommandContext(t.Context(), node, "-e", check)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("availability UI: %v\n%s", err, output)
	}
}

func TestDatabaseLabelDetailNormalizedSourceGroups(t *testing.T) {
	data := []databaseSampleRow{{Labels: map[string]string{"cluster": "abc", "job": "test"}, Samples: 10}, {Labels: map[string]string{"cluster": "abc", "job": "second"}, Samples: 17}}
	s, _, c := labelDetailFixture(t, data)
	normalizeLabelDetailFixture(t, s)
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM label_scan_expected_group WHERE metric_id=?`, c.ID); err != nil {
		t.Fatal(err)
	}
	for i, row := range data {
		id, err := internLabels(context.Background(), tx, row.Labels)
		if err != nil {
			t.Fatal(err)
		}
		// Swap the weights: the metric total is unchanged, but both source
		// groups disagree and must not enable the matched-detail button.
		weight := data[1-i].Samples
		if _, err := tx.Exec(`INSERT INTO label_scan_expected_group VALUES(?,?,?)`, c.ID, id, weight); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	d := labelDetailRead(t, s)
	if len(d.Experiments) != 1 || d.Experiments[0].Matched || *d.Experiments[0].Full.Samples != 27 || *d.Experiments[0].Grouped.Samples != 27 {
		t.Fatalf("equal metric totals bypassed source reconciliation: %+v", d)
	}
}

func TestDatabaseLabelDetailEmptySourceLabels(t *testing.T) {
	for _, normalized := range []bool{false, true} {
		t.Run(fmt.Sprint(normalized), func(t *testing.T) {
			data := []databaseSampleRow{
				{Labels: map[string]string{"cluster": "abc", "job": "test", "namespace": ""}, Samples: 10},
				{Labels: map[string]string{"cluster": "abc", "job": "test"}, Samples: 17},
			}
			s, _, _ := labelDetailFixture(t, data)
			if normalized {
				normalizeLabelDetailFixture(t, s)
			}
			d := labelDetailRead(t, s)
			if len(d.Experiments) != 1 || !d.Experiments[0].Matched || *d.Experiments[0].Full.Series != 2 {
				t.Fatalf("empty namespace failed grouped-absent comparison: %+v", d)
			}
			found := false
			for _, l := range d.Experiments[0].Labels {
				if l.Name != "namespace" {
					continue
				}
				found = true
				if l.Distinct != 1 || len(l.Values) != 2 || l.Values[0].Value != nil || l.Values[1].Value == nil || *l.Values[1].Value != "" || *l.BaseIdentities != 1 {
					t.Fatalf("empty and absent full-label identities lost: %+v", l)
				}
			}
			if !found {
				t.Fatal("empty namespace omitted from full-label summary")
			}
		})
	}
}

func TestDatabaseLabelDetailCapturedSourceRefresh(t *testing.T) {
	s, _, c := labelDetailFixture(t, []databaseSampleRow{{Labels: map[string]string{"cluster": "abc", "job": "test"}, Samples: 27}})
	normalizeLabelDetailFixture(t, s)
	before := labelDetailRead(t, s)
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT INTO attempt(query_id,owner,started_at,state,http_status) VALUES(?,'refresh',1,'succeeded',200)`, c.Source)
	if err != nil {
		t.Fatal(err)
	}
	aid, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	lid, err := internLabels(context.Background(), tx, map[string]string{"job": "changed"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO observation VALUES(?,?,100600,99,99,NULL)`, aid, lid); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE query SET accepted_attempt_id=?,state='running',classification='refreshing' WHERE id=?`, aid, c.Source); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`DELETE FROM observation WHERE attempt_id IN (SELECT source_attempt_id FROM label_scan_metric)`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	after := labelDetailRead(t, s)
	if !reflect.DeepEqual(before, after) || len(after.Experiments) != 1 || !after.Experiments[0].Matched || *after.Experiments[0].Grouped.Attempt == aid || *after.Experiments[0].Grouped.Samples != 27 || !strings.Contains(after.Experiments[0].Status, "matches captured grouped source") {
		t.Fatalf("refresh changed frozen comparison: %+v", after)
	}
}

func TestDatabaseLabelDetailCapturedGroupTampering(t *testing.T) {
	for _, labels := range []map[string]string{
		{"cluster": "abc", "job": "different"},
		{"cluster": "abc", "job": "test", "pod": "not-a-source-dimension"},
	} {
		t.Run(databaseSampleGroup(labels)+fmt.Sprint(labels["pod"]), func(t *testing.T) {
			s, _, c := labelDetailFixture(t, []databaseSampleRow{{Labels: map[string]string{"cluster": "abc", "job": "test"}, Samples: 27}})
			normalizeLabelDetailFixture(t, s)
			tx, err := s.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			gid, err := internLabels(context.Background(), tx, labels)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(`UPDATE label_scan_expected_group SET group_id=? WHERE metric_id=?`, gid, c.ID); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			d := labelDetailRead(t, s)
			if len(d.Experiments) != 1 || d.Experiments[0].Matched {
				t.Fatalf("valid foreign-key group with unchanged samples falsely matched: %+v", d)
			}
		})
	}
}

func TestDatabaseLabelDetailPersistedLiteralQuery(t *testing.T) {
	s, _, c := labelDetailFixture(t, []databaseSampleRow{{Labels: map[string]string{"cluster": "abc", "job": "test"}, Samples: 27}})
	predicate := `cluster!="old-one",cluster!="old-two"`
	expression := `count_over_time(up{` + predicate + `}[600000ms])`
	params := url.Values{"query": {expression}, "time": {time.Unix(100600, 0).UTC().Format(time.RFC3339)}}.Encode()
	if _, err := s.db.Exec(`UPDATE label_scan_query SET predicate=? WHERE query_id=?`, predicate, c.Root); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE query SET expression=?,params=? WHERE id=?`, expression, params, c.Root); err != nil {
		t.Fatal(err)
	}
	d := labelDetailRead(t, s)
	if len(d.Experiments) != 1 || !d.Experiments[0].Matched {
		t.Fatal("successful persisted literal query rejected after compaction")
	}
}

func TestDatabaseLabelDetailMetricLimit(t *testing.T) {
	s, _, c := labelDetailFixture(t, []databaseSampleRow{{Labels: map[string]string{"cluster": "abc", "job": "test"}, Samples: 27}})
	for i := 0; i < 40; i++ {
		name := fmt.Sprintf("extra_%02d", i)
		data := []databaseSampleRow{{Labels: map[string]string{"pod": "one"}, Samples: int64(1000 + i)}}
		if i < 20 {
			data = nil
			for j := 0; j < 40-i; j++ {
				data = append(data, databaseSampleRow{Labels: map[string]string{"pod": fmt.Sprint(j)}, Samples: 1})
			}
		}
		addLabelDetailMetric(t, s, c.WorkspaceID, name, data)
	}
	d := labelDetailRead(t, s)
	if len(d.Experiments) != databaseLabelMetrics {
		t.Fatalf("balanced metric cap not enforced: %+v", d)
	}
	for i, e := range d.Experiments {
		name, reason := fmt.Sprintf("extra_%02d", i/2), "High series count"
		if i%2 == 1 {
			name, reason = fmt.Sprintf("extra_%02d", 39-i/2), "High sample volume"
		}
		if e.Metric != name || e.SelectionReason != reason {
			t.Fatalf("candidate %d: got %s (%s), want %s (%s)", i, e.Metric, e.SelectionReason, name, reason)
		}
	}
	var count int
	if err := s.db.QueryRow(`SELECT count(*) FROM label_scan_observation`).Scan(&count); err != nil || count != 631 {
		t.Fatalf("reader changed omitted raw data: %d %v", count, err)
	}
}

func addLabelDetailMetric(t *testing.T, s *ScanStore, workspace int, name string, data []databaseSampleRow) string {
	t.Helper()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT INTO metric(workspace_id,name,display_name) VALUES(?,?,?)`, workspace, name, name)
	if err != nil {
		t.Fatal(err)
	}
	mid, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO label_scan_metric(metric_id,skipped_zero) VALUES(?,0)`, mid); err != nil {
		t.Fatal(err)
	}
	id, err := planLabelQuery(context.Background(), tx, mid, int64(workspace), name, "", "", 0, 100000, 100600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE label_scan_metric SET root_id=? WHERE metric_id=?`, id, mid); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	labelDetailAccept(t, s, id, data)
	return id
}

func TestDatabaseLabelDetailOversizedCandidates(t *testing.T) {
	s, _, c := labelDetailFixture(t, []databaseSampleRow{{Labels: map[string]string{"cluster": "abc", "job": "test"}, Samples: 27}})
	// Four oversized inventories would exhaust the total budget if charged before
	// preflight. A later, eligible metric must still get a complete summary.
	for i := 0; i < 4; i++ {
		id := addLabelDetailMetric(t, s, c.WorkspaceID, fmt.Sprintf("huge_%d", i), nil)
		if _, err := s.db.Exec(`WITH RECURSIVE x(n) AS (VALUES(1) UNION ALL SELECT n+1 FROM x WHERE n<?)
 INSERT INTO label_scan_observation SELECT q.accepted_attempt_id,json_object('pod',x.n),100600,100 FROM x,query q WHERE q.id=?`, databaseLabelRows+1, id); err != nil {
			t.Fatal(err)
		}
	}
	d := labelDetailRead(t, s)
	if len(d.Experiments) != 1 || d.Experiments[0].Metric != "up" || *d.Experiments[0].Full.Series != 1 {
		t.Fatalf("oversized candidates consumed summary budget: %+v", d)
	}
}

func TestDatabaseLabelRankOrder(t *testing.T) {
	for _, tc := range []struct {
		series     []int
		samples    []int64
		top        int
		order      []int
		membership []uint8
	}{
		{[]int{100, 1, 90, 2, 80}, []int64{100, 1000, 90, 900, 80}, 2, []int{0, 1, 2, 3}, []uint8{1, 2, 1, 2, 0}},
		{[]int{100, 90, 80, 70}, []int64{100, 90, 80, 70}, 3, []int{0, 1, 2}, []uint8{3, 3, 3, 0}},
		{[]int{1, 1, 1, 1}, []int64{1, 1, 1, 1}, 3, []int{0, 1, 2}, []uint8{3, 3, 3, 0}},
	} {
		for i := 0; i < 10; i++ {
			order, membership := databaseLabelRankOrder(tc.series, tc.samples, tc.top)
			if !reflect.DeepEqual(order, tc.order) || !reflect.DeepEqual(membership, tc.membership) {
				t.Fatalf("unstable/duplicate queue picks: %v %v, want %v %v", order, membership, tc.order, tc.membership)
			}
		}
	}
}

func checkLabelDetailValues(t *testing.T, values []databaseSampleValue, series int, samples int64) {
	t.Helper()
	var n int
	var total int64
	var share float64
	for _, v := range values {
		n += v.Series
		total += v.Samples
		share += *v.Share
	}
	if n != series || total != samples || share < 99.999999 || share > 100.000001 || len(values) > 17 {
		t.Fatalf("value union/remainder lost accounting: %d/%d %d/%d share=%f values=%d", n, series, total, samples, share, len(values))
	}
}

func TestDatabaseLabelDetailPrunedLabels(t *testing.T) {
	var data []databaseSampleRow
	for i := 0; i < 20; i++ {
		labels := map[string]string{"cluster": "abc", "job": "test", "prometheus_replica": "one"}
		for j := 0; j < 15; j++ {
			labels[fmt.Sprintf("identity_%02d", j)] = fmt.Sprint(i)
		}
		data = append(data, databaseSampleRow{Labels: labels, Samples: 1})
	}
	s, path, _ := labelDetailFixture(t, data)
	e := labelDetailRead(t, s).Experiments[0]
	if len(e.Labels) != 6 || e.LabelCount != 18 || e.OmittedLabels != 12 || len(e.LabelPairs) != 3 || *e.Full.Series != 20 || *e.Full.Samples != 20 {
		t.Fatalf("incorrect bounded summary: %+v", e)
	}
	for i, l := range e.Labels {
		name := fmt.Sprintf("identity_%02d", i)
		if i == 5 {
			name = "prometheus_replica"
		}
		if l.Name != name || *l.Reduction != 0 || (i < 5 && l.Distinct != 20) {
			t.Fatalf("high-distinct correlated identity or replica lost: %+v", l)
		}
	}
	var html bytes.Buffer
	if err := RenderDatabaseContext(context.Background(), &html, path); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html.String(), `"OmittedLabels":12`) || !strings.Contains(html.String(), "remaining detail in SQLite") {
		t.Fatal("omission notice/metadata not serialized")
	}
}

func TestDatabaseLabelDetailValueUnion(t *testing.T) {
	var values []databaseSampleCombination
	var total int64
	var series int
	for i := 0; i < 24; i++ {
		name := fmt.Sprintf("value_%02d", i)
		v := databaseSampleCombination{Values: [2]*string{&name, nil}, Series: 100 - i, Samples: 1}
		if i >= 8 && i < 16 {
			v.Series, v.Samples = 1, int64(1000+i)
		}
		if i == 0 {
			v.Values[0] = nil
		}
		if i == 1 {
			empty := ""
			v.Values[0] = &empty
		}
		total += v.Samples
		series += v.Series
		values = append(values, v)
	}
	var previous []databaseSampleCombination
	for trial := 0; trial < 5; trial++ {
		sort.Slice(values, func(i, j int) bool { return values[i].Samples < values[j].Samples })
		got := finishDatabaseLabelValues(values, total, 10)
		if len(got) != 17 || got[16].Remainder != 8 || got[0].Values[0] != nil || got[2].Values[0] == nil || *got[2].Values[0] != "" {
			t.Fatalf("top-8 union/absent/empty identities lost: %+v", got)
		}
		seen := map[string]bool{}
		var counts []databaseSampleValue
		for _, v := range got {
			if v.Remainder == 0 {
				key, _ := json.Marshal(v.Values)
				if seen[string(key)] {
					t.Fatal("duplicate selected value tuple")
				}
				seen[string(key)] = true
			}
			counts = append(counts, databaseSampleValue{Series: v.Series, Samples: v.Samples, Share: v.Share})
		}
		checkLabelDetailValues(t, counts, series, total)
		if previous != nil && !reflect.DeepEqual(previous, got) {
			t.Fatal("nondeterministic value ranking")
		}
		previous = got
	}
}

// Opt-in verification of a real local archive; never opens a writable database
// or contacts Azure. Normal unit tests are independent of local scan artifacts.
func TestDatabaseLabelDetailLocalArchive(t *testing.T) {
	path := os.Getenv("AMW_LABEL_DETAIL_DB")
	if path == "" {
		t.Skip("set AMW_LABEL_DETAIL_DB to inspect a local label scan read-only")
	}
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&_pragma=query_only(1)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	started := time.Now()
	d, err := readDatabaseLabelDetail(ctx, tx)
	if err != nil {
		t.Fatal(err)
	}
	if d == nil || len(d.Experiments) == 0 {
		t.Fatal("no completed label summaries available")
	}
	for _, e := range d.Experiments {
		t.Logf("%s/%s: series=%d samples=%d labels=%d pairs=%d matched=%t status=%s", e.Workspace, e.Metric, *e.Full.Series, *e.Full.Samples, len(e.Labels), len(e.LabelPairs), e.Matched, e.Status)
	}
	t.Logf("read-only label summary time: %s", time.Since(started))
}

func TestDatabaseLabelDetailLocalReport(t *testing.T) {
	path := os.Getenv("AMW_LABEL_DETAIL_REPORT")
	if path == "" {
		t.Skip("set AMW_LABEL_DETAIL_REPORT to validate a local rendered report")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > DatabaseReportMaxBytes {
		t.Fatalf("report exceeds 16 MiB: %d", len(data))
	}
	_, body, found := strings.Cut(string(data), `id="sample-data">`)
	if !found {
		t.Fatal("label summaries not embedded")
	}
	body, _, found = strings.Cut(body, `</script>`)
	if !found {
		t.Fatal("unterminated sample data")
	}
	var experiments []databaseSampleExperiment
	if err := json.Unmarshal([]byte(body), &experiments); err != nil {
		t.Fatal(err)
	}
	if len(experiments) == 0 || len(experiments) > databaseLabelMetrics {
		t.Fatalf("unexpected summary count: %d", len(experiments))
	}
	for _, e := range experiments {
		if !e.Matched {
			t.Fatalf("label button unavailable: %s", e.Metric)
		}
		t.Logf("clickable: %s/%s, %d series, %d samples, %s, labels=%d/%d", e.Workspace, e.Metric, *e.Full.Series, *e.Full.Samples, e.SelectionReason, len(e.Labels), e.LabelCount)
	}
	t.Logf("report bytes: %d; label detail bytes: %d", len(data), len(body))
}
