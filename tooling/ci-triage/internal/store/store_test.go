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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/db"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	database, err := db.OpenAndMigrate(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { database.Close() })
	return New(database)
}

func TestUpsertJobAndHasBuild(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	job := &Job{
		Env:       "int",
		JobType:   "periodic",
		JobName:   "periodic-ci-test",
		BuildID:   "1900000000000000000",
		BaseURL:   "https://example.com/job/1",
		Revision:  "abc123def456",
		State:     "failure",
		StartedAt: "2025-04-01T12:00:00",
	}

	id, err := s.UpsertJob(ctx, job)
	require.NoError(t, err)
	assert.Greater(t, id, int64(0))

	// Should exist now
	exists, err := s.HasBuild(ctx, "1900000000000000000")
	require.NoError(t, err)
	assert.True(t, exists)

	// Should not exist
	exists, err = s.HasBuild(ctx, "9999999999999999999")
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestUpsertJobIdempotent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	job := &Job{
		Env:       "int",
		JobType:   "periodic",
		JobName:   "periodic-ci-test",
		BuildID:   "1900000000000000000",
		BaseURL:   "https://example.com/job/1",
		State:     "pending",
		StartedAt: "2025-04-01T12:00:00",
	}

	id1, err := s.UpsertJob(ctx, job)
	require.NoError(t, err)

	// Update state
	job.State = "failure"
	job.Revision = "abc123"
	id2, err := s.UpsertJob(ctx, job)
	require.NoError(t, err)
	assert.Equal(t, id1, id2)

	// Verify update
	jobs, err := s.ListJobs(ctx, JobFilter{Env: "int"})
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	assert.Equal(t, "failure", jobs[0].State)
	assert.Equal(t, "abc123", jobs[0].Revision)
}

func TestInsertTestResults(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	jobID, err := s.UpsertJob(ctx, &Job{
		Env: "int", JobType: "periodic", JobName: "test",
		BuildID: "100", BaseURL: "https://x", State: "failure",
		StartedAt: "2025-04-01T12:00:00",
	})
	require.NoError(t, err)

	results := []TestResult{
		{JobID: jobID, TestName: "TestA", Status: "passed"},
		{JobID: jobID, TestName: "TestB", Status: "failed", FailureMessage: "timeout", FailureMessageNormalized: "timeout"},
		{JobID: jobID, TestName: "TestC", Status: "failed", FailureMessage: "error in rg-abc", FailureMessageNormalized: "error in <rg>"},
	}
	err = s.InsertTestResults(ctx, results)
	require.NoError(t, err)

	has, err := s.HasTestResults(ctx, jobID)
	require.NoError(t, err)
	assert.True(t, has)
}

func TestListJobsWithFilter(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Insert jobs across envs
	for _, env := range []string{"int", "stg"} {
		for i, state := range []string{"success", "failure"} {
			_, err := s.UpsertJob(ctx, &Job{
				Env: env, JobType: "periodic", JobName: "test",
				BuildID: env + state + string(rune('0'+i)), BaseURL: "https://x",
				State: state, StartedAt: "2025-04-01T12:00:00",
			})
			require.NoError(t, err)
		}
	}

	// Filter by env
	jobs, err := s.ListJobs(ctx, JobFilter{Env: "int"})
	require.NoError(t, err)
	assert.Len(t, jobs, 2)

	// Filter by env + limit
	jobs, err = s.ListJobs(ctx, JobFilter{Env: "int", Limit: 1})
	require.NoError(t, err)
	assert.Len(t, jobs, 1)
}

func TestJobStateCounts(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	states := []string{"success", "success", "failure", "error", "aborted"}
	for i, state := range states {
		_, err := s.UpsertJob(ctx, &Job{
			Env: "int", JobType: "periodic", JobName: "test",
			BuildID: string(rune('a'+i)) + "00", BaseURL: "https://x",
			State: state, StartedAt: "2025-04-01T12:00:00",
		})
		require.NoError(t, err)
	}

	counts, err := s.JobStateCounts(ctx, "int", "periodic", "", "")
	require.NoError(t, err)
	assert.Equal(t, 2, counts["success"])
	assert.Equal(t, 1, counts["failure"])
	assert.Equal(t, 1, counts["error"])
	assert.Equal(t, 1, counts["aborted"])
}

func TestFailureGroupsAndOnset(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Job 1: failure with TestA and TestB failing
	j1, _ := s.UpsertJob(ctx, &Job{
		Env: "int", JobType: "periodic", JobName: "test",
		BuildID: "bid1", BaseURL: "https://x/1", State: "failure",
		StartedAt: "2025-04-01T10:00:00",
	})
	s.InsertTestResults(ctx, []TestResult{
		{JobID: j1, TestName: "TestA", Status: "failed", FailureMessage: "err1", FailureMessageNormalized: "err1"},
		{JobID: j1, TestName: "TestB", Status: "failed", FailureMessage: "err2", FailureMessageNormalized: "err2"},
	})

	// Job 2: failure with TestA failing again
	j2, _ := s.UpsertJob(ctx, &Job{
		Env: "int", JobType: "periodic", JobName: "test",
		BuildID: "bid2", BaseURL: "https://x/2", State: "failure",
		StartedAt: "2025-04-02T10:00:00",
	})
	s.InsertTestResults(ctx, []TestResult{
		{JobID: j2, TestName: "TestA", Status: "failed", FailureMessage: "err1", FailureMessageNormalized: "err1"},
		{JobID: j2, TestName: "TestB", Status: "passed"},
	})

	// Failure groups
	groups, err := s.FailureGroups(ctx, "int", "periodic", "", "")
	require.NoError(t, err)
	require.Len(t, groups, 2)
	assert.Equal(t, "TestA", groups[0].TestName)
	assert.Equal(t, 2, groups[0].Count)
	assert.Equal(t, "TestB", groups[1].TestName)
	assert.Equal(t, 1, groups[1].Count)

	// Onset detection
	onsetMap, err := s.OnsetMap(ctx, "int", "periodic", []string{"TestA", "TestB"})
	require.NoError(t, err)
	assert.Empty(t, onsetMap["TestA"]) // never passed
	assert.Equal(t, "2025-04-02T10:00:00", onsetMap["TestB"]) // passed in job 2
}

func TestFleetFailures(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Same test failing in two envs
	for _, env := range []string{"int", "stg"} {
		jid, _ := s.UpsertJob(ctx, &Job{
			Env: env, JobType: "periodic", JobName: "test",
			BuildID: env + "bid", BaseURL: "https://x", State: "failure",
			StartedAt: "2025-04-01T10:00:00",
		})
		s.InsertTestResults(ctx, []TestResult{
			{JobID: jid, TestName: "TestCommon", Status: "failed", FailureMessage: "err"},
		})
	}

	fleet, err := s.FleetFailures(ctx, "", 10)
	require.NoError(t, err)
	require.Len(t, fleet, 1)
	assert.Equal(t, "TestCommon", fleet[0].TestName)
	assert.Contains(t, fleet[0].Envs, "int")
	assert.Contains(t, fleet[0].Envs, "stg")
}
