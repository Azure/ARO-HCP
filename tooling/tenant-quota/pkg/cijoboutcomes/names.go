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
	"sync"
	"time"
)

// nameSet decides which test names a run still has to write.
//
// Every batch this collector writes is keyed to the run that produced it, so
// IfNotExists only suppresses a repeat of the same run's rows. That is the
// right rule for outcomes and test results, which are per-run facts, and the
// wrong one for names: a name belongs to no run, and every run that executes
// a test would otherwise write its name again. ciTestNames reached 139,496
// rows for 180 distinct tests that way, because the rows are identical and
// nothing downstream notices.
//
// Names are gated on the table rather than on the run, in two parts. Those
// Kusto reports are written; the read is refreshed on a TTL so names another
// replica or an earlier process wrote are picked up. Those this process has
// submitted are held separately and expire, because acceptance is not
// completion - mirroring how the batches cache treats an accepted submission.
// A submission that is accepted and then lost therefore reappears as unwritten
// once the entry expires, and the next run to execute that test writes it
// again, rather than leaving ciTestResults rows that join to nothing.
type nameSet struct {
	load      func(context.Context) ([]string, error)
	ttl       time.Duration
	now       func() time.Time
	submitted *ttlCache[string, struct{}]

	mu          sync.Mutex
	known       map[string]struct{}
	refreshedAt time.Time
}

func newNameSet(capacity int, ttl time.Duration) *nameSet {
	return &nameSet{
		ttl:       ttl,
		now:       time.Now,
		submitted: newTTLCache[string, struct{}](capacity, ttl),
		known:     map[string]struct{}{},
	}
}

// unwritten returns the names whose rows are not already in the table, in the
// order given. It reports an error only when the table cannot be read and has
// never been read: without that the gate cannot tell a fresh table from an
// unreadable one, which is the failure that let the duplication through.
func (s *nameSet) unwritten(ctx context.Context, names []ciTestName) ([]ciTestName, error) {
	if len(names) == 0 {
		return names, nil
	}
	if err := s.refresh(ctx); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	// Preserve nil, which submitBatch reports as "unavailable" rather than
	// "empty" - the two mean different things to the batch report.
	out := make([]ciTestName, 0, len(names))
	for _, name := range names {
		if _, written := s.known[name.TestID]; written {
			continue
		}
		if _, recent := s.submitted.get(name.TestID); recent {
			continue
		}
		out = append(out, name)
	}
	return out, nil
}

// refresh reloads the written names when the last read has aged out. The read
// is held under the lock: it happens once per TTL, and letting passes race it
// would have them each write the names the others are already writing.
func (s *nameSet) refresh(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.refreshedAt.IsZero() && s.now().Before(s.refreshedAt.Add(s.ttl)) {
		return nil
	}
	if s.load == nil {
		return nil
	}

	values, err := s.load(ctx)
	if err != nil {
		if s.refreshedAt.IsZero() {
			return err
		}
		// A stale set still gates correctly; it only risks rewriting a name
		// another replica added since. Failing the pass over that would stop
		// outcomes being recorded for a reason that costs one duplicate row.
		return nil
	}
	known := make(map[string]struct{}, len(values))
	for _, value := range values {
		known[value] = struct{}{}
	}
	s.known = known
	s.refreshedAt = s.now()
	return nil
}

// submittedNames records names whose rows have been accepted for ingestion, so
// passes within the TTL do not write them again while the ingestion settles.
func (s *nameSet) submittedNames(names []ciTestName) {
	for _, name := range names {
		s.submitted.put(name.TestID, struct{}{})
	}
}
