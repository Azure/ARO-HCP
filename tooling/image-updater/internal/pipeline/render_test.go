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

package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/Azure/ARO-HCP/tooling/image-updater/internal/git"
)

// fakeMake installs a `make` shim at the front of PATH for the duration of
// the test. Invoking the repository's real makefiles would pull in azure-cli,
// bicep and a full helm render, so the shim records the arguments it was
// called with instead, and optionally writes a file to simulate a target
// that produced output.
//
// It returns the path of the log file holding one line per invocation.
func fakeMake(t *testing.T, repoRoot string, script string) string {
	t.Helper()

	binDir := t.TempDir()
	logPath := filepath.Join(binDir, "invocations.log")

	shim := fmt.Sprintf(`#!/bin/sh
echo "$@" >> %q
cd %q || exit 1
%s
`, logPath, repoRoot, script)

	shimPath := filepath.Join(binDir, "make")
	if err := os.WriteFile(shimPath, []byte(shim), 0o755); err != nil {
		t.Fatalf("failed to write make shim: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	return logPath
}

func invocations(t *testing.T, logPath string) []string {
	t.Helper()
	contents, err := os.ReadFile(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("failed to read make invocations: %v", err)
	}
	return strings.Split(strings.TrimSpace(string(contents)), "\n")
}

// renderOptions enables both commit and materialize, the combination the CI
// job uses.
func renderOptions() Options {
	opts := defaultOptions()
	opts.Materialize = true
	return opts
}

func TestRenderRunsTargetsInOrder(t *testing.T) {
	repo := newTestRepo(t)
	// Each target writes a distinct file, so both the ACM group and
	// materialize produce a commit.
	log := fakeMake(t, repo.Root(), `
case "$*" in
  "-C acm helm-charts")      echo chart > acm-chart.yaml ;;
  "-C config materialize")   echo rendered > rendered.yaml ;;
esac
`)
	updater := &fakeUpdater{
		root:   repo.Root(),
		writes: map[string]string{"config.yaml": "digest: sha256:new\n"},
	}

	result, err := New(updater, repo, renderOptions()).Run(context.Background())
	if err != nil {
		t.Fatalf("Run returned an unexpected error: %v", err)
	}

	// Order matters: materialize has to run after the ACM render, because
	// it consumes what that produces.
	want := []string{
		"-C acm helm-charts",
		"yamlfmt",
		"update-helm-fixtures",
		"-C config materialize",
	}
	if diff := cmp.Diff(want, invocations(t, log)); diff != "" {
		t.Errorf("unexpected make invocations (-want +got):\n%s", diff)
	}

	wantCommits := []string{DigestCommitMessage, ACMCommitMessage, MaterializeCommitMessage}
	if diff := cmp.Diff(wantCommits, result.Commits); diff != "" {
		t.Errorf("unexpected commits (-want +got):\n%s", diff)
	}

	// Newest first, so the recorded order is reversed, with the seed last.
	wantHistory := []string{MaterializeCommitMessage, ACMCommitMessage, DigestCommitMessage, "seed"}
	if diff := cmp.Diff(wantHistory, commitSubjects(t, repo)); diff != "" {
		t.Errorf("unexpected commit history (-want +got):\n%s", diff)
	}
}

func TestRenderSkipsEmptyACMCommit(t *testing.T) {
	// The ACM render usually produces nothing. That must not create an
	// empty commit in the bump PR.
	repo := newTestRepo(t)
	log := fakeMake(t, repo.Root(), `
case "$*" in
  "-C config materialize") echo rendered > rendered.yaml ;;
esac
`)
	updater := &fakeUpdater{
		root:   repo.Root(),
		writes: map[string]string{"config.yaml": "digest: sha256:new\n"},
	}

	result, err := New(updater, repo, renderOptions()).Run(context.Background())
	if err != nil {
		t.Fatalf("Run returned an unexpected error: %v", err)
	}

	// All four targets still ran; only the commit was skipped.
	if got := len(invocations(t, log)); got != 4 {
		t.Errorf("expected 4 make invocations, got %d", got)
	}
	wantCommits := []string{DigestCommitMessage, MaterializeCommitMessage}
	if diff := cmp.Diff(wantCommits, result.Commits); diff != "" {
		t.Errorf("unexpected commits (-want +got):\n%s", diff)
	}
}

func TestRenderWithoutCommit(t *testing.T) {
	// --materialize without --commit renders but leaves the tree dirty.
	repo := newTestRepo(t)
	log := fakeMake(t, repo.Root(), `echo rendered > rendered-$RANDOM.yaml`)
	updater := &fakeUpdater{
		root:   repo.Root(),
		writes: map[string]string{"config.yaml": "digest: sha256:new\n"},
	}
	opts := renderOptions()
	opts.Commit = false

	result, err := New(updater, repo, opts).Run(context.Background())
	if err != nil {
		t.Fatalf("Run returned an unexpected error: %v", err)
	}

	if got := len(invocations(t, log)); got != 4 {
		t.Errorf("expected 4 make invocations, got %d", got)
	}
	if len(result.Commits) != 0 {
		t.Errorf("expected no commits without --commit, got %v", result.Commits)
	}
	dirty, err := repo.IsDirty(context.Background())
	if err != nil {
		t.Fatalf("IsDirty returned an unexpected error: %v", err)
	}
	if !dirty {
		t.Error("expected the rendered output to be left uncommitted")
	}
}

func TestRenderSkippedWhenNothingChanged(t *testing.T) {
	// No new digests means no render: the expensive targets must not run.
	repo := newTestRepo(t)
	log := fakeMake(t, repo.Root(), "")
	updater := &fakeUpdater{root: repo.Root()}

	result, err := New(updater, repo, renderOptions()).Run(context.Background())
	if err != nil {
		t.Fatalf("Run returned an unexpected error: %v", err)
	}

	if diff := cmp.Diff(OutcomeNoChanges, result.Outcome); diff != "" {
		t.Errorf("unexpected outcome (-want +got):\n%s", diff)
	}
	if got := invocations(t, log); len(got) != 0 {
		t.Errorf("make ran on a no-change run: %v", got)
	}
}

func TestRenderFailurePropagates(t *testing.T) {
	repo := newTestRepo(t)
	// Fail the second target, so the first has already succeeded.
	fakeMake(t, repo.Root(), `
case "$*" in
  "yamlfmt") echo "yamlfmt: parse error" >&2; exit 2 ;;
esac
`)
	updater := &fakeUpdater{
		root:   repo.Root(),
		writes: map[string]string{"config.yaml": "digest: sha256:new\n"},
	}

	_, err := New(updater, repo, renderOptions()).Run(context.Background())
	if err == nil {
		t.Fatal("expected an error when a make target fails, got nil")
	}
	if !strings.Contains(err.Error(), "make yamlfmt") {
		t.Errorf("error does not name the failing target: %v", err)
	}
	// Output has to survive, or diagnosing a CI failure means re-running
	// the whole job at a higher verbosity.
	if !strings.Contains(err.Error(), "yamlfmt: parse error") {
		t.Errorf("error does not include the target's output: %v", err)
	}
}

func TestRenderRunsAtRepositoryRoot(t *testing.T) {
	// The binary runs from tooling/image-updater, but every make target is
	// defined relative to the repository root.
	repo := newTestRepo(t)
	sub := filepath.Join(repo.Root(), "tooling", "image-updater")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("failed to create subdirectory: %v", err)
	}

	binDir := t.TempDir()
	cwdLog := filepath.Join(binDir, "cwd.log")
	shim := fmt.Sprintf("#!/bin/sh\npwd >> %q\n", cwdLog)
	if err := os.WriteFile(filepath.Join(binDir, "make"), []byte(shim), 0o755); err != nil {
		t.Fatalf("failed to write make shim: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	discovered, err := git.Discover(context.Background(), sub, nil)
	if err != nil {
		t.Fatalf("Discover returned an unexpected error: %v", err)
	}
	updater := &fakeUpdater{
		root:   repo.Root(),
		writes: map[string]string{"config.yaml": "digest: sha256:new\n"},
	}
	opts := renderOptions()
	opts.Commit = false

	if _, err := New(updater, discovered, opts).Run(context.Background()); err != nil {
		t.Fatalf("Run returned an unexpected error: %v", err)
	}

	wantRoot, err := filepath.EvalSymlinks(repo.Root())
	if err != nil {
		t.Fatalf("failed to resolve repo root: %v", err)
	}
	for _, line := range invocations(t, cwdLog) {
		got, err := filepath.EvalSymlinks(line)
		if err != nil {
			t.Fatalf("failed to resolve reported cwd %q: %v", line, err)
		}
		if diff := cmp.Diff(wantRoot, got); diff != "" {
			t.Errorf("make ran outside the repository root (-want +got):\n%s", diff)
		}
	}
}
