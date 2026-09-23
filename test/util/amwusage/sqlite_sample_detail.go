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
	"fmt"
	"html/template"
	"math"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

type databaseSampleDetail struct {
	Experiments []databaseSampleExperiment
	Data        template.JS
}

type databaseSampleExperiment struct {
	Workspace, Metric, Selector, Window string
	Start, End, Minutes                 float64
	Grouped, Full                       databaseSampleQuery
	Status                              string
	Matched                             bool
	AMWMean, AMWPeak, AMWShare          *float64
	CoveredMinutes, ExpectedMinutes     int
	Labels                              []databaseSampleLabel
	LabelPairs                          []databaseSampleLabelPair
	Sources                             []databaseSampleValue
}

type databaseSampleQuery struct {
	ID, Kind, State, Expression, Params string
	Attempt                             *int64
	HTTP                                int
	Samples                             *int64
	Series                              *int
	Rate                                *float64
}

func (q databaseSampleQuery) SampleText() string {
	if q.Samples == nil {
		return "Unavailable"
	}
	return renderNumber(*q.Samples)
}

func (q databaseSampleQuery) AttemptText() string {
	if q.Attempt == nil {
		return "Unavailable"
	}
	return strconv.FormatInt(*q.Attempt, 10)
}

type databaseSampleValue struct {
	Value     *string
	Series    int
	Samples   int64
	Rate      float64
	Share     *float64
	Remainder int
}

func (v databaseSampleValue) Display() string {
	if v.Remainder > 0 {
		return fmt.Sprintf("Other values (exact remainder: %d)", v.Remainder)
	}
	if v.Value == nil {
		return "(absent)"
	}
	if *v.Value == "" {
		return "(empty)"
	}
	return *v.Value
}

type databaseSampleLabel struct {
	Name                                        string
	Distinct, Physical                          int
	Samples                                     int64
	Values                                      []databaseSampleValue
	BaseIdentities, Reduction                   *int
	CardinalityMultiplicity, SampleMultiplicity *float64
}

type databaseSampleLabelPair struct {
	Names                     [2]string
	Distinct                  int
	BaseIdentities, Reduction *int
	CardinalityMultiplicity   *float64
	Values                    []databaseSampleCombination
}

type databaseSampleCombination struct {
	Values    [2]*string
	Series    int
	Samples   int64
	Rate      float64
	Share     *float64
	Remainder int
}

type databaseSampleRow struct {
	Labels  map[string]string
	Samples int64
}

func readDatabaseSampleDetail(tx databaseReader) (*databaseSampleDetail, error) {
	var present, aliases bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE name='enrichment_plan'),EXISTS(SELECT 1 FROM sqlite_schema WHERE name='enrichment_query')`).Scan(&present, &aliases); err != nil {
		return nil, err
	}
	if !present {
		return nil, nil
	}
	var spec, hash string
	if err := tx.QueryRow(`SELECT spec_json,spec_hash FROM enrichment_plan WHERE id=1`).Scan(&spec, &hash); err != nil {
		return nil, err
	}
	if scanHash(spec) != hash {
		return nil, fmt.Errorf("enrichment plan fingerprint mismatch")
	}
	var plan EnrichmentPlan
	if err := json.Unmarshal([]byte(spec), &plan); err != nil {
		return nil, err
	}
	r := &databaseSampleDetail{}
	for _, m := range plan.Metrics {
		if err := databaseReadCheck(tx); err != nil {
			return nil, err
		}
		var wid, mid int
		var workspace string
		if err := tx.QueryRow(`SELECT w.id,m.id,w.name FROM workspace w JOIN metric m ON m.workspace_id=w.id WHERE (w.name=? OR w.arm_id=?) AND m.name=?`, m.Workspace, strings.ToLower(m.Workspace), m.Name).Scan(&wid, &mid, &workspace); err != nil {
			return nil, err
		}
		selector := m.Name
		var matchers []string
		for k, v := range m.MatchLabels {
			matchers = append(matchers, k+"="+strconv.Quote(v))
		}
		sort.Strings(matchers)
		if len(matchers) > 0 {
			selector += "{" + strings.Join(matchers, ",") + "}"
		}
		for _, w := range plan.Windows {
			if err := databaseReadCheck(tx); err != nil {
				return nil, err
			}
			if len(m.Windows) > 0 {
				selected := false
				for _, name := range m.Windows {
					selected = selected || name == w.Name
				}
				if !selected {
					continue
				}
			}
			e := databaseSampleExperiment{Workspace: workspace, Metric: m.Name, Selector: selector, Window: w.Name, Start: scanTime(w.Start), End: scanTime(w.End), Minutes: w.End.Sub(w.Start).Minutes()}
			if e.Minutes <= 0 || finiteSum(e.Minutes) == nil {
				return nil, fmt.Errorf("invalid enrichment duration")
			}
			var grouped, full []databaseSampleRow
			var err error
			e.Grouped, grouped, err = readDatabaseSampleQuery(tx, aliases, wid, mid, e, "samples_window")
			if err != nil {
				return nil, err
			}
			if m.Labels {
				e.Full, full, err = readDatabaseSampleQuery(tx, aliases, wid, mid, e, "inventory_samples")
				if err != nil {
					return nil, err
				}
			} else {
				e.Full.State = "not requested"
			}
			e.Status = "Unavailable: one or both queries are missing, incomplete or invalid; independent measurements are not blended."
			if e.Grouped.Samples != nil && e.Full.Samples != nil {
				a, b := map[string]int64{}, map[string]int64{}
				for _, row := range grouped {
					a[databaseSampleGroup(row.Labels)] += row.Samples
				}
				for _, row := range full {
					b[databaseSampleGroup(row.Labels)] += row.Samples
				}
				e.Matched = *e.Grouped.Samples == *e.Full.Samples && reflect.DeepEqual(a, b)
				e.Status = "INVALID SELECTED EXPERIMENT: grouped/full totals or source groups disagree. These remain independent measurements, not a reconciled total."
				if e.Matched {
					e.Status = "Matched: grouped and full-label sample totals agree, including every source group."
				}
			}
			if e.Full.Samples != nil {
				e.Labels = summarizeDatabaseSampleLabels(full, *e.Full.Samples, e.Minutes)
				if err := databaseReadCheck(tx); err != nil {
					return nil, err
				}
				e.LabelPairs = summarizeDatabaseSampleLabelPairs(full, e.Labels, *e.Full.Samples, e.Minutes)
				if err := databaseReadCheck(tx); err != nil {
					return nil, err
				}
			}
			if e.Grouped.Samples != nil {
				values := map[string]*databaseSampleValue{}
				for _, row := range grouped {
					key := databaseSampleGroup(row.Labels)
					v := values[key]
					if v == nil {
						label := databaseSampleSourceName(row.Labels)
						v = &databaseSampleValue{Value: &label}
						values[key] = v
					}
					v.Series++
					v.Samples += row.Samples
				}
				e.Sources = finishDatabaseSampleValues(values, *e.Grouped.Samples, e.Minutes)
			}
			points, err := readDatabaseSamplePlatform(tx, wid, e.Start, e.End)
			if err != nil {
				return nil, err
			}
			amw := weightedDatabaseEventRate(points, e.Start, e.End)
			e.AMWMean, e.AMWPeak = amw.Mean, amw.Peak
			e.CoveredMinutes, e.ExpectedMinutes = amw.CoveredMinutes, amw.ExpectedMinutes
			if e.Matched && e.Grouped.Rate != nil && e.AMWMean != nil && *e.AMWMean > 0 {
				e.AMWShare = finiteSum(*e.Grouped.Rate / *e.AMWMean * 100)
			}
			r.Experiments = append(r.Experiments, e)
		}
	}
	// Intern summary strings only; no full labelsets are serialized into the report.
	b, err := databaseMarshal(tx, r.Experiments)
	if err != nil {
		return nil, err
	}
	var data any
	decoder := json.NewDecoder(strings.NewReader(string(b)))
	decoder.UseNumber()
	if err = decoder.Decode(&data); err != nil {
		return nil, err
	}
	pool := []string{}
	indexes := map[string]int{}
	var intern func(any) any
	intern = func(v any) any {
		switch x := v.(type) {
		case string:
			i, ok := indexes[x]
			if !ok {
				i = len(pool)
				indexes[x] = i
				pool = append(pool, x)
			}
			return map[string]int{"s": i}
		case []any:
			for i := range x {
				x[i] = intern(x[i])
			}
		case map[string]any:
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				x[k] = intern(x[k])
			}
		}
		return v
	}
	data = intern(data)
	b, err = databaseMarshal(tx, struct {
		Strings     []string
		Experiments any
	}{pool, data})
	if err != nil {
		return nil, err
	}
	r.Data = template.JS(b)
	return r, nil
}

func readDatabaseSampleQuery(tx databaseReader, aliases bool, wid, mid int, e databaseSampleExperiment, role string) (databaseSampleQuery, []databaseSampleRow, error) {
	q := databaseSampleQuery{State: "missing"}
	filter := `q.workspace_id=? AND q.metric_id=? AND w.start=? AND w.end=? AND q.kind=? AND q.ranking=0`
	join := `JOIN window w ON w.id=q.window_id`
	groupColumn := `q.grouping`
	selectorColumn := `NULL`
	if aliases {
		join = `JOIN enrichment_query eq ON eq.query_id=q.id JOIN window w ON w.id=eq.window_id`
		filter = `eq.plan_id=1 AND eq.workspace_id=? AND eq.metric_id=? AND w.start=? AND w.end=? AND eq.role=? AND q.workspace_id=eq.workspace_id AND q.metric_id=eq.metric_id`
		groupColumn = `eq.grouping`
		selectorColumn = `eq.selector`
	}
	rows, err := tx.Query(`SELECT q.id,q.kind,q.state,q.classification,q.expression,q.params,q.accepted_attempt_id,coalesce(a.http_status,0),q.evaluation_time,q.lookback_seconds,`+groupColumn+`,a.state,a.query_id,q.path,`+selectorColumn+` FROM query q `+join+` LEFT JOIN attempt a ON a.id=q.accepted_attempt_id WHERE `+filter, wid, mid, e.Start, e.End, role)
	if err != nil {
		return q, nil, err
	}
	n := 0
	valid := false
	for rows.Next() {
		n++
		var classification, grouping, path string
		var at, lookback sql.NullFloat64
		var attemptState, attemptQuery, selector sql.NullString
		if err = rows.Scan(&q.ID, &q.Kind, &q.State, &classification, &q.Expression, &q.Params, &q.Attempt, &q.HTTP, &at, &lookback, &grouping, &attemptState, &attemptQuery, &path, &selector); err != nil {
			rows.Close()
			return q, nil, err
		}
		full := fmt.Sprintf("count_over_time(%s[%dms])", e.Selector, int64((e.End-e.Start)*1000))
		expression, expectedGrouping := full, ""
		if role == "samples_window" {
			expression = "sum by(" + scanGrouping + ")(" + full + ")"
			expectedGrouping = scanGrouping
		}
		params, paramsErr := url.ParseQuery(q.Params)
		valid = q.State == "succeeded" && q.Attempt != nil && q.HTTP == 200 &&
			attemptState.String == "succeeded" && attemptQuery.String == q.ID && at.Valid && at.Float64 == e.End &&
			(aliases || (lookback.Valid && lookback.Float64 == e.End-e.Start)) && (!aliases || selector.String == e.Selector) &&
			q.Expression == expression && grouping == expectedGrouping && path == "/api/v1/query" && paramsErr == nil &&
			len(params["query"]) == 1 && params.Get("query") == expression &&
			len(params["time"]) == 1 && params.Get("time") == time.Unix(int64(e.End), 0).UTC().Format(time.RFC3339)
		if classification != "" {
			q.State += ": " + classification
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return q, nil, err
	}
	if n != 1 || !valid {
		if n > 0 {
			q.State = "unavailable / invalid query provenance: " + q.State
		}
		return q, nil, nil
	}
	// Read one bounded inventory at a time, with normalized membership, never legacy canonical JSON.
	rows, err = tx.Query(`SELECT o.labelset_id,o.timestamp,o.value,o.integer_value,o.invalid_value,n.name,v.value
 FROM accepted_observation o LEFT JOIN labelset_member lm ON lm.labelset_id=o.labelset_id
 LEFT JOIN label_name n ON n.id=lm.name_id LEFT JOIN label_value v ON v.id=lm.value_id
 WHERE o.query_id=? ORDER BY o.labelset_id,o.timestamp,n.name`, q.ID)
	if err != nil {
		return q, nil, err
	}
	defer rows.Close()
	var result []databaseSampleRow
	var last int64 = -1
	var lastTime float64
	var total int64
	invalid := false
	for rows.Next() {
		var id int64
		var at float64
		var value sql.NullFloat64
		var count sql.NullInt64
		var bad, name, label sql.NullString
		if err = rows.Scan(&id, &at, &value, &count, &bad, &name, &label); err != nil {
			return q, nil, err
		}
		if id != last || at != lastTime {
			if id == last || at != e.End || bad.Valid || !value.Valid || !count.Valid || count.Int64 <= 0 || value.Float64 != float64(count.Int64) || count.Int64 > math.MaxInt64-total {
				invalid = true
			}
			if count.Int64 > 0 && count.Int64 <= math.MaxInt64-total {
				total += count.Int64
			}
			result = append(result, databaseSampleRow{Labels: map[string]string{}, Samples: count.Int64})
			last, lastTime = id, at
		}
		if name.Valid {
			if !label.Valid {
				invalid = true
			}
			result[len(result)-1].Labels[name.String] = label.String
		}
	}
	if err = rows.Err(); err != nil {
		return q, nil, err
	}
	if invalid {
		q.State = "invalid accepted sample values or timestamps (entire query excluded)"
		return q, nil, nil
	}
	q.State = "accepted"
	q.Samples = &total
	size := len(result)
	q.Series = &size
	q.Rate = finiteSum(float64(total) / e.Minutes)
	return q, result, nil
}

func databaseSampleGroup(labels map[string]string) string {
	group := map[string]string{}
	for _, name := range strings.Split(scanGrouping, ",") {
		if v, ok := labels[name]; ok {
			group[name] = v
		}
	}
	b, _ := json.Marshal(group)
	return string(b)
}

func databaseSampleSourceName(labels map[string]string) string {
	var parts []string
	for _, name := range strings.Split(scanGrouping, ",") {
		v, ok := labels[name]
		value := "(absent)"
		if ok {
			value = strconv.Quote(v)
		}
		parts = append(parts, name+"="+value)
	}
	return strings.Join(parts, ", ")
}

func summarizeDatabaseSampleLabels(rows []databaseSampleRow, total int64, minutes float64) []databaseSampleLabel {
	names := map[string]bool{}
	for _, row := range rows {
		for name := range row.Labels {
			names[name] = true
		}
	}
	var result []databaseSampleLabel
	for name := range names {
		l := databaseSampleLabel{Name: name, Physical: len(rows), Samples: total}
		values := map[string]*databaseSampleValue{}
		bases := map[string]int64{}
		for _, row := range rows {
			value, present := row.Labels[name]
			key := "absent"
			if present {
				key = "value:" + value
			}
			v := values[key]
			if v == nil {
				v = &databaseSampleValue{}
				if present {
					v.Value = &value
					l.Distinct++
				}
				values[key] = v
			}
			v.Series++
			v.Samples += row.Samples
			base := databaseSampleBase(row.Labels, name, name)
			if weight, ok := bases[base]; !ok || row.Samples > weight {
				bases[base] = row.Samples
			}
		}
		l.Values = finishDatabaseSampleValues(values, total, minutes)
		n, reduction := len(bases), len(rows)-len(bases)
		l.BaseIdentities, l.Reduction = &n, &reduction
		if n > 0 {
			l.CardinalityMultiplicity = finiteSum(float64(len(rows)) / float64(n))
		}
		// Only replica multiplicity compares samples against the strongest member
		// of each collision group. Neither factor estimates safe labeldrop savings.
		if name == "prometheus_replica" {
			var strongest int64
			for _, v := range bases {
				strongest += v
			}
			if strongest > 0 {
				l.SampleMultiplicity = finiteSum(float64(total) / float64(strongest))
			}
		}
		result = append(result, l)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func databaseSampleBase(labels map[string]string, first, second string) string {
	base := make(map[string]string, len(labels))
	for name, value := range labels {
		if name != first && name != second {
			base[name] = value
		}
	}
	b, _ := json.Marshal(base)
	return string(b)
}

func summarizeDatabaseSampleLabelPairs(rows []databaseSampleRow, labels []databaseSampleLabel, total int64, minutes float64) []databaseSampleLabelPair {
	var selected []databaseSampleLabel
	for _, l := range labels {
		// Missing and explicit-empty values are separate states, even though
		// Distinct counts only present values.
		if len(l.Values) > 1 || l.Name == "prometheus_replica" {
			selected = append(selected, l)
		}
	}
	sort.Slice(selected, func(i, j int) bool {
		if *selected[i].Reduction != *selected[j].Reduction {
			return *selected[i].Reduction > *selected[j].Reduction
		}
		if selected[i].Distinct != selected[j].Distinct {
			return selected[i].Distinct > selected[j].Distinct
		}
		return selected[i].Name < selected[j].Name
	})
	if len(selected) > 6 {
		for _, l := range selected[6:] {
			if l.Name == "prometheus_replica" {
				selected[5] = l
				break
			}
		}
		selected = selected[:6]
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].Name < selected[j].Name })
	var result []databaseSampleLabelPair
	for i, first := range selected {
		for _, second := range selected[i+1:] {
			p := databaseSampleLabelPair{Names: [2]string{first.Name, second.Name}}
			bases := map[string]bool{}
			values := map[string]*databaseSampleCombination{}
			for _, row := range rows {
				bases[databaseSampleBase(row.Labels, first.Name, second.Name)] = true
				var tuple [2]*string
				for k, name := range p.Names {
					if value, ok := row.Labels[name]; ok {
						tuple[k] = &value
					}
				}
				b, _ := json.Marshal(tuple)
				key := string(b)
				v := values[key]
				if v == nil {
					v = &databaseSampleCombination{Values: tuple}
					values[key] = v
				}
				v.Series++
				v.Samples += row.Samples
			}
			n, reduction := len(bases), len(rows)-len(bases)
			p.BaseIdentities, p.Reduction = &n, &reduction
			if n > 0 {
				p.CardinalityMultiplicity = finiteSum(float64(len(rows)) / float64(n))
			}
			p.Distinct = len(values)
			keys := make([]string, 0, len(values))
			for key := range values {
				keys = append(keys, key)
			}
			sort.Slice(keys, func(i, j int) bool {
				if values[keys[i]].Samples != values[keys[j]].Samples {
					return values[keys[i]].Samples > values[keys[j]].Samples
				}
				return keys[i] < keys[j]
			})
			other := databaseSampleCombination{}
			for i, key := range keys {
				if i < 20 {
					p.Values = append(p.Values, *values[key])
				} else {
					other.Remainder++
					other.Series += values[key].Series
					other.Samples += values[key].Samples
				}
			}
			if other.Remainder > 0 {
				p.Values = append(p.Values, other)
			}
			for i := range p.Values {
				v := &p.Values[i]
				v.Rate = float64(v.Samples) / minutes
				if total > 0 {
					v.Share = finiteSum(float64(v.Samples) / float64(total) * 100)
				}
			}
			result = append(result, p)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if *result[i].Reduction != *result[j].Reduction {
			return *result[i].Reduction > *result[j].Reduction
		}
		if result[i].Names[0] != result[j].Names[0] {
			return result[i].Names[0] < result[j].Names[0]
		}
		return result[i].Names[1] < result[j].Names[1]
	})
	return result
}

func finishDatabaseSampleValues(values map[string]*databaseSampleValue, total int64, minutes float64) []databaseSampleValue {
	r := make([]databaseSampleValue, 0, len(values))
	for _, v := range values {
		r = append(r, *v)
	}
	sort.Slice(r, func(i, j int) bool {
		if r[i].Samples != r[j].Samples {
			return r[i].Samples > r[j].Samples
		}
		if r[i].Value == nil {
			return r[j].Value != nil
		}
		return r[j].Value != nil && *r[i].Value < *r[j].Value
	})
	if len(r) > 20 {
		other := databaseSampleValue{Remainder: len(r) - 20}
		for _, v := range r[20:] {
			other.Series += v.Series
			other.Samples += v.Samples
		}
		r = append(r[:20], other)
	}
	for i := range r {
		r[i].Rate = float64(r[i].Samples) / minutes
		if total > 0 {
			r[i].Share = finiteSum(float64(r[i].Samples) / float64(total) * 100)
		}
	}
	return r
}

func readDatabaseSamplePlatform(tx databaseReader, wid int, start, end float64) ([]databaseEventPoint, error) {
	// Restrict physical-series validation to this window, including recorded baseline minutes outside the run.
	rows, err := tx.Query(`WITH relevant AS (
 SELECT p.* FROM platform_observation p JOIN evidence e ON e.id=p.evidence_id
 WHERE e.ok=1 AND e.workspace_id=? AND p.timestamp<? AND p.timestamp+60>?
 AND lower(p.metric) IN ('eventsperminuteingested','eventsperminuteingestedlimit')
 ), physical AS (SELECT lower(metric) metric,count(DISTINCT labelset_id) n FROM relevant GROUP BY lower(metric))
 SELECT p.timestamp,lower(p.metric),count(*),max(s.n),sum(p.invalid_value IS NOT NULL OR p.value IS NULL OR p.value<0),max(p.value)
 FROM relevant p JOIN physical s ON s.metric=lower(p.metric) GROUP BY p.timestamp,lower(p.metric) ORDER BY p.timestamp,lower(p.metric)`, wid, end, start)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var points []databaseEventPoint
	for rows.Next() {
		var at float64
		var metric string
		var count, physical, invalid int
		var value sql.NullFloat64
		if err = rows.Scan(&at, &metric, &count, &physical, &invalid, &value); err != nil {
			return nil, err
		}
		if len(points) == 0 || points[len(points)-1].Time != at {
			points = append(points, databaseEventPoint{Time: at})
		}
		if count == 1 && physical == 1 && invalid == 0 && value.Valid {
			p := &points[len(points)-1]
			if metric == "eventsperminuteingested" {
				p.Usage = finiteSum(value.Float64)
			} else {
				p.Limit = finiteSum(value.Float64)
			}
		}
	}
	return points, rows.Err()
}
