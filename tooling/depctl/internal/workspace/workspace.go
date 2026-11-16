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

// Package workspace prepares and edits the disposable candidate tree.
package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/repository"
)

// Candidate is an isolated worktree that a run may edit and then discard.
type Candidate struct {
	Root *repository.Repo
	Base string
	path string
	// removed records that cleanup already ran so that Close is idempotent.
	removed bool
}

// Prepare creates a detached worktree of base under directory.
func Prepare(ctx context.Context, checkout *repository.Repo, directory, base string) (*Candidate, error) {
	if !repository.IsCommit(base) {
		return nil, fmt.Errorf("base must be a full commit SHA, got %q", base)
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("resolve candidate path: %w", err)
	}
	// Resolve the destination before creating anything, so a refused path
	// leaves no directory behind inside the source checkout.
	realCheckout, err := filepath.EvalSymlinks(checkout.Path)
	if err != nil {
		return nil, fmt.Errorf("resolve source checkout: %w", err)
	}
	prospective, err := resolveProspectivePath(absolute)
	if err != nil {
		return nil, err
	}
	if inside(prospective, realCheckout) {
		return nil, errors.New("candidate directory must be outside the source checkout")
	}
	if err := createContainedDirectories(filepath.Dir(absolute), realCheckout); err != nil {
		return nil, err
	}
	realParent, err := filepath.EvalSymlinks(filepath.Dir(absolute))
	if err != nil {
		return nil, fmt.Errorf("resolve candidate parent: %w", err)
	}
	absolute = filepath.Join(realParent, filepath.Base(absolute))
	if inside(absolute, realCheckout) {
		return nil, errors.New("candidate directory must be outside the source checkout")
	}
	if err := checkout.AddWorktree(ctx, absolute, base); err != nil {
		return nil, fmt.Errorf("create candidate worktree: %w", err)
	}
	candidate, err := repository.Open(absolute)
	if err != nil {
		_ = checkout.RemoveWorktree(ctx, absolute)
		return nil, fmt.Errorf("open candidate worktree: %w", err)
	}
	head, err := candidate.Head(ctx)
	if err != nil {
		_ = checkout.RemoveWorktree(ctx, absolute)
		return nil, err
	}
	if head != base {
		_ = checkout.RemoveWorktree(ctx, absolute)
		return nil, fmt.Errorf("candidate head %s does not match base %s", head, base)
	}
	return &Candidate{Root: candidate, Base: base, path: absolute}, nil
}

// Path returns the candidate working-tree path.
func (c *Candidate) Path() string { return c.path }

// Close removes the worktree. Only the candidate's own scratch directory is
// touched; the source checkout is never modified.
// Removes the worktree. Only the candidate's own scratch directory is
// touched; the source checkout is never modified.
func (c *Candidate) Close(ctx context.Context, checkout *repository.Repo) error {
	if c.removed {
		return nil
	}
	c.removed = true
	if err := checkout.RemoveWorktree(ctx, c.path); err != nil {
		return err
	}
	return nil
}

// Reports whether a path is inside a root directory.
// resolveProspectivePath resolves the deepest existing ancestor of a path that
// does not exist yet, so containment can be judged without creating anything.
func resolveProspectivePath(path string) (string, error) {
	current := path
	var missing []string
	for {
		if _, err := os.Lstat(current); err == nil {
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", fmt.Errorf("resolve %s: %w", current, err)
			}
			parts := append([]string{resolved}, missing...)
			return filepath.Join(parts...), nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("stat %s: %w", current, err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("no existing ancestor for %s", path)
		}
		missing = append([]string{filepath.Base(current)}, missing...)
		current = parent
	}
}

// missingComponents lists the path components that do not exist yet.
func missingComponents(path string) []string {
	var missing []string
	for current := path; ; current = filepath.Dir(current) {
		if _, err := os.Lstat(current); err == nil {
			break
		}
		missing = append([]string{filepath.Base(current)}, missing...)
		if filepath.Dir(current) == current {
			break
		}
	}
	return missing
}

// createContainedDirectories creates the missing parents one level at a time,
// refusing a component that resolves inside the source checkout and removing
// what it created.
func createContainedDirectories(directory, sourceRoot string) error {
	missing := missingComponents(directory)
	if len(missing) == 0 {
		return nil
	}
	ancestor := directory
	for range missing {
		ancestor = filepath.Dir(ancestor)
	}
	created := make([]string, 0, len(missing))
	current := ancestor
	for _, component := range missing {
		current = filepath.Join(current, component)
		if err := os.Mkdir(current, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
			removeDirectories(created)
			return fmt.Errorf("create candidate parent: %w", err)
		}
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			removeDirectories(created)
			return fmt.Errorf("%w: parent %s is not a real directory", ErrCannotEdit, current)
		}
		resolved, err := filepath.EvalSymlinks(current)
		if err != nil || inside(resolved, sourceRoot) {
			removeDirectories(created)
			return fmt.Errorf("%w: parent %s resolves inside the source checkout", ErrCannotEdit, current)
		}
		created = append(created, current)
	}
	return nil
}

// removeDirectories removes directories created for a refused candidate.
func removeDirectories(created []string) {
	for index := len(created) - 1; index >= 0; index-- {
		_ = os.Remove(created[index])
	}
}

func inside(path, root string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return relative == "." || (!strings.HasPrefix(relative, ".."+string(filepath.Separator)) && relative != "..")
}

// maxEditBytes bounds a file depctl will rewrite.
const maxEditBytes = 4 << 20

// ConfinePath resolves a plan-supplied relative path inside root.
//
// It rejects absolute paths, traversal, empty paths and symlinked ancestors
// whose target leaves root, so an edit can never reach outside the candidate.
func ConfinePath(root, relative string) (string, error) {
	if relative == "" {
		return "", fmt.Errorf("%w: empty path", ErrCannotEdit)
	}
	if filepath.IsAbs(relative) {
		return "", fmt.Errorf("%w: absolute path %q", ErrCannotEdit, relative)
	}
	if canonical := path.Clean(relative); canonical != relative {
		return "", fmt.Errorf("%w: %q is not a canonical relative path", ErrCannotEdit, relative)
	}
	cleaned := filepath.FromSlash(relative)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: traversal path %q", ErrCannotEdit, relative)
	}
	if cleaned == "." {
		return "", fmt.Errorf("%w: path %q selects a directory", ErrCannotEdit, relative)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve candidate root: %w", err)
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(filepath.Join(resolvedRoot, cleaned)))
	if err != nil {
		return "", fmt.Errorf("%w: resolve parent of %q: %v", ErrCannotEdit, relative, err)
	}
	if !inside(parent, resolvedRoot) {
		return "", fmt.Errorf("%w: %q resolves outside the candidate", ErrCannotEdit, relative)
	}
	target := filepath.Join(parent, filepath.Base(cleaned))
	if info, err := os.Lstat(target); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%w: %q is a symlink", ErrCannotEdit, relative)
	}
	return target, nil
}

// SetVersionVariable rewrites exactly one `NAME ?= value` assignment in a file.
//
// The current repository tool-version script performs the same in-place rewrite;
// this keeps the edit deterministic without rediscovering "latest" mid-run.
func SetVersionVariable(path, name, value string) error {
	if !variablePattern.MatchString(name) {
		return fmt.Errorf("invalid make variable name %q", name)
	}
	if err := validateEditValue(value); err != nil {
		return fmt.Errorf("invalid value for %s: %w", name, err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if len(content) > maxEditBytes {
		return fmt.Errorf("%s exceeds %d bytes", path, maxEditBytes)
	}
	assignment := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(name) + `\s*\?=\s*(\S+)\s*$`)
	matches := assignment.FindAllSubmatchIndex(content, -1)
	if len(matches) != 1 {
		return fmt.Errorf("expected exactly one %s assignment in %s, found %d", name, path, len(matches))
	}
	start, end := matches[0][0], matches[0][1]
	updated := append([]byte{}, content[:start]...)
	updated = append(updated, []byte(name+" ?= "+value)...)
	updated = append(updated, content[end:]...)
	return replaceFile(path, updated)
}

var variablePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// editValuePattern admits only characters that appear in a version or digest.
// Line terminators, Make syntax and shell metacharacters are rejected here so a
// version field stays data even when a plan record is decoded from outside.
var editValuePattern = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._+-]*$`)

// Rejects any value that could alter generated file structure.
func validateEditValue(value string) error {
	if !editValuePattern.MatchString(value) {
		return fmt.Errorf("%q is not a plain version or digest", value)
	}
	return nil
}

// SetDockerfileArg rewrites exactly one `ARG NAME=value` default in a file.
//
// The CI image Dockerfile and versions.mk must agree, so a tool bump edits both.
func SetDockerfileArg(path, name, value string) error {
	if !variablePattern.MatchString(name) {
		return fmt.Errorf("invalid ARG name %q", name)
	}
	if err := validateEditValue(value); err != nil {
		return fmt.Errorf("invalid value for %s: %w", name, err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if len(content) > maxEditBytes {
		return fmt.Errorf("%s exceeds %d bytes", path, maxEditBytes)
	}
	assignment := regexp.MustCompile(`(?m)^ARG ` + regexp.QuoteMeta(name) + `=(\S+)\s*$`)
	matches := assignment.FindAllSubmatchIndex(content, -1)
	if len(matches) != 1 {
		return fmt.Errorf("expected exactly one ARG %s default in %s, found %d", name, path, len(matches))
	}
	start, end := matches[0][0], matches[0][1]
	updated := append([]byte{}, content[:start]...)
	updated = append(updated, []byte("ARG "+name+"="+value)...)
	updated = append(updated, content[end:]...)
	return replaceFile(path, updated)
}

// Writes content atomically, preserving the existing file mode.
func replaceFile(path string, content []byte) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to rewrite symlink %s", path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("refusing to rewrite non-regular file %s", path)
	}
	directory := filepath.Dir(path)
	before, err := os.Stat(directory)
	if err != nil {
		return fmt.Errorf("stat parent of %s: %w", path, err)
	}
	temporary, err := os.CreateTemp(directory, ".depctl-edit-")
	if err != nil {
		return fmt.Errorf("create temporary file for %s: %w", path, err)
	}
	defer func() { _ = os.Remove(temporary.Name()) }()
	if _, err := temporary.Write(content); err != nil {
		temporary.Close()
		return fmt.Errorf("write temporary file for %s: %w", path, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary file for %s: %w", path, err)
	}
	if err := os.Chmod(temporary.Name(), info.Mode().Perm()); err != nil {
		return fmt.Errorf("chmod temporary file for %s: %w", path, err)
	}
	// Refuse a directory swapped underneath the write: renaming into a different
	// parent would escape the confined location.
	after, err := os.Stat(directory)
	if err != nil {
		return fmt.Errorf("stat parent of %s: %w", path, err)
	}
	if !os.SameFile(before, after) {
		return fmt.Errorf("%w: parent of %s changed during the edit", ErrCannotEdit, path)
	}
	return os.Rename(temporary.Name(), path)
}

// OpenExisting wraps a candidate worktree created by Prepare.
//
// The worktree is checked against the recorded candidate commit, not the base
// revision: apply commits its changes, so HEAD is the candidate. Base is kept
// separately because every comparison and patch is taken against it.
func OpenExisting(ctx context.Context, path, commit, base string) (*Candidate, error) {
	root, err := repository.Open(path)
	if err != nil {
		return nil, err
	}
	head, err := root.Head(ctx)
	if err != nil {
		return nil, err
	}
	if !repository.IsCommit(commit) || !repository.IsCommit(base) {
		return nil, fmt.Errorf("%w: record must carry full base and commit revisions", ErrCandidateMismatch)
	}
	if head != commit {
		return nil, fmt.Errorf("%w: head %s is not the recorded commit %s", ErrCandidateMismatch, head, commit)
	}
	return &Candidate{Root: root, Base: base, path: root.Path}, nil
}

// ErrCannotEdit reports a refused edit.
var ErrCannotEdit = errors.New("edit is not permitted")

// AssertOutsideSource refuses a worktree path that resolves inside the source
// checkout, including through symlinked ancestors.
func AssertOutsideSource(candidatePath, sourcePath string) error {
	resolvedCandidate, err := filepath.EvalSymlinks(candidatePath)
	if err != nil {
		return fmt.Errorf("resolve candidate path: %w", err)
	}
	resolvedSource, err := filepath.EvalSymlinks(sourcePath)
	if err != nil {
		return fmt.Errorf("resolve source checkout: %w", err)
	}
	if inside(resolvedCandidate, resolvedSource) {
		return fmt.Errorf("%w: %s resolves inside the source checkout", ErrCandidateMismatch, candidatePath)
	}
	return nil
}

// ErrCandidateMismatch reports a worktree that is not the recorded candidate.
var ErrCandidateMismatch = errors.New("candidate does not match its record")

// BingoHelperPath is the generated helper file a bingo edit produces.
const BingoHelperPath = ".bingo/Variables.mk"
