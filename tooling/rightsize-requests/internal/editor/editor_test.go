// Copyright 2025 Microsoft Corporation
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

package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `# top comment
defaults:
  backend:
    k8s:
      resources:
        requests:
          cpu: 100m
          memory: 1Gi # keep this comment
  prometheus:
    resources:
      requests:
        cpu: NONE
`

func TestGetAndApply(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(sample), 0o644); err != nil {
		t.Fatal(err)
	}

	ed, err := New(path)
	if err != nil {
		t.Fatal(err)
	}

	v, line, err := ed.Get("defaults.backend.k8s.resources.requests.cpu")
	if err != nil {
		t.Fatal(err)
	}
	if v != "100m" || line != 7 {
		t.Fatalf("Get cpu = %q line %d, want 100m line 7", v, line)
	}

	if err := ed.ApplyUpdates([]Update{
		{Path: "defaults.backend.k8s.resources.requests.cpu", NewValue: "150m"},
		{Path: "defaults.backend.k8s.resources.requests.memory", NewValue: "2Gi"},
	}); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := `# top comment
defaults:
  backend:
    k8s:
      resources:
        requests:
          cpu: 150m
          memory: 2Gi # keep this comment
  prometheus:
    resources:
      requests:
        cpu: NONE
`
	if string(got) != want {
		t.Fatalf("unexpected file contents:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestGetMissingPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(sample), 0o644); err != nil {
		t.Fatal(err)
	}
	ed, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ed.Get("defaults.nope.cpu"); err == nil {
		t.Error("expected error for missing path")
	}
}

const overlaySample = `clouds:
  public:
    defaults:
      # RP Backend
      backend:
        exitOnPanic: true
        image:
          digest: sha256:abc
      arobit:
        kusto:
          enabled: true
      kvCert: someprincipal
`

func TestUpsertInsertsAndReplaces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "overlay.yaml")
	if err := os.WriteFile(path, []byte(overlaySample), 0o644); err != nil {
		t.Fatal(err)
	}
	ed, err := New(path)
	if err != nil {
		t.Fatal(err)
	}

	// backend has no k8s block -> full insert; two updates share the requests parent.
	// arobit has no forwarder block -> insert with limits too.
	if err := ed.Upsert([]Update{
		{Path: "clouds.public.defaults.backend.k8s.resources.requests.cpu", NewValue: "1780m"},
		{Path: "clouds.public.defaults.backend.k8s.resources.requests.memory", NewValue: "1904Mi"},
		{Path: "clouds.public.defaults.arobit.forwarder.resources.requests.cpu", NewValue: "210m"},
		{Path: "clouds.public.defaults.arobit.forwarder.resources.requests.memory", NewValue: "1408Mi"},
		{Path: "clouds.public.defaults.arobit.forwarder.resources.limits.memory", NewValue: "2816Mi"},
	}); err != nil {
		t.Fatal(err)
	}

	got, _ := os.ReadFile(path)
	want := `clouds:
  public:
    defaults:
      # RP Backend
      backend:
        exitOnPanic: true
        image:
          digest: sha256:abc
        k8s:
          resources:
            requests:
              cpu: 1780m
              memory: 1904Mi
      arobit:
        kusto:
          enabled: true
        forwarder:
          resources:
            requests:
              cpu: 210m
              memory: 1408Mi
            limits:
              memory: 2816Mi
      kvCert: someprincipal
`
	if string(got) != want {
		t.Fatalf("unexpected result:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestUpsertReplacesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.yaml")
	if err := os.WriteFile(path, []byte(sample), 0o644); err != nil {
		t.Fatal(err)
	}
	ed, _ := New(path)
	if err := ed.Upsert([]Update{
		{Path: "defaults.backend.k8s.resources.requests.cpu", NewValue: "250m"},
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), "cpu: 250m") {
		t.Fatalf("expected cpu: 250m, got:\n%s", got)
	}
	// memory line and its comment must be untouched.
	if !strings.Contains(string(got), "memory: 1Gi # keep this comment") {
		t.Fatalf("adjacent line disturbed:\n%s", got)
	}
}
