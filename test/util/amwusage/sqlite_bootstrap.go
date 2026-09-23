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
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"github.com/google/uuid"
)

const bootstrapChunkSize = 256 << 10

// Additive schema-1 metadata, created only when initialization needs it. A
// complete bootstrap is immutable until the plan and its deletion commit together.
const bootstrapSchema = `
CREATE TABLE IF NOT EXISTS bootstrap (
 id INTEGER PRIMARY KEY CHECK(id=1), token TEXT NOT NULL UNIQUE,
 complete INTEGER NOT NULL DEFAULT 0 CHECK(complete IN (0,1)), seed_hash TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS bootstrap_chunk (
 token TEXT NOT NULL REFERENCES bootstrap(token) ON DELETE CASCADE,
 sequence INTEGER NOT NULL, data BLOB NOT NULL,
 PRIMARY KEY(token,sequence)
) WITHOUT ROWID;
`

func (s *ScanStore) stageBootstrap(ctx context.Context, seed io.Reader) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, bootstrapSchema); err != nil {
		return err
	}
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT seed_hash FROM run WHERE id=1 UNION ALL SELECT seed_hash FROM bootstrap WHERE complete=1 LIMIT 1`).Scan(&existing)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	token := uuid.NewString()
	if errors.Is(err, sql.ErrNoRows) {
		// Replacing an interrupted upload fences its writer at the next chunk.
		if _, err := tx.ExecContext(ctx, `DELETE FROM bootstrap`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO bootstrap(id,token) VALUES(1,?)`, token); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	hash := sha256.New()
	limited := &io.LimitedReader{R: seed, N: (512 << 20) + 1}
	reader := io.TeeReader(limited, hash)
	destination := io.Discard
	var zipped *gzip.Writer
	var buffered *bufio.Writer
	if existing == "" {
		buffered = bufio.NewWriterSize(&bootstrapWriter{ctx: ctx, db: s.db, token: token}, bootstrapChunkSize)
		zipped = gzip.NewWriter(buffered)
		destination = zipped
	}
	// Check cancellation even for highly compressible input, which may produce
	// no full compressed chunk for many source reads.
	buffer := make([]byte, 32<<10)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, readErr := reader.Read(buffer)
		if _, err := destination.Write(buffer[:n]); err != nil {
			return err
		}
		if limited.N == 0 {
			return errors.New("seed exceeds 512 MiB")
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	fingerprint := hex.EncodeToString(hash.Sum(nil))
	if existing != "" {
		if existing != fingerprint {
			return errors.New("seed fingerprint differs from initialized database or registered bootstrap")
		}
		return nil
	}
	if err := zipped.Close(); err != nil {
		return err
	}
	if err := buffered.Flush(); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE bootstrap SET complete=1,seed_hash=? WHERE id=1 AND token=? AND complete=0`, fingerprint, token)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		err = errors.New("bootstrap upload superseded")
	}
	return err
}

type bootstrapWriter struct {
	ctx      context.Context
	db       *sql.DB
	token    string
	sequence int
}

func (w *bootstrapWriter) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		n := min(len(p), bootstrapChunkSize)
		// Each insert is an independent, fully synced transaction. Never retain
		// a source-sized buffer or an unbounded upload transaction.
		result, err := w.db.ExecContext(w.ctx, `INSERT INTO bootstrap_chunk(token,sequence,data) SELECT token,?,? FROM bootstrap WHERE token=? AND complete=0`, w.sequence, p[:n], w.token)
		if err != nil {
			return written, err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return written, err
		}
		if rows != 1 {
			return written, errors.New("bootstrap upload superseded")
		}
		w.sequence++
		written += n
		p = p[n:]
	}
	return written, nil
}

func (s *ScanStore) resumeBootstrap(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var planning string
	err = tx.QueryRowContext(ctx, `SELECT planning_state FROM run WHERE id=1`).Scan(&planning)
	if err == nil && planning == "complete" {
		return nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err := tx.ExecContext(ctx, bootstrapSchema); err != nil {
		return err
	}
	var token, fingerprint string
	if err := tx.QueryRowContext(ctx, `SELECT token,seed_hash FROM bootstrap WHERE id=1 AND complete=1`).Scan(&token, &fingerprint); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("initialize scan with --input: seed registration is missing or incomplete")
		}
		return err
	}
	reader, err := gzip.NewReader(&bootstrapReader{ctx: ctx, tx: tx, token: token})
	if err != nil {
		return fmt.Errorf("read registered bootstrap: %w", err)
	}
	defer reader.Close()
	if err := importScanSeed(ctx, tx, reader, fingerprint); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM bootstrap WHERE id=1`); err != nil {
		return err
	}
	return tx.Commit()
}

type bootstrapReader struct {
	ctx      context.Context
	tx       *sql.Tx
	token    string
	sequence int
	chunk    []byte
}

func (r *bootstrapReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if len(r.chunk) == 0 {
		err := r.tx.QueryRowContext(r.ctx, `SELECT data FROM bootstrap_chunk WHERE token=? AND sequence=?`, r.token, r.sequence).Scan(&r.chunk)
		if errors.Is(err, sql.ErrNoRows) {
			return 0, io.EOF
		}
		if err != nil {
			return 0, err
		}
		r.sequence++
	}
	n := copy(p, r.chunk)
	r.chunk = r.chunk[n:]
	return n, nil
}
