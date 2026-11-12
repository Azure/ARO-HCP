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

//go:build linux

package netwatch

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/go-logr/logr/funcr"
)

func TestLogTo(t *testing.T) {
	type loggedLine struct {
		Msg    string          `json:"msg"`
		Record json.RawMessage `json:"record"`
	}
	var lines []loggedLine
	// funcr's JSON sink is used (rather than its text sink) so the "record"
	// key/value passed to logger.Info is asserted as real structured data,
	// not just as a formatted string.
	logger := funcr.NewJSON(func(obj string) {
		var line loggedLine
		if err := json.Unmarshal([]byte(obj), &line); err != nil {
			t.Fatalf("logged line is not valid JSON: %v: %s", err, obj)
		}
		lines = append(lines, line)
	}, funcr.Options{})

	emit := LogTo(logger)
	emit(Record{
		Reason: ReasonPeriodic, SessionID: "s1", Sequence: 4,
		ObservedAt: time.Unix(0, 0).UTC(), EmittedAt: time.Unix(1, 0).UTC(),
		Status: SectionStatus{Available: true},
	})

	if len(lines) != 1 {
		t.Fatalf("got %d log calls, want 1", len(lines))
	}
	if lines[0].Msg != "SWIFT node netlink record" {
		t.Fatalf("got message %q", lines[0].Msg)
	}
	// Raw key names, not just a round-trip through the same Go struct: a
	// wrong or reverted json tag would still round-trip correctly through
	// Record but would silently change what actually appears in the log.
	var raw map[string]any
	if err := json.Unmarshal(lines[0].Record, &raw); err != nil {
		t.Fatalf("record field is not valid JSON: %v", err)
	}
	for key, want := range map[string]any{
		"reason": "periodic", "session_id": "s1", "sequence": float64(4),
		"observed_at": "1970-01-01T00:00:00Z", "emitted_at": "1970-01-01T00:00:01Z",
	} {
		if raw[key] != want {
			t.Errorf("%s = %v, want %v", key, raw[key], want)
		}
	}
	status, ok := raw["status"].(map[string]any)
	if !ok || status["available"] != true {
		t.Fatalf("nested status missing or wrong: %v", raw["status"])
	}
}
