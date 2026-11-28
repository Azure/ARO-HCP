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
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestNewPreviewCommandSubcommands(t *testing.T) {
	cmd, err := NewPreviewCommand("main")
	if err != nil {
		t.Fatalf("NewPreviewCommand: %v", err)
	}

	got := make([]string, 0, len(cmd.Commands()))
	for _, sub := range cmd.Commands() {
		got = append(got, sub.Name())
	}
	if len(got) != 1 || got[0] != "create" {
		t.Errorf("subcommands = %v, want [create]", got)
	}
}

func TestCreateFlags(t *testing.T) {
	create := getCreateCommand(t)

	for _, name := range []string{
		"pr", "base-sha", "head-sha", "repo-owner", "repo-name", "repo-path", "dashboard-dir",
		"subscription", "resource-group", "grafana-name", "dry-run", "timeout",
	} {
		if create.Flags().Lookup(name) == nil {
			t.Errorf("flag --%s must exist", name)
		}
	}

	for _, name := range []string{"pr", "base-sha"} {
		annotations := create.Flags().Lookup(name).Annotations[cobra.BashCompOneRequiredFlag]
		if len(annotations) == 0 || annotations[0] != "true" {
			t.Errorf("--%s must be required", name)
		}
	}

	// The token names a secret, so it must not be readable from the process
	// arguments of a CI job.
	if create.Flags().Lookup("github-token") != nil {
		t.Error("the GitHub token must come from the environment, not from a flag")
	}
}

func TestCreateFlagDefaults(t *testing.T) {
	create := getCreateCommand(t)

	for _, test := range []struct{ flag, want string }{
		{flag: "repo-owner", want: defaultRepoOwner},
		{flag: "repo-name", want: defaultRepoName},
		{flag: "repo-path", want: "."},
		{flag: "dashboard-dir", want: defaultDashboardDir},
	} {
		if got := create.Flags().Lookup(test.flag).DefValue; got != test.want {
			t.Errorf("--%s defaults to %q, want %q", test.flag, got, test.want)
		}
	}
}

func TestValidate(t *testing.T) {
	for _, test := range []struct {
		name      string
		mutate    func(*RawCreateOptions)
		wantError string
	}{
		{
			name:   "complete options",
			mutate: func(*RawCreateOptions) {},
		},
		{
			name:      "missing pull request number",
			mutate:    func(o *RawCreateOptions) { o.PullNumber = 0 },
			wantError: "pull request number must be positive",
		},
		{
			name:      "negative pull request number",
			mutate:    func(o *RawCreateOptions) { o.PullNumber = -1 },
			wantError: "pull request number must be positive",
		},
		{
			name:      "missing base commit",
			mutate:    func(o *RawCreateOptions) { o.BaseSHA = "" },
			wantError: "base commit is required",
		},
		{
			name:      "missing repository owner",
			mutate:    func(o *RawCreateOptions) { o.RepoOwner = "" },
			wantError: "repository owner and name are required",
		},
		{
			name:      "missing repository name",
			mutate:    func(o *RawCreateOptions) { o.RepoName = "" },
			wantError: "repository owner and name are required",
		},
		{
			name:      "missing repository path",
			mutate:    func(o *RawCreateOptions) { o.RepoPath = "" },
			wantError: "repository path is required",
		},
		{
			name:      "missing dashboard directory",
			mutate:    func(o *RawCreateOptions) { o.DashboardDir = "" },
			wantError: "dashboard directory is required",
		},
		{
			name:      "missing Grafana instance",
			mutate:    func(o *RawCreateOptions) { o.GrafanaName = "" },
			wantError: "subscription ID, resource group, and grafana name are required",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			opts := buildValidCreateOptions()
			test.mutate(opts)

			_, err := opts.Validate(t.Context())
			if test.wantError == "" {
				if err != nil {
					t.Fatalf("Validate: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate succeeded, want an error containing %q", test.wantError)
			}
			if !strings.Contains(err.Error(), test.wantError) {
				t.Errorf("error = %v, want it to contain %q", err, test.wantError)
			}
		})
	}
}

func TestCompleteResolvesTheCheckedOutCommit(t *testing.T) {
	t.Setenv(githubTokenVariable, "")

	repo := buildTestRepo(t)
	opts := buildValidCreateOptions()
	opts.RepoPath = repo
	opts.HeadSHA = ""

	validated, err := opts.Validate(t.Context())
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	completed, err := validated.Complete(t.Context())
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	if completed.HeadSHA != gitOutput(t, repo, "rev-parse", "HEAD") {
		t.Errorf("HeadSHA = %q, want the checked out commit", completed.HeadSHA)
	}
	if completed.GitHubClient != nil {
		t.Error("no GitHub client must be built without a token")
	}
}

func TestCompleteBuildsAGitHubClientFromTheEnvironment(t *testing.T) {
	t.Setenv(githubTokenVariable, "a-token")

	opts := buildValidCreateOptions()
	validated, err := opts.Validate(t.Context())
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	completed, err := validated.Complete(t.Context())
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	if completed.GitHubClient == nil {
		t.Error("a GitHub client must be built when a token is set")
	}
}

func getCreateCommand(t *testing.T) *cobra.Command {
	t.Helper()

	cmd, err := NewPreviewCommand("main")
	if err != nil {
		t.Fatalf("NewPreviewCommand: %v", err)
	}
	for _, sub := range cmd.Commands() {
		if sub.Name() == "create" {
			return sub
		}
	}
	t.Fatal("the preview command has no create subcommand")
	return nil
}

func buildValidCreateOptions() *RawCreateOptions {
	opts := BuildDefaultCreateOptions()
	opts.SubscriptionID = "00000000-0000-0000-0000-000000000000"
	opts.ResourceGroup = "global"
	opts.GrafanaName = "arohcp-dev"
	opts.PullNumber = 1234
	opts.BaseSHA = "1a2b3c4d5e6f7890abcdef1234567890abcdef12"
	opts.HeadSHA = "abcdef1234567890abcdef1234567890abcdef12"
	return opts
}
