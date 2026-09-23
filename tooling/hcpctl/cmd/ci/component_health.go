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

	"github.com/spf13/cobra"
)

// componentHealthBaseURL is the base location of the published component health
// status files. The requested component name is appended as the final path
// segment to form the full URL.
const componentHealthBaseURL = "https://gist.githubusercontent.com/deads2k/886f478443c25be174bc8ccdab2aafdc/raw/53a9c528a31884287c71d7d42c71e91f9f3457f6"

// healthyResponse is the exact (trimmed) body a component reports when healthy.
const healthyResponse = "OK"

// validComponents enumerates the components that publish a health status.
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
	// component is the name of the component to check.
	component string
	// baseURL is the base URL the component name is appended to. It is a field
	// (rather than a constant) so tests can point it at a local server.
	baseURL string
	// client performs the HTTP request. Injectable for testing.
	client httpDoer
	// out is where a non-healthy status body is written.
	out io.Writer
	// baseRef, when non-empty, is a git ref (e.g. ${PULL_BASE_SHA}). The health
	// check is skipped when every commit in baseRef..HEAD is a fix/alert-fix
	// commit; otherwise the normal health check is performed.
	baseRef string
	// repoDir is the git working tree inspected when baseRef is set. Empty means
	// the process's current working directory (the checked-out source in CI).
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
		Short: "Check the published CI health status of an ARO-HCP component",
		Long: `component-health reports the current CI health status of an ARO-HCP component.

It fetches the published status for the requested component and exits
successfully when the component is healthy ("OK"). When the component is not
healthy, the reported status is printed to stdout and the command exits with a
non-zero status, making it suitable as a CI gate.

When --base-ref is provided, the commit subjects in <base-ref>..HEAD are
inspected. If every commit on the branch is a fix commit (its subject starts
with "fix:" or "alert-fix:"), the health check is skipped and the command exits
0 without contacting the endpoint. If any commit is not a fix commit, the normal
health check runs. This lets the gate be wired into presubmits with
--base-ref=${PULL_BASE_SHA} so that pull requests consisting solely of fixes are
never blocked by a component's health status.`,
		Example: `  hcpctl ci component-health --component backend
  hcpctl ci component-health --component kube-applier
  hcpctl ci component-health --component backend --base-ref ${PULL_BASE_SHA}`,
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

	cmd.Flags().StringVar(&opts.component, "component", "",
		fmt.Sprintf("component to check; one of: %s", strings.Join(validComponents, ", ")))
	if err := cmd.MarkFlagRequired("component"); err != nil {
		return nil, err
	}

	cmd.Flags().StringVar(&opts.baseRef, "base-ref", "",
		"git ref (e.g. ${PULL_BASE_SHA}); skip the health check when every commit in <base-ref>..HEAD is a fix:/alert-fix: commit")

	return cmd, nil
}

// isValidComponent reports whether the given component is one we know how to
// check.
func isValidComponent(component string) bool {
	for _, c := range validComponents {
		if c == component {
			return true
		}
	}
	return false
}

// run performs the health check. It returns nil when the component reports
// "OK", and an error otherwise. For an unhealthy (or unexpected) response the
// body is printed to o.out before returning the error.
//
// When o.baseRef is set, the commit subjects in o.baseRef..HEAD are inspected
// first: if every commit is a fix commit (subject starting with "fix:" or
// "alert-fix:"), the health check is skipped and nil is returned.
func (o *componentHealthOptions) run(ctx context.Context) error {
	if !isValidComponent(o.component) {
		return fmt.Errorf("invalid component %q: must be one of %s", o.component, strings.Join(validComponents, ", "))
	}

	if o.baseRef != "" {
		subjects, err := commitSubjects(ctx, o.repoDir, o.baseRef)
		if err != nil {
			return fmt.Errorf("failed to read commit subjects since %s: %w", o.baseRef, err)
		}
		if allFixCommits(subjects) {
			fmt.Fprintln(o.out, "all commits are fix/alert-fix commits, skipping component health check")
			return nil
		}
	}

	url := fmt.Sprintf("%s/%s", strings.TrimRight(o.baseURL, "/"), o.component)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("failed to build health check request for component %q: %w", o.component, err)
	}

	resp, err := o.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to query health for component %q: %w", o.component, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read health response for component %q: %w", o.component, err)
	}
	trimmed := strings.TrimSpace(string(body))

	if resp.StatusCode != http.StatusOK {
		if trimmed != "" {
			fmt.Fprintln(o.out, trimmed)
		}
		return fmt.Errorf("health check for component %q returned unexpected HTTP status %d", o.component, resp.StatusCode)
	}

	if trimmed == healthyResponse {
		return nil
	}

	fmt.Fprintln(o.out, trimmed)
	return fmt.Errorf("component %q is not healthy", o.component)
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
