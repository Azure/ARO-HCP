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
	"fmt"
	"os"
	"testing"
	"time"
)

func TestEmitterAssignsSessionAndSequence(t *testing.T) {
	var got []Record
	e := NewEmitter(func(r Record) { got = append(got, r) })
	e.Emit(Record{Reason: ReasonPeriodic})
	e.Emit(Record{Reason: ReasonChange})
	if len(got) != 2 {
		t.Fatalf("got %d records, want 2", len(got))
	}
	if got[0].SessionID == "" || got[0].SessionID != got[1].SessionID {
		t.Fatalf("SessionID must be nonempty and stable within one Emitter: %q vs %q", got[0].SessionID, got[1].SessionID)
	}
	if got[0].Sequence != 1 || got[1].Sequence != 2 {
		t.Fatalf("Sequence = %d, %d; want 1, 2", got[0].Sequence, got[1].Sequence)
	}
}

func TestEmitterDegradesOversizedRecord(t *testing.T) {
	var got Record
	e := NewEmitter(func(r Record) { got = r })
	huge := make([]LinkState, 100000)
	for i := range huge {
		huge[i] = LinkState{Ifindex: int32(i), Name: fmt.Sprintf("iface-with-a-long-name-%d", i)}
	}
	e.Emit(Record{Reason: ReasonChange, Status: SectionStatus{Available: true}, State: huge})
	if got.Incomplete == "" {
		t.Fatal("expected Incomplete to be set for an oversized record")
	}
	if got.State != nil {
		t.Fatalf("State = %v, want nil: an oversized record must never emit a truncated State labeled complete", got.State)
	}
	if got.Status.Available != true {
		t.Fatal("degraded record must still carry section status")
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("degraded record itself must marshal: %v", err)
	}
	if len(raw) > maxRecordBytes {
		t.Fatalf("degraded record is itself %d bytes, exceeds budget %d", len(raw), maxRecordBytes)
	}
}

func TestEmitterAcceptsRecordUnderBudget(t *testing.T) {
	var got Record
	e := NewEmitter(func(r Record) { got = r })
	e.Emit(Record{Reason: ReasonPeriodic, Status: SectionStatus{Available: true}, State: []LinkState{{Ifindex: 1, Name: "eth0"}}})
	if got.Incomplete != "" {
		t.Fatalf("Incomplete = %q, want empty for a small record", got.Incomplete)
	}
	if len(got.State) != 1 {
		t.Fatalf("State = %v, want the one link preserved", got.State)
	}
}

func TestRecordSizeMeasurement(t *testing.T) {
	operUp := byte(6)
	state := make([]LinkState, 0, 17)
	state = append(state, LinkState{
		Ifindex: 2, Name: "eth0", MAC: "00:15:5d:00:00:01", ParentBus: "vmbus",
		ParentDevice: "f8615163-0004-1000-2000-70a8a510829f", MTU: 1500, Up: true, Running: true, LowerUp: true,
		Operstate: operUp, Carrier: true,
	})
	for i := range 16 {
		idx := int32(3 + i)
		state = append(state, LinkState{
			Ifindex: idx, Name: fmt.Sprintf("eth%d", i+1), MAC: fmt.Sprintf("00:15:5d:00:00:%02x", i+2),
			ParentBus: "pci", ParentDevice: "0001:00:02.0", MasterIndex: 2, MTU: 1500,
			Up: true, Running: true, LowerUp: true, Operstate: operUp, Carrier: true,
		})
	}
	rec := Record{
		Reason: ReasonPeriodic, SessionID: "11111111-1111-1111-1111-111111111111", Sequence: 42,
		ObservedAt: time.Unix(1, 0).UTC(), EmittedAt: time.Unix(2, 0).UTC(), LastAppliedNotification: 123,
		Status: SectionStatus{Available: true}, State: state,
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	t.Logf("representative record with 1 synthetic + 16 VFs: %d bytes (budget %d)", len(raw), maxRecordBytes)
	if len(raw) > maxRecordBytes/2 {
		t.Errorf("representative record used %d of %d budget bytes - too close to the limit for real headroom", len(raw), maxRecordBytes)
	}
}

func TestRecordGoldenChange(t *testing.T) {
	g := newTestGeneration(t)
	g.status = SectionStatus{Available: true}
	g.links[7] = newTrackedLink(LinkState{
		Ifindex: 7, Name: "eth1", ParentBus: "pci", ParentDevice: "0001:00:02.0",
		MTU: 1500, Up: true, Running: true, LowerUp: true, Carrier: true,
	})
	observations := g.applyLink(rtnlEvent(t, "NEWLINK", map[string]any{
		"index": int32(7), "name": "eth1", "parentBus": "pci", "parentDevice": "0001:00:02.0",
		"masterIndex": uint32(2), "mtu": uint32(1500), "up": true, "running": true, "lowerUp": true, "carrier": true,
	}))
	rec := Record{
		Reason: ReasonChange, SessionID: "11111111-1111-1111-1111-111111111111", Sequence: 3,
		ObservedAt: time.Unix(100, 0).UTC(), EmittedAt: time.Unix(101, 0).UTC(), LastAppliedNotification: 5,
		Status: g.status,
		State:  g.snapshotState(), Observations: observations,
	}
	assertGolden(t, "testdata/record_change.json", rec)
}

func assertGolden(t *testing.T, path string, rec Record) {
	t.Helper()
	got, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got = append(got, '\n')
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("write golden %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s (run with UPDATE_GOLDEN=1 to create it): %v", path, err)
	}
	if string(got) != string(want) {
		t.Errorf("record does not match golden %s; run with UPDATE_GOLDEN=1 to update.\ngot:\n%s\nwant:\n%s", path, got, want)
	}
}
