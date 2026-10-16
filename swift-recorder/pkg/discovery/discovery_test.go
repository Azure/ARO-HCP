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

package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"testing/synctest"
	"time"
)

const sandboxID = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func recordFields(name string) map[string]any {
	return map[string]any{
		"msg":         "Processing ADD command",
		"containerId": sandboxID,
		"netNS":       "/arbitrary/netns/location",
		"args":        "IgnoreUnknown=1;K8S_POD_NAMESPACE=namespace;K8S_POD_NAME=" + name + ";K8S_POD_UID=9ea56d4f-79e7-4137-bf93-e975023bddf6;PRIVATE=raw-secret=more;",
		"stdinData":   "sensitive-stdin",
		"pid":         1234,
		"ts":          "2026-09-17T12:34:56.123+0000",
	}
}

func marshalRecord(t *testing.T, fields map[string]any) string {
	t.Helper()
	data, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return string(data) + "\n"
}

func logRecord(t *testing.T, name string) string {
	t.Helper()
	return marshalRecord(t, recordFields(name))
}

func TestParseAttempt(t *testing.T) {
	before := time.Now()
	attempt, ok := parseAttempt([]byte(logRecord(t, "pod")))
	if !ok {
		t.Fatal("valid full CNI record rejected")
	}
	want := Attempt{
		PodUID: "9ea56d4f-79e7-4137-bf93-e975023bddf6", PodName: "pod", Namespace: "namespace",
		SandboxID: sandboxID, NetNS: "/arbitrary/netns/location",
		ObservedAt: attempt.ObservedAt, SourceTime: time.Date(2026, 9, 17, 12, 34, 56, 123000000, time.UTC),
	}
	if attempt != want {
		t.Fatalf("extracted attempt = %+v, want %+v", attempt, want)
	}
	if attempt.ObservedAt.Before(before) || attempt.ObservedAt.After(time.Now()) {
		t.Fatal("ObservedAt must reflect discovery time")
	}
	encoded, err := json.Marshal(attempt)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"PRIVATE", "raw-secret", "stdin", "IgnoreUnknown", "pid"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("attempt contains non-allowlisted data %q", forbidden)
		}
	}
}

func TestParseRejectsMalformed(t *testing.T) {
	for _, tc := range []struct {
		name  string
		field string
		value any
	}{
		{"wrong message", "msg", "Processing DEL command"},
		{"message suffix", "msg", "Processing ADD command with args"},
		{"missing message", "msg", nil},
		{"short sandbox", "containerId", sandboxID[:63]},
		{"long sandbox", "containerId", sandboxID + "0"},
		{"nonhex sandbox", "containerId", strings.Repeat("g", 64)},
		{"sandbox type", "containerId", 123},
		{"empty netns", "netNS", ""},
		{"blank netns", "netNS", " "},
		{"netns type", "netNS", []string{"/proc/1/ns/net"}},
		{"args type", "args", map[string]string{"K8S_POD_UID": "uid"}},
		{"missing UID", "args", "K8S_POD_NAME=pod;K8S_POD_NAMESPACE=ns"},
		{"missing name", "args", "K8S_POD_UID=uid;K8S_POD_NAMESPACE=ns"},
		{"missing namespace", "args", "K8S_POD_UID=uid;K8S_POD_NAME=pod"},
		{"empty UID", "args", "K8S_POD_UID=;K8S_POD_NAME=pod;K8S_POD_NAMESPACE=ns"},
		{"blank UID", "args", "K8S_POD_UID= ;K8S_POD_NAME=pod;K8S_POD_NAMESPACE=ns"},
		{"wrong key", "args", "NOT_K8S_POD_UID=uid;K8S_POD_NAME=pod;K8S_POD_NAMESPACE=ns"},
		{"duplicate key", "args", "K8S_POD_UID=uid;K8S_POD_UID=other;K8S_POD_NAME=pod;K8S_POD_NAMESPACE=ns"},
		{"broken pair", "args", "broken;K8S_POD_UID=uid;K8S_POD_NAME=pod;K8S_POD_NAMESPACE=ns"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fields := recordFields("pod")
			fields[tc.field] = tc.value
			if got, ok := parseAttempt([]byte(marshalRecord(t, fields))); ok || got != (Attempt{}) {
				t.Fatalf("malformed record must return no extracted data: %+v, %v", got, ok)
			}
		})
	}
	for _, raw := range []string{"not JSON raw-secret", "{", "null", "[]", logRecord(t, "pod") + "{}"} {
		if got, ok := parseAttempt([]byte(raw)); ok || got != (Attempt{}) {
			t.Fatal("malformed JSON must return no extracted data")
		}
	}
}

func TestParseOptionalFields(t *testing.T) {
	for _, timestamp := range []any{nil, 123, "invalid", "2026-09-17T12:34:56.123Z", "2026-09-17T12:34:56.123+0000"} {
		fields := recordFields("pod")
		delete(fields, "pid")
		fields["ts"] = timestamp
		fields["args"] = "K8S_POD_UID=non-uuid;K8S_POD_NAME=pod;K8S_POD_NAMESPACE=ns"
		fields["containerId"] = strings.ToUpper(sandboxID)
		attempt, ok := parseAttempt([]byte(marshalRecord(t, fields)))
		if !ok || attempt.PodUID != "non-uuid" {
			t.Fatal("pid and a parseable timestamp are optional; UID need only be nonempty")
		}
		validTime := timestamp != nil && timestamp != 123 && timestamp != "invalid"
		if attempt.SourceTime.IsZero() == validTime {
			t.Fatalf("unexpected SourceTime for %v: %v", timestamp, attempt.SourceTime)
		}
	}
}

func writeLog(t *testing.T, path, data string, appendOnly bool) {
	t.Helper()
	flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if appendOnly {
		flags = os.O_CREATE | os.O_WRONLY | os.O_APPEND
	}
	file, err := os.OpenFile(path, flags, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.WriteString(data)
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("writing log: %v, closing: %v", err, closeErr)
	}
}

// Called inside a synctest bubble; callbacks and cancellation are synchronized.
func startTail(t *testing.T, path string) (<-chan Attempt, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{}, 10)
	attempts := make(chan Attempt, 100)
	done := make(chan error, 1)
	go func() {
		done <- Tail(ctx, path, func(attempt Attempt) { attempts <- attempt }, func() { ready <- struct{}{} })
	}()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("Tail failed before readiness: %v", err)
	case <-time.After(time.Second):
		t.Fatal("Tail did not signal readiness")
	}
	return attempts, func() {
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("Tail cancellation = %v", err)
			}
		case <-time.After(time.Second):
			t.Error("Tail did not stop on cancellation")
		}
		if len(ready) != 0 {
			t.Error("readiness called more than once")
		}
	}
}

func expectAttempt(t *testing.T, attempts <-chan Attempt, name string) {
	t.Helper()
	select {
	case attempt := <-attempts:
		if attempt.PodName != name {
			t.Fatalf("attempt name = %q, want %q", attempt.PodName, name)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("missing attempt %q", name)
	}
}

func expectQuiet(t *testing.T, attempts <-chan Attempt) {
	t.Helper()
	time.Sleep(2 * pollInterval)
	synctest.Wait()
	select {
	case attempt := <-attempts:
		t.Fatalf("unexpected attempt: %+v", attempt)
	default:
	}
}

func TestTailStartup(t *testing.T) {
	for _, initial := range []string{"existing", "partial", "missing", "missing parent"} {
		t.Run(initial, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "azure-vnet.log")
				switch initial {
				case "existing":
					writeLog(t, path, logRecord(t, "historical"), false)
				case "partial":
					writeLog(t, path, "old partial", false)
				case "missing parent":
					path = filepath.Join(filepath.Dir(path), "new", "azure-vnet.log")
				}
				attempts, stop := startTail(t, path)
				defer stop()
				expectQuiet(t, attempts)
				if initial == "missing parent" {
					if err := os.Mkdir(filepath.Dir(path), 0700); err != nil {
						t.Fatal(err)
					}
				}
				if initial == "partial" {
					writeLog(t, path, logRecord(t, "old-suffix"), true)
				}
				writeLog(t, path, logRecord(t, "new"), true)
				expectAttempt(t, attempts, "new")
				expectQuiet(t, attempts)
			})
		})
	}
}

func TestTailPartialAndOversize(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "azure-vnet.log")
		attempts, stop := startTail(t, path)
		defer stop()
		line := logRecord(t, "partial")
		writeLog(t, path, line[:len(line)/2], true)
		expectQuiet(t, attempts)
		writeLog(t, path, line[len(line)/2:len(line)-1], true)
		expectQuiet(t, attempts)
		writeLog(t, path, "\n", true)
		expectAttempt(t, attempts, "partial")
		writeLog(t, path, strings.Repeat("x", maxLineBytes+1), true)
		expectQuiet(t, attempts)
		writeLog(t, path, logRecord(t, "oversize-suffix"), true)
		writeLog(t, path, "{broken JSON}\n"+logRecord(t, "recovered"), true)
		expectAttempt(t, attempts, "recovered")
		expectQuiet(t, attempts)
	})
}

func TestTailRotation(t *testing.T) {
	for _, gap := range []bool{false, true} {
		t.Run(map[bool]string{false: "immediate replacement", true: "missing replacement"}[gap], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "azure-vnet.log")
				writeLog(t, path, "", false)
				attempts, stop := startTail(t, path)
				defer stop()
				writeLog(t, path, logRecord(t, "before"), true)
				expectAttempt(t, attempts, "before")
				old := path + ".1"
				if err := os.Rename(path, old); err != nil {
					t.Fatal(err)
				}
				if !gap {
					writeLog(t, path, logRecord(t, "replacement"), false)
				}
				writeLog(t, old, logRecord(t, "immediate-old"), true)
				expectAttempt(t, attempts, "immediate-old")
				time.Sleep(pollInterval)
				writeLog(t, old, logRecord(t, "late-old"), true)
				expectAttempt(t, attempts, "late-old")
				if gap {
					time.Sleep(time.Second)
					writeLog(t, path, logRecord(t, "replacement"), false)
				}
				expectAttempt(t, attempts, "replacement")
				writeLog(t, old, logRecord(t, "expired-old"), true)
				writeLog(t, path, logRecord(t, "active"), true)
				expectAttempt(t, attempts, "active")
				expectQuiet(t, attempts)
			})
		})
	}
}

func TestTailCopytruncate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "azure-vnet.log")
		attempts, stop := startTail(t, path)
		defer stop()
		writeLog(t, path, strings.Repeat("x", maxLineBytes+1), false)
		expectQuiet(t, attempts)
		writeLog(t, path, logRecord(t, "after-truncation"), false)
		expectAttempt(t, attempts, "after-truncation")
		expectQuiet(t, attempts)
	})
}

func TestTailRotationDiscardsPartialLine(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "azure-vnet.log")
		attempts, stop := startTail(t, path)
		defer stop()
		writeLog(t, path, "unfinished record", true)
		expectQuiet(t, attempts)
		if err := os.Rename(path, path+".1"); err != nil {
			t.Fatal(err)
		}
		writeLog(t, path, logRecord(t, "replacement"), false)
		expectAttempt(t, attempts, "replacement")
		expectQuiet(t, attempts)
	})
}

func TestTailReplacementPermissionError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses file permissions")
	}
	synctest.Test(t, func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "azure-vnet.log")
		writeLog(t, path, "", false)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		ready := make(chan struct{}, 10)
		done := make(chan error, 1)
		go func() {
			done <- Tail(ctx, path, nil, func() { ready <- struct{}{} })
		}()
		<-ready
		if err := os.Rename(path, path+".1"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0000); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			if !errors.Is(err, os.ErrPermission) {
				t.Fatalf("expected replacement permission error, got %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("replacement permission error was not fatal within drain window")
		}
		if len(ready) != 0 {
			t.Error("readiness called again on replacement")
		}
	})
}

func TestReadPollBoundsAndCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "azure-vnet.log")
	writeLog(t, path, strings.Repeat("x", maxPollBytes*2), false)
	input, err := openLog(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer input.file.Close()
	if err := input.readPoll(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if input.offset != maxPollBytes || cap(input.line) > maxLineBytes || !input.discard {
		t.Fatalf("unbounded poll: offset=%d capacity=%d discard=%v", input.offset, cap(input.line), input.discard)
	}
	writeLog(t, path, strings.Repeat(logRecord(t, "pod"), 10), false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	err = input.readPoll(ctx, func(Attempt) { calls++; cancel() })
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("cancellation during batch: calls=%d err=%v", calls, err)
	}
}

func TestLineLimit(t *testing.T) {
	for _, size := range []int{maxLineBytes, maxLineBytes + 1} {
		path := filepath.Join(t.TempDir(), "azure-vnet.log")
		fields := recordFields("pod")
		fields["padding"] = ""
		fields["padding"] = strings.Repeat("x", size-(len(marshalRecord(t, fields))-1))
		writeLog(t, path, marshalRecord(t, fields)+logRecord(t, "recovery"), false)
		input, err := openLog(path, false)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		err = input.readPoll(context.Background(), func(attempt Attempt) { names = append(names, attempt.PodName) })
		_ = input.file.Close()
		want := "recovery"
		if size == maxLineBytes {
			want = "pod,recovery"
		}
		if err != nil || strings.Join(names, ",") != want {
			t.Fatalf("line size %d: names=%v err=%v", size, names, err)
		}
	}
}

func TestTailInitializationErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Tail(ctx, "unused", nil, func() { t.Error("ready after cancellation") }); !errors.Is(err, context.Canceled) {
		t.Fatalf("already canceled Tail: %v", err)
	}
	for _, kind := range []string{"directory", "fifo", "permission"} {
		t.Run(kind, func(t *testing.T) {
			path := t.TempDir()
			switch kind {
			case "fifo":
				path = filepath.Join(path, "fifo")
				if err := syscall.Mkfifo(path, 0600); err != nil {
					t.Fatal(err)
				}
			case "permission":
				if os.Geteuid() == 0 {
					t.Skip("root bypasses file permissions")
				}
				path = filepath.Join(path, "azure-vnet.log")
				if err := os.WriteFile(path, nil, 0000); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err := Tail(ctx, path, nil, func() { t.Error("ready after initialization error") })
			if err == nil || errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("expected immediate filesystem error, got %v", err)
			}
			if kind == "permission" && !errors.Is(err, os.ErrPermission) {
				t.Fatalf("expected permission error, got %v", err)
			}
		})
	}
}
