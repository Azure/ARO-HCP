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
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// ScanStore owns one SQLite connection; caller goroutines queue for it. Independent
// stores/processes serialize writes through SQLite's immediate transactions, while
// WAL permits concurrent read snapshots. All persisted times are Unix
// seconds (REAL), except attempt duration_ms. Schema version is PRAGMA user_version.
type ScanStore struct{ db *sql.DB }

// OpenScanStore opens or creates a schema-1 scan database, with WAL, full sync,
// foreign keys and a busy timeout on every connection. Close after use.
func OpenScanStore(path string) (*ScanStore, error) {
	if path == "" {
		return nil, errors.New("database path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(abs, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: abs}
	v := url.Values{"_pragma": {"foreign_keys(1)", "busy_timeout(10000)", "journal_mode(WAL)", "synchronous(FULL)"}, "_txlock": {"immediate"}}
	u.RawQuery = v.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	s := &ScanStore{db: db}
	var journalMode string
	// Concurrent first opens can contend while the DSN switches journal mode.
	// SQLite may skip the busy handler for that lock upgrade; retry only startup.
	deadline := time.Now().Add(10 * time.Second)
	for {
		err = db.QueryRow("PRAGMA journal_mode").Scan(&journalMode)
		var sqliteError *sqlite.Error
		if !errors.As(err, &sqliteError) || sqliteError.Code()&0xff != sqlite3.SQLITE_BUSY || !time.Now().Before(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		err = fmt.Errorf("verify scan database WAL mode: %w", err)
	} else if !strings.EqualFold(journalMode, "wal") {
		err = fmt.Errorf("scan database requires WAL journal mode, got %q", journalMode)
	}
	var version int
	if err == nil {
		if err = db.QueryRow("PRAGMA user_version").Scan(&version); err == nil && version != 0 && version != 1 {
			err = fmt.Errorf("unsupported scan schema %d", version)
		}
	}
	if err == nil {
		_, err = db.Exec(scanSchema)
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the store's connection. Wait for Scan to return first.
func (s *ScanStore) Close() error { return s.db.Close() }

const scanSchema = `
BEGIN IMMEDIATE;
CREATE TABLE IF NOT EXISTS run (
 id INTEGER PRIMARY KEY CHECK(id=1), seed_hash TEXT NOT NULL, job TEXT NOT NULL, build TEXT NOT NULL, prow TEXT NOT NULL,
 start REAL NOT NULL, end REAL NOT NULL, platform_start REAL NOT NULL, platform_end REAL NOT NULL,
 context_json TEXT NOT NULL, planning_state TEXT NOT NULL, state TEXT NOT NULL DEFAULT 'active',
 max_workers INTEGER NOT NULL DEFAULT 4 CHECK(max_workers BETWEEN 1 AND 4), created_at REAL NOT NULL
);
CREATE TABLE IF NOT EXISTS window (id INTEGER PRIMARY KEY, run_id INTEGER NOT NULL REFERENCES run(id), kind TEXT UNIQUE NOT NULL, start REAL NOT NULL, end REAL NOT NULL);
CREATE TABLE IF NOT EXISTS workspace (
 id INTEGER PRIMARY KEY, run_id INTEGER NOT NULL REFERENCES run(id), arm_id TEXT NOT NULL UNIQUE, name TEXT NOT NULL,
 endpoint TEXT NOT NULL UNIQUE, cooldown_until REAL NOT NULL DEFAULT 0, concurrency INTEGER NOT NULL DEFAULT 1 CHECK(concurrency BETWEEN 1 AND 2),
 success_streak INTEGER NOT NULL DEFAULT 0, blocked_reason TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS metric (id INTEGER PRIMARY KEY, workspace_id INTEGER NOT NULL REFERENCES workspace(id), name TEXT NOT NULL, display_name TEXT NOT NULL, catalog INTEGER NOT NULL DEFAULT 1, UNIQUE(workspace_id,name));
CREATE TABLE IF NOT EXISTS query (
 id TEXT PRIMARY KEY, workspace_id INTEGER NOT NULL REFERENCES workspace(id), metric_id INTEGER REFERENCES metric(id), window_id INTEGER REFERENCES window(id),
 kind TEXT NOT NULL, ranking INTEGER NOT NULL DEFAULT 0, path TEXT NOT NULL, params TEXT NOT NULL, expression TEXT NOT NULL,
 evaluation_time REAL, lookback_seconds REAL, grouping TEXT NOT NULL DEFAULT '',
 state TEXT NOT NULL CHECK(state IN ('pending','running','retry_wait','succeeded','blocked')),
 retry_at REAL NOT NULL DEFAULT 0, owner TEXT, lease_until REAL, current_attempt_id INTEGER REFERENCES attempt(id),
 accepted_attempt_id INTEGER REFERENCES attempt(id), attempt_count INTEGER NOT NULL DEFAULT 0, first_attempt_at REAL,
 classification TEXT NOT NULL DEFAULT '', UNIQUE(workspace_id,path,params)
);
CREATE TABLE IF NOT EXISTS attempt (
 id INTEGER PRIMARY KEY, query_id TEXT NOT NULL REFERENCES query(id), owner TEXT NOT NULL, started_at REAL NOT NULL, finished_at REAL,
 state TEXT NOT NULL, http_status INTEGER NOT NULL DEFAULT 0, response_bytes INTEGER NOT NULL DEFAULT 0, duration_ms INTEGER NOT NULL DEFAULT 0,
 api_cost REAL, request_id TEXT NOT NULL DEFAULT '', correlation_id TEXT NOT NULL DEFAULT '', classification TEXT NOT NULL DEFAULT '',
 error_gzip BLOB, error_truncated INTEGER NOT NULL DEFAULT 0, warnings_json TEXT NOT NULL DEFAULT '[]', stats_json TEXT NOT NULL DEFAULT '{}'
);
CREATE TABLE IF NOT EXISTS label_name (id INTEGER PRIMARY KEY, name TEXT UNIQUE NOT NULL);
CREATE TABLE IF NOT EXISTS label_value (id INTEGER PRIMARY KEY, value TEXT UNIQUE NOT NULL);
CREATE TABLE IF NOT EXISTS labelset (id INTEGER PRIMARY KEY, hash TEXT UNIQUE NOT NULL, canonical TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS labelset_member (labelset_id INTEGER NOT NULL REFERENCES labelset(id), name_id INTEGER NOT NULL REFERENCES label_name(id), value_id INTEGER NOT NULL REFERENCES label_value(id), PRIMARY KEY(labelset_id,name_id)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS observation (
 attempt_id INTEGER NOT NULL REFERENCES attempt(id), labelset_id INTEGER NOT NULL REFERENCES labelset(id), timestamp REAL NOT NULL,
 value REAL, integer_value INTEGER, invalid_value TEXT, PRIMARY KEY(attempt_id,labelset_id,timestamp)
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS evidence (
 id TEXT PRIMARY KEY, workspace_id INTEGER REFERENCES workspace(id), kind TEXT NOT NULL, url TEXT NOT NULL,
 ok INTEGER NOT NULL, http_status INTEGER, started_at TEXT NOT NULL, finished_at TEXT NOT NULL, duration_ms REAL NOT NULL,
 response_bytes REAL, api_cost REAL, error_gzip BLOB, error_truncated INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS platform_observation (
 evidence_id TEXT NOT NULL REFERENCES evidence(id), metric TEXT NOT NULL, labelset_id INTEGER NOT NULL REFERENCES labelset(id),
 timestamp REAL NOT NULL, value REAL, invalid_value TEXT, PRIMARY KEY(evidence_id,metric,labelset_id,timestamp)
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS workspace_identity (
 workspace_id INTEGER PRIMARY KEY REFERENCES workspace(id), account_id TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS catalog_observation (
 id INTEGER PRIMARY KEY, workspace_id INTEGER NOT NULL REFERENCES workspace(id), seed_hash TEXT NOT NULL,
 evidence_id TEXT NOT NULL REFERENCES evidence(id), metric_count INTEGER NOT NULL, observed_at REAL NOT NULL
);
CREATE TABLE IF NOT EXISTS catalog_refresh (
 id INTEGER PRIMARY KEY, seed_hash TEXT NOT NULL, spec_fingerprint TEXT NOT NULL, created_at REAL NOT NULL,
 workspace_count INTEGER NOT NULL, added_metrics INTEGER NOT NULL, retried_empty INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS query_schedule ON query(state,retry_at,workspace_id);
CREATE INDEX IF NOT EXISTS query_metric ON query(metric_id,kind);
CREATE INDEX IF NOT EXISTS attempt_query ON attempt(query_id);
CREATE VIEW IF NOT EXISTS accepted_observation AS
 SELECT q.id AS query_id,q.workspace_id,q.metric_id,q.kind,q.ranking,o.* FROM query q JOIN observation o ON o.attempt_id=q.accepted_attempt_id WHERE q.state='succeeded';
PRAGMA user_version=1;
COMMIT;
`

// DBSummary distinguishes finished scheduling from complete ranking coverage.
// NextWake is a Unix timestamp, zero when no delayed or leased work remains.
type DBSummary struct {
	State              string         `json:"state"`
	PlanningState      string         `json:"planningState"`
	Workspaces         int            `json:"workspaces"`
	Metrics            int            `json:"metrics"`
	EmptyCatalogs      int            `json:"emptyCatalogs"`
	Queries            map[string]int `json:"queries"`
	Blocked            map[string]int `json:"blocked"`
	RankingTotal       int            `json:"rankingTotal"`
	RankingSucceeded   int            `json:"rankingSucceeded"`
	Attempts           int            `json:"attempts"`
	Observations       int            `json:"observations"`
	CoverageComplete   bool           `json:"coverageComplete"`
	SchedulingComplete bool           `json:"schedulingComplete"`
	NextWake           float64        `json:"nextWake"`
}

// Summary reports durable scheduling state without credentials or HTTP requests.
func (s *ScanStore) Summary(ctx context.Context) (DBSummary, error) {
	// The writer connection uses BEGIN IMMEDIATE. Status uses ReadScanSummary
	// instead, so observing a live scan never competes for its writer lock.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DBSummary{}, err
	}
	defer func() { _ = tx.Rollback() }()
	return readScanSummary(ctx, tx)
}

type scanSummaryQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// ReadScanSummary opens an existing database read-only and reports a single
// deferred read snapshot. It never initializes or migrates a database.
func ReadScanSummary(ctx context.Context, path string) (DBSummary, error) {
	if path == "" {
		return DBSummary{}, errors.New("database path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return DBSummary{}, err
	}
	u := url.URL{Scheme: "file", Path: abs}
	u.RawQuery = url.Values{"mode": {"ro"}, "_pragma": {"query_only(1)", "busy_timeout(10000)"}}.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return DBSummary{}, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return DBSummary{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := validateScanSchema(ctx, tx); err != nil {
		return DBSummary{}, err
	}
	return readScanSummary(ctx, tx)
}

func validateScanSchema(ctx context.Context, q scanSummaryQuerier) error {
	var version, knownRun int
	if err := q.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version != 1 {
		return fmt.Errorf("unsupported scan schema %d (want 1)", version)
	}
	if err := q.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE type='table' AND name='run'`).Scan(&knownRun); err != nil {
		return err
	}
	if knownRun != 1 {
		return errors.New("not a scan database: missing run table")
	}
	var runID int
	return q.QueryRowContext(ctx, `SELECT id FROM run WHERE id=1 AND seed_hash IS NOT NULL AND context_json IS NOT NULL AND planning_state IS NOT NULL AND state IS NOT NULL`).Scan(&runID)
}

func readScanSummary(ctx context.Context, q scanSummaryQuerier) (DBSummary, error) {
	r := DBSummary{Queries: map[string]int{}, Blocked: map[string]int{}}
	err := q.QueryRowContext(ctx, `SELECT state,planning_state,(SELECT count(*) FROM workspace),(SELECT count(*) FROM metric WHERE catalog=1),
 (SELECT count(*) FROM query WHERE ranking=1),(SELECT count(*) FROM query WHERE ranking=1 AND state='succeeded'),
 (SELECT count(*) FROM attempt),(SELECT count(*) FROM accepted_observation) FROM run WHERE id=1`).Scan(&r.State, &r.PlanningState, &r.Workspaces, &r.Metrics, &r.RankingTotal, &r.RankingSucceeded, &r.Attempts, &r.Observations)
	if err != nil {
		return r, err
	}
	if err := q.QueryRowContext(ctx, `SELECT count(*) FROM workspace w WHERE NOT EXISTS(SELECT 1 FROM metric m WHERE m.workspace_id=w.id AND m.catalog=1)`).Scan(&r.EmptyCatalogs); err != nil {
		return r, err
	}
	rows, err := q.QueryContext(ctx, `SELECT state,count(*) FROM query GROUP BY state`)
	if err != nil {
		return r, err
	}
	for rows.Next() {
		var state string
		var n int
		if err := rows.Scan(&state, &n); err != nil {
			rows.Close()
			return r, err
		}
		r.Queries[state] = n
	}
	err = errors.Join(rows.Err(), rows.Close())
	if err != nil {
		return r, err
	}
	blockedRows, err := q.QueryContext(ctx, `SELECT classification,count(*) FROM query WHERE state='blocked' GROUP BY classification`)
	if err != nil {
		return r, err
	}
	for blockedRows.Next() {
		var classification string
		var n int
		if err := blockedRows.Scan(&classification, &n); err != nil {
			blockedRows.Close()
			return r, err
		}
		r.Blocked[classification] = n
	}
	if err := errors.Join(blockedRows.Err(), blockedRows.Close()); err != nil {
		return r, err
	}
	err = q.QueryRowContext(ctx, `SELECT coalesce(min(CASE WHEN q.state='running' THEN q.lease_until ELSE max(q.retry_at,w.cooldown_until) END),0) FROM query q JOIN workspace w ON w.id=q.workspace_id WHERE q.state IN ('pending','running','retry_wait')`).Scan(&r.NextWake)
	r.CoverageComplete = r.PlanningState == "complete" && r.EmptyCatalogs == 0 && r.RankingTotal > 0 && r.RankingSucceeded == r.RankingTotal
	r.SchedulingComplete = r.State == "complete" && r.PlanningState == "complete" && r.Queries["pending"] == 0 && r.Queries["running"] == 0 && r.Queries["retry_wait"] == 0
	return r, err
}

func scanHash(v string) string     { sum := sha256.Sum256([]byte(v)); return hex.EncodeToString(sum[:]) }
func scanTime(t time.Time) float64 { return float64(t.UnixNano()) / 1e9 }

// Canonical identities include lengths through JSON encoding, not separators.
// Only the hash is persisted; normalized membership verifies collisions without
// repeating label strings in each labelset. Legacy canonical JSON is not consulted;
// explicit offline compaction validates membership before clearing those copies.
func internLabels(ctx context.Context, tx *sql.Tx, labels map[string]string) (int64, error) {
	canonicalLabels := make(map[string]string, len(labels))
	for k, v := range labels {
		key := strings.ToLower(k)
		if _, ok := canonicalLabels[key]; ok {
			return 0, errors.New("case-colliding label names")
		}
		canonicalLabels[key] = strings.ToLower(v)
	}
	b, _ := json.Marshal(canonicalLabels)
	canonical := string(b)
	hash := scanHash(canonical)
	res, err := tx.ExecContext(ctx, `INSERT INTO labelset(hash,canonical) VALUES(?,'') ON CONFLICT(hash) DO NOTHING`, hash)
	if err != nil {
		return 0, err
	}
	var id int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM labelset WHERE hash=?`, hash).Scan(&id); err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if n == 0 {
		rows, err := tx.QueryContext(ctx, `SELECT n.name,v.value FROM labelset_member m JOIN label_name n ON n.id=m.name_id JOIN label_value v ON v.id=m.value_id WHERE m.labelset_id=?`, id)
		if err != nil {
			return 0, err
		}
		defer rows.Close()
		count := 0
		for rows.Next() {
			var name, value string
			if err := rows.Scan(&name, &value); err != nil {
				return 0, err
			}
			if expected, ok := canonicalLabels[name]; !ok || expected != value {
				return 0, errors.New("labelset hash collision or corrupt membership")
			}
			count++
		}
		if err := rows.Err(); err != nil {
			return 0, err
		}
		if count != len(canonicalLabels) {
			return 0, errors.New("labelset hash collision or incomplete membership")
		}
		return id, nil
	}
	keys := make([]string, 0, len(canonicalLabels))
	for key := range canonicalLabels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, err := tx.ExecContext(ctx, `INSERT INTO label_name(name) VALUES(?) ON CONFLICT DO NOTHING`, key); err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO label_value(value) VALUES(?) ON CONFLICT DO NOTHING`, canonicalLabels[key]); err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO labelset_member SELECT ?,n.id,v.id FROM label_name n,label_value v WHERE n.name=? AND v.value=?`, id, key, canonicalLabels[key]); err != nil {
			return 0, err
		}
	}
	return id, nil
}
