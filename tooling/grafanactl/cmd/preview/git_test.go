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
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestBuildPreviewUID pins the truncation to what hack/ci/grafana-preview.sh
// produced. A preview UID that changes between runs would leave a second
// dashboard behind on every push, so the expected values below are the output
// of the shell the Go code replaces.

func TestGetChangedDashboards(t *testing.T) {
	repo := buildTestRepo(t)

	baseSHA := gitOutput(t, repo, "rev-parse", "HEAD")
	gitRun(t, repo, "checkout", "-q", "-b", "feature")

	writeFile(t, repo, "observability/grafana-dashboards/backend/backend.json", `{"uid":"backend","title":"Backend v2"}`)
	writeFile(t, repo, "observability/grafana-dashboards/new/added.json", `{"uid":"added","title":"Added"}`)
	// git quotes a path that holds a non-ASCII character unless it is asked for
	// NUL-separated output, and a quoted path ends with the quote rather than
	// with .json.
	writeFile(t, repo, "observability/grafana-dashboards/new/café.json", `{"uid":"cafe","title":"Café"}`)
	writeFile(t, repo, "observability/grafana-dashboards/notes.md", "ignored")
	writeFile(t, repo, "docs/elsewhere.json", `{"uid":"elsewhere"}`)
	gitRun(t, repo, "rm", "-q", "observability/grafana-dashboards/velero/velero.json")
	gitRun(t, repo, "mv", "observability/grafana-dashboards/istio/istio.json", "observability/grafana-dashboards/istio/renamed.json")
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-q", "-m", "change dashboards")

	headSHA := gitOutput(t, repo, "rev-parse", "HEAD")

	got, err := getChangedDashboards(t.Context(), repo, baseSHA, headSHA, "observability/grafana-dashboards")
	if err != nil {
		t.Fatalf("getChangedDashboards: %v", err)
	}

	want := []string{
		"observability/grafana-dashboards/backend/backend.json",
		"observability/grafana-dashboards/istio/renamed.json",
		"observability/grafana-dashboards/new/added.json",
		"observability/grafana-dashboards/new/café.json",
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("getChangedDashboards = %v, want %v", got, want)
	}
}

func TestGetChangedDashboardsIgnoresCommitsOnTheBaseBranch(t *testing.T) {
	repo := buildTestRepo(t)

	gitRun(t, repo, "checkout", "-q", "-b", "feature")
	writeFile(t, repo, "observability/grafana-dashboards/new/added.json", `{"uid":"added"}`)
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-q", "-m", "add a dashboard")
	headSHA := gitOutput(t, repo, "rev-parse", "HEAD")

	// A dashboard that lands on the base branch after the pull request forked
	// belongs to another pull request, so a three-dot diff must leave it out.
	gitRun(t, repo, "checkout", "-q", "main")
	writeFile(t, repo, "observability/grafana-dashboards/other/other.json", `{"uid":"other"}`)
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-q", "-m", "another pull request")
	movedBaseSHA := gitOutput(t, repo, "rev-parse", "HEAD")

	got, err := getChangedDashboards(t.Context(), repo, movedBaseSHA, headSHA, "observability/grafana-dashboards")
	if err != nil {
		t.Fatalf("getChangedDashboards: %v", err)
	}

	want := []string{"observability/grafana-dashboards/new/added.json"}
	if !slices.Equal(got, want) {
		t.Errorf("getChangedDashboards = %v, want %v", got, want)
	}
}

func TestGetChangedDashboardsReportsNoChange(t *testing.T) {
	repo := buildTestRepo(t)
	head := gitOutput(t, repo, "rev-parse", "HEAD")

	got, err := getChangedDashboards(t.Context(), repo, head, head, "observability/grafana-dashboards")
	if err != nil {
		t.Fatalf("getChangedDashboards: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("getChangedDashboards = %v, want nothing", got)
	}
}

func TestGetChangedDashboardsReportsAnUnknownBase(t *testing.T) {
	repo := buildTestRepo(t)
	head := gitOutput(t, repo, "rev-parse", "HEAD")

	_, err := getChangedDashboards(t.Context(), repo,
		"0000000000000000000000000000000000000000", head, "observability/grafana-dashboards")
	if err == nil {
		t.Fatal("getChangedDashboards succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "fetched deeply enough") {
		t.Errorf("error = %v, want it to mention the fetch depth", err)
	}
}

func TestGetChangedDashboardsReportsAMissingMergeBase(t *testing.T) {
	repo := buildTestRepo(t)
	baseSHA := gitOutput(t, repo, "rev-parse", "HEAD")

	// An orphan branch shares no history with the base.
	gitRun(t, repo, "checkout", "-q", "--orphan", "unrelated")
	gitRun(t, repo, "rm", "-rq", "--cached", ".")
	writeFile(t, repo, "unrelated.txt", "unrelated")
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-q", "-m", "unrelated root")
	headSHA := gitOutput(t, repo, "rev-parse", "HEAD")

	_, err := getChangedDashboards(t.Context(), repo, baseSHA, headSHA, "observability/grafana-dashboards")
	if err == nil {
		t.Fatal("getChangedDashboards succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "no merge base") {
		t.Errorf("error = %v, want it to report the missing merge base", err)
	}
}

func TestGetHeadSHA(t *testing.T) {
	repo := buildTestRepo(t)

	got, err := getHeadSHA(t.Context(), repo)
	if err != nil {
		t.Fatalf("getHeadSHA: %v", err)
	}
	if got != gitOutput(t, repo, "rev-parse", "HEAD") {
		t.Errorf("getHeadSHA = %q, want the checked out commit", got)
	}
}

// TestRunGitKeepsStandardErrorOutOfTheResult covers a caller that parses the
// result: git writes advice and warnings to standard error, and a commit or a
// file list must not come back with one attached.
func TestRunGitKeepsStandardErrorOutOfTheResult(t *testing.T) {
	repo := buildTestRepo(t)

	// A shell alias is the one way to make git write a known line to each stream.
	out, err := runGit(t.Context(), repo,
		"-c", `alias.noisy=!printf 'the-output\n'; printf 'warning: something happened\n' >&2`,
		"noisy")
	if err != nil {
		t.Fatalf("runGit: %v", err)
	}

	if got := strings.TrimSpace(out); got != "the-output" {
		t.Errorf("runGit = %q, want only the standard output", got)
	}
}

func TestRunGitReportsStandardErrorOnFailure(t *testing.T) {
	repo := buildTestRepo(t)

	_, err := runGit(t.Context(), repo, "rev-parse", "no-such-revision")
	if err == nil {
		t.Fatal("runGit succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "no-such-revision") {
		t.Errorf("error = %v, want it to carry what git reported", err)
	}
}

// buildTestRepo creates a git repository holding a few dashboards on main.
func buildTestRepo(t *testing.T) string {
	t.Helper()

	repo := t.TempDir()
	gitRun(t, repo, "init", "-q", "--initial-branch=main")
	gitRun(t, repo, "config", "user.email", "test@example.com")
	gitRun(t, repo, "config", "user.name", "Test")

	writeFile(t, repo, "observability/grafana-dashboards/backend/backend.json", `{"uid":"backend","title":"Backend"}`)
	writeFile(t, repo, "observability/grafana-dashboards/velero/velero.json", `{"uid":"velero","title":"Velero"}`)
	writeFile(t, repo, "observability/grafana-dashboards/istio/istio.json", `{"uid":"istio","title":"Istio"}`)
	writeFile(t, repo, "README.md", "a repository")

	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-q", "-m", "initial dashboards")
	return repo
}

func writeFile(t *testing.T, repo, path, content string) {
	t.Helper()

	full := filepath.Join(repo, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("create %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
}

func gitRun(t *testing.T, repo string, args ...string) {
	t.Helper()
	gitOutput(t, repo, args...)
}

func gitOutput(t *testing.T, repo string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v; output: %s", strings.Join(args, " "), err, string(out))
	}
	return strings.TrimSpace(string(out))
}
