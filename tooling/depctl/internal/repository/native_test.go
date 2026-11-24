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
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeGitPrivateIndexDisablesExecutableConfiguration(t *testing.T) {
	root := t.TempDir()
	requestGit(t, root, "init", "-b", "main")
	requestGit(t, root, "config", "user.name", "Fixture")
	requestGit(t, root, "config", "user.email", "fixture@example.test")
	for name, text := range map[string]string{
		"go.mod":         "base\n",
		".gitattributes": "go.mod filter=trap\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	requestGit(t, root, "add", ".")
	requestGit(t, root, "commit", "-m", "baseline")
	repo := &Repo{Path: root}
	indexPath := filepath.Join(root, ".git", "index")
	before, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	trap := "touch " + filepath.Join(root, "EXECUTED")
	for _, name := range []string{"filter.trap.clean", "filter.trap.process", "filter.trap.smudge", "core.fsmonitor", "credential.helper"} {
		requestGit(t, root, "config", name, trap)
	}
	hooks := t.TempDir()
	for _, hook := range []string{"pre-commit", "prepare-commit-msg", "commit-msg", "post-commit", "post-checkout"} {
		if err := os.WriteFile(filepath.Join(hooks, hook), []byte("#!/bin/sh\n"+trap+"\nexit 1\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	requestGit(t, root, "config", "core.hooksPath", hooks)
	requestGit(t, root, "config", "commit.gpgSign", "true")
	requestGit(t, root, "config", "gpg.program", filepath.Join(root, "absent-signer"))
	for key, value := range map[string]string{
		"GIT_DIR": "/absent/repository", "GIT_WORK_TREE": "/absent/tree",
		"GIT_INDEX_FILE": "/absent/index", "GIT_CONFIG_COUNT": "1",
		"GIT_CONFIG_KEY_0": "core.fsmonitor", "GIT_CONFIG_VALUE_0": trap,
		"GIT_CONFIG_PARAMETERS": "'core.fsmonitor'='false'",
	} {
		t.Setenv(key, value)
	}
	ctx := context.Background()
	privateIndex := filepath.Join(t.TempDir(), "index")
	if _, err := repo.NativeGit(ctx, nil, privateIndex, "read-tree", "HEAD"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("candidate\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.NativeGit(ctx, nil, privateIndex, "add", "--", "go.mod"); err != nil {
		t.Fatal(err)
	}
	content, err := repo.NativeGit(ctx, nil, privateIndex, "show", ":go.mod")
	if err != nil || string(content) != "candidate\n" {
		t.Fatalf("private staged content %q: %v", content, err)
	}
	after, err := os.ReadFile(indexPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("private Git mutated caller index")
	}
	if _, err := repo.NativeGit(ctx, nil, privateIndex, "commit", "-m", "candidate"); err != nil {
		t.Fatal(err)
	}
	checkout := filepath.Join(t.TempDir(), "checkout")
	if _, err := repo.NativeGit(ctx, nil, "", "worktree", "add", "--detach", checkout, "HEAD"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := repo.NativeGit(ctx, nil, "", "worktree", "remove", "--force", checkout); err != nil {
			t.Error(err)
		}
	}()
	if _, err := os.Stat(filepath.Join(root, "EXECUTED")); !os.IsNotExist(err) {
		t.Fatal("executed Git configuration")
	}
	content, err = repo.NativeGit(ctx, nil, "", "config", "--get-all", "credential.helper")
	if err != nil || !strings.HasSuffix(string(content), "\n\n") {
		t.Fatalf("credential helpers were not reset: %q: %v", content, err)
	}
}

func TestNativeGitInputAndReplacementObjectsAreIsolated(t *testing.T) {
	root := t.TempDir()
	requestGit(t, root, "init", "-q")
	repo := &Repo{Path: root}
	ctx := context.Background()
	original := []byte("original\x00bytes\n")
	first, err := repo.NativeGit(ctx, original, "", "hash-object", "-w", "--stdin")
	if err != nil {
		t.Fatal(err)
	}
	second, err := repo.NativeGit(ctx, []byte("replacement"), "", "hash-object", "-w", "--stdin")
	if err != nil {
		t.Fatal(err)
	}
	object := strings.TrimSpace(string(first))
	requestGit(t, root, "replace", object, strings.TrimSpace(string(second)))
	content, err := repo.NativeGit(ctx, nil, "", "cat-file", "blob", object)
	if err != nil || !bytes.Equal(content, original) {
		t.Fatalf("replacement changed native object bytes: %q: %v", content, err)
	}
}
