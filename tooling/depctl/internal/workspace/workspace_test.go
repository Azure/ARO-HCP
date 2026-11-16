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

package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "versions.mk")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestSetVersionVariableRewritesExactlyOneAssignment(t *testing.T) {
	path := writeFile(t, "# comment\nPROMTOOL_VERSION  ?=  3.15.1\nBICEP_VERSION ?= 0.48.1\n")
	if err := SetVersionVariable(path, "PROMTOOL_VERSION", "3.16.0"); err != nil {
		t.Fatalf("SetVersionVariable() error = %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	if !strings.Contains(string(content), "PROMTOOL_VERSION ?= 3.16.0") {
		t.Fatalf("unexpected content:\n%s", content)
	}
	if !strings.Contains(string(content), "BICEP_VERSION ?= 0.48.1") {
		t.Fatalf("unrelated assignment changed:\n%s", content)
	}
}

func TestSetVersionVariableRejectsAmbiguityAndUnsafeValues(t *testing.T) {
	duplicate := writeFile(t, "TOOL ?= 1.0.0\nTOOL ?= 2.0.0\n")
	if err := SetVersionVariable(duplicate, "TOOL", "3.0.0"); err == nil {
		t.Fatal("ambiguous assignments must be refused")
	}
	absent := writeFile(t, "OTHER ?= 1.0.0\n")
	if err := SetVersionVariable(absent, "TOOL", "3.0.0"); err == nil {
		t.Fatal("a missing assignment must be refused")
	}
	valid := writeFile(t, "TOOL ?= 1.0.0\n")
	if err := SetVersionVariable(valid, "TOOL", "3.0.0; rm -rf /"); err == nil {
		t.Fatal("an unsafe value must be refused")
	}
	if err := SetVersionVariable(valid, "lower_case", "3.0.0"); err == nil {
		t.Fatal("an invalid variable name must be refused")
	}
}

func TestSetVersionVariableRefusesSymlink(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target.mk")
	if err := os.WriteFile(target, []byte("TOOL ?= 1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	link := filepath.Join(directory, "link.mk")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := SetVersionVariable(link, "TOOL", "2.0.0"); err == nil {
		t.Fatal("a symlink must not be rewritten")
	}
}

func TestConfinePathRejectsEscapes(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatalf("create subdirectory: %v", err)
	}
	for _, bad := range []string{"", "..", "../outside", "sub/../../outside", "/etc/passwd", "./sub/manifest"} {
		if _, err := ConfinePath(root, bad); err == nil {
			t.Fatalf("ConfinePath(%q) must be refused", bad)
		}
	}
	if _, err := ConfinePath(root, "sub/file.txt"); err != nil {
		t.Fatalf("ConfinePath() error = %v", err)
	}
	// A symlinked parent that leaves the candidate must not be followed.
	outside := t.TempDir()
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := ConfinePath(root, "escape/file.txt"); err == nil {
		t.Fatal("a symlinked parent outside the candidate must be refused")
	}
}

func TestSetDockerfileArgRewritesExactlyOneDefault(t *testing.T) {
	path := writeFile(t, "FROM base\nARG PROMTOOL_VERSION=3.15.1\nARG BICEP_VERSION=0.48.1\n")
	if err := SetDockerfileArg(path, "PROMTOOL_VERSION", "3.16.0"); err != nil {
		t.Fatalf("SetDockerfileArg() error = %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	if !strings.Contains(string(content), "ARG PROMTOOL_VERSION=3.16.0") {
		t.Fatalf("unexpected content:\n%s", content)
	}
	if !strings.Contains(string(content), "ARG BICEP_VERSION=0.48.1") {
		t.Fatalf("unrelated ARG changed:\n%s", content)
	}
	if err := SetDockerfileArg(path, "PROMTOOL_VERSION", "3.16.0\nRUN false"); err == nil {
		t.Fatal("a multiline ARG value must be refused")
	}
}

func TestConfinePathRejectsSymlinkedTargetFile(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.mk")
	if err := os.WriteFile(outside, []byte("SECRET ?= 1\n"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	link := filepath.Join(root, "versions.mk")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := ConfinePath(root, "versions.mk"); err == nil {
		t.Fatal("a symlinked target file must be refused")
	}
	if err := SetVersionVariable(link, "SECRET", "2"); err == nil {
		t.Fatal("a direct write through a symlink must be refused")
	}
	content, err := os.ReadFile(outside)
	if err != nil {
		t.Fatalf("read outside file: %v", err)
	}
	if string(content) != "SECRET ?= 1\n" {
		t.Fatalf("the file outside the candidate was modified: %q", content)
	}
}

func TestConfinePathRejectsNestedSymlinkedParent(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "versions.mk"), []byte("A ?= 1\n"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "ci"), 0o755); err != nil {
		t.Fatalf("create nested directory: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "ci", "openshift-ci")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := ConfinePath(root, "ci/openshift-ci/versions.mk"); err == nil {
		t.Fatal("a nested symlinked parent outside the candidate must be refused")
	}
}
