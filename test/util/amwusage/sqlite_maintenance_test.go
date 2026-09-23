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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadScanSummaryReadOnly(t *testing.T) {
	ctx := context.Background()
	missing := filepath.Join(t.TempDir(), "missing.db")
	if _, err := ReadScanSummary(ctx, missing); err == nil {
		t.Fatal("absent database accepted")
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("status created database: %v", err)
	}
	s, path := newScanTestStore(t)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`UPDATE query SET state='blocked' WHERE state='pending'`); err != nil {
		t.Fatal(err)
	}
	readCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	r, err := ReadScanSummary(readCtx, path)
	if err != nil || r.Queries["pending"] == 0 || r.Queries["blocked"] != 0 {
		t.Fatalf("read-only status blocked on writer or saw uncommitted data: %+v %v", r, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0400); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadScanSummary(ctx, path); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("status modified database: %v", err)
	}
}

func TestReadScanSummaryRejectsSchema(t *testing.T) {
	for _, schema := range []string{`CREATE TABLE unrelated(x);`, `PRAGMA user_version=1; CREATE TABLE unrelated(x);`, `PRAGMA user_version=2;`} {
		t.Run(schema, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "other.db")
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(schema); err != nil {
				t.Fatal(err)
			}
			db.Close()
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ReadScanSummary(context.Background(), path); err == nil {
				t.Fatal("unrelated schema accepted")
			}
			if err := PruneScanStore(context.Background(), path); err == nil {
				t.Fatal("unrelated schema compacted")
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("unrelated database modified: %v", err)
			}
		})
	}
}

// Commit between summary statements to verify they all use the original snapshot.
type summaryConcurrentWriter struct {
	*sql.Tx
	write func()
}

func (q *summaryConcurrentWriter) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if q.write != nil {
		q.write()
		q.write = nil
	}
	return q.Tx.QueryContext(ctx, query, args...)
}

func TestReadScanSummaryConsistentSnapshot(t *testing.T) {
	s, path := newScanTestStore(t)
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=query_only(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	r, err := readScanSummary(context.Background(), &summaryConcurrentWriter{Tx: tx, write: func() {
		if _, err := s.db.Exec(`UPDATE query SET state='blocked',classification='test' WHERE state='pending'; UPDATE run SET state='complete'`); err != nil {
			t.Fatal(err)
		}
	}})
	if err != nil || r.State != "active" || r.Queries["pending"] == 0 || len(r.Blocked) != 0 || r.SchedulingComplete {
		t.Fatalf("mixed snapshots: %+v %v", r, err)
	}
	current, err := ReadScanSummary(context.Background(), path)
	if err != nil || !current.SchedulingComplete || current.CoverageComplete || current.Blocked["test"] == 0 {
		t.Fatalf("blocked scheduling is not complete coverage: %+v %v", current, err)
	}
}

func TestPruneScanStoreGuards(t *testing.T) {
	for _, state := range []string{"active", "planning", "pending", "retry_wait", "running", "lease", "owner", "attempt", "current_attempt"} {
		t.Run(state, func(t *testing.T) {
			s, path := newScanTestStore(t)
			if _, err := s.db.Exec(`UPDATE query SET state='blocked' WHERE state='pending'; UPDATE run SET state='complete'`); err != nil {
				t.Fatal(err)
			}
			statement := ""
			switch state {
			case "active":
				statement = `UPDATE run SET state='active'`
			case "planning":
				statement = `UPDATE run SET planning_state='importing'`
			case "pending", "retry_wait", "running":
				statement = `UPDATE query SET state='` + state + `'`
			case "lease":
				statement = `UPDATE query SET lease_until=1`
			case "owner":
				statement = `UPDATE query SET owner='stale'`
			case "attempt":
				statement = `UPDATE attempt SET state='running'`
			case "current_attempt":
				statement = `UPDATE query SET current_attempt_id=(SELECT min(id) FROM attempt)`
			}
			if _, err := s.db.Exec(statement); err != nil {
				t.Fatal(err)
			}
			s.Close()
			if err := PruneScanStore(context.Background(), path); err == nil || !strings.Contains(err.Error(), "completed scan") {
				t.Fatalf("unsafe compaction accepted: %v", err)
			}
		})
	}
}

func TestPruneScanStoreDictionaries(t *testing.T) {
	s, path := newScanTestStore(t)
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, label := range []string{"orphan", "staged", "platform"} {
		id, err := internLabels(ctx, tx, map[string]string{label: label})
		if err != nil {
			t.Fatal(err)
		}
		switch label {
		case "staged":
			if _, err := tx.Exec(`INSERT INTO attempt(id,query_id,owner,started_at,finished_at,state,error_gzip,request_id) SELECT 999,id,'old',0,1,'abandoned',x'1234','provenance' FROM query LIMIT 1`); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(`INSERT INTO observation(attempt_id,labelset_id,timestamp,value) VALUES(999,?,1,2)`, id); err != nil {
				t.Fatal(err)
			}
		case "platform":
			if _, err := tx.Exec(`INSERT INTO evidence(id,kind,url,ok,started_at,finished_at,duration_ms) VALUES('platform','test','url',1,'start','end',0)`); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(`INSERT INTO platform_observation(evidence_id,metric,labelset_id,timestamp,value) VALUES('platform','metric',?,1,2)`, id); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := tx.Exec(`UPDATE labelset SET canonical='{"cluster":"abc","job":"test"}' WHERE id IN (SELECT labelset_id FROM accepted_observation);
 UPDATE query SET state='blocked' WHERE state='pending'; UPDATE run SET state='complete';
 CREATE TABLE unrelated(payload TEXT); INSERT INTO unrelated VALUES('keep');`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if err := PruneScanStore(ctx, path); err != nil {
		t.Fatal(err)
	}
	if err := PruneScanStore(ctx, path); err != nil {
		t.Fatalf("repeat compaction: %v", err)
	}
	if info, err := os.Stat(path + "-wal"); err == nil && info.Size() != 0 {
		t.Fatalf("compaction left nonempty WAL: %d bytes", info.Size())
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for query, want := range map[string]int{
		`SELECT count(*) FROM labelset`:                                                                3,
		`SELECT count(*) FROM labelset WHERE canonical<>''`:                                            0,
		`SELECT count(*) FROM label_name WHERE name='orphan'`:                                          0,
		`SELECT count(*) FROM label_value WHERE value='orphan'`:                                        0,
		`SELECT count(*) FROM observation`:                                                             2,
		`SELECT count(*) FROM platform_observation`:                                                    1,
		`SELECT count(*) FROM attempt WHERE id=999 AND error_gzip=x'1234' AND request_id='provenance'`: 1,
		`SELECT count(*) FROM unrelated WHERE payload='keep'`:                                          1,
	} {
		var got int
		if err := db.QueryRow(query).Scan(&got); err != nil || got != want {
			t.Fatalf("%s: got %d want %d: %v", query, got, want, err)
		}
	}
}

func TestPruneScanStoreRejectsCorruptMembership(t *testing.T) {
	for _, corrupt := range []string{`DELETE FROM labelset_member`, `UPDATE labelset SET hash='bad'`, `UPDATE labelset SET canonical='{}'`} {
		t.Run(corrupt, func(t *testing.T) {
			s, path := newScanTestStore(t)
			if _, err := s.db.Exec(`UPDATE labelset SET canonical='{"cluster":"abc","job":"test"}';
 UPDATE query SET state='blocked' WHERE state='pending'; UPDATE run SET state='complete'`); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(corrupt); err != nil {
				t.Fatal(err)
			}
			s.Close()
			if err := PruneScanStore(context.Background(), path); err == nil || !strings.Contains(err.Error(), "membership") {
				t.Fatalf("corrupt labels cleared: %v", err)
			}
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var retained int
			if err := db.QueryRow(`SELECT count(*) FROM labelset WHERE canonical<>''`).Scan(&retained); err != nil || retained != 1 {
				t.Fatalf("canonical discarded after failed verification: %d %v", retained, err)
			}
		})
	}
}

func TestPruneScanStoreRejectsConcurrentAccess(t *testing.T) {
	for _, mode := range []string{"reader", "writer"} {
		t.Run(mode, func(t *testing.T) {
			s, path := newScanTestStore(t)
			if _, err := s.db.Exec(`UPDATE query SET state='blocked' WHERE state='pending'; UPDATE run SET state='complete'`); err != nil {
				t.Fatal(err)
			}
			db := s.db
			if mode == "reader" {
				s.Close()
				var err error
				db, err = sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=query_only(1)")
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
			}
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback() }()
			var version int
			if err := tx.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
				t.Fatal(err)
			}
			if err := PruneScanStore(context.Background(), path); err == nil {
				t.Fatalf("compaction accepted concurrent %s", mode)
			}
		})
	}
}

func TestPruneScanStoreBatches(t *testing.T) {
	s, path := newScanTestStore(t)
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	for i := range 1030 {
		labels := map[string]string{"batch": fmt.Sprint(i)}
		id, err := internLabels(context.Background(), tx, labels)
		if err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(labels)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`UPDATE labelset SET canonical=? WHERE id=?`, string(b), id); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`INSERT INTO observation(attempt_id,labelset_id,timestamp,value) SELECT min(id),?,1,2 FROM attempt`, id); err != nil {
			t.Fatal(err)
		}
		if _, err := internLabels(context.Background(), tx, map[string]string{fmt.Sprint(i): "orphan"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(`UPDATE query SET state='blocked' WHERE state='pending'; UPDATE run SET state='complete'`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if err := PruneScanStore(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var sets, legacy, names int
	if err := db.QueryRow(`SELECT count(*),sum(canonical<>''),(SELECT count(*) FROM label_name) FROM labelset`).Scan(&sets, &legacy, &names); err != nil || sets != 1031 || legacy != 0 || names != 3 {
		t.Fatalf("incomplete batch cleanup: sets=%d legacy=%d names=%d: %v", sets, legacy, names, err)
	}
}
