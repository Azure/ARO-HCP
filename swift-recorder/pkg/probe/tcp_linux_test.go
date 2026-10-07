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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestValidateRequestTCPOnly(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Request)
	}{
		{"wrong role", func(r *Request) { r.Targets[0].Role = "router" }},
		{"missing role", func(r *Request) { r.Targets[0].Role = "" }},
		{"wrong port", func(r *Request) { r.Targets[0].Port = 443 }},
		{"missing source", func(r *Request) { r.Targets[0].SourceIP = "" }},
		{"undiscovered source", func(r *Request) { r.SwiftIPs = nil }},
		{"wrong source family", func(r *Request) { r.Targets[0].SourceIP = "::1"; r.SwiftIPs = []string{"::1"} }},
		{"source hostname", func(r *Request) { r.Targets[0].SourceIP = "router.test" }},
		{"destination hostname", func(r *Request) { r.Targets[0].Address = "worker.test" }},
		{"plain HTTP", func(r *Request) { r.Targets[0].PlainHTTP = true }},
		{"SNI", func(r *Request) { r.Targets[0].ServerName = "worker.test" }},
		{"HTTP path", func(r *Request) { r.Targets[0].Path = "/healthz" }},
		{"trust bundle", func(r *Request) { r.Targets[0].TrustBundle = TrustRoot }},
		{"valid IPv4", nil},
		{"valid IPv6", func(r *Request) {
			r.Targets[0].Address = "::1"
			r.Targets[0].SourceIP = "::1"
			r.SwiftIPs = []string{"::1"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := Request{SwiftIPs: []string{"127.0.0.2"}, Targets: []Target{{Role: "worker-outbound", Address: "127.0.0.1", SourceIP: "127.0.0.2", Port: 22, TCPOnly: true}}}
			if tc.mutate != nil {
				tc.mutate(&req)
			}
			if err := validateRequest(req); (err == nil) != strings.HasPrefix(tc.name, "valid") {
				t.Fatalf("unexpected validation result: %v", err)
			}
		})
	}
}

func TestTCPOutcome(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"connected", nil, "connected"},
		{"refused", unix.ECONNREFUSED, "refused"},
		{"wrapped refused", &net.OpError{Op: "dial", Err: os.NewSyscallError("connect", unix.ECONNREFUSED)}, "refused"},
		{"poll timeout", os.ErrDeadlineExceeded, "timeout"},
		{"context timeout", context.DeadlineExceeded, "timeout"},
		{"kernel timeout", unix.ETIMEDOUT, "timeout"},
		{"wrapped timeout", &socketError{error: unix.ETIMEDOUT}, "timeout"},
		{"unavailable source", fmt.Errorf("bind: %w", unix.EADDRNOTAVAIL), "bindRoutingError"},
		{"source in use", unix.EADDRINUSE, "bindRoutingError"},
		{"unreachable network", unix.ENETUNREACH, "bindRoutingError"},
		{"unreachable host", unix.EHOSTUNREACH, "bindRoutingError"},
		{"network down", unix.ENETDOWN, "bindRoutingError"},
		{"host down", unix.EHOSTDOWN, "bindRoutingError"},
		{"prohibited route", unix.EACCES, "bindRoutingError"},
		{"denied", unix.EPERM, "bindRoutingError"},
		{"other errno", unix.EMFILE, "otherError"},
		{"not an errno", errors.New("connection refused"), "otherError"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tcpOutcome(tc.err); got != tc.want {
				t.Fatalf("tcpOutcome(%v)=%q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

func TestProbeTargetTCPOnly(t *testing.T) {
	t.Run("connected without application data or banner wait", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		done := make(chan error, 1)
		go func() {
			conn, err := listener.Accept()
			if err != nil {
				done <- err
				return
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
			var data [1]byte
			n, err := conn.Read(data[:])
			if n != 0 || err != io.EOF {
				done <- fmt.Errorf("expected raw close without bytes, got n=%d err=%v", n, err)
				return
			}
			done <- nil
		}()
		// The engine is exercised on an ephemeral port; request validation
		// separately enforces port 22 before any production socket is opened.
		target := targetFor(t, listener.Addr().String(), false)
		target.TCPOnly, target.Role, target.SourceIP = true, "worker-outbound", "127.0.0.2"
		target.ServerName, target.Path, target.TrustBundle = "", "", ""
		result := probeTarget(target, nil, time.Second)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if result.TCPOutcome != "connected" || result.Stage != "complete" || result.Error != "" || !strings.HasPrefix(result.Source, "127.0.0.2:") || result.Destination != listener.Addr().String() {
			t.Fatalf("unexpected connection evidence: %+v", result)
		}
		if result.TLS || result.TLSVerify || result.ExpectedStatus != 0 || result.Expected200 || result.HTTPStatus != 0 || result.BodyBytesDiscarded != 0 || result.BodyTruncated {
			t.Fatalf("TCP-only result claims HTTP/TLS evidence: %+v", result)
		}
		if result.PreliminaryRoute == nil || result.Route == nil || result.Timings["connect"] <= 0 || len(result.Timings) != 3 {
			t.Fatalf("missing routing/timing evidence or extra protocol stages: %+v", result)
		}
		data, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		var decoded Observation
		if err := json.Unmarshal(data, &decoded); err != nil || !decoded.Target.TCPOnly || decoded.TCPOutcome != "connected" || !strings.Contains(string(data), `"tcpOnly":true`) {
			t.Fatalf("TCP-only API round trip failed: %s (%v)", data, err)
		}
	})
	t.Run("refused retains rejection and tuple", func(t *testing.T) {
		// Bind without listening to reserve a port that deterministically rejects
		// connections, without a close/rebind race with another test process.
		fd, err := unix.Socket(unix.AF_INET, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer unix.Close(fd)
		if err := unix.Bind(fd, &unix.SockaddrInet4{Addr: [4]byte{127, 0, 0, 1}}); err != nil {
			t.Fatal(err)
		}
		addr, err := unix.Getsockname(fd)
		if err != nil {
			t.Fatal(err)
		}
		target := Target{Role: "worker-outbound", Address: "127.0.0.1", SourceIP: "127.0.0.2", Port: addr.(*unix.SockaddrInet4).Port, TCPOnly: true}
		conn, err := dialSocket(target.Address, target.SourceIP, target.Port, false, time.Now().Add(time.Second))
		if conn != nil || !errors.Is(err, unix.ECONNREFUSED) {
			t.Fatalf("expected refused errno, got conn=%v err=%v", conn, err)
		}
		result := probeTarget(target, nil, time.Second)
		if result.TCPOutcome != "refused" || result.Error == "" || result.Stage != "connect" || result.Expected200 || result.TLS || result.ExpectedStatus != 0 {
			t.Fatalf("rejection treated as worker health: %+v", result)
		}
		if !strings.HasPrefix(result.Source, "127.0.0.2:") || result.Destination != net.JoinHostPort(target.Address, fmt.Sprint(target.Port)) || result.PreliminaryRoute == nil || result.Route != nil {
			t.Fatalf("missing failed connection evidence: %+v", result)
		}
	})
	t.Run("unavailable source does not fall back", func(t *testing.T) {
		result := probeTarget(Target{Role: "worker-outbound", Address: "127.0.0.1", SourceIP: "192.0.2.231", Port: 22, TCPOnly: true}, nil, time.Second)
		if result.TCPOutcome != "bindRoutingError" || result.Stage != "connect" || !strings.Contains(result.Error, "bind:") || result.Source != "" || result.Destination != "127.0.0.1:22" || result.Target.SourceIP != "192.0.2.231" || result.TLS || result.ExpectedStatus != 0 {
			t.Fatalf("unavailable source incorrectly reported: %+v", result)
		}
	})
}

// Called only in the isolated namespace: fill a port-22 accept queue so the
// next SYN is dropped, without relying on external routes or firewall tools.
func checkTCPOnlyTimeout() error {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	if err := unix.Bind(fd, &unix.SockaddrInet4{Addr: [4]byte{127, 0, 0, 3}, Port: 22}); err != nil {
		return err
	}
	if err := unix.Listen(fd, 0); err != nil {
		return err
	}
	conn, err := dialSocket("127.0.0.3", "127.0.0.2", 22, false, time.Now().Add(time.Second))
	if err != nil {
		return err
	}
	defer conn.Close()
	target := Target{Role: "worker-outbound", Address: "127.0.0.3", SourceIP: "127.0.0.2", Port: 22, TCPOnly: true}
	if err := validateRequest(Request{Targets: []Target{target}, SwiftIPs: []string{"127.0.0.2"}}); err != nil {
		return err
	}
	result := probeTarget(target, nil, 100*time.Millisecond)
	if result.TCPOutcome != "timeout" || result.Stage != "connect" || result.Error == "" || result.DurationMS < 90 || result.DurationMS > 1000 || !strings.HasPrefix(result.Source, "127.0.0.2:") || result.Destination != "127.0.0.3:22" || result.PreliminaryRoute == nil || result.Route != nil || result.TLS || result.ExpectedStatus != 0 {
		return fmt.Errorf("TCP-only timeout evidence: %+v", result)
	}
	return nil
}
