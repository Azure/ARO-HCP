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

package routercheck

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-logr/logr"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/swift-recorder/pkg/probe"
)

func TestSummarizeResult(t *testing.T) {
	for _, tc := range []struct {
		name    string
		change  func([]TargetInfo, *probe.Result)
		want    passSummary
		invalid bool
	}{
		{name: "healthy"},
		{name: "http error", change: func(_ []TargetInfo, r *probe.Result) { r.Targets[0].HTTPStatus = 503 }, want: passSummary{HTTPFailed: 1}},
		{name: "kas not ready but live", change: func(_ []TargetInfo, r *probe.Result) {
			r.Targets[0].HTTPStatus, r.Targets[0].Expected200 = 500, false
			r.Targets[0].Liveness = &probe.Observation{Stage: "complete", HTTPStatus: 200, Expected200: true, TLSVerify: true, TLSVerified: true}
		}, want: passSummary{HTTPFailed: 1}},
		{name: "unready endpoint error", change: func(targets []TargetInfo, r *probe.Result) {
			targets[1].Ready = ptr.To(false)
			r.Targets[1].Error = "timeout"
		}, want: passSummary{HTTPFailed: 1}},
		{name: "retiring endpoint error", change: func(targets []TargetInfo, r *probe.Result) {
			targets[1].Deleting = true
			r.Targets[1].Error = "timeout"
		}, want: passSummary{HTTPFailed: 1, RetiredFailed: 1}},
		{name: "retiring and active errors", change: func(targets []TargetInfo, r *probe.Result) {
			targets[1].Deleting = true
			r.Targets[0].HTTPStatus, r.Targets[1].Error = 503, "timeout"
		}, want: passSummary{HTTPFailed: 2, RetiredFailed: 1}},
		{name: "shared worker timeout", change: func(targets []TargetInfo, r *probe.Result) {
			targets[2].Deleting = true
			targets[2].Workers = []WorkerInfo{{MachineDeleting: true}, {}}
			r.Targets[2].TCPOutcome = "timeout"
		}, want: passSummary{TCPTimeout: 1}},
		{name: "retiring workers timeout", change: func(targets []TargetInfo, r *probe.Result) {
			targets[2].Workers = []WorkerInfo{{MachineDeleting: true}, {AzureMachineDeleting: true}}
			r.Targets[2].TCPOutcome = "timeout"
		}, want: passSummary{TCPTimeout: 1, RetiredFailed: 1}},
		{name: "tcp refused", change: func(_ []TargetInfo, r *probe.Result) {
			r.Targets[2].TCPOutcome, r.Targets[2].Stage, r.Targets[2].Error = "refused", "connect", "dial error"
		}, want: passSummary{TCPRefused: 1}},
		{name: "tcp timeout", change: func(_ []TargetInfo, r *probe.Result) { r.Targets[2].TCPOutcome = "timeout" }, want: passSummary{TCPTimeout: 1}},
		{name: "retiring tcp connected", change: func(targets []TargetInfo, _ *probe.Result) { targets[2].Deleting = true }},
		{name: "retiring tcp refused", change: func(targets []TargetInfo, r *probe.Result) {
			targets[2].Deleting = true
			r.Targets[2].TCPOutcome, r.Targets[2].Stage, r.Targets[2].Error = "refused", "connect", "dial error"
		}, want: passSummary{TCPRefused: 1}},
		{name: "retiring tcp timeout", change: func(targets []TargetInfo, r *probe.Result) {
			targets[2].Deleting = true
			r.Targets[2].TCPOutcome, r.Targets[2].Stage, r.Targets[2].Error = "timeout", "connect", "dial error"
		}, want: passSummary{TCPTimeout: 1, RetiredFailed: 1}},
		{name: "retiring tcp bind error", change: func(targets []TargetInfo, r *probe.Result) {
			targets[2].Deleting = true
			r.Targets[2].TCPOutcome, r.Targets[2].Stage, r.Targets[2].Error = "bindRoutingError", "connect", "dial error"
		}, want: passSummary{TCPFailed: 1, RetiredFailed: 1}},
		{name: "dns empty A", change: func(_ []TargetInfo, r *probe.Result) { r.DNS[0].Answers = nil }, want: passSummary{DNSFailed: 1}},
		{name: "dns error", change: func(_ []TargetInfo, r *probe.Result) { r.DNS[0].RCode, r.DNS[0].Error = 3, "NXDOMAIN" }, want: passSummary{DNSFailed: 1}},
		{name: "evidence errors", change: func(_ []TargetInfo, r *probe.Result) { r.Before.Errors = map[string]string{"routes": "unavailable"} }, want: passSummary{EvidenceErrors: 1}},
		{name: "truncated", change: func(_ []TargetInfo, r *probe.Result) { r.After.Truncated = []string{"bounded"} }, want: passSummary{Truncated: 1}},
		{name: "sparse result", change: func(_ []TargetInfo, r *probe.Result) { *r = probe.Result{} }, invalid: true},
		{name: "wrong namespace device", change: func(_ []TargetInfo, r *probe.Result) { r.NamespaceDevice++ }, invalid: true},
		{name: "wrong namespace inode", change: func(_ []TargetInfo, r *probe.Result) { r.NamespaceInode++ }, invalid: true},
		{name: "wrong target", change: func(_ []TargetInfo, r *probe.Result) { r.Targets[0].Target.ID = "other" }, invalid: true},
		{name: "wrong target info", change: func(targets []TargetInfo, _ *probe.Result) { targets[0].Target.ID = "other" }, invalid: true},
		{name: "missing target", change: func(_ []TargetInfo, r *probe.Result) { r.Targets = r.Targets[1:] }, invalid: true},
		{name: "missing dns", change: func(_ []TargetInfo, r *probe.Result) { r.DNS = nil }, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := probe.Request{NamespaceDevice: 1, NamespaceInode: 2, DNS: controllerSandbox().DNS, DNSNames: []string{"service.ns.svc."},
				Targets: []probe.Target{{ID: "kas", Role: "kas-service"}, {ID: "endpoint", Role: "ignition-server-endpoint"}, {ID: "worker", Role: "worker-outbound", TCPOnly: true}}}
			targets := []TargetInfo{{Target: request.Targets[0]}, {Target: request.Targets[1]}, {Target: request.Targets[2]}}
			result := successfulResult(request)
			if tc.change != nil {
				tc.change(targets, &result)
			}
			var summary passSummary
			if valid := summarizeResult(request, targets, result, &summary); valid == tc.invalid {
				t.Fatalf("valid=%t, want %t", valid, !tc.invalid)
			}
			tc.want.Reported, tc.want.DNSReported = len(result.Targets), len(result.DNS)
			if !tc.invalid {
				tc.want.HTTPSuccess = 2 - tc.want.HTTPFailed
				tc.want.TCPConnected = 1 - tc.want.TCPRefused - tc.want.TCPTimeout - tc.want.TCPFailed
				tc.want.DNSSuccess = 4 - tc.want.DNSFailed
			}
			if !reflect.DeepEqual(summary, tc.want) {
				t.Fatalf("summary=%+v, want %+v", summary, tc.want)
			}
		})
	}
}

func loggedPass(t *testing.T, output *bytes.Buffer, pod *corev1.Pod) (passSummary, map[string]json.RawMessage) {
	t.Helper()
	fields := map[string]json.RawMessage{}
	summaries := 0
	for _, line := range bytes.Split(bytes.TrimSpace(output.Bytes()), []byte{'\n'}) {
		var entry struct {
			Field  string          `json:"field"`
			Record json.RawMessage `json:"record"`
			UID    string          `json:"pod_uid"`
		}
		if err := json.Unmarshal(line, &entry); err != nil || len(line) > 65536 || entry.UID != string(pod.UID) {
			t.Fatalf("invalid/oversized record or missing identity: %s, %v", line, err)
		}
		fields[entry.Field] = entry.Record
		if entry.Field == "summary" {
			summaries++
		}
	}
	if summaries != 1 || fields["start"] != nil || fields["finish"] != nil {
		t.Fatalf("incorrect lifecycle logs: %s", output)
	}
	var summary passSummary
	if err := json.Unmarshal(fields["summary"], &summary); err != nil {
		t.Fatal(err)
	}
	return summary, fields
}

func TestPassSummaryAndFailureDiagnostics(t *testing.T) {
	for _, scenario := range []string{"healthy", "mixed failure", "advisory", "partial", "helper failure", "helper timeout", "canceled"} {
		t.Run(scenario, func(t *testing.T) {
			pod := routerPod("router", "uid", "node", "10.0.0.1")
			pod.CreationTimestamp = metav1.NewTime(time.Now().Add(-time.Hour))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			ctrl := cachedController(t, pod, runtimeFunc(func(context.Context, *corev1.Pod) (Sandbox, error) { return controllerSandbox(), nil }),
				func(_ context.Context, _ string, request probe.Request) (json.RawMessage, error) {
					calls++
					if request.TrustBundles[probe.TrustIgnition] != "ignition-public-ca" || request.TrustBundles[probe.TrustRoot] != "root-public-ca" {
						t.Fatal("controller did not pass public CA bundles to helper")
					}
					switch scenario {
					case "helper failure":
						return nil, errors.New("private helper text")
					case "helper timeout":
						return nil, context.DeadlineExceeded
					case "canceled":
						cancel()
						return nil, context.Canceled
					}
					result := successfulResult(request)
					result.Before.State = map[string]any{"marker": "raw-evidence-marker"}
					if scenario == "mixed failure" || scenario == "advisory" {
						for i := range result.Targets {
							if strings.HasSuffix(result.Targets[i].Target.Role, "-endpoint") {
								result.Targets[i].Error = "timeout"
							}
							if scenario == "mixed failure" && result.Targets[i].Target.Role == "kas-service" {
								result.Targets[i].HTTPStatus, result.Targets[i].Expected200 = 500, false
								result.Targets[i].Liveness = &probe.Observation{Stage: "complete", HTTPStatus: 200, Expected200: true, TLSVerify: true, TLSVerified: true}
							}
						}
					}
					if scenario == "partial" {
						result.Truncated = []string{"bounded"}
						result.Before.Errors = map[string]string{"routes": "unavailable"}
					}
					return json.Marshal(result)
				})
			client, dynamicClient := controllerClients(pod)
			if scenario == "healthy" {
				ctrl.discovery = testDiscoverySources(t, client, dynamicClient)
			} else {
				input := discoveryInput(t)
				input.Pod, input.Peers = pod, nil
				for _, service := range input.Services {
					service.Slices[0].Endpoints[0].Conditions.Terminating = ptr.To(true)
				}
				ctrl.discovery = cachedDiscoveryInput(t, input)
			}
			clock := clocktesting.NewFakeClock(time.Now())
			ctrl.clock = clock
			ctrl.cooldown.SetClock(clock)
			var output bytes.Buffer
			ctx = utils.ContextWithLogger(ctx, keyFor(pod).AddLoggerValues(logr.FromSlogHandler(slog.NewJSONHandler(&output, nil))))
			delay, passErr := ctrl.sync(ctx, keyFor(pod))
			if passErr != nil && (delay != 0 || ctrl.cooldown.TimeUntilReady(keyFor(pod)) != 0) {
				t.Fatalf("failed pass set cooldown: delay=%v err=%v", delay, passErr)
			}
			for _, marker := range []string{"ignition-public-ca", "root-public-ca", "private-key-must-not-log", "private helper text"} {
				if bytes.Contains(output.Bytes(), []byte(marker)) {
					t.Fatal("private material leaked into controller logs")
				}
			}
			summary, fields := loggedPass(t, &output, pod)
			if summary.CadenceSeconds != 300 || summary.AgeSeconds == nil || *summary.AgeSeconds < 3600 {
				t.Fatalf("missing cadence/age: %+v", summary)
			}
			outcome := map[string]string{"healthy": "healthy", "advisory": "advisory", "partial": "partial", "canceled": "canceled"}[scenario]
			if outcome == "" {
				outcome = "failed"
			}
			if summary.Outcome != outcome || (passErr != nil) != (outcome == "failed" || outcome == "canceled") {
				t.Fatalf("unexpected outcome: %+v err=%v", summary, passErr)
			}
			if outcome != "failed" {
				if len(fields) != 1 || bytes.Contains(output.Bytes(), []byte("raw-evidence-marker")) {
					t.Fatalf("compact pass leaked diagnostics: %s", &output)
				}
			} else if fields["plan"] == nil || fields["coverage"] == nil {
				t.Fatalf("failure missing diagnostics: %s", &output)
			}
			switch scenario {
			case "healthy":
				if summary.HTTPSuccess != 13 || summary.DNSSuccess != 12 || summary.HTTPFailed != 0 || summary.DNSFailed != 0 {
					t.Fatalf("healthy counts: %+v", summary)
				}
			case "mixed failure", "advisory":
				wantFailed := 3
				if scenario == "mixed failure" {
					wantFailed++
				}
				if summary.RetiredFailed != 3 || summary.HTTPFailed != wantFailed {
					t.Fatalf("retiring failure counts: %+v", summary)
				}
				if scenario == "mixed failure" {
					var result probe.Result
					if err := json.Unmarshal(fields["result"], &result); err != nil || len(result.Targets) != summary.Reported {
						t.Fatalf("missing full diagnostics: %+v err=%v", result, err)
					}
					retired, live := 0, 0
					for _, observation := range result.Targets {
						if strings.HasSuffix(observation.Target.Role, "-endpoint") && observation.Error == "timeout" {
							retired++
						}
						if observation.Target.Role == "kas-service" && observation.HTTPStatus == 500 && !observation.Expected200 && observation.Liveness != nil && observation.Liveness.HTTPStatus == 200 {
							live++
						}
					}
					if retired != 3 || live != 1 {
						t.Fatal("retiring failures or nested KAS liveness omitted from diagnostics")
					}
				}
			case "partial":
				if !summary.Partial || summary.Truncated != 1 || summary.EvidenceErrors != 1 || summary.Discovered != 14 || summary.Submitted != 14 || summary.Reported != 14 || summary.Omitted != 0 {
					t.Fatalf("partial coverage: %+v", summary)
				}
			case "helper failure", "helper timeout", "canceled":
				want := map[string]string{"helper failure": "probe_execution_failed", "helper timeout": "probe_timeout", "canceled": "probe_canceled"}[scenario]
				if summary.Unavailable != want || summary.Canceled != (scenario == "canceled") {
					t.Fatalf("helper classification: %+v", summary)
				}
			}
			if scenario == "healthy" {
				service, err := client.CoreV1().Services("ns").Get(ctx, "ignition-server", metav1.GetOptions{})
				if err != nil {
					t.Fatal(err)
				}
				service.Spec.ClusterIP = "172.16.0.99"
				if _, err := client.CoreV1().Services("ns").Update(ctx, service, metav1.UpdateOptions{}); err != nil {
					t.Fatal(err)
				}
				if err := wait.PollUntilContextTimeout(ctx, time.Millisecond, time.Second, true, func(ctx context.Context) (bool, error) {
					cached, err := ctrl.discovery.services.Lister().Services("ns").Get(service.Name)
					return err == nil && cached.Spec.ClusterIP == service.Spec.ClusterIP, err
				}); err != nil {
					t.Fatal(err)
				}
				output.Reset()
				wantDelay := ctrl.cooldown.TimeUntilReady(keyFor(pod))
				if delay, err := ctrl.sync(ctx, keyFor(pod)); err != nil || delay != wantDelay || calls != 1 || ctrl.queue.Len() != 0 || output.Len() != 0 {
					t.Fatalf("cache update bypassed cadence: delay=%v err=%v calls=%d", delay, err, calls)
				}
				client.ClearActions()
				dynamicClient.ClearActions()
				clock.Step(wantDelay)
				if delay, err := ctrl.sync(ctx, keyFor(pod)); err != nil || delay < 5*time.Minute || delay > 6*time.Minute {
					t.Fatalf("repeat cached pass: delay=%v err=%v", delay, err)
				}
				if calls != 2 || len(client.Actions()) != 0 || len(dynamicClient.Actions()) != 0 {
					t.Fatalf("repeat pass did not use cache: calls=%d actions=%v dynamic=%v", calls, client.Actions(), dynamicClient.Actions())
				}
			}
		})
	}
}

func TestAgeBasedCadenceFromCompletion(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name     string
		age      time.Duration
		missing  bool
		interval time.Duration
	}{
		{"below", 10*time.Minute - time.Nanosecond, false, 30 * time.Second},
		{"at", 10 * time.Minute, false, 5 * time.Minute},
		{"above", 10*time.Minute + time.Nanosecond, false, 5 * time.Minute},
		{"missing", 0, true, 5 * time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pod := routerPod("router", "uid", "node", "10.0.0.1")
			if !tc.missing {
				pod.CreationTimestamp = metav1.NewTime(now.Add(-tc.age))
			}
			if got := probeCadence(pod, now); got != tc.interval {
				t.Fatalf("cadence %v, want %v", got, tc.interval)
			}
			clock := clocktesting.NewFakeClock(now.Add(-time.Second))
			calls := 0
			ctrl := cachedController(t, pod, runtimeFunc(func(context.Context, *corev1.Pod) (Sandbox, error) { return controllerSandbox(), nil }),
				func(_ context.Context, _ string, request probe.Request) (json.RawMessage, error) {
					calls++
					clock.SetTime(now)
					return json.Marshal(successfulResult(request))
				})
			ctrl.clock = clock
			ctrl.cooldown.SetClock(clock)
			ctx := utils.ContextWithLogger(t.Context(), logr.Discard())
			delay, err := ctrl.sync(ctx, keyFor(pod))
			if err != nil || calls != 1 || delay < tc.interval || delay > tc.interval*12/10 || ctrl.cooldown.TimeUntilReady(keyFor(pod)) != delay {
				t.Fatalf("completion cadence: calls=%d delay=%v interval=%v err=%v", calls, delay, tc.interval, err)
			}
		})
	}
}

func TestRuntimeMonitorStopReason(t *testing.T) {
	for _, scenario := range []string{"runtime error", "sandbox replacement", "shutdown", "revalidation error", "revalidation replacement"} {
		t.Run(scenario, func(t *testing.T) {
			pod := routerPod("router", "uid", "node", "10.0.0.1")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			monitorEntered := make(chan struct{})
			cause := errors.New("private CRI response")
			var runtimeCalls, helperCalls atomic.Int32
			ctrl := cachedController(t, pod,
				runtimeFunc(func(ctx context.Context, _ *corev1.Pod) (Sandbox, error) {
					call := runtimeCalls.Add(1)
					sandbox := controllerSandbox()
					if call == 2 && strings.HasPrefix(scenario, "revalidation") {
						if scenario == "revalidation error" {
							return Sandbox{}, &RuntimeError{Reason: SandboxStatusUnavailable, Err: cause}
						}
						sandbox.ID = "replacement"
					}
					if call == 3 {
						close(monitorEntered)
						switch scenario {
						case "runtime error":
							return Sandbox{}, &RuntimeError{Reason: SandboxStatusUnavailable, Err: cause}
						case "sandbox replacement":
							sandbox.Inode++
						case "shutdown":
							// Interrupted CRI calls are shutdown, not monitor failures.
							<-ctx.Done()
							return Sandbox{}, ctx.Err()
						}
					}
					return sandbox, nil
				}), func(ctx context.Context, _ string, request probe.Request) (json.RawMessage, error) {
					if helperCalls.Add(1) > 1 {
						return json.Marshal(successfulResult(request))
					}
					<-ctx.Done()
					return nil, ctx.Err()
				})
			var output bytes.Buffer
			ctx = utils.ContextWithLogger(ctx, keyFor(pod).AddLoggerValues(logr.FromSlogHandler(slog.NewJSONHandler(&output, nil))))
			done := make(chan struct{})
			var passErr error
			go func() {
				defer close(done)
				var delay time.Duration
				delay, passErr = ctrl.sync(ctx, keyFor(pod))
				if passErr == nil || delay != 0 {
					t.Errorf("interrupted pass: delay=%v err=%v", delay, passErr)
				}
			}()
			defer func() { cancel(); <-done }()
			if !strings.HasPrefix(scenario, "revalidation") {
				select {
				case <-monitorEntered:
				case <-time.After(5 * time.Second):
					t.Fatal("runtime monitor did not check the running helper")
				}
			}
			if scenario == "shutdown" {
				cancel()
			}
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("helper cancellation did not finish pass")
			}
			summary, fields := loggedPass(t, &output, pod)
			if bytes.Contains(output.Bytes(), []byte("private CRI response")) {
				t.Fatalf("unsafe summary: %s", &output)
			}
			if strings.HasSuffix(scenario, "error") {
				if !errors.Is(passErr, cause) {
					t.Fatalf("runtime cause lost: %v", passErr)
				}
				if summary.Outcome != "failed" || summary.Canceled || summary.Unavailable != string(SandboxStatusUnavailable) || fields["unavailable"] == nil || fields["plan"] == nil || fields["coverage"] == nil || fields["target"] == nil {
					t.Fatalf("runtime error hidden by cancellation: %+v fields=%v", summary, fields)
				}
			} else {
				reason, wantErr := "probe_canceled", context.Canceled
				if strings.HasSuffix(scenario, "replacement") {
					reason, wantErr = "sandbox_changed", errSandboxChanged
				}
				if !errors.Is(passErr, wantErr) || summary.Outcome != "canceled" || !summary.Canceled || summary.Unavailable != reason || len(fields) != 1 {
					t.Fatalf("expected compact cancellation: summary=%+v fields=%v err=%v", summary, fields, passErr)
				}
			}
			if scenario == "runtime error" {
				if ctrl.cooldown.TimeUntilReady(keyFor(pod)) != 0 {
					t.Fatal("runtime failure set cooldown")
				}
				if delay, err := ctrl.sync(ctx, keyFor(pod)); err != nil || delay < 5*time.Minute || delay > 6*time.Minute || helperCalls.Load() != 2 {
					t.Fatalf("runtime recovery pass: delay=%v err=%v calls=%d", delay, err, helperCalls.Load())
				}
			}
		})
	}
}

func TestHTTPSummaryRequiresVerifiedTLS(t *testing.T) {
	for _, verify := range []bool{false, true} {
		for _, verified := range []bool{false, true} {
			request := probe.Request{Targets: []probe.Target{{ID: "https", TrustBundle: probe.TrustIgnition}, {ID: "ready", PlainHTTP: true}, {ID: "worker", TCPOnly: true}}}
			result := successfulResult(request)
			result.Targets[0].TLSVerify, result.Targets[0].TLSVerified = verify, verified
			summary := passSummary{}
			if !summarizeResult(request, []TargetInfo{{Target: request.Targets[0]}, {Target: request.Targets[1]}, {Target: request.Targets[2]}}, result, &summary) {
				t.Fatal("valid result rejected")
			}
			want := 1
			if verify && verified {
				want = 2
			}
			if summary.HTTPSuccess != want || summary.HTTPFailed != 2-want || summary.TCPConnected != 1 {
				t.Fatalf("verify=%t verified=%t: %+v", verify, verified, summary)
			}
		}
	}
}
