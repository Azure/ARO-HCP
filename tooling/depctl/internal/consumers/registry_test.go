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

func TestDefaultRegistryIDsAreStable(t *testing.T) {
	ids := Default().IDs()
	want := []string{"bicep-fixtures", "bingo-get", "bingo-regenerate", "ci-versions-verify", "promtool-rules", "yamlfmt-verify"}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("registry IDs = %v, want %v", ids, want)
	}
	_ = filepath.Join
}
