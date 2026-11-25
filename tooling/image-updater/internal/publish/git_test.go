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

package publish

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runGit runs one git command in repository for the test.
func runGit(t *testing.T, repository string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repository}, args...)...)
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// newRepository creates a repository with one committed file.
func newRepository(t *testing.T) string {
	t.Helper()
	repository := t.TempDir()
	runGit(t, repository, "init", "--quiet")
	runGit(t, repository, "config", "user.name", "test")
	runGit(t, repository, "config", "user.email", "test@example.com")
	writeFile(t, repository, "a.txt", "one\n")
	runGit(t, repository, "add", "a.txt")
	runGit(t, repository, "commit", "--quiet", "--message", "initial")
	return repository
}

// writeFile writes content to one repository file.
func writeFile(t *testing.T, repository, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repository, name), []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// TestGitCommitStagesOnlyIntendedPaths covers the staging contract.
func TestGitCommitStagesOnlyIntendedPaths(t *testing.T) {
	repository := newRepository(t)
	base := runGit(t, repository, "rev-parse", "HEAD")
	ops := gitOpsFor(repository, "https://example.invalid/ARO-HCP.git", func(context.Context) (string, error) {
		return "", os.ErrPermission
	})
	writeFile(t, repository, "a.txt", "two\n")
	writeFile(t, repository, "unrelated.txt", "noise\n")

	committed, err := ops.commit(context.Background(), "chore: update", "bot", "bot@example.com", []string{"a.txt"})
	if err != nil {
		t.Fatalf("commit() error = %v", err)
	}
	if len(committed) != 1 || committed[0] != "a.txt" {
		t.Fatalf("committed = %v, want [a.txt]", committed)
	}
	status := runGit(t, repository, "status", "--porcelain")
	if !strings.Contains(status, "?? unrelated.txt") {
		t.Errorf("unrelated file was touched: %s", status)
	}
	if status = runGit(t, repository, "status", "--porcelain"); strings.Contains(status, "a.txt") {
		t.Errorf("intended file was left unstaged: %s", status)
	}
	files, err := ops.changedFiles(context.Background(), base)
	if err != nil {
		t.Fatalf("changedFiles() error = %v", err)
	}
	if len(files) != 1 || files[0] != "a.txt" {
		t.Errorf("changedFiles() = %v, want [a.txt]", files)
	}
}

// TestGitCommitRefusesPreStagedChanges covers the index guard.
func TestGitCommitRefusesPreStagedChanges(t *testing.T) {
	repository := newRepository(t)
	ops := gitOpsFor(repository, "https://example.invalid/ARO-HCP.git", func(context.Context) (string, error) {
		return "", os.ErrPermission
	})
	writeFile(t, repository, "a.txt", "two\n")
	writeFile(t, repository, "staged.txt", "noise\n")
	runGit(t, repository, "add", "staged.txt")

	_, err := ops.commit(context.Background(), "chore: update", "bot", "bot@example.com", []string{"a.txt"})
	if err == nil || !strings.Contains(err.Error(), "already has staged changes") {
		t.Fatalf("commit() error = %v, want a staged-index refusal", err)
	}
	if status := runGit(t, repository, "status", "--porcelain"); !strings.Contains(status, "A  staged.txt") {
		t.Errorf("the refusal changed the index: %s", status)
	}
}
