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

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Git operations are real, against isolated local repositories. Only GitHub's
// auth/fork/PR boundary is simulated; no test can access the network.
type workflowFixture struct {
	t                    *testing.T
	upstream, fork, temp string
	failure              string
	prHead               string
	prDraft, prCreated   bool
}

func newWorkflowFixture(t *testing.T, mode, retries string) *workflowFixture {
	t.Helper()
	root := t.TempDir()
	for key, value := range map[string]string{
		"GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": os.DevNull, "GIT_CONFIG_COUNT": "0",
		"GIT_AUTHOR_NAME": "Incident test", "GIT_AUTHOR_EMAIL": "incident@example.invalid",
		"GIT_COMMITTER_NAME": "Incident test", "GIT_COMMITTER_EMAIL": "incident@example.invalid",
		"GIT_ALLOW_PROTOCOL": "file", "GIT_TERMINAL_PROMPT": "0",
	} {
		t.Setenv(key, value)
	}
	f := &workflowFixture{t: t, upstream: filepath.Join(root, "upstream"), fork: filepath.Join(root, "renamed-fork.git"), temp: filepath.Join(root, "temporary checkouts")}
	if err := os.Mkdir(f.temp, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", f.temp)
	f.git("", "init", "-b", "main", f.upstream)
	writeConfigs(t, f.upstream, []byte(fmt.Sprintf(pipelineFixture, mode)), []byte(fmt.Sprintf(retesterFixture, retries)))
	if err := os.WriteFile(filepath.Join(f.upstream, "unrelated.txt"), []byte("leave this unchanged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.git(f.upstream, "add", ".")
	f.git(f.upstream, "commit", "-m", "upstream baseline")
	f.git("", "clone", "--bare", f.upstream, f.fork)
	return f
}

func (f *workflowFixture) git(directory string, args ...string) string {
	f.t.Helper()
	output, err := run(context.Background(), directory, "git", args...)
	if err != nil {
		f.t.Fatal(err)
	}
	return output
}

func argument(args []string, name string) string {
	for i, arg := range args {
		if arg == name && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func (f *workflowFixture) command(ctx context.Context, directory, name string, args ...string) (string, error) {
	if name == "git" {
		if args[0] == "clone" {
			// Redirect the public upstream to a local repository; preserve all
			// other clone options and the helper's chosen temporary location.
			args[len(args)-2] = f.upstream
			output, err := run(ctx, directory, name, args...)
			if err == nil && f.failure == "commit" {
				hook := filepath.Join(args[len(args)-1], ".git", "hooks", "pre-commit")
				err = os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o755)
			}
			return output, err
		}
		return run(ctx, directory, name, args...)
	}
	if name != "gh" {
		return "", fmt.Errorf("unexpected executable %s", name)
	}
	switch {
	case len(args) >= 2 && args[0] == "auth" && args[1] == "status":
		if f.failure == "auth" {
			return "", errors.New("authentication unavailable")
		}
		return "", nil
	case args[0] == "api":
		if f.failure == "fork" {
			return "", errors.New("fork creation denied")
		}
		data, err := json.Marshal(map[string]any{"clone_url": f.fork, "full_name": "operator/renamed-release", "owner": map[string]string{"login": "operator"}})
		return string(data), err
	case len(args) >= 2 && args[0] == "pr" && args[1] == "create":
		f.prHead = argument(args, "--head")
		f.prDraft = slices.Contains(args, "--draft")
		_, branch, ok := strings.Cut(f.prHead, ":")
		if !ok {
			return "", errors.New("PR head must identify the fork owner and branch")
		}
		// Like GitHub, reject a PR whose head was never published.
		if _, err := run(ctx, f.fork, "git", "rev-parse", "--verify", "refs/heads/"+branch); err != nil {
			return "", fmt.Errorf("PR head not pushed: %w", err)
		}
		if f.failure == "pr" {
			return "", errors.New("PR service unavailable")
		}
		f.prCreated = true
		return "https://github.com/openshift/release/pull/1", nil
	default:
		return "", fmt.Errorf("unexpected GitHub operation: %v", args)
	}
}

func (f *workflowFixture) assertCleanedUp() {
	f.t.Helper()
	entries, err := os.ReadDir(f.temp)
	if err != nil {
		f.t.Fatal(err)
	}
	if len(entries) != 0 {
		f.t.Fatalf("temporary checkout was not removed: %v", entries)
	}
}

func TestWorkflowPublishesPairedChangeAsDraft(t *testing.T) {
	for _, action := range []string{"enable", "disable"} {
		t.Run(action, func(t *testing.T) {
			mode, retries, desiredMode, desiredRetries := "auto", "true", "manual", "false"
			if action == "disable" {
				mode, retries, desiredMode, desiredRetries = "manual", "false", "auto", "true"
			}
			f := newWorkflowFixture(t, mode, retries)
			if err := createPR(context.Background(), action, f.command); err != nil {
				t.Fatal(err)
			}
			if !f.prCreated || !f.prDraft {
				t.Fatal("workflow did not create a draft PR")
			}
			_, branch, _ := strings.Cut(f.prHead, ":")
			if got := f.git(f.fork, "show", branch+":"+pipelinePath); got != strings.TrimSpace(fmt.Sprintf(pipelineFixture, desiredMode)) {
				t.Fatalf("wrong published pipeline config:\n%s", got)
			}
			if got := f.git(f.fork, "show", branch+":"+retesterPath); got != strings.TrimSpace(fmt.Sprintf(retesterFixture, desiredRetries)) {
				t.Fatalf("wrong published retester config:\n%s", got)
			}
			changed := strings.Fields(f.git(f.fork, "diff", "--name-only", "main..."+branch))
			if !slices.Equal(changed, []string{pipelinePath, retesterPath}) {
				t.Fatalf("published unexpected file changes: %v", changed)
			}
			assertConfigs(t, f.upstream, []byte(fmt.Sprintf(pipelineFixture, mode)), []byte(fmt.Sprintf(retesterFixture, retries)))
			f.assertCleanedUp()
		})
	}
}

func TestWorkflowFailurePublicationBoundaries(t *testing.T) {
	for _, failure := range []string{"auth", "validation", "commit", "fork", "push", "pr"} {
		t.Run(failure, func(t *testing.T) {
			mode, retries := "auto", "true"
			if failure == "validation" {
				mode = "manual" // mixed state must not reach a push
			}
			f := newWorkflowFixture(t, mode, retries)
			f.failure = failure
			if failure == "push" {
				if err := os.WriteFile(filepath.Join(f.fork, "hooks", "pre-receive"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			err := createPR(context.Background(), "enable", f.command)
			if err == nil || f.prCreated {
				t.Fatalf("failed workflow created a PR: err=%v", err)
			}
			branches := f.git(f.fork, "for-each-ref", "--format=%(refname:short)", "refs/heads/ci/")
			if failure == "pr" {
				_, branch, _ := strings.Cut(f.prHead, ":")
				if branches != branch || branch == "" {
					t.Fatalf("pushed recovery branch missing or duplicate: %q", branches)
				}
				if !strings.Contains(err.Error(), "Branch is already pushed") || !strings.Contains(err.Error(), "--head "+f.prHead+" --draft") {
					t.Fatalf("missing recovery instructions for the actual published head: %v", err)
				}
			} else if branches != "" || f.prHead != "" {
				t.Fatalf("pre-publication failure left a branch or requested a PR: branches=%q head=%q", branches, f.prHead)
			}
			assertConfigs(t, f.upstream, []byte(fmt.Sprintf(pipelineFixture, mode)), []byte(fmt.Sprintf(retesterFixture, retries)))
			f.assertCleanedUp()
		})
	}
}
