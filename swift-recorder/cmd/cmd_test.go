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

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/go-logr/logr"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/client-go/util/workqueue"

	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/swift-recorder/pkg/recorder"
	"github.com/Azure/ARO-HCP/swift-recorder/pkg/testutil"
)

func testOptions(t *testing.T) RawOptions {
	t.Helper()
	dir := testutil.ResolvedTempDir(t)
	return RawOptions{
		Config: recorder.Config{
			NodeName: "node", ClusterName: "cluster", Region: "region", Environment: "test", BootID: "boot",
			NetNSDir: dir, Executable: "fake-capture", CaptureMode: "slow",
			StartupDwell: time.Second, PostSuccessCapture: time.Second, SampleInterval: 100 * time.Millisecond,
			CaptureTimeout: time.Second, EpisodeTimeout: time.Minute,
			MaxPods: 2, MaxRecordBytes: 2048, MaxBufferBytes: 8192,
		},
		HealthAddress: "127.0.0.1:0", CNILog: filepath.Join(dir, "azure-vnet.log"), BootIDFile: filepath.Join(dir, "boot-id"),
	}
}

func TestControllerFlags(t *testing.T) {
	t.Setenv("NODE_NAME", "node-from-env")
	root := NewRootCmd()
	command, _, err := root.Find([]string{"controller"})
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"node-name": "node-from-env", "cluster-name": "", "region": "", "environment": "",
		"health-address": ":8091", "kubeconfig": "", "cni-log": "/host/var/log/azure-vnet.log",
		"netns-dir": "/var/run/netns", "boot-id-file": "/host/boot-id", "capture-mode": "slow",
		"startup-dwell": "30s", "post-success-capture": "10s", "sample-interval": "1s",
		"capture-timeout": "500ms", "episode-timeout": "15m0s", "max-pods": "32",
		"max-buffer-bytes": "16777216", "max-record-bytes": "65536", "log-verbosity": "0",
	} {
		if flag := command.Flags().Lookup(name); flag == nil || flag.DefValue != want {
			t.Errorf("flag %s = %v, want default %q", name, flag, want)
		}
	}
	if err := command.Args(command, []string{"unexpected"}); err == nil {
		t.Error("controller accepted positional arguments")
	}
	helper, _, err := root.Find([]string{"capture"})
	if err != nil || !helper.Hidden || helper.Flags().Lookup("path") == nil {
		t.Fatalf("hidden capture helper not registered: command=%v, error=%v", helper, err)
	}

	for _, tc := range []struct {
		name, flag, want string
	}{
		{"required node", "--node-name=", "are required"},
		{"required cluster", "--cluster-name=", "are required"},
		{"required region", "--region=", "are required"},
		{"required environment", "--environment=", "are required"},
		{"mode", "--capture-mode=other", "capture-mode must be"},
		{"timing", "--sample-interval=99ms", "timing budgets"},
		{"memory", "--max-pods=257", "memory budgets"},
		{"verbosity", "--log-verbosity=-1", "log verbosity"},
		{"health", "--health-address=", "health address"},
		{"path", "--cni-log=relative", "paths must be absolute"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := NewRootCmd()
			root.SetArgs([]string{"controller", "--cluster-name=cluster", "--region=region", "--environment=test", tc.flag})
			if err := root.ExecuteContext(t.Context()); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("flag validation error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	base := testOptions(t)
	for _, tc := range []struct {
		name   string
		change func(*RawOptions)
		valid  bool
	}{
		{"defaults", func(*RawOptions) {}, true},
		{"all mode", func(o *RawOptions) { o.CaptureMode = "all" }, true},
		{"no post success capture", func(o *RawOptions) { o.PostSuccessCapture = 0 }, true},
		{"maximum timeout", func(o *RawOptions) { o.CaptureTimeout = 2 * time.Second }, true},
		{"minimum memory", func(o *RawOptions) { o.MaxBufferBytes = o.MaxPods * o.MaxRecordBytes }, true},
		{"maximum memory", func(o *RawOptions) {
			o.MaxPods = 256
			o.MaxRecordBytes = 1024 * 1024
			o.MaxBufferBytes = 256 * 1024 * 1024
		}, true},
		{"node", func(o *RawOptions) { o.NodeName = "" }, false},
		{"cluster", func(o *RawOptions) { o.ClusterName = "" }, false},
		{"region", func(o *RawOptions) { o.Region = "" }, false},
		{"environment", func(o *RawOptions) { o.Environment = "" }, false},
		{"mode", func(o *RawOptions) { o.CaptureMode = "ALL" }, false},
		{"zero dwell", func(o *RawOptions) { o.StartupDwell = 0 }, false},
		{"negative dwell", func(o *RawOptions) { o.StartupDwell = -time.Second }, false},
		{"negative post success", func(o *RawOptions) { o.PostSuccessCapture = -time.Second }, false},
		{"short interval", func(o *RawOptions) { o.SampleInterval = 100*time.Millisecond - 1 }, false},
		{"zero timeout", func(o *RawOptions) { o.CaptureTimeout = 0 }, false},
		{"long timeout", func(o *RawOptions) { o.CaptureTimeout = 2*time.Second + 1 }, false},
		{"episode too short", func(o *RawOptions) { o.EpisodeTimeout = o.PostSuccessCapture }, false},
		{"zero pods", func(o *RawOptions) { o.MaxPods = 0 }, false},
		{"too many pods", func(o *RawOptions) { o.MaxPods = 257 }, false},
		{"small record", func(o *RawOptions) { o.MaxRecordBytes = 1023 }, false},
		{"large record", func(o *RawOptions) { o.MaxRecordBytes = 1024*1024 + 1 }, false},
		{"small buffer", func(o *RawOptions) { o.MaxBufferBytes = o.MaxPods*o.MaxRecordBytes - 1 }, false},
		{"large buffer", func(o *RawOptions) { o.MaxBufferBytes = 256*1024*1024 + 1 }, false},
		{"negative verbosity", func(o *RawOptions) { o.LogVerbosity = -1 }, false},
		{"empty health address", func(o *RawOptions) { o.HealthAddress = "" }, false},
		{"relative cni log", func(o *RawOptions) { o.CNILog = "relative" }, false},
		{"relative netns", func(o *RawOptions) { o.NetNSDir = "relative" }, false},
		{"relative boot id", func(o *RawOptions) { o.BootIDFile = "relative" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := base
			tc.change(&o)
			validated, err := o.Validate()
			if (err == nil) != tc.valid || (validated != nil) != tc.valid {
				t.Fatalf("Validate() = %v, %v; want valid=%v", validated, err, tc.valid)
			}
			if tc.valid {
				if validated.options != o {
					t.Error("validation changed options")
				}
				o.NodeName = "changed"
				if validated.options.NodeName != base.NodeName {
					t.Error("validated options did not retain an independent copy")
				}
			}
		})
	}
}

func TestMuxReadinessAndMetrics(t *testing.T) {
	var ready atomic.Bool
	mux := newMux(ready.Load)
	for _, state := range []bool{false, true, false} {
		ready.Store(state)
		for _, path := range []string{"/healthz", "/readyz"} {
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
			want := http.StatusOK
			if path == "/readyz" && !state {
				want = http.StatusServiceUnavailable
			}
			if response.Code != want {
				t.Errorf("%s with ready=%v: status=%d, want %d", path, state, response.Code, want)
			}
		}
	}

	// Do not register a provider here: the command's clientgo import must do it.
	queue := workqueue.NewTypedWithConfig[string](workqueue.TypedQueueConfig[string]{Name: "synthetic"})
	defer queue.ShutDown()
	queue.Add("pod")
	defer func() {
		// Drain the gauge as metrics survive repeated tests in the same process.
		key, _ := queue.Get()
		queue.Done(key)
	}()
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "workqueue_depth{name=\"synthetic\"} 1\n") {
		t.Fatalf("metrics handler did not expose synthetic workqueue depth 1: status=%d", response.Code)
	}
}

func TestComplete(t *testing.T) {
	for _, kind := range []string{"valid", "missing boot id", "empty boot id", "missing netns", "invalid kubeconfig"} {
		t.Run(kind, func(t *testing.T) {
			defer checkRecorderGoroutines(t)()
			o := testOptions(t)
			o.Kubeconfig = filepath.Join(o.NetNSDir, "kubeconfig")
			if err := os.WriteFile(o.Kubeconfig, []byte(`apiVersion: v1
kind: Config
clusters:
- name: test
  cluster:
    server: http://127.0.0.1:1
contexts:
- name: test
  context:
    cluster: test
current-context: test
`), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(o.BootIDFile, []byte("  host-boot-id\n"), 0600); err != nil {
				t.Fatal(err)
			}
			wantError := ""
			switch kind {
			case "missing boot id":
				o.BootIDFile += "-missing"
				wantError = "read host boot ID"
			case "empty boot id":
				if err := os.WriteFile(o.BootIDFile, []byte(" \n\t"), 0600); err != nil {
					t.Fatal(err)
				}
				wantError = "empty host boot ID"
			case "missing netns":
				o.NetNSDir = filepath.Join(o.NetNSDir, "missing")
				wantError = "resolve mounted namespace directory"
			case "invalid kubeconfig":
				o.Kubeconfig += "-missing"
				wantError = "build Kubernetes config"
			}
			validated, err := o.Validate()
			if err != nil {
				t.Fatal(err)
			}
			ctx := utils.ContextWithLogger(t.Context(), logr.Discard())
			completed, err := validated.Complete(ctx)
			if wantError != "" {
				if err == nil || !strings.Contains(err.Error(), wantError) || completed != nil {
					t.Fatalf("Complete() = %v, %v; want %q", completed, err, wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				ctx, cancel := context.WithCancel(ctx)
				cancel()
				_ = completed.controller.Run(ctx)
			}()
			if completed.options.BootID != "host-boot-id" || !filepath.IsAbs(completed.options.Executable) || completed.factory == nil || completed.controller == nil {
				t.Fatalf("incomplete runtime configuration: %+v", completed)
			}
			if validated.options != o {
				t.Error("Complete mutated validated options")
			}
		})
	}
}

// Track owned goroutines by ID, not total counts, so unrelated runtime activity
// cannot mask a leaked controller, informer, tailer or workqueue.
func checkRecorderGoroutines(t *testing.T) func() {
	t.Helper()
	stacks := func() map[string]string {
		buf := make([]byte, 2*1024*1024)
		n := runtime.Stack(buf, true)
		if n == len(buf) {
			t.Fatal("goroutine stack buffer exhausted")
		}
		result := map[string]string{}
		for _, stack := range strings.Split(string(buf[:n]), "\n\n") {
			id, _, _ := strings.Cut(stack, "\n")
			// Strip the state, which changes while a goroutine is shutting down.
			id, _, _ = strings.Cut(id, " [")
			result[id] = stack
		}
		return result
	}
	before := stacks()
	return func() {
		t.Helper()
		var leaked []string
		err := wait.PollUntilContextTimeout(context.Background(), 10*time.Millisecond, 5*time.Second, true, func(context.Context) (bool, error) {
			leaked = nil
			for id, stack := range stacks() {
				if _, existed := before[id]; existed {
					continue
				}
				for _, owner := range []string{"/swift-recorder/", "/client-go/util/workqueue.", "/client-go/tools/cache.", "/client-go/informers."} {
					if strings.Contains(stack, owner) {
						leaked = append(leaked, stack)
						break
					}
				}
			}
			return len(leaked) == 0, nil
		})
		if err != nil {
			t.Errorf("recorder goroutines remain after Run returned:\n%s", strings.Join(leaked, "\n\n"))
		}
	}
}

func healthAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func waitHTTPStatus(t *testing.T, client *http.Client, address, path string, want int) {
	t.Helper()
	var status int
	err := wait.PollUntilContextTimeout(t.Context(), 10*time.Millisecond, 5*time.Second, true, func(ctx context.Context) (bool, error) {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+path, nil)
		if err != nil {
			return false, err
		}
		response, err := client.Do(request)
		if err != nil {
			return false, nil
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		status = response.StatusCode
		return status == want, nil
	})
	if err != nil {
		t.Fatalf("%s never returned %d (last status %d): %v", path, want, status, err)
	}
}

func TestRun(t *testing.T) {
	for _, kind := range []string{"existing log", "missing log", "cancel before cache sync"} {
		t.Run(kind, func(t *testing.T) {
			defer checkRecorderGoroutines(t)()
			o := testOptions(t)
			o.HealthAddress = healthAddress(t)
			if kind != "missing log" {
				if err := os.WriteFile(o.CNILog, nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "pod", UID: "uid"},
				Spec: corev1.PodSpec{NodeName: o.NodeName, Containers: []corev1.Container{{Name: "main", Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{"aro.openshift.io/swift-nic": resource.MustParse("1")},
				}}}},
				Status: corev1.PodStatus{Phase: corev1.PodPending, Conditions: []corev1.PodCondition{{Type: corev1.PodReadyToStartContainers, Status: corev1.ConditionFalse}}},
			}
			client := fake.NewClientset(pod)
			listStarted, releaseList := make(chan struct{}), make(chan struct{})
			var listOnce, releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(releaseList) }) }
			client.PrependReactor("list", "pods", func(clienttesting.Action) (bool, k8sruntime.Object, error) {
				listOnce.Do(func() { close(listStarted) })
				<-releaseList
				return false, nil, nil
			})
			factory := informers.NewSharedInformerFactory(client, 0)
			captured := make(chan string, 1)
			ctrl, err := recorder.New(o.Config, factory.Core().V1().Pods(), func(ctx context.Context, executable, path string, limit int) (json.RawMessage, error) {
				if executable != o.Executable || limit != o.MaxRecordBytes {
					t.Errorf("capture arguments: executable=%q, limit=%d", executable, limit)
				}
				if _, ok := ctx.Deadline(); !ok {
					t.Error("capture missing deadline")
				}
				select {
				case captured <- path:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				<-ctx.Done()
				return nil, ctx.Err()
			})
			if err != nil {
				t.Fatal(err)
			}
			completed := &CompletedOptions{options: o, factory: factory, controller: ctrl}
			ctx, cancel := context.WithCancel(utils.ContextWithLogger(t.Context(), logr.Discard()))
			done := make(chan struct{})
			var runErr error
			go func() { defer close(done); runErr = completed.Run(ctx) }()
			defer func() {
				cancel()
				release()
				select {
				case <-done:
					if runErr != nil {
						t.Errorf("Run cancellation returned %v", runErr)
					}
				case <-time.After(5 * time.Second):
					t.Error("Run did not stop on cancellation")
				}
			}()
			httpClient := &http.Client{Timeout: time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
			defer httpClient.CloseIdleConnections()
			select {
			case <-listStarted:
			case <-time.After(5 * time.Second):
				t.Fatal("pod informer did not start")
			}
			waitHTTPStatus(t, httpClient, o.HealthAddress, "/healthz", http.StatusOK)
			waitHTTPStatus(t, httpClient, o.HealthAddress, "/readyz", http.StatusServiceUnavailable)
			if ctrl.Ready() {
				t.Fatal("controller ready before cache synchronization")
			}
			if kind == "cancel before cache sync" {
				cancel()
				release()
				return
			}
			release()
			waitHTTPStatus(t, httpClient, o.HealthAddress, "/readyz", http.StatusOK)
			path := filepath.Join(o.NetNSDir, "cni-sandbox")
			line, err := json.Marshal(map[string]string{
				"msg": "Processing ADD command", "containerId": strings.Repeat("a", 64), "netNS": path,
				"args": "K8S_POD_NAMESPACE=ns;K8S_POD_NAME=pod;K8S_POD_UID=uid",
			})
			if err != nil {
				t.Fatal(err)
			}
			file, err := os.OpenFile(o.CNILog, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
			if err != nil {
				t.Fatal(err)
			}
			_, writeErr := file.Write(append(line, '\n'))
			closeErr := file.Close()
			if writeErr != nil || closeErr != nil {
				t.Fatalf("append CNI log: %v, close: %v", writeErr, closeErr)
			}
			select {
			case got := <-captured:
				if got != path {
					t.Fatalf("captured namespace %q, want %q", got, path)
				}
			case <-done:
				t.Fatalf("Run exited before capture: %v", runErr)
			case <-time.After(5 * time.Second):
				t.Fatal("CNI attempt did not reach capture")
			}
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("Run did not cancel active capture")
			}
			if ctrl.Ready() {
				t.Error("controller remained ready after shutdown")
			}
			listener, err := net.Listen("tcp", o.HealthAddress)
			if err != nil {
				t.Fatalf("health listener not released: %v", err)
			}
			_ = listener.Close()
		})
	}
}

func TestRunStartupFailure(t *testing.T) {
	for _, kind := range []string{"bind", "tail initialization"} {
		t.Run(kind, func(t *testing.T) {
			check := checkRecorderGoroutines(t)
			o := testOptions(t)
			if kind == "bind" {
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer listener.Close()
				o.HealthAddress = listener.Addr().String()
			} else {
				o.CNILog = o.NetNSDir // A directory is an invalid tail input, even as root.
			}
			client := fake.NewClientset()
			factory := informers.NewSharedInformerFactory(client, 0)
			ctrl, err := recorder.New(o.Config, factory.Core().V1().Pods(), func(context.Context, string, string, int) (json.RawMessage, error) {
				t.Error("capture ran during startup failure")
				return json.RawMessage(`{}`), nil
			})
			if err != nil {
				t.Fatal(err)
			}
			// Clean up even if the bind-failure regression leaks the unstarted queue.
			defer func() {
				ctx, cancel := context.WithCancel(utils.ContextWithLogger(context.Background(), logr.Discard()))
				cancel()
				_ = ctrl.Run(ctx)
			}()
			defer check()
			completed := &CompletedOptions{options: o, factory: factory, controller: ctrl}
			ctx, cancel := context.WithCancel(utils.ContextWithLogger(t.Context(), logr.Discard()))
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- completed.Run(ctx) }()
			select {
			case err := <-done:
				if kind == "bind" {
					if !errors.Is(err, syscall.EADDRINUSE) {
						t.Fatalf("bind error = %v, want EADDRINUSE", err)
					}
					if actions := client.Actions(); len(actions) != 0 {
						t.Errorf("informer started before successful bind: %v", actions)
					}
				} else if err == nil || !strings.Contains(err.Error(), "CNI log must be a regular file") {
					t.Fatalf("tail initialization error = %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("startup failure did not stop Run")
			}
			if ctrl.Ready() {
				t.Error("controller ready after startup failure")
			}
		})
	}
}
