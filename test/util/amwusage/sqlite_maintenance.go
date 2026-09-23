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
	"net/url"
	"path/filepath"
)

// PruneScanStore compacts an existing, completed scan artifact without removing
// observations, attempts, errors or provenance. Stop scanners and close their
// stores and all readers first. This is explicit maintenance, never scan cleanup.
// The exclusive connection retains its lock between batches and through VACUUM,
// preventing a new scanner from claiming work after the durable-state check.
func PruneScanStore(ctx context.Context, path string) error {
	if path == "" {
		return errors.New("database path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	u := url.URL{Scheme: "file", Path: abs}
	u.RawQuery = url.Values{"mode": {"rw"}, "_pragma": {"foreign_keys(1)", "busy_timeout(0)", "locking_mode(EXCLUSIVE)"}, "_txlock": {"immediate"}}.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("compact requires exclusive database access: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := validateScanSchema(ctx, tx); err != nil {
		return err
	}
	var ready bool
	if err := tx.QueryRowContext(ctx, `SELECT state='complete' AND planning_state='complete'
 AND NOT EXISTS (SELECT 1 FROM query WHERE state NOT IN ('succeeded','blocked') OR owner IS NOT NULL OR lease_until IS NOT NULL OR current_attempt_id IS NOT NULL)
 AND NOT EXISTS (SELECT 1 FROM attempt WHERE state='running' OR finished_at IS NULL)
 FROM run WHERE id=1`).Scan(&ready); err != nil {
		return err
	}
	if !ready {
		return errors.New("compact requires a completed scan with no active work, leases or unfinished attempts")
	}
	// Every observation counts, not just accepted observations: staging and
	// platform references must never lose their normalized dictionaries.
	if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE orphan_labelset AS SELECT id FROM labelset WHERE id NOT IN (SELECT labelset_id FROM observation UNION SELECT labelset_id FROM platform_observation)`); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	for {
		n, err := pruneScanDictionaryBatch(ctx, db)
		if err != nil {
			return err
		}
		if n == 0 {
			break
		}
	}
	var afterID int64
	for {
		lastID, err := clearScanCanonicalBatch(ctx, db, afterID)
		if err != nil {
			return err
		}
		if lastID == 0 {
			break
		}
		afterID = lastID
	}
	checkpoint := func() error {
		var busy, log, checkpointed int
		if err := db.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &log, &checkpointed); err != nil {
			return err
		}
		if busy != 0 {
			return errors.New("compact WAL checkpoint blocked by an active transaction")
		}
		return nil
	}
	if err := checkpoint(); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `VACUUM`); err != nil {
		return err
	}
	return checkpoint()
}

func pruneScanDictionaryBatch(ctx context.Context, db *sql.DB) (int64, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	var removed int64
	for _, statement := range []string{
		`DELETE FROM labelset_member WHERE labelset_id IN (SELECT id FROM orphan_labelset ORDER BY rowid LIMIT 512)`,
		`DELETE FROM labelset WHERE id IN (SELECT id FROM orphan_labelset ORDER BY rowid LIMIT 512)`,
		`DELETE FROM orphan_labelset WHERE rowid IN (SELECT rowid FROM orphan_labelset ORDER BY rowid LIMIT 512)`,
		`DELETE FROM label_name WHERE id IN (SELECT id FROM label_name WHERE id NOT IN (SELECT name_id FROM labelset_member) LIMIT 512)`,
		`DELETE FROM label_value WHERE id IN (SELECT id FROM label_value WHERE id NOT IN (SELECT value_id FROM labelset_member) LIMIT 512)`,
	} {
		res, err := tx.ExecContext(ctx, statement)
		if err != nil {
			return 0, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return 0, err
		}
		removed += n
	}
	return removed, tx.Commit()
}

func clearScanCanonicalBatch(ctx context.Context, db *sql.DB, afterID int64) (int64, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `SELECT id FROM labelset WHERE id>? AND canonical<>'' ORDER BY id LIMIT 512`, afterID)
	if err != nil {
		return 0, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return 0, err
	}
	for _, id := range ids {
		var hash, canonical string
		if err := tx.QueryRowContext(ctx, `SELECT hash,canonical FROM labelset WHERE id=?`, id).Scan(&hash, &canonical); err != nil {
			return 0, err
		}
		rows, err := tx.QueryContext(ctx, `SELECT n.name,v.value FROM labelset_member m LEFT JOIN label_name n ON n.id=m.name_id LEFT JOIN label_value v ON v.id=m.value_id WHERE m.labelset_id=?`, id)
		if err != nil {
			return 0, err
		}
		labels := map[string]string{}
		for rows.Next() {
			var name, value string
			if err := rows.Scan(&name, &value); err != nil {
				rows.Close()
				return 0, err
			}
			labels[name] = value
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return 0, err
		}
		encoded, err := json.Marshal(labels)
		if err != nil {
			return 0, err
		}
		if string(encoded) != canonical || scanHash(string(encoded)) != hash {
			return 0, fmt.Errorf("labelset %d: canonical/hash does not match normalized membership", id)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE labelset SET canonical='' WHERE id=?`, id); err != nil {
			return 0, err
		}
	}
	var lastID int64
	if len(ids) > 0 {
		lastID = ids[len(ids)-1]
	}
	return lastID, tx.Commit()
}
