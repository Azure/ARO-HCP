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
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/streaming/pkg/httpstream"
)

// curlExitTimeout is curl's exit code for "operation timed out" (-m elapsed).
const curlExitTimeout = 28

// curlWriteFormat is emitted by curl on stdout and parsed by parseCurlOutput:
// "<http_code> <time_connect>". curl prints this even on failure (http_code
// "000", time_connect "0.000000"), so stdout classifies the edge and the exec
// exit code distinguishes a timeout from other failures.
const curlWriteFormat = "%{http_code} %{time_connect}"

// ExecDriver probes each edge by exec'ing curl inside the vantage router pod,
// mirroring `kubectl exec <pod> -- curl -sk -m<t> https://<ip>:<port>/`. This is
// the only way to exercise the data path from the pod's own network namespace
// without changing the router DaemonSet.
type ExecDriver struct {
	clientset  kubernetes.Interface
	restConfig *rest.Config
	timeout    time.Duration // curl -m value.

	// newExecutor is overridable in tests; nil uses the real SPDY/WebSocket path.
	newExecutor func(config *rest.Config, u *url.URL) (remotecommand.Executor, error)
}

// NewExecDriver builds an ExecDriver. timeout is passed to curl as -m.
func NewExecDriver(clientset kubernetes.Interface, restConfig *rest.Config, timeout time.Duration) *ExecDriver {
	return &ExecDriver{clientset: clientset, restConfig: restConfig, timeout: timeout}
}

func (d *ExecDriver) Probe(ctx context.Context, from RouterPod, target Target, port int) Result {
	res := Result{FromPod: from.Name, FromIP: from.SwiftIP, ToIP: target.IP, Kind: target.Kind}

	addr := net.JoinHostPort(target.IP, strconv.Itoa(port))
	// curl -m accepts fractional seconds; format the exact duration so a
	// sub-second timeout is honoured rather than truncated to whole seconds.
	timeout := d.timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	seconds := strconv.FormatFloat(timeout.Seconds(), 'f', -1, 64)
	command := []string{"curl", "-sk", "-m", seconds, "-o", "/dev/null", "-w", curlWriteFormat, "https://" + addr + "/"}

	req := d.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(from.Namespace).
		Name(from.Name).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: from.Container,
			Command:   command,
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)

	newExecutor := d.newExecutor
	if newExecutor == nil {
		newExecutor = defaultExecutor
	}
	exec, err := newExecutor(d.restConfig, req.URL())
	if err != nil {
		res.Err = fmt.Errorf("build executor for pod %s/%s: %w", from.Namespace, from.Name, err)
		return res
	}

	var stdout, stderr bytes.Buffer
	streamErr := exec.StreamWithContext(ctx, remotecommand.StreamOptions{Stdout: &stdout, Stderr: &stderr})

	if status, connect, ok := parseCurlOutput(stdout.String()); ok {
		res.StatusCode = status
		res.ConnectTime = connect
	}
	if streamErr != nil {
		res.Err = fmt.Errorf("exec curl in %s/%s: %w (stderr: %q)", from.Namespace, from.Name, streamErr, strings.TrimSpace(stderr.String()))
		if code, ok := exitCode(streamErr); ok && code == curlExitTimeout {
			res.TimedOut = true
		}
	}
	// haproxy answered iff curl reported a real HTTP status.
	res.OK = res.StatusCode > 0
	if !res.OK && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		res.TimedOut = true
	}
	return res
}

// defaultExecutor prefers a WebSocket stream and falls back to SPDY on an
// upgrade failure, matching current kubectl behaviour (SPDY is deprecated).
func defaultExecutor(config *rest.Config, u *url.URL) (remotecommand.Executor, error) {
	spdyExec, err := remotecommand.NewSPDYExecutor(config, "POST", u)
	if err != nil {
		return nil, err
	}
	wsExec, err := remotecommand.NewWebSocketExecutor(config, "GET", u.String())
	if err != nil {
		return nil, err
	}
	return remotecommand.NewFallbackExecutor(wsExec, spdyExec, httpstream.IsUpgradeFailure)
}

// exitCoder is satisfied by remotecommand's non-zero-exit error type,
// k8s.io/client-go/util/exec.CodeExitError.
type exitCoder interface {
	error
	ExitStatus() int
}

// exitCode extracts a command exit status from a remotecommand error.
func exitCode(err error) (int, bool) {
	if ec, ok := errors.AsType[exitCoder](err); ok {
		return ec.ExitStatus(), true
	}
	return 0, false
}

// parseCurlOutput parses "<http_code> <time_connect>". http_code "000" (no
// response) parses to 0. Returns ok=false only when the line is unparseable.
func parseCurlOutput(s string) (status int, connect time.Duration, ok bool) {
	fields := strings.Fields(s)
	if len(fields) < 2 {
		return 0, 0, false
	}
	code, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0, 0, false
	}
	secs, err := strconv.ParseFloat(fields[1], 64)
	if err != nil {
		return code, 0, true // status is usable even if the timing field is not.
	}
	return code, time.Duration(secs * float64(time.Second)), true
}
