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

package swiftmesh

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	utilexec "k8s.io/client-go/util/exec"
)

// fakeExecutor stands in for a remotecommand.Executor: it writes canned curl
// stdout and returns a canned error (mirroring curl's exit code via CodeExitError).
type fakeExecutor struct {
	stdout string
	err    error
}

func (f fakeExecutor) Stream(opts remotecommand.StreamOptions) error {
	return f.StreamWithContext(context.Background(), opts)
}

func (f fakeExecutor) StreamWithContext(_ context.Context, opts remotecommand.StreamOptions) error {
	if opts.Stdout != nil && f.stdout != "" {
		_, _ = io.WriteString(opts.Stdout, f.stdout)
	}
	return f.err
}

func newTestExecDriver(t *testing.T, stdout string, streamErr error) *ExecDriver {
	t.Helper()
	cfg := &rest.Config{Host: "https://example.test"}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("build clientset: %v", err)
	}
	d := NewExecDriver(cs, cfg, 5*time.Second)
	d.newExecutor = func(*rest.Config, *url.URL) (remotecommand.Executor, error) {
		return fakeExecutor{stdout: stdout, err: streamErr}, nil
	}
	return d
}

func codeExit(code int) error {
	return utilexec.CodeExitError{Code: code, Err: fmt.Errorf("command terminated with exit code %d", code)}
}

func TestExecDriverProbe(t *testing.T) {
	from := RouterPod{Name: "router-a", Namespace: "ocm-int-abc", Container: "router", SwiftIP: "10.100.77.5"}
	peer := Target{IP: "10.100.77.7", Kind: KindPeer}

	tests := []struct {
		name         string
		stdout       string
		streamErr    error
		wantOK       bool
		wantStatus   int
		wantTimedOut bool
		wantErr      bool
	}{
		{name: "healthy 403", stdout: "403 0.005000", streamErr: nil, wantOK: true, wantStatus: 403},
		{name: "timeout exit 28", stdout: "000 0.000000", streamErr: codeExit(28), wantTimedOut: true, wantErr: true},
		{name: "connection refused exit 7", stdout: "000 0.000000", streamErr: codeExit(7), wantErr: true},
		{name: "curl missing exit 127", stdout: "", streamErr: codeExit(127), wantErr: true},
		{name: "generic exec error", stdout: "", streamErr: fmt.Errorf("stream upgrade failed"), wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := newTestExecDriver(t, tc.stdout, tc.streamErr)
			res := d.Probe(context.Background(), from, peer, 8443)

			if res.OK != tc.wantOK {
				t.Fatalf("OK = %v, want %v", res.OK, tc.wantOK)
			}
			if res.StatusCode != tc.wantStatus {
				t.Fatalf("StatusCode = %d, want %d", res.StatusCode, tc.wantStatus)
			}
			if res.TimedOut != tc.wantTimedOut {
				t.Fatalf("TimedOut = %v, want %v", res.TimedOut, tc.wantTimedOut)
			}
			if (res.Err != nil) != tc.wantErr {
				t.Fatalf("Err = %v, want error=%v", res.Err, tc.wantErr)
			}
			if res.FromPod != from.Name || res.ToIP != peer.IP || res.Kind != KindPeer {
				t.Fatalf("edge identity not propagated: %+v", res)
			}
		})
	}
}

// Exit 7 (connection refused) must NOT be classified as a timeout: the reviewer
// specifically called out conflating these.
func TestExecDriverRefusedIsNotTimeout(t *testing.T) {
	d := newTestExecDriver(t, "000 0.000000", codeExit(7))
	res := d.Probe(context.Background(), RouterPod{Name: "r", SwiftIP: "10.0.0.1"}, Target{IP: "10.0.0.2", Kind: KindPeer}, 8443)
	if res.TimedOut {
		t.Fatal("exit 7 (refused) must not be reported as a timeout")
	}
	if res.OK {
		t.Fatal("exit 7 (refused) must not be reported as reachable")
	}
}

// A context deadline with no curl exit code still classifies as a timeout.
func TestExecDriverContextDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()
	<-ctx.Done()

	d := newTestExecDriver(t, "", fmt.Errorf("context deadline exceeded"))
	res := d.Probe(ctx, RouterPod{Name: "r", SwiftIP: "10.0.0.1"}, Target{IP: "10.0.0.2", Kind: KindPeer}, 8443)
	if !res.TimedOut {
		t.Fatalf("expected context deadline to classify as timeout, got %+v", res)
	}
	if res.OK {
		t.Fatal("deadline-exceeded edge must not be reachable")
	}
}

func TestExecDriverCommandFormatsFractionalTimeout(t *testing.T) {
	// A sub-second timeout must reach curl as a fractional -m, not be truncated.
	cfg := &rest.Config{Host: "https://example.test"}
	cs, _ := kubernetes.NewForConfig(cfg)
	d := NewExecDriver(cs, cfg, 500*time.Millisecond)

	var gotURL *url.URL
	d.newExecutor = func(_ *rest.Config, u *url.URL) (remotecommand.Executor, error) {
		gotURL = u
		return fakeExecutor{stdout: "403 0.100000"}, nil
	}
	_ = d.Probe(context.Background(), RouterPod{Name: "r", Namespace: "ocm-x", Container: "router", SwiftIP: "10.0.0.1"}, Target{IP: "10.0.0.2", Kind: KindPeer}, 8443)

	// The exec command is encoded in the request URL query as repeated "command"
	// params; the -m value must be "0.5", never "0".
	raw := gotURL.RawQuery
	if !strings.Contains(raw, "command=0.5") {
		t.Fatalf("expected fractional curl -m 0.5 in exec command, query=%q", raw)
	}
	if strings.Contains(raw, "command=0&") || strings.HasSuffix(raw, "command=0") {
		t.Fatalf("curl -m was truncated to 0, query=%q", raw)
	}
}
