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

package preview

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// getChangedDashboards lists the dashboard files that a pull request adds,
// copies, modifies or renames, compared with the merge base of the two commits.
// A dashboard the pull request deletes is left out, because there is nothing to
// upload for it.
func getChangedDashboards(ctx context.Context, repoPath, baseSHA, headSHA, dashboardDir string) ([]string, error) {
	if _, err := runGit(ctx, repoPath, "cat-file", "-e", baseSHA+"^{commit}"); err != nil {
		return nil, fmt.Errorf("base commit %s is absent from the git history (is the repository fetched deeply enough?): %w", baseSHA, err)
	}

	if _, err := runGit(ctx, repoPath, "merge-base", baseSHA, headSHA); err != nil {
		return nil, fmt.Errorf("no merge base between %s and %s (is the repository fetched deeply enough?): %w", baseSHA, headSHA, err)
	}

	// -z separates the paths with a NUL and reports each one as it is. Without
	// it, git quotes a path that holds a non-ASCII character, a quote, a
	// backslash or a tab. A quoted path ends with the quote, so the test for
	// the .json suffix below would drop the dashboard without a word.
	out, err := runGit(ctx, repoPath,
		"diff", "-z", "--name-only", "--diff-filter=ACMR", baseSHA+"..."+headSHA, "--", dashboardDir)
	if err != nil {
		return nil, fmt.Errorf("failed to list the dashboards the pull request changed: %w", err)
	}

	var changed []string
	for _, file := range strings.Split(out, "\x00") {
		if strings.HasSuffix(file, ".json") {
			changed = append(changed, file)
		}
	}
	return changed, nil
}

// getHeadSHA reads the commit the repository is checked out at.
func getHeadSHA(ctx context.Context, repoPath string) (string, error) {
	out, err := runGit(ctx, repoPath, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("failed to resolve the checked out commit: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// runGit runs one git command in the given directory and reports what it wrote
// to standard output.
//
// Standard error is kept apart, and reported only when the command fails. git
// writes advice and warnings there, which would otherwise reach the callers
// that read a commit or a file list out of the result.
func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s failed: %w; output: %s",
			strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}
