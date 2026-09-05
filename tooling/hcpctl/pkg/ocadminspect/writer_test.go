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

package ocadminspect

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApiGroup(t *testing.T) {
	tests := map[string]string{
		"v1":                              "core",
		"apps/v1":                         "apps",
		"hypershift.openshift.io/v1beta1": "hypershift.openshift.io",
		"":                                "core",
	}
	for apiVersion, want := range tests {
		if got := apiGroup(apiVersion); got != want {
			t.Errorf("apiGroup(%q) = %q, want %q", apiVersion, got, want)
		}
	}
}

func TestRenderLogLine(t *testing.T) {
	if got := renderLogLine("hello\n"); got != "hello" {
		t.Errorf("renderLogLine(string) = %q, want %q", got, "hello")
	}
	if got := renderLogLine(nil); got != "" {
		t.Errorf("renderLogLine(nil) = %q, want empty", got)
	}
	if got := renderLogLine(map[string]any{"msg": "hi"}); !strings.Contains(got, "hi") {
		t.Errorf("renderLogLine(map) = %q, want it to contain the value", got)
	}
}

func TestFilesystemWriter_Layout(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	w := NewFilesystemWriter(root)
	const ns = "kube-system"

	resources := []Resource{
		// The Namespace object is cluster-scoped (empty namespace field) but named ns.
		{APIVersion: "v1", Kind: "Namespace", Namespace: "", Name: ns, Object: map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": ns}}},
		{APIVersion: "v1", Kind: "ConfigMap", Namespace: ns, Name: "coredns", Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": "coredns"}}},
		{APIVersion: "v1", Kind: "Pod", Namespace: ns, Name: "coredns-abc", Object: map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": "coredns-abc"}}},
	}
	if err := w.WriteResources(ctx, ns, resources); err != nil {
		t.Fatalf("WriteResources: %v", err)
	}
	if err := w.WriteEvents(ctx, ns, []map[string]any{{"reason": "Started", "message": "started container"}}); err != nil {
		t.Fatalf("WriteEvents: %v", err)
	}
	if err := w.WriteContainerLog(ctx, ns, "coredns-abc", "coredns", []LogLine{{Timestamp: "t0", Log: "log line one"}}); err != nil {
		t.Fatalf("WriteContainerLog: %v", err)
	}

	// Namespace object: namespaces/<ns>/<ns>.yaml (leading --- separator).
	assertFileContains(t, filepath.Join(root, "namespaces", ns, ns+".yaml"), "kind: Namespace")
	assertFileContains(t, filepath.Join(root, "namespaces", ns, ns+".yaml"), "---")
	// Non-pod resource List: namespaces/<ns>/core/configmaps.yaml
	assertFileContains(t, filepath.Join(root, "namespaces", ns, "core", "configmaps.yaml"), "kind: List")
	assertFileContains(t, filepath.Join(root, "namespaces", ns, "core", "configmaps.yaml"), "name: coredns")
	// Pods appear in the core/pods.yaml List...
	assertFileContains(t, filepath.Join(root, "namespaces", ns, "core", "pods.yaml"), "name: coredns-abc")
	// ...and individually at pods/<pod>/<pod>.yaml.
	assertFileContains(t, filepath.Join(root, "namespaces", ns, "pods", "coredns-abc", "coredns-abc.yaml"), "kind: Pod")
	// Events: namespaces/<ns>/core/events.yaml, with the same "---" prefix as other files.
	assertFileContains(t, filepath.Join(root, "namespaces", ns, "core", "events.yaml"), "started container")
	assertFileContains(t, filepath.Join(root, "namespaces", ns, "core", "events.yaml"), "---")
	// Container log with doubled container segment.
	assertFileContains(t, filepath.Join(root, "namespaces", ns, "pods", "coredns-abc", "coredns", "coredns", "logs", "current.log"), "log line one")
}

func TestFilesystemWriter_RejectsPathTraversal(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	w := NewFilesystemWriter(root)

	// A traversal namespace must not write outside the root.
	if err := w.WriteContainerLog(ctx, "../../evil", "pod", "ctr", []LogLine{{Log: "x"}}); err == nil {
		t.Errorf("expected WriteContainerLog to reject a path-traversal namespace")
	}
	if err := w.WriteResources(ctx, "../../evil", []Resource{
		{APIVersion: "v1", Kind: "ConfigMap", Name: "c", Object: map[string]any{"kind": "ConfigMap"}},
	}); err == nil {
		t.Errorf("expected WriteResources to reject a path-traversal namespace")
	}
	// Nothing should have been created outside the root.
	if entries, _ := os.ReadDir(filepath.Dir(root)); len(entries) > 0 {
		for _, e := range entries {
			if e.Name() == "evil" {
				t.Errorf("path traversal wrote outside the output root: %s", e.Name())
			}
		}
	}
}

func assertFileContains(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected file %s: %v", path, err)
	}
	if !strings.Contains(string(data), want) {
		t.Errorf("file %s missing %q; got:\n%s", path, want, string(data))
	}
}
