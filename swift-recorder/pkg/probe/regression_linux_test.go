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
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

type deadlineRecordingConn struct {
	net.Conn
	writes atomic.Int32
}

func (c *deadlineRecordingConn) SetWriteDeadline(deadline time.Time) error {
	c.writes.Add(1)
	return c.Conn.SetWriteDeadline(deadline)
}

func TestTLSBodyCleanupClosesRawSocket(t *testing.T) {
	ca := newTestCA(t)
	certificates := []tls.Certificate{ca.issue(t, "router.test", false)}
	roots := loadTrustBundles(map[string]string{TrustIgnition: ca.pem})[TrustIgnition]
	clientRaw, serverRaw := net.Pipe()
	defer clientRaw.Close()
	defer serverRaw.Close()
	raw := &deadlineRecordingConn{Conn: clientRaw}
	client := tls.Client(raw, &tls.Config{ServerName: "router.test", RootCAs: roots})
	server := tls.Server(serverRaw, &tls.Config{Certificates: certificates})
	release := make(chan struct{})
	defer close(release)
	done := make(chan error, 1)
	go func() {
		_, err := io.WriteString(server, "HTTP/1.1 200 OK\r\nContent-Length: 4\r\n\r\nbody")
		done <- err
		// Never read close_notify: tls.Conn.Close would extend the deadline
		// and block for five seconds on this unbuffered connection.
		<-release
	}()
	if err := raw.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(client), &http.Request{Method: "GET"})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	n, err := discardBody(raw, response.Body)
	if err != nil || n != 4 || raw.writes.Load() != 0 || time.Since(start) > 200*time.Millisecond {
		t.Fatalf("cleanup extended deadline or lost body: n=%d err=%v deadlineWrites=%d elapsed=%s", n, err, raw.writes.Load(), time.Since(start))
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestChunkFramingWireBudget(t *testing.T) {
	for _, tc := range []struct {
		name, body, reason string
		wantError          bool
	}{
		{"small chunks", strings.Repeat("1\r\nx\r\n", maxBodyBytes) + "0\r\n\r\n", "wireLimit", false},
		{"short remote body", "1\r\nx\r\n", "", true},
		{"complete", "1\r\nx\r\n0\r\n\r\n", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			address, stop := rawHTTPServer(t, func(conn net.Conn) {
				_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n"+tc.body)
			})
			result := probeTarget(targetFor(t, address, true), nil, time.Second)
			stop()
			if (result.Error != "") != tc.wantError || result.BodyTruncated != (tc.reason != "") || result.BodyTruncationReason != tc.reason {
				t.Fatalf("wire budget misclassified: %+v", result)
			}
			if tc.reason != "" && (result.BodyBytesDiscarded >= maxBodyBytes || result.Stage != "complete") {
				t.Fatalf("expected framing budget before decoded byte limit: %+v", result)
			}
		})
	}
}

func TestResponseBodyLimitBoundary(t *testing.T) {
	for _, framing := range []string{"content-length", "chunked", "close-delimited"} {
		for _, size := range []int{maxBodyBytes - 1, maxBodyBytes, maxBodyBytes + 1} {
			t.Run(fmt.Sprintf("%s/%d", framing, size), func(t *testing.T) {
				address, stop := rawHTTPServer(t, func(conn net.Conn) {
					body := strings.Repeat("x", size)
					switch framing {
					case "content-length":
						_, _ = fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\n\r\n%s", size, body)
					case "chunked":
						_, _ = fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n%x\r\n%s\r\n0\r\n\r\n", size, body)
					case "close-delimited":
						_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nConnection: close\r\n\r\n"+body)
					}
				})
				result := probeTarget(targetFor(t, address, true), nil, time.Second)
				stop()
				reason := ""
				if size > maxBodyBytes {
					reason = "decodedBodyLimit"
				}
				if result.Error != "" || result.Stage != "complete" || result.HTTPStatus != http.StatusOK || result.BodyTruncated != (size > maxBodyBytes) || result.BodyTruncationReason != reason || result.BodyBytesDiscarded != int64(min(size, maxBodyBytes)) {
					t.Fatalf("incorrect body boundary result: %+v", result)
				}
			})
		}
	}
}

func TestSparseCounterDeltas(t *testing.T) {
	before := Evidence{Counters: parseCounters("IcmpMsg: InType3\nIcmpMsg: 2\nIcmpMsg: OutType3\nIcmpMsg: 4\n"), CounterReads: map[string]bool{"snmp": true}}
	after := Evidence{Counters: parseCounters("IcmpMsg: InType3 InType8\nIcmpMsg: 5 7\nIcmpMsg: OutType3 OutType8\nIcmpMsg: 6 9\n")}
	if len(before.Counters["IcmpMsg"]) != 2 || len(after.Counters["IcmpMsg"]) != 4 {
		t.Fatal("repeated IcmpMsg sections overwritten")
	}
	delta := counterDeltas(before, after)["IcmpMsg"]
	if delta["InType3"] != 3 || delta["OutType3"] != 2 || delta["InType8"] != 7 || delta["OutType8"] != 9 {
		t.Fatalf("incorrect sparse deltas: %+v", delta)
	}
	for _, complete := range []bool{true, false} {
		before := Evidence{CounterReads: map[string]bool{"snmp": complete}}
		delta := counterDeltas(before, after)["IcmpMsg"]
		if complete && len(delta) != 4 || !complete && len(delta) != 0 {
			t.Fatalf("complete=%v deltas=%+v", complete, delta)
		}
	}
	before.CounterReads["snmp"] = false
	if delta := counterDeltas(before, after)["IcmpMsg"]; len(delta) != 2 {
		t.Fatalf("partial before read invented zeroes: %+v", delta)
	}
}

// Called only inside the opt-in private user/net namespace. A source-port
// policy must distinguish the preliminary query from the connected tuple.
func checkSourcePortPolicy() error {
	lo, err := net.InterfaceByName("lo")
	if err != nil {
		return err
	}
	request := func(kind uint16, body []byte, attrs map[uint16][]byte) error {
		fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_ROUTE)
		if err != nil {
			return err
		}
		defer unix.Close(fd)
		timeout := unix.NsecToTimeval(time.Second.Nanoseconds())
		if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &timeout); err != nil {
			return err
		}
		message := make([]byte, unix.NLMSG_HDRLEN)
		message = append(message, body...)
		for kind, value := range attrs {
			attr := make([]byte, (len(value)+7)&^3)
			binary.NativeEndian.PutUint16(attr, uint16(len(value)+4))
			binary.NativeEndian.PutUint16(attr[2:], kind)
			copy(attr[4:], value)
			message = append(message, attr...)
		}
		binary.NativeEndian.PutUint32(message, uint32(len(message)))
		binary.NativeEndian.PutUint16(message[4:], kind)
		binary.NativeEndian.PutUint16(message[6:], unix.NLM_F_REQUEST|unix.NLM_F_ACK|unix.NLM_F_CREATE|unix.NLM_F_EXCL)
		if err := unix.Sendto(fd, message, 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
			return err
		}
		buffer := make([]byte, 4096)
		n, _, err := unix.Recvfrom(fd, buffer, 0)
		if err != nil {
			return err
		}
		messages, err := syscall.ParseNetlinkMessage(buffer[:n])
		if err != nil {
			return err
		}
		for _, response := range messages {
			if response.Header.Type == unix.NLMSG_ERROR && len(response.Data) >= 4 {
				code := int32(binary.NativeEndian.Uint32(response.Data))
				if code != 0 {
					return unix.Errno(-code)
				}
				return nil
			}
		}
		return fmt.Errorf("missing policy setup acknowledgement")
	}
	route := make([]byte, unix.SizeofRtMsg)
	route[0], route[1], route[4], route[5], route[6], route[7] = unix.AF_INET, 32, unix.RT_TABLE_MAIN, unix.RTPROT_STATIC, unix.RT_SCOPE_LINK, unix.RTN_UNICAST
	if err := request(unix.RTM_NEWROUTE, route, map[uint16][]byte{unix.RTA_DST: {192, 0, 2, 1}, unix.RTA_OIF: binary.NativeEndian.AppendUint32(nil, uint32(lo.Index))}); err != nil {
		return err
	}
	rule := make([]byte, unix.SizeofRtMsg)
	rule[0], rule[1], rule[2], rule[7] = unix.AF_INET, 32, 32, unix.FR_ACT_PROHIBIT
	ports := binary.NativeEndian.AppendUint16(nil, 12345)
	ports = binary.NativeEndian.AppendUint16(ports, 12345)
	if err := request(unix.RTM_NEWRULE, rule, map[uint16][]byte{unix.FRA_PRIORITY: binary.NativeEndian.AppendUint32(nil, 100), unix.FRA_DST: {192, 0, 2, 1}, unix.FRA_SRC: {127, 0, 0, 2}, unix.FRA_IP_PROTO: {unix.IPPROTO_TCP}, unix.FRA_SPORT_RANGE: ports}); err != nil {
		return err
	}
	target := Target{Address: "192.0.2.1", SourceIP: "127.0.0.2", Port: 443}
	if _, err := lookupRoute(target, 0, time.Now().Add(time.Second)); err != nil {
		return fmt.Errorf("preliminary route: %w", err)
	}
	if _, err := lookupRoute(target, 12345, time.Now().Add(time.Second)); !errors.Is(err, unix.EACCES) {
		return fmt.Errorf("source-port policy not selected: %v", err)
	}
	target.SourceIP = "127.0.0.1"
	if _, err := lookupRoute(target, 12345, time.Now().Add(time.Second)); err != nil {
		return fmt.Errorf("source-IP policy not respected: %w", err)
	}
	return nil
}
