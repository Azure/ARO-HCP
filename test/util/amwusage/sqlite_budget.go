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
	"database/sql"
	"encoding/json"
	"html/template"
	"math"
	"strings"
)

type budgetWorkspace struct {
	ID                                                                      int
	Name, Label                                                             string
	Series, Rate                                                            databaseReconciliation
	IncompleteSeries, IncompleteRate                                        int
	ActiveEnd, ActiveBefore, ActiveDelta, ActiveLimitEnd, ActiveUtilization *float64
	Peak, PeakLimit, PeakUtilization, Mean                                  *float64
	PeakTime                                                                string
	SeriesChart, RateChart                                                  renderChart
	SeriesBands, RateBands                                                  []budgetBand
	// Facts: metric index, namespace, cluster, job, HCP, Prometheus, count index,
	// partial mask (1 = before, 2 = end, 4 = samples).
	// Label -1 is absent; the interned empty string is a different identity.
	Facts   [][8]int
	Metrics []int
}

type budgetBand struct {
	Name       string
	Start, End float64
}

func budgetBands(start, end float64, baselineStart, baselineEnd sql.NullFloat64, first, last float64) []budgetBand {
	if last <= first {
		return nil
	}
	var bands []budgetBand
	add := func(name string, a, z float64) {
		a, z = math.Max(first, a), math.Min(last, z)
		if z > a {
			bands = append(bands, budgetBand{name, (a - first) / (last - first), (z - first) / (last - first)})
		}
	}
	if baselineStart.Valid && baselineEnd.Valid && baselineStart.Float64 < baselineEnd.Float64 && baselineEnd.Float64 <= start {
		add("Previous baseline", baselineStart.Float64, baselineEnd.Float64)
	}
	add("Test run", start, end)
	return bands
}

type budgetMetric struct {
	ID                                  int `json:"id"`
	Name                                int `json:"n"`
	Before, End, Samples                *float64
	LowerBefore, LowerEnd, LowerSamples *float64 `json:",omitempty"`
}

type databaseBudget struct {
	Workspaces []*budgetWorkspace
	Strings    []string
	Counts     [][3]float64
	Metrics    []budgetMetric
	Minutes    float64
	Data       template.JS `json:"-"`
	Samples    template.JS `json:"-"`
}

func readDatabaseBudget(tx databaseReader, r *databaseReport, metrics map[int]*databaseMetric) (*databaseBudget, error) {
	b := &databaseBudget{Minutes: (r.End - r.Start) / 60}
	var baselineStart, baselineEnd sql.NullFloat64
	if err := tx.QueryRow(`SELECT min(start),max(end) FROM window WHERE kind='baseline'`).Scan(&baselineStart, &baselineEnd); err != nil {
		return nil, err
	}
	workspaces := map[int]*budgetWorkspace{}
	metricIndex := map[int]int{}
	stringsIndex := map[string]int{}
	intern := func(s sql.NullString) int {
		if !s.Valid {
			return -1
		}
		if id, ok := stringsIndex[s.String]; ok {
			return id
		}
		id := len(b.Strings)
		stringsIndex[s.String] = id
		b.Strings = append(b.Strings, s.String)
		return id
	}
	for _, w := range r.Workspaces {
		if err := databaseReadCheck(tx); err != nil {
			return nil, err
		}
		label := w.Name
		if strings.HasPrefix(strings.ToLower(label), "services") {
			label = "Services"
		}
		if strings.HasPrefix(strings.ToLower(label), "hcp") {
			label = "HCP"
		}
		w2 := &budgetWorkspace{ID: w.ID, Name: w.Name, Label: label, Series: w.Reconciliation[1], Rate: w.Events.Reconciliation,
			ActiveEnd: w.ActiveEnd, ActiveBefore: w.ActiveBefore, ActiveDelta: w.ActiveDelta, ActiveLimitEnd: w.ActiveLimitEnd, ActiveUtilization: w.ActiveUtilization,
			Peak: w.Events.Peak, PeakLimit: w.Events.PeakLimit, PeakUtilization: w.Events.PeakUtilization, Mean: w.Events.Mean, PeakTime: w.Events.PeakTimestamp(), SeriesChart: w.Chart, RateChart: w.Events.Chart}
		first, last := r.Start, r.End
		if len(w.Timeline) > 0 {
			first, last = math.Min(first, w.Timeline[0].Time), math.Max(last, w.Timeline[len(w.Timeline)-1].Time)
		}
		w2.SeriesBands = budgetBands(r.Start, r.End, baselineStart, baselineEnd, first, last)
		first, last = r.Start, r.End
		if len(w.Events.Timeline) > 0 {
			first, last = math.Min(first, w.Events.Timeline[0].Time), math.Max(last, w.Events.Timeline[len(w.Events.Timeline)-1].Time)
		}
		w2.RateBands = budgetBands(r.Start, r.End, baselineStart, baselineEnd, first, last)
		// Paths already encode every timeline point. Per-point tooltip strings add
		// no information to this thin chart and are not sent to the browser.
		for _, chart := range []*renderChart{&w2.SeriesChart, &w2.RateChart} {
			chart.Lines = append([]chartLine(nil), chart.Lines...)
			for i := range chart.Lines {
				chart.Lines[i].Dots = nil
			}
		}
		b.Workspaces = append(b.Workspaces, w2)
		workspaces[w.ID] = w2
	}
	var accepted [][4]int
	for _, m := range r.Metrics {
		if err := databaseReadCheck(tx); err != nil {
			return nil, err
		}
		w := workspaces[m.Workspace]
		flags := [4]int{m.ID}
		for i, p := range []databaseMeasure{m.Before, m.End, m.Samples} {
			if p.Value != nil {
				flags[i+1] = 1
			} else if p.LowerBound != nil {
				flags[i+1] = 2
			}
		}
		accepted = append(accepted, flags)
		index := len(b.Metrics)
		metricIndex[m.ID] = index
		w.Metrics = append(w.Metrics, index)
		b.Metrics = append(b.Metrics, budgetMetric{ID: m.ID, Name: intern(sql.NullString{String: m.Name, Valid: true}), Before: m.Before.Value, End: m.End.Value, Samples: m.Samples.Value, LowerBefore: m.Before.LowerBound, LowerEnd: m.End.LowerBound, LowerSamples: m.Samples.LowerBound})
		if m.End.Value == nil {
			w.IncompleteSeries++
		}
		if m.Samples.Value == nil {
			w.IncompleteRate++
		}
	}
	flags, err := databaseMarshal(tx, accepted)
	if err != nil {
		return nil, err
	}
	// Older archives have no authoritative active partition plan.
	var active bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM pragma_table_info('query_partition') WHERE name='active')`).Scan(&active); err != nil {
		return nil, err
	}
	selected := `selected_observations AS (
 SELECT o.metric_id,o.kind,o.labelset_id,o.value,0 partial FROM accepted_observation o JOIN valid v ON v.id=o.metric_id
 WHERE o.ranking=1 AND ((o.kind='before12h' AND v.b=1) OR (o.kind='end12h' AND v.e=1) OR (o.kind='run' AND v.s=1))`
	if active {
		// Match readDatabaseLowerBounds: recurse through accepted internal nodes,
		// validate the whole terminal leaf, and reject nonfinite totals. Integer
		// storage is not required by that reader; value is the canonical count.
		selected = `tree(root,id) AS (
 SELECT q.id,q.id FROM query q JOIN valid v ON v.id=q.metric_id WHERE q.ranking=1
 AND ((q.kind='before12h' AND v.b=2) OR (q.kind='end12h' AND v.e=2) OR (q.kind='run' AND v.s=2))
 AND EXISTS(SELECT 1 FROM query_partition p WHERE p.parent_id=q.id AND p.active=1)
 UNION SELECT t.root,p.child_id FROM tree t JOIN query_partition p ON p.parent_id=t.id WHERE p.active=1
 ), leaves AS MATERIALIZED (
 SELECT t.root,q.id FROM tree t JOIN query q ON q.id=t.id
 LEFT JOIN accepted_observation o ON o.query_id=q.id
 WHERE t.id!=t.root AND NOT EXISTS(SELECT 1 FROM query_partition p WHERE p.parent_id=t.id AND p.active=1)
 GROUP BY t.root,q.id
 HAVING q.state='succeeded' AND q.accepted_attempt_id IS NOT NULL
 AND sum(CASE WHEN o.attempt_id IS NOT NULL AND (o.invalid_value IS NOT NULL OR o.value IS NULL OR o.value<0
 OR q.evaluation_time IS NULL OR o.timestamp!=q.evaluation_time) THEN 1 ELSE 0 END)=0
 AND total(o.value)<=1.7976931348623157e308
 ), ` + selected + `
 UNION ALL SELECT root.metric_id,root.kind,o.labelset_id,o.value,1 partial
 FROM leaves l JOIN query root ON root.id=l.root JOIN accepted_observation o ON o.query_id=l.id`
	}
	// No top-N precedes this grouping. Complete parents and partial frontiers
	// are disjoint, and the mask retains provenance after source aggregation.
	rows, err := tx.Query(`WITH RECURSIVE valid AS MATERIALIZED (
 SELECT json_extract(value,'$[0]') id,json_extract(value,'$[1]') b,json_extract(value,'$[2]') e,json_extract(value,'$[3]') s FROM json_each(?)
 ), `+selected+`), labels AS MATERIALIZED (
 SELECT lm.labelset_id,max(CASE WHEN n.name='namespace' THEN v.value END) namespace,
 max(CASE WHEN n.name='cluster' THEN v.value END) cluster,
 max(CASE WHEN n.name='job' THEN v.value END) job,
 max(CASE WHEN n.name='hostedcontrolplane' THEN v.value END) hcp,
 max(CASE WHEN n.name='prometheus' THEN v.value END) prometheus
 FROM labelset_member lm JOIN label_name n ON n.id=lm.name_id JOIN label_value v ON v.id=lm.value_id
 WHERE n.name IN ('namespace','cluster','job','hostedcontrolplane','prometheus') GROUP BY lm.labelset_id
 ) SELECT o.metric_id,l.namespace,l.cluster,l.job,l.hcp,l.prometheus,
 total(CASE WHEN o.kind='before12h' THEN o.value END),
 total(CASE WHEN o.kind='end12h' THEN o.value END),
 total(CASE WHEN o.kind='run' THEN o.value END),
 max(CASE WHEN o.kind='before12h' THEN o.partial ELSE 0 END)
 +2*max(CASE WHEN o.kind='end12h' THEN o.partial ELSE 0 END)
 +4*max(CASE WHEN o.kind='run' THEN o.partial ELSE 0 END)
 FROM selected_observations o LEFT JOIN labels l ON l.labelset_id=o.labelset_id
 GROUP BY o.metric_id,l.namespace,l.cluster,l.job,l.hcp,l.prometheus
 ORDER BY o.metric_id,l.namespace,l.cluster,l.job,l.hcp,l.prometheus`, string(flags))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[[3]float64]int{}
	for rows.Next() {
		if err := databaseReadCheck(tx); err != nil {
			return nil, err
		}
		var mid, partial int
		var labels [5]sql.NullString
		var values [3]float64
		if err := rows.Scan(&mid, &labels[0], &labels[1], &labels[2], &labels[3], &labels[4], &values[0], &values[1], &values[2], &partial); err != nil {
			return nil, err
		}
		id, ok := counts[values]
		if !ok {
			id = len(b.Counts)
			counts[values] = id
			b.Counts = append(b.Counts, values)
		}
		fact := [8]int{metricIndex[mid], 0, 0, 0, 0, 0, id, partial}
		for i, l := range labels {
			fact[i+1] = intern(l)
		}
		w := workspaces[metrics[mid].Workspace]
		w.Facts = append(w.Facts, fact)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	data, err := databaseMarshal(tx, b)
	if err != nil {
		return nil, err
	}
	if err := databaseReadCheck(tx); err != nil {
		return nil, err
	}
	b.Data = template.JS(data)
	// Keep selected label summaries, not raw queries, source ledgers or experiment
	// tables. JSON projection preserves optional label-analysis fields from the
	// enrichment reader without reconstructing physical inventories in the UI.
	if r.SampleDetail != nil {
		raw, err := databaseMarshal(tx, r.SampleDetail.Experiments)
		if err != nil {
			return nil, err
		}
		var experiments []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &experiments); err != nil {
			return nil, err
		}
		for _, e := range experiments {
			if err := databaseReadCheck(tx); err != nil {
				return nil, err
			}
			for k := range e {
				switch k {
				case "Workspace", "Metric", "Selector", "Window", "Start", "End", "Minutes", "Matched", "Labels", "LabelPairs", "Full", "AMWMean", "AMWShare":
				default:
					delete(e, k)
				}
			}
			var full map[string]json.RawMessage
			if err := json.Unmarshal(e["Full"], &full); err != nil {
				return nil, err
			}
			for k := range full {
				if k != "Series" && k != "Samples" && k != "Rate" {
					delete(full, k)
				}
			}
			e["Full"], err = json.Marshal(full)
			if err != nil {
				return nil, err
			}
		}
		data, err := databaseMarshal(tx, experiments)
		if err != nil {
			return nil, err
		}
		b.Samples = template.JS(data)
	}
	return b, nil
}
