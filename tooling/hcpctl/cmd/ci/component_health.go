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

package ci

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode"

	"github.com/spf13/cobra"
)

// componentHealthBaseURL is the base location of the published component health
// status files. The component name is appended as the final path segment to
// form the full URL.
const componentHealthBaseURL = "https://gist.githubusercontent.com/deads2k/886f478443c25be174bc8ccdab2aafdc/raw/53a9c528a31884287c71d7d42c71e91f9f3457f6"

// healthyResponse is the exact (trimmed) body a component reports when healthy.
const healthyResponse = "OK"

// maxBodySize caps how many bytes of a component's health response are read. A
// healthy response is tiny ("OK") and unhealthy ones are short status messages,
// so this only guards against an unexpectedly large body. When the body exceeds
// the limit, the truncated content is used for the status message.
const maxBodySize = 1 << 20 // 1 MiB

// validComponents enumerates the components that publish a health status, in the
// canonical order used for deterministic output. A file changed under the
// same-named top-level directory (e.g. "backend/") marks that component as
// affected.
var validComponents = []string{"backend", "frontend", "kube-applier", "fleet"}

// fixCommitPrefixes are the commit-subject prefixes that mark a change as a
// targeted fix. When every commit in the range starts with one of these, the
// component health gate is bypassed.
var fixCommitPrefixes = []string{"fix:", "alert-fix:"}

// httpDoer is the subset of *http.Client used by the component-health command.
// It is defined as an interface so tests can inject a fake or a client pointed
// at an httptest server.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// componentHealthOptions holds the configuration for the component-health
// command.
type componentHealthOptions struct {
	// baseURL is the base URL the component name is appended to. It is a field
	// (rather than a constant) so tests can point it at a local server.
	baseURL string
	// client performs the HTTP request. Injectable for testing.
	client httpDoer
	// out is where component status lines are written.
	out io.Writer
	// baseRef is a git ref (e.g. ${PULL_BASE_SHA}). The files changed in
	// baseRef..HEAD determine which components are health-checked; when every
	// commit in that range is a fix/alert-fix commit, the checks are skipped.
	baseRef string
	// repoDir is the git working tree inspected for changed files and commit
	// subjects. Empty means the process's current working directory (the
	// checked-out source in CI).
	repoDir string
}

// defaultComponentHealthOptions returns options wired to the real health
// endpoint, a client with a sane timeout, and stdout.
func defaultComponentHealthOptions() *componentHealthOptions {
	return &componentHealthOptions{
		baseURL: componentHealthBaseURL,
		client:  &http.Client{Timeout: 30 * time.Second},
		out:     os.Stdout,
	}
}

// newComponentHealthCommand builds the "component-health" subcommand.
func newComponentHealthCommand() (*cobra.Command, error) {
	opts := defaultComponentHealthOptions()

	cmd := &cobra.Command{
		Use:   "component-health",
		Short: "Check the published CI health status of the ARO-HCP components changed in a pull request",
		Long: `component-health reports the current CI health status of the ARO-HCP
components affected by a change.

It inspects the files changed in <base-ref>..HEAD and maps each one to a
component by its top-level directory (backend/, frontend/, kube-applier/,
fleet/). For every affected component it fetches the published status and exits
successfully only when all of them are healthy ("OK"). When any affected
component is not healthy, every component's status is printed and the command
exits non-zero, making it suitable as a CI gate.

If the change touches no component directories (for example a tooling- or
docs-only change), there is nothing to gate on and the command exits 0.

The commit subjects in <base-ref>..HEAD are also inspected: if every commit on
the branch is a fix commit (its subject starts with "fix:" or "alert-fix:"), all
health checks are skipped and the command exits 0 without contacting the
endpoint. Wire the gate into presubmits with --base-ref=${PULL_BASE_SHA} so that
pull requests consisting solely of fixes are never blocked by a component's
health status.`,
		Example:          `  hcpctl ci component-health --base-ref ${PULL_BASE_SHA}`,
		Args:             cobra.NoArgs,
		SilenceUsage:     true,
		SilenceErrors:    true,
		TraverseChildren: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.out = cmd.OutOrStdout()
			return opts.run(cmd.Context())
		},
		CompletionOptions: cobra.CompletionOptions{
			HiddenDefaultCmd: true,
		},
	}

	cmd.Flags().StringVar(&opts.baseRef, "base-ref", "",
		"git ref to diff against (e.g. ${PULL_BASE_SHA}); components changed in <base-ref>..HEAD are health-checked")
	if err := cmd.MarkFlagRequired("base-ref"); err != nil {
		return nil, err
	}

	return cmd, nil
}

// run detects the components affected by the change in baseRef..HEAD and checks
// the published health status of each. It returns nil when every affected
// component reports "OK" (or when there are no affected components, or when the
// change consists solely of fix/alert-fix commits), and an error otherwise.
//
// The fix-commit bypass is evaluated first: if every commit subject in
// baseRef..HEAD starts with "fix:" or "alert-fix:", all checks are skipped.
func (o *componentHealthOptions) run(ctx context.Context) error {
	// Validate the user-supplied base ref before it is interpolated into a git
	// revision argument and handed to git. This must happen before any git
	// command runs.
	if err := validateBaseRef(o.baseRef); err != nil {
		return err
	}

	// Bypass: when every commit in baseRef..HEAD is a fix/alert-fix commit, skip
	// all health checks regardless of which components changed.
	subjects, err := commitSubjects(ctx, o.repoDir, o.baseRef)
	if err != nil {
		return fmt.Errorf("failed to read commit subjects since %s: %w", o.baseRef, err)
	}
	if allFixCommits(subjects) {
		fmt.Fprintln(o.out, "all commits are fix/alert-fix commits, skipping component health check")
		return nil
	}

	// Detect which components are affected by the files changed in baseRef..HEAD.
	components, err := changedComponents(ctx, o.repoDir, o.baseRef)
	if err != nil {
		return fmt.Errorf("failed to detect changed components since %s: %w", o.baseRef, err)
	}
	if len(components) == 0 {
		fmt.Fprintln(o.out, "no component directories changed, skipping component health check")
		return nil
	}

	// Check the health of every affected component, printing each result and
	// collecting the unhealthy ones so all failures are reported together.
	var unhealthy []string
	for _, c := range components {
		healthy, status := o.checkComponent(ctx, c)
		fmt.Fprintf(o.out, "%s: %s\n", c, status)
		if !healthy {
			unhealthy = append(unhealthy, c)
		}
	}

	if len(unhealthy) > 0 {
		return fmt.Errorf("component(s) not healthy: %s", strings.Join(unhealthy, ", "))
	}
	return nil
}

// validateBaseRef guards the user-supplied --base-ref value before it is
// interpolated into a git revision argument ("<base-ref>..HEAD") and passed to
// `git diff`/`git log`. The value is user-controlled, so it is validated to
// prevent git from misinterpreting it:
//
//   - An empty value is rejected (--base-ref is required).
//   - A value starting with "-" is rejected, since git would treat the derived
//     "<base-ref>..HEAD" argument as an option rather than a revision.
//   - A value containing any whitespace is rejected; git refnames never contain
//     whitespace, and allowing it risks the argument being split or otherwise
//     mishandled.
//
// A legitimate ref (branch name, tag, or commit SHA such as ${PULL_BASE_SHA})
// always passes these checks.
func validateBaseRef(baseRef string) error {
	if baseRef == "" {
		return fmt.Errorf("--base-ref is required")
	}
	if strings.HasPrefix(baseRef, "-") {
		return fmt.Errorf("invalid --base-ref %q: must not start with '-'", baseRef)
	}
	if strings.ContainsFunc(baseRef, unicode.IsSpace) {
		return fmt.Errorf("invalid --base-ref %q: must not contain whitespace", baseRef)
	}
	return nil
}

// checkComponent fetches the published health status for a single component. It
// returns whether the component is healthy along with a human-readable status
// line: "OK" when healthy, the reported body when unhealthy, or a description of
// why the endpoint could not be queried.
func (o *componentHealthOptions) checkComponent(ctx context.Context, component string) (bool, string) {
	url := fmt.Sprintf("%s/%s", strings.TrimRight(o.baseURL, "/"), component)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, fmt.Sprintf("failed to build health check request: %v", err)
	}

	resp, err := o.client.Do(req)
	if err != nil {
		return false, fmt.Sprintf("failed to query health: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize))
	if err != nil {
		return false, fmt.Sprintf("failed to read health response: %v", err)
	}
	trimmed := strings.TrimSpace(string(body))

	if resp.StatusCode != http.StatusOK {
		if trimmed == "" {
			return false, fmt.Sprintf("unexpected HTTP status %d", resp.StatusCode)
		}
		return false, fmt.Sprintf("unexpected HTTP status %d: %s", resp.StatusCode, trimmed)
	}

	if trimmed == healthyResponse {
		return true, healthyResponse
	}

	if trimmed == "" {
		return false, "empty health response"
	}
	return false, trimmed
}

// changedComponents returns the components affected by the files changed in
// baseRef..HEAD, determined by matching each changed file path against the known
// component directory prefixes. The result preserves the canonical order of
// validComponents and contains no duplicates.
func changedComponents(ctx context.Context, repoDir, baseRef string) ([]string, error) {
	files, err := changedFiles(ctx, repoDir, baseRef)
	if err != nil {
		return nil, err
	}

	affected := make(map[string]bool, len(validComponents))
	for _, f := range files {
		for _, c := range validComponents {
			if strings.HasPrefix(f, c+"/") {
				affected[c] = true
				// A file path maps to at most one component, so stop scanning.
				break
			}
		}
	}

	var components []string
	for _, c := range validComponents {
		if affected[c] {
			components = append(components, c)
		}
	}
	return components, nil
}

// changedFiles returns the paths of files changed in baseRef..HEAD (files
// reachable from HEAD but not from baseRef), via `git diff --name-only`. repoDir
// selects the git working tree; an empty repoDir uses the current working
// directory.
func changedFiles(ctx context.Context, repoDir, baseRef string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "diff", "--name-only", fmt.Sprintf("%s..HEAD", baseRef))
	cmd.Dir = repoDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git diff --name-only %s..HEAD failed: %w: %s", baseRef, err, strings.TrimSpace(stderr.String()))
	}

	var files []string
	for _, line := range strings.Split(stdout.String(), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

// allFixCommits reports whether there is at least one commit and every commit
// subject starts with one of the fix prefixes ("fix:" or "alert-fix:"). An
// empty set of subjects returns false so an empty commit range proceeds to the
// normal health check.
func allFixCommits(subjects []string) bool {
	if len(subjects) == 0 {
		return false
	}
	for _, s := range subjects {
		if !hasFixPrefix(s) {
			return false
		}
	}
	return true
}

// hasFixPrefix reports whether the commit subject starts with any fix prefix.
func hasFixPrefix(subject string) bool {
	subject = strings.TrimSpace(subject)
	for _, p := range fixCommitPrefixes {
		if strings.HasPrefix(subject, p) {
			return true
		}
	}
	return false
}

// commitSubjects returns the subject line of every commit in baseRef..HEAD
// (commits reachable from HEAD but not from baseRef), most-recent first. repoDir
// selects the git working tree; an empty repoDir uses the current working
// directory.
func commitSubjects(ctx context.Context, repoDir, baseRef string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "log", "--format=%s", fmt.Sprintf("%s..HEAD", baseRef))
	cmd.Dir = repoDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git log %s..HEAD failed: %w: %s", baseRef, err, strings.TrimSpace(stderr.String()))
	}

	var subjects []string
	for _, line := range strings.Split(stdout.String(), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			subjects = append(subjects, line)
		}
	}
	return subjects, nil
}
