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

// Package rtnl is the shared, low-level rtnetlink wire-format code: request a
// bounded dump, or subscribe to multicast route-group notifications, and
// decode either into plain maps. No `ip`, `ethtool` or other external binary
// is used. Two callers share this: swift-recorder/pkg/capture (a one-shot
// dump inside a namespace it already entered, per pod) and
// swift-recorder/pkg/netwatch (a long-lived root-namespace multicast
// subscriber, entering nothing).
package rtnl

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// MaxEntries bounds a plain Dump. Memory is bounded throughout: a dump that
// hits the cap is reported Truncated rather than growing further.
const MaxEntries = 256

// ErrDumpDeadlineExceeded reports that a dump did not finish reading before
// its deadline. Unlike Section.Truncated (a complete, bounded read that hit
// MaxEntries/maxRetain), this means the read stopped mid-dump: the caller
// has neither a complete nor a soundly-bounded-partial result and must
// discard it, not treat it as available state.
var ErrDumpDeadlineExceeded = errors.New("netlink dump deadline exceeded")

// ErrDumpInterrupted reports that the kernel marked the dump inconsistent
// (NLM_F_DUMP_INTR, e.g. a concurrent mutation reused a sequence cookie) or
// a reply did not match the request this Dump/FilteredDump call sent. Either
// way the read so far cannot be trusted as a coherent snapshot and must be
// discarded and retried, not merged with a later attempt.
var ErrDumpInterrupted = errors.New("netlink dump interrupted or mismatched")

// Section is one bounded rtnetlink dump.
type Section struct {
	Entries   []map[string]any `json:"entries"`
	Truncated bool             `json:"truncated"`
}

// Dump requests one rtnetlink dump (kind is a RTM_GET* request type, e.g.
// unix.RTM_GETLINK) and decodes every reply entry, retaining up to
// MaxEntries of them regardless of kind - the existing pod-capture behavior,
// unchanged. A caller wanting only entries matching some predicate, without
// an unrelated namespace-wide population crowding out MaxEntries before
// filtering, wants FilteredDump instead.
//
// Dump operates in whatever network namespace the calling thread is currently
// in - it neither enters nor requires entering any particular one. A caller
// that must read a specific namespace's state is responsible for having
// already entered it (e.g. via setns on a locked OS thread); a caller that
// wants only the root namespace simply calls this without doing so.
//
// Every error return carries an empty Section, never a partial one: a caller
// must discard an unsuccessful attempt wholesale and retry, not merge it
// with anything read afterward.
func Dump(kind uint16, size int, deadline time.Time) (Section, error) {
	return dump(context.Background(), kind, size, deadline, nil, MaxEntries, MaxEntries)
}

// FilteredDump behaves like Dump but retains only entries for which keep
// returns true, discarding the rest immediately rather than letting them
// crowd out maxRetain accepted entries. It still bounds the number of
// messages it will read via maxScan (independent of the deadline) so a
// namespace containing many entries keep rejects cannot make collection scan
// unboundedly even though almost nothing is kept. Section.Truncated reports
// that either bound stopped collection before the dump's NLMSG_DONE - a
// selected-state cap, not merely the namespace-wide MaxEntries.
func FilteredDump(ctx context.Context, kind uint16, size int, deadline time.Time, keep func(map[string]any) bool, maxRetain, maxScan int) (Section, error) {
	return dump(ctx, kind, size, deadline, keep, maxRetain, maxScan)
}

func dump(ctx context.Context, kind uint16, size int, deadline time.Time, keep func(map[string]any) bool, maxRetain, maxScan int) (Section, error) {
	if err := ctx.Err(); err != nil {
		return Section{}, err
	}
	result := Section{Entries: make([]map[string]any, 0)}
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_ROUTE)
	if err != nil {
		return Section{}, err
	}
	defer unix.Close(fd)
	if err := unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return Section{}, err
	}
	timeout := unix.NsecToTimeval((100 * time.Millisecond).Nanoseconds())
	if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_SNDTIMEO, &timeout); err != nil {
		return Section{}, err
	}
	req := make([]byte, unix.NLMSG_HDRLEN+size)
	order := binary.NativeEndian
	order.PutUint32(req, uint32(len(req)))
	order.PutUint16(req[4:], kind)
	order.PutUint16(req[6:], unix.NLM_F_REQUEST|unix.NLM_F_DUMP)
	order.PutUint32(req[8:], 1)
	if err := unix.Sendto(fd, req, 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return Section{}, err
	}
	buf := make([]byte, 64*1024)
	scanned := 0
	for {
		if err := ctx.Err(); err != nil {
			return Section{}, err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return Section{}, ErrDumpDeadlineExceeded
		}
		timeout = unix.NsecToTimeval(min(remaining, 100*time.Millisecond).Nanoseconds())
		if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &timeout); err != nil {
			return Section{}, err
		}
		n, _, flags, from, err := unix.Recvmsg(fd, buf, nil, 0)
		if err != nil {
			// See rtnl.Monitor.Recv: a raw blocking syscall can return EINTR
			// from a signal that isn't a real error (notably Go's own
			// asynchronous goroutine preemption). The deadline check at the
			// top of this loop still bounds how long retrying can continue.
			if errors.Is(err, unix.EINTR) {
				continue
			}
			if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) {
				continue
			}
			return Section{}, err
		}
		peer, ok := from.(*unix.SockaddrNetlink)
		if !ok || peer.Pid != 0 || flags&unix.MSG_TRUNC != 0 {
			return Section{}, fmt.Errorf("invalid or oversized netlink datagram")
		}
		messages, err := syscall.ParseNetlinkMessage(buf[:n])
		if err != nil {
			return Section{}, err
		}
		for _, msg := range messages {
			if err := ctx.Err(); err != nil {
				return Section{}, err
			}
			if msg.Header.Seq != 1 || msg.Header.Flags&unix.NLM_F_DUMP_INTR != 0 {
				return Section{}, ErrDumpInterrupted
			}
			if msg.Header.Type == unix.NLMSG_DONE || msg.Header.Type == unix.NLMSG_ERROR {
				if len(msg.Data) >= 4 && int32(order.Uint32(msg.Data)) != 0 {
					return Section{}, syscall.Errno(-int32(order.Uint32(msg.Data)))
				}
				if msg.Header.Type == unix.NLMSG_DONE {
					return result, nil
				}
				return Section{}, fmt.Errorf("unexpected netlink acknowledgement")
			}
			if msg.Header.Type != kind-2 {
				return Section{}, fmt.Errorf("unexpected netlink message type %d", msg.Header.Type)
			}
			if scanned == maxScan {
				result.Truncated = true
				return result, nil
			}
			scanned++
			entry, err := Decode(kind, size, msg.Data)
			if err != nil {
				return Section{}, err
			}
			if keep != nil && !keep(entry) {
				continue
			}
			if len(result.Entries) == maxRetain {
				result.Truncated = true
				return result, nil
			}
			result.Entries = append(result.Entries, entry)
		}
	}
}

// Decode interprets one rtnetlink message body according to the schema named
// by kind (a RTM_GET* request type - GETLINK's schema also matches the
// NEWLINK/DELLINK notifications a Monitor delivers, GETADDR's matches
// NEWADDR/DELADDR, and so on: the request and notification message bodies for
// a given object share one wire format).
func Decode(kind uint16, size int, data []byte) (map[string]any, error) {
	if len(data) < size {
		return nil, fmt.Errorf("short netlink message")
	}
	attrs := make(map[uint16][]byte)
	order := binary.NativeEndian
	for rest := data[size:]; len(rest) > 0; {
		if len(rest) < 4 {
			return nil, fmt.Errorf("short netlink attribute header")
		}
		n := int(order.Uint16(rest))
		aligned := (n + 3) &^ 3
		if n < 4 || aligned > len(rest) {
			return nil, fmt.Errorf("invalid netlink attribute length")
		}
		attrs[order.Uint16(rest[2:])&0x3fff] = rest[4:n] // Mask nested/network-byte-order flags.
		rest = rest[aligned:]
	}
	u32 := func(key uint16) uint32 {
		if len(attrs[key]) >= 4 {
			return order.Uint32(attrs[key])
		}
		return 0
	}
	text := func(key uint16) string { return string(bytes.TrimRight(attrs[key], "\x00")) }
	ip := func(key uint16) string {
		if value := attrs[key]; len(value) == net.IPv4len || len(value) == net.IPv6len {
			return net.IP(value).String()
		}
		return ""
	}
	entry := map[string]any{"family": data[0]}
	switch kind {
	case unix.RTM_GETLINK:
		flags := order.Uint32(data[8:])
		entry["name"], entry["index"] = text(unix.IFLA_IFNAME), int32(order.Uint32(data[4:]))
		entry["mac"] = net.HardwareAddr(attrs[unix.IFLA_ADDRESS]).String()
		entry["flags"], entry["up"] = flags, flags&unix.IFF_UP != 0
		entry["running"], entry["lowerUp"] = flags&unix.IFF_RUNNING != 0, flags&unix.IFF_LOWER_UP != 0
		if carrier := attrs[unix.IFLA_CARRIER]; len(carrier) == 1 {
			entry["carrier"] = carrier[0] != 0
		}
		entry["masterIndex"], entry["parentIndex"] = u32(unix.IFLA_MASTER), u32(unix.IFLA_LINK)
		entry["mtu"] = u32(unix.IFLA_MTU)
		if state := attrs[unix.IFLA_OPERSTATE]; len(state) == 1 {
			entry["operstate"] = state[0]
		}
		// parentBus/parentDevice name the bus type ("vmbus"/"pci") and bus
		// address/GUID backing this netdev - available directly from the
		// kernel (IFLA_PARENT_DEV_BUS_NAME/IFLA_PARENT_DEV_NAME, kernel >=
		// 5.19; ip -d link show prints these as "parentbus"/"parentdev") with
		// no sysfs read and independent of which namespace this link lives
		// in. Together with masterIndex (the enslavement proof) this fully
		// reproduces the sysfs lower_*/upper_*/master/device evidence for any
		// interface actually present in a dump or notification.
		if v := text(unix.IFLA_PARENT_DEV_BUS_NAME); len(v) > 0 {
			entry["parentBus"] = v
		}
		if v := text(unix.IFLA_PARENT_DEV_NAME); len(v) > 0 {
			entry["parentDevice"] = v
		}
		// newNetnsID/newIfindex are present only on a DELLINK notification
		// caused by the device moving to another network namespace (never on
		// a dump reply, and never on a NEWLINK). They identify the
		// destination namespace (as a small integer local to the observing
		// namespace's peer-netns table, not a global id) and the interface's
		// ifindex there - the only place this information exists; it cannot
		// be recovered by dumping state again afterward.
		if len(attrs[unix.IFLA_NEW_NETNSID]) >= 4 {
			entry["newNetnsID"] = int32(u32(unix.IFLA_NEW_NETNSID))
		}
		if len(attrs[unix.IFLA_NEW_IFINDEX]) >= 4 {
			entry["newIfindex"] = int32(u32(unix.IFLA_NEW_IFINDEX))
		}
		// Master/parent indices expose synthetic/VF relationships. These
		// counters describe observations, not the selected or working datapath.
		stats, width := attrs[unix.IFLA_STATS64], 8
		if len(stats) < 8*width {
			stats, width = attrs[unix.IFLA_STATS], 4
		}
		if len(stats) >= 8*width {
			values := make(map[string]uint64, 8)
			for i, name := range []string{"rxPackets", "txPackets", "rxBytes", "txBytes", "rxErrors", "txErrors", "rxDrops", "txDrops"} {
				if width == 8 {
					values[name] = order.Uint64(stats[i*width:])
				} else {
					values[name] = uint64(order.Uint32(stats[i*width:]))
				}
			}
			entry["stats"] = values
		}
	case unix.RTM_GETADDR:
		entry["index"], entry["prefixLength"], entry["scope"] = order.Uint32(data[4:]), data[1], data[3]
		entry["flags"] = uint32(data[2])
		if len(attrs[unix.IFA_FLAGS]) >= 4 {
			entry["flags"] = u32(unix.IFA_FLAGS)
		}
		entry["address"], entry["local"], entry["label"] = ip(unix.IFA_ADDRESS), ip(unix.IFA_LOCAL), text(unix.IFA_LABEL)
	case unix.RTM_GETROUTE:
		entry["destinationPrefixLength"], entry["sourcePrefixLength"] = data[1], data[2]
		entry["tos"], entry["table"], entry["protocol"], entry["scope"], entry["type"] = data[3], uint32(data[4]), data[5], data[6], data[7]
		entry["flags"] = order.Uint32(data[8:])
		if len(attrs[unix.RTA_TABLE]) >= 4 {
			entry["table"] = u32(unix.RTA_TABLE)
		}
		entry["destination"], entry["source"], entry["gateway"], entry["preferredSource"] = ip(unix.RTA_DST), ip(unix.RTA_SRC), ip(unix.RTA_GATEWAY), ip(unix.RTA_PREFSRC)
		entry["inputIndex"], entry["outputIndex"], entry["priority"] = u32(unix.RTA_IIF), u32(unix.RTA_OIF), u32(unix.RTA_PRIORITY)
	case unix.RTM_GETRULE:
		entry["destinationPrefixLength"], entry["sourcePrefixLength"] = data[1], data[2]
		entry["tos"], entry["table"], entry["action"], entry["flags"] = data[3], uint32(data[4]), data[7], order.Uint32(data[8:])
		if len(attrs[unix.FRA_TABLE]) >= 4 {
			entry["table"] = u32(unix.FRA_TABLE)
		}
		entry["destination"], entry["source"] = ip(unix.FRA_DST), ip(unix.FRA_SRC)
		entry["inputName"], entry["outputName"] = text(unix.FRA_IIFNAME), text(unix.FRA_OIFNAME)
		entry["priority"], entry["mark"], entry["mask"], entry["goto"] = u32(unix.FRA_PRIORITY), u32(unix.FRA_FWMARK), u32(unix.FRA_FWMASK), u32(unix.FRA_GOTO)
	case unix.RTM_GETNEIGH:
		entry["index"], entry["state"], entry["flags"], entry["type"] = int32(order.Uint32(data[4:])), order.Uint16(data[8:]), data[10], data[11]
		entry["destination"], entry["mac"] = ip(unix.NDA_DST), net.HardwareAddr(attrs[unix.NDA_LLADDR]).String()
	}
	return entry, nil
}
