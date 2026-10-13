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

package probe

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/Azure/ARO-HCP/swift-recorder/pkg/capture"
	"github.com/Azure/ARO-HCP/swift-recorder/pkg/rtnl"
)

func collectEvidence() Evidence {
	result := Evidence{At: time.Now().UTC(), State: map[string]any{}, Listeners: map[string][]string{}, RPFilter: map[string]string{}, Counters: map[string]map[string]uint64{}, CounterReads: map[string]bool{}, Errors: map[string]string{}}
	deadline := time.Now().Add(500 * time.Millisecond)
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
		section, err := rtnl.Dump(request.kind, request.size, deadline)
		if err != nil {
			result.Errors[request.name] = err.Error()
			continue
		}
		result.State[request.name] = section
		if section.Truncated {
			result.Truncated = append(result.Truncated, request.name)
		}
		if request.name == "links" {
			for _, entry := range section.Entries {
				name, _ := entry["name"].(string)
				if counters, ok := entry["stats"].(map[string]uint64); ok {
					result.Counters["link/"+name] = counters
				}
			}
		}
	}
	// /proc/net follows the process leader, which can still be in the host
	// netns. thread-self follows this locked, namespace-entered OS thread.
	for _, name := range []string{"tcp", "tcp6", "udp", "udp6", "snmp", "netstat"} {
		data, truncated, err := readBounded("/proc/thread-self/net/"+name, 64*1024)
		if err != nil {
			result.Errors[name] = err.Error()
			continue
		}
		if truncated {
			result.Truncated = append(result.Truncated, name)
		}
		if name == "snmp" || name == "netstat" {
			result.CounterReads[name] = !truncated
			for group, values := range parseCounters(data) {
				result.Counters[group] = values
			}
			continue
		}
		result.Listeners[name] = []string{}
		scanner := bufio.NewScanner(strings.NewReader(data))
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) < 4 || fields[0] == "sl" {
				continue
			}
			if strings.HasPrefix(name, "tcp") && fields[3] != "0A" {
				continue
			}
			if strings.HasPrefix(name, "udp") && fields[3] != "07" {
				continue
			}
			if len(result.Listeners[name]) == 256 {
				result.Truncated = append(result.Truncated, name+" listeners")
				break
			}
			// Retain only local bindings, not peer traffic, UIDs or inodes.
			binding, err := procBinding(fields[1])
			if err != nil {
				result.Errors[name] = err.Error()
				continue
			}
			result.Listeners[name] = append(result.Listeners[name], binding)
		}
	}
	interfaces := []string{"all", "default"}
	if links, ok := result.State["links"].(rtnl.Section); ok {
		for _, link := range links.Entries {
			if name, ok := link["name"].(string); ok && name != "." && name != ".." && filepath.Base(name) == name {
				interfaces = append(interfaces, name)
			}
		}
	}
	// Network sysctl values are selected by the reading task's current netns;
	// no mount changes or writes are needed.
	for _, name := range interfaces {
		data, truncated, err := readBounded("/proc/sys/net/ipv4/conf/"+name+"/rp_filter", 32)
		if err != nil {
			result.Errors["rp_filter/"+name] = err.Error()
			continue
		}
		if truncated {
			result.Truncated = append(result.Truncated, "rp_filter/"+name)
		}
		result.RPFilter[name] = strings.TrimSpace(data)
	}
	return result
}

func readBounded(path string, limit int64) (string, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer capture.CloseWithLog("evidence file", file.Close)
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return "", false, err
	}
	truncated := int64(len(data)) > limit
	if truncated {
		data = data[:limit]
	}
	return string(data), truncated, nil
}

func parseCounters(data string) map[string]map[string]uint64 {
	result := map[string]map[string]uint64{}
	lines := strings.Split(data, "\n")
	for i := 0; i+1 < len(lines); i += 2 {
		header, values := strings.Fields(lines[i]), strings.Fields(lines[i+1])
		if len(header) < 2 || len(header) != len(values) || header[0] != values[0] {
			continue
		}
		group := strings.TrimSuffix(header[0], ":")
		if result[group] == nil {
			result[group] = map[string]uint64{}
		}
		for j := 1; j < len(header); j++ {
			if value, err := strconv.ParseUint(values[j], 10, 64); err == nil {
				result[group][header[j]] = value
			}
		}
	}
	return result
}

func counterDeltas(before, after Evidence) map[string]map[string]uint64 {
	result := map[string]map[string]uint64{}
	for group, values := range after.Counters {
		for name, value := range values {
			old, ok := before.Counters[group][name]
			// Linux omits zero IcmpMsg counters (and the whole group if all
			// are zero). Absence is not zero when the earlier read failed or
			// was truncated. Dense groups and new links cannot use this rule.
			if !ok && group == "IcmpMsg" && before.CounterReads["snmp"] {
				ok = true
			}
			// Gauges (including CurrEstab) remain in the snapshots, but are
			// not labeled as counter deltas. Resets cannot safely be subtracted.
			if !ok || value < old || name == "CurrEstab" || name == "RtoAlgorithm" || name == "RtoMin" || name == "RtoMax" || name == "MaxConn" || name == "Forwarding" || name == "DefaultTTL" {
				continue
			}
			if result[group] == nil {
				result[group] = map[string]uint64{}
			}
			result[group][name] = value - old
		}
	}
	return result
}

func procBinding(value string) (string, error) {
	address, port, ok := strings.Cut(value, ":")
	if !ok || len(address) != 8 && len(address) != 32 {
		return "", fmt.Errorf("invalid proc listener address")
	}
	data := make([]byte, len(address)/2)
	for offset := 0; offset < len(address); offset += 8 {
		word, err := strconv.ParseUint(address[offset:offset+8], 16, 32)
		if err != nil {
			return "", fmt.Errorf("invalid proc listener address")
		}
		binary.NativeEndian.PutUint32(data[offset/2:], uint32(word))
	}
	ip, _ := netip.AddrFromSlice(data)
	n, err := strconv.ParseUint(port, 16, 16)
	if err != nil {
		return "", fmt.Errorf("invalid proc listener port")
	}
	return netip.AddrPortFrom(ip, uint16(n)).String(), nil
}

func ip6String(ip [16]byte) string { return netip.AddrFrom16(ip).String() }

// lookupRoute asks the kernel to resolve this destination and optional source
// IP/port rather than guessing from a table dump. A zero sourcePort is a
// preliminary query, not the connected tuple. It is read-only.
func lookupRoute(target Target, sourcePort int, deadline time.Time) (map[string]any, error) {
	ip, err := literalIP(target.Address)
	if err != nil {
		return nil, err
	}
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, unix.NETLINK_ROUTE)
	if err != nil {
		return nil, err
	}
	defer capture.CloseWithLog("route socket", func() error { return unix.Close(fd) })
	if err := unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return nil, err
	}
	message := make([]byte, unix.NLMSG_HDRLEN+unix.SizeofRtMsg)
	order := binary.NativeEndian
	order.PutUint16(message[4:], unix.RTM_GETROUTE)
	order.PutUint16(message[6:], unix.NLM_F_REQUEST)
	order.PutUint32(message[8:], 1)
	message[unix.NLMSG_HDRLEN] = unix.AF_INET6
	message[unix.NLMSG_HDRLEN+1] = 128
	if ip.Is4() {
		message[unix.NLMSG_HDRLEN] = unix.AF_INET
		message[unix.NLMSG_HDRLEN+1] = 32
	}
	appendAttr := func(kind uint16, data []byte) {
		n := len(data) + 4
		attr := make([]byte, (n+3)&^3)
		order.PutUint16(attr, uint16(n))
		order.PutUint16(attr[2:], kind)
		copy(attr[4:], data)
		message = append(message, attr...)
	}
	appendAttr(unix.RTA_DST, ip.AsSlice())
	if target.SourceIP != "" {
		source, err := literalIP(target.SourceIP)
		if err != nil {
			return nil, err
		}
		message[unix.NLMSG_HDRLEN+2] = message[unix.NLMSG_HDRLEN+1]
		appendAttr(unix.RTA_SRC, source.AsSlice())
	}
	appendAttr(unix.RTA_IP_PROTO, []byte{unix.IPPROTO_TCP})
	if sourcePort != 0 {
		appendAttr(unix.RTA_SPORT, binary.BigEndian.AppendUint16(nil, uint16(sourcePort)))
	}
	appendAttr(unix.RTA_DPORT, binary.BigEndian.AppendUint16(nil, uint16(target.Port)))
	order.PutUint32(message, uint32(len(message)))
	if err := unix.Sendto(fd, message, 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return nil, err
	}
	buffer := make([]byte, 8192)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, os.ErrDeadlineExceeded
		}
		poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		n, err := unix.Poll(poll, int((remaining+time.Millisecond-1)/time.Millisecond))
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, os.ErrDeadlineExceeded
		}
		n, _, flags, from, err := unix.Recvmsg(fd, buffer, nil, 0)
		if errors.Is(err, unix.EINTR) || errors.Is(err, unix.EAGAIN) {
			continue
		}
		if err != nil {
			return nil, err
		}
		peer, ok := from.(*unix.SockaddrNetlink)
		if !ok || peer.Pid != 0 || flags&unix.MSG_TRUNC != 0 {
			return nil, fmt.Errorf("invalid route lookup datagram")
		}
		messages, err := syscall.ParseNetlinkMessage(buffer[:n])
		if err != nil {
			return nil, err
		}
		for _, response := range messages {
			if response.Header.Seq != 1 {
				return nil, fmt.Errorf("mismatched route lookup sequence")
			}
			if response.Header.Type == unix.NLMSG_ERROR && len(response.Data) >= 4 {
				return nil, fmt.Errorf("route lookup: %w", unix.Errno(-int32(order.Uint32(response.Data))))
			}
			if response.Header.Type != unix.RTM_NEWROUTE {
				return nil, fmt.Errorf("unexpected route lookup response")
			}
			return rtnl.Decode(unix.RTM_GETROUTE, unix.SizeofRtMsg, response.Data)
		}
	}
}
