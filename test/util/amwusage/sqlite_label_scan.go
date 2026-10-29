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
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"modernc.org/sqlite"
)

const labelResponseReservation int64 = (32 << 20) + 1

// LabelScanOptions bounds the cumulative physical attempts, including retries
// and interrupted requests. Increasing a limit resumes the same immutable plan.
type LabelScanOptions struct {
	MaxBytes    int64
	MaxRequests int
}

const labelScanSchema = `
CREATE TABLE IF NOT EXISTS label_scan (
 id INTEGER PRIMARY KEY CHECK(id=1), start REAL NOT NULL, end REAL NOT NULL,
 max_bytes INTEGER NOT NULL, max_requests INTEGER NOT NULL, created_at REAL NOT NULL
);
CREATE TABLE IF NOT EXISTS label_scan_metric (
 metric_id INTEGER PRIMARY KEY REFERENCES metric(id), source_query_id TEXT REFERENCES query(id),
 root_id TEXT REFERENCES query(id), expected_samples INTEGER, skipped_zero INTEGER NOT NULL,
 source_attempt_id INTEGER REFERENCES attempt(id)
);
CREATE TABLE IF NOT EXISTS label_scan_query (
 query_id TEXT PRIMARY KEY REFERENCES query(id), metric_id INTEGER NOT NULL REFERENCES label_scan_metric(metric_id),
 parent_id TEXT REFERENCES label_scan_query(query_id), predicate TEXT NOT NULL, depth INTEGER NOT NULL,
 split INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS label_scan_parent ON label_scan_query(parent_id);
CREATE INDEX IF NOT EXISTS label_scan_metric_query ON label_scan_query(metric_id,split);
CREATE TABLE IF NOT EXISTS label_scan_hint (
 metric_id INTEGER NOT NULL REFERENCES metric(id), name TEXT NOT NULL, value TEXT NOT NULL,
 PRIMARY KEY(metric_id,name,value)
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS label_scan_observation (
 attempt_id INTEGER NOT NULL REFERENCES attempt(id), labelset_id INTEGER NOT NULL REFERENCES labelset(id), timestamp REAL NOT NULL,
 samples INTEGER NOT NULL CHECK(typeof(samples)='integer' AND samples>0), PRIMARY KEY(attempt_id,labelset_id,timestamp)
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS label_scan_group (
 labelset_id INTEGER PRIMARY KEY REFERENCES labelset(id), group_id INTEGER NOT NULL REFERENCES labelset(id)
);
CREATE TABLE IF NOT EXISTS label_scan_expected_group (
 metric_id INTEGER NOT NULL REFERENCES label_scan_metric(metric_id), group_id INTEGER NOT NULL REFERENCES labelset(id),
 samples INTEGER NOT NULL CHECK(typeof(samples)='integer' AND samples>0), PRIMARY KEY(metric_id,group_id)
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS label_scan_http (
 attempt_id INTEGER PRIMARY KEY REFERENCES attempt(id), method TEXT NOT NULL CHECK(method IN ('GET','POST'))
);
CREATE TRIGGER IF NOT EXISTS label_scan_discard AFTER UPDATE OF state ON attempt
 WHEN NEW.state NOT IN ('running','succeeded') BEGIN
 DELETE FROM label_scan_observation WHERE attempt_id=NEW.id;
 END;
`

const labelScanViews = `
CREATE VIEW IF NOT EXISTS label_scan_leaf_observation AS
 SELECT l.metric_id,l.query_id,o.* FROM label_scan_query l JOIN query q ON q.id=l.query_id
 JOIN label_scan_observation o ON o.attempt_id=q.accepted_attempt_id
 WHERE l.split=0 AND q.state='succeeded';
CREATE VIEW IF NOT EXISTS label_scan_actual_group AS
 SELECT o.metric_id,g.group_id,sum(o.samples) AS samples FROM label_scan_leaf_observation o
 JOIN label_scan_group g ON g.labelset_id=o.labelset_id GROUP BY o.metric_id,g.group_id;
CREATE VIEW IF NOT EXISTS label_scan_coverage AS
 SELECT m.metric_id,m.root_id,m.source_query_id,m.expected_samples,m.skipped_zero,
 (SELECT count(*) FROM label_scan_query l WHERE l.metric_id=m.metric_id AND l.split=0) AS leaves,
 (SELECT count(*) FROM label_scan_query l JOIN query q ON q.id=l.query_id
 WHERE l.metric_id=m.metric_id AND l.split=0 AND q.state<>'succeeded') AS unfinished,
 (SELECT count(*) FROM label_scan_leaf_observation o WHERE o.metric_id=m.metric_id) AS series,
 (SELECT coalesce(sum(samples),0) FROM label_scan_leaf_observation o WHERE o.metric_id=m.metric_id) AS samples,
 EXISTS(SELECT 1 FROM label_scan_leaf_observation o WHERE o.metric_id=m.metric_id
 GROUP BY labelset_id,timestamp HAVING count(*)>1) AS overlapping,
 m.source_attempt_id,
 NOT EXISTS(SELECT 1 FROM label_scan_query l JOIN query q ON q.id=l.query_id
 WHERE l.metric_id=m.metric_id AND l.split=0 AND (q.state<>'succeeded' OR q.accepted_attempt_id IS NULL)) AS execution_complete,
 CASE WHEN m.source_attempt_id IS NULL THEN NULL ELSE
 NOT EXISTS(SELECT group_id,samples FROM label_scan_expected_group WHERE metric_id=m.metric_id
 EXCEPT SELECT group_id,samples FROM label_scan_actual_group WHERE metric_id=m.metric_id)
 AND NOT EXISTS(SELECT group_id,samples FROM label_scan_actual_group WHERE metric_id=m.metric_id
 EXCEPT SELECT group_id,samples FROM label_scan_expected_group WHERE metric_id=m.metric_id) END AS source_reconciled
 FROM label_scan_metric m;
CREATE VIEW IF NOT EXISTS label_scan_result AS
 SELECT o.* FROM label_scan_leaf_observation o JOIN label_scan_coverage c ON c.metric_id=o.metric_id
 WHERE c.execution_complete=1 AND c.overlapping=0 AND (c.source_reconciled IS NULL OR c.source_reconciled=1);
`

// PlanLabels selects every catalog pair except confidently complete empty run
// results. The window is exactly (run.start,run.end], never a 12h inventory.
// Full labels use the same case-folded identity as the ranking dictionaries.
func (s *ScanStore) PlanLabels(ctx context.Context, o LabelScanOptions) error {
	if o.MaxBytes < labelResponseReservation || o.MaxRequests < 1 {
		return errors.New("labels requires max-bytes >= 33554433 and positive max-requests")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var legacy int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('label_scan_observation') WHERE name='labels_json'`).Scan(&legacy); err != nil {
		return err
	}
	if legacy != 0 {
		return errors.New("legacy raw label storage requires labels --migrate-only on an isolated backup")
	}
	if err := labelMigrationReady(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, labelScanSchema); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, labelScanViews); err != nil {
		return err
	}
	var start, end float64
	var planning string
	if err := tx.QueryRowContext(ctx, `SELECT start,end,planning_state FROM run WHERE id=1`).Scan(&start, &end, &planning); err != nil {
		return err
	}
	if planning != "complete" || end <= start || start != float64(int64(start)) || end != float64(int64(end)) {
		return errors.New("labels requires an initialized run with a positive whole-second window")
	}
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM label_scan`).Scan(&exists); err != nil {
		return err
	}
	if exists != 0 {
		res, err := tx.ExecContext(ctx, `UPDATE label_scan SET max_bytes=?,max_requests=? WHERE id=1 AND start=? AND end=?`, o.MaxBytes, o.MaxRequests, start, end)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return errors.New("label scan window changed")
		}
		return tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO label_scan VALUES(1,?,?,?,?,?)`, start, end, o.MaxBytes, o.MaxRequests, scanTime(time.Now())); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT m.id,m.workspace_id,m.display_name,q.id,q.state,
 coalesce(sum(o.integer_value),0),count(o.invalid_value),coalesce(a.warnings_json,'[]')
 FROM metric m LEFT JOIN query q ON q.metric_id=m.id AND q.kind='run' AND q.ranking=1
 LEFT JOIN attempt a ON a.id=q.accepted_attempt_id LEFT JOIN observation o ON o.attempt_id=a.id
 WHERE m.catalog=1 GROUP BY m.id ORDER BY coalesce(sum(o.integer_value),0) DESC,m.id`)
	if err != nil {
		return err
	}
	type candidate struct {
		mid, wid      int64
		name          string
		source, state sql.NullString
		samples       int64
		invalid       int
		warnings      string
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.mid, &c.wid, &c.name, &c.source, &c.state, &c.samples, &c.invalid, &c.warnings); err != nil {
			rows.Close()
			return err
		}
		candidates = append(candidates, c)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, c := range candidates {
		if !collectionMetricName.MatchString(c.name) {
			return fmt.Errorf("invalid catalog metric %q", c.name)
		}
		var expected any
		if c.state.String == "succeeded" && c.invalid == 0 && c.warnings == "[]" {
			expected = c.samples
		}
		skip := expected != nil && c.samples == 0
		if _, err := tx.ExecContext(ctx, `INSERT INTO label_scan_metric(metric_id,source_query_id,expected_samples,skipped_zero) VALUES(?,?,?,?)`, c.mid, c.source, expected, skip); err != nil {
			return err
		}
		if err := freezeLabelSource(ctx, tx, c.mid, start, end); err != nil {
			return err
		}
		if skip {
			continue
		}
		id, err := planLabelQuery(ctx, tx, c.mid, c.wid, c.name, "", "", 0, start, end)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE label_scan_metric SET root_id=? WHERE metric_id=?`, id, c.mid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func planLabelQuery(ctx context.Context, tx *sql.Tx, mid, wid int64, name, predicate, parent string, depth int, start, end float64) (string, error) {
	selector := name
	if predicate != "" {
		selector += "{" + compactLabelPredicate(predicate) + "}"
	}
	expression := fmt.Sprintf("count_over_time(%s[%dms])", selector, int64((end-start)*1000))
	params := url.Values{"query": {expression}, "time": {time.Unix(int64(end), 0).UTC().Format(time.RFC3339)}, "timeout": {"90s"}}.Encode()
	id := scanHash(fmt.Sprintf("labels:%d:%s", wid, params))
	// Do not reuse the old case-normalized enrichment responses as raw evidence.
	var old string
	err := tx.QueryRowContext(ctx, `SELECT id FROM query WHERE workspace_id=? AND path='/api/v1/query' AND params=?`, wid, params).Scan(&old)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if old != "" && old != id {
		return "", errors.New("raw label request already exists outside label scan; use a fresh ranking snapshot")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO query(id,workspace_id,metric_id,window_id,kind,ranking,path,params,expression,evaluation_time,lookback_seconds,state)
 VALUES(?,?,?,(SELECT id FROM window WHERE kind='run'),'inventory_labels',0,'/api/v1/query',?,?,?,?,'pending')`, id, wid, mid, params, expression, end, end-start)
	if err != nil {
		return "", err
	}
	var parentID any
	if parent != "" {
		parentID = parent
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO label_scan_query(query_id,metric_id,parent_id,predicate,depth) VALUES(?,?,?,?,?)`, id, mid, parentID, predicate, depth)
	return id, err
}

// Factor common literal prefixes in complement matchers to keep GET URLs small.
// PromQL regex matchers are fully anchored; all values remain exact literals.
func compactLabelPredicate(predicate string) string {
	matcher := regexp.MustCompile(`([a-zA-Z_][a-zA-Z_0-9]*)(!=|=)("(?:[^"\\]|\\.)*")`)
	matches := matcher.FindAllStringSubmatch(predicate, -1)
	var literals []string
	values := map[string][]string{}
	for _, match := range matches {
		literals = append(literals, match[0])
		value, err := strconv.Unquote(match[3])
		if err != nil {
			return predicate
		}
		if match[2] == "!=" {
			values[match[1]] = append(values[match[1]], value)
		}
	}
	if strings.Join(literals, ",") != predicate {
		return predicate
	}
	var factor func([]string) string
	factor = func(v []string) string {
		if len(v) == 1 {
			return regexp.QuoteMeta(v[0])
		}
		prefix := v[0]
		for _, s := range v[1:] {
			for !strings.HasPrefix(s, prefix) {
				prefix = prefix[:len(prefix)-1]
			}
		}
		// A common byte prefix can end in the middle of a UTF-8 rune.
		for !utf8.ValidString(prefix) {
			prefix = prefix[:len(prefix)-1]
		}
		groups := map[string][]string{}
		for _, s := range v {
			s = strings.TrimPrefix(s, prefix)
			key := ""
			if s != "" {
				_, size := utf8.DecodeRuneInString(s)
				key = s[:size]
			}
			groups[key] = append(groups[key], s)
		}
		var alternatives []string
		for key, group := range groups {
			if key == "" {
				alternatives = append(alternatives, "")
			} else {
				alternatives = append(alternatives, factor(group))
			}
		}
		sort.Strings(alternatives)
		return regexp.QuoteMeta(prefix) + "(?:" + strings.Join(alternatives, "|") + ")"
	}
	seen := map[string]bool{}
	var parts []string
	for _, match := range matches {
		key := match[1]
		if match[2] != "!=" || len(values[key]) < 2 {
			parts = append(parts, match[0])
			continue
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		parts = append(parts, key+"!~"+strconv.Quote(factor(values[key])))
	}
	return strings.Join(parts, ",")
}

func insertLabelObservations(ctx context.Context, tx *sql.Tx, aid int64, batch []scanObservation) error {
	// Hints from an incomplete response may guide disjoint selectors, but are
	// never observations or completeness evidence. Complements cover unseen values.
	hints := map[string]map[string]bool{}
	for _, o := range batch {
		for _, key := range []string{"cluster", "job", "namespace", "hostedcontrolplane", "prometheus", "le", "instance"} {
			if value, ok := o.labels[key]; ok {
				if hints[key] == nil {
					hints[key] = map[string]bool{}
				}
				hints[key][value] = true
			}
		}
	}
	for key, values := range hints {
		for value := range values {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO label_scan_hint SELECT q.metric_id,?,? FROM query q JOIN attempt a ON a.query_id=q.id WHERE a.id=?`, key, value, aid); err != nil {
				return scanDBError{err}
			}
		}
	}
	return insertNormalizedLabels(ctx, tx, aid, batch)
}

// Prepared statements and per-batch dictionary caches bound both memory and
// repeated string lookups. Hashes and membership match internLabels exactly.
func insertNormalizedLabels(ctx context.Context, tx *sql.Tx, aid int64, batch []scanObservation) error {
	queries := []string{
		`INSERT INTO labelset(hash,canonical) VALUES(?,'') ON CONFLICT(hash) DO NOTHING RETURNING id`,
		`INSERT INTO label_name(name) VALUES(?) ON CONFLICT(name) DO UPDATE SET name=excluded.name RETURNING id`,
		`INSERT INTO label_value(value) VALUES(?) ON CONFLICT(value) DO UPDATE SET value=excluded.value RETURNING id`,
		`INSERT INTO labelset_member VALUES(?,?,?)`,
		`INSERT INTO label_scan_observation VALUES(?,?,?,?)`,
		`INSERT INTO label_scan_group VALUES(?,?) ON CONFLICT DO NOTHING`,
	}
	stmts := make([]*sql.Stmt, 0, len(queries))
	for _, query := range queries {
		stmt, err := tx.PrepareContext(ctx, query)
		if err != nil {
			return scanDBError{err}
		}
		defer stmt.Close()
		stmts = append(stmts, stmt)
	}
	names, values, sets := map[string]int64{}, map[string]int64{}, map[string]int64{}
	intern := func(labels map[string]string) (int64, error) {
		canonical := map[string]string{}
		for k, v := range labels {
			key := strings.ToLower(k)
			if _, ok := canonical[key]; ok {
				return 0, errors.New("case-colliding label names")
			}
			canonical[key] = strings.ToLower(v)
		}
		b, _ := json.Marshal(canonical)
		hash := scanHash(string(b))
		if id, ok := sets[hash]; ok {
			return id, nil
		}
		var id int64
		err := stmts[0].QueryRowContext(ctx, hash).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			// Input names are already canonical and collision-checked. Failures
			// here are SQL errors or corrupt stored membership, not bad responses.
			id, err = internLabels(ctx, tx, canonical)
		} else if err == nil {
			for k, v := range canonical {
				nid, ok := names[k]
				if !ok {
					if err := stmts[1].QueryRowContext(ctx, k).Scan(&nid); err != nil {
						return 0, scanDBError{err}
					}
					names[k] = nid
				}
				vid, ok := values[v]
				if !ok {
					if err := stmts[2].QueryRowContext(ctx, v).Scan(&vid); err != nil {
						return 0, scanDBError{err}
					}
					values[v] = vid
				}
				if _, err := stmts[3].ExecContext(ctx, id, nid, vid); err != nil {
					return 0, scanDBError{err}
				}
			}
		}
		if err != nil {
			return 0, scanDBError{err}
		}
		sets[hash] = id
		return id, nil
	}
	for _, o := range batch {
		if o.integer == nil || *o.integer <= 0 {
			return errors.New("sample counts must be positive integers")
		}
		id, err := intern(o.labels)
		if err != nil {
			return err
		}
		group := map[string]string{}
		for k, v := range o.labels {
			key := strings.ToLower(k)
			if slices.Contains(strings.Split(scanGrouping, ","), key) && v != "" {
				group[key] = strings.ToLower(v)
			}
		}
		gid, err := intern(group)
		if err != nil {
			return err
		}
		if _, err := stmts[5].ExecContext(ctx, id, gid); err != nil {
			return scanDBError{err}
		}
		if _, err := stmts[4].ExecContext(ctx, aid, id, o.at, *o.integer); err != nil {
			var sqliteError *sqlite.Error
			if errors.As(err, &sqliteError) && (sqliteError.Code() == 1555 || sqliteError.Code() == 2067) {
				return errors.New("duplicate normalized label identity")
			}
			return scanDBError{err}
		}
	}
	return nil
}

func freezeLabelSource(ctx context.Context, tx *sql.Tx, mid int64, start, end float64) error {
	var qid sql.NullString
	var expected sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT source_query_id,expected_samples FROM label_scan_metric WHERE metric_id=?`, mid).Scan(&qid, &expected); err != nil {
		return err
	}
	if !expected.Valid {
		return nil
	}
	var aid int64
	var expr, params, grouping, name string
	if err := tx.QueryRowContext(ctx, `SELECT q.accepted_attempt_id,q.expression,q.params,q.grouping,m.display_name FROM query q JOIN metric m ON m.id=q.metric_id JOIN window w ON w.id=q.window_id JOIN attempt a ON a.id=q.accepted_attempt_id
 WHERE q.id=? AND q.metric_id=? AND q.workspace_id=m.workspace_id AND q.kind='run' AND q.ranking=1 AND q.state='succeeded'
 AND q.path='/api/v1/query' AND q.evaluation_time=? AND q.lookback_seconds=? AND w.kind='run' AND w.start=? AND w.end=? AND a.state='succeeded' AND a.warnings_json='[]'`, qid, mid, end, end-start, start, end).Scan(&aid, &expr, &params, &grouping, &name); err != nil {
		return fmt.Errorf("source provenance for metric %d: %w", mid, err)
	}
	want := fmt.Sprintf("sum by(%s)(count_over_time(%s[%dms]))", scanGrouping, name, int64((end-start)*1000))
	p, err := url.ParseQuery(params)
	if err != nil {
		return err
	}
	if expr != want || grouping != scanGrouping || p.Get("query") != expr || p.Get("time") != time.Unix(int64(end), 0).UTC().Format(time.RFC3339) {
		return fmt.Errorf("source request mismatch for metric %d", mid)
	}
	rows, err := tx.QueryContext(ctx, `SELECT o.labelset_id,o.timestamp,o.integer_value,o.invalid_value FROM observation o WHERE o.attempt_id=?`, aid)
	if err != nil {
		return err
	}
	type source struct {
		id      int64
		at      float64
		n       sql.NullInt64
		invalid sql.NullString
	}
	var sources []source
	var total int64
	for rows.Next() {
		var o source
		if err := rows.Scan(&o.id, &o.at, &o.n, &o.invalid); err != nil {
			rows.Close()
			return err
		}
		if o.at != end || !o.n.Valid || o.n.Int64 <= 0 || o.invalid.Valid {
			rows.Close()
			return errors.New("invalid grouped source observation")
		}
		total += o.n.Int64
		sources = append(sources, o)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	if total != expected.Int64 {
		return errors.New("source total changed since label planning")
	}
	for _, o := range sources {
		labels := map[string]string{}
		r, err := tx.QueryContext(ctx, `SELECT n.name,v.value FROM labelset_member l JOIN label_name n ON n.id=l.name_id JOIN label_value v ON v.id=l.value_id WHERE l.labelset_id=?`, o.id)
		if err != nil {
			return err
		}
		for r.Next() {
			var k, v string
			if err := r.Scan(&k, &v); err != nil {
				r.Close()
				return err
			}
			if !slices.Contains(strings.Split(scanGrouping, ","), k) {
				r.Close()
				return errors.New("unexpected grouped source label")
			}
			if v != "" {
				labels[k] = v
			}
		}
		if err := errors.Join(r.Err(), r.Close()); err != nil {
			return err
		}
		gid, err := internLabels(ctx, tx, labels)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO label_scan_expected_group VALUES(?,?,?)`, mid, gid, o.n.Int64); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE label_scan_metric SET source_attempt_id=? WHERE metric_id=?`, aid, mid)
	return err
}

// Split only capacity failures, using exact literals and their exhaustive
// complement. Cached normalized values are hints, not proof of completeness:
// changed/mixed-case/missing values must still be collected by the complement.
func splitLabelQueries(ctx context.Context, tx *sql.Tx, start, end float64) error {
	rows, err := tx.QueryContext(ctx, `SELECT l.query_id,l.metric_id,q.workspace_id,m.display_name,l.predicate,l.depth
 FROM label_scan_query l JOIN query q ON q.id=l.query_id JOIN metric m ON m.id=l.metric_id
 WHERE l.split=0 AND l.depth<7 AND q.state='blocked' AND
 (q.classification='hard_limit' OR (q.classification='invalid_response' AND EXISTS
 (SELECT 1 FROM attempt a WHERE a.query_id=q.id AND a.response_bytes>=33554433)))`)
	if err != nil {
		return err
	}
	type failed struct {
		id, name, predicate string
		mid, wid            int64
		depth               int
	}
	var failures []failed
	for rows.Next() {
		var f failed
		if err := rows.Scan(&f.id, &f.mid, &f.wid, &f.name, &f.predicate, &f.depth); err != nil {
			rows.Close()
			return err
		}
		failures = append(failures, f)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	keys := []string{"cluster", "job", "namespace", "hostedcontrolplane", "prometheus", "le", "instance"}
	for _, f := range failures {
		// Existing failed attempts retain a bounded diagnostic prefix. Recover
		// literal hints on resume without repeating their oversized request.
		var compressed []byte
		if err := tx.QueryRowContext(ctx, `SELECT error_gzip FROM attempt WHERE query_id=? ORDER BY id DESC LIMIT 1`, f.id).Scan(&compressed); err != nil {
			return err
		}
		if len(compressed) > 0 {
			reader, err := gzip.NewReader(bytes.NewReader(compressed))
			if err != nil {
				return err
			}
			prefix, err := io.ReadAll(io.LimitReader(reader, 65537))
			reader.Close()
			if err != nil {
				return err
			}
			for offset := 0; offset < len(prefix); {
				i := bytes.Index(prefix[offset:], []byte(`"metric":`))
				if i < 0 {
					break
				}
				offset += i + len(`"metric":`)
				var labels map[string]string
				if json.NewDecoder(bytes.NewReader(prefix[offset:])).Decode(&labels) != nil {
					break
				}
				for _, key := range keys {
					if value, ok := labels[key]; ok {
						if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO label_scan_hint VALUES(?,?,?)`, f.mid, key, value); err != nil {
							return err
						}
					}
				}
			}
		}
		for depth := f.depth; depth < len(keys); depth++ {
			key := keys[depth]
			values := []string{}
			hints, err := tx.QueryContext(ctx, `SELECT value FROM label_scan_hint WHERE metric_id=? AND name=? ORDER BY value LIMIT 1000`, f.mid, key)
			if err != nil {
				return err
			}
			for hints.Next() {
				var value string
				if err := hints.Scan(&value); err != nil {
					hints.Close()
					return err
				}
				values = append(values, value)
			}
			if err := errors.Join(hints.Err(), hints.Close()); err != nil {
				return err
			}
			for _, metricOnly := range []bool{true, false} {
				if len(values) > 0 {
					break
				}
				filter := `q.workspace_id=?`
				id := f.wid
				if metricOnly {
					filter = `q.metric_id=?`
					id = f.mid
				}
				r, err := tx.QueryContext(ctx, `SELECT DISTINCT v.value FROM query q JOIN observation o ON o.attempt_id=q.accepted_attempt_id
 JOIN labelset_member lm ON lm.labelset_id=o.labelset_id JOIN label_name n ON n.id=lm.name_id JOIN label_value v ON v.id=lm.value_id
 WHERE q.state='succeeded' AND q.kind='run' AND `+filter+` AND n.name=? ORDER BY v.value LIMIT 1000`, id, key)
				if err != nil {
					return err
				}
				for r.Next() {
					var v string
					if err := r.Scan(&v); err != nil {
						r.Close()
						return err
					}
					values = append(values, v)
				}
				if err := errors.Join(r.Err(), r.Close()); err != nil {
					return err
				}
				if len(values) > 0 {
					break
				}
			}
			if len(values) == 0 {
				continue
			}
			prefix := f.predicate
			if prefix != "" {
				prefix += ","
			}
			var complement []string
			for _, v := range values {
				if _, err := planLabelQuery(ctx, tx, f.mid, f.wid, f.name, prefix+key+"="+strconv.Quote(v), f.id, depth+1, start, end); err != nil {
					return err
				}
				complement = append(complement, key+"!="+strconv.Quote(v))
			}
			if _, err := planLabelQuery(ctx, tx, f.mid, f.wid, f.name, prefix+strings.Join(complement, ","), f.id, depth+1, start, end); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE label_scan_query SET split=1 WHERE query_id=?`, f.id); err != nil {
				return err
			}
			break
		}
	}
	return nil
}

func (s *ScanStore) claimLabelScan(ctx context.Context, owner string, now time.Time) (*scanClaim, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback() }()
	n := scanTime(time.Now())
	const filter = `id IN (SELECT query_id FROM label_scan_query)`
	for _, statement := range []string{
		`UPDATE attempt SET state='abandoned',finished_at=unixepoch('subsec'),classification='lease_expired' WHERE id IN (SELECT current_attempt_id FROM query WHERE state='running' AND lease_until<=? AND ` + filter + `)`,
		`UPDATE query SET state='pending',owner=NULL,lease_until=NULL,current_attempt_id=NULL WHERE state='running' AND lease_until<=? AND ` + filter,
	} {
		if _, err := tx.ExecContext(ctx, statement, n); err != nil {
			return nil, false, err
		}
	}
	var start, end float64
	var maxBytes, usedBytes int64
	var maxRequests, requests int
	if err := tx.QueryRowContext(ctx, `SELECT start,end,max_bytes,max_requests FROM label_scan WHERE id=1`).Scan(&start, &end, &maxBytes, &maxRequests); err != nil {
		return nil, false, err
	}
	if err := splitLabelQueries(ctx, tx, start, end); err != nil {
		return nil, false, err
	}
	// Reserve a maximum response before each request. Abandoned attempts retain
	// their reservation because the bytes received before a crash are unknowable.
	if err := tx.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(CASE WHEN a.state IN ('running','abandoned') THEN ? ELSE a.response_bytes END),0)
 FROM attempt a JOIN label_scan_query l ON l.query_id=a.query_id`, labelResponseReservation).Scan(&requests, &usedBytes); err != nil {
		return nil, false, err
	}
	if requests >= maxRequests || maxBytes-usedBytes < labelResponseReservation {
		return nil, true, tx.Commit()
	}
	// Retry a prior GET 414 once using a query POST, never by inheriting its
	// oversized selector into further partition children. A POST 414 is terminal.
	if _, err := tx.ExecContext(ctx, `UPDATE query SET state='pending',classification='' WHERE state='blocked' AND id IN
 (SELECT l.query_id FROM label_scan_query l JOIN attempt a ON a.query_id=l.query_id JOIN label_scan_http h ON h.attempt_id=a.id
 WHERE l.split=0 AND a.http_status=414 AND h.method='GET' AND a.id=(SELECT max(id) FROM attempt WHERE query_id=l.query_id))`); err != nil {
		return nil, false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE query SET state='blocked',classification='workspace_authorization' WHERE state IN ('pending','retry_wait') AND `+filter+` AND workspace_id IN (SELECT id FROM workspace WHERE blocked_reason<>'')`); err != nil {
		return nil, false, err
	}
	c := &scanClaim{owner: owner}
	err = tx.QueryRowContext(ctx, `SELECT q.id,q.workspace_id,w.endpoint,q.path,q.params,'inventory_labels',q.attempt_count
 FROM label_scan_query l JOIN query q ON q.id=l.query_id JOIN workspace w ON w.id=q.workspace_id JOIN label_scan_metric m ON m.metric_id=l.metric_id
 WHERE l.split=0 AND q.state IN ('pending','retry_wait') AND q.retry_at<=? AND w.cooldown_until<=? AND w.blocked_reason=''
 AND (SELECT count(*) FROM query WHERE state='running' AND `+filter+`)<(SELECT max_workers FROM run WHERE id=1)
 AND (SELECT count(*) FROM query active WHERE active.workspace_id=q.workspace_id AND active.state='running' AND active.id IN (SELECT query_id FROM label_scan_query))<w.concurrency
 ORDER BY coalesce(m.expected_samples,0) DESC,m.metric_id,l.depth DESC,q.rowid LIMIT 1`, n, n).Scan(&c.id, &c.workspace, &c.endpoint, &c.path, &c.params, &c.kind, &c.count)
	if errors.Is(err, sql.ErrNoRows) {
		var remaining int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM query WHERE state IN ('pending','retry_wait','running') AND `+filter).Scan(&remaining); err != nil {
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

type LabelScanSummary struct {
	Metrics                      int   `json:"metrics"`
	SkippedZero                  int   `json:"skippedZero"`
	Complete                     int   `json:"complete"`
	Unverified                   int   `json:"unverified"`
	Mismatched                   int   `json:"mismatched"`
	Requests                     int   `json:"requests"`
	ChargedBytes                 int64 `json:"chargedBytes"`
	CoverageComplete             bool  `json:"coverageComplete"`
	Reconciled                   int   `json:"sourceReconciled"`
	SourceReconciliationComplete bool  `json:"sourceReconciliationComplete"`
}

func (s *ScanStore) LabelSummary(ctx context.Context) (LabelScanSummary, error) {
	var r LabelScanSummary
	err := s.db.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(skipped_zero),0),
 coalesce(sum(skipped_zero=0 AND execution_complete=1 AND overlapping=0),0),
 coalesce(sum(skipped_zero=0 AND execution_complete=1 AND source_reconciled IS NULL),0),
 coalesce(sum(execution_complete=1 AND (overlapping<>0 OR source_reconciled=0)),0),
 coalesce(sum(execution_complete=1 AND overlapping=0 AND source_reconciled=1),0)
 FROM label_scan_coverage`).Scan(&r.Metrics, &r.SkippedZero, &r.Complete, &r.Unverified, &r.Mismatched, &r.Reconciled)
	if err != nil {
		return r, err
	}
	err = s.db.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(CASE WHEN a.state IN ('running','abandoned') THEN ? ELSE a.response_bytes END),0)
 FROM attempt a JOIN label_scan_query l ON l.query_id=a.query_id`, labelResponseReservation).Scan(&r.Requests, &r.ChargedBytes)
	r.CoverageComplete = r.Metrics > 0 && r.Complete+r.SkippedZero == r.Metrics && r.Mismatched == 0
	r.SourceReconciliationComplete = r.CoverageComplete && r.Reconciled == r.Metrics
	return r, err
}

func labelMigrationReady(ctx context.Context, q scanSummaryQuerier) error {
	var n int
	if err := q.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE name='label_scan_migration'`).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return nil
	}
	var state string
	if err := q.QueryRowContext(ctx, `SELECT state FROM label_scan_migration WHERE id=1`).Scan(&state); err != nil {
		return err
	}
	if state != "complete" {
		return errors.New("label normalization migration is incomplete; use labels --migrate-only")
	}
	return nil
}

// MigrateLabels is offline and restartable. The original raw table remains
// intact until every normalized row and frozen source group has been validated.
// Call only on an isolated SQLite backup; no cloud request is made here.
func (s *ScanStore) MigrateLabels(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var running, legacy int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM query WHERE state='running' OR owner IS NOT NULL OR current_attempt_id IS NOT NULL`).Scan(&running); err != nil {
		return err
	}
	if running != 0 {
		return errors.New("stop all scanners before label migration")
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('label_scan_observation') WHERE name='labels_json'`).Scan(&legacy); err != nil {
		return err
	}
	if legacy != 0 {
		if _, err := tx.ExecContext(ctx, `DROP VIEW IF EXISTS label_scan_result; DROP VIEW IF EXISTS label_scan_coverage; DROP VIEW IF EXISTS label_scan_leaf_observation;
 DROP TRIGGER IF EXISTS label_scan_discard;
 ALTER TABLE label_scan_observation RENAME TO label_scan_raw;
 ALTER TABLE label_scan_metric ADD COLUMN source_attempt_id INTEGER REFERENCES attempt(id);
 CREATE TABLE label_scan_migration(id INTEGER PRIMARY KEY CHECK(id=1),state TEXT NOT NULL,last_attempt INTEGER NOT NULL,last_labels TEXT NOT NULL,last_timestamp REAL NOT NULL,rows_done INTEGER NOT NULL);
 INSERT INTO label_scan_migration VALUES(1,'migrating',0,'',0,0);`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, labelScanSchema); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO label_scan_http SELECT a.id,'GET' FROM attempt a JOIN label_scan_query l ON l.query_id=a.query_id`); err != nil {
			return err
		}
		var start, end float64
		if err := tx.QueryRowContext(ctx, `SELECT start,end FROM label_scan WHERE id=1`).Scan(&start, &end); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT metric_id FROM label_scan_metric ORDER BY metric_id`)
		if err != nil {
			return err
		}
		var ids []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		for _, id := range ids {
			if err := freezeLabelSource(ctx, tx, id, start, end); err != nil {
				return err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	for {
		done, err := s.migrateLabelBatch(ctx)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
	}
}

func (s *ScanStore) migrateLabelBatch(ctx context.Context) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	var state, lastLabels string
	var lastAttempt int64
	var lastTime float64
	if err := tx.QueryRowContext(ctx, `SELECT state,last_attempt,last_labels,last_timestamp FROM label_scan_migration WHERE id=1`).Scan(&state, &lastAttempt, &lastLabels, &lastTime); err != nil {
		return false, err
	}
	if state == "complete" {
		return true, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT attempt_id,labels_json,timestamp,samples FROM label_scan_raw WHERE (attempt_id,labels_json,timestamp)>(?,?,?) ORDER BY attempt_id,labels_json,timestamp LIMIT 256`, lastAttempt, lastLabels, lastTime)
	if err != nil {
		return false, err
	}
	type raw struct {
		aid, n int64
		labels string
		at     float64
	}
	var batch []raw
	for rows.Next() {
		var r raw
		if err := rows.Scan(&r.aid, &r.labels, &r.at, &r.n); err != nil {
			rows.Close()
			return false, err
		}
		batch = append(batch, r)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return false, err
	}
	if len(batch) == 0 {
		var mismatched int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM (SELECT attempt_id,count(*),sum(samples) FROM label_scan_raw GROUP BY attempt_id EXCEPT SELECT attempt_id,count(*),sum(samples) FROM label_scan_observation GROUP BY attempt_id)`).Scan(&mismatched); err != nil {
			return false, err
		}
		if mismatched != 0 {
			return false, errors.New("normalized migration row/sample totals do not match raw evidence")
		}
		if _, err := tx.ExecContext(ctx, `DROP TABLE label_scan_raw; UPDATE label_scan_migration SET state='complete' WHERE id=1;`+labelScanViews); err != nil {
			return false, err
		}
		return true, tx.Commit()
	}
	for i := 0; i < len(batch); {
		aid := batch[i].aid
		var observations []scanObservation
		for i < len(batch) && batch[i].aid == aid {
			r := &batch[i]
			var labels map[string]string
			if err := json.Unmarshal([]byte(r.labels), &labels); err != nil {
				return false, err
			}
			observations = append(observations, scanObservation{labels: labels, at: r.at, integer: &r.n})
			i++
		}
		if err := insertNormalizedLabels(ctx, tx, aid, observations); err != nil {
			return false, fmt.Errorf("migrate attempt %d: %w", aid, err)
		}
	}
	last := batch[len(batch)-1]
	if _, err := tx.ExecContext(ctx, `UPDATE label_scan_migration SET last_attempt=?,last_labels=?,last_timestamp=?,rows_done=rows_done+? WHERE id=1`, last.aid, last.labels, last.at, len(batch)); err != nil {
		return false, err
	}
	return false, tx.Commit()
}
