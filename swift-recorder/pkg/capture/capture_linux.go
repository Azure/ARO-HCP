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

package capture

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const maxEntries = 256

// Run enters path and writes namespace-local rtnetlink state as JSON. It MUST
// only be called in a dedicated child process whose main exits after returning:
// the calling OS thread remains locked and is never restored to its old netns.
// The caller enforces its configured namespace directory; Run accepts any real
// parent directory, but requires a clean absolute path with no symlink components
// and a cni- basename. No sysfs is read: its mount may describe a different netns.
func Run(path string, output io.Writer) error {
	fd, err := openNamespace(path)
	if err != nil {
		return err
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		_ = unix.Close(fd)
		return fmt.Errorf("stat network namespace: %w", err)
	}
	runtime.LockOSThread()
	err = unix.Setns(fd, unix.CLONE_NEWNET)
	_ = unix.Close(fd)
	if err != nil {
		return fmt.Errorf("enter network namespace: %w", err)
	}
	result, err := collect()
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(captureResult{
		Namespace: namespaceIdentity{Device: uint64(stat.Dev), Inode: stat.Ino},
		State:     result,
	})
}

// Identity comes from the same pinned descriptor used by setns, not a second
// path lookup. No capture timestamp is included, so unchanged state can dedupe.
type namespaceIdentity struct {
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
}

type captureResult struct {
	Namespace namespaceIdentity  `json:"namespace"`
	State     map[string]section `json:"state"`
}

func openNamespace(path string) (int, error) {
	base := filepath.Base(path)
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || !strings.HasPrefix(base, "cni-") || len(base) <= len("cni-") {
		return -1, fmt.Errorf("namespace path must be clean, absolute and have a cni- basename")
	}
	parent := filepath.Dir(path)
	root, err := os.OpenRoot(parent)
	if err != nil {
		return -1, fmt.Errorf("open namespace parent: %w", err)
	}
	defer root.Close()
	dir, err := root.OpenFile(".", os.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		return -1, err
	}
	defer dir.Close()
	// OpenRoot confines later lookups, but follows symlinks in its own path.
	// Validate the pinned parent with a no-symlinks lookup, failing closed if
	// openat2 is unavailable or the directory changed between the two opens.
	verified, err := unix.Openat2(unix.AT_FDCWD, parent, &unix.OpenHow{
		Flags: unix.O_PATH | unix.O_DIRECTORY | unix.O_CLOEXEC, Resolve: unix.RESOLVE_NO_SYMLINKS,
	})
	if err != nil {
		return -1, fmt.Errorf("validate namespace parent: %w", err)
	}
	var actual, expected unix.Stat_t
	statErr := unix.Fstat(verified, &expected)
	_ = unix.Close(verified)
	if statErr != nil {
		return -1, statErr
	}
	if err := unix.Fstat(int(dir.Fd()), &actual); err != nil {
		return -1, err
	}
	if actual.Dev != expected.Dev || actual.Ino != expected.Ino {
		return -1, fmt.Errorf("namespace parent changed during validation")
	}
	fd, err := unix.Openat(int(dir.Fd()), base, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return -1, fmt.Errorf("open namespace: %w", err)
	}
	var fs unix.Statfs_t
	if err = unix.Fstatfs(fd, &fs); err == nil && fs.Type != unix.NSFS_MAGIC {
		err = fmt.Errorf("namespace file is not nsfs")
	}
	if err == nil {
		var kind int
		kind, err = unix.IoctlRetInt(fd, unix.NS_GET_NSTYPE)
		if err == nil && kind != unix.CLONE_NEWNET {
			err = fmt.Errorf("namespace is not a network namespace")
		}
	}
	if err != nil {
		_ = unix.Close(fd)
		return -1, fmt.Errorf("validate namespace: %w", err)
	}
	return fd, nil
}

type section struct {
	Entries   []map[string]any `json:"entries"`
	Truncated bool             `json:"truncated"`
}

func collect() (map[string]section, error) {
	deadline := time.Now().Add(350 * time.Millisecond)
	result := make(map[string]section, 5)
	for _, request := range []struct {
		name string
		kind uint16
		size int
	}{
		{"links", unix.RTM_GETLINK, unix.SizeofIfInfomsg},
		{"addresses", unix.RTM_GETADDR, unix.SizeofIfAddrmsg},
		{"routes", unix.RTM_GETROUTE, unix.SizeofRtMsg},
		{"rules", unix.RTM_GETRULE, unix.SizeofRtMsg},
		{"neighbors", unix.RTM_GETNEIGH, unix.SizeofNdMsg},
	} {
		entries, err := dump(request.kind, request.size, deadline)
		if err != nil {
			return nil, fmt.Errorf("dump %s: %w", request.name, err)
		}
		result[request.name] = entries
	}
	return result, nil
}

// Each dump uses its own socket so stopping at the cap cannot leave unread
// multipart replies to contaminate the next dump. Memory is bounded throughout.
func dump(kind uint16, size int, deadline time.Time) (section, error) {
	result := section{Entries: make([]map[string]any, 0)}
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_ROUTE)
	if err != nil {
		return result, err
	}
	defer unix.Close(fd)
	if err := unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return result, err
	}
	timeout := unix.NsecToTimeval((100 * time.Millisecond).Nanoseconds())
	if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_SNDTIMEO, &timeout); err != nil {
		return result, err
	}
	req := make([]byte, unix.NLMSG_HDRLEN+size)
	order := binary.NativeEndian
	order.PutUint32(req, uint32(len(req)))
	order.PutUint16(req[4:], kind)
	order.PutUint16(req[6:], unix.NLM_F_REQUEST|unix.NLM_F_DUMP)
	order.PutUint32(req[8:], 1)
	if err := unix.Sendto(fd, req, 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return result, err
	}
	buf := make([]byte, 64*1024)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return result, fmt.Errorf("netlink deadline exceeded")
		}
		timeout = unix.NsecToTimeval(min(remaining, 100*time.Millisecond).Nanoseconds())
		if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &timeout); err != nil {
			return result, err
		}
		n, _, flags, from, err := unix.Recvmsg(fd, buf, nil, 0)
		if err != nil {
			return result, err
		}
		peer, ok := from.(*unix.SockaddrNetlink)
		if !ok || peer.Pid != 0 || flags&unix.MSG_TRUNC != 0 {
			return result, fmt.Errorf("invalid or oversized netlink datagram")
		}
		messages, err := syscall.ParseNetlinkMessage(buf[:n])
		if err != nil {
			return result, err
		}
		for _, msg := range messages {
			if msg.Header.Seq != 1 || msg.Header.Flags&unix.NLM_F_DUMP_INTR != 0 {
				return result, fmt.Errorf("interrupted or mismatched netlink dump")
			}
			if msg.Header.Type == unix.NLMSG_DONE || msg.Header.Type == unix.NLMSG_ERROR {
				if len(msg.Data) >= 4 && int32(order.Uint32(msg.Data)) != 0 {
					return result, syscall.Errno(-int32(order.Uint32(msg.Data)))
				}
				if msg.Header.Type == unix.NLMSG_DONE {
					return result, nil
				}
				return result, fmt.Errorf("unexpected netlink acknowledgement")
			}
			if msg.Header.Type != kind-2 {
				return result, fmt.Errorf("unexpected netlink message type %d", msg.Header.Type)
			}
			if len(result.Entries) == maxEntries {
				result.Truncated = true
				return result, nil
			}
			entry, err := decode(kind, size, msg.Data)
			if err != nil {
				return result, err
			}
			result.Entries = append(result.Entries, entry)
		}
	}
}

func decode(kind uint16, size int, data []byte) (map[string]any, error) {
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
