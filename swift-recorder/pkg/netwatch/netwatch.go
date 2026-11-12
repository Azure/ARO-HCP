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

// Package netwatch maintains an explicit, in-memory current-state view of
// root-namespace links for the SWIFT v2 accelerated-networking devices
// (hv_netvsc synthetics and their MANA/mlx5_core VFs), using the
// subscribe/dump/replay pattern, and emits one structured Record whenever
// tracked state changes and every two minutes regardless. It never enters
// any other network namespace, needs no capability beyond opening a netlink
// socket, and depends on no external binary.
//
// Run never returns except when ctx is cancelled: every recoverable dump,
// decode, subscription and overflow failure is handled internally (with
// bounded per-attempt deadlines and backoff) so a caller running this
// alongside another controller never has a netwatch hiccup cancel it too.
package netwatch

import (
	"context"
	"errors"
	"time"

	"golang.org/x/sys/unix"

	"github.com/Azure/ARO-HCP/swift-recorder/pkg/rtnl"
)

type notificationMonitor interface {
	Recv() ([]rtnl.Event, error)
	Drain() ([]rtnl.Event, error)
	Close() error
}

// LinkFilter decides whether a decoded link should be tracked.
type LinkFilter func(link map[string]any) bool

// SwiftDevicePairFilter reports whether a decoded link is backed by the vmbus
// or pci bus — i.e. an hv_netvsc synthetic or a MANA/mlx5_core VF that forms
// a SWIFT accelerated-networking device pair, never an ordinary veth, bridge
// or other software interface. See pkg/rtnl.Decode for parentBus.
func SwiftDevicePairFilter(link map[string]any) bool {
	bus, _ := link["parentBus"].(string)
	return bus == "vmbus" || bus == "pci"
}

// Options configures Run.
type Options struct {
	// Filter decides which links to track. Nil defaults to
	// SwiftDevicePairFilter.
	Filter LinkFilter
	// NewMonitor opens a netlink subscription. Nil defaults to the real
	// rtnetlink multicast socket. Override in tests to inject a fake.
	NewMonitor func() (notificationMonitor, error)
}

var (
	periodicInterval     = 2 * time.Minute
	minEstablishBackoff  = time.Second
	maxEstablishBackoff  = 30 * time.Second
	dumpAttemptTimeout   = 5 * time.Second
	receiverPollInterval = 20 * time.Millisecond
	maxScanLinks         = 4096
)

// errStateGap forces a full re-establishment: something happened whose
// effect on tracked state cannot be determined from notifications alone.
var errStateGap = errors.New("netwatch: notification state gap requires reconciliation")

// Run maintains current root-namespace link state and reports a Record to
// emit for every tracked change and every two-minute heartbeat.
//
// Run blocks until ctx is cancelled, at which point it returns ctx.Err().
func Run(ctx context.Context, emit func(Record), opts Options) error {
	filter := opts.Filter
	if filter == nil {
		filter = SwiftDevicePairFilter
	}
	newMonitor := opts.NewMonitor
	if newMonitor == nil {
		newMonitor = func() (notificationMonitor, error) {
			return rtnl.NewMonitor(receiverPollInterval, unix.RTNLGRP_LINK)
		}
	}
	r := &runner{
		filter:     filter,
		emitter:    NewEmitter(emit),
		newMonitor: newMonitor,
	}
	return r.run(ctx)
}

// runner is the outer retry loop. For each attempt it creates a generation,
// runs it, and discards it entirely.
type runner struct {
	filter     LinkFilter
	emitter    *Emitter
	newMonitor func() (notificationMonitor, error)
	collect    func(context.Context) (map[int32]*trackedLink, error)
}

// run is the outer retry loop. Each iteration calls runOnce to open a
// socket, establish state and process events until something unrecoverable
// happens, then emits an unavailable record, backs off, and retries.
// Periodic heartbeats continue during backoff so the log stream is never
// silent. run only returns when ctx is cancelled.
func (r *runner) run(ctx context.Context) error {
	ticker := time.NewTicker(periodicInterval)
	defer ticker.Stop()
	backoff := minEstablishBackoff
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		reason, wasAvailable := r.runOnce(ctx, ticker.C)
		if wasAvailable {
			backoff = minEstablishBackoff
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		r.emitUnavailable(reason, time.Now())
		timer := time.NewTimer(backoff)
		waiting := true
		for waiting {
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-ticker.C:
				r.emitUnavailable(reason, time.Now())
			case <-timer.C:
				waiting = false
			}
		}
		backoff = min(backoff*2, maxEstablishBackoff)
	}
}

// runOnce opens a monitor, creates a generation, and runs it until it
// fails or ctx is cancelled. It returns the reason string describing why
// the generation ended and whether the generation ever reached available
// state.
func (r *runner) runOnce(ctx context.Context, periodic <-chan time.Time) (string, bool) {
	mon, err := r.newMonitor()
	if err != nil {
		return "subscription_open_failed", false
	}
	collect := r.collect
	if collect == nil {
		collect = func(ctx context.Context) (map[int32]*trackedLink, error) {
			return dumpLinks(ctx, r.filter)
		}
	}
	g := newGeneration(mon, r.emitter, r.filter, collect)
	err = g.run(ctx, periodic)
	wasAvailable := g.status.Available
	_ = g.monitor.Close()
	var failed *collectionFailure
	if errors.As(err, &failed) {
		return dumpReason(failed.err), wasAvailable
	}
	return statusReason(err), wasAvailable
}

func (r *runner) emitUnavailable(reason string, observedAt time.Time) {
	r.emitter.Emit(Record{
		Reason: ReasonPeriodic, ObservedAt: observedAt, EmittedAt: time.Now(),
		Status: SectionStatus{Reason: reason},
	})
}

// generation owns one socket lifetime: the monitor, tracked state, and all
// state-mutation and emission methods. A new generation starts with fresh
// state by construction. All methods are called from a single goroutine.
type generation struct {
	monitor notificationMonitor

	// tracked state
	links       map[int32]*trackedLink
	status      SectionStatus
	lastApplied uint64

	// dependencies (passed in, never mutated)
	emitter *Emitter
	filter  LinkFilter
	collect func(context.Context) (map[int32]*trackedLink, error)
}

func newGeneration(mon notificationMonitor, emitter *Emitter, filter LinkFilter, collect func(context.Context) (map[int32]*trackedLink, error)) *generation {
	return &generation{
		monitor: mon,
		links:   map[int32]*trackedLink{},
		status:  SectionStatus{Reason: "collection_pending"},
		emitter: emitter,
		filter:  filter,
		collect: collect,
	}
}

type collectionFailure struct{ err error }

func (e *collectionFailure) Error() string { return e.err.Error() }
func (e *collectionFailure) Unwrap() error { return e.err }

func statusReason(err error) string {
	switch {
	case errors.Is(err, rtnl.ErrOverflow):
		return "kernel_notification_overflow"
	case errors.Is(err, errStateGap):
		return "malformed_notification"
	case err == nil:
		return "subscription_closed"
	default:
		return "subscription_lost"
	}
}

// run establishes state via a dump, then polls the monitor for events until
// a fatal error or ctx cancellation. The monitor's receive timeout (20ms)
// bounds how long each poll blocks, allowing periodic and cancellation
// checks between receives.
func (g *generation) run(ctx context.Context, periodic <-chan time.Time) error {
	if err := g.establish(ctx); err != nil {
		return err
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		select {
		case <-periodic:
			g.emitState(time.Now())
		default:
		}
		events, err := g.monitor.Recv()
		if err != nil {
			return err
		}
		for _, ev := range events {
			g.lastApplied++
			obs, err := g.apply(ev)
			if err != nil {
				return err
			}
			if len(obs) > 0 {
				g.emitChange(obs, ev.ObservedAt)
			}
		}
	}
}

// establish dumps a link baseline, then replays every notification queued
// on the monitor since it subscribed, and transitions to available. The
// monitor is subscribed before the dump starts, so the kernel socket buffer
// holds every change the dump might have missed; replaying all of them in
// order onto the dump converges on current state even when some predate the
// dump. Replay discards Observations: establish reports the resulting state
// as a whole, never a diff against the dump. A kernel overflow during the
// dump surfaces from Drain as ErrOverflow and forces re-establishment.
func (g *generation) establish(ctx context.Context) error {
	dumpCtx, cancel := context.WithTimeout(ctx, dumpAttemptTimeout)
	links, err := g.collect(dumpCtx)
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &collectionFailure{err}
	}
	g.links = links
	events, err := g.monitor.Drain()
	if err != nil {
		return err
	}
	for _, ev := range events {
		g.lastApplied++
		if _, err := g.apply(ev); err != nil {
			return err
		}
	}
	g.status = SectionStatus{Available: true}
	g.emitState(time.Now())
	return nil
}

func dumpLinks(ctx context.Context, filter LinkFilter) (map[int32]*trackedLink, error) {
	section, err := rtnl.FilteredDump(ctx, unix.RTM_GETLINK, unix.SizeofIfInfomsg, time.Now().Add(dumpAttemptTimeout), filter, rtnl.MaxEntries, maxScanLinks)
	if err != nil {
		return nil, err
	}
	if section.Truncated {
		return nil, errDumpTruncated
	}
	links := make(map[int32]*trackedLink, len(section.Entries))
	for _, entry := range section.Entries {
		state, err := linkStateFromDecoded(entry)
		if err != nil {
			return nil, err
		}
		links[state.Ifindex] = newTrackedLink(state)
	}
	resolvePairings(links)
	return links, nil
}

func dumpReason(err error) string {
	switch {
	case errors.Is(err, rtnl.ErrDumpDeadlineExceeded), errors.Is(err, context.DeadlineExceeded):
		return "dump_deadline_exceeded"
	case errors.Is(err, rtnl.ErrDumpInterrupted):
		return "dump_interrupted"
	case errors.Is(err, errDumpTruncated):
		return "selected_state_truncated"
	default:
		return "dump_failed"
	}
}

var errDumpTruncated = errors.New("selected-state dump truncated")

// apply decodes one notification's effect on tracked state and returns the
// Observations it produced (nil for a no-op or an ignored/unselected
// interface). A non-nil error means the notification signaled a state gap
// that only a full re-establishment can resolve.
func (g *generation) apply(ev rtnl.Event) ([]Observation, error) {
	if ev.Malformed {
		switch ev.Type {
		case unix.RTM_NEWLINK, unix.RTM_DELLINK:
			return nil, errStateGap
		}
		return nil, nil
	}
	switch ev.Type {
	case unix.RTM_NEWLINK:
		return g.applyLink(ev), nil
	case unix.RTM_DELLINK:
		return g.applyLinkDelete(ev), nil
	default:
		return nil, nil
	}
}

func (g *generation) applyLink(ev rtnl.Event) []Observation {
	newState, err := linkStateFromDecoded(ev.Body)
	if err != nil {
		return nil
	}
	existing, tracked := g.links[newState.Ifindex]
	if !tracked {
		if !g.filter(ev.Body) {
			return nil
		}
		g.links[newState.Ifindex] = newTrackedLink(newState)
		resolvePairings(g.links)
		return []Observation{observeAppearance(newState)}
	}
	before := *existing
	obs := compareLinks(newState.Ifindex, before.Current, newState)
	existing.update(newState)
	resolvePairings(g.links)
	enrichPairingObservations(obs, before)
	return obs
}

func (g *generation) applyLinkDelete(ev rtnl.Event) []Observation {
	idx, ok := ev.Body["index"].(int32)
	if !ok {
		return nil
	}
	existing, tracked := g.links[idx]
	if !tracked {
		return nil
	}
	trigger := Trigger{Notification: "DELLINK"}
	if v, ok := ev.Body["newNetnsID"].(int32); ok {
		trigger.NewNetnsID = &v
	}
	if v, ok := ev.Body["newIfindex"].(int32); ok {
		trigger.NewIfindex = &v
	}
	obs := observeDisappearance(*existing, trigger)
	delete(g.links, idx)
	return []Observation{obs}
}

// emit builds the common Record envelope - timestamps, sequencing, status,
// and the full resulting state whenever status is available - and hands it
// to the Emitter. Every emission path funnels through here so that
// invariant is enforced once.
func (g *generation) emit(reason Reason, observedAt time.Time, obs []Observation) {
	rec := Record{
		Reason: reason, ObservedAt: observedAt, EmittedAt: time.Now(),
		LastAppliedNotification: g.lastApplied,
		Status:                  g.status,
		Observations:            obs,
	}
	if g.status.Available {
		rec.State = g.snapshotState()
	}
	g.emitter.Emit(rec)
}

func (g *generation) snapshotState() []LinkState {
	state := make([]LinkState, 0, len(g.links))
	for _, link := range g.links {
		state = append(state, link.Current)
	}
	return state
}

func (g *generation) emitState(observedAt time.Time) {
	g.emit(ReasonPeriodic, observedAt, nil)
}

func (g *generation) emitChange(obs []Observation, observedAt time.Time) {
	g.emit(ReasonChange, observedAt, obs)
}
