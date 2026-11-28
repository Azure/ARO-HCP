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
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-Tools/tools/grafanactl/cmd/dashboard"

	"github.com/Azure/ARO-HCP/tooling/grafanactl/internal/github"
)

// commentUpserter keeps the preview comment on the pull request up to date. The
// GitHub client satisfies it.
type commentUpserter interface {
	UpsertMarkedComment(ctx context.Context, number int, marker, body string) (github.Comment, error)
}

// Run uploads the dashboards a pull request changed into a folder of its own,
// and reports the links in the job log and in a comment on the pull request.
func (o *CompletedCreateOptions) Run(ctx context.Context) error {
	changed, err := getChangedDashboards(ctx, o.RepoPath, o.BaseSHA, o.HeadSHA, o.DashboardDir)
	if err != nil {
		return err
	}
	if len(changed) == 0 {
		printNoChange()
		return nil
	}

	printChangedDashboards(changed)

	if o.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.Timeout)
		defer cancel()
	}

	folders, err := o.buildCompletedFolderOptions(ctx)
	if err != nil {
		return err
	}

	// A nil *github.Client must not reach the interface as a non-nil value.
	var upserter commentUpserter
	if o.GitHubClient != nil {
		upserter = o.GitHubClient
	}

	return o.createPreview(ctx, changed, folders, o.uploadToPreviewFolder, upserter)
}

// createPreview uploads the given dashboards and reports where they went.
func (o *CompletedCreateOptions) createPreview(
	ctx context.Context,
	changed []string,
	folders folderCreator,
	upload uploadDashboard,
	comments commentUpserter,
) error {
	previewFolder, err := folders.Create(ctx)
	if err != nil {
		return err
	}

	links, err := o.uploadDashboards(ctx, changed, upload)
	if err != nil {
		return err
	}

	printSummary(previewFolder.URL, links)

	return o.commentOnPullRequest(ctx, previewFolder.URL, links, comments)
}

// uploadDashboards stores each dashboard under a UID of its own, and reports
// where each one landed.
func (o *CompletedCreateOptions) uploadDashboards(ctx context.Context, changed []string, upload uploadDashboard) ([]dashboardLink, error) {
	logger := logr.FromContextOrDiscard(ctx)

	links := make([]dashboardLink, 0, len(changed))
	for _, file := range changed {
		path := filepath.Join(o.RepoPath, file)

		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("failed to read dashboard %q: %w", file, err)
		}

		originalUID, title, err := dashboard.GetDashboardIdentity(raw)
		if err != nil {
			return nil, fmt.Errorf("failed to parse dashboard %q: %w", file, err)
		}
		if originalUID == "" {
			logger.Info("Skipping dashboard, it carries no uid", "file", file)
			continue
		}

		printUploading(title, file)

		info, err := upload(ctx, path, buildPreviewUID(o.PullNumber, originalUID))
		if err != nil {
			return nil, err
		}

		links = append(links, dashboardLink{Title: info.Title, URL: info.URL})
	}
	return links, nil
}

// commentOnPullRequest leaves the preview links on the pull request.
//
// The links already reached the job log, so a comment this cannot write is a
// degraded preview rather than a failed one, and none of the paths below
// returns an error.
func (o *CompletedCreateOptions) commentOnPullRequest(
	ctx context.Context,
	folderURL string,
	links []dashboardLink,
	comments commentUpserter,
) error {
	logger := logr.FromContextOrDiscard(ctx)

	if len(links) == 0 {
		logger.Info("No dashboard was uploaded, skipping PR comment")
		return nil
	}

	body := buildCommentBody(folderURL, links, o.HeadSHA)

	if o.DryRun {
		logger.Info("Dry run - would comment on the pull request", "pr", o.PullNumber, "body", body)
		return nil
	}
	if comments == nil {
		printNoGitHubToken()
		return nil
	}

	if _, err := comments.UpsertMarkedComment(ctx, o.PullNumber, commentMarker, body); err != nil {
		logger.Error(err, "Failed to post or update the PR comment")
	}
	return nil
}
