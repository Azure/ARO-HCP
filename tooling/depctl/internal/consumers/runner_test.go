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

package consumers

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/model"
)

func TestRunnerFullLogsPreservesCombinedOutputDigestAndIsolation(t *testing.T) {
	registry, err := NewRegistry(Spec{
		ID: "full-output", Program: "sh",
		Arguments: []string{"-c", "printf 'start\\n'; head -c 2200000 /dev/zero; printf 'stderr-evidence\\n' >&2; exit 7"},
		Timeout:   10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	root, logs := t.TempDir(), t.TempDir()
	runner := NewRunner(registry)
	runner.FullLogs = true
	runner.MaxLogBytes = 64
	first := runner.Run(context.Background(), root, model.ConsumerRef{ID: "full-output"}, logs)
	if first.Status != model.ConsumerFailed || first.ExitCode != 7 {
		t.Fatalf("lost failure: %+v", first)
	}
	content, err := os.ReadFile(first.LogPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(content) < 2200000 || !strings.HasPrefix(string(content), "start\n") || !strings.HasSuffix(string(content), "stderr-evidence\n") {
		t.Fatal("lost combined full output")
	}
	if first.LogDigest != digestBytes(content) || strings.Contains(first.Reason, "truncated") {
		t.Fatalf("incorrect complete-log evidence: %+v", first)
	}
	second := runner.Run(context.Background(), root, model.ConsumerRef{ID: "full-output"}, logs)
	if second.LogPath == first.LogPath {
		t.Fatal("reused log file")
	}
	again, err := os.ReadFile(first.LogPath)
	if err != nil || string(again) != string(content) {
		t.Fatal("overwrote earlier failure evidence")
	}
	bounded := NewRunner(registry)
	bounded.MaxLogBytes = 64
	result := bounded.Run(context.Background(), root, model.ConsumerRef{ID: "full-output"}, logs)
	truncated, err := os.ReadFile(result.LogPath)
	if err != nil || len(truncated) != 64 || !strings.Contains(result.Reason, "truncated") {
		t.Fatal("changed default bounded-log behavior")
	}
}

func TestRunnerInheritedAndOverrideMakeControlsCannotBypassRecursiveChecks(t *testing.T) {
	for _, name := range []string{"MAKEFLAGS", "MFLAGS", "GNUMAKEFLAGS"} {
		for _, flag := range []string{"-i", "-n", "-q", "-t"} {
			t.Run(name+flag, func(t *testing.T) {
				runFailingRecursiveMake(t, map[string]string{name: flag})
			})
		}
	}
	for _, command := range []string{"make -i", "make -n", "true"} {
		t.Run(command, func(t *testing.T) {
			runFailingRecursiveMake(t, map[string]string{"MAKE": command})
		})
	}
	t.Run("inherited-recursive-overrides", func(t *testing.T) {
		extra := filepath.Join(t.TempDir(), "inherited.mk")
		if err := os.WriteFile(extra, []byte("override MAKEFLAGS += -i\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		runFailingRecursiveMake(t, map[string]string{
			"MAKEFILES": extra, "MAKELEVEL": "10", "MAKEOVERRIDES": "MAKEFLAGS=-i",
		})
	})
}

func runFailingRecursiveMake(t *testing.T, controls map[string]string) {
	t.Helper()
	root := t.TempDir()
	for name, text := range map[string]string{
		"Makefile": "check:\n\t@$(MAKE) -f child.mk verify\n",
		"child.mk": "verify:\n\t@echo checked > marker\n\t@exit 37\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var overrides []string
	for name, value := range controls {
		t.Setenv(name, value)
		overrides = append(overrides, name+"="+value)
	}
	registry, err := NewRegistry(Spec{
		ID: "recursive", Program: "make", Arguments: []string{"check"},
		Environment: overrides, Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := NewRunner(registry)
	runner.FullLogs = true
	result := runner.Run(context.Background(), root, model.ConsumerRef{ID: "recursive"}, t.TempDir())
	if result.Status != model.ConsumerFailed || result.ExitCode == 0 {
		t.Fatalf("Make controls hid failure: %+v", result)
	}
	marker, err := os.ReadFile(filepath.Join(root, "marker"))
	if err != nil || string(marker) != "checked\n" {
		t.Fatal("Make controls skipped recursive consumer")
	}
	log, err := os.ReadFile(result.LogPath)
	if err != nil || !strings.Contains(string(log), "37") {
		t.Fatal("failure evidence missing")
	}
}

func TestRunnerFullLogTimeoutKillsDescendants(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("descendant liveness proof uses Linux procfs")
	}
	registry, err := NewRegistry(Spec{
		ID: "timeout", Program: "sh", Arguments: []string{"-c", "sleep 60 & child=$!; echo $$ $child; wait"},
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := NewRunner(registry)
	runner.FullLogs = true
	result := runner.Run(context.Background(), t.TempDir(), model.ConsumerRef{ID: "timeout"}, t.TempDir())
	if result.Status != model.ConsumerFailed || result.ExitCode != -1 || !strings.Contains(result.Reason, "timed out") {
		t.Fatalf("timeout did not fail: %+v", result)
	}
	content, err := os.ReadFile(result.LogPath)
	if err != nil {
		t.Fatal(err)
	}
	pids := strings.Fields(string(content))
	if len(pids) != 2 {
		t.Fatalf("missing parent/child evidence: %q", content)
	}
	for _, raw := range pids {
		pid, err := strconv.Atoi(raw)
		if err != nil || pid <= 1 {
			t.Fatalf("bad PID %q", raw)
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			stat, err := os.ReadFile(filepath.Join("/proc", raw, "stat"))
			if os.IsNotExist(err) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			_, tail, _ := strings.Cut(string(stat), ") ")
			if strings.HasPrefix(tail, "Z ") {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("consumer descendant %d survived timeout", pid)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestRunnerMissingProgramIsErrorNotSkip(t *testing.T) {
	registry, err := NewRegistry(Spec{
		ID: "missing", Program: filepath.Join(t.TempDir(), "absent"), Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := NewRunner(registry)
	runner.FullLogs = true
	result := runner.Run(context.Background(), t.TempDir(), model.ConsumerRef{ID: "missing"}, t.TempDir())
	if result.Status != model.ConsumerError || result.LogPath == "" {
		t.Fatalf("missing program was skipped: %+v", result)
	}
}
