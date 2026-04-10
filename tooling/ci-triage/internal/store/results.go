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
)

// TestResult represents a test result row.
type TestResult struct {
	ID                       int64
	JobID                    int64
	TestName                 string
	Status                   string
	FailureMessage           string
	FailureMessageNormalized string
}

// InsertTestResults batch-inserts test results within a transaction.
func (s *Store) InsertTestResults(ctx context.Context, results []TestResult) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO test_results (job_id, test_name, status, failure_message, failure_message_normalized)
		VALUES (?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("preparing statement: %w", err)
	}
	defer stmt.Close()

	for _, r := range results {
		_, err := stmt.ExecContext(ctx, r.JobID, r.TestName, r.Status,
			nullString(r.FailureMessage), nullString(r.FailureMessageNormalized))
		if err != nil {
			return fmt.Errorf("inserting test result %s: %w", r.TestName, err)
		}
	}

	return tx.Commit()
}

// HasTestResults checks if test results already exist for a job.
func (s *Store) HasTestResults(ctx context.Context, jobID int64) (bool, error) {
	var exists int
	err := s.db.QueryRowContext(ctx,
		"SELECT 1 FROM test_results WHERE job_id = ? LIMIT 1", jobID).Scan(&exists)
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
