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
	"database/sql/driver"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"testing"
	"time"
)

func assertScanWALSettings(t *testing.T, db *sql.DB) {
	t.Helper()
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for pragma, want := range map[string]string{
		"journal_mode": "wal", "busy_timeout": "10000", "synchronous": "2", "foreign_keys": "1",
	} {
		var got string
		if err := conn.QueryRowContext(context.Background(), "PRAGMA "+pragma).Scan(&got); err != nil || got != want {
			t.Errorf("PRAGMA %s = %q, want %q: %v", pragma, got, want, err)
		}
	}
}

func TestSQLiteWALConnectionSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scan.db")
	for range 2 {
		s, err := OpenScanStore(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		assertScanWALSettings(t, s.db)
		conn, err := s.db.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		// Force database/sql to discard the physical connection, not just idle it.
		if err := conn.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
			t.Fatalf("discard connection: %v", err)
		}
		_ = conn.Close()
		if got := s.db.Stats().OpenConnections; got != 0 {
			t.Fatalf("discarded connection still pooled: %d", got)
		}
		assertScanWALSettings(t, s.db)
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSQLiteWALConcurrentOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scan.db")
	start := make(chan struct{})
	type result struct {
		store *ScanStore
		err   error
	}
	done := make(chan result, 4)
	for range 4 {
		go func() {
			<-start
			s, err := OpenScanStore(path)
			done <- result{s, err}
		}()
	}
	close(start)
	for range 4 {
		r := <-done
		if r.err != nil {
			t.Errorf("concurrent fresh database open: %v", r.err)
			continue
		}
		t.Cleanup(func() { r.store.Close() })
		assertScanWALSettings(t, r.store.db)
		var version, tables int
		if err := r.store.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != 1 {
			t.Errorf("schema version = %d, want 1: %v", version, err)
		}
		if err := r.store.db.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE type='table' AND name IN ('labelset','observation')`).Scan(&tables); err != nil || tables != 2 {
			t.Errorf("schema tables = %d, want 2: %v", tables, err)
		}
	}
}

func TestSQLiteWALWriterWaits(t *testing.T) {
	s, path := newScanTestStore(t)
	other, err := OpenScanStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	type result struct {
		tx  *sql.Tx
		err error
	}
	started, done := make(chan struct{}), make(chan result, 1)
	go func() {
		close(started)
		waiting, err := other.db.Begin()
		done <- result{waiting, err}
	}()
	<-started
	select {
	case r := <-done:
		if r.tx != nil {
			_ = r.tx.Rollback()
		}
		t.Fatalf("second BEGIN returned before lock release: %v", r.err)
	case <-time.After(200 * time.Millisecond):
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("waiting BEGIN failed after lock release: %v", r.err)
		}
		defer func() { _ = r.tx.Rollback() }()
		if _, err := r.tx.Exec(`INSERT INTO label_name(name) VALUES('waited')`); err != nil {
			t.Fatal(err)
		}
		if err := r.tx.Commit(); err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("writer did not resume after lock release")
	}
	var count int
	if err := s.db.QueryRow(`SELECT count(*) FROM label_name WHERE name='waited'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("waiting writer did not commit: count=%d: %v", count, err)
	}
}

func TestSQLiteWALConcurrentStagingWithSnapshot(t *testing.T) {
	s, path := newScanTestStore(t)
	ctx := context.Background()
	const writers, batches = 4, 8
	stores := []*ScanStore{s}
	for range writers - 1 {
		other, err := OpenScanStore(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { other.Close() })
		stores = append(stores, other)
	}
	attempts := make([]int64, writers)
	for i := range attempts {
		res, err := s.db.Exec(`INSERT INTO attempt(query_id,owner,started_at,state) SELECT id,?,0,'running' FROM query WHERE state='pending' LIMIT 1`, fmt.Sprint(i))
		if err != nil {
			t.Fatal(err)
		}
		attempts[i], err = res.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
	}
	u := url.URL{Scheme: "file", Path: path, RawQuery: url.Values{"mode": {"ro"}, "_pragma": {"query_only(1)", "busy_timeout(10000)"}}.Encode()}
	reader, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	snapshot, err := reader.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = snapshot.Rollback() }()
	var before int
	// A SELECT, not just BEGIN, pins the WAL read snapshot.
	if err := snapshot.QueryRow(`SELECT count(*) FROM observation`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	start, done := make(chan struct{}), make(chan error, writers)
	for i, store := range stores {
		go func() {
			<-start
			for batch := range batches {
				tx, err := store.db.BeginTx(ctx, nil)
				if err != nil {
					done <- err
					return
				}
				value := int64(7)
				err = insertObservations(ctx, tx, attempts[i], []scanObservation{
					{labels: map[string]string{"Cluster": "ABC", "job": "Shared"}, at: float64(batch), integer: &value},
					{labels: map[string]string{"cluster": "abc", "job": fmt.Sprintf("writer-%d-batch-%d", i, batch)}, at: float64(batch), integer: &value},
				})
				if err == nil {
					err = tx.Commit()
				}
				_ = tx.Rollback()
				if err != nil {
					done <- fmt.Errorf("writer %d batch %d: %w", i, batch, err)
					return
				}
			}
			done <- nil
		}()
	}
	close(start)
	for range writers {
		if err := <-done; err != nil {
			t.Errorf("staging with read snapshot held: %v", err)
		}
	}
	var old, current, sets, members, names, values int
	if err := snapshot.QueryRow(`SELECT count(*) FROM observation`).Scan(&old); err != nil || old != before {
		t.Fatalf("read snapshot changed: before=%d after=%d: %v", before, old, err)
	}
	if err := snapshot.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := reader.QueryRow(`SELECT count(*) FROM observation`).Scan(&current); err != nil || current != before+writers*batches*2 {
		t.Fatalf("fresh reader observation count=%d, want %d: %v", current, before+writers*batches*2, err)
	}
	if err := s.db.QueryRow(`SELECT (SELECT count(*) FROM labelset),(SELECT count(*) FROM labelset_member),(SELECT count(*) FROM label_name),(SELECT count(*) FROM label_value)`).Scan(&sets, &members, &names, &values); err != nil {
		t.Fatal(err)
	}
	if sets != 2+writers*batches || members != sets*2 || names != 2 || values != 3+writers*batches {
		t.Fatalf("dictionary not shared correctly: sets=%d members=%d names=%d values=%d", sets, members, names, values)
	}
	for _, attempt := range attempts {
		var count, total int
		if err := s.db.QueryRow(`SELECT count(*),sum(integer_value) FROM observation WHERE attempt_id=?`, attempt).Scan(&count, &total); err != nil || count != batches*2 || total != batches*2*7 {
			t.Errorf("attempt %d: observations=%d total=%d: %v", attempt, count, total, err)
		}
	}
}
