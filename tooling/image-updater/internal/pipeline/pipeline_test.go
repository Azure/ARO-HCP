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
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/Azure/ARO-HCP/tooling/image-updater/internal/command"
	"github.com/Azure/ARO-HCP/tooling/image-updater/internal/git"
)

// fakeUpdater stands in for the real updater. It writes files into the
// working tree the way a digest update does, so the stages after it see a
// realistic dirty tree without any registry access.
type fakeUpdater struct {
	// writes maps a path relative to the repository root to its contents.
	writes map[string]string
	// summary is returned by Summary.
	summary string
	// updateErr, when set, is returned by UpdateImages.
	updateErr error
	// summaryErr, when set, is returned by Summary.
	summaryErr error

	root         string
	updateCalls  int
	summaryCalls int
}

func (f *fakeUpdater) UpdateImages(_ context.Context) error {
	f.updateCalls++
	if f.updateErr != nil {
		return f.updateErr
	}
	for name, contents := range f.writes {
		path := filepath.Join(f.root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeUpdater) Summary(string) (string, error) {
	f.summaryCalls++
	if f.summaryErr != nil {
		return "", f.summaryErr
	}
	return f.summary, nil
}

// newTestRepo creates a git repository with one commit, standing in for a
// fresh CI checkout.
func newTestRepo(t *testing.T) *git.Repo {
	t.Helper()

	dir := t.TempDir()
	runner := command.NewRunner(dir, nil)
	ctx := context.Background()

	for _, args := range [][]string{
		{"init", "--initial-branch", "main"},
		{"config", "--local", "user.name", "seed"},
		{"config", "--local", "user.email", "seed@example.com"},
		{"config", "--local", "commit.gpgsign", "false"},
	} {
		if err := runner.Run(ctx, "git", args...); err != nil {
			t.Fatalf("failed to set up test repo (git %s): %v", strings.Join(args, " "), err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("digest: sha256:old\n"), 0o644); err != nil {
		t.Fatalf("failed to write seed file: %v", err)
	}
	if err := runner.Run(ctx, "git", "add", "--all"); err != nil {
		t.Fatalf("failed to stage seed file: %v", err)
	}
	if err := runner.Run(ctx, "git", "commit", "--message", "seed"); err != nil {
		t.Fatalf("failed to commit seed file: %v", err)
	}

	return git.New(dir, nil)
}

func commitSubjects(t *testing.T, repo *git.Repo) []string {
	t.Helper()
	out, err := repo.Runner().Output(context.Background(), "git", "log", "--pretty=%s")
	if err != nil {
		t.Fatalf("failed to read git log: %v", err)
	}
	return strings.Split(out, "\n")
}

func defaultOptions() Options {
	return Options{
		Commit:   true,
		GitUser:  "aro-hcp-robot[bot]",
		GitEmail: "aro-hcp-robot[bot]@users.noreply.github.com",
	}
}

func TestRunCommitsDigestChanges(t *testing.T) {
	repo := newTestRepo(t)
	updater := &fakeUpdater{
		root:    repo.Root(),
		writes:  map[string]string{"config.yaml": "digest: sha256:new\n"},
		summary: "| Name | Old Digest | New Digest |\n",
	}

	result, err := New(updater, repo, defaultOptions()).Run(context.Background())
	if err != nil {
		t.Fatalf("Run returned an unexpected error: %v", err)
	}

	if diff := cmp.Diff(OutcomeCommitted, result.Outcome); diff != "" {
		t.Errorf("unexpected outcome (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{DigestCommitMessage}, result.Commits); diff != "" {
		t.Errorf("unexpected commits (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("| Name | Old Digest | New Digest |\n", result.Summary); diff != "" {
		t.Errorf("unexpected summary (-want +got):\n%s", diff)
	}

	// The commit subject is what shows up in the bump PR, and must stay
	// identical to the message the bash script used.
	if diff := cmp.Diff([]string{DigestCommitMessage, "seed"}, commitSubjects(t, repo)); diff != "" {
		t.Errorf("unexpected commit history (-want +got):\n%s", diff)
	}

	dirty, err := repo.IsDirty(context.Background())
	if err != nil {
		t.Fatalf("IsDirty returned an unexpected error: %v", err)
	}
	if dirty {
		t.Error("working tree still dirty after a committing run")
	}
}

func TestRunNoChanges(t *testing.T) {
	// A run that finds no new digests is a success, not a failure: the
	// script exits 0 here, and AROSLSRE-1735 needs it distinguishable from
	// both a real update and an error.
	repo := newTestRepo(t)
	updater := &fakeUpdater{root: repo.Root()}

	result, err := New(updater, repo, defaultOptions()).Run(context.Background())
	if err != nil {
		t.Fatalf("Run returned an unexpected error: %v", err)
	}

	if diff := cmp.Diff(OutcomeNoChanges, result.Outcome); diff != "" {
		t.Errorf("unexpected outcome (-want +got):\n%s", diff)
	}
	if len(result.Commits) != 0 {
		t.Errorf("expected no commits, got %v", result.Commits)
	}
	if diff := cmp.Diff([]string{"seed"}, commitSubjects(t, repo)); diff != "" {
		t.Errorf("history changed on a no-op run (-want +got):\n%s", diff)
	}
	// No point rendering a summary of nothing.
	if updater.summaryCalls != 0 {
		t.Errorf("Summary was called %d times on a no-change run", updater.summaryCalls)
	}
}

func TestRunWithoutCommit(t *testing.T) {
	// --commit off: digests land on disk, nothing is committed.
	repo := newTestRepo(t)
	updater := &fakeUpdater{
		root:    repo.Root(),
		writes:  map[string]string{"config.yaml": "digest: sha256:new\n"},
		summary: "summary",
	}
	opts := defaultOptions()
	opts.Commit = false

	result, err := New(updater, repo, opts).Run(context.Background())
	if err != nil {
		t.Fatalf("Run returned an unexpected error: %v", err)
	}

	if diff := cmp.Diff(OutcomeUpdated, result.Outcome); diff != "" {
		t.Errorf("unexpected outcome (-want +got):\n%s", diff)
	}
	if len(result.Commits) != 0 {
		t.Errorf("expected no commits, got %v", result.Commits)
	}

	dirty, err := repo.IsDirty(context.Background())
	if err != nil {
		t.Fatalf("IsDirty returned an unexpected error: %v", err)
	}
	if !dirty {
		t.Error("expected the working tree to be left dirty without --commit")
	}
}

func TestRunConfiguresIdentityBeforeUpdating(t *testing.T) {
	// Identity is configured up front so a bad value fails before the
	// registry round trips, not after several minutes of work.
	repo := newTestRepo(t)
	updater := &fakeUpdater{
		root:   repo.Root(),
		writes: map[string]string{"config.yaml": "digest: sha256:new\n"},
	}

	if _, err := New(updater, repo, defaultOptions()).Run(context.Background()); err != nil {
		t.Fatalf("Run returned an unexpected error: %v", err)
	}

	ctx := context.Background()
	for _, tc := range []struct{ key, want string }{
		{"user.name", "aro-hcp-robot[bot]"},
		{"user.email", "aro-hcp-robot[bot]@users.noreply.github.com"},
	} {
		got, err := repo.Runner().Output(ctx, "git", "config", "--local", "--get", tc.key)
		if err != nil {
			t.Fatalf("failed to read %s: %v", tc.key, err)
		}
		if diff := cmp.Diff(tc.want, got); diff != "" {
			t.Errorf("unexpected %s (-want +got):\n%s", tc.key, diff)
		}
	}

	// The commit must be attributed to the bot, not the ambient identity.
	author, err := repo.Runner().Output(ctx, "git", "log", "-1", "--pretty=%an <%ae>")
	if err != nil {
		t.Fatalf("failed to read commit author: %v", err)
	}
	want := "aro-hcp-robot[bot] <aro-hcp-robot[bot]@users.noreply.github.com>"
	if diff := cmp.Diff(want, author); diff != "" {
		t.Errorf("unexpected commit author (-want +got):\n%s", diff)
	}
}

func TestRunDoesNotConfigureIdentityWithoutCommit(t *testing.T) {
	// Running by hand must not rewrite a developer's git config.
	repo := newTestRepo(t)
	updater := &fakeUpdater{root: repo.Root()}
	opts := defaultOptions()
	opts.Commit = false

	if _, err := New(updater, repo, opts).Run(context.Background()); err != nil {
		t.Fatalf("Run returned an unexpected error: %v", err)
	}

	got, err := repo.Runner().Output(context.Background(), "git", "config", "--local", "--get", "user.name")
	if err != nil {
		t.Fatalf("failed to read user.name: %v", err)
	}
	if diff := cmp.Diff("seed", got); diff != "" {
		t.Errorf("identity was overwritten without --commit (-want +got):\n%s", diff)
	}
}

func TestRunUpdateFailurePropagates(t *testing.T) {
	repo := newTestRepo(t)
	sentinel := errors.New("registry unreachable")
	updater := &fakeUpdater{root: repo.Root(), updateErr: sentinel}

	_, err := New(updater, repo, defaultOptions()).Run(context.Background())
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected the update error to propagate, got %v", err)
	}
	if diff := cmp.Diff([]string{"seed"}, commitSubjects(t, repo)); diff != "" {
		t.Errorf("a failed update left commits behind (-want +got):\n%s", diff)
	}
}

func TestRunSummaryFailurePropagates(t *testing.T) {
	repo := newTestRepo(t)
	sentinel := errors.New("bad format")
	updater := &fakeUpdater{
		root:       repo.Root(),
		writes:     map[string]string{"config.yaml": "digest: sha256:new\n"},
		summaryErr: sentinel,
	}

	_, err := New(updater, repo, defaultOptions()).Run(context.Background())
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected the summary error to propagate, got %v", err)
	}
}

func TestMain(m *testing.M) {
	if len(command.MissingTools("git")) > 0 {
		os.Exit(0)
	}
	os.Exit(m.Run())
}
