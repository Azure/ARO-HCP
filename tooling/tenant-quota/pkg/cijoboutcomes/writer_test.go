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
	"testing"
	"time"
)

// Runs written but not yet visible to a query must not be written again, and the
// set that remembers them must not grow without bound.
func TestQueuedRunsAreSuppressedThenForgotten(t *testing.T) {
	w := &Writer{queued: make(map[string]time.Time)}
	older := time.Date(2026, 8, 25, 6, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)

	w.rememberQueued([]ciJobOutcome{
		{BuildID: "old-run", StartedAt: older},
		{BuildID: "new-run", StartedAt: newer},
	})

	if !w.queuedRecently("old-run") || !w.queuedRecently("new-run") {
		t.Fatal("a run just queued must be suppressed on the next pass")
	}

	// Once a pass reads from 09:00 onward, the query itself reports old-run, so
	// remembering it separately is no longer needed.
	w.forgetQueuedBefore(time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC))

	if w.queuedRecently("old-run") {
		t.Error("a run the query now covers must be forgotten")
	}
	if !w.queuedRecently("new-run") {
		t.Error("a run the query does not cover yet must still be suppressed")
	}
}
