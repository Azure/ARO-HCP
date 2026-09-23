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
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type EnrichmentWindow struct {
	Name  string    `json:"name"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

type EnrichmentMetric struct {
	Workspace   string            `json:"workspace"`
	Name        string            `json:"name"`
	Labels      bool              `json:"labels"`
	MatchLabels map[string]string `json:"matchLabels,omitempty"`
	Windows     []string          `json:"windows,omitempty"`
}

// EnrichmentPlan is a bounded cross product, not arbitrary PromQL or endpoints.
type EnrichmentPlan struct {
	Windows []EnrichmentWindow `json:"windows"`
	Metrics []EnrichmentMetric `json:"metrics"`
}

// PlanEnrichment atomically stores an immutable plan and all requests. A nil
// reader resumes the stored plan; a conflicting replacement is never appended.
func (s *ScanStore) PlanEnrichment(ctx context.Context, reader io.Reader) error {
	var p EnrichmentPlan
	if reader != nil {
		limited := &io.LimitedReader{R: reader, N: 65537}
		d := json.NewDecoder(limited)
		d.DisallowUnknownFields()
		if err := d.Decode(&p); err != nil {
			return err
		}
		var extra any
		if err := d.Decode(&extra); err != io.EOF {
			return errors.New("plan must contain one bounded JSON object")
		}
		if limited.N == 0 {
			return errors.New("plan exceeds 64 KiB")
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS enrichment_plan(id INTEGER PRIMARY KEY CHECK(id=1), spec_json TEXT NOT NULL, spec_hash TEXT NOT NULL, created_at REAL NOT NULL);
 CREATE TABLE IF NOT EXISTS enrichment_query (
 plan_id INTEGER NOT NULL REFERENCES enrichment_plan(id), window_id INTEGER NOT NULL REFERENCES window(id),
 workspace_id INTEGER NOT NULL REFERENCES workspace(id), metric_id INTEGER NOT NULL REFERENCES metric(id),
 role TEXT NOT NULL CHECK(role IN ('samples_window','inventory_samples')), query_id TEXT NOT NULL REFERENCES query(id),
 selector TEXT NOT NULL, grouping TEXT NOT NULL,
 PRIMARY KEY(plan_id,window_id,workspace_id,metric_id,role));
 CREATE INDEX IF NOT EXISTS enrichment_query_request ON enrichment_query(query_id)`); err != nil {
		return err
	}
	var stored, fingerprint string
	err = tx.QueryRowContext(ctx, `SELECT spec_json,spec_hash FROM enrichment_plan WHERE id=1`).Scan(&stored, &fingerprint)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if stored != "" && scanHash(stored) != fingerprint {
		return errors.New("stored enrichment plan fingerprint mismatch")
	}
	if reader == nil {
		if stored == "" {
			return errors.New("first enrichment requires --plan")
		}
		if err := json.Unmarshal([]byte(stored), &p); err != nil {
			return err
		}
	}
	if len(p.Windows) < 1 || len(p.Windows) > 3 || len(p.Metrics) < 1 || len(p.Metrics) > 4 {
		return errors.New("plan requires 1-3 windows and 1-4 workspace/metric pairs (at most 24 queries)")
	}
	var start, end float64
	var rawContext, planning string
	if err := tx.QueryRowContext(ctx, `SELECT start,end,context_json,planning_state FROM run WHERE id=1`).Scan(&start, &end, &rawContext, &planning); err != nil {
		return err
	}
	if planning != "complete" {
		return errors.New("seed planning must be complete")
	}
	var baseline *collectionContext
	if rawContext != "" && rawContext != "null" && rawContext != "{}" {
		baseline, err = (CollectOptions{Start: time.Unix(int64(start), 0), End: time.Unix(int64(end), 0), Context: json.RawMessage(rawContext)}).collectionContext()
		if err != nil {
			return fmt.Errorf("seed context: %w", err)
		}
	}
	names, intervals := map[string]bool{}, map[string]bool{}
	for i := range p.Windows {
		w := &p.Windows[i]
		w.Start, w.End = w.Start.UTC(), w.End.UTC()
		d := w.End.Sub(w.Start)
		key := fmt.Sprintf("samples:%d:%d", w.Start.Unix(), w.End.Unix())
		if strings.TrimSpace(w.Name) == "" || len(w.Name) > 128 || names[w.Name] || intervals[key] || w.Start.Nanosecond() != 0 || w.End.Nanosecond() != 0 || d < time.Minute || d > 30*time.Minute {
			return errors.New("windows need unique names and intervals, whole seconds and durations of 1-30 minutes")
		}
		names[w.Name], intervals[key] = true, true
		inRun := float64(w.Start.Unix()) >= start && float64(w.End.Unix()) <= end
		inBaseline := baseline != nil && baseline.Baseline != nil && !w.Start.Before(baseline.Baseline.Start) && !w.End.After(baseline.Baseline.End)
		if !inRun && !inBaseline {
			return fmt.Errorf("window %q is outside run and explicit baseline", w.Name)
		}
	}
	type metricIDs struct{ workspace, metric int64 }
	ids := map[string]metricIDs{}
	for i := range p.Metrics {
		m := &p.Metrics[i]
		if !collectionMetricName.MatchString(m.Name) {
			return errors.New("invalid exact metric name")
		}
		var id metricIDs
		var endpoint, workspace string
		if err := tx.QueryRowContext(ctx, `SELECT w.id,m.id,w.endpoint,w.name FROM workspace w JOIN metric m ON m.workspace_id=w.id WHERE (lower(w.name)=lower(?) OR lower(w.arm_id)=lower(?)) AND m.name=? AND m.catalog=1`, m.Workspace, m.Workspace, m.Name).Scan(&id.workspace, &id.metric, &endpoint, &workspace); err != nil {
			return fmt.Errorf("unknown catalog pair %s/%s: %w", m.Workspace, m.Name, err)
		}
		if err := validateScanEndpoint(endpoint); err != nil {
			return err
		}
		m.Workspace = workspace
		key := workspace + "/" + m.Name
		if _, exists := ids[key]; exists {
			return errors.New("duplicate workspace/metric pair")
		}
		ids[key] = id
		selected := map[string]bool{}
		for _, name := range m.Windows {
			if !names[name] || selected[name] {
				return errors.New("metric windows must be unique names from the plan")
			}
			selected[name] = true
		}
		sort.Strings(m.Windows)
		for label, value := range m.MatchLabels {
			if (label != "cluster" && label != "namespace" && label != "job") || value == "" || len(value) > 1024 {
				return errors.New("matchLabels permits nonempty exact cluster, namespace and job values up to 1024 bytes")
			}
		}
	}
	sort.Slice(p.Windows, func(i, j int) bool { return p.Windows[i].Name < p.Windows[j].Name })
	sort.Slice(p.Metrics, func(i, j int) bool {
		return p.Metrics[i].Workspace+"/"+p.Metrics[i].Name < p.Metrics[j].Workspace+"/"+p.Metrics[j].Name
	})
	spec, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if stored != "" && stored != string(spec) {
		return errors.New("enrichment plan conflicts with immutable stored plan; use a new snapshot")
	}
	if stored == "" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO enrichment_plan VALUES(1,?,?,?)`, string(spec), scanHash(string(spec)), scanTime(time.Now())); err != nil {
			return err
		}
	}
	for _, w := range p.Windows {
		kind := fmt.Sprintf("samples:%d:%d", w.Start.Unix(), w.End.Unix())
		if _, err := tx.ExecContext(ctx, `INSERT INTO window(run_id,kind,start,end) VALUES(1,?,?,?) ON CONFLICT(kind) DO NOTHING`, kind, scanTime(w.Start), scanTime(w.End)); err != nil {
			return err
		}
		var window int64
		if err := tx.QueryRowContext(ctx, `SELECT id FROM window WHERE kind=? AND start=? AND end=?`, kind, scanTime(w.Start), scanTime(w.End)).Scan(&window); err != nil {
			return err
		}
		for _, m := range p.Metrics {
			if len(m.Windows) > 0 {
				i := sort.SearchStrings(m.Windows, w.Name)
				if i == len(m.Windows) || m.Windows[i] != w.Name {
					continue
				}
			}
			id := ids[m.Workspace+"/"+m.Name]
			selector := m.Name
			var labels []string
			for k, v := range m.MatchLabels {
				labels = append(labels, k+"="+strconv.Quote(v))
			}
			sort.Strings(labels)
			if len(labels) > 0 {
				selector += "{" + strings.Join(labels, ",") + "}"
			}
			full := fmt.Sprintf("count_over_time(%s[%dms])", selector, w.End.Sub(w.Start).Milliseconds())
			roles := []string{"samples_window"}
			if m.Labels {
				roles = append(roles, "inventory_samples")
			}
			for _, role := range roles {
				expression, grouping := full, ""
				if role == "samples_window" {
					grouping = scanGrouping
					expression = "sum by(" + grouping + ")(" + full + ")"
				}
				params := url.Values{"query": {expression}, "time": {w.End.Format(time.RFC3339)}, "timeout": {"90s"}}.Encode()
				identity, _ := json.Marshal([]any{role, id.workspace, expression, w.Start.Unix(), w.End.Unix()})
				qid := scanHash(string(identity))
				// Request identity is independent of its logical roles. Preserve an
				// existing ranking/cached request and its accepted attempt verbatim.
				var existing string
				err := tx.QueryRowContext(ctx, `SELECT id FROM query WHERE workspace_id=? AND path='/api/v1/query' AND params=?`, id.workspace, params).Scan(&existing)
				if err != nil && !errors.Is(err, sql.ErrNoRows) {
					return err
				}
				if existing != "" {
					qid = existing
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO query(id,workspace_id,metric_id,window_id,kind,ranking,path,params,expression,evaluation_time,lookback_seconds,grouping,state) VALUES(?,?,?,?,?,0,'/api/v1/query',?,?,?,?,?,'pending') ON CONFLICT(id) DO NOTHING`, qid, id.workspace, id.metric, window, role, params, expression, scanTime(w.End), w.End.Sub(w.Start).Seconds(), grouping); err != nil {
					return err
				}
				var matches int
				if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM query WHERE id=? AND workspace_id=? AND metric_id=? AND path='/api/v1/query' AND params=? AND expression=? AND evaluation_time=?`, qid, id.workspace, id.metric, params, expression, scanTime(w.End)).Scan(&matches); err != nil {
					return err
				}
				if matches != 1 {
					return errors.New("stored enrichment query identity conflicts with plan")
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO enrichment_query(plan_id,window_id,workspace_id,metric_id,role,query_id,selector,grouping) VALUES(1,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING`, window, id.workspace, id.metric, role, qid, selector, grouping); err != nil {
					return err
				}
				if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM enrichment_query WHERE plan_id=1 AND window_id=? AND workspace_id=? AND metric_id=? AND role=? AND query_id=? AND selector=? AND grouping=?`, window, id.workspace, id.metric, role, qid, selector, grouping).Scan(&matches); err != nil {
					return err
				}
				if matches != 1 {
					return errors.New("stored enrichment role link conflicts with plan")
				}
			}
		}
	}
	return tx.Commit()
}

// claimEnrichment only touches explicitly linked requests, including identical
// ranking requests required by the plan. It never plans partitions.
func (s *ScanStore) claimEnrichment(ctx context.Context, owner string, now time.Time, kinds []string) (*scanClaim, bool, error) {
	waitStarted := time.Now()
	encoded, _ := json.Marshal(kinds)
	filter := `id IN (SELECT query_id FROM enrichment_query WHERE plan_id=1 AND role IN (SELECT value FROM json_each(?)))`
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback() }()
	n := scanTime(now.Add(time.Since(waitStarted)))
	for _, statement := range []string{
		`UPDATE attempt SET state='abandoned',finished_at=?,classification='lease_expired' WHERE id IN (SELECT current_attempt_id FROM query WHERE state='running' AND lease_until<=? AND ` + filter + `)`,
		`DELETE FROM observation WHERE attempt_id IN (SELECT current_attempt_id FROM query WHERE state='running' AND lease_until<=? AND ` + filter + `)`,
		`UPDATE query SET state='pending',owner=NULL,lease_until=NULL,current_attempt_id=NULL WHERE state='running' AND lease_until<=? AND ` + filter,
	} {
		args := []any{n, string(encoded)}
		if strings.HasPrefix(statement, "UPDATE attempt") {
			args = append([]any{n}, args...)
		}
		if _, err := tx.ExecContext(ctx, statement, args...); err != nil {
			return nil, false, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE query SET state='blocked',classification='retry_budget' WHERE state IN ('pending','retry_wait') AND `+filter+` AND id IN (SELECT query_id FROM attempt WHERE `+scanRetryFailures+` GROUP BY query_id HAVING count(*)>=6 OR sum(duration_ms)>=900000)`, string(encoded)); err != nil {
		return nil, false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE query SET state='blocked',classification='workspace_authorization' WHERE state IN ('pending','retry_wait') AND `+filter+` AND workspace_id IN (SELECT id FROM workspace WHERE blocked_reason<>'')`, string(encoded)); err != nil {
		return nil, false, err
	}
	c := &scanClaim{owner: owner}
	err = tx.QueryRowContext(ctx, `WITH selected AS (SELECT * FROM query WHERE `+filter+`) SELECT q.id,q.workspace_id,w.endpoint,q.path,q.params,(SELECT role FROM enrichment_query WHERE plan_id=1 AND query_id=q.id LIMIT 1),q.attempt_count FROM selected q JOIN workspace w ON w.id=q.workspace_id WHERE q.state IN ('pending','retry_wait') AND q.retry_at<=? AND w.cooldown_until<=? AND w.blocked_reason='' AND (SELECT count(*) FROM selected WHERE state='running')<(SELECT max_workers FROM run WHERE id=1) AND (SELECT count(*) FROM selected active WHERE active.workspace_id=q.workspace_id AND active.state='running')<w.concurrency ORDER BY q.attempt_count DESC,q.id LIMIT 1`, string(encoded), n, n).Scan(&c.id, &c.workspace, &c.endpoint, &c.path, &c.params, &c.kind, &c.count)
	if errors.Is(err, sql.ErrNoRows) {
		var remaining int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM query WHERE state IN ('pending','retry_wait','running') AND `+filter, string(encoded)).Scan(&remaining); err != nil {
			return nil, false, err
		}
		return nil, remaining == 0, tx.Commit()
	}
	if err != nil {
		return nil, false, err
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO attempt(query_id,owner,started_at,state) VALUES(?,?,?,'running')`, c.id, owner, n)
	if err != nil {
		return nil, false, err
	}
	c.attempt, err = res.LastInsertId()
	if err != nil {
		return nil, false, err
	}
	c.count++
	if _, err := tx.ExecContext(ctx, `UPDATE query SET state='running',owner=?,lease_until=?,current_attempt_id=?,attempt_count=attempt_count+1,first_attempt_at=coalesce(first_attempt_at,?) WHERE id=?`, owner, n+scanLease.Seconds(), c.attempt, n, c.id); err != nil {
		return nil, false, err
	}
	return c, false, tx.Commit()
}

type EnrichmentSummary struct {
	Queries            map[string]int `json:"queries"`
	SchedulingComplete bool           `json:"schedulingComplete"`
	CoverageComplete   bool           `json:"coverageComplete"`
}

func (s *ScanStore) EnrichmentSummary(ctx context.Context) (EnrichmentSummary, error) {
	r := EnrichmentSummary{Queries: map[string]int{}}
	rows, err := s.db.QueryContext(ctx, `SELECT q.state,count(*) FROM enrichment_query e JOIN query q ON q.id=e.query_id WHERE e.plan_id=1 GROUP BY q.state`)
	if err != nil {
		return r, err
	}
	defer rows.Close()
	for rows.Next() {
		var state string
		var n int
		if err := rows.Scan(&state, &n); err != nil {
			return r, err
		}
		r.Queries[state] = n
	}
	r.SchedulingComplete = len(r.Queries) > 0 && r.Queries["pending"]+r.Queries["running"]+r.Queries["retry_wait"] == 0
	r.CoverageComplete = r.SchedulingComplete && r.Queries["blocked"] == 0
	return r, rows.Err()
}
