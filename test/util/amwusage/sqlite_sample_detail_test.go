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
	"math"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
)

func sampleDetailAccept(t *testing.T, s *ScanStore, role string, rows []databaseSampleRow) {
	t.Helper()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	var id string
	if err = tx.QueryRow(`SELECT id FROM query WHERE kind=?`, role).Scan(&id); err != nil {
		t.Fatal(err)
	}
	res, err := tx.Exec(`INSERT INTO attempt(query_id,owner,started_at,state,http_status) VALUES(?,'test',0,'succeeded',200)`, id)
	if err != nil {
		t.Fatal(err)
	}
	aid, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		lid, err := internLabels(context.Background(), tx, row.Labels)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(`INSERT INTO observation SELECT ?,?,evaluation_time,?,?,NULL FROM query WHERE id=?`, aid, lid, row.Samples, row.Samples, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = tx.Exec(`UPDATE query SET state='succeeded',accepted_attempt_id=? WHERE id=?`, aid, id); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestDatabaseSampleReplicaWeights(t *testing.T) {
	for _, weights := range [][]int64{{1000, 1, 1, 1}, {1000, 1000, 1000, 1000}, {1000, 1, 1}} {
		var rows []databaseSampleRow
		var total int64
		for i, w := range weights {
			rows = append(rows, databaseSampleRow{Labels: map[string]string{"pod": "same", "prometheus_replica": fmt.Sprint(i)}, Samples: w})
			total += w
		}
		for _, minutes := range []float64{1, 5} {
			labels := summarizeDatabaseSampleLabels(rows, total, minutes)
			l := labels[1]
			if l.Name != "prometheus_replica" || *l.BaseIdentities != 1 || *l.Reduction != len(weights)-1 || *l.CardinalityMultiplicity != float64(len(weights)) || *l.SampleMultiplicity != float64(total)/1000 {
				t.Fatalf("cardinality substituted for weights: %+v", l)
			}
			if pod := labels[0]; *pod.BaseIdentities != len(weights) || *pod.Reduction != 0 || *pod.CardinalityMultiplicity != 1 || pod.SampleMultiplicity != nil {
				t.Fatalf("non-replica impact or multiplicity wrong: %+v", pod)
			}
			if l.Values[0].Rate != 1000/minutes || math.Abs(*l.Values[0].Share-100000/float64(total)) > 1e-12 {
				t.Fatalf("duration or denominator wrong: %+v", l.Values[0])
			}
		}
	}
}

func TestDatabaseSampleLabelCorrelation(t *testing.T) {
	rows := []databaseSampleRow{
		{Labels: map[string]string{"pod": "a", "instance": "a", "prometheus_replica": "0"}, Samples: 90},
		{Labels: map[string]string{"pod": "b", "instance": "b", "prometheus_replica": "0"}, Samples: 10},
	}
	labels := summarizeDatabaseSampleLabels(rows, 100, 5)
	for _, l := range labels {
		if *l.BaseIdentities != 2 || *l.Reduction != 0 || *l.CardinalityMultiplicity != 1 {
			t.Fatalf("correlated label alone must not collapse identities: %+v", l)
		}
		if l.Name != "prometheus_replica" && l.SampleMultiplicity != nil {
			t.Fatalf("non-replica weighted multiplicity is undefined: %+v", l)
		}
		var share float64
		for _, v := range l.Values {
			share += *v.Share
		}
		if math.Abs(share-100) > 1e-12 {
			t.Fatalf("each dimension independently partitions 100%%, not an additive attribution: %s %f", l.Name, share)
		}
	}
	pairs := summarizeDatabaseSampleLabelPairs(rows, labels, 100, 5)
	if len(pairs) != 3 || pairs[0].Names != [2]string{"instance", "pod"} || *pairs[0].BaseIdentities != 1 || *pairs[0].Reduction != 1 || *pairs[0].CardinalityMultiplicity != 2 {
		t.Fatalf("joint drop must expose correlation: %+v", pairs)
	}
	if v := pairs[0].Values[0]; v.Samples != 90 || v.Series != 1 || v.Rate != 18 || *v.Share != 90 || *v.Values[0] != "a" || *v.Values[1] != "a" {
		t.Fatalf("weighted tuple distribution wrong: %+v", v)
	}
	for _, p := range pairs[1:] {
		if *p.Reduction != 0 {
			t.Fatalf("constant replica must not change joint impact: %+v", p)
		}
	}
}

func TestDatabaseSamplePairBoundsAndMissing(t *testing.T) {
	var rows []databaseSampleRow
	var total int64
	for i := 0; i < 27; i++ {
		labels := map[string]string{"prometheus_replica": "0"}
		for j := 0; j < 8; j++ {
			if i == 0 {
				continue
			}
			value := ""
			if i > 1 {
				value = fmt.Sprint(i)
			}
			labels[fmt.Sprintf("label%d", j)] = value
		}
		rows = append(rows, databaseSampleRow{Labels: labels, Samples: int64(27 - i)})
		total += int64(27 - i)
	}
	labels := summarizeDatabaseSampleLabels(rows, total, 5)
	pairs := summarizeDatabaseSampleLabelPairs(rows, labels, total, 5)
	if len(pairs) != 15 {
		t.Fatalf("six selected dimensions must produce 15 pairs: %d", len(pairs))
	}
	names := map[string]bool{}
	for _, p := range pairs {
		for _, name := range p.Names {
			names[name] = true
		}
		var samples int64
		var series int
		var rate, share float64
		for _, v := range p.Values {
			samples += v.Samples
			series += v.Series
			rate += v.Rate
			share += *v.Share
		}
		if p.Distinct != 27 || len(p.Values) != 21 || p.Values[20].Remainder != 7 || samples != total || series != 27 || math.Abs(rate-float64(total)/5) > 1e-12 || math.Abs(share-100) > 1e-12 {
			t.Fatalf("top20 plus exact remainder lost weights: %+v", p)
		}
		if p.Values[0].Values[0] != nil || p.Values[1].Values[0] == nil || *p.Values[1].Values[0] != "" {
			t.Fatalf("missing and empty conflated: %+v", p.Values[:2])
		}
	}
	if len(names) != 6 || !names["prometheus_replica"] || names["label5"] || names["label6"] || names["label7"] {
		t.Fatalf("ranked selection must reserve replica and break ties by name: %v", names)
	}
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	if again := summarizeDatabaseSampleLabelPairs(rows, summarizeDatabaseSampleLabels(rows, total, 5), total, 5); !reflect.DeepEqual(pairs, again) {
		t.Fatal("pair selection/order depends on input or map iteration order")
	}
	if empty := summarizeDatabaseSampleLabelPairs(nil, labels, 0, 5); len(empty) != 15 {
		t.Fatal("expected selected dimensions for empty-window denominator check")
	} else {
		for _, p := range empty {
			if *p.BaseIdentities != 0 || *p.Reduction != 0 || p.CardinalityMultiplicity != nil || len(p.Values) != 0 {
				t.Fatalf("empty window must not invent zero multiplicity: %+v", p)
			}
		}
	}
}

func TestDatabaseSamplePairDistinctBeforeTruncation(t *testing.T) {
	var rows []databaseSampleRow
	for i := 0; i < 625; i++ {
		value := fmt.Sprint(i)
		rows = append(rows, databaseSampleRow{Labels: map[string]string{"pod": value, "instance": value}, Samples: 1})
	}
	labels := summarizeDatabaseSampleLabels(rows, 625, 5)
	for _, l := range labels {
		if *l.Reduction != 0 {
			t.Fatalf("correlated label alone must not collapse identities: %+v", l)
		}
	}
	pairs := summarizeDatabaseSampleLabelPairs(rows, labels, 625, 5)
	if len(pairs) != 1 {
		t.Fatalf("expected one correlated pair, got %d", len(pairs))
	}
	p := pairs[0]
	if p.Distinct != 625 || len(p.Values) != 21 || p.Values[20].Remainder != 605 || p.Values[20].Samples != 605 || *p.Reduction != 624 {
		t.Fatalf("distinct tuple count must precede top20 truncation: %+v", p)
	}
}

func TestDatabaseSampleBlankAndRemainder(t *testing.T) {
	rows := []databaseSampleRow{{Labels: map[string]string{}, Samples: 2}, {Labels: map[string]string{"label": ""}, Samples: 3}}
	for i := 0; i < 25; i++ {
		rows = append(rows, databaseSampleRow{Labels: map[string]string{"label": fmt.Sprint(i)}, Samples: int64(i + 4)})
	}
	var total int64
	for _, r := range rows {
		total += r.Samples
	}
	l := summarizeDatabaseSampleLabels(rows, total, 5)[0]
	var samples int64
	var series int
	for _, v := range l.Values {
		samples += v.Samples
		series += v.Series
	}
	if l.Distinct != 26 || l.Physical != 27 || *l.BaseIdentities != 1 || *l.Reduction != 26 || *l.CardinalityMultiplicity != 27 || l.SampleMultiplicity != nil || len(l.Values) != 21 || l.Values[20].Remainder != 7 || samples != total || series != 27 {
		t.Fatalf("remainder lost weights: %+v", l)
	}
	l = summarizeDatabaseSampleLabels(rows[:2], 5, 5)[0]
	if l.Distinct != 1 || l.Values[0].Display() != "(empty)" || l.Values[1].Display() != "(absent)" {
		t.Fatalf("blank/absent conflated: %+v", l)
	}
}

func TestDatabaseSampleAcceptedFenceAndValidation(t *testing.T) {
	for _, mode := range []string{"valid", "legacy", "alias", "empty", "blocked", "late invalid", "null", "wrong timestamp", "wrong evaluation", "wrong lookback", "wrong params", "mismatch", "group mismatch", "failed staging"} {
		t.Run(mode, func(t *testing.T) {
			s, path := newScanTestStore(t)
			if err := s.PlanEnrichment(context.Background(), strings.NewReader(enrichmentTestPlan(t))); err != nil {
				t.Fatal(err)
			}
			group := map[string]string{"cluster": "mgmt-1", "namespace": "velero"}
			groupRows := []databaseSampleRow{{Labels: group, Samples: 1003}}
			var full []databaseSampleRow
			for i, w := range []int64{1000, 1, 1, 1} {
				full = append(full, databaseSampleRow{Labels: map[string]string{"cluster": "mgmt-1", "namespace": "velero", "prometheus_replica": fmt.Sprint(i)}, Samples: w})
			}
			if mode == "empty" {
				groupRows = nil
				full = nil
			}
			if mode == "mismatch" {
				groupRows[0].Samples++
			}
			if mode == "group mismatch" {
				group["namespace"] = "other"
			}
			sampleDetailAccept(t, s, "samples_window", groupRows)
			sampleDetailAccept(t, s, "inventory_samples", full)
			mutations := map[string]string{
				"legacy":           `DROP TABLE enrichment_query`,
				"alias":            `UPDATE query SET kind='cached_samples',ranking=1,window_id=NULL,lookback_seconds=NULL,grouping='' WHERE kind='samples_window'`,
				"blocked":          `UPDATE query SET state='blocked',classification='hard_limit' WHERE kind='inventory_samples'`,
				"late invalid":     `UPDATE observation SET invalid_value='NaN' WHERE attempt_id=(SELECT accepted_attempt_id FROM query WHERE kind='inventory_samples') AND labelset_id=(SELECT max(labelset_id) FROM accepted_observation WHERE kind='inventory_samples')`,
				"null":             `UPDATE observation SET integer_value=NULL WHERE attempt_id=(SELECT accepted_attempt_id FROM query WHERE kind='inventory_samples')`,
				"wrong timestamp":  `UPDATE observation SET timestamp=100000 WHERE attempt_id=(SELECT accepted_attempt_id FROM query WHERE kind='inventory_samples')`,
				"wrong evaluation": `UPDATE query SET evaluation_time=100000 WHERE kind='inventory_samples'`,
				"wrong lookback":   `DROP TABLE enrichment_query; UPDATE query SET lookback_seconds=300 WHERE kind='inventory_samples'`,
				"wrong params":     `UPDATE query SET params='query=wrong&time=wrong' WHERE kind='inventory_samples'`,
				"failed staging":   `INSERT INTO attempt(query_id,owner,started_at,state) SELECT id,'failed',0,'failed' FROM query WHERE kind='inventory_samples'; INSERT INTO observation SELECT (SELECT max(id) FROM attempt),labelset_id,100060,999999,999999,NULL FROM accepted_observation WHERE kind='inventory_samples' LIMIT 1`,
			}
			if mutation := mutations[mode]; mutation != "" {
				if _, err := s.db.Exec(mutation); err != nil {
					t.Fatal(err)
				}
			}
			r := databaseTestReport(t, s)
			e := r.SampleDetail.Experiments[0]
			switch mode {
			case "valid", "legacy", "alias", "failed staging":
				if !e.Matched || *e.Full.Samples != 1003 || *e.Full.Series != 4 || *e.Full.Rate != 1003 {
					t.Fatalf("valid weighted query lost: %+v", e)
				}
			case "empty":
				if !e.Matched || *e.Full.Samples != 0 || *e.Full.Series != 0 || *e.Full.Rate != 0 || len(e.Labels) != 0 || len(e.LabelPairs) != 0 {
					t.Fatalf("paired empty must be measured zero: %+v", e)
				}
			case "mismatch", "group mismatch":
				if e.Matched || e.AMWShare != nil || e.Full.Samples == nil || !strings.Contains(e.Status, "INVALID SELECTED EXPERIMENT") {
					t.Fatalf("independent mismatch silently blended: %+v", e)
				}
			default:
				if e.Matched || e.Full.Samples != nil || e.Full.Series != nil || len(e.Labels) != 0 || len(e.LabelPairs) != 0 || e.Grouped.Samples == nil {
					t.Fatalf("invalid inventory leaked partial result: %+v", e)
				}
			}
			var html bytes.Buffer
			if err := RenderDatabase(&html, path); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(html.String(), "999999") {
				t.Fatal("failed attempt leaked")
			}
			var encoded any
			if err := json.Unmarshal([]byte(r.SampleDetail.Data), &encoded); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDatabaseSampleWindowPlatform(t *testing.T) {
	s, _ := newScanTestStore(t)
	if _, err := s.db.Exec(`INSERT INTO evidence(id,workspace_id,kind,url,ok,started_at,finished_at,duration_ms) VALUES('sample-platform',1,'platform','cached',1,'','',0);
 INSERT INTO platform_observation(evidence_id,metric,labelset_id,timestamp,value)
 SELECT 'sample-platform',m.metric,(SELECT min(id) FROM labelset),t.at,CASE WHEN m.metric='EventsPerMinuteIngested' THEN t.usage ELSE 1000 END
 FROM (SELECT 'EventsPerMinuteIngested' metric UNION ALL SELECT 'EventsPerMinuteIngestedLimit') m,
 (SELECT 99660 at,100 usage UNION ALL SELECT 99720,200 UNION ALL SELECT 99780,300 UNION ALL SELECT 99840,400 UNION ALL SELECT 99900,500 UNION ALL SELECT 100020,700) t;`); err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, tc := range []struct{ start, end, mean float64 }{{99660, 99960, 300}, {100020, 100080, 700}} {
		points, err := readDatabaseSamplePlatform(tx, 1, tc.start, tc.end)
		if err != nil {
			t.Fatal(err)
		}
		r := weightedDatabaseEventRate(points, tc.start, tc.end)
		if r.Mean == nil || *r.Mean != tc.mean {
			t.Fatalf("baseline outside run or peak lost: %+v", r)
		}
	}
}

func TestDatabaseSampleWorkspaceARMCaseAndCanonicalLabels(t *testing.T) {
	s, _ := newScanTestStore(t)
	if err := s.PlanEnrichment(context.Background(), strings.NewReader(enrichmentTestPlan(t))); err != nil {
		t.Fatal(err)
	}
	// Persisted plans may use an ARM ID rather than the workspace name.
	var spec, arm string
	if err := s.db.QueryRow(`SELECT spec_json,(SELECT arm_id FROM workspace WHERE id=1) FROM enrichment_plan WHERE id=1`).Scan(&spec, &arm); err != nil {
		t.Fatal(err)
	}
	spec = strings.Replace(spec, `"workspace":"test"`, `"workspace":"`+strings.ToUpper(arm)+`"`, 1)
	if _, err := s.db.Exec(`UPDATE enrichment_plan SET spec_json=?,spec_hash=? WHERE id=1`, spec, scanHash(spec)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE workspace SET name='hcps-westus3' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	sampleDetailAccept(t, s, "samples_window", []databaseSampleRow{{Labels: map[string]string{"CLUSTER": "MGMT-1", "NAMESPACE": "VELERO"}, Samples: 5}})
	sampleDetailAccept(t, s, "inventory_samples", []databaseSampleRow{
		{Labels: map[string]string{"Cluster": "Mgmt-1", "Namespace": "Velero", "Pod": "A", "INSTANCE": "A"}, Samples: 3},
		{Labels: map[string]string{"cluster": "mgmt-1", "namespace": "velero", "pod": "B", "instance": "B"}, Samples: 2},
	})
	report := databaseTestReport(t, s)
	e := report.SampleDetail.Experiments[0]
	var browser []databaseSampleExperiment
	if err := json.Unmarshal([]byte(report.Budget.Samples), &browser); err != nil {
		t.Fatal(err)
	}
	if len(browser) != 1 || browser[0].Workspace != "hcps-westus3" || browser[0].Workspace != report.Budget.Workspaces[0].Name || browser[0].Metric != "up" || browser[0].Selector != `up{cluster="mgmt-1",namespace="velero"}` {
		t.Fatalf("ARM-sourced experiment must join the UI workspace name without changing its selector: %+v", browser)
	}
	if !e.Matched || len(e.LabelPairs) != 1 || e.LabelPairs[0].Names != [2]string{"instance", "pod"} || *e.LabelPairs[0].Values[0].Values[0] != "a" || *e.LabelPairs[0].Reduction != 1 {
		t.Fatalf("ARM lookup or AMW case canonicalization lost: %+v", e)
	}
}

func TestDatabaseSampleSummaryDoesNotExportInventory(t *testing.T) {
	s, path := newScanTestStore(t)
	if err := s.PlanEnrichment(context.Background(), strings.NewReader(enrichmentTestPlan(t))); err != nil {
		t.Fatal(err)
	}
	var rows []databaseSampleRow
	for i := 0; i < 100; i++ {
		value := fmt.Sprintf("private-physical-tuple-%03d", i)
		rows = append(rows, databaseSampleRow{Labels: map[string]string{"cluster": "mgmt-1", "namespace": "velero", "pod": value, "instance": value, "uid": value}, Samples: int64(100 - i)})
	}
	sampleDetailAccept(t, s, "samples_window", []databaseSampleRow{{Labels: map[string]string{"cluster": "mgmt-1", "namespace": "velero"}, Samples: 5050}})
	sampleDetailAccept(t, s, "inventory_samples", rows)
	r := databaseTestReport(t, s).SampleDetail
	if len(r.Experiments[0].LabelPairs) != 3 {
		t.Fatal("expected three pairs of varying labels")
	}
	var html bytes.Buffer
	if err := RenderDatabase(&html, path); err != nil {
		t.Fatal(err)
	}
	for i := 20; i < 100; i++ {
		if value := fmt.Sprintf("private-physical-tuple-%03d", i); strings.Contains(html.String(), value) || strings.Contains(string(r.Data), value) {
			t.Fatalf("raw inventory outside top20 leaked into HTML: %s", value)
		}
	}
	if len(r.Data) > 300_000 {
		t.Fatalf("unbounded sample summary: %d bytes", len(r.Data))
	}
}

func TestDatabaseSampleRealSnapshot(t *testing.T) {
	path := os.Getenv("AMW_SAMPLE_DATABASE")
	if path == "" {
		t.Skip("set AMW_SAMPLE_DATABASE for read-only snapshot verification")
	}
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&_pragma=query_only(1)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	detail, err := readDatabaseSampleDetail(tx)
	if err != nil {
		t.Fatal(err)
	}
	metrics := map[string]bool{}
	pairs := 0
	for _, e := range detail.Experiments {
		metrics[e.Workspace+"/"+e.Metric] = true
		pairs += len(e.LabelPairs)
		if len(e.LabelPairs) > 15 {
			t.Fatal("too many label pairs")
		}
	}
	if len(metrics) != 4 || len(detail.Experiments) != 8 || pairs == 0 || len(detail.Data) > 300_000 {
		t.Fatalf("unexpected fixture summary: metrics=%d experiments=%d pairs=%d bytes=%d", len(metrics), len(detail.Experiments), pairs, len(detail.Data))
	}
	t.Logf("complete summary: metrics=%d experiments=%d pairs=%d JSON=%d bytes", len(metrics), len(detail.Experiments), pairs, len(detail.Data))
	var html bytes.Buffer
	if err := RenderDatabase(&html, path); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html.String(), `"Samples":4068`) || !strings.Contains(html.String(), `"Samples":25460`) || strings.Contains(html.String(), "INVALID SELECTED EXPERIMENT") {
		t.Fatal("real grouped/full sample reconciliation failed")
	}
	// Keep this assertion independent of report-scale catalog data size.
	start := strings.Index(html.String(), `id="sample-data">`)
	if start < 0 {
		t.Fatal("missing summary")
	}
	end := strings.Index(html.String()[start:], "</script>")
	if end < 0 || end > 300_000 {
		t.Fatalf("unbounded enrichment summary: %d bytes", end)
	}
	t.Logf("summary JSON including tag: %d bytes", end)
}
