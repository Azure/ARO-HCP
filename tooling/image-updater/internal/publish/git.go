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
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// gitOps is the git surface publication needs, injectable for tests.
type gitOps struct {
	// stagedPaths returns the paths already staged in the index.
	stagedPaths func(ctx context.Context) ([]string, error)
	// commit stages only paths, refuses unrelated staged content, and reports what it committed.
	commit func(ctx context.Context, message, name, email string, paths []string) ([]string, error)
	// head returns the commit that will be published.
	head func(ctx context.Context) (string, error)
	// revParse resolves a revision that must exist in the checkout.
	revParse func(ctx context.Context, rev string) (string, error)
	// changedFiles lists the files that differ between from and HEAD.
	changedFiles func(ctx context.Context, from string) ([]string, error)
	// remoteTip returns the current tip of ref on the remote, or "" when absent.
	remoteTip func(ctx context.Context, ref string) (string, error)
	// push publishes HEAD to branch while pinning the expected remote tip.
	push func(ctx context.Context, branch, expectedTip string) error
}

// defaultGit runs git in repository and pushes to headRepo with token authentication.
func defaultGit(repository, headRepo string, token TokenSource) gitOps {
	return gitOpsFor(repository, "https://github.com/"+headRepo+".git", token)
}

// gitOpsFor runs git in repository against remoteURL.
func gitOpsFor(repository, remoteURL string, token TokenSource) gitOps {
	run := func(ctx context.Context, args ...string) (string, error) {
		command := exec.CommandContext(ctx, "git", append([]string{"-C", repository}, args...)...)
		var stderr strings.Builder
		command.Stderr = &stderr
		out, err := command.Output()
		if err != nil {
			return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
		}
		return strings.TrimSpace(string(out)), nil
	}
	stagedPaths := func(ctx context.Context) ([]string, error) {
		out, err := run(ctx, "diff", "--cached", "--name-only")
		if err != nil {
			return nil, err
		}
		return splitLines(out), nil
	}
	return gitOps{
		stagedPaths: stagedPaths,
		commit: func(ctx context.Context, message, name, email string, paths []string) ([]string, error) {
			if len(paths) == 0 {
				return nil, fmt.Errorf("no publishable paths were provided")
			}
			staged, err := stagedPaths(ctx)
			if err != nil {
				return nil, err
			}
			if len(staged) > 0 {
				return nil, fmt.Errorf("the index already has staged changes: %s", strings.Join(staged, ", "))
			}
			if _, err := run(ctx, append([]string{"add", "-A", "--"}, paths...)...); err != nil {
				return nil, err
			}
			committed, err := stagedPaths(ctx)
			if err != nil {
				return nil, err
			}
			if err := onlyIntended(committed, paths); err != nil {
				return nil, err
			}
			if len(committed) == 0 {
				return nil, nil
			}
			if _, err := run(ctx, "-c", "user.name="+name, "-c", "user.email="+email, "commit", "--quiet", "--message", message); err != nil {
				return nil, err
			}
			return committed, nil
		},
		head: func(ctx context.Context) (string, error) {
			return run(ctx, "rev-parse", "HEAD")
		},
		revParse: func(ctx context.Context, rev string) (string, error) {
			return run(ctx, "rev-parse", rev+"^{commit}")
		},
		changedFiles: func(ctx context.Context, from string) ([]string, error) {
			out, err := run(ctx, "diff", "--name-only", from, "HEAD")
			if err != nil {
				return nil, err
			}
			return splitLines(out), nil
		},
		remoteTip: func(ctx context.Context, ref string) (string, error) {
			out, err := run(ctx, "ls-remote", remoteURL, "refs/heads/"+ref)
			if err != nil {
				return "", err
			}
			fields := strings.Fields(out)
			if len(fields) == 0 {
				return "", nil
			}
			return fields[0], nil
		},
		push: func(ctx context.Context, branch, expectedTip string) error {
			value, err := token(ctx)
			if err != nil {
				return err
			}
			askpass, err := createAskpass(value)
			if err != nil {
				return err
			}
			defer func() { _ = os.Remove(askpass) }()
			args := []string{
				"-c", "credential.helper=",
				"push", "--force-with-lease=refs/heads/" + branch + ":" + expectedTip,
				remoteURL, "HEAD:refs/heads/" + branch,
			}
			command := exec.CommandContext(ctx, "git", append([]string{"-C", repository}, args...)...)
			command.Env = append(os.Environ(), "GIT_ASKPASS="+askpass, "GITHUB_TOKEN="+value, "GIT_TERMINAL_PROMPT=0")
			if out, err := command.CombinedOutput(); err != nil {
				return fmt.Errorf("push %s: %w: %s", branch, err, strings.TrimSpace(string(out)))
			}
			return nil
		},
	}
}

// onlyIntended rejects a staged set that includes a path outside the intended list.
func onlyIntended(committed, intended []string) error {
	allowed := make(map[string]struct{}, len(intended))
	for _, path := range intended {
		allowed[path] = struct{}{}
	}
	for _, path := range committed {
		if _, ok := allowed[path]; !ok {
			return fmt.Errorf("%s is outside the publishable paths", path)
		}
	}
	return nil
}

// createAskpass writes the helper that feeds the installation token to git.
func createAskpass(token string) (string, error) {
	file, err := os.CreateTemp("", "askpass-*.sh")
	if err != nil {
		return "", fmt.Errorf("create the askpass helper: %w", err)
	}
	if _, err := file.WriteString("#!/bin/sh\nprintf '%s' \"${GITHUB_TOKEN}\"\n"); err != nil {
		return "", fmt.Errorf("write the askpass helper: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close the askpass helper: %w", err)
	}
	if err := os.Chmod(file.Name(), 0o700); err != nil {
		return "", fmt.Errorf("secure the askpass helper: %w", err)
	}
	return file.Name(), nil
}

// splitLines drops blank entries from git output.
func splitLines(out string) []string {
	lines := make([]string, 0, strings.Count(out, "\n")+1)
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
