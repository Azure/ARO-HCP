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

package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/Azure/ARO-HCP/tooling/image-updater/internal/command"
)

// newTestRepo initialises an empty git repository with a committed file, and
// returns a Repo for it. Exercising the real git binary is deliberate: these
// helpers exist to encode git's actual behaviour, so mocking it would test
// nothing.
func newTestRepo(t *testing.T) *Repo {
	t.Helper()

	dir := t.TempDir()
	runner := command.NewRunner(dir, nil)
	ctx := context.Background()

	for _, args := range [][]string{
		{"init", "--initial-branch", "main"},
		{"config", "--local", "user.name", "test"},
		{"config", "--local", "user.email", "test@example.com"},
		{"config", "--local", "commit.gpgsign", "false"},
	} {
		if err := runner.Run(ctx, "git", args...); err != nil {
			t.Fatalf("failed to set up test repo (git %s): %v", strings.Join(args, " "), err)
		}
	}

	writeFile(t, dir, "seed.txt", "seed\n")
	if err := runner.Run(ctx, "git", "add", "--all"); err != nil {
		t.Fatalf("failed to stage seed file: %v", err)
	}
	if err := runner.Run(ctx, "git", "commit", "--message", "seed"); err != nil {
		t.Fatalf("failed to commit seed file: %v", err)
	}

	return New(dir, nil)
}

func writeFile(t *testing.T, dir, name, contents string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("failed to create parent dir for %s: %v", name, err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("failed to write %s: %v", name, err)
	}
}

func TestDiscoverFromSubdirectory(t *testing.T) {
	// This is the real arrangement: the binary runs from
	// tooling/image-updater and must find the repository root above it.
	repo := newTestRepo(t)
	sub := filepath.Join(repo.Root(), "tooling", "image-updater")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("failed to create subdirectory: %v", err)
	}

	discovered, err := Discover(context.Background(), sub, nil)
	if err != nil {
		t.Fatalf("Discover returned an unexpected error: %v", err)
	}

	want, err := filepath.EvalSymlinks(repo.Root())
	if err != nil {
		t.Fatalf("failed to resolve repo root: %v", err)
	}
	got, err := filepath.EvalSymlinks(discovered.Root())
	if err != nil {
		t.Fatalf("failed to resolve discovered root: %v", err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("unexpected discovered root (-want +got):\n%s", diff)
	}
}

func TestDiscoverOutsideRepository(t *testing.T) {
	// Replaces the script's `[[ ! -d ".git" ]]` preflight.
	dir := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))

	if _, err := Discover(context.Background(), dir, nil); err == nil {
		t.Error("expected an error outside a git repository, got nil")
	}
}

func TestIsDirty(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	dirty, err := repo.IsDirty(ctx)
	if err != nil {
		t.Fatalf("IsDirty returned an unexpected error: %v", err)
	}
	if dirty {
		t.Error("a freshly committed tree reported dirty")
	}

	// Untracked files must count: a bump can add a rendered file that git
	// has never seen, and `git status --porcelain` lists those.
	writeFile(t, repo.Root(), "untracked.txt", "new\n")
	dirty, err = repo.IsDirty(ctx)
	if err != nil {
		t.Fatalf("IsDirty returned an unexpected error: %v", err)
	}
	if !dirty {
		t.Error("a tree with an untracked file reported clean")
	}
}

func TestCommitAll(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	writeFile(t, repo.Root(), "nested/dir/file.txt", "content\n")
	writeFile(t, repo.Root(), "seed.txt", "modified\n")

	committed, err := repo.CommitAll(ctx, "chore: execute image-updater for all components")
	if err != nil {
		t.Fatalf("CommitAll returned an unexpected error: %v", err)
	}
	if !committed {
		t.Fatal("CommitAll reported no commit despite a dirty tree")
	}

	dirty, err := repo.IsDirty(ctx)
	if err != nil {
		t.Fatalf("IsDirty returned an unexpected error: %v", err)
	}
	if dirty {
		t.Error("tree is still dirty after CommitAll")
	}

	subject, err := repo.Runner().Output(ctx, "git", "log", "-1", "--pretty=%s")
	if err != nil {
		t.Fatalf("failed to read commit subject: %v", err)
	}
	if diff := cmp.Diff("chore: execute image-updater for all components", subject); diff != "" {
		t.Errorf("unexpected commit subject (-want +got):\n%s", diff)
	}
}

func TestCommitAllOnCleanTree(t *testing.T) {
	// The ACM render step often produces nothing. That has to be a quiet
	// no-op, not an error, or every such run would fail.
	repo := newTestRepo(t)
	ctx := context.Background()

	before, err := repo.Runner().Output(ctx, "git", "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("failed to read HEAD: %v", err)
	}

	committed, err := repo.CommitAll(ctx, "chore: render ACM helm-charts")
	if err != nil {
		t.Fatalf("CommitAll on a clean tree returned an error: %v", err)
	}
	if committed {
		t.Error("CommitAll reported a commit on a clean tree")
	}

	after, err := repo.Runner().Output(ctx, "git", "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("failed to read HEAD: %v", err)
	}
	if before != after {
		t.Errorf("HEAD moved on a clean tree: %s -> %s", before, after)
	}
}

func TestConfigureIdentity(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	if err := repo.ConfigureIdentity(ctx, "aro-hcp-robot[bot]", "aro-hcp-robot[bot]@users.noreply.github.com"); err != nil {
		t.Fatalf("ConfigureIdentity returned an unexpected error: %v", err)
	}

	for _, tc := range []struct{ key, want string }{
		{"user.name", "aro-hcp-robot[bot]"},
		{"user.email", "aro-hcp-robot[bot]@users.noreply.github.com"},
		{"commit.gpgsign", "false"},
	} {
		got, err := repo.Runner().Output(ctx, "git", "config", "--local", "--get", tc.key)
		if err != nil {
			t.Fatalf("failed to read %s: %v", tc.key, err)
		}
		if diff := cmp.Diff(tc.want, got); diff != "" {
			t.Errorf("unexpected %s (-want +got):\n%s", tc.key, diff)
		}
	}
}

func TestCheckoutBranch(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	branch := BranchNameFromTitle("Automated - Update component image digests")

	if err := repo.CheckoutBranch(ctx, branch); err != nil {
		t.Fatalf("CheckoutBranch returned an unexpected error: %v", err)
	}
	current, err := repo.Runner().Output(ctx, "git", "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		t.Fatalf("failed to read current branch: %v", err)
	}
	if diff := cmp.Diff(branch, current); diff != "" {
		t.Errorf("unexpected current branch (-want +got):\n%s", diff)
	}

	// -B semantics: re-running against an existing branch must reset it
	// rather than fail, since every scheduled run reuses the same name.
	if err := repo.CheckoutBranch(ctx, branch); err != nil {
		t.Errorf("re-checking out an existing branch failed: %v", err)
	}
}

func TestCommitsCarryOntoCheckedOutBranch(t *testing.T) {
	// The pipeline commits on the current branch first and only then cuts
	// the PR branch, exactly as prcreator did. Assert those commits come
	// along, since the whole PR content depends on it.
	repo := newTestRepo(t)
	ctx := context.Background()

	writeFile(t, repo.Root(), "digest.txt", "sha256:abc\n")
	if _, err := repo.CommitAll(ctx, "chore: execute image-updater for all components"); err != nil {
		t.Fatalf("CommitAll returned an unexpected error: %v", err)
	}
	if err := repo.CheckoutBranch(ctx, "automated---update-component-image-digests"); err != nil {
		t.Fatalf("CheckoutBranch returned an unexpected error: %v", err)
	}

	subject, err := repo.Runner().Output(ctx, "git", "log", "-1", "--pretty=%s")
	if err != nil {
		t.Fatalf("failed to read commit subject: %v", err)
	}
	if diff := cmp.Diff("chore: execute image-updater for all components", subject); diff != "" {
		t.Errorf("commit did not carry onto the branch (-want +got):\n%s", diff)
	}
}

func TestPush(t *testing.T) {
	// Push against a local bare repo standing in for GitHub, so the refspec
	// and force behaviour are exercised without network access.
	repo := newTestRepo(t)
	ctx := context.Background()

	remoteDir := t.TempDir()
	if err := command.NewRunner(remoteDir, nil).Run(ctx, "git", "init", "--bare"); err != nil {
		t.Fatalf("failed to create bare remote: %v", err)
	}

	branch := "automated---update-component-image-digests"
	if err := repo.Push(ctx, remoteDir, branch); err != nil {
		t.Fatalf("Push returned an unexpected error: %v", err)
	}

	remote := command.NewRunner(remoteDir, nil)
	got, err := remote.Output(ctx, "git", "log", "-1", "--pretty=%s", branch)
	if err != nil {
		t.Fatalf("failed to read pushed branch: %v", err)
	}
	if diff := cmp.Diff("seed", got); diff != "" {
		t.Errorf("unexpected pushed commit (-want +got):\n%s", diff)
	}

	// A second run must be able to overwrite the branch; that force-push is
	// why AROSLSRE-1736 needs a Tide precheck in front of it.
	writeFile(t, repo.Root(), "second.txt", "second\n")
	if _, err := repo.CommitAll(ctx, "second"); err != nil {
		t.Fatalf("CommitAll returned an unexpected error: %v", err)
	}
	if err := repo.Push(ctx, remoteDir, branch); err != nil {
		t.Fatalf("force push returned an unexpected error: %v", err)
	}
	got, err = remote.Output(ctx, "git", "log", "-1", "--pretty=%s", branch)
	if err != nil {
		t.Fatalf("failed to read pushed branch: %v", err)
	}
	if diff := cmp.Diff("second", got); diff != "" {
		t.Errorf("force push did not update the branch (-want +got):\n%s", diff)
	}
}

func TestPushErrorIsCensored(t *testing.T) {
	// Push failures echo the remote URL, which carries the token.
	repo := New(newTestRepo(t).Root(), command.NewCensor("ghs_tokenvalue"))
	url := "https://x-access-token:ghs_tokenvalue@127.0.0.1:1/Azure/ARO-HCP.git"

	err := repo.Push(context.Background(), url, "branch")
	if err == nil {
		t.Fatal("expected an error pushing to an unreachable remote, got nil")
	}
	if strings.Contains(err.Error(), "ghs_tokenvalue") {
		t.Errorf("token leaked into the push error: %v", err)
	}
}

func TestBranchNameFromTitle(t *testing.T) {
	for _, tc := range []struct {
		name  string
		title string
		want  string
	}{
		{
			name: "the live job title, which must keep matching prcreator",
			// Any drift here orphans the open bump PR and opens a second.
			title: "Automated - Update component image digests",
			want:  "automated---update-component-image-digests",
		},
		{
			name:  "colons become dashes",
			title: "chore: bump images",
			want:  "chore--bump-images",
		},
		{
			name:  "already lowercase and dashed",
			title: "bump-all",
			want:  "bump-all",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if diff := cmp.Diff(tc.want, BranchNameFromTitle(tc.title)); diff != "" {
				t.Errorf("unexpected branch name (-want +got):\n%s", diff)
			}
		})
	}
}

func TestMain(m *testing.M) {
	if len(command.MissingTools("git")) > 0 {
		os.Exit(0)
	}
	os.Exit(m.Run())
}
