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
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeDoer is a test double for httpDoer that records the request it received
// and returns a canned response/error.
type fakeDoer struct {
	called  bool
	lastReq *http.Request
	resp    *http.Response
	err     error
}

func (f *fakeDoer) Do(req *http.Request) (*http.Response, error) {
	f.called = true
	f.lastReq = req
	return f.resp, f.err
}

func TestComponentHealthRun(t *testing.T) {
	tests := []struct {
		name            string
		component       string
		statusCode      int
		responseBody    string
		wantErr         bool
		wantOutContains string
		wantEmptyOut    bool
	}{
		{
			name:         "healthy component returns OK",
			component:    "backend",
			statusCode:   http.StatusOK,
			responseBody: "OK",
			wantErr:      false,
			wantEmptyOut: true,
		},
		{
			name:         "healthy component tolerates surrounding whitespace",
			component:    "frontend",
			statusCode:   http.StatusOK,
			responseBody: "  OK\n",
			wantErr:      false,
			wantEmptyOut: true,
		},
		{
			name:            "unhealthy component prints body and errors",
			component:       "kube-applier",
			statusCode:      http.StatusOK,
			responseBody:    "FAIL: pod crashlooping",
			wantErr:         true,
			wantOutContains: "FAIL: pod crashlooping",
		},
		{
			name:            "empty body is treated as unhealthy",
			component:       "fleet",
			statusCode:      http.StatusOK,
			responseBody:    "",
			wantErr:         true,
			wantOutContains: "",
		},
		{
			name:            "non-200 status returns error and prints body",
			component:       "backend",
			statusCode:      http.StatusInternalServerError,
			responseBody:    "500 boom",
			wantErr:         true,
			wantOutContains: "500 boom",
		},
		{
			name:            "not found status returns error",
			component:       "frontend",
			statusCode:      http.StatusNotFound,
			responseBody:    "404 page not found",
			wantErr:         true,
			wantOutContains: "404 page not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Verify the component is appended as the final path segment.
				assert.Equal(t, "/"+tt.component, r.URL.Path, "unexpected request path")
				assert.Equal(t, http.MethodGet, r.Method, "unexpected request method")
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.responseBody))
			}))
			defer server.Close()

			var out bytes.Buffer
			opts := &componentHealthOptions{
				component: tt.component,
				baseURL:   server.URL,
				client:    server.Client(),
				out:       &out,
			}

			err := opts.run(context.Background())

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			if tt.wantEmptyOut {
				assert.Empty(t, out.String(), "healthy component should not print output")
			}
			if tt.wantOutContains != "" {
				assert.Contains(t, out.String(), tt.wantOutContains)
			}
		})
	}
}

func TestComponentHealthRunTrailingSlashBaseURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/backend", r.URL.Path, "trailing slash in base URL must not double up")
		_, _ = w.Write([]byte("OK"))
	}))
	defer server.Close()

	opts := &componentHealthOptions{
		component: "backend",
		baseURL:   server.URL + "/",
		client:    server.Client(),
		out:       &bytes.Buffer{},
	}

	require.NoError(t, opts.run(context.Background()))
}

func TestComponentHealthRunInvalidComponent(t *testing.T) {
	doer := &fakeDoer{}
	opts := &componentHealthOptions{
		component: "does-not-exist",
		baseURL:   componentHealthBaseURL,
		client:    doer,
		out:       &bytes.Buffer{},
	}

	err := opts.run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid component")
	assert.False(t, doer.called, "no HTTP request should be made for an invalid component")
}

func TestComponentHealthRunHTTPError(t *testing.T) {
	doer := &fakeDoer{err: fmt.Errorf("connection refused")}
	opts := &componentHealthOptions{
		component: "backend",
		baseURL:   componentHealthBaseURL,
		client:    doer,
		out:       &bytes.Buffer{},
	}

	err := opts.run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to query health for component")
	assert.True(t, doer.called, "HTTP client should have been invoked")
}

func TestComponentHealthRunBuildsExpectedURL(t *testing.T) {
	doer := &fakeDoer{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("OK")),
		},
	}
	opts := &componentHealthOptions{
		component: "backend",
		baseURL:   "https://example.com/base",
		client:    doer,
		out:       &bytes.Buffer{},
	}

	require.NoError(t, opts.run(context.Background()))
	require.NotNil(t, doer.lastReq)
	assert.Equal(t, "https://example.com/base/backend", doer.lastReq.URL.String())
}

func TestIsValidComponent(t *testing.T) {
	for _, c := range []string{"backend", "frontend", "kube-applier", "fleet"} {
		assert.Truef(t, isValidComponent(c), "%q should be valid", c)
	}
	for _, c := range []string{"", "Backend", "unknown", "kube_applier"} {
		assert.Falsef(t, isValidComponent(c), "%q should be invalid", c)
	}
}

func TestNewComponentHealthCommand(t *testing.T) {
	cmd, err := newComponentHealthCommand()
	require.NoError(t, err)
	require.NotNil(t, cmd)
	assert.Equal(t, "component-health", cmd.Name())

	flag := cmd.Flags().Lookup("component")
	require.NotNil(t, flag, "--component flag must be defined")

	// --component is required.
	err = cmd.ValidateRequiredFlags()
	require.Error(t, err, "expected an error when --component is not set")
	assert.Contains(t, err.Error(), "component")
}

func TestNewCommand(t *testing.T) {
	cmd, err := NewCommand("helper")
	require.NoError(t, err)
	require.NotNil(t, cmd)
	assert.Equal(t, "ci", cmd.Name())
	assert.Equal(t, "helper", cmd.GroupID)

	sub, _, err := cmd.Find([]string{"component-health"})
	require.NoError(t, err)
	assert.Equal(t, "component-health", sub.Name())
}

func TestNewComponentHealthCommandHasBaseRefFlag(t *testing.T) {
	cmd, err := newComponentHealthCommand()
	require.NoError(t, err)

	flag := cmd.Flags().Lookup("base-ref")
	require.NotNil(t, flag, "--base-ref flag must be defined")
	assert.Equal(t, "", flag.DefValue, "--base-ref should default to empty (disabled)")
}

// --- commit-checking logic (exercised against real temporary git repositories) ---

// gitExec runs a git command in dir with an isolated, deterministic identity and
// no host/global config, failing the test on error.
func gitExec(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=test",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test",
		"GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "git %s failed: %s", strings.Join(args, " "), string(out))
	return strings.TrimSpace(string(out))
}

// writeRepoFile writes content to a repo-relative path, creating parent dirs.
func writeRepoFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
}

// newTestRepo initializes a temporary git repo with a single base commit and
// returns the repo directory and the base commit SHA.
func newTestRepo(t *testing.T) (dir, baseSHA string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	dir = t.TempDir()
	gitExec(t, dir, "init")
	writeRepoFile(t, dir, "README.md", "base\n")
	gitExec(t, dir, "add", ".")
	gitExec(t, dir, "commit", "-m", "base commit")
	baseSHA = gitExec(t, dir, "rev-parse", "HEAD")
	return dir, baseSHA
}

// commitWithSubject creates a commit whose subject is exactly subject, writing
// file with content so the working tree has something to commit.
func commitWithSubject(t *testing.T, dir, subject, file, content string) {
	t.Helper()
	writeRepoFile(t, dir, file, content)
	gitExec(t, dir, "add", ".")
	gitExec(t, dir, "commit", "-m", subject)
}

func TestCommitSubjects(t *testing.T) {
	ctx := context.Background()
	dir, baseSHA := newTestRepo(t)

	commitWithSubject(t, dir, "fix: one", "a.txt", "1\n")
	commitWithSubject(t, dir, "feat: two", "b.txt", "2\n")

	subjects, err := commitSubjects(ctx, dir, baseSHA)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"fix: one", "feat: two"}, subjects)
}

func TestCommitSubjectsEmptyRange(t *testing.T) {
	ctx := context.Background()
	dir, baseSHA := newTestRepo(t)

	subjects, err := commitSubjects(ctx, dir, baseSHA)
	require.NoError(t, err)
	assert.Empty(t, subjects, "base..HEAD with no new commits should be empty")
}

func TestCommitSubjectsInvalidRef(t *testing.T) {
	ctx := context.Background()
	dir, _ := newTestRepo(t)

	_, err := commitSubjects(ctx, dir, "does-not-exist-ref")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "git log")
}

func TestAllFixCommits(t *testing.T) {
	tests := []struct {
		name     string
		subjects []string
		want     bool
	}{
		{"all fix", []string{"fix: a", "fix: b"}, true},
		{"all alert-fix", []string{"alert-fix: a", "alert-fix: b"}, true},
		{"fix and alert-fix", []string{"fix: a", "alert-fix: b"}, true},
		{"fix and feat", []string{"fix: a", "feat: b"}, false},
		{"none fix", []string{"feat: a", "chore: b"}, false},
		{"empty", nil, false},
		{"fixup is not a fix commit", []string{"fixup! a"}, false},
		{"leading whitespace tolerated", []string{"  fix: a"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, allFixCommits(tt.subjects))
		})
	}
}

// okDoer returns a fakeDoer that responds with a healthy ("OK") 200 response,
// used by the "proceed" cases to confirm the endpoint is actually contacted.
func okDoer() *fakeDoer {
	return &fakeDoer{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("OK")),
		},
	}
}

// (1) all commits are fix: commits -> skip, endpoint not contacted.
func TestComponentHealthRunBaseRefAllFixCommitsSkips(t *testing.T) {
	ctx := context.Background()
	dir, baseSHA := newTestRepo(t)

	commitWithSubject(t, dir, "fix: correct one", "backend/a.go", "package backend\n")
	commitWithSubject(t, dir, "fix: correct two", "frontend/b.go", "package frontend\n")

	doer := &fakeDoer{}
	var out bytes.Buffer
	opts := &componentHealthOptions{
		component: "backend",
		baseURL:   componentHealthBaseURL,
		client:    doer,
		out:       &out,
		baseRef:   baseSHA,
		repoDir:   dir,
	}

	require.NoError(t, opts.run(ctx), "all fix commits should skip and pass")
	assert.False(t, doer.called, "endpoint must not be contacted when all commits are fixes")
	assert.Contains(t, out.String(), "all commits are fix/alert-fix commits, skipping component health check")
}

// (2) all commits are alert-fix: commits -> skip, endpoint not contacted.
func TestComponentHealthRunBaseRefAllAlertFixCommitsSkips(t *testing.T) {
	ctx := context.Background()
	dir, baseSHA := newTestRepo(t)

	commitWithSubject(t, dir, "alert-fix: silence alert", "backend/a.go", "package backend\n")
	commitWithSubject(t, dir, "alert-fix: tune threshold", "backend/b.go", "package backend2\n")

	doer := &fakeDoer{}
	var out bytes.Buffer
	opts := &componentHealthOptions{
		component: "backend",
		baseURL:   componentHealthBaseURL,
		client:    doer,
		out:       &out,
		baseRef:   baseSHA,
		repoDir:   dir,
	}

	require.NoError(t, opts.run(ctx), "all alert-fix commits should skip and pass")
	assert.False(t, doer.called, "endpoint must not be contacted when all commits are alert-fixes")
	assert.Contains(t, out.String(), "all commits are fix/alert-fix commits, skipping component health check")
}

// (3) mixed commits (a fix and a non-fix) -> proceed with the health check.
func TestComponentHealthRunBaseRefMixedCommitsProceeds(t *testing.T) {
	ctx := context.Background()
	dir, baseSHA := newTestRepo(t)

	commitWithSubject(t, dir, "fix: a real fix", "backend/a.go", "1\n")
	commitWithSubject(t, dir, "feat: a feature", "backend/b.go", "2\n")

	doer := okDoer()
	opts := &componentHealthOptions{
		component: "backend",
		baseURL:   componentHealthBaseURL,
		client:    doer,
		out:       &bytes.Buffer{},
		baseRef:   baseSHA,
		repoDir:   dir,
	}

	require.NoError(t, opts.run(ctx))
	assert.True(t, doer.called, "endpoint must be contacted when commits are mixed")
}

// (4) no fix commits at all -> proceed with the health check.
func TestComponentHealthRunBaseRefNoFixCommitsProceeds(t *testing.T) {
	ctx := context.Background()
	dir, baseSHA := newTestRepo(t)

	commitWithSubject(t, dir, "feat: something new", "backend/a.go", "1\n")
	commitWithSubject(t, dir, "chore: tidy up", "backend/b.go", "2\n")

	doer := okDoer()
	opts := &componentHealthOptions{
		component: "backend",
		baseURL:   componentHealthBaseURL,
		client:    doer,
		out:       &bytes.Buffer{},
		baseRef:   baseSHA,
		repoDir:   dir,
	}

	require.NoError(t, opts.run(ctx))
	assert.True(t, doer.called, "endpoint must be contacted when there are no fix commits")
}

// (5) empty commit range -> proceed with the health check.
func TestComponentHealthRunBaseRefEmptyRangeProceeds(t *testing.T) {
	ctx := context.Background()
	dir, baseSHA := newTestRepo(t)

	// No commits after base; baseSHA..HEAD is empty.
	doer := okDoer()
	opts := &componentHealthOptions{
		component: "backend",
		baseURL:   componentHealthBaseURL,
		client:    doer,
		out:       &bytes.Buffer{},
		baseRef:   baseSHA,
		repoDir:   dir,
	}

	require.NoError(t, opts.run(ctx))
	assert.True(t, doer.called, "empty commit range must proceed to the health check")
}

// A mixed change set that is actually unhealthy fails, printing the body.
func TestComponentHealthRunBaseRefProceedsAndFailsWhenUnhealthy(t *testing.T) {
	ctx := context.Background()
	dir, baseSHA := newTestRepo(t)

	commitWithSubject(t, dir, "feat: risky change", "backend/a.go", "1\n")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/backend", r.URL.Path)
		_, _ = w.Write([]byte("FAIL: broken"))
	}))
	defer server.Close()

	var out bytes.Buffer
	opts := &componentHealthOptions{
		component: "backend",
		baseURL:   server.URL,
		client:    server.Client(),
		out:       &out,
		baseRef:   baseSHA,
		repoDir:   dir,
	}

	err := opts.run(ctx)
	require.Error(t, err, "non-fix commits + unhealthy component should fail")
	assert.Contains(t, out.String(), "FAIL: broken")
}

func TestComponentHealthRunBaseRefGitError(t *testing.T) {
	ctx := context.Background()
	dir, _ := newTestRepo(t)

	doer := &fakeDoer{}
	opts := &componentHealthOptions{
		component: "backend",
		baseURL:   componentHealthBaseURL,
		client:    doer,
		out:       &bytes.Buffer{},
		baseRef:   "bogus-ref-xyz",
		repoDir:   dir,
	}

	err := opts.run(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read commit subjects")
	assert.False(t, doer.called, "health endpoint must not be contacted when the git check errors")
}
