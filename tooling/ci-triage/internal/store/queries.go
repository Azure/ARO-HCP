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
	"fmt"
	"strings"
)

// FailureGroupRow represents a failure group from SQL aggregation.
type FailureGroupRow struct {
	TestName  string
	Count     int
	FirstSeen string
	LastSeen  string
}

// FailureGroups returns test failures grouped by test name for an env/type/time range.
// Only considers test failures from jobs with state 'failure' or 'error'.
func (s *Store) FailureGroups(ctx context.Context, env, jobType, since, until string) ([]FailureGroupRow, error) {
	query := `
		SELECT tr.test_name, COUNT(*) as cnt,
		       MIN(j.started_at) as first_seen,
		       MAX(j.started_at) as last_seen
		FROM test_results tr
		JOIN jobs j ON tr.job_id = j.id
		WHERE j.env = ? AND j.job_type = ? AND tr.status = 'failed'
		  AND j.state IN ('failure', 'error')
	`
	args := []any{env, jobType}

	if since != "" {
		query += " AND j.started_at >= ?"
		args = append(args, since)
	}
	if until != "" {
		query += " AND j.started_at <= ?"
		args = append(args, until)
	}
	query += " GROUP BY tr.test_name ORDER BY cnt DESC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying failure groups: %w", err)
	}
	defer rows.Close()

	var groups []FailureGroupRow
	for rows.Next() {
		var g FailureGroupRow
		if err := rows.Scan(&g.TestName, &g.Count, &g.FirstSeen, &g.LastSeen); err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

// OnsetMap returns the last_passed timestamp for each of the given test names.
func (s *Store) OnsetMap(ctx context.Context, env, jobType string, testNames []string) (map[string]string, error) {
	if len(testNames) == 0 {
		return nil, nil
	}

	placeholders := make([]string, len(testNames))
	args := []any{env, jobType}
	for i, name := range testNames {
		placeholders[i] = "?"
		args = append(args, name)
	}

	query := fmt.Sprintf(`
		SELECT tr.test_name, MAX(j.started_at) as last_passed
		FROM test_results tr
		JOIN jobs j ON tr.job_id = j.id
		WHERE j.env = ? AND j.job_type = ? AND tr.status = 'passed'
		  AND tr.test_name IN (%s)
		GROUP BY tr.test_name
	`, strings.Join(placeholders, ","))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying onset map: %w", err)
	}
	defer rows.Close()

	result := make(map[string]string)
	for rows.Next() {
		var name, lastPassed string
		if err := rows.Scan(&name, &lastPassed); err != nil {
			return nil, err
		}
		result[name] = lastPassed
	}
	return result, rows.Err()
}

// DedupedMessageRow represents a deduplicated failure message from SQL.
type DedupedMessageRow struct {
	NormalizedKey  string
	Representative string
	Count          int
}

// FailureMessages returns deduplicated failure messages for a specific test in an env/type/time range.
// Only considers test failures from jobs with state 'failure' or 'error'.
func (s *Store) FailureMessages(ctx context.Context, env, jobType, since, testName string) ([]DedupedMessageRow, error) {
	query := `
		SELECT COALESCE(failure_message_normalized, failure_message) as norm_key,
		       MIN(failure_message) as representative,
		       COUNT(*) as cnt
		FROM test_results tr
		JOIN jobs j ON tr.job_id = j.id
		WHERE j.env = ? AND j.job_type = ? AND tr.test_name = ? AND tr.status = 'failed'
		  AND j.state IN ('failure', 'error')
		  AND failure_message IS NOT NULL AND failure_message != ''
	`
	args := []any{env, jobType, testName}

	if since != "" {
		query += " AND j.started_at >= ?"
		args = append(args, since)
	}
	query += " GROUP BY norm_key ORDER BY cnt DESC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying failure messages: %w", err)
	}
	defer rows.Close()

	var msgs []DedupedMessageRow
	for rows.Next() {
		var m DedupedMessageRow
		if err := rows.Scan(&m.NormalizedKey, &m.Representative, &m.Count); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

// FailureJobURLs returns the short base_urls of jobs where a test failed.
// Only considers test failures from jobs with state 'failure' or 'error'.
func (s *Store) FailureJobURLs(ctx context.Context, env, jobType, since, testName string) ([]string, error) {
	query := `
		SELECT DISTINCT j.base_url
		FROM test_results tr
		JOIN jobs j ON tr.job_id = j.id
		WHERE j.env = ? AND j.job_type = ? AND tr.test_name = ? AND tr.status = 'failed'
		  AND j.state IN ('failure', 'error')
	`
	args := []any{env, jobType, testName}

	if since != "" {
		query += " AND j.started_at >= ?"
		args = append(args, since)
	}
	query += " ORDER BY j.started_at DESC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var urls []string
	for rows.Next() {
		var url string
		if err := rows.Scan(&url); err != nil {
			return nil, err
		}
		urls = append(urls, url)
	}
	return urls, rows.Err()
}

// FailurePRs returns PR numbers associated with failures of a test.
// Only considers test failures from jobs with state 'failure' or 'error'.
func (s *Store) FailurePRs(ctx context.Context, env, jobType, since, testName string) ([]int, error) {
	query := `
		SELECT DISTINCT j.pr_number
		FROM test_results tr
		JOIN jobs j ON tr.job_id = j.id
		WHERE j.env = ? AND j.job_type = ? AND tr.test_name = ? AND tr.status = 'failed'
		  AND j.state IN ('failure', 'error')
		  AND j.pr_number IS NOT NULL
	`
	args := []any{env, jobType, testName}

	if since != "" {
		query += " AND j.started_at >= ?"
		args = append(args, since)
	}
	query += " ORDER BY j.pr_number"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var prs []int
	for rows.Next() {
		var pr int
		if err := rows.Scan(&pr); err != nil {
			return nil, err
		}
		prs = append(prs, pr)
	}
	return prs, rows.Err()
}

// PerJobTestEntry represents per-job test counts.
type PerJobTestEntry struct {
	BaseURL  string
	Started  string
	Revision string
	Passed   int
	Failed   int
}

// PerJobTests returns per-job test pass/fail counts for failed/error jobs in a time range.
func (s *Store) PerJobTests(ctx context.Context, env, jobType, since, until string) ([]PerJobTestEntry, error) {
	query := `
		SELECT j.base_url, j.started_at, COALESCE(j.revision,''),
		       SUM(CASE WHEN tr.status = 'passed' THEN 1 ELSE 0 END) as passed,
		       SUM(CASE WHEN tr.status = 'failed' THEN 1 ELSE 0 END) as failed
		FROM jobs j
		JOIN test_results tr ON tr.job_id = j.id
		WHERE j.env = ? AND j.job_type = ?
		  AND j.state IN ('failure', 'error')
	`
	args := []any{env, jobType}

	if since != "" {
		query += " AND j.started_at >= ?"
		args = append(args, since)
	}
	if until != "" {
		query += " AND j.started_at <= ?"
		args = append(args, until)
	}
	query += " GROUP BY j.id ORDER BY j.started_at"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying per-job tests: %w", err)
	}
	defer rows.Close()

	var entries []PerJobTestEntry
	for rows.Next() {
		var e PerJobTestEntry
		if err := rows.Scan(&e.BaseURL, &e.Started, &e.Revision, &e.Passed, &e.Failed); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// FleetFailureRow represents a test failing across multiple environments.
type FleetFailureRow struct {
	TestName string
	Envs     string // comma-separated
}

// FleetFailures returns tests that fail across multiple environments.
// Only considers test failures from jobs with state 'failure' or 'error'.
func (s *Store) FleetFailures(ctx context.Context, since string, limit int) ([]FleetFailureRow, error) {
	query := `
		SELECT tr.test_name, GROUP_CONCAT(DISTINCT j.env) as envs
		FROM test_results tr
		JOIN jobs j ON tr.job_id = j.id
		WHERE tr.status = 'failed' AND j.state IN ('failure', 'error')
	`
	args := []any{}
	if since != "" {
		query += " AND j.started_at >= ?"
		args = append(args, since)
	}
	query += fmt.Sprintf(`
		GROUP BY tr.test_name
		HAVING COUNT(DISTINCT j.env) > 1
		ORDER BY COUNT(DISTINCT j.env) DESC, tr.test_name
		LIMIT %d
	`, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying fleet failures: %w", err)
	}
	defer rows.Close()

	var results []FleetFailureRow
	for rows.Next() {
		var r FleetFailureRow
		if err := rows.Scan(&r.TestName, &r.Envs); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// TopFailures returns the most frequently failing test names for an env/type since a time.
// Only considers test failures from jobs with state 'failure' or 'error'.
func (s *Store) TopFailures(ctx context.Context, env, jobType, since string, limit int) ([]string, error) {
	query := `
		SELECT tr.test_name
		FROM test_results tr
		JOIN jobs j ON tr.job_id = j.id
		WHERE j.env = ? AND j.job_type = ? AND tr.status = 'failed'
		  AND j.state IN ('failure', 'error')
	`
	args := []any{env, jobType}
	if since != "" {
		query += " AND j.started_at >= ?"
		args = append(args, since)
	}
	query += fmt.Sprintf(`
		GROUP BY tr.test_name
		ORDER BY COUNT(*) DESC
		LIMIT %d
	`, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// PRFailures returns test failures for jobs associated with a specific PR.
type PRFailureRow struct {
	TestName string
	Message  string
	BaseURL  string
}

// PRTestFailures returns all test failures from jobs for a specific PR in a given env.
func (s *Store) PRTestFailures(ctx context.Context, env string, prNumber int) ([]PRFailureRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT tr.test_name, COALESCE(tr.failure_message,''), j.base_url
		FROM test_results tr
		JOIN jobs j ON tr.job_id = j.id
		WHERE j.env = ? AND j.pr_number = ? AND tr.status = 'failed'
		ORDER BY tr.test_name
	`, env, prNumber)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []PRFailureRow
	for rows.Next() {
		var r PRFailureRow
		if err := rows.Scan(&r.TestName, &r.Message, &r.BaseURL); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// BaselineFailingTests returns test names that are currently failing in the
// most recent periodic jobs. Only considers the `recentJobs` most recent
// periodic jobs with state 'failure' or 'error', matching the Python tool's
// behavior of checking history=20 for baseline comparison.
func (s *Store) BaselineFailingTests(ctx context.Context, env string, recentJobs int) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT tr.test_name
		FROM test_results tr
		JOIN jobs j ON tr.job_id = j.id
		WHERE j.env = ? AND j.job_type = 'periodic' AND tr.status = 'failed'
		  AND j.state IN ('failure', 'error')
		  AND j.id IN (
			SELECT id FROM jobs
			WHERE env = ? AND job_type = 'periodic'
			ORDER BY started_at DESC
			LIMIT ?
		  )
		ORDER BY tr.test_name
	`, env, env, recentJobs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		result[name] = true
	}

	return result, rows.Err()
}
