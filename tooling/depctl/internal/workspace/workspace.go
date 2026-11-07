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
	if inside(absolute, checkout.Path) {
		return nil, errors.New("candidate directory must be outside the source checkout")
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		return nil, fmt.Errorf("create candidate parent: %w", err)
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
func inside(path, root string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return relative == "." || (!strings.HasPrefix(relative, ".."+string(filepath.Separator)) && relative != "..")
}

// maxEditBytes bounds a file depctl will rewrite.
const maxEditBytes = 4 << 20

// SetVersionVariable rewrites exactly one `NAME ?= value` assignment in a file.
//
// The current repository tool-version script performs the same in-place rewrite;
// this keeps the edit deterministic without rediscovering "latest" mid-run.
func SetVersionVariable(path, name, value string) error {
	if !variablePattern.MatchString(name) {
		return fmt.Errorf("invalid make variable name %q", name)
	}
	if strings.ContainsAny(value, " \t\"'`$\\") || value == "" {
		return fmt.Errorf("invalid value for %s", name)
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
	temporary, err := os.CreateTemp(filepath.Dir(path), ".depctl-edit-")
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
	return os.Rename(temporary.Name(), path)
}

// OpenExisting wraps an existing candidate worktree created by Prepare.
//
// It verifies the base revision without creating or removing anything, so the
// standalone verify and proposal commands can operate on an apply result.
func OpenExisting(ctx context.Context, path, base string) (*Candidate, error) {
	root, err := repository.Open(path)
	if err != nil {
		return nil, err
	}
	head, err := root.Head(ctx)
	if err != nil {
		return nil, err
	}
	if head != base {
		return nil, fmt.Errorf("candidate head %s does not match base %s", head, base)
	}
	return &Candidate{Root: root, Base: base, path: root.Path}, nil
}

// ErrCannotEdit reports a refused edit.
var ErrCannotEdit = errors.New("edit is not permitted")
