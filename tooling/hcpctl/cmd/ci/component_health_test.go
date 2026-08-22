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

// newComponentServer starts an httptest server that serves each component's
// health body from bodies, keyed by component name (the final path segment). A
// component absent from the map returns 404. The server is closed on cleanup.
func newComponentServer(t *testing.T, bodies map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method, "unexpected request method")
		name := strings.TrimPrefix(r.URL.Path, "/")
		body, ok := bodies[name]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("404 page not found"))
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// --- checkComponent: single-component HTTP behavior ---

func TestCheckComponent(t *testing.T) {
	tests := []struct {
		name            string
		statusCode      int
		responseBody    string
		wantHealthy     bool
		wantStatusHas   string
		wantStatusExact string
	}{
		{
			name:            "healthy component returns OK",
			statusCode:      http.StatusOK,
			responseBody:    "OK",
			wantHealthy:     true,
			wantStatusExact: "OK",
		},
		{
			name:            "healthy component tolerates surrounding whitespace",
			statusCode:      http.StatusOK,
			responseBody:    "  OK\n",
			wantHealthy:     true,
			wantStatusExact: "OK",
		},
		{
			name:          "unhealthy component reports body",
			statusCode:    http.StatusOK,
			responseBody:  "FAIL: pod crashlooping",
			wantHealthy:   false,
			wantStatusHas: "FAIL: pod crashlooping",
		},
		{
			name:          "empty body is treated as unhealthy",
			statusCode:    http.StatusOK,
			responseBody:  "",
			wantHealthy:   false,
			wantStatusHas: "empty health response",
		},
		{
			name:          "non-200 status is unhealthy and includes body",
			statusCode:    http.StatusInternalServerError,
			responseBody:  "500 boom",
			wantHealthy:   false,
			wantStatusHas: "500 boom",
		},
		{
			name:          "not found status is unhealthy",
			statusCode:    http.StatusNotFound,
			responseBody:  "404 page not found",
			wantHealthy:   false,
			wantStatusHas: "404 page not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/backend", r.URL.Path, "unexpected request path")
				assert.Equal(t, http.MethodGet, r.Method, "unexpected request method")
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.responseBody))
			}))
			defer server.Close()

			opts := &componentHealthOptions{
				baseURL: server.URL,
				client:  server.Client(),
				out:     &bytes.Buffer{},
			}

			healthy, status := opts.checkComponent(context.Background(), "backend")
			assert.Equal(t, tt.wantHealthy, healthy)
			if tt.wantStatusExact != "" {
				assert.Equal(t, tt.wantStatusExact, status)
			}
			if tt.wantStatusHas != "" {
				assert.Contains(t, status, tt.wantStatusHas)
			}
		})
	}
}

func TestCheckComponentTrailingSlashBaseURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/backend", r.URL.Path, "trailing slash in base URL must not double up")
		_, _ = w.Write([]byte("OK"))
	}))
	defer server.Close()

	opts := &componentHealthOptions{
		baseURL: server.URL + "/",
		client:  server.Client(),
		out:     &bytes.Buffer{},
	}

	healthy, status := opts.checkComponent(context.Background(), "backend")
	assert.True(t, healthy)
	assert.Equal(t, "OK", status)
}

func TestCheckComponentHTTPError(t *testing.T) {
	doer := &fakeDoer{err: fmt.Errorf("connection refused")}
	opts := &componentHealthOptions{
		baseURL: componentHealthBaseURL,
		client:  doer,
		out:     &bytes.Buffer{},
	}

	healthy, status := opts.checkComponent(context.Background(), "backend")
	assert.False(t, healthy)
	assert.Contains(t, status, "failed to query health")
	assert.True(t, doer.called, "HTTP client should have been invoked")
}

func TestCheckComponentBuildsExpectedURL(t *testing.T) {
	doer := &fakeDoer{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("OK")),
		},
	}
	opts := &componentHealthOptions{
		baseURL: "https://example.com/base",
		client:  doer,
		out:     &bytes.Buffer{},
	}

	healthy, _ := opts.checkComponent(context.Background(), "kube-applier")
	assert.True(t, healthy)
	require.NotNil(t, doer.lastReq)
	assert.Equal(t, "https://example.com/base/kube-applier", doer.lastReq.URL.String())
}

func TestCheckComponentTruncatesLargeBody(t *testing.T) {
	// A body larger than maxBodySize must be read only up to the limit, and the
	// truncated content is what surfaces in the (unhealthy) status message.
	big := strings.Repeat("A", maxBodySize+1024)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(big))
	}))
	defer server.Close()

	opts := &componentHealthOptions{
		baseURL: server.URL,
		client:  server.Client(),
		out:     &bytes.Buffer{},
	}

	healthy, status := opts.checkComponent(context.Background(), "backend")
	assert.False(t, healthy, "an oversized non-OK body is unhealthy")
	assert.Len(t, status, maxBodySize, "status must be truncated to maxBodySize")
}

// --- changedComponents: mapping changed files to components ---

func TestChangedComponents(t *testing.T) {
	ctx := context.Background()

	t.Run("single component", func(t *testing.T) {
		dir, baseSHA := newTestRepo(t)
		commitWithSubject(t, dir, "feat: backend change", "backend/pkg/foo.go", "package pkg\n")

		got, err := changedComponents(ctx, dir, baseSHA)
		require.NoError(t, err)
		assert.Equal(t, []string{"backend"}, got)
	})

	t.Run("multiple components in canonical order", func(t *testing.T) {
		dir, baseSHA := newTestRepo(t)
		// Commit frontend before backend to prove output order follows the
		// canonical validComponents order, not commit/file order.
		commitWithSubject(t, dir, "feat: frontend change", "frontend/a.go", "package frontend\n")
		commitWithSubject(t, dir, "feat: backend change", "backend/b.go", "package backend\n")
		commitWithSubject(t, dir, "feat: fleet change", "fleet/c.go", "package fleet\n")

		got, err := changedComponents(ctx, dir, baseSHA)
		require.NoError(t, err)
		assert.Equal(t, []string{"backend", "frontend", "fleet"}, got)
	})

	t.Run("dedupes multiple files in one component", func(t *testing.T) {
		dir, baseSHA := newTestRepo(t)
		commitWithSubject(t, dir, "feat: two backend files", "backend/a.go", "1\n")
		commitWithSubject(t, dir, "feat: more backend", "backend/pkg/b.go", "2\n")

		got, err := changedComponents(ctx, dir, baseSHA)
		require.NoError(t, err)
		assert.Equal(t, []string{"backend"}, got)
	})

	t.Run("no component directories changed", func(t *testing.T) {
		dir, baseSHA := newTestRepo(t)
		commitWithSubject(t, dir, "chore: tooling", "tooling/hcpctl/x.go", "1\n")
		commitWithSubject(t, dir, "docs: readme", "docs/readme.md", "hi\n")

		got, err := changedComponents(ctx, dir, baseSHA)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("prefix match requires directory boundary", func(t *testing.T) {
		dir, baseSHA := newTestRepo(t)
		// "backendtools/" must not be mistaken for the "backend" component.
		commitWithSubject(t, dir, "chore: sibling dir", "backendtools/x.go", "1\n")

		got, err := changedComponents(ctx, dir, baseSHA)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("invalid ref errors", func(t *testing.T) {
		dir, _ := newTestRepo(t)
		_, err := changedComponents(ctx, dir, "does-not-exist-ref")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "git diff")
	})
}

// --- run: end-to-end orchestration over a temporary git repo ---

// (1) A single changed component is checked; healthy -> exit 0.
func TestRunSingleComponentHealthy(t *testing.T) {
	ctx := context.Background()
	dir, baseSHA := newTestRepo(t)
	commitWithSubject(t, dir, "feat: backend work", "backend/a.go", "package backend\n")

	server := newComponentServer(t, map[string]string{"backend": "OK"})

	var out bytes.Buffer
	opts := &componentHealthOptions{
		baseURL: server.URL,
		client:  server.Client(),
		out:     &out,
		baseRef: baseSHA,
		repoDir: dir,
	}

	require.NoError(t, opts.run(ctx))
	assert.Contains(t, out.String(), "backend: OK")
	assert.NotContains(t, out.String(), "frontend")
}

// (2) Multiple changed components are all checked; all healthy -> exit 0.
func TestRunMultipleComponentsAllHealthy(t *testing.T) {
	ctx := context.Background()
	dir, baseSHA := newTestRepo(t)
	commitWithSubject(t, dir, "feat: backend", "backend/a.go", "package backend\n")
	commitWithSubject(t, dir, "feat: frontend", "frontend/b.go", "package frontend\n")
	commitWithSubject(t, dir, "feat: kube-applier", "kube-applier/c.go", "package kubeapplier\n")
	commitWithSubject(t, dir, "feat: fleet", "fleet/d.go", "package fleet\n")

	server := newComponentServer(t, map[string]string{
		"backend":      "OK",
		"frontend":     "OK",
		"kube-applier": "OK",
		"fleet":        "OK",
	})

	var out bytes.Buffer
	opts := &componentHealthOptions{
		baseURL: server.URL,
		client:  server.Client(),
		out:     &out,
		baseRef: baseSHA,
		repoDir: dir,
	}

	require.NoError(t, opts.run(ctx))
	for _, c := range []string{"backend", "frontend", "kube-applier", "fleet"} {
		assert.Contains(t, out.String(), c+": OK")
	}
}

// (3) No component directories changed -> skip, endpoint not contacted, exit 0.
func TestRunNoComponentsChanged(t *testing.T) {
	ctx := context.Background()
	dir, baseSHA := newTestRepo(t)
	commitWithSubject(t, dir, "chore: tooling only", "tooling/hcpctl/x.go", "1\n")
	commitWithSubject(t, dir, "docs: update", "docs/readme.md", "hi\n")

	doer := &fakeDoer{}
	var out bytes.Buffer
	opts := &componentHealthOptions{
		baseURL: componentHealthBaseURL,
		client:  doer,
		out:     &out,
		baseRef: baseSHA,
		repoDir: dir,
	}

	require.NoError(t, opts.run(ctx))
	assert.False(t, doer.called, "endpoint must not be contacted when no component dirs changed")
	assert.Contains(t, out.String(), "no component directories changed")
}

// (4) All commits are fix/alert-fix commits -> skip everything, exit 0.
func TestRunFixCommitBypass(t *testing.T) {
	ctx := context.Background()
	dir, baseSHA := newTestRepo(t)
	// Both commits touch a component directory, but the fix-commit bypass wins.
	commitWithSubject(t, dir, "fix: patch backend", "backend/a.go", "package backend\n")
	commitWithSubject(t, dir, "alert-fix: tune frontend", "frontend/b.go", "package frontend\n")

	doer := &fakeDoer{}
	var out bytes.Buffer
	opts := &componentHealthOptions{
		baseURL: componentHealthBaseURL,
		client:  doer,
		out:     &out,
		baseRef: baseSHA,
		repoDir: dir,
	}

	require.NoError(t, opts.run(ctx))
	assert.False(t, doer.called, "endpoint must not be contacted when all commits are fixes")
	assert.Contains(t, out.String(), "all commits are fix/alert-fix commits, skipping component health check")
}

// (5) One component healthy, another unhealthy -> print all, exit non-zero.
func TestRunOneHealthyOneUnhealthy(t *testing.T) {
	ctx := context.Background()
	dir, baseSHA := newTestRepo(t)
	commitWithSubject(t, dir, "feat: backend", "backend/a.go", "package backend\n")
	commitWithSubject(t, dir, "feat: frontend", "frontend/b.go", "package frontend\n")

	server := newComponentServer(t, map[string]string{
		"backend":  "OK",
		"frontend": "FAIL: broken",
	})

	var out bytes.Buffer
	opts := &componentHealthOptions{
		baseURL: server.URL,
		client:  server.Client(),
		out:     &out,
		baseRef: baseSHA,
		repoDir: dir,
	}

	err := opts.run(ctx)
	require.Error(t, err, "an unhealthy component must fail the gate")
	assert.Contains(t, err.Error(), "frontend")
	// All outputs are printed, including the healthy one.
	assert.Contains(t, out.String(), "backend: OK")
	assert.Contains(t, out.String(), "frontend: FAIL: broken")
}

// A single changed component that is unhealthy also fails.
func TestRunSingleComponentUnhealthy(t *testing.T) {
	ctx := context.Background()
	dir, baseSHA := newTestRepo(t)
	commitWithSubject(t, dir, "feat: risky backend change", "backend/a.go", "package backend\n")

	server := newComponentServer(t, map[string]string{"backend": "FAIL: broken"})

	var out bytes.Buffer
	opts := &componentHealthOptions{
		baseURL: server.URL,
		client:  server.Client(),
		out:     &out,
		baseRef: baseSHA,
		repoDir: dir,
	}

	err := opts.run(ctx)
	require.Error(t, err)
	assert.Contains(t, out.String(), "backend: FAIL: broken")
}

// A mix of fix and non-fix commits proceeds to the health check.
func TestRunMixedCommitsProceeds(t *testing.T) {
	ctx := context.Background()
	dir, baseSHA := newTestRepo(t)
	commitWithSubject(t, dir, "fix: a real fix", "backend/a.go", "1\n")
	commitWithSubject(t, dir, "feat: a feature", "backend/b.go", "2\n")

	server := newComponentServer(t, map[string]string{"backend": "OK"})

	var out bytes.Buffer
	opts := &componentHealthOptions{
		baseURL: server.URL,
		client:  server.Client(),
		out:     &out,
		baseRef: baseSHA,
		repoDir: dir,
	}

	require.NoError(t, opts.run(ctx))
	assert.Contains(t, out.String(), "backend: OK")
}

func TestRunRequiresBaseRef(t *testing.T) {
	doer := &fakeDoer{}
	opts := &componentHealthOptions{
		baseURL: componentHealthBaseURL,
		client:  doer,
		out:     &bytes.Buffer{},
	}

	err := opts.run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "base-ref is required")
	assert.False(t, doer.called)
}

// An invalid --base-ref must be rejected before any git command runs or the
// health endpoint is contacted. A leading "-" would otherwise be interpolated
// into "-rf..HEAD" and misinterpreted by git as an option.
func TestRunRejectsInvalidBaseRef(t *testing.T) {
	ctx := context.Background()
	dir, _ := newTestRepo(t)

	doer := &fakeDoer{}
	opts := &componentHealthOptions{
		baseURL: componentHealthBaseURL,
		client:  doer,
		out:     &bytes.Buffer{},
		baseRef: "-rf",
		repoDir: dir,
	}

	err := opts.run(ctx)
	require.Error(t, err)
	// The validation error (not a "git log ... failed" error) proves the check
	// short-circuited before git was invoked.
	assert.Contains(t, err.Error(), "must not start with '-'")
	assert.False(t, doer.called, "health endpoint must not be contacted when base-ref is invalid")
}

func TestRunGitError(t *testing.T) {
	ctx := context.Background()
	dir, _ := newTestRepo(t)

	doer := &fakeDoer{}
	opts := &componentHealthOptions{
		baseURL: componentHealthBaseURL,
		client:  doer,
		out:     &bytes.Buffer{},
		baseRef: "bogus-ref-xyz",
		repoDir: dir,
	}

	err := opts.run(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read commit subjects")
	assert.False(t, doer.called, "health endpoint must not be contacted when the git check errors")
}

// --- base-ref validation ---

func TestValidateBaseRef(t *testing.T) {
	tests := []struct {
		name    string
		baseRef string
		wantErr string // substring the error must contain; "" means no error expected
	}{
		{"valid commit sha", "0123456789abcdef0123456789abcdef01234567", ""},
		{"valid branch name", "main", ""},
		{"valid remote ref with slashes", "origin/release-4.19", ""},
		{"valid tag with dots and dashes", "v4.19.0-rc.1", ""},
		{"empty is rejected", "", "required"},
		{"leading dash is rejected", "-rf", "must not start with '-'"},
		{"option-like value is rejected", "--output=/tmp/pwn", "must not start with '-'"},
		{"leading space is rejected", " main", "must not contain whitespace"},
		{"embedded space is rejected", "main HEAD", "must not contain whitespace"},
		{"trailing newline is rejected", "main\n", "must not contain whitespace"},
		{"embedded tab is rejected", "ma\tin", "must not contain whitespace"},
		{"only whitespace is rejected", "   ", "must not contain whitespace"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateBaseRef(tt.baseRef)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// --- command wiring ---

func TestNewComponentHealthCommand(t *testing.T) {
	cmd, err := newComponentHealthCommand()
	require.NoError(t, err)
	require.NotNil(t, cmd)
	assert.Equal(t, "component-health", cmd.Name())

	// --component must no longer exist.
	assert.Nil(t, cmd.Flags().Lookup("component"), "--component flag must be removed")

	// --base-ref must exist, default empty, and be required.
	flag := cmd.Flags().Lookup("base-ref")
	require.NotNil(t, flag, "--base-ref flag must be defined")
	assert.Equal(t, "", flag.DefValue, "--base-ref should default to empty")

	err = cmd.ValidateRequiredFlags()
	require.Error(t, err, "expected an error when --base-ref is not set")
	assert.Contains(t, err.Error(), "base-ref")
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
