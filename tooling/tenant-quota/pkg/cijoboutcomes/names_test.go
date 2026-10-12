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

package cijoboutcomes

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func testNames(ids ...string) []ciTestName {
	names := make([]ciTestName, 0, len(ids))
	for _, id := range ids {
		names = append(names, ciTestName{TestID: id, Name: "name of " + id})
	}
	return names
}

func ids(names []ciTestName) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, name.TestID)
	}
	return out
}

func newTestNameSet(t *testing.T, load func(context.Context) ([]string, error)) (*nameSet, *time.Time) {
	t.Helper()
	now := time.Now()
	set := newNameSet(100, 15*time.Minute)
	set.load = load
	set.now = func() time.Time { return now }
	set.claimed.now = set.now
	return set, &now
}

// Names a run executed but did not add are what made ciTestNames a fact table:
// every run wrote all of its names because the batch tag is keyed to the run.
func TestNamesAlreadyInTheTableAreNotWrittenAgain(t *testing.T) {
	set, _ := newTestNameSet(t, func(context.Context) ([]string, error) {
		return []string{"already-there"}, nil
	})

	unwritten, err := set.unwritten(t.Context(), testNames("already-there", "new"))
	require.NoError(t, err)
	require.Equal(t, []string{"new"}, ids(unwritten))
}

// Two runs executing the same test must produce one row between them, even
// though neither can see the other's batch tag.
func TestASecondRunDoesNotRewriteTheSameNames(t *testing.T) {
	set, _ := newTestNameSet(t, func(context.Context) ([]string, error) { return nil, nil })

	first, err := set.unwritten(t.Context(), testNames("a", "b"))
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, ids(first))

	second, err := set.unwritten(t.Context(), testNames("a", "b", "c"))
	require.NoError(t, err)
	require.Equal(t, []string{"c"}, ids(second), "only the name no run has written yet")
}

// Acceptance is not completion. A submission that is accepted and then lost
// has to come back as unwritten, or its ciTestResults rows join to nothing.
func TestAnAcceptedButLostSubmissionIsRetriedAfterTheTTL(t *testing.T) {
	var stored []string
	set, now := newTestNameSet(t, func(context.Context) ([]string, error) { return stored, nil })

	first, err := set.unwritten(t.Context(), testNames("a"))
	require.NoError(t, err)
	require.Equal(t, []string{"a"}, ids(first))

	// Still within the TTL: the submission is assumed to be settling.
	again, err := set.unwritten(t.Context(), testNames("a"))
	require.NoError(t, err)
	require.Empty(t, ids(again))

	// The TTL passes and the row never landed, so the name is unwritten again.
	*now = now.Add(16 * time.Minute)
	repaired, err := set.unwritten(t.Context(), testNames("a"))
	require.NoError(t, err)
	require.Equal(t, []string{"a"}, ids(repaired))

	// Once the row is really there, the refreshed read keeps it gated.
	stored = []string{"a"}
	*now = now.Add(16 * time.Minute)
	settled, err := set.unwritten(t.Context(), testNames("a"))
	require.NoError(t, err)
	require.Empty(t, ids(settled))
}

// A gate that cannot tell a fresh table from an unreadable one is how the
// duplication went unnoticed, so the first read has to be allowed to fail.
func TestAFirstReadThatFailsIsAnErrorRatherThanAnEmptyTable(t *testing.T) {
	failure := errors.New("kusto unavailable")
	calls := 0
	set, _ := newTestNameSet(t, func(context.Context) ([]string, error) {
		calls++
		if calls == 1 {
			return nil, failure
		}
		return []string{"a"}, nil
	})

	_, err := set.unwritten(t.Context(), testNames("a"))
	require.ErrorIs(t, err, failure, "an unreadable table must not look empty")

	unwritten, err := set.unwritten(t.Context(), testNames("a", "b"))
	require.NoError(t, err, "the next pass retries the read")
	require.Equal(t, []string{"b"}, ids(unwritten))
}

// Once the set has been read, a later read failing must not stop the pass:
// a stale set still gates, and the cost of being wrong is one duplicate row.
func TestALaterReadFailureFallsBackToTheStaleSet(t *testing.T) {
	failing := false
	set, now := newTestNameSet(t, func(context.Context) ([]string, error) {
		if failing {
			return nil, errors.New("kusto unavailable")
		}
		return []string{"a"}, nil
	})

	_, err := set.unwritten(t.Context(), testNames("a"))
	require.NoError(t, err)

	failing = true
	*now = now.Add(16 * time.Minute)
	unwritten, err := set.unwritten(t.Context(), testNames("a", "b"))
	require.NoError(t, err, "a stale set still gates")
	require.Equal(t, []string{"b"}, ids(unwritten))
}

// The read costs a round trip, so it must not happen per run.
func TestTheTableIsReadOncePerTTL(t *testing.T) {
	reads := 0
	set, now := newTestNameSet(t, func(context.Context) ([]string, error) {
		reads++
		return nil, nil
	})

	for range 5 {
		_, err := set.unwritten(t.Context(), testNames("a"))
		require.NoError(t, err)
	}
	require.Equal(t, 1, reads)

	*now = now.Add(16 * time.Minute)
	_, err := set.unwritten(t.Context(), testNames("a"))
	require.NoError(t, err)
	require.Equal(t, 2, reads)
}

// submitBatch distinguishes an empty batch from an unavailable one, so the
// gate must not turn a nil slice into an empty one.
func TestAnAbsentSourceIsLeftAlone(t *testing.T) {
	reads := 0
	set, _ := newTestNameSet(t, func(context.Context) ([]string, error) {
		reads++
		return nil, nil
	})

	unwritten, err := set.unwritten(t.Context(), nil)
	require.NoError(t, err)
	require.Nil(t, unwritten, "nil means unavailable, not empty")
	require.Zero(t, reads, "an absent source must not cost a read")
}

// Runs are reconciled by a pool of workers, so two runs that both executed a
// newly added test are read at the same time. Deciding first and recording the
// decision later would let both of them write it.
func TestConcurrentRunsClaimANewNameOnce(t *testing.T) {
	set, _ := newTestNameSet(t, func(context.Context) ([]string, error) { return nil, nil })

	const workers = 16
	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	results := make([][]ciTestName, workers)
	errs := make([]error, workers)
	for i := range workers {
		done.Add(1)
		go func() {
			defer done.Done()
			start.Wait()
			results[i], errs[i] = set.unwritten(t.Context(), testNames("shared"))
		}()
	}
	start.Done()
	done.Wait()

	claims := 0
	for i := range workers {
		require.NoError(t, errs[i])
		claims += len(results[i])
	}
	require.Equal(t, 1, claims, "exactly one caller may write a given new name")
}

// A claim is only good for a submission that happened; a failed one must not
// cost the name a whole TTL before anything retries it.
func TestAReleasedClaimIsRetriedImmediately(t *testing.T) {
	set, _ := newTestNameSet(t, func(context.Context) ([]string, error) { return nil, nil })

	claimed, err := set.unwritten(t.Context(), testNames("a"))
	require.NoError(t, err)
	require.Equal(t, []string{"a"}, ids(claimed))

	set.release(claimed)

	retried, err := set.unwritten(t.Context(), testNames("a"))
	require.NoError(t, err)
	require.Equal(t, []string{"a"}, ids(retried), "a released name is unwritten again at once")
}

// An unreachable Kusto must not put every pass back into the same blocking
// query: the read is held under the gate lock, so that serializes the workers.
func TestAFailedRefreshBacksOffRatherThanRetryingEveryPass(t *testing.T) {
	reads := 0
	failing := false
	set, now := newTestNameSet(t, func(context.Context) ([]string, error) {
		reads++
		if failing {
			return nil, errors.New("kusto unavailable")
		}
		return nil, nil
	})

	_, err := set.unwritten(t.Context(), testNames("a"))
	require.NoError(t, err)
	require.Equal(t, 1, reads)

	failing = true
	*now = now.Add(16 * time.Minute)
	for range 5 {
		_, err := set.unwritten(t.Context(), testNames("b"))
		require.NoError(t, err)
	}
	require.Equal(t, 2, reads, "a failed read holds the stale set for another TTL")
}
