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

package capture

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv("SWIFT_CAPTURE_HELPER"); mode != "" {
		if len(os.Args) != 4 || os.Args[1] != "capture" || os.Args[2] != "--path" {
			os.Exit(2)
		}
		switch mode {
		case "json":
			_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"path": os.Args[3]})
		case "invalid":
			fmt.Fprint(os.Stdout, "not JSON")
		case "fail":
			fmt.Fprint(os.Stderr, "synthetic failure")
			os.Exit(3)
		case "stdout", "stderr":
			out := os.Stdout
			if mode == "stderr" {
				out = os.Stderr
			}
			for {
				if _, err := io.WriteString(out, strings.Repeat("x", 4096)); err != nil {
					os.Exit(4)
				}
			}
		case "wait":
			time.Sleep(10 * time.Second)
		case "run":
			if err := Run(os.Args[3], os.Stdout); err != nil {
				fmt.Fprint(os.Stderr, err)
				os.Exit(1)
			}
		case "isolated":
			if err := runIsolated(os.Args[3]); err != nil {
				fmt.Fprint(os.Stderr, err)
				os.Exit(1)
			}
		default:
			os.Exit(5)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestSnapshot(t *testing.T) {
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		mode string
		want string
	}{
		{"json", ""}, {"invalid", "invalid JSON"}, {"fail", "synthetic failure"},
		{"stdout", "exceeded"}, {"stderr", "exceeded"}, {"wait", "deadline exceeded"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			t.Setenv("SWIFT_CAPTURE_HELPER", tc.mode)
			start := time.Now()
			data, err := Snapshot(context.Background(), executable, "/arbitrary/cni-test", 1024)
			if elapsed := time.Since(start); elapsed > 2*time.Second {
				t.Fatalf("capture took %s", elapsed)
			}
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) || data != nil {
					t.Fatalf("got data %q, error %v; want %q and no data", data, err, tc.want)
				}
				return
			}
			if err != nil || string(bytes.TrimSpace(data)) != `{"path":"/arbitrary/cni-test"}` {
				t.Fatalf("unexpected snapshot %s: %v", data, err)
			}
		})
	}
	t.Run("cancellation", func(t *testing.T) {
		t.Setenv("SWIFT_CAPTURE_HELPER", "wait")
		ctx, cancel := context.WithCancel(context.Background())
		timer := time.AfterFunc(50*time.Millisecond, cancel)
		defer timer.Stop()
		defer cancel()
		start := time.Now()
		if _, err := Snapshot(ctx, executable, "/arbitrary/cni-test", 1024); !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation, got %v", err)
		}
		if time.Since(start) > time.Second {
			t.Fatal("cancellation did not kill child promptly")
		}
	})
	t.Run("earlier deadline", func(t *testing.T) {
		t.Setenv("SWIFT_CAPTURE_HELPER", "wait")
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		if _, err := Snapshot(ctx, executable, "/arbitrary/cni-test", 1024); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected deadline, got %v", err)
		}
	})
	t.Run("invalid limit", func(t *testing.T) {
		for _, limit := range []int{0, -1} {
			if _, err := Snapshot(context.Background(), executable, "unused", limit); err == nil {
				t.Fatal("accepted nonpositive output limit")
			}
		}
	})
}

func TestBoundedBuffer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	buf := boundedBuffer{limit: 4, cancel: cancel}
	for _, part := range []string{"ab", "cd"} {
		if n, err := buf.Write([]byte(part)); n != len(part) || err != nil {
			t.Fatalf("write: %d, %v", n, err)
		}
	}
	if ctx.Err() != nil || buf.exceeded || string(buf.data) != "abcd" {
		t.Fatalf("exact limit rejected: %+v", buf)
	}
	for range 10 {
		_, _ = buf.Write([]byte("excess"))
	}
	if !buf.exceeded || ctx.Err() == nil || string(buf.data) != "abcd" {
		t.Fatalf("output was not bounded: %+v", buf)
	}
}

func TestNamespacePathValidation(t *testing.T) {
	dir := t.TempDir()
	regular := filepath.Join(dir, "cni-regular")
	if err := os.WriteFile(regular, nil, 0600); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(dir, "cni-symlink")
	if err := os.Symlink(regular, symlink); err != nil {
		t.Fatal(err)
	}
	intermediate := filepath.Join(dir, "intermediate")
	if err := os.Symlink(dir, intermediate); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(dir, "cni-fifo")
	if err := unix.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"", "cni-relative", "/wrong-name", filepath.Join(dir, "cni-"),
		dir + "/../" + filepath.Base(dir) + "/cni-regular", dir + "//cni-regular",
		regular, symlink, filepath.Join(intermediate, "cni-regular"), fifo,
		filepath.Join(dir, "cni-missing"), regular + "/",
	} {
		t.Run(path, func(t *testing.T) {
			if fd, err := openNamespace(path); err == nil {
				_ = unix.Close(fd)
				t.Fatalf("accepted invalid namespace path %q", path)
			}
			var out bytes.Buffer
			if err := Run(path, &out); err == nil || out.Len() != 0 {
				t.Fatalf("invalid path produced output %q or no error: %v", out.String(), err)
			}
		})
	}
	for _, path := range []string{symlink, filepath.Join(intermediate, "cni-regular")} {
		if _, err := openNamespace(path); !errors.Is(err, unix.ELOOP) {
			t.Errorf("symlink %q was not rejected at lookup: %v", path, err)
		}
	}
	if _, err := openNamespace(regular); err == nil || !strings.Contains(err.Error(), "not nsfs") {
		t.Errorf("regular file did not reach nsfs validation: %v", err)
	}
}

func TestDecodeLink(t *testing.T) {
	order := binary.NativeEndian
	data := make([]byte, unix.SizeofIfInfomsg)
	order.PutUint32(data[4:], 8)
	order.PutUint32(data[8:], unix.IFF_UP|unix.IFF_RUNNING|unix.IFF_LOWER_UP)
	attr := func(kind uint16, value []byte) {
		b := make([]byte, (len(value)+7)&^3)
		order.PutUint16(b, uint16(len(value)+4))
		order.PutUint16(b[2:], kind)
		copy(b[4:], value)
		data = append(data, b...)
	}
	attr(unix.IFLA_IFNAME, []byte("vf0\x00"))
	attr(unix.IFLA_ADDRESS, []byte{0, 1, 2, 3, 4, 5})
	attr(unix.IFLA_OPERSTATE, []byte{6}) // IF_OPER_UP
	attr(unix.IFLA_CARRIER, []byte{0})   // Deliberately differs from IFF_RUNNING.
	attr(unix.IFLA_MASTER, order.AppendUint32(nil, 3))
	attr(unix.IFLA_LINK, order.AppendUint32(nil, 7))
	stats := make([]byte, 8*8)
	for i := range 8 {
		order.PutUint64(stats[i*8:], uint64(i)+1<<33)
	}
	attr(unix.IFLA_STATS64, stats)
	entry, err := decode(unix.RTM_GETLINK, unix.SizeofIfInfomsg, data)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{
		"name": "vf0", "index": int32(8), "mac": "00:01:02:03:04:05",
		"up": true, "carrier": false, "running": true, "lowerUp": true,
		"masterIndex": uint32(3), "parentIndex": uint32(7),
		"operstate": uint8(6),
	} {
		if entry[key] != want {
			t.Errorf("%s = %v, want %v", key, entry[key], want)
		}
	}
	values := entry["stats"].(map[string]uint64)
	if values["rxPackets"] != 1<<33 || values["txDrops"] != 1<<33+7 {
		t.Fatalf("incorrect 64-bit counters: %v", values)
	}
	// Older devices may expose only the 32-bit stats attribute.
	data = data[:unix.SizeofIfInfomsg]
	attr(unix.IFLA_STATS, stats[:32])
	entry, err = decode(unix.RTM_GETLINK, unix.SizeofIfInfomsg, data)
	if err != nil || entry["stats"].(map[string]uint64)["txPackets"] != 2 {
		t.Fatalf("incorrect 32-bit counter fallback: %v, %v", entry, err)
	}
	if _, present := entry["carrier"]; present {
		t.Fatal("missing IFLA_CARRIER must not be inferred from flags")
	}
	order.PutUint32(data[8:], unix.IFF_UP)
	attr(unix.IFLA_CARRIER, []byte{1})
	entry, err = decode(unix.RTM_GETLINK, unix.SizeofIfInfomsg, data)
	if err != nil || entry["carrier"] != true || entry["running"] != false || entry["lowerUp"] != false {
		t.Fatalf("carrier and running flags were conflated: %v, %v", entry, err)
	}
	for _, malformed := range [][]byte{nil, data[:3], append(data[:unix.SizeofIfInfomsg:unix.SizeofIfInfomsg], 1, 0, 1, 0)} {
		if _, err := decode(unix.RTM_GETLINK, unix.SizeofIfInfomsg, malformed); err == nil {
			t.Fatal("accepted malformed message")
		}
	}
}

func TestCollectCurrentNamespace(t *testing.T) {
	result, err := collect()
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(captureResult{Namespace: namespaceIdentity{Device: 4, Inode: 1<<33 + 7}, State: result})
	if err != nil {
		t.Fatal(err)
	}
	var decoded captureResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Namespace.Device != 4 || decoded.Namespace.Inode != 1<<33+7 || len(decoded.State) != 5 {
		t.Fatalf("incorrect serialized snapshot: %s", data)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || len(fields) != 2 {
		t.Fatalf("expected only namespace and state, without timestamps: %s (%v)", data, err)
	}
	result = decoded.State
	for _, name := range []string{"links", "addresses", "routes", "rules", "neighbors"} {
		section, ok := result[name]
		if !ok || section.Entries == nil || len(section.Entries) > maxEntries {
			t.Fatalf("missing or unbounded %s: %+v", name, section)
		}
	}
	if len(result["links"].Entries) == 0 {
		t.Fatal("expected at least loopback")
	}
	if _, err := dump(unix.RTM_GETLINK, unix.SizeofIfInfomsg, time.Now().Add(-time.Second)); err == nil {
		t.Fatal("accepted expired netlink deadline")
	}
}

// Supply a pre-existing, non-symlink cni- namespace bind mount and CAP_SYS_ADMIN.
// The test neither creates mounts nor changes links, addresses or routes.
func TestRunNamespaceOptIn(t *testing.T) {
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	path := os.Getenv("SWIFT_RECORDER_TEST_NETNS")
	if path == "" {
		t.Skip("set SWIFT_RECORDER_TEST_NETNS to an existing cni- network namespace")
	}
	t.Setenv("SWIFT_CAPTURE_HELPER", "run")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := Snapshot(context.Background(), executable, path, 1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	var result captureResult
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.State["links"].Entries) == 0 || len(result.State) != 5 {
		t.Fatalf("incomplete namespace snapshot: %s", data)
	}
	var stat unix.Stat_t
	if err := unix.Stat(path, &stat); err != nil {
		t.Fatal(err)
	}
	if result.Namespace != (namespaceIdentity{Device: uint64(stat.Dev), Inode: stat.Ino}) {
		t.Fatalf("incorrect namespace identity: %+v, stat: %+v", result.Namespace, stat)
	}
}

// Mounts are confined to a fresh private mount namespace owned by a new user
// namespace. The helper has CAP_SYS_ADMIN there, never in the host namespace.
func TestRunIsolatedNamespaceOptIn(t *testing.T) {
	if os.Getenv("SWIFT_RECORDER_TEST_ISOLATED") != "1" {
		t.Skip("set SWIFT_RECORDER_TEST_ISOLATED=1 to test with rootless user/network/mount namespaces")
	}
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	t.Setenv("SWIFT_CAPTURE_HELPER", "isolated")
	path := filepath.Join(t.TempDir(), "cni-isolated")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "capture", "--path", path)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags:  unix.CLONE_NEWUSER | unix.CLONE_NEWNET | unix.CLONE_NEWNS,
		UidMappings: []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getuid(), Size: 1}},
		GidMappings: []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getgid(), Size: 1}},
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("isolated namespace capture: %v; stderr: %s", err, stderr.String())
	}
	var result captureResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.State) != 5 || len(result.State["links"].Entries) != 1 || result.State["links"].Entries[0]["name"] != "lo" {
		t.Fatalf("expected only isolated loopback and all five sections: %s", stdout.String())
	}
	var host unix.Stat_t
	if err := unix.Stat("/proc/self/ns/net", &host); err != nil {
		t.Fatal(err)
	}
	if result.Namespace.Inode == 0 || result.Namespace == (namespaceIdentity{Device: uint64(host.Dev), Inode: host.Ino}) {
		t.Fatalf("capture did not identify a separate namespace: %+v", result.Namespace)
	}
	if _, err := openNamespace(path); err == nil || !strings.Contains(err.Error(), "not nsfs") {
		t.Fatalf("child mount escaped into parent mount namespace: %v", err)
	}
}

func runIsolated(path string) error {
	runtime.LockOSThread()
	if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); err != nil {
		return fmt.Errorf("make helper mounts private: %w", err)
	}
	if err := unix.Mount("/proc/thread-self/ns/net", path, "", unix.MS_BIND, ""); err != nil {
		return fmt.Errorf("bind helper namespace: %w", err)
	}
	defer func() { _ = unix.Unmount(path, unix.MNT_DETACH) }()
	var stat unix.Stat_t
	if err := unix.Stat(path, &stat); err != nil {
		return err
	}
	var output bytes.Buffer
	if err := Run(path, &output); err != nil {
		return err
	}
	var result captureResult
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		return err
	}
	if result.Namespace != (namespaceIdentity{Device: uint64(stat.Dev), Inode: stat.Ino}) {
		return fmt.Errorf("captured identity %+v does not match opened namespace %+v", result.Namespace, stat)
	}
	_, err := os.Stdout.Write(output.Bytes())
	return err
}
