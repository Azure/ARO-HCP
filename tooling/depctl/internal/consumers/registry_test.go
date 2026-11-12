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
	"strings"
	"testing"
	"time"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/model"
)

func TestSpecRejectsUnknownAndMalformedParameters(t *testing.T) {
	spec := Spec{
		ID: "test", Program: "sh", Timeout: time.Second,
		AllowedParams: []string{"tool", "version"},
	}
	cases := map[string]map[string]string{
		"unknown parameter": {"command": "rm"},
		"invalid tool":      {"tool": "Bad Tool; rm -rf /"},
		"invalid version":   {"version": ";bump"},
	}
	for name, params := range cases {
		t.Run(name, func(t *testing.T) {
			if err := spec.ValidateParams(params); err == nil {
				t.Fatal("expected a validation error")
			}
		})
	}
	if err := spec.ValidateParams(map[string]string{"tool": "gojq", "version": "v0.12.20"}); err != nil {
		t.Fatalf("valid parameters rejected: %v", err)
	}
}

func TestSpecExpandRejectsUnknownPlaceholder(t *testing.T) {
	spec := Spec{ID: "test", Program: "sh", Arguments: []string{"-c", "echo {{missing}}"}, Timeout: time.Second}
	if _, err := spec.Expand(t.TempDir(), nil); err == nil {
		t.Fatal("expected an unknown placeholder error")
	}
}

func TestRegistryRefusesDuplicatesAndUnknownIDs(t *testing.T) {
	if _, err := NewRegistry(
		Spec{ID: "a", Program: "sh", Timeout: time.Second},
		Spec{ID: "a", Program: "sh", Timeout: time.Second},
	); err == nil {
		t.Fatal("expected a duplicate ID error")
	}
	registry, err := NewRegistry(Spec{ID: "a", Program: "sh", Timeout: time.Second})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	if _, err := registry.Get("b"); err == nil {
		t.Fatal("expected an unknown consumer error")
	}
}

func TestRunnerReportsOutcomes(t *testing.T) {
	root := t.TempDir()
	logs := t.TempDir()
	registry, err := NewRegistry(
		Spec{ID: "pass", Program: "sh", Arguments: []string{"-c", "echo hello"}, Timeout: 10 * time.Second},
		Spec{ID: "fail", Program: "sh", Arguments: []string{"-c", "echo boom >&2; exit 37"}, Timeout: 10 * time.Second},
		Spec{ID: "missing", Program: "sh", Arguments: []string{"-c", "true"},
			Prerequisites: []Prerequisite{{Binary: "definitely-not-installed-binary"}}, Timeout: time.Second},
	)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	runner := NewRunner(registry)

	passed := runner.Run(context.Background(), root, model.ConsumerRef{ID: "pass"}, logs)
	if passed.Status != model.ConsumerPassed || passed.ExitCode != 0 {
		t.Fatalf("unexpected pass result %+v", passed)
	}
	if passed.LogPath == "" || passed.LogDigest == "" {
		t.Fatalf("expected a captured log, got %+v", passed)
	}
	if content, err := os.ReadFile(passed.LogPath); err != nil || !strings.Contains(string(content), "hello") {
		t.Fatalf("log content = %q, %v", content, err)
	}

	failed := runner.Run(context.Background(), root, model.ConsumerRef{ID: "fail"}, logs)
	if failed.Status != model.ConsumerFailed || failed.ExitCode != 37 {
		t.Fatalf("unexpected failure result %+v", failed)
	}
	if !strings.Contains(failed.Reason, "boom") {
		t.Fatalf("expected the failure tail in the reason, got %q", failed.Reason)
	}

	missing := runner.Run(context.Background(), root, model.ConsumerRef{ID: "missing"}, logs)
	if missing.Status != model.ConsumerNotRun {
		t.Fatalf("a missing prerequisite must be not_run, got %+v", missing)
	}

	unknown := runner.Run(context.Background(), root, model.ConsumerRef{ID: "nope"}, logs)
	if unknown.Status != model.ConsumerError {
		t.Fatalf("an unregistered consumer must be an error, got %+v", unknown)
	}
}

func TestRunnerBudgetExhaustion(t *testing.T) {
	registry, err := NewRegistry(Spec{
		ID: "blocked", Program: "sh", Arguments: []string{"-c", "true"},
		Directory:     "does-not-exist",
		Prerequisites: []Prerequisite{{Path: "does-not-exist"}},
		Timeout:       time.Second,
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	result := NewRunner(registry).Run(context.Background(), t.TempDir(), model.ConsumerRef{ID: "blocked"}, t.TempDir())
	if result.Status != model.ConsumerNotRun {
		t.Fatalf("unmet prerequisites must be not_run, got %+v", result)
	}
}

func TestRunnerIgnoresInheritedMakeControlFlags(t *testing.T) {
	root := t.TempDir()
	makefile := "all:\n\t@echo recipe ran\n\t@false\n"
	if err := os.WriteFile(filepath.Join(root, "Makefile"), []byte(makefile), 0o644); err != nil {
		t.Fatalf("write Makefile: %v", err)
	}
	registry, err := NewRegistry(Spec{
		ID: "fails", Program: "make", Arguments: []string{"all"}, Timeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	// -n makes Make print recipes without running them; the consumer must still
	// observe the real failure instead of reporting a pass.
	t.Setenv("MAKEFLAGS", "-n")
	t.Setenv("MAKELEVEL", "3")
	result := NewRunner(registry).Run(context.Background(), root, model.ConsumerRef{ID: "fails"}, t.TempDir())
	if result.Status != model.ConsumerFailed {
		t.Fatalf("an inherited MAKEFLAGS must not turn a failing recipe into %q", result.Status)
	}
}

func TestDefaultRegistryPinsExecutableVersions(t *testing.T) {
	registry := Default()
	for _, id := range []string{"bicep-fixtures", "promtool-rules"} {
		spec, err := registry.Get(id)
		if err != nil {
			t.Fatalf("Get(%s) error = %v", id, err)
		}
		if spec.Preflight == nil {
			t.Fatalf("%s must verify its executable version before running", id)
		}
		found := false
		for _, param := range spec.AllowedParams {
			if param == "expectedVersion" {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s must accept an expected version", id)
		}
	}
}

func TestDefaultRegistryIDsAreStable(t *testing.T) {
	ids := Default().IDs()
	want := []string{"bicep-fixtures", "bingo-get", "bingo-regenerate", "ci-versions-verify", "promtool-rules", "yamlfmt-verify"}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("registry IDs = %v, want %v", ids, want)
	}
	_ = filepath.Join
}

func TestRunnerKeepsLauncherAndPassesSelectedBinary(t *testing.T) {
	root := t.TempDir()
	compiler := filepath.Join(root, "bicep")
	if err := os.WriteFile(compiler, []byte("#!/usr/bin/env bash\necho 'Bicep CLI version 0.49.0'\n"), 0o755); err != nil {
		t.Fatalf("write compiler stub: %v", err)
	}
	launcher := filepath.Join(root, "launcher")
	script := "#!/usr/bin/env bash\necho \"launcher=$0 bicep=$BICEP args=$*\"\n"
	if err := os.WriteFile(launcher, []byte(script), 0o755); err != nil {
		t.Fatalf("write launcher stub: %v", err)
	}
	// Start from the production entry and replace only the launcher, so the
	// selection mechanism under test is the reviewed one.
	spec, err := Default().Get("bicep-fixtures")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	spec.Program = launcher
	registry, err := NewRegistry(spec)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	logs := t.TempDir()
	result := NewRunner(registry).Run(context.Background(), root, model.ConsumerRef{
		ID: "bicep-fixtures", Params: map[string]string{"expectedVersion": "0.49.0", "executablePath": compiler},
	}, logs)
	if result.Status != model.ConsumerPassed {
		t.Fatalf("the selected executable must pass preflight and run through the launcher: %+v", result)
	}
	content, err := os.ReadFile(result.LogPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(content), "launcher="+launcher) {
		t.Fatalf("the reviewed launcher must execute, got %s", content)
	}
	if !strings.Contains(string(content), "bicep="+compiler) {
		t.Fatalf("the selection must reach the launcher as BICEP, got %s", content)
	}
}

func TestProductionSelectionUsesLauncherMechanisms(t *testing.T) {
	registry := Default()
	bicep, err := registry.Get("bicep-fixtures")
	if err != nil {
		t.Fatalf("Get(bicep-fixtures) error = %v", err)
	}
	if bicep.Program != "make" {
		t.Fatalf("the bicep gate must keep make as its launcher, got %q", bicep.Program)
	}
	env := bicep.SelectionEnvironment("/opt/bicep-0.49.0/bicep")
	if len(env) != 1 || env[0] != "BICEP=/opt/bicep-0.49.0/bicep" {
		t.Fatalf("unexpected bicep selection environment %v", env)
	}
	promtool, err := registry.Get("promtool-rules")
	if err != nil {
		t.Fatalf("Get(promtool-rules) error = %v", err)
	}
	if promtool.Program != "go" {
		t.Fatalf("the promtool gate must keep go as its launcher, got %q", promtool.Program)
	}
	if !promtool.AliasOnPath || promtool.SelectedBinary != "promtool" {
		t.Fatalf("the promtool gate must resolve the selected binary by name, got %+v", promtool)
	}
}

func TestRunnerAliasesSelectedBinaryOnPath(t *testing.T) {
	root := t.TempDir()
	selected := filepath.Join(root, "promtool-3.16.0")
	if err := os.WriteFile(selected, []byte("#!/usr/bin/env bash\necho 'promtool, version 3.16.0'\n"), 0o755); err != nil {
		t.Fatalf("write selected binary: %v", err)
	}
	registry, err := NewRegistry(Spec{
		ID: "by-name", Program: "sh",
		Arguments:      []string{"-c", "command -v promtool; readlink \"$(command -v promtool)\""},
		Timeout:        30 * time.Second,
		AllowedParams:  []string{"executablePath"},
		SelectedBinary: "promtool",
		AliasOnPath:    true,
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	result := NewRunner(registry).Run(context.Background(), root, model.ConsumerRef{
		ID: "by-name", Params: map[string]string{"executablePath": selected},
	}, t.TempDir())
	if result.Status != model.ConsumerPassed {
		t.Fatalf("unexpected result %+v", result)
	}
	content, err := os.ReadFile(result.LogPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	lines := strings.Fields(strings.TrimSpace(string(content)))
	if len(lines) != 2 {
		t.Fatalf("unexpected resolution output %q", content)
	}
	if lines[0] == selected || !strings.HasSuffix(lines[0], "/promtool") {
		t.Fatalf("the name must resolve to an alias of the selected binary, got %q", lines[0])
	}
	if lines[1] != selected {
		t.Fatalf("the alias must point at the selected binary, got %q", lines[1])
	}
}

func TestRunnerKillsDescendantsOnTimeout(t *testing.T) {
	root := t.TempDir()
	registry, err := NewRegistry(Spec{
		ID: "slow", Program: "sh",
		Arguments: []string{"-c", "sleep 1.5; echo done"},
		Timeout:   150 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	started := time.Now()
	result := NewRunner(registry).Run(context.Background(), root, model.ConsumerRef{ID: "slow"}, t.TempDir())
	elapsed := time.Since(started)
	if result.Status != model.ConsumerFailed {
		t.Fatalf("a timed-out consumer must fail, got %+v", result)
	}
	if elapsed > time.Second {
		t.Fatalf("cancellation waited for the descendant: %s", elapsed)
	}
}

func TestYamlfmtPrerequisitesMatchItsMakeTarget(t *testing.T) {
	spec, err := Default().Get("yamlfmt-verify")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	for _, requirement := range spec.Prerequisites {
		if requirement.Binary == "yamlfmt" || requirement.Binary == "yamlwrap" {
			t.Fatalf("the Make target builds its own formatter prerequisites, got %v", spec.Prerequisites)
		}
	}
}

func TestProductionRegistryResolvesRenamedPromtool(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"promtool", "promtool-3.16.0"} {
		path := filepath.Join(root, name)
		script := "#!/usr/bin/env bash\nif [[ \"${1:-}\" == \"--version\" ]]; then echo 'promtool, version 3.16.0'; fi\n"
		if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	for _, name := range []string{"promtool", "promtool-3.16.0"} {
		// Keep the production selection fields and preflight; replace only the
		// launcher with a shell that resolves the tool by name, as the rules do.
		spec, err := Default().Get("promtool-rules")
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		spec.Program = "sh"
		spec.Arguments = []string{"-c", "promtool --version"}
		registry, err := NewRegistry(spec)
		if err != nil {
			t.Fatalf("NewRegistry() error = %v", err)
		}
		result := NewRunner(registry).Run(context.Background(), root, model.ConsumerRef{
			ID: "promtool-rules",
			Params: map[string]string{
				"expectedVersion": "3.16.0",
				"executablePath":  filepath.Join(root, name),
			},
		}, t.TempDir())
		if result.Status != model.ConsumerPassed {
			t.Fatalf("%s: the selected executable must be the one resolved by name: %+v", name, result)
		}
	}
}

func TestRunnerCreatesSelectedLogDirectory(t *testing.T) {
	registry, err := NewRegistry(Spec{ID: "ok", Program: "sh", Arguments: []string{"-c", "true"}, Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	root := t.TempDir()
	logs := filepath.Join(t.TempDir(), "new", "logs")
	result := NewRunner(registry).Run(context.Background(), root, model.ConsumerRef{ID: "ok"}, logs)
	if result.Status != model.ConsumerPassed {
		t.Fatalf("a supplied log directory must be created, got %+v", result)
	}
	if info, err := os.Stat(logs); err != nil || !info.IsDir() {
		t.Fatalf("log directory was not created: %v", err)
	}
	if result.LogPath == "" {
		t.Fatal("expected a captured log")
	}
}
