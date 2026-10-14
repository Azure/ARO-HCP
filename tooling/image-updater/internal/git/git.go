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

// Package git wraps the git operations the image-updater needs. It shells out
// to the git binary rather than linking a git library, so the behaviour
// matches what an operator reproduces by hand when debugging a bump.
package git

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/ARO-HCP/tooling/image-updater/internal/command"
)

// Repo is a local git working tree.
type Repo struct {
	root   string
	runner *command.Runner
}

// Discover locates the root of the working tree containing dir.
//
// The binary runs from tooling/image-updater, because the image-updater
// config addresses target files relative to that directory, while commits and
// make targets have to happen at the repository root. Asking git for the root
// keeps that from being a hardcoded "../..".
func Discover(ctx context.Context, dir string, censor *command.Censor) (*Repo, error) {
	root, err := command.NewRunner(dir, censor).Output(ctx, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("%q is not inside a git repository: %w", dir, err)
	}
	return New(root, censor), nil
}

// New returns a Repo rooted at the given directory without verifying it.
func New(root string, censor *command.Censor) *Repo {
	return &Repo{root: root, runner: command.NewRunner(root, censor)}
}

// Root returns the path of the working tree root.
func (r *Repo) Root() string {
	return r.root
}

// Runner returns a command runner rooted at the working tree, for callers
// that need to run non-git commands -- the make targets -- in the same place.
func (r *Repo) Runner() *command.Runner {
	return r.runner
}

// ConfigureIdentity sets the local commit identity and disables GPG signing,
// which is never configured in a CI container and would otherwise make every
// commit fail.
func (r *Repo) ConfigureIdentity(ctx context.Context, name, email string) error {
	for _, args := range [][]string{
		{"config", "--local", "user.name", name},
		{"config", "--local", "user.email", email},
		{"config", "--local", "commit.gpgsign", "false"},
	} {
		if err := r.runner.Run(ctx, "git", args...); err != nil {
			return fmt.Errorf("failed to configure git identity: %w", err)
		}
	}
	return nil
}

// Status returns the porcelain status of the working tree.
func (r *Repo) Status(ctx context.Context) (string, error) {
	status, err := r.runner.Output(ctx, "git", "status", "--porcelain")
	if err != nil {
		return "", fmt.Errorf("failed to read git status: %w", err)
	}
	return status, nil
}

// IsDirty reports whether the working tree has changes, including untracked
// files. This is the check that decides whether a bump run found anything.
func (r *Repo) IsDirty(ctx context.Context) (bool, error) {
	status, err := r.Status(ctx)
	if err != nil {
		return false, err
	}
	return status != "", nil
}

// CommitAll stages every change, including untracked files, and commits them.
// It reports whether a commit was actually made: a clean tree is not an
// error, because individual stages legitimately produce no changes -- the ACM
// render step frequently does not.
func (r *Repo) CommitAll(ctx context.Context, message string) (bool, error) {
	dirty, err := r.IsDirty(ctx)
	if err != nil {
		return false, err
	}
	if !dirty {
		return false, nil
	}

	if err := r.runner.Run(ctx, "git", "add", "--all"); err != nil {
		return false, fmt.Errorf("failed to stage changes: %w", err)
	}
	if err := r.runner.Run(ctx, "git", "commit", "--message", message); err != nil {
		return false, fmt.Errorf("failed to commit changes: %w", err)
	}
	return true, nil
}

// CheckoutBranch creates or resets a branch at the current HEAD.
func (r *Repo) CheckoutBranch(ctx context.Context, branch string) error {
	if err := r.runner.Run(ctx, "git", "checkout", "-B", branch); err != nil {
		return fmt.Errorf("failed to create branch %s: %w", branch, err)
	}
	return nil
}

// Push force-pushes HEAD to the named branch on the given remote URL.
//
// The URL carries the credential, so the caller must register that credential
// with the censor this Repo was built with, or git's own error output will
// leak it.
func (r *Repo) Push(ctx context.Context, remoteURL, branch string) error {
	refspec := fmt.Sprintf("HEAD:refs/heads/%s", branch)
	if err := r.runner.Run(ctx, "git", "push", "--force", remoteURL, refspec); err != nil {
		return fmt.Errorf("failed to push branch %s: %w", branch, err)
	}
	return nil
}

// Diff returns the diff between the working tree and the given ref.
func (r *Repo) Diff(ctx context.Context, ref string) (string, error) {
	diff, err := r.runner.Output(ctx, "git", "diff", ref)
	if err != nil {
		return "", fmt.Errorf("failed to diff against %s: %w", ref, err)
	}
	return diff, nil
}

// BranchNameFromTitle derives a branch name from a pull request title.
//
// It reproduces the scheme used by openshift/ci-tools' prcreator so the
// branch this tool pushes to is the one the bash-driven job already used.
// That continuity is what keeps an open bump PR being updated in place
// instead of a second one appearing alongside it on the first run after this
// change ships.
func BranchNameFromTitle(title string) string {
	name := strings.ToLower(title)
	name = strings.ReplaceAll(name, " ", "-")
	name = strings.ReplaceAll(name, ":", "-")
	return name
}
