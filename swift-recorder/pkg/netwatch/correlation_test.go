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
	"reflect"
	"testing"
	"time"
)

func TestDepartureRecordRetainsObservedCorrelation(t *testing.T) {
	type notification struct {
		kind string
		body map[string]any
	}
	for _, tt := range []struct {
		name       string
		events     []notification
		master     int32
		lastMaster int32
		guid       string
	}{
		{name: "initially paired", master: 3, lastMaster: 3, guid: "synthetic-a"},
		{
			name:       "unpaired before departure",
			events:     []notification{{"NEWLINK", map[string]any{"index": int32(9), "name": "vf", "mac": "00:11:22:33:44:55", "parentBus": "pci", "parentDevice": "0001:00:02.0"}}},
			lastMaster: 3, guid: "synthetic-a",
		},
		{
			name:   "synthetic departs first",
			events: []notification{{"DELLINK", map[string]any{"index": int32(3)}}},
			master: 3, lastMaster: 3, guid: "synthetic-a",
		},
		{
			name: "synthetic departs before unpairing",
			events: []notification{
				{"DELLINK", map[string]any{"index": int32(3)}},
				{"NEWLINK", map[string]any{"index": int32(9), "name": "vf", "mac": "00:11:22:33:44:55", "parentBus": "pci", "parentDevice": "0001:00:02.0"}},
			},
			lastMaster: 3, guid: "synthetic-a",
		},
		{
			name:   "repaired to another synthetic",
			events: []notification{{"NEWLINK", map[string]any{"index": int32(9), "name": "vf", "mac": "00:11:22:33:44:55", "parentBus": "pci", "parentDevice": "0001:00:02.0", "masterIndex": uint32(4)}}},
			master: 4, lastMaster: 4, guid: "synthetic-b",
		},
		{
			name:   "repaired to unknown master clears old GUID",
			events: []notification{{"NEWLINK", map[string]any{"index": int32(9), "name": "vf", "mac": "00:11:22:33:44:55", "parentBus": "pci", "parentDevice": "0001:00:02.0", "masterIndex": uint32(99)}}},
			master: 99, lastMaster: 99,
		},
		{
			name: "master index reuse does not rewrite captured evidence",
			events: []notification{
				{"DELLINK", map[string]any{"index": int32(3)}},
				{"NEWLINK", map[string]any{"index": int32(3), "parentBus": "vmbus", "parentDevice": "replacement"}},
			},
			master: 3, lastMaster: 3, guid: "synthetic-a",
		},
		{
			name: "unpair and repair to reused index captures new association",
			events: []notification{
				{"NEWLINK", map[string]any{"index": int32(9), "name": "vf", "mac": "00:11:22:33:44:55", "parentBus": "pci", "parentDevice": "0001:00:02.0"}},
				{"DELLINK", map[string]any{"index": int32(3)}},
				{"NEWLINK", map[string]any{"index": int32(3), "parentBus": "vmbus", "parentDevice": "replacement"}},
				{"NEWLINK", map[string]any{"index": int32(9), "name": "vf", "mac": "00:11:22:33:44:55", "parentBus": "pci", "parentDevice": "0001:00:02.0", "masterIndex": uint32(3)}},
			},
			master: 3, lastMaster: 3, guid: "replacement",
		},
		{
			name: "unpair and repair to missing reused index clears old GUID",
			events: []notification{
				{"NEWLINK", map[string]any{"index": int32(9), "name": "vf", "mac": "00:11:22:33:44:55", "parentBus": "pci", "parentDevice": "0001:00:02.0"}},
				{"DELLINK", map[string]any{"index": int32(3)}},
				{"NEWLINK", map[string]any{"index": int32(9), "name": "vf", "mac": "00:11:22:33:44:55", "parentBus": "pci", "parentDevice": "0001:00:02.0", "masterIndex": uint32(3)}},
			},
			master: 3, lastMaster: 3,
		},
		{
			name: "VF index reuse forgets prior incarnation",
			events: []notification{
				{"DELLINK", map[string]any{"index": int32(9)}},
				{"NEWLINK", map[string]any{"index": int32(9), "name": "vf", "mac": "00:11:22:33:44:55", "parentBus": "pci", "parentDevice": "0001:00:02.0"}},
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			g := newTestGeneration(t)
			g.status = SectionStatus{Available: true}
			g.applyLink(rtnlEvent(t, "NEWLINK", map[string]any{"index": int32(3), "name": "eth0", "parentBus": "vmbus", "parentDevice": "synthetic-a"}))
			g.applyLink(rtnlEvent(t, "NEWLINK", map[string]any{"index": int32(4), "name": "eth1", "parentBus": "vmbus", "parentDevice": "synthetic-b"}))
			g.applyLink(rtnlEvent(t, "NEWLINK", map[string]any{"index": int32(9), "name": "vf", "mac": "00:11:22:33:44:55", "parentBus": "pci", "parentDevice": "0001:00:02.0", "masterIndex": uint32(3)}))
			for _, event := range tt.events {
				if _, err := g.apply(rtnlEvent(t, event.kind, event.body)); err != nil {
					t.Fatal(err)
				}
			}
			departure := rtnlEvent(t, "DELLINK", map[string]any{"index": int32(9), "newNetnsID": int32(0), "newIfindex": int32(2)})
			obs := g.applyLinkDelete(departure)
			var record Record
			g.emitter = NewEmitter(func(rec Record) { record = rec })
			g.emitChange(obs, departure.ObservedAt)
			wire := marshalCorrelationJSON(t, record)
			observation := wire["observations"].([]any)[0].(map[string]any)
			before := map[string]any{
				"ifindex": float64(9), "name": "vf", "mac": "00:11:22:33:44:55",
				"parent_bus": "pci", "parent_device": "0001:00:02.0",
				"mtu": float64(0), "up": false, "running": false, "lower_up": false,
				"operstate": float64(0), "carrier": false,
			}
			if tt.master != 0 {
				before["master_index"] = float64(tt.master)
			}
			trigger := map[string]any{"notification": "DELLINK", "new_netns_id": float64(0), "new_ifindex": float64(2)}
			if tt.lastMaster != 0 {
				trigger["last_known_master_index"] = float64(tt.lastMaster)
			}
			if len(tt.guid) > 0 {
				trigger["last_known_synthetic_guid"] = tt.guid
			}
			want := map[string]any{"kind": "link_disappeared", "ifindex": float64(9), "before": before, "trigger": trigger}
			if !reflect.DeepEqual(observation, want) {
				t.Fatalf("departure JSON = %#v, want %#v", observation, want)
			}
			for _, state := range wire["state"].([]any) {
				if state.(map[string]any)["ifindex"] == float64(9) {
					t.Fatal("departed VF remains in resulting state")
				}
			}
		})
	}
}

func TestPairingEvidenceRequiresExplicitSyntheticMaster(t *testing.T) {
	for _, tt := range []struct {
		name   string
		master int32
		guid   string
	}{
		{name: "shared PCI and matching name do not infer pairing"},
		{name: "unknown master", master: 99},
		{name: "PCI master is not a synthetic", master: 10},
		{name: "explicit synthetic master", master: 4, guid: "synthetic-b"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			links := map[int32]*trackedLink{
				3:  newTrackedLink(LinkState{Ifindex: 3, Name: "eth0", ParentBus: "vmbus", ParentDevice: "synthetic-a"}),
				4:  newTrackedLink(LinkState{Ifindex: 4, Name: "eth1", ParentBus: "vmbus", ParentDevice: "synthetic-b"}),
				9:  newTrackedLink(LinkState{Ifindex: 9, Name: "eth0", ParentBus: "pci", ParentDevice: "0001:00:02.0", MasterIndex: tt.master}),
				10: newTrackedLink(LinkState{Ifindex: 10, Name: "vf", ParentBus: "pci", ParentDevice: "0001:00:02.0", MasterIndex: 3}),
			}
			resolvePairings(links)
			obs := observeDisappearance(*links[9], Trigger{Notification: "DELLINK"})
			trigger := marshalCorrelationJSON(t, obs)["trigger"].(map[string]any)
			guid, known := trigger["last_known_synthetic_guid"]
			if known != (len(tt.guid) > 0) || known && guid != tt.guid {
				t.Fatalf("synthetic GUID = %v (present %v), want %q", guid, known, tt.guid)
			}
			master, known := trigger["last_known_master_index"]
			if known != (tt.master != 0) || known && master != float64(tt.master) {
				t.Fatalf("last master = %v (present %v), want %d", master, known, tt.master)
			}
		})
	}
}

func TestLateSyntheticIdentityRequiresCurrentPairing(t *testing.T) {
	for _, unpaired := range []bool{false, true} {
		t.Run(map[bool]string{false: "still paired", true: "already unpaired"}[unpaired], func(t *testing.T) {
			g := newTestGeneration(t)
			g.applyLink(rtnlEvent(t, "NEWLINK", map[string]any{"index": int32(9), "name": "vf", "masterIndex": uint32(3)}))
			if unpaired {
				g.applyLink(rtnlEvent(t, "NEWLINK", map[string]any{"index": int32(9), "name": "vf"}))
			}
			g.applyLink(rtnlEvent(t, "NEWLINK", map[string]any{"index": int32(3), "parentBus": "vmbus", "parentDevice": "late-synthetic"}))
			obs := g.applyLinkDelete(rtnlEvent(t, "DELLINK", map[string]any{"index": int32(9)}))
			trigger := marshalCorrelationJSON(t, obs[0])["trigger"].(map[string]any)
			guid, known := trigger["last_known_synthetic_guid"]
			if unpaired && known || !unpaired && guid != "late-synthetic" {
				t.Fatalf("late GUID evidence = %v, unpaired = %v", trigger, unpaired)
			}
			if trigger["last_known_master_index"] != float64(3) {
				t.Fatalf("observed master lost: %v", trigger)
			}
		})
	}
}

func TestPairingRecordPreservesPreviousIdentity(t *testing.T) {
	for _, nextMaster := range []uint32{0, 4} {
		t.Run(map[uint32]string{0: "unpair", 4: "repair"}[nextMaster], func(t *testing.T) {
			g := newTestGeneration(t)
			g.applyLink(rtnlEvent(t, "NEWLINK", map[string]any{"index": int32(3), "parentBus": "vmbus", "parentDevice": "synthetic-a"}))
			g.applyLink(rtnlEvent(t, "NEWLINK", map[string]any{"index": int32(9), "name": "vf-before", "mac": "00:11:22:33:44:55", "parentBus": "pci", "masterIndex": uint32(3)}))
			obs := g.applyLink(rtnlEvent(t, "NEWLINK", map[string]any{"index": int32(9), "name": "vf-after", "mac": "00:11:22:33:44:55", "parentBus": "pci", "masterIndex": nextMaster}))
			wire := marshalCorrelationJSON(t, Record{Observations: obs})
			observations := wire["observations"].([]any)
			if len(observations) != 2 {
				t.Fatalf("observations = %v, want rename and pairing change", observations)
			}
			for _, raw := range observations {
				observation := raw.(map[string]any)
				if observation["kind"] == "link_renamed" {
					if _, ok := observation["before"]; ok {
						t.Fatal("pairing enrichment changed unrelated rename observation")
					}
					continue
				}
				if observation["kind"] != "link_pairing_changed" {
					t.Fatalf("unexpected classification: %v", observation)
				}
				before := observation["before"].(map[string]any)
				if before["name"] != "vf-before" || before["mac"] != "00:11:22:33:44:55" || before["master_index"] != float64(3) {
					t.Fatalf("previous identity missing: %v", before)
				}
				trigger := observation["trigger"].(map[string]any)
				if trigger["last_known_master_index"] != float64(3) || trigger["last_known_synthetic_guid"] != "synthetic-a" {
					t.Fatalf("previous association missing: %v", trigger)
				}
				wantFields := []any{map[string]any{"field": "master_index", "from": float64(3), "to": float64(nextMaster)}}
				if !reflect.DeepEqual(observation["fields"], wantFields) {
					t.Fatalf("pairing fields = %v, want %v", observation["fields"], wantFields)
				}
			}
		})
	}
}

func TestBeforeEvidenceIsIndependentOfLaterState(t *testing.T) {
	for _, kind := range []ObservationKind{LinkDisappeared, LinkPairingChanged} {
		t.Run(string(kind), func(t *testing.T) {
			link := newTrackedLink(LinkState{
				Ifindex: 3, Name: "synthetic", ParentBus: "vmbus", ParentDevice: "synthetic-a",
				Carrier: true, Operstate: 6,
			})
			obs := []Observation{observeDisappearance(*link, Trigger{Notification: "DELLINK"})}
			if kind == LinkPairingChanged {
				obs = []Observation{{Kind: LinkPairingChanged, Ifindex: 3}}
				enrichPairingObservations(obs, *link)
			}
			before := marshalCorrelationJSON(t, obs[0])["before"]
			link.Current.Carrier = false
			link.Current.Operstate = 0
			link.Current.Name = "replacement"
			link.Current.ParentDevice = "replacement-guid"
			after := marshalCorrelationJSON(t, obs[0])["before"]
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("retained before evidence mutated: %v, originally %v", after, before)
			}
			identity := after.(map[string]any)
			if identity["parent_device"] != "synthetic-a" || identity["name"] != "synthetic" {
				t.Fatalf("synthetic departure lost direct GUID identity: %v", identity)
			}
		})
	}
}

func TestRecordGoldenDeparture(t *testing.T) {
	g := newTestGeneration(t)
	g.status = SectionStatus{Available: true}
	g.applyLink(rtnlEvent(t, "NEWLINK", map[string]any{
		"index": int32(3), "name": "eth0", "parentBus": "vmbus", "parentDevice": "synthetic-a",
	}))
	g.applyLink(rtnlEvent(t, "NEWLINK", map[string]any{
		"index": int32(9), "name": "vf", "mac": "00:11:22:33:44:55",
		"parentBus": "pci", "parentDevice": "0001:00:02.0", "masterIndex": uint32(3),
		"mtu": uint32(1500), "up": true, "carrier": true,
	}))
	g.applyLinkDelete(rtnlEvent(t, "DELLINK", map[string]any{"index": int32(3)}))
	g.applyLink(rtnlEvent(t, "NEWLINK", map[string]any{
		"index": int32(9), "name": "vf", "mac": "00:11:22:33:44:55",
		"parentBus": "pci", "parentDevice": "0001:00:02.0",
		"mtu": uint32(1500), "up": true, "carrier": true,
	}))
	obs := g.applyLinkDelete(rtnlEvent(t, "DELLINK", map[string]any{
		"index": int32(9), "newNetnsID": int32(0), "newIfindex": int32(2),
	}))
	rec := Record{
		Reason: ReasonChange, SessionID: "11111111-1111-1111-1111-111111111111", Sequence: 3,
		ObservedAt: time.Unix(100, 0).UTC(), EmittedAt: time.Unix(101, 0).UTC(), LastAppliedNotification: 5,
		Status: g.status,
		State:  g.snapshotState(), Observations: obs,
	}
	assertGolden(t, "testdata/record_departure.json", rec)
}

func marshalCorrelationJSON(t *testing.T, value any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	return wire
}
