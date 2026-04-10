// Copyright 2025 Microsoft Corporation
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

package store

import (
	"context"
	"database/sql"
	"fmt"
)

// Job represents a CI job row.
type Job struct {
	ID         int64
	Env        string
	JobType    string
	JobName    string
	BuildID    string
	BaseURL    string
	Revision   string
	PRNumber   int
	State      string
	StartedAt  string
	FinishedAt string
	IngestedAt string
}

// UpsertJob inserts a job or updates it if the build_id already exists.
func (s *Store) UpsertJob(ctx context.Context, j *Job) (int64, error) {
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO jobs (env, job_type, job_name, build_id, base_url, revision, pr_number, state, started_at, finished_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(build_id) DO UPDATE SET
			state = excluded.state,
			revision = excluded.revision,
			finished_at = excluded.finished_at
	`, j.Env, j.JobType, j.JobName, j.BuildID, j.BaseURL,
		nullString(j.Revision), nullInt(j.PRNumber),
		j.State, nullString(j.StartedAt), nullString(j.FinishedAt))
	if err != nil {
		return 0, fmt.Errorf("upserting job %s: %w", j.BuildID, err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	// If this was an update, LastInsertId may be 0; look up the real ID
	if id == 0 {
		err = s.db.QueryRowContext(ctx, "SELECT id FROM jobs WHERE build_id = ?", j.BuildID).Scan(&id)
		if err != nil {
			return 0, fmt.Errorf("looking up job ID for %s: %w", j.BuildID, err)
		}
	}
	return id, nil
}

// HasBuild checks if a build ID already exists in the database.
func (s *Store) HasBuild(ctx context.Context, buildID string) (bool, error) {
	var exists int
	err := s.db.QueryRowContext(ctx,
		"SELECT 1 FROM jobs WHERE build_id = ? LIMIT 1", buildID).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// JobFilter specifies criteria for listing jobs.
type JobFilter struct {
	Env     string
	JobType string
	Since   string // RFC 3339 timestamp
	Until   string // RFC 3339 timestamp
	PR      int
	Limit   int
}

// ListJobs returns jobs matching the filter, ordered by started_at descending.
func (s *Store) ListJobs(ctx context.Context, f JobFilter) ([]Job, error) {
	query := "SELECT id, env, job_type, job_name, build_id, base_url, COALESCE(revision,''), COALESCE(pr_number,0), state, COALESCE(started_at,''), COALESCE(finished_at,''), ingested_at FROM jobs WHERE 1=1"
	var args []any

	if f.Env != "" {
		query += " AND env = ?"
		args = append(args, f.Env)
	}
	if f.JobType != "" {
		query += " AND job_type = ?"
		args = append(args, f.JobType)
	}
	if f.Since != "" {
		query += " AND started_at >= ?"
		args = append(args, f.Since)
	}
	if f.Until != "" {
		query += " AND started_at <= ?"
		args = append(args, f.Until)
	}
	if f.PR > 0 {
		query += " AND pr_number = ?"
		args = append(args, f.PR)
	}

	query += " ORDER BY started_at DESC"

	if f.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", f.Limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing jobs: %w", err)
	}
	defer rows.Close()

	var jobs []Job
	for rows.Next() {
		var j Job
		if err := rows.Scan(&j.ID, &j.Env, &j.JobType, &j.JobName, &j.BuildID, &j.BaseURL, &j.Revision, &j.PRNumber, &j.State, &j.StartedAt, &j.FinishedAt, &j.IngestedAt); err != nil {
			return nil, fmt.Errorf("scanning job: %w", err)
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// JobStateCounts returns the count of jobs by state for a given env/type/time range.
func (s *Store) JobStateCounts(ctx context.Context, env, jobType, since, until string) (map[string]int, error) {
	query := "SELECT state, COUNT(*) FROM jobs WHERE env = ? AND job_type = ?"
	args := []any{env, jobType}

	if since != "" {
		query += " AND started_at >= ?"
		args = append(args, since)
	}
	if until != "" {
		query += " AND started_at <= ?"
		args = append(args, until)
	}
	query += " GROUP BY state"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var state string
		var count int
		if err := rows.Scan(&state, &count); err != nil {
			return nil, err
		}
		counts[state] = count
	}
	return counts, rows.Err()
}

// PruneOlderThan deletes jobs (and their test results via CASCADE) older than the given timestamp.
// Returns the number of jobs deleted.
func (s *Store) PruneOlderThan(ctx context.Context, before string) (int64, error) {
	result, err := s.db.ExecContext(ctx,
		"DELETE FROM jobs WHERE started_at < ?", before)
	if err != nil {
		return 0, fmt.Errorf("pruning old jobs: %w", err)
	}
	return result.RowsAffected()
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullInt(n int) any {
	if n == 0 {
		return nil
	}
	return n
}
