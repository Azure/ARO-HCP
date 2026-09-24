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
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"time"
)

// These are embedding limits, not collection limits. Inspect at most 30 completed
// candidates from the top 15 by whole-run series and samples, summarize 12, and
// never sample a raw inventory. Omitted inventories remain in SQLite. Summaries
// retain six labels, three pairs, and the top eight values by each measure.
const (
	databaseLabelMetrics      = 12
	databaseLabelRows         = 500000
	databaseLabelTotalRows    = 2000000
	databaseLabelSummaryBytes = 4 << 20
)

type databaseLabelCandidate struct {
	ID, WorkspaceID                 int
	Metric, Workspace, Root, Source string
	Expected                        sql.NullInt64
	SourceAttempt                   sql.NullInt64
	FrozenSource                    bool
	Rows                            int
	Samples                         int64
	SelectionReason                 string
}

type databaseLabelIdentity struct {
	Key     string
	Samples int64
}

type databaseLabelDictionary struct {
	Name   string
	Values []string
	Index  map[string]uint32
}

// readDatabaseLabelDetail uses request metadata before reading individual
// accepted attempts. Selecting label_scan_result across the catalog
// would repeatedly evaluate its correlated raw-row aggregates.
func readDatabaseLabelDetail(ctx context.Context, tx databaseReader) (*databaseSampleDetail, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if reader, ok := tx.(databaseContextReader); ok {
		reader.ctx = ctx
		tx = reader
	}
	if reader, ok := tx.(*sql.Tx); ok {
		tx = databaseContextReader{ctx: ctx, tx: reader}
	}
	var present bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE name='label_scan')`).Scan(&present); err != nil || !present {
		return nil, err
	}
	var migrating bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE name='label_scan_migration')`).Scan(&migrating); err != nil {
		return nil, err
	}
	if migrating {
		var state string
		if err := tx.QueryRow(`SELECT state FROM label_scan_migration WHERE id=1`).Scan(&state); err != nil {
			return nil, err
		}
		if state != "complete" {
			return nil, fmt.Errorf("label normalization migration is incomplete; use labels --migrate-only")
		}
	}
	var start, end, runStart, runEnd float64
	if err := tx.QueryRow(`SELECT l.start,l.end,r.start,r.end FROM label_scan l JOIN run r ON r.id=l.id WHERE l.id=1`).Scan(&start, &end, &runStart, &runEnd); err != nil {
		return nil, err
	}
	if start != runStart || end != runEnd || end <= start || finiteSum(end-start) == nil || start != math.Trunc(start) || end != math.Trunc(end) {
		return nil, fmt.Errorf("invalid full-run label scan window")
	}
	var frozen bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM pragma_table_info('label_scan_metric') WHERE name='source_attempt_id')`).Scan(&frozen); err != nil {
		return nil, err
	}
	sourceAttempt := "NULL"
	if frozen {
		sourceAttempt = "c.source_attempt_id"
	}
	// Materialize accepted leaf counts once, without label joins or correlated
	// observation aggregates. Raw row counts are provisional cardinality: the
	// bounded reader below still rejects duplicate identities and invalid windows.
	rows, err := tx.Query(`WITH leaves AS MATERIALIZED (
 SELECT l.metric_id,q.accepted_attempt_id,
 q.state='succeeded' AND q.accepted_attempt_id IS NOT NULL AS accepted
 FROM label_scan_query l JOIN query q ON q.id=l.query_id WHERE l.split=0
 ), complete AS MATERIALIZED (
 SELECT metric_id FROM leaves GROUP BY metric_id HAVING min(accepted)=1
 ), counts AS MATERIALIZED (
 SELECT l.metric_id,count(o.attempt_id) AS series,coalesce(sum(o.samples),0) AS samples
 FROM complete c JOIN leaves l ON l.metric_id=c.metric_id
 LEFT JOIN label_scan_observation o ON o.attempt_id=l.accepted_attempt_id GROUP BY l.metric_id
 ) SELECT c.metric_id,m.workspace_id,m.display_name,w.name,c.root_id,coalesce(c.source_query_id,''),c.expected_samples,` + sourceAttempt + `,n.series,n.samples
 FROM label_scan_metric c JOIN metric m ON m.id=c.metric_id JOIN workspace w ON w.id=m.workspace_id
 JOIN counts n ON n.metric_id=c.metric_id WHERE c.skipped_zero=0 ORDER BY c.metric_id`)
	if err != nil {
		return nil, err
	}
	var candidates []databaseLabelCandidate
	for rows.Next() {
		var c databaseLabelCandidate
		if err := rows.Scan(&c.ID, &c.WorkspaceID, &c.Metric, &c.Workspace, &c.Root, &c.Source, &c.Expected, &c.SourceAttempt, &c.Rows, &c.Samples); err != nil {
			rows.Close()
			return nil, err
		}
		c.FrozenSource = frozen
		candidates = append(candidates, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	series, samples := make([]int, len(candidates)), make([]int64, len(candidates))
	for i, c := range candidates {
		series[i], samples[i] = c.Rows, c.Samples
	}
	order, membership := databaseLabelRankOrder(series, samples, 15)
	detail := &databaseSampleDetail{}
	remaining, bytes := databaseLabelTotalRows, 0
	for _, index := range order {
		c := candidates[index]
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(detail.Experiments) >= databaseLabelMetrics || remaining == 0 {
			break
		}
		if c.Rows > min(databaseLabelRows, remaining) {
			continue
		}
		c.SelectionReason = map[uint8]string{1: "High series count", 2: "High sample volume", 3: "High series count and sample volume"}[membership[index]]
		e, used, err := readDatabaseLabelMetric(ctx, tx, c, start, end, min(databaseLabelRows, remaining))
		if err != nil {
			return nil, fmt.Errorf("label detail %s/%s: %w", c.Workspace, c.Metric, err)
		}
		remaining -= used
		if e == nil {
			continue
		}
		encoded, err := databaseMarshal(tx, e)
		if err != nil {
			return nil, err
		}
		if len(encoded) > databaseLabelSummaryBytes-bytes {
			continue
		}
		bytes += len(encoded)
		detail.Experiments = append(detail.Experiments, *e)
	}
	return detail, nil
}

// Alternate the next unseen entry in each top-N queue. Input order breaks ties;
// callers supply stable metric IDs or sorted value tuples. All counts cover the
// same full-run window, so dividing samples by duration cannot change the rank.
func databaseLabelRankOrder(series []int, samples []int64, top int) ([]int, []uint8) {
	queues := [2][]int{make([]int, len(series)), make([]int, len(series))}
	membership := make([]uint8, len(series))
	for q := range queues {
		for i := range series {
			queues[q][i] = i
		}
		sort.SliceStable(queues[q], func(i, j int) bool {
			a, b := queues[q][i], queues[q][j]
			if q == 0 {
				return series[a] > series[b]
			}
			return samples[a] > samples[b]
		})
		queues[q] = queues[q][:min(top, len(series))]
		for _, i := range queues[q] {
			membership[i] |= 1 << q
		}
	}
	seen := make([]bool, len(series))
	var order []int
	for len(queues[0])+len(queues[1]) > 0 {
		for q := range queues {
			for len(queues[q]) > 0 {
				i := queues[q][0]
				queues[q] = queues[q][1:]
				if !seen[i] {
					seen[i] = true
					order = append(order, i)
					break
				}
			}
		}
	}
	return order, membership
}

// Query provenance is checked for each physical leaf, not just the inventory's
// root (which can be blocked after a successful exhaustive partition).
func readDatabaseLabelQuery(tx databaseReader, c databaseLabelCandidate, id, selector, kind string, start, end float64) (databaseSampleQuery, bool, error) {
	q := databaseSampleQuery{}
	var at, lookback sql.NullFloat64
	var mid, wid, ranking int
	var grouping, path, classification string
	var state, attemptQuery, warnings, attemptClass sql.NullString
	accepted := "q.accepted_attempt_id"
	args := []any{id}
	if kind == "run" && c.FrozenSource {
		if !c.SourceAttempt.Valid {
			return q, false, nil
		}
		accepted = "?"
		args = []any{c.SourceAttempt.Int64, id}
	}
	err := tx.QueryRow(`SELECT q.id,q.kind,q.state,q.expression,q.params,a.id,coalesce(a.http_status,0),
 q.metric_id,q.workspace_id,q.ranking,q.evaluation_time,q.lookback_seconds,q.grouping,q.path,q.classification,
 a.state,a.query_id,a.warnings_json,a.classification
 FROM query q LEFT JOIN attempt a ON a.id=`+accepted+` WHERE q.id=?`, args...).Scan(
		&q.ID, &q.Kind, &q.State, &q.Expression, &q.Params, &q.Attempt, &q.HTTP, &mid, &wid, &ranking, &at, &lookback, &grouping, &path, &classification, &state, &attemptQuery, &warnings, &attemptClass)
	if err == sql.ErrNoRows {
		return q, false, nil
	}
	if err != nil {
		return q, false, err
	}
	if kind == "run" && c.FrozenSource {
		// A later ranking refresh does not invalidate the captured comparator.
		q.State, classification = state.String, attemptClass.String
	}
	expression := fmt.Sprintf("count_over_time(%s[%dms])", selector, int64((end-start)*1000))
	expectedRanking, expectedGrouping := 0, ""
	if kind == "run" {
		expression = "sum by(" + scanGrouping + ")(" + expression + ")"
		expectedRanking, expectedGrouping = 1, scanGrouping
	}
	params, paramsErr := url.ParseQuery(q.Params)
	// Reconciled partition roots are local synthetic attempts, not HTTP requests.
	httpOK := q.HTTP == 200 || (kind == "run" && q.HTTP == 0 && classification == "partitioned" && attemptClass.String == "partitioned")
	valid := mid == c.ID && wid == c.WorkspaceID && q.Kind == kind && ranking == expectedRanking &&
		q.State == "succeeded" && q.Attempt != nil && state.String == "succeeded" && attemptQuery.String == id && httpOK && warnings.String == "[]" &&
		at.Valid && at.Float64 == end && lookback.Valid && lookback.Float64 == end-start && grouping == expectedGrouping &&
		q.Expression == expression && path == "/api/v1/query" && paramsErr == nil && len(params["query"]) == 1 && params.Get("query") == expression &&
		len(params["time"]) == 1 && params.Get("time") == time.Unix(int64(end), 0).UTC().Format(time.RFC3339)
	return q, valid, nil
}

func readDatabaseLabelMetric(ctx context.Context, tx databaseReader, c databaseLabelCandidate, start, end float64, limit int) (*databaseSampleExperiment, int, error) {
	var normalized bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM pragma_table_info('label_scan_observation') WHERE name='labelset_id')`).Scan(&normalized); err != nil {
		return nil, 0, err
	}
	c.FrozenSource = c.FrozenSource && normalized
	rows, err := tx.Query(`SELECT query_id,predicate FROM label_scan_query WHERE metric_id=? AND split=0 ORDER BY query_id`, c.ID)
	if err != nil {
		return nil, 0, err
	}
	type leaf struct {
		id, predicate string
		query         databaseSampleQuery
	}
	var leaves []leaf
	for rows.Next() {
		var l leaf
		if err := rows.Scan(&l.id, &l.predicate); err != nil {
			rows.Close()
			return nil, 0, err
		}
		leaves = append(leaves, l)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, 0, err
	}
	count := 0
	for i := range leaves {
		l := &leaves[i]
		selector := c.Metric
		if l.predicate != "" {
			selector += "{" + compactLabelPredicate(l.predicate) + "}"
		}
		q, valid, err := readDatabaseLabelQuery(tx, c, l.id, selector, "inventory_labels", start, end)
		// Successful requests from before URL compaction retain their literal
		// expression in the durable ledger and are never reissued on resume.
		if err == nil && !valid && l.predicate != "" && compactLabelPredicate(l.predicate) != l.predicate {
			q, valid, err = readDatabaseLabelQuery(tx, c, l.id, c.Metric+"{"+l.predicate+"}", "inventory_labels", start, end)
		}
		if err != nil || !valid {
			return nil, 0, err
		}
		l.query = q
		var n int
		if err := tx.QueryRow(`SELECT count(*) FROM (SELECT 1 FROM label_scan_observation WHERE attempt_id=? LIMIT ?)`, q.Attempt, limit-count+1).Scan(&n); err != nil {
			return nil, 0, err
		}
		count += n
		if count > limit {
			return nil, 0, nil
		}
	}
	if len(leaves) == 0 {
		return nil, 0, nil
	}
	e := &databaseSampleExperiment{Workspace: c.Workspace, Metric: c.Metric, Selector: c.Metric, Window: "Full normal run", Start: start, End: end, Minutes: (end - start) / 60, SelectionReason: c.SelectionReason}
	identities := make([]databaseLabelIdentity, 0, count)
	dictionary := []databaseLabelDictionary{{}}
	names := map[string]uint32{}
	seen := make(map[string]struct{}, count)
	groups := map[string]int64{}
	var total int64
	textBytes, identityBytes := 0, 0
	for _, l := range leaves {
		valid, err := streamDatabaseLabelRows(ctx, tx, *l.query.Attempt, normalized, func(labels map[string]string, at float64, samples int64) bool {
			if at != end || samples <= 0 || samples > math.MaxInt64-total || len(identities) >= count {
				return false
			}
			ids := make([][2]uint32, 0, len(labels))
			for name, value := range labels {
				id, ok := names[name]
				if !ok {
					id = uint32(len(dictionary))
					names[name] = id
					dictionary = append(dictionary, databaseLabelDictionary{Name: name, Values: []string{""}, Index: map[string]uint32{}})
					textBytes += len(name)
				}
				d := &dictionary[id]
				v, ok := d.Index[value]
				if !ok {
					v = uint32(len(d.Values))
					d.Index[value] = v
					d.Values = append(d.Values, value)
					textBytes += len(value)
				}
				ids = append(ids, [2]uint32{id, v})
			}
			if textBytes > 32<<20 || len(dictionary) > 129 {
				return false
			}
			sort.Slice(ids, func(i, j int) bool { return ids[i][0] < ids[j][0] })
			key := make([]byte, 0, 8*len(ids))
			for _, pair := range ids {
				key = binary.LittleEndian.AppendUint32(key, pair[0])
				key = binary.LittleEndian.AppendUint32(key, pair[1])
			}
			identity := string(key)
			identityBytes += len(identity)
			if identityBytes > 128<<20 {
				return false
			}
			if _, ok := seen[identity]; ok {
				return false
			}
			seen[identity] = struct{}{}
			identities = append(identities, databaseLabelIdentity{identity, samples})
			total += samples
			groups[databaseLabelSourceGroup(labels)] += samples
			return len(groups) <= 10000
		})
		if err != nil || !valid {
			return nil, count, err
		}
	}
	if len(identities) != count || (c.Expected.Valid && total != c.Expected.Int64) {
		return nil, count, nil
	}
	seen = nil
	e.Full = databaseSampleQuery{ID: c.Root, Kind: "inventory_labels", State: "accepted exhaustive leaves", Samples: &total, Series: &count, Rate: finiteSum(float64(total) / e.Minutes)}
	if len(leaves) == 1 {
		e.Full = leaves[0].query
		e.Full.Samples = &total
		e.Full.Series = &count
		e.Full.Rate = finiteSum(float64(total) / e.Minutes)
	}
	var valid bool
	e.Grouped, valid, err = readDatabaseLabelQuery(tx, c, c.Source, c.Metric, "run", start, end)
	if err != nil {
		return nil, count, err
	}
	e.Status = "Exhaustive full-run labels; independent grouped comparison unavailable."
	if valid && c.Expected.Valid {
		// Build only the small grouped comparator, never a full raw-label model.
		query := `SELECT o.labelset_id,o.timestamp,o.value,o.integer_value,o.invalid_value,n.name,v.value
 FROM observation o LEFT JOIN labelset_member lm ON lm.labelset_id=o.labelset_id
 LEFT JOIN label_name n ON n.id=lm.name_id LEFT JOIN label_value v ON v.id=lm.value_id
 WHERE o.attempt_id=? ORDER BY o.labelset_id,n.name`
		args := []any{e.Grouped.Attempt}
		if c.FrozenSource {
			query = `SELECT g.group_id,?,g.samples,CASE WHEN ls.id IS NOT NULL THEN g.samples END,NULL,n.name,v.value
 FROM label_scan_expected_group g LEFT JOIN labelset ls ON ls.id=g.group_id
 LEFT JOIN labelset_member lm ON lm.labelset_id=g.group_id
 LEFT JOIN label_name n ON n.id=lm.name_id LEFT JOIN label_value v ON v.id=lm.value_id
 WHERE g.metric_id=? ORDER BY g.group_id,n.name`
			args = []any{end, c.ID}
		}
		rows, err := tx.Query(query, args...)
		if err != nil {
			return nil, count, err
		}
		var grouped []databaseSampleRow
		var last int64 = -1
		var sum int64
		for rows.Next() {
			if err := ctx.Err(); err != nil {
				rows.Close()
				return nil, count, err
			}
			var id int64
			var at float64
			var value sql.NullFloat64
			var samples sql.NullInt64
			var bad, name, label sql.NullString
			if err := rows.Scan(&id, &at, &value, &samples, &bad, &name, &label); err != nil {
				rows.Close()
				return nil, count, err
			}
			if at != end {
				valid = false
				break
			}
			if id != last {
				if at != end || !value.Valid || !samples.Valid || bad.Valid || samples.Int64 <= 0 || value.Float64 != float64(samples.Int64) || samples.Int64 > math.MaxInt64-sum || len(grouped) >= 10000 {
					valid = false
					break
				}
				sum += samples.Int64
				grouped = append(grouped, databaseSampleRow{Labels: map[string]string{}, Samples: samples.Int64})
				last = id
			}
			if name.Valid {
				if !label.Valid || (c.FrozenSource && (!strings.Contains(","+scanGrouping+",", ","+name.String+",") || label.String == "" || label.String != strings.ToLower(label.String))) {
					valid = false
					break
				}
				grouped[len(grouped)-1].Labels[name.String] = label.String
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, count, err
		}
		if valid {
			compare := map[string]int64{}
			for _, row := range grouped {
				compare[databaseLabelSourceGroup(row.Labels)] += row.Samples
			}
			n := len(grouped)
			e.Grouped.Samples = &sum
			e.Grouped.Series = &n
			e.Grouped.Rate = finiteSum(float64(sum) / e.Minutes)
			e.Matched = sum == total && reflect.DeepEqual(compare, groups)
			e.Status = "Grouped/full totals or source groups disagree. Measurements remain independent."
			if e.Matched {
				e.Status = "Matched: grouped and full-label sample totals agree, including every source group."
			}
			if c.FrozenSource {
				e.Grouped.State = "captured grouped source"
				e.Status = "Captured grouped source and full-label totals or source groups disagree. Measurements remain independent."
				if e.Matched {
					e.Status = "Matched: full-label accounting matches captured grouped source, including every source group."
				}
			}
		}
	}
	e.Labels, e.LabelPairs, err = summarizeDatabaseLabelIdentities(ctx, identities, dictionary, total, e.Minutes)
	if err != nil {
		return nil, count, err
	}
	e.LabelCount = len(dictionary) - 1
	e.OmittedLabels = e.LabelCount - len(e.Labels)
	points, err := readDatabaseSamplePlatform(tx, c.WorkspaceID, start, end)
	if err != nil {
		return nil, count, err
	}
	amw := weightedDatabaseEventRate(points, start, end)
	e.AMWMean, e.AMWPeak = amw.Mean, amw.Peak
	e.CoveredMinutes, e.ExpectedMinutes = amw.CoveredMinutes, amw.ExpectedMinutes
	if e.Matched && e.AMWMean != nil && *e.AMWMean > 0 {
		e.AMWShare = finiteSum(*e.Full.Rate / *e.AMWMean * 100)
	}
	return e, count, nil
}

// PromQL grouping treats an empty source label as absent. This normalization is
// only for comparison keys; full label identities and distributions retain it.
func databaseLabelSourceGroup(labels map[string]string) string {
	group := map[string]string{}
	for _, name := range strings.Split(scanGrouping, ",") {
		if value := labels[name]; value != "" {
			group[name] = value
		}
	}
	b, _ := json.Marshal(group)
	return string(b)
}

// Stream one identity at a time using primary-key lookups on accepted attempts
// and labelset membership. No compatibility JSON view or global dictionary scan
// is involved. The legacy checkpoint uses the same canonical case convention.
func streamDatabaseLabelRows(ctx context.Context, tx databaseReader, attempt int64, normalized bool, visit func(map[string]string, float64, int64) bool) (bool, error) {
	query := `SELECT labels_json,timestamp,samples FROM label_scan_observation WHERE attempt_id=?`
	if normalized {
		query = `SELECT o.labelset_id,o.timestamp,o.samples,n.name,v.value
 FROM label_scan_observation o LEFT JOIN labelset_member lm ON lm.labelset_id=o.labelset_id
 LEFT JOIN label_name n ON n.id=lm.name_id LEFT JOIN label_value v ON v.id=lm.value_id
 WHERE o.attempt_id=? ORDER BY o.labelset_id,o.timestamp,lm.name_id`
	}
	rows, err := tx.Query(query, attempt)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	var labels map[string]string
	var last int64
	var at float64
	var samples int64
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if !normalized {
			var raw string
			if err := rows.Scan(&raw, &at, &samples); err != nil {
				return false, err
			}
			if len(raw) > 65536 || json.Unmarshal([]byte(raw), &labels) != nil || labels == nil {
				return false, nil
			}
			canonical := make(map[string]string, len(labels))
			for name, value := range labels {
				name = strings.ToLower(name)
				if _, exists := canonical[name]; exists {
					return false, nil
				}
				canonical[name] = strings.ToLower(value)
			}
			if len(canonical) > 128 || !visit(canonical, at, samples) {
				return false, nil
			}
			labels = nil
			continue
		}
		var id, weight int64
		var timestamp float64
		var name, value sql.NullString
		if err := rows.Scan(&id, &timestamp, &weight, &name, &value); err != nil {
			return false, err
		}
		if labels == nil || id != last || timestamp != at {
			if labels != nil && !visit(labels, at, samples) {
				return false, nil
			}
			labels = map[string]string{}
			last, at, samples = id, timestamp, weight
		}
		if weight != samples {
			return false, nil
		}
		if name.Valid {
			if !value.Valid || name.String != strings.ToLower(name.String) || value.String != strings.ToLower(value.String) {
				return false, nil
			}
			if _, exists := labels[name.String]; exists {
				return false, nil
			}
			labels[name.String] = value.String
			if len(labels) > 128 {
				return false, nil
			}
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return labels == nil || visit(labels, at, samples), nil
}

// Fixed-width dictionary IDs are an exact identity, not a probabilistic hash.
// Zero denotes absence, distinct from an interned explicit empty string.
func projectDatabaseLabelIdentities(ctx context.Context, rows []databaseLabelIdentity, first, second uint32) (int, int64, map[[2]uint32]*databaseSampleValue, error) {
	bases := make(map[string]int64, len(rows))
	values := map[[2]uint32]*databaseSampleValue{}
	buffer := make([]byte, 0, 1024)
	for i, row := range rows {
		if i%1024 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, 0, nil, err
			}
		}
		buffer = buffer[:0]
		var tuple [2]uint32
		for offset := 0; offset < len(row.Key); offset += 8 {
			id := binary.LittleEndian.Uint32([]byte(row.Key[offset : offset+4]))
			switch id {
			case first:
				tuple[0] = binary.LittleEndian.Uint32([]byte(row.Key[offset+4 : offset+8]))
			case second:
				tuple[1] = binary.LittleEndian.Uint32([]byte(row.Key[offset+4 : offset+8]))
			default:
				buffer = append(buffer, row.Key[offset:offset+8]...)
			}
		}
		key := string(buffer)
		if row.Samples > bases[key] {
			bases[key] = row.Samples
		}
		v := values[tuple]
		if v == nil {
			v = &databaseSampleValue{}
			values[tuple] = v
		}
		v.Series++
		v.Samples += row.Samples
	}
	var strongest int64
	for _, v := range bases {
		strongest += v
	}
	return len(bases), strongest, values, nil
}

func summarizeDatabaseLabelIdentities(ctx context.Context, rows []databaseLabelIdentity, dict []databaseLabelDictionary, total int64, minutes float64) ([]databaseSampleLabel, []databaseSampleLabelPair, error) {
	var labels []databaseSampleLabel
	for id := 1; id < len(dict); id++ {
		d := dict[id]
		n, strongest, values, err := projectDatabaseLabelIdentities(ctx, rows, uint32(id), 0)
		if err != nil {
			return nil, nil, err
		}
		l := databaseSampleLabel{Name: d.Name, Distinct: len(d.Values) - 1, Physical: len(rows), Samples: total, BaseIdentities: &n}
		reduction := len(rows) - n
		l.Reduction = &reduction
		if n > 0 {
			l.CardinalityMultiplicity = finiteSum(float64(len(rows)) / float64(n))
		}
		if d.Name == "prometheus_replica" && strongest > 0 {
			l.SampleMultiplicity = finiteSum(float64(total) / float64(strongest))
		}
		var frequencies []databaseSampleCombination
		for ids, v := range values {
			item := databaseSampleCombination{Series: v.Series, Samples: v.Samples}
			if ids[0] != 0 {
				value := d.Values[ids[0]]
				item.Values[0] = &value
			}
			frequencies = append(frequencies, item)
		}
		for _, v := range finishDatabaseLabelValues(frequencies, total, minutes) {
			l.Values = append(l.Values, databaseSampleValue{Value: v.Values[0], Series: v.Series, Samples: v.Samples, Rate: v.Rate, Share: v.Share, Remainder: v.Remainder})
		}
		labels = append(labels, l)
	}
	sort.Slice(labels, func(i, j int) bool {
		if *labels[i].Reduction != *labels[j].Reduction {
			return *labels[i].Reduction > *labels[j].Reduction
		}
		if labels[i].Distinct != labels[j].Distinct {
			return labels[i].Distinct > labels[j].Distinct
		}
		return labels[i].Name < labels[j].Name
	})
	if len(labels) > 6 {
		for _, l := range labels[6:] {
			if l.Name == "prometheus_replica" {
				labels[5] = l
				break
			}
		}
		labels = labels[:6]
	}
	var selected []databaseSampleLabel
	for _, l := range labels {
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
	if len(selected) > 3 {
		selected = selected[:3]
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].Name < selected[j].Name })
	ids := map[string]uint32{}
	for id, d := range dict {
		ids[d.Name] = uint32(id)
	}
	var pairs []databaseSampleLabelPair
	for i, first := range selected {
		for _, second := range selected[i+1:] {
			a, b := ids[first.Name], ids[second.Name]
			n, _, values, err := projectDatabaseLabelIdentities(ctx, rows, a, b)
			if err != nil {
				return nil, nil, err
			}
			reduction := len(rows) - n
			p := databaseSampleLabelPair{Names: [2]string{first.Name, second.Name}, Distinct: len(values), BaseIdentities: &n, Reduction: &reduction}
			if n > 0 {
				p.CardinalityMultiplicity = finiteSum(float64(len(rows)) / float64(n))
			}
			for tuple, v := range values {
				item := databaseSampleCombination{Series: v.Series, Samples: v.Samples}
				for j, id := range []uint32{a, b} {
					if tuple[j] != 0 {
						value := dict[id].Values[tuple[j]]
						item.Values[j] = &value
					}
				}
				p.Values = append(p.Values, item)
			}
			p.Values = finishDatabaseLabelValues(p.Values, total, minutes)
			pairs = append(pairs, p)
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if *pairs[i].Reduction != *pairs[j].Reduction {
			return *pairs[i].Reduction > *pairs[j].Reduction
		}
		if pairs[i].Names[0] != pairs[j].Names[0] {
			return pairs[i].Names[0] < pairs[j].Names[0]
		}
		return pairs[i].Names[1] < pairs[j].Names[1]
	})
	return labels, pairs, nil
}

func finishDatabaseLabelValues(values []databaseSampleCombination, total int64, minutes float64) []databaseSampleCombination {
	sort.Slice(values, func(i, j int) bool {
		for k, a := range values[i].Values {
			b := values[j].Values[k]
			if a == nil {
				if b != nil {
					return true
				}
				continue
			}
			if b == nil {
				return false
			}
			if *a != *b {
				return *a < *b
			}
		}
		return false
	})
	series, samples := make([]int, len(values)), make([]int64, len(values))
	for i, v := range values {
		series[i], samples[i] = v.Series, v.Samples
	}
	order, membership := databaseLabelRankOrder(series, samples, 8)
	result := make([]databaseSampleCombination, 0, len(order)+1)
	for _, i := range order {
		result = append(result, values[i])
	}
	other := databaseSampleCombination{}
	for i, v := range values {
		if membership[i] == 0 {
			other.Remainder++
			other.Series += v.Series
			other.Samples += v.Samples
		}
	}
	if other.Remainder > 0 {
		result = append(result, other)
	}
	for i := range result {
		v := &result[i]
		v.Rate = float64(v.Samples) / minutes
		if total > 0 {
			v.Share = finiteSum(float64(v.Samples) / float64(total) * 100)
		}
	}
	return result
}

func databaseLabelBudgetFlags(tx databaseReader, r *databaseReport, b *databaseBudget) error {
	var present bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE name='label_scan')`).Scan(&present); err != nil || !present {
		return err
	}
	// Collected means all leaf requests were accepted. It does not assert a
	// reconciled summary; omitted, mismatched and oversized metrics stay distinct.
	rows, err := tx.Query(`SELECT c.metric_id FROM label_scan_metric c WHERE c.skipped_zero=0
 AND EXISTS(SELECT 1 FROM label_scan_query l WHERE l.metric_id=c.metric_id AND l.split=0)
 AND NOT EXISTS(SELECT 1 FROM label_scan_query l JOIN query q ON q.id=l.query_id
 WHERE l.metric_id=c.metric_id AND l.split=0 AND (q.state<>'succeeded' OR q.accepted_attempt_id IS NULL))`)
	if err != nil {
		return err
	}
	defer rows.Close()
	collected := map[int]bool{}
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return err
		}
		collected[id] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	included := map[string]bool{}
	if r.SampleDetail != nil {
		for _, e := range r.SampleDetail.Experiments {
			if len(e.Labels) > 0 {
				included[e.Workspace+"\x00"+e.Metric] = true
			}
		}
	}
	workspace := map[int]string{}
	for _, m := range r.Metrics {
		workspace[m.ID] = m.WorkspaceName
	}
	for i := range b.Metrics {
		m := &b.Metrics[i]
		m.LabelCollected = collected[m.ID]
		m.LabelSummaryIncluded = included[workspace[m.ID]+"\x00"+b.Strings[m.Name]]
		m.LabelCollected = m.LabelCollected || m.LabelSummaryIncluded
	}
	return nil
}
