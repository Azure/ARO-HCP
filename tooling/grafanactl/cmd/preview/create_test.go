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
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Azure/ARO-Tools/tools/grafanactl/cmd/base"
	"github.com/Azure/ARO-Tools/tools/grafanactl/cmd/dashboard"
	"github.com/Azure/ARO-Tools/tools/grafanactl/cmd/folder"

	"github.com/Azure/ARO-HCP/tooling/grafanactl/internal/github"
)

const testGrafanaURL = "https://grafana.example.com"

// fakeFolders stands in for the folder command.
type fakeFolders struct {
	info folder.FolderInfo
	err  error

	calls int
}

func (f *fakeFolders) Create(ctx context.Context) (folder.FolderInfo, error) {
	f.calls++
	return f.info, f.err
}

// uploadCall is one dashboard the preview asked to upload.
type uploadCall struct {
	File string
	UID  string
}

// fakeUploader stands in for the dashboard command. It reports the title each
// document carries, as the real upload does when no title overrides it.
type fakeUploader struct {
	err error

	calls []uploadCall
}

func (f *fakeUploader) upload(ctx context.Context, file, uid string) (dashboard.DashboardInfo, error) {
	f.calls = append(f.calls, uploadCall{File: file, UID: uid})
	if f.err != nil {
		return dashboard.DashboardInfo{}, f.err
	}

	_, title, _ := dashboard.GetDashboardIdentity(readTestFile(file))
	return dashboard.DashboardInfo{UID: uid, Title: title, URL: testGrafanaURL + "/d/" + uid}, nil
}

// fakeComments stands in for the GitHub client.
type fakeComments struct {
	err error

	bodies []string
}

func (f *fakeComments) UpsertMarkedComment(ctx context.Context, number int, marker, body string) (github.Comment, error) {
	f.bodies = append(f.bodies, body)
	if f.err != nil {
		return github.Comment{}, f.err
	}
	return github.Comment{ID: 1, Body: body}, nil
}

func TestCreatePreview(t *testing.T) {
	for _, test := range []struct {
		name       string
		dashboards map[string]string
		// absentFiles are reported as changed without being written, as a
		// dashboard deleted between the git diff and the upload would be.
		absentFiles   []string
		dryRun        bool
		withComments  bool
		folderErr     error
		uploadErr     error
		commentErr    error
		wantUploads   []uploadCall
		wantCommented bool
		wantLinks     []string
		wantError     string
	}{
		{
			name: "uploads every changed dashboard and comments",
			dashboards: map[string]string{
				"observability/grafana-dashboards/backend/backend.json":   `{"uid":"backend","title":"Backend"}`,
				"observability/grafana-dashboards/frontend/frontend.json": `{"uid":"frontend","title":"Frontend"}`,
			},
			withComments: true,
			wantUploads: []uploadCall{
				{File: "observability/grafana-dashboards/backend/backend.json", UID: "pr-1234-backend"},
				{File: "observability/grafana-dashboards/frontend/frontend.json", UID: "pr-1234-frontend"},
			},
			wantCommented: true,
			wantLinks: []string{
				"- [Backend](" + testGrafanaURL + "/d/pr-1234-backend)",
				"- [Frontend](" + testGrafanaURL + "/d/pr-1234-frontend)",
			},
		},
		{
			name: "truncates a uid Grafana would reject",
			dashboards: map[string]string{
				"observability/grafana-dashboards/istio/istio.json": `{"uid":"e78df788-2208-4a2b-b9ba-5e9ea155d7ed","title":"Istio"}`,
			},
			withComments: true,
			wantUploads: []uploadCall{
				{File: "observability/grafana-dashboards/istio/istio.json", UID: "pr-1234-e78df788-2208-4a2b-b9ba-5-288111"},
			},
			wantCommented: true,
			wantLinks:     []string{"- [Istio](" + testGrafanaURL + "/d/pr-1234-e78df788-2208-4a2b-b9ba-5-288111)"},
		},
		{
			name: "unwraps an exported dashboard",
			dashboards: map[string]string{
				"observability/grafana-dashboards/backend/backend.json": `{"dashboard":{"uid":"backend","title":"Backend"},"overwrite":true}`,
			},
			withComments: true,
			wantUploads: []uploadCall{
				{File: "observability/grafana-dashboards/backend/backend.json", UID: "pr-1234-backend"},
			},
			wantCommented: true,
			wantLinks:     []string{"- [Backend](" + testGrafanaURL + "/d/pr-1234-backend)"},
		},
		{
			name: "skips a dashboard that carries no uid",
			dashboards: map[string]string{
				"observability/grafana-dashboards/backend/backend.json": `{"uid":"backend","title":"Backend"}`,
				"observability/grafana-dashboards/broken/broken.json":   `{"title":"No UID"}`,
			},
			withComments: true,
			wantUploads: []uploadCall{
				{File: "observability/grafana-dashboards/backend/backend.json", UID: "pr-1234-backend"},
			},
			wantCommented: true,
			wantLinks:     []string{"- [Backend](" + testGrafanaURL + "/d/pr-1234-backend)"},
		},
		{
			name: "writes no comment when every dashboard is skipped",
			dashboards: map[string]string{
				"observability/grafana-dashboards/broken/broken.json": `{"title":"No UID"}`,
			},
			withComments:  true,
			wantUploads:   nil,
			wantCommented: false,
		},
		{
			name: "writes no comment without a GitHub token",
			dashboards: map[string]string{
				"observability/grafana-dashboards/backend/backend.json": `{"uid":"backend","title":"Backend"}`,
			},
			withComments: false,
			wantUploads: []uploadCall{
				{File: "observability/grafana-dashboards/backend/backend.json", UID: "pr-1234-backend"},
			},
			wantCommented: false,
		},
		{
			name: "writes no comment on a dry run",
			dashboards: map[string]string{
				"observability/grafana-dashboards/backend/backend.json": `{"uid":"backend","title":"Backend"}`,
			},
			dryRun:       true,
			withComments: true,
			wantUploads: []uploadCall{
				{File: "observability/grafana-dashboards/backend/backend.json", UID: "pr-1234-backend"},
			},
			wantCommented: false,
		},
		{
			name: "a failed comment does not fail the preview",
			dashboards: map[string]string{
				"observability/grafana-dashboards/backend/backend.json": `{"uid":"backend","title":"Backend"}`,
			},
			withComments: true,
			commentErr:   errors.New("github is unreachable"),
			wantUploads: []uploadCall{
				{File: "observability/grafana-dashboards/backend/backend.json", UID: "pr-1234-backend"},
			},
			wantCommented: true,
		},
		{
			name: "a failed upload fails the preview",
			dashboards: map[string]string{
				"observability/grafana-dashboards/backend/backend.json": `{"uid":"backend","title":"Backend"}`,
			},
			withComments: true,
			uploadErr:    errors.New("grafana rejected the dashboard"),
			wantError:    "grafana rejected the dashboard",
		},
		{
			name: "a dashboard that is not JSON fails the preview",
			dashboards: map[string]string{
				"observability/grafana-dashboards/backend/backend.json": `not json`,
			},
			withComments: true,
			wantError:    "failed to parse dashboard",
		},
		{
			name: "a folder that cannot be created fails the preview",
			dashboards: map[string]string{
				"observability/grafana-dashboards/backend/backend.json": `{"uid":"backend","title":"Backend"}`,
			},
			withComments: true,
			folderErr:    errors.New("grafana rejected the folder"),
			wantError:    "grafana rejected the folder",
		},
		{
			name:         "a dashboard that cannot be read fails the preview",
			absentFiles:  []string{"observability/grafana-dashboards/backend/backend.json"},
			withComments: true,
			wantError:    "failed to read dashboard",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := t.TempDir()
			changed := make([]string, 0, len(test.dashboards)+len(test.absentFiles))
			for path, content := range test.dashboards {
				writeFile(t, repo, path, content)
				changed = append(changed, path)
			}
			changed = append(changed, test.absentFiles...)
			slices.Sort(changed)

			opts := buildTestCreateOptions(t, repo, test.dryRun)
			folders := &fakeFolders{
				info: folder.FolderInfo{
					UID:   "pr-1234",
					Title: "PR-1234",
					URL:   testGrafanaURL + "/dashboards/f/pr-1234",
				},
				err: test.folderErr,
			}
			uploader := &fakeUploader{err: test.uploadErr}

			var comments *fakeComments
			var upserter commentUpserter
			if test.withComments {
				comments = &fakeComments{err: test.commentErr}
				upserter = comments
			}

			err := opts.createPreview(t.Context(), changed, folders, uploader.upload, upserter)
			if test.wantError != "" {
				if err == nil {
					t.Fatalf("createPreview succeeded, want an error containing %q", test.wantError)
				}
				if !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("error = %v, want it to contain %q", err, test.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("createPreview: %v", err)
			}

			if folders.calls != 1 {
				t.Errorf("folder created %d times, want 1", folders.calls)
			}

			wantUploads := make([]uploadCall, 0, len(test.wantUploads))
			for _, want := range test.wantUploads {
				wantUploads = append(wantUploads, uploadCall{File: filepath.Join(repo, want.File), UID: want.UID})
			}
			if !slices.Equal(uploader.calls, wantUploads) {
				t.Errorf("uploads = %v, want %v", uploader.calls, wantUploads)
			}

			commented := comments != nil && len(comments.bodies) > 0
			if commented != test.wantCommented {
				t.Fatalf("commented = %v, want %v", commented, test.wantCommented)
			}
			if !commented {
				return
			}

			body := comments.bodies[0]
			if !strings.HasPrefix(body, commentMarker) {
				t.Errorf("comment body does not open with the marker: %q", body)
			}
			if !strings.Contains(body, folders.info.URL) {
				t.Errorf("comment body does not link the preview folder %q:\n%s", folders.info.URL, body)
			}
			for _, link := range test.wantLinks {
				if !strings.Contains(body, link) {
					t.Errorf("comment body is missing %q:\n%s", link, body)
				}
			}
		})
	}
}

// TestCreatePreviewPrintsLinksWhenTheCommentFails covers the reason the links
// are printed before the comment is written: a pull request whose comment
// cannot be written still reports its preview through the job log.
func TestCreatePreviewPrintsLinksWhenTheCommentFails(t *testing.T) {
	repo := t.TempDir()
	file := "observability/grafana-dashboards/backend/backend.json"
	writeFile(t, repo, file, `{"uid":"backend","title":"Backend"}`)

	opts := buildTestCreateOptions(t, repo, false)
	folders := &fakeFolders{info: folder.FolderInfo{URL: testGrafanaURL + "/dashboards/f/pr-1234"}}
	comments := &fakeComments{err: errors.New("github is unreachable")}

	out := captureStdout(t, func() {
		err := opts.createPreview(t.Context(), []string{file}, folders, (&fakeUploader{}).upload, comments)
		if err != nil {
			t.Errorf("createPreview: %v", err)
		}
	})

	for _, want := range []string{
		"Grafana Dashboard Preview Links",
		"Folder: " + testGrafanaURL + "/dashboards/f/pr-1234",
		"- [Backend](" + testGrafanaURL + "/d/pr-1234-backend)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the job log is missing %q:\n%s", want, out)
		}
	}
}

// TestRunReportsAFailedDetection covers the first thing that fails in CI when
// the repository is cloned too shallowly to hold the base commit.
func TestRunReportsAFailedDetection(t *testing.T) {
	opts := buildTestCreateOptions(t, buildTestRepo(t), false)
	opts.BaseSHA = "0000000000000000000000000000000000000000"

	err := opts.Run(t.Context())
	if err == nil {
		t.Fatal("Run succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "fetched deeply enough") {
		t.Errorf("error = %v, want it to report the base commit", err)
	}
}

// TestRunReachesGrafanaOnceDashboardsChange proves Run gets past detection into
// the Grafana path. It stops at folder validation, which rejects the missing
// instance before any network call.
func TestRunReachesGrafanaOnceDashboardsChange(t *testing.T) {
	repo := buildTestRepo(t)
	baseSHA := gitOutput(t, repo, "rev-parse", "HEAD")

	writeFile(t, repo, "observability/grafana-dashboards/backend/backend.json", `{"uid":"backend","title":"Backend v2"}`)
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-q", "-m", "change a dashboard")

	opts := buildTestCreateOptions(t, repo, false)
	opts.BaseSHA = baseSHA
	opts.HeadSHA = gitOutput(t, repo, "rev-parse", "HEAD")

	err := opts.Run(t.Context())
	if err == nil {
		t.Fatal("Run succeeded, want the folder options to be rejected")
	}
	if !strings.Contains(err.Error(), "folder validation failed") {
		t.Errorf("error = %v, want it to come from the folder options", err)
	}
}

func TestRunSkipsAPullRequestWithNoDashboardChange(t *testing.T) {
	repo := buildTestRepo(t)
	head := gitOutput(t, repo, "rev-parse", "HEAD")

	opts := buildTestCreateOptions(t, repo, false)
	opts.BaseSHA = head
	opts.HeadSHA = head

	// Run builds the Grafana clients only once it has work to do, so reaching
	// them here without Azure credentials would fail the test.
	if err := opts.Run(t.Context()); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func buildTestCreateOptions(t *testing.T, repo string, dryRun bool) *CompletedCreateOptions {
	t.Helper()

	raw := BuildDefaultCreateOptions()
	raw.DryRun = dryRun
	raw.PullNumber = 1234
	raw.BaseSHA = "0000000000000000000000000000000000000000"
	raw.HeadSHA = "1a2b3c4d5e6f7890abcdef1234567890abcdef12"
	raw.RepoPath = repo

	return &CompletedCreateOptions{
		validatedCreateOptions: &validatedCreateOptions{
			RawCreateOptions:     raw,
			CompletedBaseOptions: &base.CompletedBaseOptions{},
		},
	}
}

// captureStdout collects what fn writes to standard output, so that a test can
// read the job log the preview produces.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	read, write, err := os.Pipe()
	if err != nil {
		t.Fatalf("open a pipe: %v", err)
	}

	original := os.Stdout
	os.Stdout = write
	defer func() { os.Stdout = original }()

	fn()

	if err := write.Close(); err != nil {
		t.Fatalf("close the pipe: %v", err)
	}

	// The preview writes far less than a pipe holds, so one read after the
	// close collects all of it without a reader goroutine.
	out, err := io.ReadAll(read)
	if err != nil {
		t.Fatalf("read the captured output: %v", err)
	}
	return string(out)
}

func readTestFile(path string) []byte {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return raw
}
