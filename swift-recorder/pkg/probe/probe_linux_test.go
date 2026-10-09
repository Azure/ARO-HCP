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
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/sys/unix"

	"github.com/Azure/ARO-HCP/swift-recorder/pkg/capture"
	"github.com/Azure/ARO-HCP/swift-recorder/pkg/rtnl"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv("SWIFT_PROBE_HELPER"); mode != "" {
		if len(os.Args) != 2 || os.Args[1] != "router-probe" {
			os.Exit(2)
		}
		var req Request
		if err := json.NewDecoder(io.LimitReader(os.Stdin, MaxRequestBytes)).Decode(&req); err != nil {
			os.Exit(3)
		}
		switch mode {
		case "json":
			_ = json.NewEncoder(os.Stdout).Encode(req)
		case "invalid":
			fmt.Fprint(os.Stdout, "not JSON")
		case "fail":
			fmt.Fprint(os.Stderr, "sensitive error not included")
			os.Exit(4)
		case "wait":
			time.Sleep(time.Minute)
		case "stdout", "stderr":
			out := os.Stdout
			if mode == "stderr" {
				out = os.Stderr
			}
			for {
				if _, err := io.WriteString(out, strings.Repeat("x", 4096)); err != nil {
					os.Exit(5)
				}
			}
		case "isolated":
			if err := runIsolated(req.NamespacePath); err != nil {
				fmt.Fprint(os.Stderr, err)
				os.Exit(6)
			}
		case "run":
			if err := Run(req, os.Stdout); err != nil {
				fmt.Fprint(os.Stderr, err)
				os.Exit(7)
			}
		default:
			os.Exit(8)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestExecute(t *testing.T) {
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ mode, want string }{
		{"json", ""}, {"invalid", "invalid JSON"}, {"fail", "probe helper:"}, {"stdout", "exceeded"}, {"stderr", "exceeded"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			t.Setenv("SWIFT_PROBE_HELPER", tc.mode)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			req := Request{NamespacePath: "/test/cni-probe", NamespaceDevice: 4, NamespaceInode: 1<<33 + 17, DNSNames: []string{"service.namespace.svc.cluster.local"}}
			data, err := Execute(ctx, executable, req)
			if tc.want != "" {
				if data != nil || err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "sensitive") {
					t.Fatalf("data=%s error=%v; expected %q", data, err, tc.want)
				}
				return
			}
			var decoded Request
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatal(err)
			}
			if decoded.NamespaceInode != req.NamespaceInode || decoded.NamespacePath != req.NamespacePath {
				t.Fatalf("request lost fields: %s", data)
			}
		})
	}
	t.Run("cancellation", func(t *testing.T) {
		t.Setenv("SWIFT_PROBE_HELPER", "wait")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		timer := time.AfterFunc(50*time.Millisecond, cancel)
		defer timer.Stop()
		start := time.Now()
		if _, err := Execute(ctx, executable, Request{}); !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation, got %v", err)
		}
		if time.Since(start) > time.Second {
			t.Fatal("cancellation failed to promptly reap child")
		}
	})
	t.Run("deadline", func(t *testing.T) {
		t.Setenv("SWIFT_PROBE_HELPER", "wait")
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		if _, err := Execute(ctx, executable, Request{}); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected deadline, got %v", err)
		}
	})
	t.Run("oversized request", func(t *testing.T) {
		if _, err := Execute(context.Background(), "must-not-execute", Request{NamespacePath: strings.Repeat("x", MaxRequestBytes)}); err == nil || !strings.Contains(err.Error(), "request exceeded") {
			t.Fatalf("got %v", err)
		}
	})
}

func TestLimitedOutputAndResult(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	buf := limitedOutput{limit: 4, cancel: cancel}
	_, _ = buf.Write([]byte("abcd"))
	if ctx.Err() != nil || string(buf.data) != "abcd" {
		t.Fatal("exact limit rejected")
	}
	for range 5 {
		_, _ = buf.Write([]byte("excess"))
	}
	if !buf.exceeded || ctx.Err() == nil || string(buf.data) != "abcd" {
		t.Fatal("output unbounded")
	}
	var out bytes.Buffer
	result := Result{Before: Evidence{State: map[string]any{"oversized": strings.Repeat("x", MaxResultBytes)}}}
	if err := writeResult(&out, result); err != nil {
		t.Fatal(err)
	}
	if out.Len() > MaxResultBytes || !strings.Contains(out.String(), "supplementary evidence omitted") {
		t.Fatalf("missing explicit output truncation: %d bytes", out.Len())
	}
}

func TestRequestValidation(t *testing.T) {
	valid := func() Request {
		return Request{Targets: []Target{{ID: "api", Address: "127.0.0.1", Port: 443, ServerName: "api.test", Path: "/healthz"}}, DNS: DNSConfig{Servers: []string{"127.0.0.53"}}, DNSNames: []string{"api.test"}}
	}
	for _, tc := range []struct {
		name   string
		mutate func(*Request)
	}{
		{"hostname address", func(r *Request) { r.Targets[0].Address = "api.test" }},
		{"scoped address", func(r *Request) { r.Targets[0].Address = "fe80::1%eth0" }},
		{"invalid port", func(r *Request) { r.Targets[0].Port = 0 }},
		{"non Swift source", func(r *Request) { r.Targets[0].SourceIP = "127.0.0.2" }},
		{"family mismatch", func(r *Request) { r.Targets[0].SourceIP = "::1"; r.SwiftIPs = []string{"::1"} }},
		{"external plain HTTP", func(r *Request) {
			r.Targets[0].PlainHTTP = true
			r.Targets[0].Address = "192.0.2.1"
			r.Targets[0].Port = 9444
		}},
		{"wrong plain port", func(r *Request) { r.Targets[0].PlainHTTP = true }},
		{"SNI injection", func(r *Request) { r.Targets[0].ServerName = "api.test\r\nAuthorization: secret" }},
		{"query credentials", func(r *Request) { r.Targets[0].Path = "/?token=secret" }},
		{"absolute URL", func(r *Request) { r.Targets[0].Path = "http://other.test/" }},
		{"network path", func(r *Request) { r.Targets[0].Path = "//other.test/" }},
		{"resolver hostname", func(r *Request) { r.DNS.Servers = []string{"dns.test"} }},
		{"DNS label", func(r *Request) { r.DNSNames = []string{"a..test"} }},
		{"DNS arbitrary address", func(r *Request) { r.DNSNames = []string{"192.0.2.1"} }},
		{"oversized target metadata", func(r *Request) { r.Targets[0].ID = strings.Repeat("x", 257) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := valid()
			tc.mutate(&req)
			if err := validateRequest(req); err == nil {
				t.Fatal("accepted invalid request")
			}
		})
	}
	for _, req := range []Request{valid(), {Targets: []Target{{Address: "::1", Port: 9444, PlainHTTP: true}}}, {Targets: []Target{{Address: "127.0.0.1", SourceIP: "127.0.0.2", Port: 443}}, SwiftIPs: []string{"127.0.0.2"}}} {
		if err := validateRequest(req); err != nil {
			t.Fatalf("rejected valid request: %v", err)
		}
	}
}

func targetFor(t *testing.T, address string, plain bool) Target {
	t.Helper()
	host, portString, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portString)
	if err != nil {
		t.Fatal(err)
	}
	return Target{Address: host, Port: port, PlainHTTP: plain, ServerName: "router.test", TrustBundle: TrustIgnition, Path: "/healthz"}
}

func rawHTTPServer(t *testing.T, respond func(net.Conn)) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.SetDeadline(time.Now().Add(time.Second))
			_, _ = http.ReadRequest(bufio.NewReader(conn))
			respond(conn)
			_ = conn.Close()
		}
	}()
	stop := func() {
		_ = listener.Close()
		<-done
	}
	t.Cleanup(stop)
	return listener.Addr().String(), stop
}

func TestHTTPAndTLS(t *testing.T) {
	ca := newTestCA(t)
	roots := loadTrustBundles(map[string]string{TrustIgnition: ca.pem})[TrustIgnition]
	var hits atomic.Int32
	var wrong atomic.Bool
	server := newTLSServer(t, &tls.Config{Certificates: []tls.Certificate{ca.issue(t, "router.test", false)}}, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Host != "router.test" || r.URL.Path != "/healthz" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.TLS.ServerName != "router.test" {
			wrong.Store(true)
		}
		w.Header().Set("Set-Cookie", "private-cookie")
		fmt.Fprint(w, "private-response-body")
	})
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	target := targetFor(t, server.Listener.Addr().String(), false)
	target.SourceIP = "127.0.0.2"
	previousSource := ""
	for range 2 {
		result := probeTarget(target, roots, time.Second)
		if result.Error != "" || result.Stage != "complete" || !result.Expected200 || result.HTTPStatus != 200 || !result.TLS || !result.TLSVerify || !result.TLSVerified || !strings.HasPrefix(result.Source, "127.0.0.2:") || result.Destination != server.Listener.Addr().String() {
			t.Fatalf("unexpected TLS result: %+v", result)
		}
		if result.TCPOutcome != "" || result.ExpectedStatus != http.StatusOK {
			t.Fatalf("TCP-only changes affected TLS observations: %+v", result)
		}
		if result.Timings["tls"] <= 0 || result.Timings["connect"] <= 0 || result.DurationMS <= 0 || result.BodyBytesDiscarded != int64(len("private-response-body")) {
			t.Fatalf("missing stage evidence: %+v", result)
		}
		if result.Source == previousSource {
			t.Fatal("probe reused a connection")
		}
		previousSource = result.Source
		data, _ := json.Marshal(result)
		if bytes.Contains(data, []byte("private")) {
			t.Fatalf("response data escaped: %s", data)
		}
	}
	if hits.Load() != 2 || wrong.Load() {
		t.Fatalf("hits=%d, wrong request/SNI=%v", hits.Load(), wrong.Load())
	}
}

func TestHTTPBoundsNoRedirectRetry(t *testing.T) {
	for _, tc := range []struct {
		name      string
		response  string
		status    int
		wantErr   bool
		truncated bool
	}{
		{"redirect", "HTTP/1.1 302 Found\r\nLocation: http://127.0.0.1:1/\r\nContent-Length: 0\r\n\r\n", 302, true, false},
		{"unavailable", "HTTP/1.1 503 Unavailable\r\nContent-Length: 0\r\n\r\n", 503, true, false},
		{"oversized body", "HTTP/1.1 200 OK\r\nContent-Length: 10000000\r\n\r\n" + strings.Repeat("x", maxBodyBytes+1), 200, false, true},
		{"chunked body", "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n10000\r\n" + strings.Repeat("x", maxBodyBytes+1), 200, false, true},
		{"oversized headers", "HTTP/1.1 200 OK\r\nX-Secret: " + strings.Repeat("private", maxHeaderBytes) + "\r\n\r\n", 0, true, false},
		{"malformed status", "PRIVATE_RESPONSE\r\n\r\n", 0, true, false},
		{"closed", "", 0, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var accepts atomic.Int32
			address, stop := rawHTTPServer(t, func(conn net.Conn) {
				accepts.Add(1)
				_, _ = io.WriteString(conn, tc.response)
			})
			result := probeTarget(targetFor(t, address, true), nil, time.Second)
			stop()
			if result.HTTPStatus != tc.status || (result.Error != "") != tc.wantErr || result.BodyTruncated != tc.truncated || result.BodyBytesDiscarded > maxBodyBytes || accepts.Load() != 1 {
				t.Fatalf("result=%+v accepts=%d", result, accepts.Load())
			}
			data, _ := json.Marshal(result)
			if bytes.Contains(bytes.ToLower(data), []byte("private")) {
				t.Fatalf("server-controlled error leaked: %s", data)
			}
		})
	}
}

func TestTotalDeadline(t *testing.T) {
	ca := newTestCA(t)
	roots := loadTrustBundles(map[string]string{TrustIgnition: ca.pem})[TrustIgnition]
	for _, stage := range []string{"tls", "httpHeaders", "httpBody"} {
		t.Run(stage, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan struct{})
			go func() {
				defer close(done)
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				if stage == "httpBody" {
					_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 4\r\n\r\n")
				}
				_, _ = io.Copy(io.Discard, conn)
			}()
			start := time.Now()
			result := probeTarget(targetFor(t, listener.Addr().String(), stage != "tls"), roots, 100*time.Millisecond)
			<-done
			if !strings.Contains(result.Error, "deadline exceeded") || result.Stage != stage || time.Since(start) > time.Second {
				t.Fatalf("deadline failed: %+v", result)
			}
		})
	}
}

func TestDeadlineSharedAcrossStages(t *testing.T) {
	ca := newTestCA(t)
	roots := loadTrustBundles(map[string]string{TrustIgnition: ca.pem})[TrustIgnition]
	server := newTLSServer(t, &tls.Config{Certificates: []tls.Certificate{ca.issue(t, "router.test", false)}, GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
		time.Sleep(150 * time.Millisecond)
		return nil, nil
	}}, func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(150 * time.Millisecond)
		w.WriteHeader(200)
	})
	result := probeTarget(targetFor(t, server.Listener.Addr().String(), false), roots, 250*time.Millisecond)
	if result.Stage != "httpHeaders" || !strings.Contains(result.Error, "deadline exceeded") || result.DurationMS > 400 {
		t.Fatalf("each stage incorrectly got a new timeout: %+v", result)
	}
}

func TestSourceBindFailure(t *testing.T) {
	ca := newTestCA(t)
	roots := loadTrustBundles(map[string]string{TrustIgnition: ca.pem})[TrustIgnition]
	result := probeTarget(Target{Address: "127.0.0.1", Port: 443, SourceIP: "192.0.2.231", ServerName: "router.test", TrustBundle: TrustIgnition}, roots, time.Second)
	if result.Stage != "connect" || !strings.Contains(result.Error, "bind:") {
		t.Fatalf("unavailable source was silently ignored: %+v", result)
	}
}

func TestDNSExplicitTransports(t *testing.T) {
	for _, protocol := range []string{"udp", "tcp"} {
		for _, mode := range []string{"success", "mismatch", "truncated", "rcode", "oversized"} {
			t.Run(protocol+"/"+mode, func(t *testing.T) {
				var listener net.Listener
				var packet net.PacketConn
				var address string
				var err error
				if protocol == "tcp" {
					listener, err = net.Listen("tcp", "127.0.0.1:0")
					if err == nil {
						address = listener.Addr().String()
						defer listener.Close()
					}
				} else {
					packet, err = net.ListenPacket("udp", "127.0.0.1:0")
					if err == nil {
						address = packet.LocalAddr().String()
						defer packet.Close()
					}
				}
				if err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() {
					buffer := make([]byte, 4096)
					var request []byte
					var write func([]byte) error
					if protocol == "tcp" {
						conn, err := listener.Accept()
						if err != nil {
							done <- err
							return
						}
						defer conn.Close()
						_ = conn.SetDeadline(time.Now().Add(time.Second))
						if _, err := io.ReadFull(conn, buffer[:2]); err != nil {
							done <- err
							return
						}
						n := int(binary.BigEndian.Uint16(buffer[:2]))
						if _, err := io.ReadFull(conn, buffer[:n]); err != nil {
							done <- err
							return
						}
						request = buffer[:n]
						write = func(data []byte) error {
							_, err := conn.Write(append(binary.BigEndian.AppendUint16(nil, uint16(len(data))), data...))
							return err
						}
					} else {
						_ = packet.SetDeadline(time.Now().Add(time.Second))
						n, peer, err := packet.ReadFrom(buffer)
						if err != nil {
							done <- err
							return
						}
						request = buffer[:n]
						write = func(data []byte) error { _, err := packet.WriteTo(data, peer); return err }
					}
					var query dnsmessage.Message
					if err := query.Unpack(request); err != nil {
						done <- err
						return
					}
					if len(query.Questions) != 1 || query.Questions[0].Name.String() != "api.test." {
						done <- fmt.Errorf("wrong DNS question")
						return
					}
					response := dnsmessage.Message{Header: dnsmessage.Header{ID: query.ID, Response: true}, Questions: query.Questions, Answers: []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: query.Questions[0].Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 1}, Body: &dnsmessage.AResource{A: [4]byte{192, 0, 2, 1}}}}}
					// Mixed-family answers must not contaminate this A observation.
					response.Answers = append(response.Answers, dnsmessage.Resource{Header: dnsmessage.ResourceHeader{Name: query.Questions[0].Name, Type: dnsmessage.TypeAAAA, Class: dnsmessage.ClassINET}, Body: &dnsmessage.AAAAResource{AAAA: [16]byte{15: 1}}})
					switch mode {
					case "mismatch":
						response.ID++
					case "truncated":
						response.Truncated = true
					case "rcode":
						response.RCode = dnsmessage.RCodeNameError
					}
					data, err := response.Pack()
					if err != nil {
						done <- err
						return
					}
					if mode == "oversized" {
						data = make([]byte, 4097)
					}
					done <- write(data)
				}()
				target := targetFor(t, address, false)
				result := probeDNSPort(target.Address, target.Port, "api.test", protocol, dnsmessage.TypeA, time.Second)
				if err := <-done; err != nil {
					t.Fatal(err)
				}
				if (result.Error == "") != (mode == "success") || result.Source == "" || result.Destination != address || result.DurationMS <= 0 {
					t.Fatalf("unexpected DNS result: %+v", result)
				}
				if mode == "success" && (len(result.Answers) != 1 || result.Answers[0] != "192.0.2.1") {
					t.Fatalf("wrong DNS answer: %+v", result)
				}
				if (mode == "truncated" || mode == "oversized") && !result.Truncated {
					t.Fatalf("missing truncation: %+v", result)
				}
			})
		}
	}
}

func TestEvidenceAndRoute(t *testing.T) {
	evidence := collectEvidence()
	for _, name := range []string{"links", "addresses", "routes", "rules", "neighbors"} {
		if _, ok := evidence.State[name].(rtnl.Section); !ok {
			t.Fatalf("missing %s: %+v", name, evidence.Errors)
		}
	}
	for _, name := range []string{"all", "default", "lo"} {
		if evidence.RPFilter[name] == "" {
			t.Fatalf("missing rp_filter %s: %+v", name, evidence.Errors)
		}
	}
	if len(evidence.Counters["Tcp"]) == 0 || len(evidence.Counters["link/lo"]) == 0 {
		t.Fatalf("missing counters: %+v", evidence)
	}
	route, err := lookupRoute(Target{Address: "127.0.0.1", Port: 443}, 0, time.Now().Add(time.Second))
	if err != nil || route["destination"] != "127.0.0.1" {
		t.Fatalf("route=%+v err=%v", route, err)
	}
	for value, want := range map[string]string{"0100007F:24E4": "127.0.0.1:9444", "00000000000000000000000001000000:01BB": "[::1]:443"} {
		if got, err := procBinding(value); err != nil || got != want {
			t.Fatalf("procBinding(%q)=%q,%v want %q", value, got, err, want)
		}
	}
	before := Evidence{Counters: parseCounters("Tcp: ActiveOpens CurrEstab MaxConn\nTcp: 2 4 -1\n")}
	after := Evidence{Counters: parseCounters("Tcp: ActiveOpens CurrEstab MaxConn\nTcp: 5 5 -1\n")}
	delta := counterDeltas(before, after)
	if delta["Tcp"]["ActiveOpens"] != 3 || len(delta["Tcp"]) != 1 {
		t.Fatalf("incorrect deltas: %+v", delta)
	}
	if len(counterDeltas(after, before)) != 0 {
		t.Fatal("counter reset underflow")
	}
}

func TestDNSAAAAAndNoRetries(t *testing.T) {
	for _, respond := range []bool{true, false} {
		t.Run(fmt.Sprint(respond), func(t *testing.T) {
			packet, err := net.ListenPacket("udp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer packet.Close()
			var requests atomic.Int32
			done := make(chan struct{})
			go func() {
				defer close(done)
				for {
					buffer := make([]byte, 4096)
					n, peer, err := packet.ReadFrom(buffer)
					if err != nil {
						return
					}
					requests.Add(1)
					if !respond {
						continue
					}
					var query dnsmessage.Message
					if err := query.Unpack(buffer[:n]); err != nil || len(query.Questions) != 1 {
						return
					}
					query.Response = true
					query.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: query.Questions[0].Name, Type: dnsmessage.TypeAAAA, Class: dnsmessage.ClassINET}, Body: &dnsmessage.AAAAResource{AAAA: [16]byte{15: 1}}}}
					query.Answers = append(query.Answers, dnsmessage.Resource{Header: dnsmessage.ResourceHeader{Name: query.Questions[0].Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}, Body: &dnsmessage.AResource{A: [4]byte{192, 0, 2, 1}}})
					data, err := query.Pack()
					if err != nil {
						return
					}
					_, _ = packet.WriteTo(data, peer)
				}
			}()
			target := targetFor(t, packet.LocalAddr().String(), false)
			result := probeDNSPort(target.Address, target.Port, "api.test.", "udp", dnsmessage.TypeAAAA, 100*time.Millisecond)
			_ = packet.Close()
			<-done
			if requests.Load() != 1 {
				t.Fatalf("query retried %d times", requests.Load())
			}
			if respond {
				if result.Error != "" || len(result.Answers) != 1 || result.Answers[0] != "::1" {
					t.Fatalf("AAAA failed: %+v", result)
				}
			} else if !strings.Contains(result.Error, "deadline exceeded") || result.Stage != "response" {
				t.Fatalf("DNS timeout failed: %+v", result)
			}
		})
	}
}

func TestIPv6Socket(t *testing.T) {
	listener, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback unavailable: %v", err)
	}
	defer listener.Close()
	server := http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }), ReadHeaderTimeout: time.Second}
	defer server.Close()
	go func() { _ = server.Serve(listener) }()
	target := targetFor(t, listener.Addr().String(), true)
	target.SourceIP = "::1"
	result := probeTarget(target, nil, time.Second)
	if result.Error != "" || !strings.HasPrefix(result.Source, "[::1]:") || result.Destination != listener.Addr().String() {
		t.Fatalf("IPv6 probe: %+v", result)
	}
}

func TestNamespaceIdentity(t *testing.T) {
	fd, err := unix.Open("/proc/thread-self/ns/net", unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		t.Fatal(err)
	}
	req := Request{NamespaceDevice: uint64(stat.Dev), NamespaceInode: stat.Ino}
	if err := validateIdentity(fd, req); err != nil {
		t.Fatal(err)
	}
	req.NamespaceInode++
	if err := validateIdentity(fd, req); err == nil {
		t.Fatal("accepted stale inode")
	}
	req.NamespaceInode = stat.Ino
	req.NamespaceDevice++
	if err := validateIdentity(fd, req); err == nil {
		t.Fatal("accepted wrong device")
	}
	var out bytes.Buffer
	if err := Run(Request{NamespacePath: "/proc/self/ns/net"}, &out); err == nil || out.Len() != 0 {
		t.Fatalf("unsafe namespace path accepted: %v", err)
	}
}

// This opt-in test needs unprivileged user namespaces, not host capabilities.
// Mounts and loopback setup are confined to private user/mount/net namespaces.
func TestRunIsolatedNamespaceOptIn(t *testing.T) {
	if os.Getenv("SWIFT_RECORDER_TEST_ISOLATED") != "1" {
		t.Skip("set SWIFT_RECORDER_TEST_ISOLATED=1 for rootless network namespace isolation tests")
	}
	t.Setenv("SWIFT_PROBE_HELPER", "isolated")
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	path := filepath.Join(t.TempDir(), "cni-probe")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "router-probe")
	cmd.SysProcAttr = &syscall.SysProcAttr{Cloneflags: unix.CLONE_NEWUSER | unix.CLONE_NEWNS, UidMappings: []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getuid(), Size: 1}}, GidMappings: []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getgid(), Size: 1}}}
	input, _ := json.Marshal(Request{NamespacePath: path})
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("isolated helper: %v: %s", err, stderr.String())
	}
	for _, marker := range []string{"PRIVATE_INVALID_CA", "BEGIN CERTIFICATE", "PRIVATE KEY", "trustBundles"} {
		if strings.Contains(stdout.String(), marker) || strings.Contains(stderr.String(), marker) {
			t.Fatal("trust input leaked into helper output")
		}
	}
	var result Result
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Targets) != 8 {
		t.Fatalf("missing targets: %s", stdout.String())
	}
	for _, observation := range result.Targets {
		if strings.HasPrefix(observation.Target.ID, "trust-") {
			if observation.Stage != "trust" || observation.Error != "TLS trust unavailable or invalid" || !observation.TLSVerify || observation.TLSVerified || observation.Source != "" {
				t.Fatalf("invalid trust not isolated to its target: %+v", observation)
			}
			continue
		}
		if observation.Target.TCPOnly {
			if observation.TCPOutcome != "refused" || observation.Stage != "connect" || observation.Error == "" || observation.PreliminaryRoute == nil || observation.Route != nil || observation.TLS || observation.ExpectedStatus != 0 || observation.Expected200 || !strings.HasPrefix(observation.Source, "127.0.0.2:") || observation.Destination != "127.0.0.1:22" {
				t.Fatalf("missing TCP-only rejection evidence: %+v", observation)
			}
			continue
		}
		if observation.Target.ID == "refused" {
			if observation.Stage != "connect" || observation.Error == "" || observation.PreliminaryRoute == nil || observation.Route != nil {
				t.Fatalf("missing failed target evidence: %+v", observation)
			}
			continue
		}
		if observation.Error != "" || !observation.Expected200 || observation.RouteError != "" {
			t.Fatalf("wrong namespace or failed probe: %+v", observation)
		}
		if observation.TLSVerify != !observation.Target.PlainHTTP || observation.TLSVerified != !observation.Target.PlainHTTP {
			t.Fatalf("incorrect TLS verification evidence: %+v", observation)
		}
		source, _, err := net.SplitHostPort(observation.Source)
		if err != nil || observation.PreliminaryRoute == nil || observation.Route["source"] != source {
			t.Fatalf("post-connect route did not use actual source: %+v (%v)", observation, err)
		}
	}
	links, _ := json.Marshal(result.Before.State["links"])
	var section rtnl.Section
	if err := json.Unmarshal(links, &section); err != nil {
		t.Fatal(err)
	}
	if len(section.Entries) != 1 || section.Entries[0]["name"] != "lo" {
		t.Fatalf("evidence escaped namespace: %s", links)
	}
	if result.NamespaceInode == 0 || result.CounterDeltas["Tcp"]["ActiveOpens"] != 7 {
		t.Fatalf("missing identity or probe counters: %+v", result.CounterDeltas)
	}
	if len(result.DNS) != 4 {
		t.Fatalf("missing DNS transport/type matrix: %+v", result.DNS)
	}
	for _, observation := range result.DNS {
		if observation.Error != "" || observation.Stage != "complete" || observation.Destination != "127.0.0.1:53" {
			t.Fatalf("DNS worker escaped namespace: %+v", observation)
		}
	}
	if _, err := capture.OpenNamespace(path); err == nil {
		t.Fatal("mount escaped child")
	}
}

func runIsolated(path string) error {
	runtime.LockOSThread()
	if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); err != nil {
		return err
	}
	if err := unix.Unshare(unix.CLONE_NEWNET); err != nil {
		return err
	}
	if err := unix.Mount("/proc/thread-self/ns/net", path, "", unix.MS_BIND, ""); err != nil {
		return err
	}
	defer func() { _ = unix.Unmount(path, unix.MNT_DETACH) }()
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return err
	}
	ifr, err := unix.NewIfreq("lo")
	if err != nil {
		_ = unix.Close(fd)
		return err
	}
	ifr.SetUint16(unix.IFF_UP)
	err = unix.IoctlIfreq(fd, unix.SIOCSIFFLAGS, ifr)
	_ = unix.Close(fd)
	if err != nil {
		return err
	}
	// httptest provides a self-signed certificate, but socket creation remains
	// on this entered thread. Server goroutines only accept from this listener.
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "example.com" || r.TLS == nil || r.TLS.ServerName != "example.com" {
			w.WriteHeader(400)
			return
		}
		w.WriteHeader(200)
	}))
	server.StartTLS()
	defer server.Close()
	healthListener, err := net.Listen("tcp", "127.0.0.1:9444")
	if err != nil {
		return err
	}
	defer healthListener.Close()
	health := http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }), ReadHeaderTimeout: time.Second}
	defer health.Close()
	go func() { _ = health.Serve(healthListener) }()
	address, portString, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		return err
	}
	port, err := strconv.Atoi(portString)
	if err != nil {
		return err
	}
	var stat unix.Stat_t
	if err := unix.Stat(path, &stat); err != nil {
		return err
	}
	udpDNS, err := net.ListenPacket("udp", "127.0.0.1:53")
	if err != nil {
		return err
	}
	defer udpDNS.Close()
	tcpDNS, err := net.Listen("tcp", "127.0.0.1:53")
	if err != nil {
		return err
	}
	defer tcpDNS.Close()
	answerDNS := func(data []byte) []byte {
		var message dnsmessage.Message
		if err := message.Unpack(data); err != nil {
			return nil
		}
		message.Response = true
		data, _ = message.Pack()
		return data
	}
	go func() {
		for {
			data := make([]byte, 4096)
			n, peer, err := udpDNS.ReadFrom(data)
			if err != nil {
				return
			}
			_, _ = udpDNS.WriteTo(answerDNS(data[:n]), peer)
		}
	}()
	go func() {
		for {
			conn, err := tcpDNS.Accept()
			if err != nil {
				return
			}
			_ = conn.SetDeadline(time.Now().Add(time.Second))
			var length [2]byte
			if _, err := io.ReadFull(conn, length[:]); err == nil {
				data := make([]byte, int(binary.BigEndian.Uint16(length[:])))
				if _, err := io.ReadFull(conn, data); err == nil {
					data = answerDNS(data)
					_, _ = conn.Write(append(binary.BigEndian.AppendUint16(nil, uint16(len(data))), data...))
				}
			}
			_ = conn.Close()
		}
	}()
	req := Request{NamespacePath: path, NamespaceDevice: uint64(stat.Dev), NamespaceInode: stat.Ino, SwiftIPs: []string{"127.0.0.2"}, DNS: DNSConfig{Servers: []string{"127.0.0.1"}}, DNSNames: []string{"router.test"}, Targets: []Target{
		{ID: "default", Address: address, Port: port, ServerName: "example.com", TrustBundle: TrustIgnition},
		{ID: "swift", Address: address, Port: port, ServerName: "example.com", TrustBundle: TrustIgnition, SourceIP: "127.0.0.2"},
		{ID: "local", Address: "127.0.0.1", Port: 9444, PlainHTTP: true},
		{ID: "refused", Address: "127.0.0.1", Port: 1, ServerName: "example.com", TrustBundle: TrustIgnition},
		{ID: "worker", Role: "worker-outbound", Address: "127.0.0.1", SourceIP: "127.0.0.2", Port: 22, TCPOnly: true},
		{ID: "trust-missing", Address: address, Port: port, ServerName: "example.com", TrustBundle: "missing"},
		{ID: "trust-corrupt", Address: address, Port: port, ServerName: "example.com", TrustBundle: "corrupt"},
		{ID: "trust-oversized", Address: address, Port: port, ServerName: "example.com", TrustBundle: "oversized"},
	}}
	req.TrustBundles = map[string]string{
		TrustIgnition: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})),
		"corrupt":     "PRIVATE_INVALID_CA",
		"oversized":   strings.Repeat("x", maxTrustBundleBytes+1),
	}
	if err := checkSourcePortPolicy(); err != nil {
		return err
	}
	if err := checkTCPOnlyTimeout(); err != nil {
		return err
	}
	// Main and all existing runtime threads are now outside the target. Run
	// and EACH worker must enter the pinned FD to reach these listeners.
	if err := unix.Unshare(unix.CLONE_NEWNET); err != nil {
		return err
	}
	// A path replacement cannot alter an already opened descriptor. Verify
	// both directions: the pinned FD keeps its identity, and Run rejects the
	// replacement path instead of entering a newly discovered namespace.
	pinned, err := capture.OpenNamespace(path)
	if err != nil {
		return err
	}
	defer unix.Close(pinned)
	if err := unix.Mount("/proc/thread-self/ns/net", path, "", unix.MS_BIND, ""); err != nil {
		return err
	}
	if err := validateIdentity(pinned, req); err != nil {
		return err
	}
	var replaced bytes.Buffer
	if err := Run(req, &replaced); err == nil || replaced.Len() != 0 {
		return fmt.Errorf("replacement namespace accepted")
	}
	if err := unix.Unmount(path, unix.MNT_DETACH); err != nil {
		return err
	}
	stale := req
	stale.NamespaceInode++
	var out bytes.Buffer
	if err := Run(stale, &out); err == nil || out.Len() != 0 {
		return fmt.Errorf("stale namespace accepted")
	}
	return Run(req, os.Stdout)
}
