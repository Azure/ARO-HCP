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

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-Tools/tools/grafanactl/cmd/base"

	"github.com/Azure/ARO-HCP/tooling/grafanactl/internal/github"
)

const (
	// defaultRepoOwner and defaultRepoName name the repository the preview
	// comments on. They are kept apart from Prow's REPO_OWNER and REPO_NAME,
	// which name the repository whose pull request started the job.
	defaultRepoOwner = "Azure"
	defaultRepoName  = "ARO-HCP"
	// defaultDashboardDir is where ARO-HCP keeps its Grafana dashboards.
	defaultDashboardDir = "observability/grafana-dashboards"
	// githubTokenVariable names the environment variable that holds the GitHub
	// token. The token is read from the environment, and never from a flag, so
	// that it stays out of the process arguments.
	githubTokenVariable = "GITHUB_TOKEN"
)

// RawCreateOptions represents the initial, unvalidated configuration for
// creating a pull-request dashboard preview.
type RawCreateOptions struct {
	*base.BaseOptions
	// PullNumber is the pull request the preview belongs to.
	PullNumber int
	// BaseSHA is the commit the pull request targets.
	BaseSHA string
	// HeadSHA is the commit the pull request proposes. An empty value is
	// resolved from the checked out commit.
	HeadSHA string
	// RepoOwner and RepoName name the repository the comment goes to.
	RepoOwner string
	RepoName  string
	// RepoPath is the git working tree the dashboards are read from.
	RepoPath string
	// DashboardDir is the directory, relative to RepoPath, holding the dashboards.
	DashboardDir string
}

// validatedCreateOptions is a private struct that enforces the options validation pattern.
type validatedCreateOptions struct {
	*RawCreateOptions
	*base.CompletedBaseOptions
}

// ValidatedCreateOptions represents create configuration that has passed validation.
type ValidatedCreateOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package
	*validatedCreateOptions
}

// CompletedCreateOptions represents the final, fully validated and initialized
// configuration for creating a preview.
type CompletedCreateOptions struct {
	*validatedCreateOptions
	// GitHubClient is nil when no token is set, which leaves the preview links
	// in the job log and posts no comment.
	GitHubClient *github.Client
}

// BuildDefaultCreateOptions returns a new RawCreateOptions with default values.
func BuildDefaultCreateOptions() *RawCreateOptions {
	return &RawCreateOptions{
		BaseOptions:  base.DefaultBaseOptions(),
		RepoOwner:    defaultRepoOwner,
		RepoName:     defaultRepoName,
		RepoPath:     ".",
		DashboardDir: defaultDashboardDir,
	}
}

// BindCreateOptions binds command-line flags to the options.
func BindCreateOptions(opts *RawCreateOptions, cmd *cobra.Command) error {
	if err := base.BindBaseOptions(opts.BaseOptions, cmd); err != nil {
		return err
	}

	flags := cmd.Flags()
	flags.IntVar(&opts.PullNumber, "pr", opts.PullNumber, "Number of the pull request to preview (required)")
	flags.StringVar(&opts.BaseSHA, "base-sha", opts.BaseSHA, "Commit the pull request targets (required)")
	flags.StringVar(&opts.HeadSHA, "head-sha", opts.HeadSHA, "Commit the pull request proposes. Defaults to the checked out commit")
	flags.StringVar(&opts.RepoOwner, "repo-owner", opts.RepoOwner, "Owner of the repository to comment on")
	flags.StringVar(&opts.RepoName, "repo-name", opts.RepoName, "Name of the repository to comment on")
	flags.StringVar(&opts.RepoPath, "repo-path", opts.RepoPath, "Path to the git working tree holding the dashboards")
	flags.StringVar(&opts.DashboardDir, "dashboard-dir", opts.DashboardDir, "Directory, relative to the working tree, holding the dashboards")

	_ = cmd.MarkFlagRequired("pr")
	_ = cmd.MarkFlagRequired("base-sha")
	return nil
}

// Validate performs validation on the raw options.
func (o *RawCreateOptions) Validate(ctx context.Context) (*ValidatedCreateOptions, error) {
	completedBase, err := base.ValidateBaseOptions(o.BaseOptions)
	if err != nil {
		return nil, err
	}

	if o.PullNumber <= 0 {
		return nil, fmt.Errorf("pull request number must be positive, got %d", o.PullNumber)
	}
	if o.BaseSHA == "" {
		return nil, fmt.Errorf("base commit is required")
	}
	if o.RepoOwner == "" || o.RepoName == "" {
		return nil, fmt.Errorf("repository owner and name are required")
	}
	if o.RepoPath == "" {
		return nil, fmt.Errorf("repository path is required")
	}
	if o.DashboardDir == "" {
		return nil, fmt.Errorf("dashboard directory is required")
	}

	return &ValidatedCreateOptions{
		validatedCreateOptions: &validatedCreateOptions{
			RawCreateOptions:     o,
			CompletedBaseOptions: completedBase,
		},
	}, nil
}

// Complete resolves the commit to preview and the GitHub client.
//
// It builds no Grafana client, because a pull request that changes no dashboard
// needs none. Run builds one once it knows there is work to do.
func (o *ValidatedCreateOptions) Complete(ctx context.Context) (*CompletedCreateOptions, error) {
	if o.HeadSHA == "" {
		headSHA, err := getHeadSHA(ctx, o.RepoPath)
		if err != nil {
			return nil, err
		}
		o.HeadSHA = headSHA
	}

	completed := &CompletedCreateOptions{validatedCreateOptions: o.validatedCreateOptions}

	token := os.Getenv(githubTokenVariable)
	if token == "" {
		return completed, nil
	}

	client, err := github.NewClient(token, o.RepoOwner, o.RepoName)
	if err != nil {
		return nil, fmt.Errorf("failed to create GitHub client: %w", err)
	}
	completed.GitHubClient = client

	return completed, nil
}
