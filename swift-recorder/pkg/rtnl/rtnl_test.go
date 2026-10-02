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

package rtnl

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// linkMessage builds a synthetic RTM_GETLINK-schema message body: a fixed
// ifinfomsg header followed by attr(kind, value) appends.
type linkMessage struct {
	order binary.ByteOrder
	data  []byte
}

func newLinkMessage(flags uint32, ifindex uint32) *linkMessage {
	order := binary.NativeEndian
	data := make([]byte, unix.SizeofIfInfomsg)
	order.PutUint32(data[4:], ifindex)
	order.PutUint32(data[8:], flags)
	return &linkMessage{order: order, data: data}
}

func (m *linkMessage) attr(kind uint16, value []byte) *linkMessage {
	b := make([]byte, (len(value)+7)&^3)
	m.order.PutUint16(b, uint16(len(value)+4))
	m.order.PutUint16(b[2:], kind)
	copy(b[4:], value)
	m.data = append(m.data, b...)
	return m
}

func TestDecodeLink(t *testing.T) {
	order := binary.NativeEndian
	msg := newLinkMessage(unix.IFF_UP|unix.IFF_RUNNING|unix.IFF_LOWER_UP, 8).
		attr(unix.IFLA_IFNAME, []byte("vf0\x00")).
		attr(unix.IFLA_ADDRESS, []byte{0, 1, 2, 3, 4, 5}).
		attr(unix.IFLA_OPERSTATE, []byte{6}). // IF_OPER_UP
		attr(unix.IFLA_CARRIER, []byte{0}).   // Deliberately differs from IFF_RUNNING.
		attr(unix.IFLA_MASTER, order.AppendUint32(nil, 3)).
		attr(unix.IFLA_LINK, order.AppendUint32(nil, 7))
	stats := make([]byte, 8*8)
	for i := range 8 {
		order.PutUint64(stats[i*8:], uint64(i)+1<<33)
	}
	msg.attr(unix.IFLA_STATS64, stats)

	entry, err := Decode(unix.RTM_GETLINK, unix.SizeofIfInfomsg, msg.data)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{
		"name": "vf0", "index": int32(8), "mac": "00:01:02:03:04:05",
		"up": true, "carrier": false, "running": true, "lowerUp": true,
		"masterIndex": uint32(3), "parentIndex": uint32(7),
		"operstate": uint8(6),
	} {
		if entry[key] != want {
			t.Errorf("%s = %v, want %v", key, entry[key], want)
		}
	}
	values := entry["stats"].(map[string]uint64)
	if values["rxPackets"] != 1<<33 || values["txDrops"] != 1<<33+7 {
		t.Fatalf("incorrect 64-bit counters: %v", values)
	}

	// Older devices may expose only the 32-bit stats attribute.
	msg32 := newLinkMessage(unix.IFF_UP|unix.IFF_RUNNING|unix.IFF_LOWER_UP, 8).attr(unix.IFLA_STATS, stats[:32])
	entry, err = Decode(unix.RTM_GETLINK, unix.SizeofIfInfomsg, msg32.data)
	if err != nil || entry["stats"].(map[string]uint64)["txPackets"] != 2 {
		t.Fatalf("incorrect 32-bit counter fallback: %v, %v", entry, err)
	}
	if _, present := entry["carrier"]; present {
		t.Fatal("missing IFLA_CARRIER must not be inferred from flags")
	}

	msgCarrier := newLinkMessage(unix.IFF_UP, 8).attr(unix.IFLA_CARRIER, []byte{1})
	entry, err = Decode(unix.RTM_GETLINK, unix.SizeofIfInfomsg, msgCarrier.data)
	if err != nil || entry["carrier"] != true || entry["running"] != false || entry["lowerUp"] != false {
		t.Fatalf("carrier and running flags were conflated: %v, %v", entry, err)
	}

	for _, malformed := range [][]byte{nil, msg.data[:3], append(msg.data[:unix.SizeofIfInfomsg:unix.SizeofIfInfomsg], 1, 0, 1, 0)} {
		if _, err := Decode(unix.RTM_GETLINK, unix.SizeofIfInfomsg, malformed); err == nil {
			t.Fatal("accepted malformed message")
		}
	}
}

func TestDecodeLinkParentDeviceAndNamespaceMove(t *testing.T) {
	order := binary.NativeEndian

	synthetic := newLinkMessage(unix.IFF_UP, 3).
		attr(unix.IFLA_PARENT_DEV_BUS_NAME, []byte("vmbus\x00")).
		attr(unix.IFLA_PARENT_DEV_NAME, []byte("f8615163-0004-1000-2000-70a8a510829f\x00"))
	entry, err := Decode(unix.RTM_GETLINK, unix.SizeofIfInfomsg, synthetic.data)
	if err != nil {
		t.Fatal(err)
	}
	if entry["parentBus"] != "vmbus" || entry["parentDevice"] != "f8615163-0004-1000-2000-70a8a510829f" {
		t.Fatalf("got parentBus=%v parentDevice=%v", entry["parentBus"], entry["parentDevice"])
	}

	vf := newLinkMessage(unix.IFF_UP, 16).
		attr(unix.IFLA_PARENT_DEV_BUS_NAME, []byte("pci\x00")).
		attr(unix.IFLA_PARENT_DEV_NAME, []byte("7870:00:00.0\x00"))
	entry, err = Decode(unix.RTM_GETLINK, unix.SizeofIfInfomsg, vf.data)
	if err != nil {
		t.Fatal(err)
	}
	if entry["parentBus"] != "pci" || entry["parentDevice"] != "7870:00:00.0" {
		t.Fatalf("got parentBus=%v parentDevice=%v", entry["parentBus"], entry["parentDevice"])
	}

	noParent := newLinkMessage(unix.IFF_UP, 2)
	entry, err = Decode(unix.RTM_GETLINK, unix.SizeofIfInfomsg, noParent.data)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := entry["parentBus"]; present {
		t.Fatal("absent IFLA_PARENT_DEV_BUS_NAME must not be reported")
	}
	if _, present := entry["parentDevice"]; present {
		t.Fatal("absent IFLA_PARENT_DEV_NAME must not be reported")
	}

	// new-netnsid / new-ifindex: present only on the DELLINK a namespace move
	// emits in the source namespace, empirically confirmed to carry these two
	// fields (`ip monitor link` prints "new-netnsid 1 new-ifindex 3" at the
	// exact moment a device is moved to another netns).
	moved := newLinkMessage(0, 3).
		attr(unix.IFLA_NEW_NETNSID, order.AppendUint32(nil, 1)).
		attr(unix.IFLA_NEW_IFINDEX, order.AppendUint32(nil, 3))
	entry, err = Decode(unix.RTM_GETLINK, unix.SizeofIfInfomsg, moved.data)
	if err != nil {
		t.Fatal(err)
	}
	if entry["newNetnsID"] != int32(1) || entry["newIfindex"] != int32(3) {
		t.Fatalf("got newNetnsID=%v newIfindex=%v", entry["newNetnsID"], entry["newIfindex"])
	}

	stayed := newLinkMessage(unix.IFF_UP, 3)
	entry, err = Decode(unix.RTM_GETLINK, unix.SizeofIfInfomsg, stayed.data)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := entry["newNetnsID"]; present {
		t.Fatal("a link that did not move must not report newNetnsID")
	}
}

func TestDumpRejectsExpiredDeadline(t *testing.T) {
	if _, err := Dump(unix.RTM_GETLINK, unix.SizeofIfInfomsg, time.Now().Add(-time.Second)); err == nil {
		t.Fatal("accepted expired netlink deadline")
	}
}

func TestDumpCurrentNamespace(t *testing.T) {
	section, err := Dump(unix.RTM_GETLINK, unix.SizeofIfInfomsg, time.Now().Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(section.Entries) == 0 {
		t.Fatal("expected at least loopback")
	}
	if len(section.Entries) > MaxEntries {
		t.Fatalf("dump exceeded MaxEntries: %d", len(section.Entries))
	}
}

func TestFilteredDumpCancellationDiscardsPartialState(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	section, err := FilteredDump(ctx, unix.RTM_GETLINK, unix.SizeofIfInfomsg, time.Now().Add(time.Second), func(map[string]any) bool {
		cancel()
		return true
	}, MaxEntries, MaxEntries)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("dump returned %v, want cancellation", err)
	}
	if len(section.Entries) != 0 {
		t.Fatalf("cancelled dump retained partial state: %+v", section)
	}
}

func TestDecodeSchemaFor(t *testing.T) {
	tests := []struct {
		notificationType uint16
		wantKind         uint16
		wantOK           bool
	}{
		{unix.RTM_NEWLINK, unix.RTM_GETLINK, true},
		{unix.RTM_DELLINK, unix.RTM_GETLINK, true},
		{unix.RTM_NEWADDR, unix.RTM_GETADDR, true},
		{unix.RTM_DELADDR, unix.RTM_GETADDR, true},
		{unix.RTM_NEWROUTE, 0, false},
		{unix.RTM_DELROUTE, 0, false},
		{unix.RTM_NEWNEIGH, 0, false},
	}
	for _, tt := range tests {
		kind, _, ok := decodeSchemaFor(tt.notificationType)
		if ok != tt.wantOK || (ok && kind != tt.wantKind) {
			t.Errorf("decodeSchemaFor(%d) = %d, %v; want %d, %v", tt.notificationType, kind, ok, tt.wantKind, tt.wantOK)
		}
	}
}

func TestMonitorOpenCloseAndTimeout(t *testing.T) {
	m, err := NewMonitor(200*time.Millisecond, unix.RTNLGRP_LINK)
	if err != nil {
		t.Fatalf("NewMonitor: %v", err)
	}
	defer m.Close()

	start := time.Now()
	events, err := m.Recv()
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if events != nil {
		t.Fatalf("expected no events on a timeout in a quiet namespace, got %v", events)
	}
	if elapsed < 150*time.Millisecond || elapsed > 2*time.Second {
		t.Fatalf("Recv returned after %v, want ~200ms receive timeout", elapsed)
	}
}

func TestNewMonitorDefaultsTimeout(t *testing.T) {
	m, err := NewMonitor(0, unix.RTNLGRP_LINK)
	if err != nil {
		t.Fatalf("NewMonitor: %v", err)
	}
	defer m.Close()
	if _, err := m.Recv(); err != nil {
		t.Fatalf("Recv: %v", err)
	}
}

// TestMonitorRecvRetriesOnEINTR reproduces the production failure directly:
// a signal interrupting the blocked Recvmsg syscall must not surface as an
// error. Delivered live: this crashed every swift-recorder pod with
// "interrupted system call" immediately after the startup snapshot, because
// an unhandled EINTR here propagated up through the fail-fast Run() wiring
// and cancelled the whole process, not just this goroutine.
func TestMonitorRecvRetriesOnEINTR(t *testing.T) {
	const recvTimeout = 1500 * time.Millisecond
	m, err := NewMonitor(recvTimeout, unix.RTNLGRP_LINK)
	if err != nil {
		t.Fatalf("NewMonitor: %v", err)
	}
	defer m.Close()

	// Suppress SIGUSR1's default terminate action for the process; Go routes
	// it to this channel instead. Never read - existence of the
	// registration is what matters.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGUSR1)
	defer signal.Stop(sigCh)

	tid := make(chan int, 1)
	type recvResult struct {
		events []Event
		err    error
	}
	result := make(chan recvResult, 1)
	go func() {
		// Locked so the OS thread whose tid we send below is guaranteed to
		// be the one actually blocked in Recvmsg, not a different one the
		// goroutine might otherwise migrate to.
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		tid <- unix.Gettid()
		events, err := m.Recv()
		result <- recvResult{events, err}
	}()

	targetTID := <-tid
	time.Sleep(100 * time.Millisecond) // let the target thread actually enter the blocking syscall
	if err := unix.Tgkill(os.Getpid(), targetTID, unix.SIGUSR1); err != nil {
		t.Fatalf("Tgkill: %v", err)
	}

	start := time.Now()
	select {
	case r := <-result:
		elapsed := time.Since(start)
		if r.err != nil {
			t.Fatalf("Recv returned an error after being signal-interrupted (this is the production bug): %v", r.err)
		}
		if r.events != nil {
			t.Fatalf("expected nil events from a timeout reached after retrying past EINTR, got %v", r.events)
		}
		// A non-retried EINTR returns within ~100ms (as soon as the signal
		// lands). A correctly retried one waits out the full receive
		// timeout, measured from before the signal was sent.
		if elapsed < recvTimeout/2 {
			t.Fatalf("Recv returned after %v, want it to have retried through to the ~%v timeout", elapsed, recvTimeout)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Recv did not return within 5s")
	}
}
