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

// Package repository reads and writes a local Git checkout through plumbing
// commands only. It never runs repository hooks and never inherits Git control
// variables that could redirect a write.
package repository

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Repo is a local checkout.
type Repo struct {
	// Path is the working tree path.
	Path string
}

var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// ErrNotRepository reports a path that is not a Git work tree.
var ErrNotRepository = errors.New("not a git working tree")

// Open verifies that path is the root of a Git working tree.
func Open(path string) (*Repo, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve repository path: %w", err)
	}
	repo := &Repo{Path: absolute}
	top, err := repo.output(context.Background(), "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrNotRepository, absolute)
	}
	if resolved, err := filepath.EvalSymlinks(strings.TrimSpace(top)); err == nil {
		if tree, err := filepath.EvalSymlinks(absolute); err == nil && resolved != tree {
			return nil, fmt.Errorf("%w: %s is not the working-tree root", ErrNotRepository, absolute)
		}
	}
	return repo, nil
}

// IsCommit reports whether value is a full lowercase commit SHA.
func IsCommit(value string) bool { return commitPattern.MatchString(value) }

// Builds a Git invocation with inherited control variables removed so
// that user configuration cannot redirect hooks, refs or the object store.
// Builds a Git invocation with inherited control variables removed so
// that user configuration cannot redirect hooks, refs or the object store.
func (r *Repo) command(ctx context.Context, arguments ...string) *exec.Cmd {
	base := []string{"-C", r.Path, "-c", "core.hooksPath=/dev/null", "--no-optional-locks"}
	command := exec.CommandContext(ctx, "git", append(base, arguments...)...)
	command.Env = sanitizedEnvironment()
	return command
}

// Returns the process environment without Git control variables.
func sanitizedEnvironment() []string {
	drop := map[string]bool{
		"GIT_DIR": true, "GIT_WORK_TREE": true, "GIT_INDEX_FILE": true,
		"GIT_OBJECT_DIRECTORY": true, "GIT_ALTERNATE_OBJECT_DIRECTORIES": true,
		"GIT_NAMESPACE": true, "GIT_COMMON_DIR": true, "GIT_CONFIG": true,
		"GIT_CONFIG_PARAMETERS": true, "GIT_CONFIG_COUNT": true, "GIT_CEILING_DIRECTORIES": true,
		"MAKE": true, "MAKEFLAGS": true, "MFLAGS": true, "GNUMAKEFLAGS": true,
		"MAKEOVERRIDES": true, "MAKEFILES": true, "MAKELEVEL": true,
	}
	var environment []string
	for _, item := range os.Environ() {
		name, _, _ := strings.Cut(item, "=")
		if drop[name] {
			continue
		}
		environment = append(environment, item)
	}
	return environment
}

// Runs Git plumbing and returns its standard output.
func (r *Repo) output(ctx context.Context, arguments ...string) (string, error) {
	var stdout, stderr bytes.Buffer
	command := r.command(ctx, arguments...)
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(arguments, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// Head returns the commit checked out in the working tree.
func (r *Repo) Head(ctx context.Context) (string, error) {
	value, err := r.output(ctx, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	sha := strings.TrimSpace(value)
	if !IsCommit(sha) {
		return "", fmt.Errorf("unexpected HEAD revision %q", sha)
	}
	return sha, nil
}

// Resolves a revision to a commit SHA.
func (r *Repo) Resolve(ctx context.Context, revision string) (string, error) {
	if revision == "" {
		return "", errors.New("revision is required")
	}
	value, err := r.output(ctx, "rev-parse", "--verify", revision+"^{commit}")
	if err != nil {
		return "", err
	}
	sha := strings.TrimSpace(value)
	if !IsCommit(sha) {
		return "", fmt.Errorf("unexpected revision for %q: %q", revision, sha)
	}
	return sha, nil
}

// DirtyPaths returns tracked modifications in the working tree.
func (r *Repo) DirtyPaths(ctx context.Context) ([]string, error) {
	value, err := r.output(ctx, "status", "--porcelain=v1", "--untracked-files=no")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, line := range strings.Split(value, "\n") {
		if len(line) < 4 {
			continue
		}
		paths = append(paths, strings.TrimSpace(line[3:]))
	}
	sort.Strings(paths)
	return paths, nil
}

// ChangedPaths lists paths that differ between base and the working tree,
// including staged and unstaged edits and deletions.
// Lists paths that differ between base and the working tree,
// including staged and unstaged edits and deletions.
func (r *Repo) ChangedPaths(ctx context.Context, base string) ([]string, error) {
	value, err := r.output(ctx, "diff", "--name-only", "--no-renames", base, "--")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, line := range strings.Split(value, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			paths = append(paths, line)
		}
	}
	sort.Strings(paths)
	return paths, nil
}

// Diff returns the binary-safe patch between base and the working tree.
func (r *Repo) Diff(ctx context.Context, base string) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	command := r.command(ctx, "diff", "--binary", "--no-renames", base, "--")
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("git diff: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// TreeDigest digests the tracked tree content: sorted path and blob SHA pairs.
func (r *Repo) TreeDigest(ctx context.Context, base string) (string, error) {
	value, err := r.output(ctx, "ls-tree", "-r", "--full-tree", base)
	if err != nil {
		return "", err
	}
	var entries []string
	for _, line := range strings.Split(value, "\n") {
		fields := strings.SplitN(line, "\t", 2)
		if len(fields) == 2 {
			entries = append(entries, fields[0]+" "+fields[1])
		}
	}
	sort.Strings(entries)
	hash := sha256.New()
	for _, entry := range entries {
		hash.Write([]byte(entry))
		hash.Write([]byte{'\n'})
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

// MaterializedDigest digests the on-disk content of the given paths.
func (r *Repo) MaterializedDigest(paths []string) (map[string]string, error) {
	digests := make(map[string]string, len(paths))
	for _, path := range paths {
		content, err := os.ReadFile(filepath.Join(r.Path, filepath.FromSlash(path)))
		if err != nil {
			if os.IsNotExist(err) {
				digests[path] = "absent"
				continue
			}
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		sum := sha256.Sum256(content)
		digests[path] = "sha256:" + hex.EncodeToString(sum[:])
	}
	return digests, nil
}

// AddWorktree creates a detached worktree at path for ref.
func (r *Repo) AddWorktree(ctx context.Context, path, ref string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("worktree path already exists: %s", path)
	}
	if _, err := r.output(ctx, "worktree", "add", "--detach", path, ref); err != nil {
		// A directory removed outside Git leaves a stale administrative entry
		// that blocks a new worktree at the same path. Pruning only removes
		// registrations whose directory is gone.
		if _, pruneErr := r.output(ctx, "worktree", "prune"); pruneErr != nil {
			return err
		}
		_, err = r.output(ctx, "worktree", "add", "--detach", path, ref)
		return err
	}
	return nil
}

// RemoveWorktree removes a worktree created by AddWorktree.
func (r *Repo) RemoveWorktree(ctx context.Context, path string) error {
	_, err := r.output(ctx, "worktree", "remove", "--force", path)
	return err
}

// TopLevel returns the repository name from the origin or upstream remote URL.
func (r *Repo) TopLevel(ctx context.Context) (string, error) {
	for _, remote := range []string{"upstream", "origin", "fork"} {
		value, err := r.output(ctx, "remote", "get-url", remote)
		if err != nil {
			continue
		}
		if name := repositoryFromURL(strings.TrimSpace(value)); name != "" {
			return name, nil
		}
	}
	return "", errors.New("cannot determine OWNER/REPO from git remotes")
}

var remotePattern = regexp.MustCompile(`(?:github\.com[:/])([A-Za-z0-9][A-Za-z0-9_.-]*)/([A-Za-z0-9][A-Za-z0-9_.-]*?)(?:\.git)?$`)

// Extracts OWNER/REPO from a GitHub remote URL.
func repositoryFromURL(value string) string {
	match := remotePattern.FindStringSubmatch(value)
	if match == nil {
		return ""
	}
	return match[1] + "/" + match[2]
}

// ChangedPathsBetween lists paths that differ between two revisions.
func (r *Repo) ChangedPathsBetween(ctx context.Context, base, head string) ([]string, error) {
	if !IsCommit(base) || !IsCommit(head) {
		return nil, errors.New("both revisions must be full commit SHAs")
	}
	value, err := r.output(ctx, "diff", "--name-only", "--no-renames", base, head, "--")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, line := range strings.Split(value, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			paths = append(paths, line)
		}
	}
	sort.Strings(paths)
	return paths, nil
}
