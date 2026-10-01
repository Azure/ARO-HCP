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
	"errors"
	"fmt"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Event is one rtnetlink change notification.
type Event struct {
	// Type is the raw notification type (unix.RTM_NEWLINK, RTM_DELLINK,
	// RTM_NEWADDR, RTM_DELADDR, ...).
	Type uint16
	// Body is Decode-d using the RTM_GET* schema matching Type (NEWLINK and
	// DELLINK both decode with RTM_GETLINK's schema, and so on). Nil when
	// Malformed is true.
	Body map[string]any
	// Malformed is true when Type named a relevant rtnetlink object
	// (decodeSchemaFor matched) but Decode failed on its body - a corrupt or
	// unexpectedly shaped message for a family the caller does care about.
	// Unlike a message from an entirely unrelated family (decodeSchemaFor
	// ok=false, which Recv filters out before ever constructing an Event),
	// this must not be silently dropped: the caller's tracked state for
	// this object may now be stale and cannot be repaired by reading
	// further notifications alone.
	Malformed bool
	// DecodeErr is the Decode error when Malformed is true, nil otherwise.
	DecodeErr error
	// ObservedAt is this process's wall-clock time immediately after the
	// syscall that delivered the notification returned - captured here, in
	// the receive path, and nowhere later, so it reflects true arrival order
	// regardless of what a caller does with the event afterward.
	ObservedAt time.Time
}

// ErrOverflow reports that the kernel's rtnetlink multicast socket buffer
// overflowed and notifications were dropped before this Monitor could read
// them (recvmsg returned ENOBUFS, or a single datagram was truncated). Unlike
// a Kubernetes watch, rtnetlink multicast delivery is not reliable: there is
// no resourceVersion, no relist signal beyond this error, and no replay. A
// caller that keeps its own running state MUST treat ErrOverflow as "state
// may have changed by an unknown amount" and reconcile by dumping fresh state,
// not merely resume reading.
var ErrOverflow = errors.New("rtnetlink notification socket overflowed; events were lost")

// decodeSchemaFor maps a notification type to the RTM_GET* constant whose schema
// Decode should use to interpret it. Unrecognized types return ok=false and
// are silently omitted by Recv, not treated as an error.
func decodeSchemaFor(notificationType uint16) (kind uint16, size int, ok bool) {
	switch notificationType {
	case unix.RTM_NEWLINK, unix.RTM_DELLINK:
		return unix.RTM_GETLINK, unix.SizeofIfInfomsg, true
	case unix.RTM_NEWADDR, unix.RTM_DELADDR:
		return unix.RTM_GETADDR, unix.SizeofIfAddrmsg, true
	default:
		return 0, 0, false
	}
}

// Monitor is a long-lived subscription to rtnetlink multicast group
// notifications in whatever network namespace it is opened in. It never
// enters any other namespace and never issues a dump request; Dump is
// separate.
//
// NETLINK_NO_ENOBUFS is deliberately never set: the socket option would
// suppress the overflow error return, not the underlying message loss, and a
// caller relies on seeing ErrOverflow to know a reconcile is required.
//
// The socket has a receive timeout so a caller loop can check context
// cancellation between Recv calls: closing fd from another goroutine does not
// reliably unblock a concurrent blocking Recvmsg on Linux.
type Monitor struct {
	fd  int
	buf []byte
}

// NewMonitor opens a NETLINK_ROUTE socket and joins the given multicast
// groups (e.g. unix.RTNLGRP_LINK, unix.RTNLGRP_IPV4_IFADDR). Joining a route
// multicast group requires no capability - only mutating the state it reports
// does. recvTimeout bounds how long Recv blocks when idle; NewMonitor uses 1
// second if recvTimeout is zero.
func NewMonitor(recvTimeout time.Duration, groups ...int) (*Monitor, error) {
	if recvTimeout <= 0 {
		recvTimeout = time.Second
	}
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_ROUTE)
	if err != nil {
		return nil, err
	}
	addr := &unix.SockaddrNetlink{Family: unix.AF_NETLINK}
	for _, g := range groups {
		addr.Groups |= 1 << (uint(g) - 1)
	}
	if err := unix.Bind(fd, addr); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	timeout := unix.NsecToTimeval(recvTimeout.Nanoseconds())
	if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &timeout); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	return &Monitor{fd: fd, buf: make([]byte, 64*1024)}, nil
}

// Close releases the underlying socket. Safe to call once; concurrent Recv
// calls are not supported and will race.
func (m *Monitor) Close() error { return unix.Close(m.fd) }

// Recv blocks up to the socket's receive timeout for notifications, and
// returns every event parsed from one datagram (a single recvmsg can carry
// several). A timeout returns (nil, nil) - not an error - so a caller loop
// can check context cancellation and call Recv again indefinitely.
//
// It returns ErrOverflow if the kernel reports lost messages, or if a single
// datagram was truncated - both mean a caller's own accounting of "what have
// I seen" is stale and a full reconcile is required, not merely resuming Recv.
func (m *Monitor) Recv() ([]Event, error) {
	events, _, err := m.recv(0)
	return events, err
}

// maxDrainEvents bounds one Drain. Drain runs once per baseline, right after
// the dump, and must terminate even if notifications keep arriving as fast as
// they are read: exceeding the bound is reported as ErrOverflow so the caller
// discards the generation and rebuilds, exactly as for a kernel overflow.
const maxDrainEvents = 4096

// Drain returns every notification already queued on the socket without
// blocking, in arrival order, stopping once the queue is empty. Errors match
// Recv, plus ErrOverflow after more than maxDrainEvents events.
func (m *Monitor) Drain() ([]Event, error) {
	var all []Event
	for {
		events, received, err := m.recv(unix.MSG_DONTWAIT)
		if err != nil || !received {
			return all, err
		}
		all = append(all, events...)
		if len(all) > maxDrainEvents {
			return nil, ErrOverflow
		}
	}
}

// recv reads one datagram. received is false when the socket had nothing to
// read before its timeout (or immediately, with MSG_DONTWAIT).
func (m *Monitor) recv(recvFlags int) (events []Event, received bool, err error) {
	var n int
	var flags int
	var from unix.Sockaddr
	var observedAt time.Time
	for {
		n, _, flags, from, err = unix.Recvmsg(m.fd, m.buf, nil, recvFlags)
		observedAt = time.Now()
		if err != nil {
			// A blocking syscall on a raw fd - not one of Go's own I/O
			// wrappers - can return EINTR when a signal interrupts it. In
			// practice this includes Go's own asynchronous goroutine
			// preemption (SIGURG), which is neither rare nor an actual
			// error: retry the same call rather than surfacing it as fatal.
			// Observed live: an unhandled EINTR here previously crashed the
			// whole process (Run's caller treats any Recv error as fatal),
			// visible in production as "interrupted system call" immediately
			// after the startup snapshot and a CrashLoopBackOff.
			if errors.Is(err, unix.EINTR) {
				continue
			}
			if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) {
				return nil, false, nil
			}
			if errors.Is(err, unix.ENOBUFS) {
				return nil, false, ErrOverflow
			}
			return nil, false, err
		}
		break
	}
	if peer, ok := from.(*unix.SockaddrNetlink); !ok || peer.Pid != 0 {
		return nil, false, fmt.Errorf("notification from unexpected peer")
	}
	if flags&unix.MSG_TRUNC != 0 {
		return nil, false, ErrOverflow
	}
	events, err = parseNotifications(m.buf[:n], observedAt)
	if err != nil {
		return nil, false, err
	}
	return events, true, nil
}

// parseNotifications decodes the netlink messages of one received datagram.
func parseNotifications(datagram []byte, observedAt time.Time) ([]Event, error) {
	messages, err := syscall.ParseNetlinkMessage(datagram)
	if err != nil {
		return nil, err
	}
	events := make([]Event, 0, len(messages))
	for _, msg := range messages {
		// Linux reports multicast loss through ENOBUFS (handled in recv) and,
		// as far as known, never sends NLMSG_OVERRUN on rtnetlink sockets.
		// The type is nonetheless defined by the netlink protocol to mean
		// "data was lost", so it is treated as an overflow rather than
		// skipped as an unrelated message: skipping it would leave the
		// caller's state marked available while possibly stale.
		if msg.Header.Type == unix.NLMSG_OVERRUN {
			return nil, ErrOverflow
		}
		kind, size, ok := decodeSchemaFor(msg.Header.Type)
		if !ok {
			continue
		}
		body, err := Decode(kind, size, msg.Data)
		if err != nil {
			// A relevant family with an undecodable body: the caller's
			// tracked state for this object may now be stale. Surface it
			// rather than continuing as if nothing happened - see Event.
			events = append(events, Event{Type: msg.Header.Type, Malformed: true, DecodeErr: err, ObservedAt: observedAt})
			continue
		}
		events = append(events, Event{Type: msg.Header.Type, Body: body, ObservedAt: observedAt})
	}
	return events, nil
}
