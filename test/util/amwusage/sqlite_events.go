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
	"html/template"
	"math"
)

type databaseEventPoint struct {
	Time         float64
	Usage, Limit *float64
}

type databaseEvents struct {
	Timeline                                         []databaseEventPoint
	Peak, PeakTime, PeakLimit, PeakUtilization, Mean *float64
	CoveredMinutes, ExpectedMinutes                  int
	Chart                                            renderChart
	Reconciliation                                   databaseReconciliation
	Namespaces                                       []databaseNamespace
	NamespaceCount                                   int
	NamespaceTotal                                   databaseNamespaceValues
}

type databaseNamespaceValues struct {
	Before, End, Delta, Samples, Rate, EndShare, RateShare *float64
}

type databaseNamespaceMetric struct {
	Name                                  string
	Count                                 int
	Remainder                             bool
	BeforeMetrics, EndMetrics, RunMetrics int
	databaseNamespaceValues
}

type databaseNamespace struct {
	Cluster, Namespace, HCP *string
	Count                   int
	Remainder               bool
	databaseNamespaceValues
	Metrics []databaseNamespaceMetric
}

// weightedDatabaseEventRate integrates minute bucket maxima, not received event
// counts. A bucket at t represents [t,t+60); endpoints are clipped to the run.
// Missing/invalid usage OR limit makes the whole-run reference unavailable.
func weightedDatabaseEventRate(points []databaseEventPoint, start, end float64) databaseEvents {
	r := databaseEvents{Timeline: points}
	if end <= start || finiteSum(end-start) == nil {
		return r
	}
	byTime := map[float64]databaseEventPoint{}
	validGrain := true
	for _, p := range points {
		if p.Time >= end || p.Time+60 <= start {
			continue
		}
		if math.Mod(p.Time, 60) != 0 {
			validGrain = false
			continue
		}
		if _, exists := byTime[p.Time]; exists {
			validGrain = false
		}
		byTime[p.Time] = p
	}
	integral := 0.0
	for at := math.Floor(start/60) * 60; at < end; at += 60 {
		r.ExpectedMinutes++
		p, ok := byTime[at]
		validUsage := ok && p.Usage != nil && finiteSum(*p.Usage) != nil && *p.Usage >= 0
		validLimit := ok && p.Limit != nil && finiteSum(*p.Limit) != nil && *p.Limit > 0
		if validUsage && (r.Peak == nil || *p.Usage > *r.Peak) {
			r.Peak, r.PeakTime = p.Usage, finiteSum(at)
			r.PeakLimit, r.PeakUtilization = nil, nil
			if validLimit {
				r.PeakLimit = p.Limit
				r.PeakUtilization = finiteSum(*p.Usage / *p.Limit * 100)
			}
		}
		if !validUsage || !validLimit {
			continue
		}
		r.CoveredMinutes++
		integral += *p.Usage * (math.Min(end, at+60) - math.Max(start, at))
	}
	if validGrain && r.CoveredMinutes == r.ExpectedMinutes {
		r.Mean = finiteSum(integral / (end - start))
	}
	if !validGrain {
		r.Peak, r.PeakTime, r.PeakLimit, r.PeakUtilization = nil, nil, nil, nil
	}
	return r
}

func (e databaseEvents) PeakTimestamp() string {
	if e.PeakTime == nil {
		return "Unavailable"
	}
	return renderUTC(*e.PeakTime)
}

func (n databaseNamespace) Label(v *string) string {
	if v == nil {
		return "(absent)"
	}
	if *v == "" {
		return "(empty)"
	}
	return *v
}

// readDatabaseEvents extends the caller's read-only snapshot. metrics supplies
// the already-validated full parent periods; no leaf or browser top-N rows are
// reused for namespace attribution. Future enrichment can attach separate data
// after this call without changing these measurement identities or denominators.
func readDatabaseEvents(tx databaseReader, r *databaseReport, metrics map[int]*databaseMetric) error {
	workspaces := map[int]*databaseWorkspace{}
	for _, w := range r.Workspaces {
		workspaces[w.ID] = w
		w.Events = databaseEvents{}
	}
	rows, err := tx.Query(`WITH physical AS (
 SELECT e.workspace_id,lower(p.metric) metric,count(DISTINCT p.labelset_id) n
 FROM platform_observation p JOIN evidence e ON e.id=p.evidence_id
 WHERE e.ok=1 AND lower(p.metric) IN ('eventsperminuteingested','eventsperminuteingestedlimit')
 GROUP BY e.workspace_id,lower(p.metric)
 ) SELECT e.workspace_id,p.timestamp,lower(p.metric),count(*),max(s.n),
 sum(CASE WHEN p.invalid_value IS NOT NULL OR p.value IS NULL OR p.value<0 THEN 1 ELSE 0 END),max(p.value)
 FROM platform_observation p JOIN evidence e ON e.id=p.evidence_id
 JOIN physical s ON s.workspace_id=e.workspace_id AND s.metric=lower(p.metric)
 WHERE e.ok=1 GROUP BY e.workspace_id,p.timestamp,lower(p.metric)
 ORDER BY e.workspace_id,p.timestamp,lower(p.metric)`)
	if err != nil {
		return err
	}
	for rows.Next() {
		if err := databaseReadCheck(tx); err != nil {
			rows.Close()
			return err
		}
		var wid, count, physical, invalid int
		var at float64
		var metric string
		var value sql.NullFloat64
		if err := rows.Scan(&wid, &at, &metric, &count, &physical, &invalid, &value); err != nil {
			rows.Close()
			return err
		}
		w := workspaces[wid]
		if w == nil {
			continue
		}
		var v *float64
		if count == 1 && physical == 1 && invalid == 0 && value.Valid && math.Mod(at, 60) == 0 {
			v = finiteSum(value.Float64)
		}
		points := &w.Events.Timeline
		if n := len(*points); n == 0 || (*points)[n-1].Time != at {
			if n > 0 && at-(*points)[n-1].Time > 60 {
				*points = append(*points, databaseEventPoint{Time: (*points)[n-1].Time + 60})
			}
			*points = append(*points, databaseEventPoint{Time: at})
		}
		p := &(*points)[len(*points)-1]
		if metric == "eventsperminuteingested" {
			p.Usage = v
		} else if v != nil && *v > 0 {
			p.Limit = v
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, w := range r.Workspaces {
		if err := databaseReadCheck(tx); err != nil {
			return err
		}
		w.Events = weightedDatabaseEventRate(w.Events.Timeline, r.Start, r.End)
		var usage, limit []renderPoint
		for _, p := range w.Events.Timeline {
			usage = append(usage, renderPoint{Time: p.Time, Value: p.Usage})
			limit = append(limit, renderPoint{Time: p.Time, Value: p.Limit})
		}
		start, end := r.Start, r.End
		if len(usage) > 0 {
			start = math.Min(start, usage[0].Time)
			end = math.Max(end, usage[len(usage)-1].Time)
		}
		w.Events.Chart = makeChart(w.Name+" / Events per minute", []chartInput{{"Usage: minute maximum", "#006f79", false, usage}, {"Limit: same-minute maximum", "#b45309", false, limit}}, start, end, &artifactRun{Start: r.Start, End: r.End})
		var rates []databaseMeasure
		for _, m := range metrics {
			if m.Workspace == w.ID {
				rates = append(rates, databaseMeasure{Value: m.Rate, LowerBound: m.LowerRate})
			}
		}
		w.Events.Reconciliation = reconcileDatabasePeriod("Run-average samples/min vs AMW mean of minute maxima", w.Events.Mean, rates)
	}
	if err := readDatabaseNamespaces(tx, r, metrics, workspaces); err != nil {
		return err
	}
	data := map[int][]databaseNamespace{}
	for _, w := range r.Workspaces {
		data[w.ID] = w.Events.Namespaces
	}
	b, err := databaseMarshal(tx, data)
	if err != nil {
		return err
	}
	r.EventsData = template.JS(b)
	return nil
}

func readDatabaseNamespaces(tx databaseReader, r *databaseReport, metrics map[int]*databaseMetric, workspaces map[int]*databaseWorkspace) error {
	// Passing the model's accepted-period flags keeps whole-parent invalidation
	// identical to metric ranking, including duplicate parents and nonfinite sums.
	var accepted [][4]int
	comparable := map[int]bool{}
	for id := range workspaces {
		comparable[id] = true
	}
	for _, m := range metrics {
		if err := databaseReadCheck(tx); err != nil {
			return err
		}
		v := [4]int{m.ID}
		for i, p := range []*float64{m.Before.Value, m.End.Value, m.Samples.Value} {
			if p != nil {
				v[i+1] = 1
			}
		}
		if v[1] != v[2] {
			comparable[m.Workspace] = false
		}
		accepted = append(accepted, v)
	}
	b, err := databaseMarshal(tx, accepted)
	if err != nil {
		return err
	}
	// Rank only after summing every accepted group across jobs and metrics. Keep
	// underlay namespace and HCP as separate dimensions, including NULL vs empty.
	rows, err := tx.Query(`WITH valid AS MATERIALIZED (
 SELECT json_extract(value,'$[0]') id,json_extract(value,'$[1]') b,json_extract(value,'$[2]') e,json_extract(value,'$[3]') s FROM json_each(?)
 ), labels AS MATERIALIZED (
 SELECT lm.labelset_id,max(CASE WHEN n.name='cluster' THEN v.value END) cluster,
 max(CASE WHEN n.name='namespace' THEN v.value END) namespace,
 max(CASE WHEN n.name='hostedcontrolplane' THEN v.value END) hcp
 FROM labelset_member lm JOIN label_name n ON n.id=lm.name_id JOIN label_value v ON v.id=lm.value_id
 WHERE n.name IN ('cluster','namespace','hostedcontrolplane') GROUP BY lm.labelset_id
 ), contributions AS MATERIALIZED (
 SELECT o.workspace_id,o.metric_id,l.cluster,l.namespace,l.hcp,
 total(CASE WHEN o.kind='before12h' THEN o.value END) b,
 total(CASE WHEN o.kind='end12h' THEN o.value END) e,
 total(CASE WHEN o.kind='run' THEN o.value END) s
 FROM accepted_observation o JOIN valid v ON v.id=o.metric_id LEFT JOIN labels l ON l.labelset_id=o.labelset_id
 WHERE o.ranking=1 AND ((o.kind='before12h' AND v.b=1) OR (o.kind='end12h' AND v.e=1) OR (o.kind='run' AND v.s=1))
 GROUP BY o.workspace_id,o.metric_id,l.cluster,l.namespace,l.hcp
 ), namespaces AS (
 SELECT workspace_id,cluster,namespace,hcp,total(b) b,total(e) e,total(s) s FROM contributions GROUP BY workspace_id,cluster,namespace,hcp
 ), ranked AS MATERIALIZED (
 SELECT *,row_number() OVER (PARTITION BY workspace_id ORDER BY max(e,s*60/?) DESC,cluster,namespace,hcp) nr FROM namespaces
 ), detail AS (
 SELECT c.*,v.b vb,v.e ve,v.s vs,r.nr,row_number() OVER (PARTITION BY c.workspace_id,c.cluster,c.namespace,c.hcp ORDER BY max(c.e,c.s*60/?) DESC,c.metric_id) mr
 FROM contributions c JOIN valid v ON v.id=c.metric_id JOIN ranked r ON r.workspace_id=c.workspace_id AND r.cluster IS c.cluster AND r.namespace IS c.namespace AND r.hcp IS c.hcp WHERE r.nr<=200
 ) SELECT workspace_id,nr,cluster,namespace,hcp,0,0,1,b,e,s,0,0,0,0 FROM ranked WHERE nr<=200
 UNION ALL SELECT workspace_id,201,NULL,NULL,NULL,0,0,count(*),total(b),total(e),total(s),0,0,0,0 FROM ranked WHERE nr>200 GROUP BY workspace_id
 UNION ALL SELECT workspace_id,nr,cluster,namespace,hcp,CASE WHEN mr<=20 THEN mr ELSE 21 END,
 CASE WHEN mr<=20 THEN metric_id ELSE 0 END,count(*),total(b),total(e),total(s),sum(vb),sum(ve),sum(vs),min(vb=ve)
 FROM detail GROUP BY workspace_id,nr,CASE WHEN mr<=20 THEN mr ELSE 21 END
 ORDER BY 1,2,6`, string(b), r.End-r.Start, r.End-r.Start)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var wid, nr, mr, mid, count int
		var bc, ec, sc, matched int
		var cluster, namespace, hcp *string
		var before, end, samples float64
		if err := rows.Scan(&wid, &nr, &cluster, &namespace, &hcp, &mr, &mid, &count, &before, &end, &samples, &bc, &ec, &sc, &matched); err != nil {
			return err
		}
		w := workspaces[wid]
		v := databaseNamespaceValues{}
		beforeOK, endOK, runOK := w.BeforeCount > 0, w.EndCount > 0, w.SampleCount > 0
		deltaOK := comparable[wid]
		if mr > 0 {
			beforeOK, endOK, runOK = bc > 0, ec > 0, sc > 0
			deltaOK = matched == 1
		}
		if beforeOK {
			v.Before = finiteSum(before)
		}
		if endOK {
			v.End = finiteSum(end)
		}
		if runOK {
			v.Samples = finiteSum(samples)
			v.Rate = finiteSum(samples * 60 / (r.End - r.Start))
		}
		if beforeOK && endOK && deltaOK {
			v.Delta = finiteSum(end - before)
		}
		if v.End != nil && w.ActiveEnd != nil && *w.ActiveEnd > 0 {
			v.EndShare = finiteSum(end / *w.ActiveEnd * 100)
		}
		if v.Rate != nil && w.Events.Mean != nil && *w.Events.Mean > 0 {
			v.RateShare = finiteSum(*v.Rate / *w.Events.Mean * 100)
		}
		if mr == 0 {
			w.Events.NamespaceCount += count
			w.Events.Namespaces = append(w.Events.Namespaces, databaseNamespace{Cluster: cluster, Namespace: namespace, HCP: hcp, Count: count, Remainder: nr == 201, databaseNamespaceValues: v})
		} else {
			n := &w.Events.Namespaces[len(w.Events.Namespaces)-1]
			name := "Other metrics (exact remainder)"
			if mid != 0 {
				name = metrics[mid].Name
			}
			n.Metrics = append(n.Metrics, databaseNamespaceMetric{Name: name, Count: count, Remainder: mr == 21, BeforeMetrics: bc, EndMetrics: ec, RunMetrics: sc, databaseNamespaceValues: v})
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, w := range r.Workspaces {
		t := &w.Events.NamespaceTotal
		if w.BeforeCount > 0 {
			t.Before = finiteSum(w.Before)
		}
		if w.EndCount > 0 {
			t.End = finiteSum(w.End)
		}
		if w.SampleCount > 0 {
			t.Samples = finiteSum(w.Samples)
			t.Rate = finiteSum(w.Samples * 60 / (r.End - r.Start))
		}
		if t.Before != nil && t.End != nil && comparable[w.ID] {
			t.Delta = finiteSum(w.End - w.Before)
		}
	}
	return nil
}
