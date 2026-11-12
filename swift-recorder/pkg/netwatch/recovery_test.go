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
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/Azure/ARO-HCP/swift-recorder/pkg/rtnl"
)

type monitorReply struct {
	events []rtnl.Event
	err    error
}

type testMonitor struct {
	replies chan monitorReply
	receive func() ([]rtnl.Event, error)
	closed  atomic.Bool
}

func (m *testMonitor) Recv() ([]rtnl.Event, error) {
	if m.receive != nil {
		return m.receive()
	}
	select {
	case reply := <-m.replies:
		return reply.events, reply.err
	case <-time.After(time.Millisecond):
		return nil, nil
	}
}

func (m *testMonitor) Drain() ([]rtnl.Event, error) {
	var all []rtnl.Event
	for {
		select {
		case reply := <-m.replies:
			if reply.err != nil {
				return all, reply.err
			}
			all = append(all, reply.events...)
		default:
			return all, nil
		}
	}
}

func (m *testMonitor) Close() error {
	m.closed.Store(true)
	return nil
}

func newTestRunner(t *testing.T) *runner {
	t.Helper()
	return &runner{
		filter:     permissive,
		emitter:    NewEmitter(func(Record) {}),
		newMonitor: func() (notificationMonitor, error) { return nil, fmt.Errorf("no monitor") },
	}
}

func TestBaselineReplay(t *testing.T) {
	r := newTestRunner(t)
	mon := &testMonitor{replies: make(chan monitorReply, 1)}
	mon.replies <- monitorReply{events: []rtnl.Event{rtnlEvent(t, "NEWLINK", map[string]any{"index": int32(7), "name": "renamed"})}}
	r.newMonitor = func() (notificationMonitor, error) { return mon, nil }
	r.collect = func(context.Context) (map[int32]*trackedLink, error) {
		return map[int32]*trackedLink{7: newTrackedLink(LinkState{Ifindex: 7, Name: "original"})}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var committed *Record
	r.emitter = NewEmitter(func(rec Record) {
		if rec.Reason == ReasonChange {
			t.Error("baseline replay fabricated a post-baseline change")
		}
		if rec.Status.Available {
			committed = &rec
			cancel()
		}
	})
	if err := r.run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("run: %v", err)
	}
	if committed == nil {
		t.Fatal("no reconciled baseline")
	}
	if committed.Reason != ReasonPeriodic {
		t.Fatalf("reason = %s, want %s", committed.Reason, ReasonPeriodic)
	}
	if len(committed.State) != 1 || committed.State[0].Ifindex != 7 || committed.State[0].Name != "renamed" {
		t.Fatalf("replay not applied: %+v", committed.State)
	}
	if !mon.closed.Load() {
		t.Fatal("monitor must be closed after generation ends")
	}
}

func TestBaselineReplayLossNeverPublishesAvailable(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reply  monitorReply
		reason string
	}{
		{"overflow", monitorReply{err: rtnl.ErrOverflow}, "kernel_notification_overflow"},
		{"malformed", monitorReply{events: []rtnl.Event{{Type: unix.RTM_NEWLINK, Malformed: true}}}, "malformed_notification"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestRunner(t)
			mon := &testMonitor{replies: make(chan monitorReply, 1)}
			mon.replies <- tc.reply
			r.newMonitor = func() (notificationMonitor, error) { return mon, nil }
			r.collect = func(context.Context) (map[int32]*trackedLink, error) {
				return map[int32]*trackedLink{9: newTrackedLink(LinkState{Ifindex: 9})}, nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			var last Record
			r.emitter = NewEmitter(func(rec Record) {
				last = rec
				if rec.Status.Available {
					t.Error("loss published available state")
				}
				if rec.Status.Reason == tc.reason {
					cancel()
				}
			})
			if err := r.run(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("run: %v", err)
			}
			if last.Status.Reason != tc.reason || len(last.State) != 0 {
				t.Fatalf("loss not propagated: %+v", last)
			}
		})
	}
}

func TestSubscriptionFailureEmitsDuringBackoff(t *testing.T) {
	oldPeriodic, oldBackoff := periodicInterval, minEstablishBackoff
	periodicInterval, minEstablishBackoff = 5*time.Millisecond, time.Second
	t.Cleanup(func() { periodicInterval, minEstablishBackoff = oldPeriodic, oldBackoff })
	r := newTestRunner(t)
	attempts := 0
	r.newMonitor = func() (notificationMonitor, error) { attempts++; return nil, unix.EPERM }
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var records []Record
	r.emitter = NewEmitter(func(rec Record) {
		records = append(records, rec)
		if len(records) == 2 {
			cancel()
		}
	})
	if err := r.run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("run: %v", err)
	}
	if len(records) != 2 || records[0].Reason != ReasonPeriodic || records[1].Reason != ReasonPeriodic {
		t.Fatalf("records: %+v", records)
	}
	if attempts != 1 {
		t.Fatalf("backoff did not contain periodic emission: attempts=%d", attempts)
	}
	for _, rec := range records {
		if rec.Status.Available || len(rec.State) != 0 {
			t.Fatalf("subscription failure claimed current state: %+v", rec)
		}
	}
}

func TestRecoveryDiscardsObsoleteGeneration(t *testing.T) {
	oldBackoff := minEstablishBackoff
	minEstablishBackoff = time.Millisecond
	t.Cleanup(func() { minEstablishBackoff = oldBackoff })
	r := newTestRunner(t)
	first := &testMonitor{replies: make(chan monitorReply, 1)}
	second := &testMonitor{replies: make(chan monitorReply, 1)}
	opens := 0
	r.newMonitor = func() (notificationMonitor, error) {
		opens++
		if opens == 1 {
			return first, nil
		}
		if !first.closed.Load() {
			t.Error("new generation opened before old monitor closed")
		}
		return second, nil
	}
	var attempts atomic.Int32
	r.collect = func(ctx context.Context) (map[int32]*trackedLink, error) {
		if attempts.Add(1) == 1 {
			first.replies <- monitorReply{err: rtnl.ErrOverflow}
			return map[int32]*trackedLink{99: newTrackedLink(LinkState{Ifindex: 99, Name: "obsolete"})}, nil
		}
		second.replies <- monitorReply{events: []rtnl.Event{{Type: unix.RTM_NEWLINK, Body: map[string]any{"index": int32(9), "name": "current-renamed"}}}}
		return map[int32]*trackedLink{9: newTrackedLink(LinkState{Ifindex: 9, Name: "current"})}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var recovered *Record
	r.emitter = NewEmitter(func(rec Record) {
		if rec.Status.Available {
			recovered = &rec
			cancel()
		}
	})
	if err := r.run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("run: %v", err)
	}
	if recovered == nil || len(recovered.State) != 1 || recovered.State[0].Ifindex != 9 || recovered.State[0].Name != "current-renamed" {
		t.Fatalf("obsolete generation leaked or current replay lost: %+v", recovered)
	}
	if opens != 2 || !second.closed.Load() {
		t.Fatalf("bad generation teardown: opens=%d", opens)
	}
}

func TestCollectionDeadlineRetriesWithoutPublishingAttemptState(t *testing.T) {
	oldTimeout, oldBackoff := dumpAttemptTimeout, minEstablishBackoff
	dumpAttemptTimeout, minEstablishBackoff = 10*time.Millisecond, time.Millisecond
	t.Cleanup(func() { dumpAttemptTimeout, minEstablishBackoff = oldTimeout, oldBackoff })
	r := newTestRunner(t)
	r.newMonitor = func() (notificationMonitor, error) { return &testMonitor{replies: make(chan monitorReply)}, nil }
	var attempts atomic.Int32
	r.collect = func(ctx context.Context) (map[int32]*trackedLink, error) {
		if attempts.Add(1) == 1 {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return map[int32]*trackedLink{}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var records []Record
	r.emitter = NewEmitter(func(rec Record) {
		records = append(records, rec)
		if rec.Status.Available {
			cancel()
		}
	})
	if err := r.run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("run: %v", err)
	}
	if len(records) != 2 || records[0].Status.Reason != "dump_deadline_exceeded" || records[1].Reason != ReasonPeriodic {
		t.Fatalf("deadline recovery: %+v", records)
	}
}
