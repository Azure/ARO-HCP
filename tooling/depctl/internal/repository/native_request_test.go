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

package repository

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func requestGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(sanitizedEnvironment(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestNativeRequestPatchCompleteAndInert(t *testing.T) {
	root := t.TempDir()
	requestGit(t, root, "init", "-q")
	write := func(name, value string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "base\n")
	write(".gitattributes", "go.mod filter=trap diff=trap\n")
	requestGit(t, root, "add", ".")
	requestGit(t, root, "commit", "-qm", "base")
	base := requestGit(t, root, "rev-parse", "HEAD")
	write("go.mod", "committed\n")
	requestGit(t, root, "commit", "-qam", "candidate")
	write("go.sum", "staged\n")
	requestGit(t, root, "add", "go.sum")
	write("go.mod", "unstaged\n")
	write("untracked", "excluded\n")
	trap := "touch " + filepath.Join(root, "TRAP")
	for _, key := range []string{"filter.trap.clean", "filter.trap.smudge", "filter.trap.process", "diff.trap.command", "diff.trap.textconv", "core.fsmonitor"} {
		requestGit(t, root, "config", key, trap)
	}
	requestGit(t, root, "config", "diff.noprefix", "true")
	requestGit(t, root, "config", "diff.context", "99")
	index := filepath.Join(root, ".git", "index")
	before, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	repo := &Repo{Path: root}
	patch, err := repo.NativeRequestPatch(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	again, err := repo.NativeRequestPatch(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(patch, again) || !bytes.Contains(patch, []byte("diff --git a/go.mod b/go.mod")) || !bytes.Contains(patch, []byte("+unstaged")) || !bytes.Contains(patch, []byte("+staged")) || bytes.Contains(patch, []byte("untracked")) {
		t.Fatalf("unexpected patch: %s", patch)
	}
	after, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("index changed")
	}
	if _, err := os.Stat(filepath.Join(root, "TRAP")); !os.IsNotExist(err) {
		t.Fatal("Git driver executed")
	}
	for _, invalid := range []string{"HEAD", "--help", strings.ToUpper(base), strings.Repeat("f", 40)} {
		if _, err := repo.NativeRequestPatch(context.Background(), invalid); err == nil {
			t.Fatalf("accepted %q", invalid)
		}
	}
}
