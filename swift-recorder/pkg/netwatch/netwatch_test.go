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
	"context"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/Azure/ARO-HCP/swift-recorder/pkg/rtnl"
)

// permissive accepts every link - there is no vmbus/pci hardware in a test
// container, so SwiftDevicePairFilter alone would exercise nothing.
func permissive(map[string]any) bool { return true }

func TestRunReturnsPromptlyOnCancellationBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := Run(ctx, func(Record) { t.Fatal("emit must not be called when ctx is already cancelled") }, Options{Filter: permissive})
	if err != context.Canceled {
		t.Fatalf("Run returned %v, want context.Canceled", err)
	}
}

func TestRunEmitsInitialRecordThenLoopback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var mu sync.Mutex
	var records []Record
	err := Run(ctx, func(r Record) {
		mu.Lock()
		records = append(records, r)
		mu.Unlock()
		if r.Status.Available {
			cancel()
		}
	}, Options{Filter: permissive})

	if err != context.Canceled {
		t.Fatalf("Run returned %v, want context.Canceled", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(records) == 0 {
		t.Fatal("expected at least one record")
	}
	first := records[0]
	if first.Reason != ReasonPeriodic {
		t.Fatalf("first record Reason = %q, want %q", first.Reason, ReasonPeriodic)
	}
	if !first.Status.Available {
		t.Fatal("expected the status section to be available in a working namespace")
	}
	if first.ObservedAt.IsZero() || first.EmittedAt.IsZero() {
		t.Fatal("record missing ObservedAt/EmittedAt")
	}
	if first.SessionID == "" {
		t.Fatal("record missing SessionID")
	}
	found := false
	for _, l := range first.State {
		if l.Name == "lo" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected loopback in a permissive-filter State, got %+v", first.State)
	}
}

func TestRunEmptySelectedSet(t *testing.T) {
	// A reject-all filter exercises an explicit available-empty state on any host.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var first *Record
	err := Run(ctx, func(r Record) {
		if first == nil {
			rc := r
			first = &rc
			cancel()
		}
	}, Options{Filter: func(map[string]any) bool { return false }})
	if err != context.Canceled {
		t.Fatalf("Run returned %v, want context.Canceled", err)
	}
	if first == nil {
		t.Fatal("expected an initial record")
	}
	if first.Reason != ReasonPeriodic || !first.Status.Available {
		t.Fatalf("got %+v, want an available Periodic record", first)
	}
	if len(first.State) != 0 {
		t.Fatalf("reject-all filter unexpectedly selected %d interfaces: %+v", len(first.State), first.State)
	}
}

func TestSwiftDevicePairFilter(t *testing.T) {
	tests := []struct {
		name string
		link map[string]any
		want bool
	}{
		{"vmbus synthetic", map[string]any{"parentBus": "vmbus"}, true},
		{"pci VF", map[string]any{"parentBus": "pci"}, true},
		{"ordinary veth", map[string]any{"parentBus": ""}, false},
		{"no parent bus at all", map[string]any{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SwiftDevicePairFilter(tt.link); got != tt.want {
				t.Errorf("SwiftDevicePairFilter(%+v) = %v, want %v", tt.link, got, tt.want)
			}
		})
	}
}

func TestApplyLinkAppearedRenamedDisappeared(t *testing.T) {
	g := newTestGeneration(t)
	g.status = SectionStatus{Available: true}

	obs := g.applyLink(rtnlEvent(t, "NEWLINK", map[string]any{"index": int32(9), "name": "eth3", "parentBus": "pci", "parentDevice": "0001:00:02.0"}))
	if len(obs) != 1 || obs[0].Kind != LinkAppeared {
		t.Fatalf("got %+v, want one LinkAppeared", obs)
	}
	if _, ok := g.links[9]; !ok {
		t.Fatal("expected ifindex 9 to be tracked after appearance")
	}

	obs = g.applyLink(rtnlEvent(t, "NEWLINK", map[string]any{"index": int32(9), "name": "eth3-renamed", "parentBus": "pci", "parentDevice": "0001:00:02.0"}))
	if len(obs) != 1 || obs[0].Kind != LinkRenamed {
		t.Fatalf("got %+v, want one LinkRenamed", obs)
	}

	deleteObs := g.applyLinkDelete(rtnlEvent(t, "DELLINK", map[string]any{"index": int32(9)}))
	if len(deleteObs) != 1 || deleteObs[0].Kind != LinkDisappeared {
		t.Fatalf("got %+v, want one LinkDisappeared", deleteObs)
	}
	if _, ok := g.links[9]; ok {
		t.Fatal("expected ifindex 9 to be untracked after departure")
	}
}

func TestApplyLinkIndexReuseDoesNotInheritState(t *testing.T) {
	g := newTestGeneration(t)
	g.status = SectionStatus{Available: true}
	g.applyLink(rtnlEvent(t, "NEWLINK", map[string]any{"index": int32(9), "name": "eth3", "parentBus": "pci"}))
	g.applyLinkDelete(rtnlEvent(t, "DELLINK", map[string]any{"index": int32(9)}))

	obs := g.applyLink(rtnlEvent(t, "NEWLINK", map[string]any{"index": int32(9), "name": "eth9-new", "parentBus": "pci"}))
	if len(obs) != 1 || obs[0].Kind != LinkAppeared {
		t.Fatalf("got %+v, want a fresh LinkAppeared for the reused ifindex", obs)
	}
}

func TestApplyIgnoresUnselectedInterface(t *testing.T) {
	g := newTestGeneration(t)
	g.filter = SwiftDevicePairFilter // no parentBus below, so nothing is selected
	obs := g.applyLink(rtnlEvent(t, "NEWLINK", map[string]any{"index": int32(50), "name": "veth123"}))
	if len(obs) != 0 {
		t.Fatalf("got %+v, want an ordinary veth to be ignored entirely", obs)
	}
	if _, ok := g.links[50]; ok {
		t.Fatal("an unselected interface must never be tracked")
	}
}

func newTestGeneration(t *testing.T) *generation {
	t.Helper()
	return &generation{
		links:   map[int32]*trackedLink{},
		status:  SectionStatus{Reason: "test"},
		emitter: NewEmitter(func(Record) {}),
		filter:  permissive,
	}
}

func rtnlEvent(t *testing.T, kind string, body map[string]any) rtnl.Event {
	t.Helper()
	types := map[string]uint16{
		"NEWLINK": unix.RTM_NEWLINK, "DELLINK": unix.RTM_DELLINK,
	}
	rtmType, ok := types[kind]
	if !ok {
		t.Fatalf("unknown notification kind %q", kind)
	}
	return rtnl.Event{Type: rtmType, Body: body, ObservedAt: time.Now()}
}
