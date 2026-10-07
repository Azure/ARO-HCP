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
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/sys/unix"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	"github.com/Azure/ARO-HCP/swift-recorder/pkg/capture"
)

const (
	maxDNSServers    = 4
	maxDNSNames      = 8
	maxBodyBytes     = 32 * 1024
	maxHeaderBytes   = 32 * 1024
	maxBodyWireBytes = 64 * 1024
)

// Run MUST be called only by a dedicated child process which exits on return.
// Every namespace-entered thread stays locked, with no restoration or reuse.
// Each independent probe has a fresh socket and a five-second total deadline.
func Run(req Request, output io.Writer) error {
	if err := validateRequest(req); err != nil {
		return err
	}
	fd, err := capture.OpenNamespace(req.NamespacePath)
	if err != nil {
		return err
	}
	defer capture.CloseWithLog("namespace", func() error { return unix.Close(fd) })
	if err := validateIdentity(fd, req); err != nil {
		return err
	}
	runtime.LockOSThread()
	if err := unix.Setns(fd, unix.CLONE_NEWNET); err != nil {
		return fmt.Errorf("enter network namespace: %w", err)
	}
	result := Result{NamespaceDevice: req.NamespaceDevice, NamespaceInode: req.NamespaceInode, StartedAt: time.Now().UTC(), DNSConfig: req.DNS}
	if len(req.Targets) > MaxTargets {
		result.Truncated = append(result.Truncated, fmt.Sprintf("targets: retained %d of %d", MaxTargets, len(req.Targets)))
		req.Targets = req.Targets[:MaxTargets]
	}
	if len(req.DNS.Servers) > maxDNSServers {
		result.Truncated = append(result.Truncated, fmt.Sprintf("DNS servers: retained %d of %d", maxDNSServers, len(req.DNS.Servers)))
		req.DNS.Servers = req.DNS.Servers[:maxDNSServers]
	}
	if len(req.DNSNames) > maxDNSNames {
		result.Truncated = append(result.Truncated, fmt.Sprintf("DNS names: retained %d of %d", maxDNSNames, len(req.DNSNames)))
		req.DNSNames = req.DNSNames[:maxDNSNames]
	}
	result.Before = collectEvidence()
	trustBundles := loadTrustBundles(req.TrustBundles)
	result.Targets = make([]Observation, len(req.Targets))
	result.DNS = make([]DNSObservation, len(req.DNS.Servers)*len(req.DNSNames)*4)
	var wg sync.WaitGroup
	for i, target := range req.Targets {
		wg.Add(1)
		go func() {
			defer utilruntime.HandleCrash()
			defer wg.Done()
			runtime.LockOSThread()
			if err := unix.Setns(fd, unix.CLONE_NEWNET); err != nil {
				result.Targets[i] = Observation{Target: target, StartedAt: time.Now().UTC(), Stage: "namespace", Error: err.Error(), ExpectedStatus: http.StatusOK, TLS: !target.PlainHTTP && !target.TCPOnly, TLSVerify: !target.PlainHTTP && !target.TCPOnly}
				if target.TCPOnly {
					result.Targets[i].ExpectedStatus = 0
				}
				return
			}
			result.Targets[i] = probeTarget(target, trustBundles[target.TrustBundle], probeTimeout)
		}()
	}
	index := 0
	for _, server := range req.DNS.Servers {
		for _, name := range req.DNSNames {
			for _, protocol := range []string{"udp", "tcp"} {
				for _, kind := range []dnsmessage.Type{dnsmessage.TypeA, dnsmessage.TypeAAAA} {
					i := index
					index++
					wg.Add(1)
					go func() {
						defer utilruntime.HandleCrash()
						defer wg.Done()
						runtime.LockOSThread()
						if err := unix.Setns(fd, unix.CLONE_NEWNET); err != nil {
							result.DNS[i] = DNSObservation{Name: name, Server: server, Protocol: protocol, Type: kind.String(), StartedAt: time.Now().UTC(), Stage: "namespace", Error: err.Error()}
							return
						}
						result.DNS[i] = probeDNS(server, name, protocol, kind, probeTimeout)
					}()
				}
			}
		}
	}
	wg.Wait()
	result.After = collectEvidence()
	result.CounterDeltas = counterDeltas(result.Before, result.After)
	result.FinishedAt = time.Now().UTC()
	return writeResult(output, result)
}

func validateIdentity(fd int, req Request) error {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return fmt.Errorf("stat namespace: %w", err)
	}
	if req.NamespaceDevice != uint64(stat.Dev) || req.NamespaceInode != stat.Ino {
		return fmt.Errorf("namespace identity changed: expected %d:%d, opened %d:%d", req.NamespaceDevice, req.NamespaceInode, uint64(stat.Dev), stat.Ino)
	}
	return nil
}

func validateRequest(req Request) error {
	// Bound even callers of Run that bypass the command's limited JSON reader.
	if len(req.NamespacePath) > 4096 || len(req.Targets) > 4096 || len(req.DNSNames) > 256 || len(req.DNS.Servers) > 256 || len(req.SwiftIPs) > 256 || len(req.DNS.Searches) > 256 || len(req.DNS.Options) > 256 || len(req.DNS.Source) > 4096 {
		return fmt.Errorf("probe request exceeded input safety bounds")
	}
	for _, ip := range req.SwiftIPs {
		if _, err := literalIP(ip); err != nil {
			return fmt.Errorf("invalid Swift IP")
		}
	}
	for _, value := range append(slices.Clone(req.DNS.Searches), req.DNS.Options...) {
		if len(value) > 256 {
			return fmt.Errorf("resolver metadata exceeded safety bounds")
		}
	}
	for _, target := range req.Targets {
		ip, err := literalIP(target.Address)
		if err != nil {
			return fmt.Errorf("target address must be a literal IP")
		}
		if target.Port < 1 || target.Port > 65535 || len(target.ID) > 256 || len(target.Role) > 128 {
			return fmt.Errorf("invalid target port or metadata")
		}
		if target.TrustBundle != "" && !validDNSName(target.TrustBundle) {
			return fmt.Errorf("invalid trust bundle identifier")
		}
		if target.SourceIP != "" {
			source, err := literalIP(target.SourceIP)
			if err != nil || source.Is4() != ip.Is4() {
				return fmt.Errorf("invalid target source IP or address family")
			}
			allowed := false
			for _, swift := range req.SwiftIPs {
				parsed, _ := literalIP(swift)
				allowed = allowed || parsed == source
			}
			if !allowed {
				return fmt.Errorf("explicit source binding requires a discovered Swift IP")
			}
		}
		if target.TCPOnly {
			if target.Role != "worker-outbound" || target.Port != 22 || target.SourceIP == "" {
				return fmt.Errorf("TCP-only probes require worker-outbound port 22 and a discovered Swift source IP")
			}
			if target.PlainHTTP || target.ServerName != "" || target.Path != "" || target.TrustBundle != "" {
				return fmt.Errorf("TCP-only probes must not specify plain HTTP, server name, HTTP path or trust bundle")
			}
		}
		if target.PlainHTTP && (!ip.IsLoopback() || target.Port != 9444) {
			return fmt.Errorf("plain HTTP is restricted to loopback port 9444")
		}
		if target.ServerName != "" && !validDNSName(target.ServerName) {
			return fmt.Errorf("invalid target server name")
		}
		if target.LivenessOnFailure && (!isKASTLS(target) || target.Path != "/readyz") {
			return fmt.Errorf("liveness follow-up requires a KAS HTTPS readiness probe with root trust")
		}
		if target.Path != "" {
			path, err := url.ParseRequestURI(target.Path)
			allowedQuery := isKASTLS(target) && target.Path == "/livez?exclude=etcd"
			if err != nil || len(target.Path) > 1024 || !strings.HasPrefix(target.Path, "/") || strings.HasPrefix(target.Path, "//") || path.IsAbs() || path.Host != "" || (path.RawQuery != "" || path.ForceQuery) && !allowedQuery || path.Fragment != "" || strings.ContainsAny(target.Path, "\r\n") {
				return fmt.Errorf("target path must be a credential-free absolute path; only KAS HTTPS root-trusted /livez?exclude=etcd may contain a query")
			}
		}
	}
	for _, server := range req.DNS.Servers {
		if _, err := literalIP(server); err != nil {
			return fmt.Errorf("DNS server must be a literal IP")
		}
	}
	for _, name := range req.DNSNames {
		if !validDNSName(name) || net.ParseIP(name) != nil {
			return fmt.Errorf("invalid DNS discovery name")
		}
	}
	return nil
}

func validDNSName(name string) bool {
	name = strings.TrimSuffix(name, ".")
	if len(name) == 0 || len(name) > 253 {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' {
				return false
			}
		}
	}
	return true
}

func literalIP(value string) (netip.Addr, error) {
	ip, err := netip.ParseAddr(value)
	if err != nil || ip.Zone() != "" {
		return netip.Addr{}, fmt.Errorf("expected unscoped literal IP")
	}
	return ip.Unmap(), nil
}

// dialSocket creates the socket synchronously on this locked OS thread. The
// net package's Dialer can create sockets on other goroutines in the host netns.
// FileConn only duplicates this already-connected, namespace-owned descriptor.
func dialSocket(address, source string, port int, datagram bool, deadline time.Time) (_ net.Conn, err error) {
	ip, err := literalIP(address)
	if err != nil {
		return nil, err
	}
	family, kind := unix.AF_INET6, unix.SOCK_STREAM
	if ip.Is4() {
		family = unix.AF_INET
	}
	if datagram {
		kind = unix.SOCK_DGRAM
	}
	fd, err := unix.Socket(family, kind|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "probe-socket")
	defer capture.CloseWithLog("probe socket file", file.Close)
	if source != "" {
		src, err := literalIP(source)
		if err != nil || src.Is4() != ip.Is4() {
			return nil, fmt.Errorf("invalid source address")
		}
		if err := unix.Bind(fd, sockaddr(src, 0)); err != nil {
			return nil, fmt.Errorf("bind: %w", err)
		}
	}
	// Preserve an assigned local tuple before closing a failed socket, without
	// changing the error text used by existing HTTP/TLS and DNS observations.
	defer func() {
		if err == nil {
			return
		}
		local, _ := unix.Getsockname(fd)
		switch local := local.(type) {
		case *unix.SockaddrInet4:
			if local.Port != 0 {
				err = &socketError{error: err, source: net.JoinHostPort(netip.AddrFrom4(local.Addr).String(), fmt.Sprint(local.Port))}
			}
		case *unix.SockaddrInet6:
			if local.Port != 0 {
				err = &socketError{error: err, source: net.JoinHostPort(netip.AddrFrom16(local.Addr).String(), fmt.Sprint(local.Port))}
			}
		}
	}()
	err = unix.Connect(fd, sockaddr(ip, port))
	if err != nil && !errors.Is(err, unix.EINPROGRESS) && !errors.Is(err, unix.EINTR) {
		return nil, err
	}
	if err != nil {
		for {
			remaining := time.Until(deadline)
			if remaining <= 0 {
				return nil, os.ErrDeadlineExceeded
			}
			poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLOUT}}
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
			code, err := unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_ERROR)
			if err != nil {
				return nil, err
			}
			if code != 0 {
				return nil, unix.Errno(code)
			}
			break
		}
	}
	conn, err := net.FileConn(file)
	if err != nil {
		return nil, err
	}
	if err := conn.SetDeadline(deadline); err != nil {
		capture.CloseWithLog("probe socket", conn.Close)
		return nil, err
	}
	return conn, nil
}

type socketError struct {
	error
	source string
}

func (e *socketError) Unwrap() error { return e.error }

func tcpOutcome(err error) string {
	var networkError net.Error
	switch {
	case err == nil:
		return "connected"
	case errors.Is(err, unix.ECONNREFUSED):
		return "refused"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded), errors.As(err, &networkError) && networkError.Timeout():
		return "timeout"
	case errors.Is(err, unix.EADDRNOTAVAIL), errors.Is(err, unix.EADDRINUSE), errors.Is(err, unix.ENETUNREACH), errors.Is(err, unix.EHOSTUNREACH), errors.Is(err, unix.ENETDOWN), errors.Is(err, unix.EHOSTDOWN), errors.Is(err, unix.EACCES), errors.Is(err, unix.EPERM):
		return "bindRoutingError"
	default:
		return "otherError"
	}
}

func sockaddr(ip netip.Addr, port int) unix.Sockaddr {
	if ip.Is4() {
		return &unix.SockaddrInet4{Port: port, Addr: ip.As4()}
	}
	return &unix.SockaddrInet6{Port: port, Addr: ip.As16()}
}

func isKASTLS(target Target) bool {
	return strings.HasPrefix(target.Role, "kas-") && !target.PlainHTTP && !target.TCPOnly && target.TrustBundle == TrustRoot
}

func probeTarget(target Target, roots *x509.CertPool, timeout time.Duration) Observation {
	result := probeTargetOnce(target, roots, timeout)
	if target.LivenessOnFailure && isKASTLS(target) && target.Path == "/readyz" && result.TLSVerified && result.Stage == "complete" && !result.BodyTruncated && result.HTTPStatus >= http.StatusInternalServerError {
		// Reuse only discovery and trust, not the connection or its deadline.
		target.Path, target.LivenessOnFailure = "/livez?exclude=etcd", false
		liveness := probeTargetOnce(target, roots, timeout)
		result.Liveness = &liveness
	}
	return result
}

func probeTargetOnce(target Target, roots *x509.CertPool, timeout time.Duration) (result Observation) {
	start := time.Now()
	deadline := start.Add(timeout)
	result = Observation{Target: target, StartedAt: start.UTC(), Timings: map[string]float64{}, TLS: !target.PlainHTTP, ExpectedStatus: http.StatusOK, Stage: "connect"}
	if target.TCPOnly {
		result.TLS, result.ExpectedStatus = false, 0
		result.Destination = net.JoinHostPort(target.Address, fmt.Sprint(target.Port))
	}
	defer func() { result.DurationMS = milliseconds(time.Since(start)) }()
	result.TLSVerify = result.TLS
	if result.TLS && (target.ServerName == "" || target.TrustBundle == "" || roots == nil || roots.Equal(x509.NewCertPool())) {
		result.Stage, result.Error = "trust", "TLS trust unavailable or invalid"
		return
	}
	// Include route evidence even when the following connect fails. This is
	// supporting evidence only and shares the total connection deadline.
	var err error
	result.PreliminaryRoute, err = lookupRoute(target, 0, minTime(deadline, time.Now().Add(100*time.Millisecond)))
	if err != nil {
		result.PreliminaryRouteError = err.Error()
	}
	result.Timings["preliminaryRoute"] = milliseconds(time.Since(start))
	connectStart := time.Now()
	conn, err := dialSocket(target.Address, target.SourceIP, target.Port, false, deadline)
	result.Timings["connect"] = milliseconds(time.Since(connectStart))
	if target.TCPOnly {
		result.TCPOutcome = tcpOutcome(err)
		var socketErr *socketError
		if errors.As(err, &socketErr) {
			result.Source = socketErr.source
		}
	}
	if err != nil {
		result.Error = err.Error()
		return
	}
	rawConn := conn
	defer func() {
		if rawConn != nil {
			capture.CloseWithLog("probe socket", rawConn.Close)
		}
	}()
	result.Source, result.Destination = conn.LocalAddr().String(), conn.RemoteAddr().String()
	routeStart := time.Now()
	local := conn.LocalAddr().(*net.TCPAddr)
	remote := conn.RemoteAddr().(*net.TCPAddr)
	actual := target
	actual.SourceIP, actual.Address, actual.Port = local.IP.String(), remote.IP.String(), remote.Port
	result.Route, err = lookupRoute(actual, local.Port, minTime(deadline, time.Now().Add(100*time.Millisecond)))
	if err != nil {
		result.RouteError = err.Error()
	}
	result.Timings["route"] = milliseconds(time.Since(routeStart))
	if target.TCPOnly {
		result.Stage = "complete"
		return
	}
	if !target.PlainHTTP {
		result.Stage = "tls"
		stageStart := time.Now()
		tlsConn := tls.Client(conn, &tls.Config{ServerName: target.ServerName, RootCAs: roots, MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}})
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		err := tlsConn.HandshakeContext(ctx)
		cancel()
		result.Timings["tls"] = milliseconds(time.Since(stageStart))
		if err != nil {
			result.Error = tlsError(err)
			return
		}
		result.TLSVerified = true
		conn = tlsConn
	}
	result.Stage = "httpWrite"
	stageStart := time.Now()
	path := target.Path
	if path == "" {
		path = "/healthz"
	}
	host := target.ServerName
	if host == "" {
		host = net.JoinHostPort(target.Address, fmt.Sprint(target.Port))
	}
	requestURL, err := url.ParseRequestURI(path)
	if err != nil {
		result.Error = "invalid HTTP request path"
		return
	}
	request := &http.Request{Method: http.MethodGet, URL: requestURL, Host: host, Header: make(http.Header), Close: true}
	request.Header.Set("User-Agent", "swift-recorder-probe")
	if err := request.Write(conn); err != nil {
		result.Error = protocolError("HTTP request write failed", err)
		return
	}
	result.Timings["httpWrite"] = milliseconds(time.Since(stageStart))
	result.Stage = "httpHeaders"
	stageStart = time.Now()
	limited := &wireBudgetReader{reader: conn, remaining: maxHeaderBytes}
	reader := bufio.NewReader(limited)
	response, err := http.ReadResponse(reader, request)
	result.Timings["httpHeaders"] = milliseconds(time.Since(stageStart))
	if err != nil {
		result.Error = protocolError("invalid, oversized or incomplete HTTP response headers", err)
		return
	}
	result.HTTPStatus, result.Expected200 = response.StatusCode, response.StatusCode == http.StatusOK
	result.Stage = "httpBody"
	stageStart = time.Now()
	// Allow bounded chunk framing as well as the extra decoded byte used to
	// detect truncation. Count prefetched bytes against the wire budget too.
	limited.remaining = maxBodyWireBytes - int64(reader.Buffered())
	result.BodyBytesDiscarded, err = discardBody(rawConn, response.Body)
	rawConn = nil // discardBody owns socket cleanup once a response body exists.
	result.BodyTruncated = result.BodyBytesDiscarded > maxBodyBytes
	result.BodyBytesDiscarded = min(result.BodyBytesDiscarded, maxBodyBytes)
	if result.BodyTruncated {
		result.BodyTruncationReason = "decodedBodyLimit"
	}
	if errors.Is(err, errWireBudget) {
		result.BodyTruncated = true
		result.BodyTruncationReason = "wireLimit"
		err = nil
	}
	result.Timings["httpBody"] = milliseconds(time.Since(stageStart))
	if err != nil {
		result.Error = protocolError("HTTP response body read failed", err)
		return
	}
	result.Stage = "complete"
	if !result.Expected200 {
		result.Error = fmt.Sprintf("unexpected HTTP status %d (expected 200)", result.HTTPStatus)
		if strings.HasPrefix(target.Role, "kas-") {
			switch {
			case result.HTTPStatus == http.StatusUnauthorized || result.HTTPStatus == http.StatusForbidden:
				result.Error = fmt.Sprintf("KAS authentication/authorization denied: HTTP status %d", result.HTTPStatus)
			case result.HTTPStatus >= http.StatusInternalServerError:
				result.Error = fmt.Sprintf("KAS health check failed: HTTP status %d", result.HTTPStatus)
			}
		}
	}
	return
}

func discardBody(rawConn net.Conn, body io.ReadCloser) (int64, error) {
	n, err := io.Copy(io.Discard, io.LimitReader(body, maxBodyBytes+1))
	// Close the raw socket FIRST: Body.Close may drain an unlimited chunked
	// body, and tls.Conn.Close may extend the deadline to send close_notify.
	capture.CloseWithLog("probe socket", rawConn.Close)
	capture.CloseWithLog("HTTP body", func() error {
		err := body.Close()
		// Draining after intentionally closing the socket or exhausting the
		// wire budget can fail as expected; unrelated close errors still log.
		if errors.Is(err, net.ErrClosed) || errors.Is(err, io.ErrClosedPipe) || errors.Is(err, errWireBudget) {
			return nil
		}
		return err
	})
	return n, err
}

var errWireBudget = errors.New("probe HTTP wire budget exhausted")

type wireBudgetReader struct {
	reader    io.Reader
	remaining int64
}

func (r *wireBudgetReader) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, errWireBudget
	}
	n, err := r.reader.Read(p[:min(int64(len(p)), r.remaining)])
	r.remaining -= int64(n)
	return n, err
}

func protocolError(message string, err error) string {
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() || errors.Is(err, context.DeadlineExceeded) {
		return message + ": deadline exceeded"
	}
	return message
}

func milliseconds(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
