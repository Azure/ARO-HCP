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
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"math"
	"net/url"
	"path/filepath"
	"sort"

	_ "modernc.org/sqlite"
)

type databaseMeasure struct {
	Value                                       *float64
	State                                       string
	LowerBound                                  *float64
	Leaves, Measured, Blocked, Pending, Invalid int
}

type databaseMetric struct {
	ID, Workspace        int
	Name, WorkspaceName  string
	Before, End, Samples databaseMeasure
	Delta, Rate          *float64
	Sources              []databaseSource
	LowerRate            *float64
	Unresolved           bool
	AMWShare             *float64
}

type databaseSource struct {
	Cluster, Job, Namespace, HCP      *string
	Groups                            int
	Remainder                         bool
	Before, End, Delta, Samples, Rate *float64
}

type databaseWorkspace struct {
	ID                                          int
	Name, ARMID, Blocked                        string
	Metrics, BeforeCount, EndCount, SampleCount int
	Before, End, Samples                        float64
	ActiveBefore, ActiveEnd, ActiveDelta        *float64
	ActiveLimitEnd, ActiveUtilization           *float64
	ActiveTimeline                              []databaseEventPoint
	Timeline                                    []renderPoint
	Chart                                       renderChart
	FullyRanked                                 int
	FullyRankedEnd                              float64
	PlatformGap                                 *float64
	Reconciliation                              []databaseReconciliation
	Events                                      databaseEvents
}

type databaseReconciliation struct {
	Period                                                  string
	Reference, Complete, Partial, Residual, Over            *float64
	CompleteShare, PartialShare, ResidualShare              *float64
	CompleteWidth, PartialWidth, GapWidth, GapX, ReferenceX float64
	HasScale                                                bool
}

// An AMW reference is not a strict bound on PromQL sampled-series estimates.
// Keep the signed difference and use a larger plotting scale for over-accounting,
// without changing the AMW denominator of any displayed percentage.
func reconcileDatabasePeriod(period string, reference *float64, measures []databaseMeasure) databaseReconciliation {
	r := databaseReconciliation{Period: period, Reference: reference}
	complete, partial := 0.0, 0.0
	completeKnown, partialKnown := false, false
	for _, m := range measures {
		if m.Value != nil {
			complete += *m.Value
			completeKnown = true
		} else if m.LowerBound != nil {
			partial += *m.LowerBound
			partialKnown = true
		}
	}
	if completeKnown {
		r.Complete = finiteSum(complete)
	}
	if partialKnown {
		r.Partial = finiteSum(partial)
	}
	if reference == nil || (completeKnown && r.Complete == nil) || (partialKnown && r.Partial == nil) {
		return r
	}
	r.Residual = finiteSum(*reference - complete - partial)
	if r.Residual == nil {
		return r
	}
	if *reference > 0 {
		if r.Complete != nil {
			r.CompleteShare = finiteSum(complete / *reference * 100)
		}
		if r.Partial != nil {
			r.PartialShare = finiteSum(partial / *reference * 100)
		}
		r.ResidualShare = finiteSum(*r.Residual / *reference * 100)
	}
	if *r.Residual < 0 {
		r.Over = finiteSum(-*r.Residual)
	}
	scale := math.Max(*reference, complete+partial)
	if scale > 0 && finiteSum(scale) != nil {
		r.HasScale = true
		r.CompleteWidth = complete / scale * 600
		r.PartialWidth = partial / scale * 600
		r.GapX = r.CompleteWidth + r.PartialWidth
		r.GapWidth = math.Max(0, *r.Residual) / scale * 600
		r.ReferenceX = *reference / scale * 600
	}
	return r
}

func databasePercent(v *float64) string {
	if v == nil {
		return "Unavailable"
	}
	return fmt.Sprintf("%.2f%%", *v)
}

type databaseState struct {
	Role, Kind, State, Classification string
	Count                             int
}

type databaseReport struct {
	Job, Build, State, Planning                   string
	Start, End                                    float64
	Metrics                                       []*databaseMetric
	Workspaces                                    []*databaseWorkspace
	States                                        []databaseState
	Expected, Succeeded, Valid, CachedInventories int
	SampleCount                                   int
	UnresolvedMetrics, UnresolvedWindows          int
	Before, After, Delta                          *float64
	Data                                          template.JS
	EventsData                                    template.JS
	SampleDetail                                  *databaseSampleDetail
	Budget                                        *databaseBudget
}

// RenderDatabase writes a standalone, offline report from a schema-1 scan database.
// It opens SQLite read-only and reads one consistent snapshot, including during a
// live scan. Only normalized accepted observations are used; raw responses and
// full physical-series inventories are never reconstructed or embedded.
func RenderDatabase(w io.Writer, path string) error {
	return renderDatabase(context.Background(), w, path, false)
}

// RenderDatabaseContext bounds snapshot queries, CPU loops and HTML writes by ctx.
// Oversized reports are refused, never truncated. RenderDatabase remains available
// for offline rendering without the CI size limits.
func RenderDatabaseContext(ctx context.Context, w io.Writer, path string) error {
	return renderDatabase(ctx, w, path, true)
}

// DatabaseReportMaxBytes bounds CI output and its subsequent in-memory embedding.
const DatabaseReportMaxBytes = 16 << 20

func renderDatabase(ctx context.Context, w io.Writer, path string, bounded bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if path == "" {
		return fmt.Errorf("database path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	u := url.URL{Scheme: "file", Path: abs}
	u.RawQuery = url.Values{"mode": {"ro"}, "_pragma": {"query_only(1)", "busy_timeout(10000)"}}.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return fmt.Errorf("open report snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	reader := databaseContextReader{ctx: ctx, tx: tx}
	if bounded {
		// Bound models before allocating them or entering non-interruptible sort /
		// JSON operations. LIMIT makes row-count guards cheap on large archives.
		for _, limit := range []struct {
			table string
			rows  int
		}{
			{"workspace", 4}, {"metric", 10000}, {"query", 100000},
			{"observation", 500000}, {"platform_observation", 100000},
			{"labelset_member", 1000000}, {"label_name", 256},
		} {
			var n int
			if err := reader.QueryRow(fmt.Sprintf("SELECT count(*) FROM (SELECT 1 FROM %s LIMIT ?)", limit.table), limit.rows+1).Scan(&n); err != nil {
				return err
			}
			if n > limit.rows {
				return fmt.Errorf("CI report limit exceeded: %s has more than %d rows; database preserved for offline rendering", limit.table, limit.rows)
			}
		}
		var bytes int64
		if err := reader.QueryRow(`SELECT coalesce(sum(length(CAST(value AS BLOB))),0) FROM label_value`).Scan(&bytes); err != nil {
			return err
		}
		if bytes > DatabaseReportMaxBytes {
			return fmt.Errorf("CI report label text exceeds %d bytes; database preserved for offline rendering", DatabaseReportMaxBytes)
		}
		// Interned labels can be repeated in source and enrichment models. Bound
		// their expanded size as well, not just the dictionary on disk.
		if err := reader.QueryRow(`SELECT coalesce(sum(length(CAST(v.value AS BLOB))),0)
 FROM observation o JOIN labelset_member lm ON lm.labelset_id=o.labelset_id
 JOIN label_value v ON v.id=lm.value_id`).Scan(&bytes); err != nil {
			return err
		}
		if bytes > 32<<20 {
			return fmt.Errorf("CI report expanded labels exceed 32 MiB; database preserved for offline rendering")
		}
		if err := reader.QueryRow(`SELECT coalesce(sum(length(CAST(display_name AS BLOB))),0) FROM metric`).Scan(&bytes); err != nil {
			return err
		}
		if bytes > 1<<20 {
			return fmt.Errorf("CI report metric names exceed 1 MiB; database preserved for offline rendering")
		}
		var duration float64
		if err := reader.QueryRow(`SELECT end-start FROM run WHERE id=1`).Scan(&duration); err != nil {
			return err
		}
		if duration > 12*60*60 {
			return fmt.Errorf("CI report window exceeds twelve hours; database preserved for offline rendering")
		}
		var enrichment bool
		if err := reader.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE name='enrichment_plan')`).Scan(&enrichment); err != nil {
			return err
		}
		if enrichment {
			// Label-pair analysis is substantially more expensive than ranking.
			var n int
			if err := reader.QueryRow(`SELECT count(*) FROM (SELECT 1 FROM observation LIMIT 10001)`).Scan(&n); err != nil {
				return err
			}
			if n > 10000 {
				return fmt.Errorf("CI enrichment report exceeds 10000 observations; database preserved for offline rendering")
			}
		}
	}
	r, err := readDatabaseReport(reader)
	if err != nil {
		return fmt.Errorf("read report snapshot: %w", err)
	}
	// Release the WAL snapshot before HTML generation or a potentially slow writer.
	if err := tx.Rollback(); err != nil {
		return err
	}
	t, err := template.New("budget.html").Funcs(template.FuncMap{"number": renderNumber, "utc": renderUTC, "percent": databasePercent}).ParseFS(renderTemplates,
		"templates/budget.html", "templates/budget.css", "templates/budget.js")
	if err != nil {
		return err
	}
	writer := &databaseContextWriter{ctx: ctx, Writer: w}
	if bounded {
		writer.limit = DatabaseReportMaxBytes
	}
	if err := t.ExecuteTemplate(writer, "budget.html", r); err != nil {
		return fmt.Errorf("render database report: %w", err)
	}
	return nil
}

type databaseReader interface {
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
}

type databaseContextReader struct {
	ctx context.Context
	tx  *sql.Tx
}

func (r databaseContextReader) Query(query string, args ...any) (*sql.Rows, error) {
	return r.tx.QueryContext(r.ctx, query, args...)
}

func (r databaseContextReader) QueryRow(query string, args ...any) *sql.Row {
	return r.tx.QueryRowContext(r.ctx, query, args...)
}

type databaseContextWriter struct {
	ctx context.Context
	io.Writer
	limit, written int
}

func (w *databaseContextWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	if w.limit > 0 && len(p) > w.limit-w.written {
		return 0, fmt.Errorf("CI report output exceeds %d bytes; database preserved for offline rendering", w.limit)
	}
	n, err := w.Writer.Write(p)
	w.written += n
	return n, err
}

func databaseReadCheck(reader databaseReader) error {
	if r, ok := reader.(databaseContextReader); ok {
		return r.ctx.Err()
	}
	return nil
}

func databaseMarshal(reader databaseReader, value any) ([]byte, error) {
	if err := databaseReadCheck(reader); err != nil {
		return nil, err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if err := databaseReadCheck(reader); err != nil {
		return nil, err
	}
	return data, nil
}

func readDatabaseReport(tx databaseReader) (*databaseReport, error) {
	var version int
	if err := tx.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return nil, err
	}
	if version != 1 {
		return nil, fmt.Errorf("unsupported scan schema %d (want 1)", version)
	}
	r := &databaseReport{}
	if err := tx.QueryRow(`SELECT job,build,start,end,state,planning_state FROM run WHERE id=1`).Scan(&r.Job, &r.Build, &r.Start, &r.End, &r.State, &r.Planning); err != nil {
		return nil, err
	}
	if r.Start < 0 || r.End <= r.Start || r.End > 253402300799 || finiteSum(r.End-r.Start) == nil {
		return nil, fmt.Errorf("invalid run window")
	}
	workspaces := map[int]*databaseWorkspace{}
	rows, err := tx.Query(`SELECT id,name,arm_id,blocked_reason FROM workspace ORDER BY id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		w := &databaseWorkspace{}
		if err := databaseReadCheck(tx); err != nil {
			rows.Close()
			return nil, err
		}
		if err := rows.Scan(&w.ID, &w.Name, &w.ARMID, &w.Blocked); err != nil {
			rows.Close()
			return nil, err
		}
		workspaces[w.ID] = w
		r.Workspaces = append(r.Workspaces, w)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	var partitionTable bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE type='table' AND name='query_partition')`).Scan(&partitionTable); err != nil {
		return nil, err
	}
	role := `CASE WHEN ranking=1 THEN 'ranking' ELSE 'cached' END`
	if partitionTable {
		role = `CASE WHEN ranking=1 THEN 'ranking' WHEN EXISTS(SELECT 1 FROM query_partition p WHERE p.child_id=q.id) THEN 'partition' ELSE 'cached' END`
	}
	rows, err = tx.Query(`SELECT ` + role + ` AS role,kind,state,classification,count(*) FROM query q GROUP BY role,kind,state,classification ORDER BY role,kind,state,classification`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var s databaseState
		if err := rows.Scan(&s.Role, &s.Kind, &s.State, &s.Classification, &s.Count); err != nil {
			rows.Close()
			return nil, err
		}
		r.States = append(r.States, s)
		if s.Role == "cached" && s.State == "succeeded" && (s.Kind == "inventory_before" || s.Kind == "inventory_end") {
			r.CachedInventories += s.Count
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	// A completed empty vector is measured zero. A missing/blocked parent, invalid
	// value, or unexpected evaluation poisons the entire period, not just its group.
	rows, err = tx.Query(`SELECT m.id,m.workspace_id,m.display_name,q.kind,q.state,q.classification,
 sum(CASE WHEN o.attempt_id IS NOT NULL AND
 (o.invalid_value IS NOT NULL OR o.value IS NULL OR o.value<0 OR q.evaluation_time IS NULL OR o.timestamp!=q.evaluation_time) THEN 1 ELSE 0 END),
 total(o.value),q.accepted_attempt_id
 FROM metric m LEFT JOIN query q ON q.metric_id=m.id AND q.ranking=1
 LEFT JOIN accepted_observation o ON o.query_id=q.id
 WHERE m.catalog=1 GROUP BY m.id,q.id ORDER BY m.id,q.kind`)
	if err != nil {
		return nil, err
	}
	metrics := map[int]*databaseMetric{}
	seen := map[[2]string]bool{}
	for rows.Next() {
		var id, wid, invalid int
		if err := databaseReadCheck(tx); err != nil {
			rows.Close()
			return nil, err
		}
		var name string
		var kind, state, classification sql.NullString
		var accepted sql.NullInt64
		var total float64
		if err := rows.Scan(&id, &wid, &name, &kind, &state, &classification, &invalid, &total, &accepted); err != nil {
			rows.Close()
			return nil, err
		}
		m := metrics[id]
		if m == nil {
			m = &databaseMetric{ID: id, Workspace: wid, Name: name, Before: databaseMeasure{State: "missing"}, End: databaseMeasure{State: "missing"}, Samples: databaseMeasure{State: "missing"}}
			metrics[id] = m
			r.Metrics = append(r.Metrics, m)
		}
		var target *databaseMeasure
		switch kind.String {
		case "before12h":
			target = &m.Before
		case "end12h":
			target = &m.End
		case "run":
			target = &m.Samples
		default:
			continue
		}
		key := [2]string{fmt.Sprint(id), kind.String}
		duplicate := seen[key]
		seen[key] = true
		target.State = state.String
		if classification.String != "" {
			target.State += ": " + classification.String
		}
		target.Value = nil
		if state.String == "succeeded" {
			r.Succeeded++
		}
		switch {
		case duplicate:
			target.State = "ambiguous parent queries"
		case state.String != "succeeded":
		case !accepted.Valid:
			target.State = "missing accepted attempt"
		case invalid > 0 || finiteSum(total) == nil:
			target.State = "invalid accepted values"
		default:
			target.Value = finiteSum(total)
			target.State = "accepted"
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	// Only version-4 active edges establish which partition plan is current.
	// Older databases still render complete parents, without speculative bounds.
	if partitionTable {
		var active bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM pragma_table_info('query_partition') WHERE name='active')`).Scan(&active); err != nil {
			return nil, err
		}
		if active {
			if err := readDatabaseLowerBounds(tx, metrics); err != nil {
				return nil, err
			}
		}
	}
	r.Expected = len(r.Metrics) * 3
	for _, m := range r.Metrics {
		w := workspaces[m.Workspace]
		if err := databaseReadCheck(tx); err != nil {
			return nil, err
		}
		if w == nil {
			return nil, fmt.Errorf("metric %d references missing workspace", m.ID)
		}
		m.WorkspaceName = w.Name
		w.Metrics++
		for _, period := range []*databaseMeasure{&m.Before, &m.End, &m.Samples} {
			if period.Value == nil {
				m.Unresolved = true
				r.UnresolvedWindows++
			}
		}
		if m.Unresolved {
			r.UnresolvedMetrics++
		} else {
			w.FullyRanked++
			w.FullyRankedEnd += *m.End.Value
		}
		if m.Samples.LowerBound != nil {
			m.LowerRate = finiteSum(*m.Samples.LowerBound * 60 / (r.End - r.Start))
		}
		if m.Before.Value != nil {
			w.Before += *m.Before.Value
			w.BeforeCount++
			r.Valid++
		}
		if m.End.Value != nil {
			w.End += *m.End.Value
			w.EndCount++
			r.Valid++
		}
		if m.Samples.Value != nil {
			w.Samples += *m.Samples.Value
			w.SampleCount++
			r.SampleCount++
			r.Valid++
			m.Rate = finiteSum(*m.Samples.Value * 60 / (r.End - r.Start))
		}
		if m.Before.Value != nil && m.End.Value != nil {
			m.Delta = finiteSum(*m.End.Value - *m.Before.Value)
		}
	}
	if err := readDatabaseSources(tx, r, metrics); err != nil {
		return nil, err
	}
	if err := readDatabasePlatform(tx, r, workspaces); err != nil {
		return nil, err
	}
	if err := readDatabaseEvents(tx, r, metrics); err != nil {
		return nil, err
	}
	r.SampleDetail, err = readDatabaseSampleDetail(tx)
	if err != nil {
		return nil, err
	}
	for _, w := range r.Workspaces {
		var before, end []databaseMeasure
		for _, m := range r.Metrics {
			if err := databaseReadCheck(tx); err != nil {
				return nil, err
			}
			if m.Workspace != w.ID {
				continue
			}
			before = append(before, m.Before)
			end = append(end, m.End)
			value := m.End.Value
			if value == nil {
				value = m.End.LowerBound
			}
			if w.ActiveEnd != nil && *w.ActiveEnd > 0 && value != nil {
				m.AMWShare = finiteSum(*value / *w.ActiveEnd * 100)
			}
		}
		w.Reconciliation = []databaseReconciliation{reconcileDatabasePeriod("Before the run", w.ActiveBefore, before), reconcileDatabasePeriod("At run end", w.ActiveEnd, end)}
	}
	sort.SliceStable(r.Metrics, func(i, j int) bool {
		a, b := r.Metrics[i].End.Value, r.Metrics[j].End.Value
		if a == nil {
			return false
		}
		return b == nil || *a > *b
	})
	if err := databaseReadCheck(tx); err != nil {
		return nil, err
	}
	// Intern metric names and source labels once. Source tuples contain four label
	// indexes (-1 = absent, distinct from the interned empty string), group count,
	// remainder flag, then before/end/delta/samples/rate. Decode only on selection.
	// ARM IDs appear once in the human-readable workspace inventory.
	type browserMetric struct {
		ID, Workspace                                  int
		Name                                           int
		Before, End, Delta, Samples, Rate              *float64
		Sources                                        [][11]any
		LowerBefore, LowerEnd, LowerSamples, LowerRate *float64 `json:",omitempty"`
	}
	data := struct {
		Metrics    []browserMetric
		Workspaces map[int]string
		Strings    []string
	}{Workspaces: map[int]string{}}
	strings := map[string]int{}
	intern := func(value *string) int {
		if value == nil {
			return -1
		}
		if index, ok := strings[*value]; ok {
			return index
		}
		index := len(data.Strings)
		strings[*value] = index
		data.Strings = append(data.Strings, *value)
		return index
	}
	for _, w := range r.Workspaces {
		data.Workspaces[w.ID] = w.Name
	}
	for _, m := range r.Metrics {
		if err := databaseReadCheck(tx); err != nil {
			return nil, err
		}
		metric := browserMetric{ID: m.ID, Workspace: m.Workspace, Name: intern(&m.Name), Before: m.Before.Value, End: m.End.Value, Delta: m.Delta, Samples: m.Samples.Value, Rate: m.Rate}
		metric.LowerBefore, metric.LowerEnd, metric.LowerSamples, metric.LowerRate = m.Before.LowerBound, m.End.LowerBound, m.Samples.LowerBound, m.LowerRate
		for _, s := range m.Sources {
			metric.Sources = append(metric.Sources, [11]any{intern(s.Cluster), intern(s.Job), intern(s.Namespace), intern(s.HCP), s.Groups, s.Remainder, s.Before, s.End, s.Delta, s.Samples, s.Rate})
		}
		data.Metrics = append(data.Metrics, metric)
	}
	b, err := databaseMarshal(tx, data)
	if err != nil {
		return nil, err
	}
	// encoding/json's HTML escaping protects script terminators and Unicode separators.
	r.Data = template.JS(b)
	r.Budget, err = readDatabaseBudget(tx, r, metrics)
	if err != nil {
		return nil, err
	}
	return r, nil
}

func readDatabaseLowerBounds(tx databaseReader, metrics map[int]*databaseMetric) error {
	// Walk through accepted internal nodes as well as unfinished ones. Only the
	// terminal frontier contributes, never both an assembled node and descendants.
	// UNION (not UNION ALL) also makes accidental cycles terminate defensively.
	rows, err := tx.Query(`WITH RECURSIVE tree(root,id) AS (
 SELECT q.id,q.id FROM query q JOIN metric m ON m.id=q.metric_id WHERE q.ranking=1 AND m.catalog=1
 AND EXISTS(SELECT 1 FROM query_partition p WHERE p.parent_id=q.id AND p.active=1)
 UNION SELECT t.root,p.child_id FROM tree t JOIN query_partition p ON p.parent_id=t.id WHERE p.active=1
 ) SELECT root.metric_id,root.kind,q.state,q.accepted_attempt_id,total(o.value),
 sum(CASE WHEN o.attempt_id IS NOT NULL AND (o.invalid_value IS NOT NULL OR o.value IS NULL OR o.value<0
 OR q.evaluation_time IS NULL OR o.timestamp!=q.evaluation_time) THEN 1 ELSE 0 END)
 FROM tree t JOIN query root ON root.id=t.root JOIN query q ON q.id=t.id
 LEFT JOIN accepted_observation o ON o.query_id=q.id
 WHERE t.id!=t.root AND NOT EXISTS(SELECT 1 FROM query_partition p WHERE p.parent_id=t.id AND p.active=1)
 GROUP BY t.root,q.id ORDER BY t.root,q.id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, invalid int
		if err := databaseReadCheck(tx); err != nil {
			return err
		}
		var kind, state string
		var accepted sql.NullInt64
		var total float64
		if err := rows.Scan(&id, &kind, &state, &accepted, &total, &invalid); err != nil {
			return err
		}
		m := metrics[id]
		if m == nil {
			continue
		}
		var p *databaseMeasure
		switch kind {
		case "before12h":
			p = &m.Before
		case "end12h":
			p = &m.End
		case "run":
			p = &m.Samples
		default:
			continue
		}
		if p.Value != nil || p.State == "ambiguous parent queries" {
			continue
		}
		p.Leaves++
		switch {
		case state == "succeeded" && accepted.Valid && invalid == 0 && finiteSum(total) != nil:
			if p.Measured == 0 {
				p.LowerBound = finiteSum(total)
			} else if p.LowerBound != nil {
				p.LowerBound = finiteSum(*p.LowerBound + total)
			}
			p.Measured++
		case state == "succeeded":
			p.Invalid++
		case state == "blocked":
			p.Blocked++
		default:
			p.Pending++
		}
	}
	return rows.Err()
}

func readDatabaseSources(tx databaseReader, r *databaseReport, metrics map[int]*databaseMetric) error {
	// Collapse prometheus replicas/sources before ranking. SQL returns at most 51
	// rows per metric, so Go and HTML memory do not scale with physical labelsets.
	rows, err := tx.Query(`WITH labels AS MATERIALIZED (
 SELECT lm.labelset_id,
 max(CASE WHEN n.name='cluster' THEN v.value END) cluster,
 max(CASE WHEN n.name='job' THEN v.value END) job,
 max(CASE WHEN n.name='namespace' THEN v.value END) namespace,
 max(CASE WHEN n.name='hostedcontrolplane' THEN v.value END) hcp
 FROM labelset_member lm JOIN label_name n ON n.id=lm.name_id JOIN label_value v ON v.id=lm.value_id
 WHERE n.name IN ('cluster','job','namespace','hostedcontrolplane') GROUP BY lm.labelset_id
 ), grouped AS (
 SELECT o.metric_id,l.cluster,l.job,l.namespace,l.hcp,
 total(CASE WHEN o.kind='before12h' THEN o.value END) b,
 total(CASE WHEN o.kind='end12h' THEN o.value END) e,
 total(CASE WHEN o.kind='run' THEN o.value END) s
 FROM accepted_observation o JOIN query q ON q.id=o.query_id JOIN metric m ON m.id=o.metric_id
 LEFT JOIN labels l ON l.labelset_id=o.labelset_id
 WHERE q.ranking=1 AND m.catalog=1 GROUP BY o.metric_id,l.cluster,l.job,l.namespace,l.hcp
 ), ranked AS (
 SELECT *,row_number() OVER (PARTITION BY metric_id ORDER BY max(b,e,s*60/?) DESC,cluster,job,namespace,hcp) rank FROM grouped
 ) SELECT metric_id,CASE WHEN rank<=50 THEN cluster END,
 CASE WHEN rank<=50 THEN job END,CASE WHEN rank<=50 THEN namespace END,
 CASE WHEN rank<=50 THEN hcp END,count(*),min(rank)>50,total(b),total(e),total(s)
 FROM ranked GROUP BY metric_id,CASE WHEN rank<=50 THEN rank ELSE 51 END ORDER BY metric_id,min(rank)`, r.End-r.Start)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int
		if err := databaseReadCheck(tx); err != nil {
			return err
		}
		var s databaseSource
		var before, end, samples float64
		if err := rows.Scan(&id, &s.Cluster, &s.Job, &s.Namespace, &s.HCP, &s.Groups, &s.Remainder, &before, &end, &samples); err != nil {
			return err
		}
		m := metrics[id]
		if m == nil {
			continue
		}
		// Absent groups imply zero only when their entire parent query is accepted.
		if m.Before.Value != nil {
			s.Before = finiteSum(before)
		}
		if m.End.Value != nil {
			s.End = finiteSum(end)
		}
		if s.Before != nil && s.End != nil {
			s.Delta = finiteSum(end - before)
		}
		if m.Samples.Value != nil {
			s.Samples = finiteSum(samples)
			s.Rate = finiteSum(samples * 60 / (r.End - r.Start))
		}
		m.Sources = append(m.Sources, s)
	}
	return rows.Err()
}

func readDatabasePlatform(tx databaseReader, r *databaseReport, workspaces map[int]*databaseWorkspace) error {
	// ActiveTimeSeries is authoritative. Multiple physical series/evidence at one
	// minute are ambiguous, not additive; never sum workspace quota measurements.
	rows, err := tx.Query(`WITH physical AS (
 SELECT e.workspace_id,lower(p.metric) metric,count(DISTINCT p.labelset_id) n
 FROM platform_observation p JOIN evidence e ON e.id=p.evidence_id
 WHERE e.ok=1 AND lower(p.metric) IN ('activetimeseries','activetimeserieslimit')
 GROUP BY e.workspace_id,lower(p.metric)
 ) SELECT e.workspace_id,p.timestamp,lower(p.metric),count(*),max(s.n),
 sum(CASE WHEN p.invalid_value IS NOT NULL OR p.value IS NULL OR p.value<0 THEN 1 ELSE 0 END),max(p.value)
	 FROM platform_observation p JOIN evidence e ON e.id=p.evidence_id
	 JOIN physical s ON s.workspace_id=e.workspace_id AND s.metric=lower(p.metric)
	 WHERE e.ok=1
	 GROUP BY e.workspace_id,p.timestamp,lower(p.metric) ORDER BY e.workspace_id,p.timestamp,lower(p.metric)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var wid, count, physical, invalid int
		if err := databaseReadCheck(tx); err != nil {
			return err
		}
		var metric string
		var at float64
		var value sql.NullFloat64
		if err := rows.Scan(&wid, &at, &metric, &count, &physical, &invalid, &value); err != nil {
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
		points := &w.ActiveTimeline
		if n := len(*points); n == 0 || (*points)[n-1].Time != at {
			if n > 0 && at-(*points)[n-1].Time > 60 {
				*points = append(*points, databaseEventPoint{Time: (*points)[n-1].Time + 60})
			}
			*points = append(*points, databaseEventPoint{Time: at})
		}
		p := &(*points)[len(*points)-1]
		if metric == "activetimeserieslimit" {
			if v != nil && *v > 0 {
				p.Limit = v
			}
			if at <= r.End && r.End-at < 60 {
				w.ActiveLimitEnd = p.Limit
			}
			continue
		}
		p.Usage = v
		if at <= r.Start && r.Start-at < 60 {
			w.ActiveBefore = v
		}
		if at <= r.End && r.End-at < 60 {
			w.ActiveEnd = v
		}
		if n := len(w.Timeline); n > 0 && at-w.Timeline[n-1].Time > 60 {
			w.Timeline = append(w.Timeline, renderPoint{Time: w.Timeline[n-1].Time + 60})
		}
		w.Timeline = append(w.Timeline, renderPoint{Time: at, Value: v})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	before, end := 0.0, 0.0
	beforeOK, endOK := len(r.Workspaces) > 0, len(r.Workspaces) > 0
	for _, w := range r.Workspaces {
		if err := databaseReadCheck(tx); err != nil {
			return err
		}
		if w.ActiveEnd != nil && w.ActiveLimitEnd != nil {
			w.ActiveUtilization = finiteSum(*w.ActiveEnd / *w.ActiveLimitEnd * 100)
		}
		if w.ActiveEnd != nil && w.FullyRanked > 0 {
			w.PlatformGap = finiteSum(*w.ActiveEnd - w.FullyRankedEnd)
		}
		if w.ActiveBefore == nil {
			beforeOK = false
		} else {
			before += *w.ActiveBefore
		}
		if w.ActiveEnd == nil {
			endOK = false
		} else {
			end += *w.ActiveEnd
		}
		if w.ActiveBefore != nil && w.ActiveEnd != nil {
			w.ActiveDelta = finiteSum(*w.ActiveEnd - *w.ActiveBefore)
		}
		start, finish := r.Start, r.End
		if len(w.Timeline) > 0 {
			start = math.Min(start, w.Timeline[0].Time)
			finish = math.Max(finish, w.Timeline[len(w.Timeline)-1].Time)
		}
		var limits []renderPoint
		for _, p := range w.ActiveTimeline {
			limits = append(limits, renderPoint{Time: p.Time, Value: p.Limit})
		}
		w.Chart = makeChart(w.Name+" / ActiveTimeSeries", []chartInput{{"Active series (one-minute maximum)", "#006f79", false, w.Timeline}, {"Same-minute limit", "#b45309", false, limits}}, start, finish, &artifactRun{Start: r.Start, End: r.End})
	}
	if beforeOK {
		r.Before = finiteSum(before)
	}
	if endOK {
		r.After = finiteSum(end)
	}
	if r.Before != nil && r.After != nil {
		r.Delta = finiteSum(end - before)
	}
	return nil
}
