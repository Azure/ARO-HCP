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

//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
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
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestRecorderIntegration(t *testing.T) {
	if os.Getenv("SWIFT_RECORDER_TEST_INTEGRATION") != "1" {
		t.Skip("set SWIFT_RECORDER_TEST_INTEGRATION=1 for the rootless Linux binary integration test")
	}
	if os.Getenv("SWIFT_RECORDER_INTEGRATION_CHILD") == "1" {
		runRecorderIntegration(t)
		return
	}

	// Keep even the compiled binary outside the worktree, regardless of TMPDIR.
	dir, err := os.MkdirTemp("/tmp", "swift-recorder-integration-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	binary := filepath.Join(dir, "swift-recorder")
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	if output, err := exec.CommandContext(ctx, "go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build recorder: %v\n%s", err, output)
	}
	mountpoint := filepath.Join(dir, "sandbox")
	if err := os.Mkdir(mountpoint, 0700); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.CommandContext(ctx, executable, "-test.run=^TestRecorderIntegration$", "-test.v", "-test.timeout=45s")
	child.Env = append(os.Environ(), "SWIFT_RECORDER_INTEGRATION_CHILD=1", "SWIFT_RECORDER_INTEGRATION_DIR="+dir,
		"SWIFT_RECORDER_INTEGRATION_PARENT_NET="+integrationNamespaceID(t, "/proc/thread-self/ns/net"))
	child.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags:                 unix.CLONE_NEWUSER | unix.CLONE_NEWNS | unix.CLONE_NEWNET,
		UidMappings:                []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getuid(), Size: 1}},
		GidMappings:                []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getgid(), Size: 1}},
		GidMappingsEnableSetgroups: false,
		Setpgid:                    true,
	}
	// Include the recorder and any capture subprocess in timeout cleanup.
	child.Cancel = func() error { return syscall.Kill(-child.Process.Pid, syscall.SIGKILL) }
	child.WaitDelay = 3 * time.Second
	output, runErr := child.CombinedOutput()
	if child.Process != nil {
		_ = syscall.Kill(-child.Process.Pid, syscall.SIGKILL)
	}
	entries, err := os.ReadDir(mountpoint)
	if err != nil || len(entries) != 0 {
		t.Fatalf("sandbox mount or files escaped into parent: %v, %v", entries, err)
	}
	if runErr != nil {
		t.Fatalf("isolated integration: %v (requires rootless user/mount/network namespaces)\n%s", runErr, output)
	}
	t.Logf("isolated child:\n%s", output)
}

func integrationNamespaceID(t *testing.T, path string) string {
	t.Helper()
	var stat unix.Stat_t
	if err := unix.Stat(path, &stat); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%d:%d", stat.Dev, stat.Ino)
}

func runRecorderIntegration(t *testing.T) {
	dir := os.Getenv("SWIFT_RECORDER_INTEGRATION_DIR")
	sandbox := filepath.Join(dir, "sandbox")
	controllerNS := integrationNamespaceID(t, "/proc/thread-self/ns/net")
	if controllerNS == os.Getenv("SWIFT_RECORDER_INTEGRATION_PARENT_NET") || os.Getuid() != 0 {
		t.Fatal("helper was not isolated in a mapped-root network namespace")
	}
	if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); err != nil {
		t.Fatalf("make mounts private: %v", err)
	}
	if err := unix.Mount("tmpfs", sandbox, "tmpfs", unix.MS_NOSUID|unix.MS_NODEV, "size=16m,mode=0700"); err != nil {
		t.Fatalf("mount sandbox tmpfs: %v", err)
	}
	defer func() { _ = unix.Unmount(sandbox, unix.MNT_DETACH) }()

	// Only this new network namespace's loopback is brought up, for local HTTP.
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	ifr, err := unix.NewIfreq("lo")
	if err != nil {
		t.Fatal(err)
	}
	ifr.SetUint16(unix.IFF_UP)
	err = unix.IoctlIfreq(fd, unix.SIOCSIFFLAGS, ifr)
	_ = unix.Close(fd)
	if err != nil {
		t.Fatalf("bring isolated loopback up: %v", err)
	}
	netnsDir := filepath.Join(sandbox, "netns")
	if err := os.Mkdir(netnsDir, 0700); err != nil {
		t.Fatal(err)
	}
	netns := filepath.Join(netnsDir, "cni-integration")
	if err := os.WriteFile(netns, nil, 0600); err != nil {
		t.Fatal(err)
	}
	// Pin a SECOND network namespace, then restore this thread before running
	// HTTP or spawning the controller. Its down loopback distinguishes a real
	// setns capture from accidentally collecting the controller's own network.
	runtime.LockOSThread()
	original, err := unix.Open("/proc/thread-self/ns/net", unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Unshare(unix.CLONE_NEWNET); err != nil {
		t.Fatalf("create capture namespace: %v", err)
	}
	if err := unix.Mount("/proc/thread-self/ns/net", netns, "", unix.MS_BIND, ""); err != nil {
		t.Fatalf("pin capture namespace: %v", err)
	}
	if err := unix.Setns(original, unix.CLONE_NEWNET); err != nil {
		t.Fatalf("restore controller namespace: %v", err)
	}
	_ = unix.Close(original)
	runtime.UnlockOSThread()
	wantNS := integrationNamespaceID(t, netns)
	if wantNS == controllerNS || wantNS == os.Getenv("SWIFT_RECORDER_INTEGRATION_PARENT_NET") {
		t.Fatal("capture namespace is not separate from controller and parent")
	}

	// A small HTTP list/watch implementation, not a client-go fake: requests
	// exercise kubeconfig loading, selectors, informer decoding and watch RVs.
	var mu sync.Mutex
	var history []*corev1.Pod
	changed := make(chan struct{})
	watchStarted := make(chan struct{})
	var watchOnce sync.Once
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/pods" || r.URL.Query().Get("fieldSelector") != "spec.nodeName=integration-node" {
			t.Errorf("unexpected Kubernetes request: %s %s", r.Method, r.URL)
			http.Error(w, "only node-scoped pod list/watch is supported", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		encoder := json.NewEncoder(w)
		// Model an API server without streaming-list support. Modern client-go
		// must fall back to an actual LIST followed by WATCH, not a test fake.
		if r.URL.Query().Get("sendInitialEvents") == "true" {
			w.WriteHeader(http.StatusBadRequest)
			_ = encoder.Encode(metav1.Status{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Status"},
				Status: metav1.StatusFailure, Reason: metav1.StatusReasonBadRequest, Code: http.StatusBadRequest,
				Message: "sendInitialEvents is not supported by the integration API"})
			return
		}
		if r.URL.Query().Get("watch") != "true" {
			mu.Lock()
			defer mu.Unlock()
			list := corev1.PodList{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "PodList"},
				ListMeta: metav1.ListMeta{ResourceVersion: strconv.Itoa(len(history) + 1)}, Items: []corev1.Pod{}}
			if len(history) > 0 {
				list.Items = append(list.Items, *history[len(history)-1])
			}
			_ = encoder.Encode(list)
			return
		}
		rv, err := strconv.Atoi(r.URL.Query().Get("resourceVersion"))
		if err != nil || rv < 1 {
			t.Errorf("invalid watch resourceVersion: %s", r.URL)
			http.Error(w, "invalid resourceVersion", http.StatusBadRequest)
			return
		}
		w.(http.Flusher).Flush()
		watchOnce.Do(func() { close(watchStarted) })
		for {
			mu.Lock()
			pending := append([]*corev1.Pod(nil), history...)
			notify := changed
			mu.Unlock()
			for i, pod := range pending {
				if i+2 <= rv {
					continue
				}
				event := "MODIFIED"
				if i == 0 {
					event = "ADDED"
				}
				if err := encoder.Encode(map[string]any{"type": event, "object": pod}); err != nil {
					return
				}
				w.(http.Flusher).Flush()
				rv = i + 2
			}
			select {
			case <-r.Context().Done():
				return
			case <-notify:
			}
		}
	}))
	defer api.Close()
	publishPod := func(pod *corev1.Pod) {
		mu.Lock()
		defer mu.Unlock()
		pod = pod.DeepCopy()
		pod.ResourceVersion = strconv.Itoa(len(history) + 2)
		history = append(history, pod)
		close(changed)
		changed = make(chan struct{})
	}
	kubeconfig := filepath.Join(sandbox, "kubeconfig")
	cniLog := filepath.Join(sandbox, "azure-vnet.log")
	bootID := filepath.Join(sandbox, "boot-id")
	for path, data := range map[string]string{
		kubeconfig: fmt.Sprintf("apiVersion: v1\nkind: Config\nclusters:\n- name: fake\n  cluster:\n    server: %s\ncontexts:\n- name: fake\n  context:\n    cluster: fake\n    user: fake\ncurrent-context: fake\nusers:\n- name: fake\n  user: {}\n", api.URL),
		cniLog:     "", bootID: "integration-boot\n",
	} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// The CLI cannot inherit a listener. Reserve an ephemeral address in this
	// private network namespace; no unrelated host process can claim it.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	healthAddress := listener.Addr().String()
	_ = listener.Close()
	logPath := filepath.Join(sandbox, "recorder.log")
	logs, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer logs.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, filepath.Join(dir, "swift-recorder"), "controller",
		"--node-name=integration-node", "--cluster-name=integration-cluster", "--region=local", "--environment=test",
		"--capture-mode=all", "--kubeconfig="+kubeconfig, "--cni-log="+cniLog, "--netns-dir="+netnsDir,
		"--boot-id-file="+bootID, "--health-address="+healthAddress, "--startup-dwell=1s",
		"--sample-interval=100ms", "--capture-timeout=2s", "--post-success-capture=500ms", "--episode-timeout=20s")
	command.Stdout, command.Stderr = logs, logs
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	var exitErr error
	go func() { exitErr = command.Wait(); close(exited) }()
	defer func() {
		_ = command.Process.Signal(syscall.SIGTERM)
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
			_ = command.Process.Kill()
			<-exited
			t.Error("recorder did not shut down within five seconds")
		}
		if exitErr != nil {
			t.Errorf("recorder exit: %v", exitErr)
		}
		if t.Failed() {
			data, _ := os.ReadFile(logPath)
			t.Logf("recorder output:\n%s", data)
		}
	}()
	await := func(description string, check func() bool) {
		t.Helper()
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for {
			if check() {
				return
			}
			select {
			case <-ctx.Done():
				t.Fatalf("waiting for %s: %v", description, ctx.Err())
			case <-exited:
				t.Fatalf("recorder exited while waiting for %s: %v", description, exitErr)
			case <-ticker.C:
			}
		}
	}
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	get := func(path string) (string, bool) {
		response, err := client.Get("http://" + healthAddress + path)
		if err != nil {
			return "", false
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		return string(data), err == nil && response.StatusCode == http.StatusOK
	}
	await("/readyz and initial pod watch", func() bool {
		_, ready := get("/readyz")
		select {
		case <-watchStarted:
			return ready
		default:
			return false
		}
	})
	if _, ok := get("/healthz"); !ok {
		t.Fatal("/healthz failed after readiness")
	}
	pod := &corev1.Pod{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"},
		ObjectMeta: metav1.ObjectMeta{Namespace: "integration", Name: "swift-pod", UID: types.UID("integration-pod-uid"), CreationTimestamp: metav1.Now()},
		Spec: corev1.PodSpec{NodeName: "integration-node", Containers: []corev1.Container{{Name: "app", Image: "not-pulled",
			Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{"aro.openshift.io/swift-nic": resource.MustParse("1")}}}}},
		Status: corev1.PodStatus{Phase: corev1.PodPending, Conditions: []corev1.PodCondition{{Type: corev1.PodReadyToStartContainers, Status: corev1.ConditionFalse, LastTransitionTime: metav1.Now()}}},
	}
	publishPod(pod)
	sandboxID := strings.Repeat("a", 64)
	sourceTime := time.Now().UTC()
	add, err := os.OpenFile(cniLog, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	err = json.NewEncoder(add).Encode(map[string]string{
		"msg": "Processing ADD command", "containerId": sandboxID, "netNS": netns, "ts": sourceTime.Format(time.RFC3339Nano),
		"args": "K8S_POD_UID=" + string(pod.UID) + ";K8S_POD_NAME=" + pod.Name + ";K8S_POD_NAMESPACE=" + pod.Namespace,
	})
	_ = add.Close()
	if err != nil {
		t.Fatal(err)
	}
	readRecords := func() []integrationRecord {
		data, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatal(err)
		}
		var records []integrationRecord
		lines := bytes.Split(data, []byte("\n"))
		for _, line := range lines[:len(lines)-1] { // Ignore a write still in progress.
			var entry struct {
				Record *integrationRecord `json:"record"`
			}
			if err := json.Unmarshal(line, &entry); err != nil {
				t.Fatalf("non-JSON recorder output: %s (%v)", line, err)
			}
			if entry.Record != nil {
				records = append(records, *entry.Record)
			}
		}
		return records
	}
	await("successful subprocess namespace snapshot", func() bool {
		for _, record := range readRecords() {
			if record.Event == "snapshot" {
				return true
			}
		}
		return false
	})
	readyAt := time.Now()
	pod.Status.Phase = corev1.PodRunning
	pod.Status.Conditions = []corev1.PodCondition{
		{Type: corev1.PodReadyToStartContainers, Status: corev1.ConditionTrue, LastTransitionTime: metav1.NewTime(readyAt)},
		{Type: corev1.PodReady, Status: corev1.ConditionTrue, LastTransitionTime: metav1.NewTime(readyAt)},
	}
	publishPod(pod)
	await("recovered episode close", func() bool {
		for _, record := range readRecords() {
			if record.Event == "close" {
				return true
			}
		}
		return false
	})
	counts := map[string]int{}
	for _, record := range readRecords() {
		if counts["close"] != 0 || (record.Event != "open" && counts["open"] != 1) ||
			(record.Event == "recovery" && counts["snapshot"] == 0) || (record.Event == "close" && counts["recovery"] != 1) {
			t.Errorf("out-of-order lifecycle event %s after %v", record.Event, counts)
		}
		counts[record.Event]++
		if record.PodUID != string(pod.UID) {
			t.Errorf("record belongs to wrong pod: %+v", record)
		}
		switch record.Event {
		case "error":
			// The informer may observe the pod before the tailer sees its ADD.
			if record.Reason != "attempt_unavailable" || counts["snapshot"] != 0 {
				t.Errorf("unexpected capture failure: %+v", record)
			}
		case "snapshot":
			if record.SandboxID != sandboxID || record.NetNS != netns || !record.ADDSourceTime.Equal(sourceTime) || record.ADDObservedAt.IsZero() {
				t.Errorf("snapshot lost ADD identity/timing: %+v", record)
			}
			if record.Snapshot == nil {
				t.Fatal("snapshot record has no payload")
			}
			snapshot := record.Snapshot
			if snapshot.StartedAt.IsZero() || snapshot.FinishedAt.Before(snapshot.StartedAt) {
				t.Errorf("invalid capture timing: %+v", snapshot)
			}
			if id := fmt.Sprintf("%d:%d", snapshot.State.Namespace.Device, snapshot.State.Namespace.Inode); id != wantNS {
				t.Errorf("captured namespace %s, want pinned namespace %s", id, wantNS)
			}
			for _, name := range []string{"links", "addresses", "routes", "rules", "neighbors"} {
				section, ok := snapshot.State.State[name]
				if !ok || section.Entries == nil || section.Truncated {
					t.Errorf("missing or truncated %s: %+v", name, section)
				}
			}
			links := snapshot.State.State["links"].Entries
			if len(links) != 1 || links[0]["name"] != "lo" || links[0]["up"] != false {
				t.Errorf("expected only capture namespace's down loopback, got %v", links)
			}
		case "recovery", "close":
			if record.NetworkCondition != "True" || record.At.Before(readyAt) {
				t.Errorf("episode recovered before Ready watch update: %+v", record)
			}
			if record.Event == "close" && record.Reason != "recovered" {
				t.Errorf("wrong close reason: %s", record.Reason)
			}
		}
	}
	if counts["open"] != 1 || counts["snapshot"] < 1 || counts["recovery"] != 1 || counts["close"] != 1 {
		t.Fatalf("incomplete or duplicated episode lifecycle: %v", counts)
	}
	metrics, ok := get("/metrics")
	if !ok {
		t.Fatal("metrics scrape failed")
	}
	for sample, minimum := range map[string]float64{
		`swift_recorder_captures_total{outcome="success"}`:                           1,
		`swift_recorder_episodes_total{outcome="started"}`:                           1,
		`swift_recorder_episodes_total{outcome="published"}`:                         1,
		`swift_recorder_episodes_total{outcome="recovered"}`:                         1,
		`workqueue_depth{name="swift-startup-recorder"}`:                             0,
		`workqueue_adds_total{name="swift-startup-recorder"}`:                        1,
		`workqueue_queue_duration_seconds_count{name="swift-startup-recorder"}`:      1,
		`workqueue_work_duration_seconds_count{name="swift-startup-recorder"}`:       1,
		`workqueue_retries_total{name="swift-startup-recorder"}`:                     0,
		`workqueue_unfinished_work_seconds{name="swift-startup-recorder"}`:           0,
		`workqueue_longest_running_processor_seconds{name="swift-startup-recorder"}`: 0,
	} {
		found := false
		for line := range strings.SplitSeq(metrics, "\n") {
			if value, match := strings.CutPrefix(line, sample+" "); match {
				found = true
				n, err := strconv.ParseFloat(value, 64)
				if err != nil || !(n >= minimum) {
					t.Errorf("metric %s = %q, want >= %g", sample, value, minimum)
				}
			}
		}
		if !found {
			t.Errorf("missing metric %s", sample)
		}
	}
	t.Logf("verified real capture in namespace %s: lifecycle %v and named workqueue metrics", wantNS, counts)
}

type integrationRecord struct {
	Event            string    `json:"event"`
	Reason           string    `json:"reason"`
	PodUID           string    `json:"pod_uid"`
	SandboxID        string    `json:"sandbox_id"`
	NetNS            string    `json:"netns_path"`
	NetworkCondition string    `json:"network_condition"`
	At               time.Time `json:"at"`
	ADDSourceTime    time.Time `json:"add_source_time"`
	ADDObservedAt    time.Time `json:"add_observed_at"`
	Snapshot         *struct {
		StartedAt  time.Time `json:"started_at"`
		FinishedAt time.Time `json:"finished_at"`
		State      struct {
			Namespace struct {
				Device uint64 `json:"device"`
				Inode  uint64 `json:"inode"`
			} `json:"namespace"`
			State map[string]struct {
				Entries   []map[string]any `json:"entries"`
				Truncated bool             `json:"truncated"`
			} `json:"state"`
		} `json:"state"`
	} `json:"snapshot"`
}
