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

package otelaudit

import (
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/microsoft/go-otel-audit/audit/conn"
	"github.com/microsoft/go-otel-audit/audit/msgs"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
)

// fakeReceiver is a minimal Unix domain socket peer standing in for mdsd. It accepts
// connections, discards everything written to them, and lets the test force a peer-restart
// by dropping the currently accepted connection out from under the client.
type fakeReceiver struct {
	listener net.Listener
	accepted atomic.Int32

	mu      sync.Mutex
	current net.Conn
}

func startFakeReceiver(t *testing.T, path string) *fakeReceiver {
	t.Helper()
	l, err := net.Listen("unix", path)
	require.NoError(t, err)

	r := &fakeReceiver{listener: l}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			r.mu.Lock()
			r.current = c
			r.mu.Unlock()
			r.accepted.Add(1)
			go io.Copy(io.Discard, c) //nolint:errcheck // draining until the test or peer closes it
		}
	}()
	t.Cleanup(func() { _ = l.Close() })
	return r
}

// dropConnection closes the currently accepted connection to simulate the remote peer
// (mdsd) restarting, which is what the client's reconnect logic must recover from.
func (r *fakeReceiver) dropConnection() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.current != nil {
		_ = r.current.Close()
		r.current = nil
	}
}

// openFDCount returns the number of file descriptors open in this process, via the
// /dev/fd pseudo-filesystem available on both Linux and Darwin.
func openFDCount(t *testing.T) int {
	t.Helper()
	// Readdirnames (unlike os.ReadDir) does not lstat each entry, avoiding a race where
	// an fd closes between listing and stat-ing it.
	dir, err := os.Open("/dev/fd")
	require.NoError(t, err)
	defer dir.Close()
	names, err := dir.Readdirnames(-1)
	require.NoError(t, err)
	return len(names)
}

// TestAuditClientReconnectsOverUnixSocket exercises the recovery path against a real Unix
// domain socket receiver end to end: no receiver present at startup (degraded gauge set),
// a receiver coming online (gauge clears), and repeated peer restarts. The upstream
// go-otel-audit v1.1.0 reconnect loop never closes the conn.Audit it is replacing
// (see the comment in newOtelAuditClient), which leaks one file descriptor per restart;
// this test fails on that regression by tracking this process's open FD count.
func TestAuditClientReconnectsOverUnixSocket(t *testing.T) {
	// The upstream client has no Close API. Isolate its process-lifetime workers.
	if os.Getenv("ARO_HCP_AUDIT_TEST") != t.Name() {
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^"+t.Name()+"$", "-test.timeout=25s")
		command.Env = append(os.Environ(), "ARO_HCP_AUDIT_TEST="+t.Name())
		output, err := command.CombinedOutput()
		require.NoError(t, err, "%s", output)
		return
	}

	socketDir, err := os.MkdirTemp("", "aro-hcp-audit-*")
	require.NoError(t, err, "unix socket paths have a short max length (sun_path); t.TempDir() nests too deep")
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	socketPath := filepath.Join(socketDir, "audit.sock")
	registry := prometheus.NewRegistry()

	client, err := newOtelAuditClient(t.Context(), uuid.MustParse("11111111-1111-4111-8111-111111111111"), func() (conn.Audit, error) {
		return conn.NewDomainSocket(conn.DomainSocketPath(socketPath))
	}, registry)
	require.NoError(t, err, "deferred connection mode must not fail startup just because the receiver isn't up yet")
	require.Equal(t, float64(1), auditMetric(t, registry, MetricAuditLogConnectionDegraded), "no receiver listening yet")

	receiver := startFakeReceiver(t, socketPath)
	require.Eventually(t, func() bool {
		return auditMetric(t, registry, MetricAuditLogConnectionDegraded) == 0
	}, 10*time.Second, 50*time.Millisecond, "client must reconnect once the receiver comes online")
	require.NoError(t, client.Send(t.Context(), msgs.Msg{Type: msgs.ControlPlane}))

	require.Eventually(t, func() bool { return receiver.accepted.Load() >= 1 }, 5*time.Second, 50*time.Millisecond)
	baselineFDs := openFDCount(t)

	const restarts = 20
	for i := range restarts {
		before := receiver.accepted.Load()
		receiver.dropConnection()
		// Keep sending until the dead connection is noticed and replaced; the first
		// write or two may still land in a kernel buffer before the peer close surfaces.
		require.Eventually(t, func() bool {
			_ = client.Send(t.Context(), msgs.Msg{Type: msgs.ControlPlane})
			return receiver.accepted.Load() > before
		}, 5*time.Second, 10*time.Millisecond, "client must reconnect after restart #%d", i)
	}

	// Each restart must close the connection it replaces. A leak grows this by ~1 per
	// restart (20 here); the fix keeps it flat aside from the one live connection.
	require.LessOrEqual(t, openFDCount(t)-baselineFDs, 3,
		"file descriptors grew with reconnects: the old conn.Audit is not being closed")
}
