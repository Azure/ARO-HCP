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
	"fmt"
	"math"
	"net/url"
	"os"
	"strings"
	"testing"
)

func TestDatabaseEventsWeightedReference(t *testing.T) {
	n := func(v float64) *float64 { return &v }
	points := []databaseEventPoint{{Time: 0, Usage: n(100), Limit: n(1000)}, {Time: 60, Usage: n(300), Limit: n(500)}, {Time: 120, Usage: n(200), Limit: n(2000)}}
	r := weightedDatabaseEventRate(points, 15, 130)
	want := (100.0*45 + 300*60 + 200*10) / 115
	if r.Mean == nil || *r.Mean != want || *r.Peak != 300 || *r.PeakTime != 60 || *r.PeakLimit != 500 || *r.PeakUtilization != 60 || r.CoveredMinutes != 3 {
		t.Fatalf("clipped duration or paired peak limit incorrect: %+v", r)
	}
	for _, tc := range []struct {
		name   string
		points []databaseEventPoint
	}{
		{"missing minute", []databaseEventPoint{points[0], points[2]}},
		{"missing limit", []databaseEventPoint{points[0], {Time: 60, Usage: n(300)}, points[2]}},
		{"zero limit", []databaseEventPoint{points[0], {Time: 60, Usage: n(300), Limit: n(0)}, points[2]}},
		{"negative usage", []databaseEventPoint{points[0], {Time: 60, Usage: n(-1), Limit: n(500)}, points[2]}},
		{"nonfinite", []databaseEventPoint{points[0], {Time: 60, Usage: n(math.Inf(1)), Limit: n(500)}, points[2]}},
		{"off grain", []databaseEventPoint{points[0], {Time: 61, Usage: n(300), Limit: n(500)}, points[2]}},
		{"duplicate", append(append([]databaseEventPoint{}, points...), points[1])},
		{"empty", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if r := weightedDatabaseEventRate(tc.points, 15, 130); r.Mean != nil {
				t.Fatalf("incomplete data averaged observed minutes: %v", *r.Mean)
			}
		})
	}
	r = weightedDatabaseEventRate([]databaseEventPoint{{Time: 60, Usage: n(0), Limit: n(10)}}, 60, 120)
	if r.Mean == nil || *r.Mean != 0 || r.ExpectedMinutes != 1 {
		t.Fatal("zero usage or half-open bucket boundaries lost")
	}
	r = weightedDatabaseEventRate([]databaseEventPoint{{Time: 60, Usage: n(12)}}, 60, 120)
	if r.Peak == nil || r.PeakLimit != nil || r.PeakUtilization != nil {
		t.Fatal("missing paired limit borrowed another minute")
	}
}

func TestDatabaseEventsPlatformValidation(t *testing.T) {
	for _, mode := range []string{"valid", "duplicate evidence", "physical switch", "missing", "invalid", "zero limit", "off grain"} {
		t.Run(mode, func(t *testing.T) {
			s, _ := newScanTestStore(t)
			_, err := s.db.Exec(`UPDATE run SET start=99975,end=100090;
 INSERT INTO evidence(id,workspace_id,kind,url,ok,started_at,finished_at,duration_ms) VALUES('events',1,'platform','cached',1,'','',0),('duplicate',1,'platform','cached',1,'','',0);
 INSERT INTO labelset(hash,canonical) VALUES('events-extra','');
 INSERT INTO platform_observation(evidence_id,metric,labelset_id,timestamp,value)
 SELECT 'events',m.metric,(SELECT min(id) FROM labelset),t.at,CASE WHEN m.metric='EventsPerMinuteIngested' THEN t.usage ELSE t.lim END
 FROM (SELECT 'EventsPerMinuteIngested' metric UNION ALL SELECT 'EventsPerMinuteIngestedLimit') m,
 (SELECT 99960 at,100 usage,1000 lim UNION ALL SELECT 100020,300,500 UNION ALL SELECT 100080,200,2000) t;`)
			if err != nil {
				t.Fatal(err)
			}
			mutation := map[string]string{
				"duplicate evidence": `INSERT INTO platform_observation SELECT 'duplicate',metric,labelset_id,timestamp,value,invalid_value FROM platform_observation WHERE evidence_id='events' AND timestamp=100020`,
				"physical switch":    `UPDATE platform_observation SET labelset_id=(SELECT max(id) FROM labelset) WHERE timestamp=100020`,
				"missing":            `DELETE FROM platform_observation WHERE timestamp=100020`,
				"invalid":            `UPDATE platform_observation SET invalid_value='NaN' WHERE timestamp=100020`,
				"zero limit":         `UPDATE platform_observation SET value=0 WHERE timestamp=100020 AND metric='EventsPerMinuteIngestedLimit'`,
				"off grain":          `UPDATE platform_observation SET timestamp=100021 WHERE timestamp=100020`,
			}[mode]
			if mutation != "" {
				if _, err := s.db.Exec(mutation); err != nil {
					t.Fatal(err)
				}
			}
			r := databaseTestReport(t, s).Workspaces[0].Events
			if mode == "valid" {
				if r.Mean == nil || *r.Mean != (100.0*45+300*60+200*10)/115 || *r.PeakLimit != 500 {
					t.Fatalf("bad platform reference: %+v", r)
				}
				if r.Reconciliation.Complete == nil || *r.Reconciliation.Complete != 27.0*60/115 {
					t.Fatal("sample rate does not use exact full duration")
				}
			} else if r.Mean != nil {
				t.Fatalf("accepted %s reference", mode)
			}
			if mode == "missing" && (len(r.Timeline) != 3 || r.Timeline[1].Usage != nil) {
				t.Fatal("missing minute not preserved as gap")
			}
		})
	}
}

func TestDatabaseNamespacesFullParentsAndSeparateWindows(t *testing.T) {
	s, _ := newScanTestStore(t)
	databaseTestAccept(t, s, "up", "before12h", `[{"metric":{"cluster":"a","namespace":"velero","job":"one"},"value":[100000,"8"]}]`)
	databaseTestAccept(t, s, "up", "end12h", `[{"metric":{"cluster":"a","namespace":"velero","job":"one"},"value":[100600,"10"]},{"metric":{"cluster":"a","namespace":"velero","job":"two"},"value":[100600,"20"]}]`)
	databaseTestAccept(t, s, "up", "run", `[{"metric":{"cluster":"a","namespace":"velero"},"value":[100600,"100"]}]`)
	databaseTestAccept(t, s, "other", "end12h", `[{"metric":{"cluster":"a","namespace":"velero"},"value":[100600,"50"]}]`)
	r := databaseTestReport(t, s)
	n := r.Workspaces[0].Events.Namespaces[0]
	if len(r.Workspaces[0].Events.Namespaces) != 1 || *n.Before != 8 || *n.End != 80 || *n.Samples != 100 || *n.Rate != 10 || n.Delta != nil {
		t.Fatalf("cross-metric/job aggregation or differing windows: %+v", n)
	}
	if n.Metrics[0].Before != nil || n.Metrics[0].Samples != nil || n.Metrics[0].Delta != nil {
		t.Fatal("unknown metric period zero-filled")
	}
	databaseTestAccept(t, s, "other", "before12h", `[]`)
	r = databaseTestReport(t, s)
	n = r.Workspaces[0].Events.Namespaces[0]
	if n.Delta == nil || *n.Delta != 72 || n.Metrics[0].Before == nil || *n.Metrics[0].Before != 0 {
		t.Fatal("completed zero not distinguished from unknown")
	}
	databaseTestAccept(t, s, "other", "end12h", `[{"metric":{"cluster":"a","namespace":"velero"},"value":[100600,"99999"]},{"metric":{"namespace":"bad"},"value":[100600,"NaN"]}]`)
	r = databaseTestReport(t, s)
	if *r.Workspaces[0].Events.Namespaces[0].End != 30 || *r.Workspaces[0].Events.NamespaceTotal.End != 30 {
		t.Fatal("invalid group did not exclude entire parent period")
	}
	for _, n := range r.Workspaces[0].Events.Namespaces {
		if n.Namespace != nil && *n.Namespace == "bad" {
			t.Fatal("invalid-only namespace leaked into ranking")
		}
	}
}

func TestDatabaseNamespacesIdentityAndRemainders(t *testing.T) {
	s, path := newScanTestStore(t)
	var groups []string
	for i := 0; i < 215; i++ {
		groups = append(groups, fmt.Sprintf(`{"metric":{"namespace":"ns-%03d","cluster":"c"},"value":[100600,"%d"]}`, i, i+1))
	}
	for _, labels := range []string{`{}`, `{"namespace":""}`, `{"namespace":"velero"}`, `{"namespace":"velero","hostedcontrolplane":""}`, `{"namespace":"velero","hostedcontrolplane":"hcp"}`, `{"namespace":"velero","cluster":"other"}`} {
		groups = append(groups, fmt.Sprintf(`{"metric":%s,"value":[100600,"1000"]}`, labels))
	}
	databaseTestAccept(t, s, "up", "end12h", "["+strings.Join(groups, ",")+"]")
	r := databaseTestReport(t, s)
	w := r.Workspaces[0]
	if w.Events.NamespaceCount != 222 || len(w.Events.Namespaces) != 201 {
		t.Fatalf("wrong bounded count: %d / %d", w.Events.NamespaceCount, len(w.Events.Namespaces))
	}
	var total float64
	for _, n := range w.Events.Namespaces {
		total += *n.End
	}
	if total != 215*216/2+6000 || total != *w.Events.NamespaceTotal.End {
		t.Fatal("namespace remainder not additive")
	}
	last := w.Events.Namespaces[200]
	if !last.Remainder || last.Count != 22 || *last.End != 21*22/2 {
		t.Fatalf("incorrect namespace remainder: %+v", last)
	}
	// The metric's existing 50-source truncation must not constrain this analysis.
	if len(databaseTestMetric(t, r, "up").Sources) != 51 {
		t.Fatal("fixture must exceed browser source cap")
	}
	var html bytes.Buffer
	if err := RenderDatabase(&html, path); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Events per minute", "Namespace", "(empty)", "(absent)", "Prometheus source", "This dimension"} {
		if !strings.Contains(html.String(), want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestDatabaseNamespaceMetricRemainder(t *testing.T) {
	s, _ := newScanTestStore(t)
	// Reuse accepted data but create independent catalog parents. Cached queries
	// sharing those attempts must not add a second contribution.
	_, err := s.db.Exec(`WITH RECURSIVE nums(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM nums WHERE n<25)
 INSERT INTO metric(workspace_id,name,display_name) SELECT 1,'metric-'||n,'metric-'||n FROM nums;
 INSERT INTO query(id,workspace_id,metric_id,kind,ranking,path,params,expression,evaluation_time,state,accepted_attempt_id)
 SELECT 'ns-'||m.id,1,m.id,'run',1,'query','ns-'||m.id,'test',q.evaluation_time,'succeeded',q.accepted_attempt_id
 FROM metric m,query q WHERE m.name LIKE 'metric-%' AND q.ranking=1 AND q.kind='run' AND q.state='succeeded';
 INSERT INTO query(id,workspace_id,metric_id,kind,ranking,path,params,expression,evaluation_time,state,accepted_attempt_id)
 SELECT 'cached-'||id,workspace_id,metric_id,kind,0,path,'cached-'||id,expression,evaluation_time,state,accepted_attempt_id FROM query WHERE id LIKE 'ns-%';`)
	if err != nil {
		t.Fatal(err)
	}
	r := databaseTestReport(t, s)
	n := r.Workspaces[0].Events.Namespaces[0]
	if len(n.Metrics) != 21 || *n.Samples != 26*27 {
		t.Fatalf("wrong drilldown cap or cached double counting: %+v", n)
	}
	last := n.Metrics[20]
	if !last.Remainder || last.Count != 6 || *last.Samples != 6*27 || last.Before != nil || last.End != nil || last.Delta != nil {
		t.Fatalf("wrong metric remainder or unknown windows: %+v", last)
	}
}

// Optional local evidence regression. The live scan is opened read-only and is
// never imported, migrated or changed by this test.
func TestDatabaseEventsSavedEvidence(t *testing.T) {
	path := os.Getenv("AMW_EVENTS_DATABASE")
	if path == "" {
		t.Skip("set AMW_EVENTS_DATABASE to the saved fullscan.db")
	}
	u := url.URL{Scheme: "file", Path: path}
	u.RawQuery = "mode=ro&_pragma=query_only(1)"
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
	r, err := readDatabaseReport(tx)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range r.Workspaces {
		if w.Events.Mean == nil || len(w.Events.Timeline) != 204 {
			t.Fatalf("incomplete saved platform reference for %s", w.Name)
		}
		t.Logf("%s: scopes=%d, mean=%.2f, peak=%.0f at %s", w.Name, w.Events.NamespaceCount, *w.Events.Mean, *w.Events.Peak, w.Events.PeakTimestamp())
		for _, b := range r.Budget.Workspaces {
			if b.ID != w.ID {
				continue
			}
			var end, samples float64
			var veleroEnd, veleroSamples float64
			for _, f := range b.Facts {
				v := r.Budget.Counts[f[6]]
				end += v[1]
				samples += v[2]
				if f[1] >= 0 && r.Budget.Strings[f[1]] == "velero" {
					veleroEnd += v[1]
					veleroSamples += v[2]
				}
			}
			if w.Name == "services-westus3" && (veleroEnd != 918182+957115 || veleroSamples != 43538652+44672175) {
				t.Fatal("namespace facts truncated before combining clusters")
			}
			if end != w.End || samples != w.Samples {
				t.Fatalf("source facts changed workspace totals: %s", w.Name)
			}
			if b.ActiveLimitEnd == nil || b.ActiveUtilization == nil {
				t.Fatalf("missing paired active quota: %s", w.Name)
			}
		}
		if w.Name != "services-westus3" {
			continue
		}
		found := 0
		var allEnd, allSamples float64
		for _, n := range w.Events.Namespaces {
			allEnd += *n.End
			allSamples += *n.Samples
			if n.Namespace != nil && *n.Namespace == "velero" {
				if n.Cluster == nil {
					t.Fatal("velero lost cluster identity")
				}
				want := map[string][2]float64{"int-westus3-mgmt-1": {918182, 43538652}, "int-westus3-mgmt-2": {957115, 44672175}}[*n.Cluster]
				if *n.End != want[0] || *n.Samples != want[1] {
					t.Fatalf("saved velero totals changed for %s: %v / %v", *n.Cluster, *n.End, *n.Samples)
				}
				found++
			}
		}
		if found != 2 || allEnd != *w.Events.NamespaceTotal.End || allSamples != *w.Events.NamespaceTotal.Samples {
			t.Fatalf("saved namespace remainders not additive or velero scopes missing: %d", found)
		}
	}
}
