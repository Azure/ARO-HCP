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

package options

import (
	"context"
	"fmt"
	"os"
	"path"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/image-updater/internal/publish"
)

// repositoryPattern matches one owner/name repository reference.
var repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)

// commitPattern matches a full commit SHA.
var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// RawPublishOptions contains the raw command-line input for publication.
type RawPublishOptions struct {
	Repo          string
	Base          string
	BaseCommit    string
	HeadRepo      string
	Branch        string
	Title         string
	BodyPath      string
	Marker        string
	CommitMessage string
	GitName       string
	GitEmail      string
	Actor         string
	Paths         []string
	PathsFile     string
	AppIDPath     string
	AppKeyPath    string
	TokenPath     string
	DryRun        bool
}

// ValidatedPublishOptions contains the raw input that passed validation.
type ValidatedPublishOptions struct {
	*validatedPublishOptions
}

type validatedPublishOptions struct {
	*RawPublishOptions
}

// DefaultPublishOptions returns the publication defaults.
func DefaultPublishOptions() *RawPublishOptions {
	return &RawPublishOptions{
		Repo:          ".",
		Base:          "main",
		HeadRepo:      "Azure/ARO-HCP",
		Branch:        "bot/image-updater",
		Title:         "Automated - Update component image digests",
		Marker:        "<!-- aro-hcp-image-updater -->",
		CommitMessage: "chore: update component image digests",
		GitName:       "aro-hcp-robot[bot]",
		GitEmail:      "aro-hcp-robot[bot]@users.noreply.github.com",
		Actor:         "aro-hcp-robot[bot]",
	}
}

// BindPublishOptions binds command-line flags to the raw publication options.
func BindPublishOptions(opts *RawPublishOptions, cmd *cobra.Command) error {
	flags := cmd.Flags()
	flags.StringVar(&opts.Repo, "repo", opts.Repo, "Checkout holding the intended changes")
	flags.StringVar(&opts.Base, "base", opts.Base, "Base branch of the pull request")
	flags.StringVar(&opts.BaseCommit, "base-commit", opts.BaseCommit, "Full SHA the update was prepared from (required unless --dry-run)")
	flags.StringVar(&opts.HeadRepo, "head-repo", opts.HeadRepo, "Repository the rolling branch is pushed to")
	flags.StringVar(&opts.Branch, "branch", opts.Branch, "Rolling branch reused across runs")
	flags.StringVar(&opts.Title, "title", opts.Title, "Pull request title")
	flags.StringVar(&opts.BodyPath, "body-file", "", "File holding the pull request body (required)")
	flags.StringVar(&opts.Marker, "marker", opts.Marker, "Standalone HTML comment identifying generated pull requests")
	flags.StringVar(&opts.CommitMessage, "commit-message", opts.CommitMessage, "Commit message for the digest update")
	flags.StringVar(&opts.GitName, "git-name", opts.GitName, "Commit author name")
	flags.StringVar(&opts.GitEmail, "git-email", opts.GitEmail, "Commit author email")
	flags.StringVar(&opts.Actor, "actor", opts.Actor, "Login the pull request is published as")
	flags.StringArrayVar(&opts.Paths, "paths", opts.Paths, "Repository path the update may publish (repeatable)")
	flags.StringVar(&opts.PathsFile, "paths-file", "", "File listing one publishable repository path per line")
	flags.StringVar(&opts.AppIDPath, "app-id-file", "", "File holding the GitHub App ID")
	flags.StringVar(&opts.AppKeyPath, "app-key-file", "", "File holding the GitHub App private key")
	flags.StringVar(&opts.TokenPath, "token-file", "", "File holding a GitHub token, instead of App credentials")
	flags.BoolVar(&opts.DryRun, "dry-run", false, "Commit locally and skip the push and publication")
	return cmd.MarkFlagRequired("body-file")
}

// Validate rejects incomplete input before any file, credential or network access.
func (o *RawPublishOptions) Validate() (*ValidatedPublishOptions, error) {
	for _, required := range []struct {
		name  string
		value string
	}{
		{"--repo", o.Repo},
		{"--base", o.Base},
		{"--head-repo", o.HeadRepo},
		{"--branch", o.Branch},
		{"--title", o.Title},
		{"--body-file", o.BodyPath},
		{"--marker", o.Marker},
		{"--commit-message", o.CommitMessage},
		{"--git-name", o.GitName},
		{"--git-email", o.GitEmail},
		{"--actor", o.Actor},
	} {
		if strings.TrimSpace(required.value) == "" {
			return nil, fmt.Errorf("%s is required", required.name)
		}
	}
	if !repositoryPattern.MatchString(o.HeadRepo) {
		return nil, fmt.Errorf("--head-repo must be owner/name")
	}
	if !isMarker(o.Marker) {
		return nil, fmt.Errorf("--marker must be one standalone HTML comment")
	}
	if o.Base == o.Branch {
		return nil, fmt.Errorf("--base and --branch must differ")
	}
	if len(o.Paths) == 0 && o.PathsFile == "" {
		return nil, fmt.Errorf("--paths or --paths-file is required")
	}
	if !o.DryRun {
		if !commitPattern.MatchString(o.BaseCommit) {
			return nil, fmt.Errorf("--base-commit must be a full commit SHA")
		}
		if o.TokenPath == "" && o.AppIDPath == "" {
			return nil, fmt.Errorf("--token-file or the App credential files are required unless --dry-run")
		}
	}
	if (o.AppIDPath == "") != (o.AppKeyPath == "") {
		return nil, fmt.Errorf("--app-id-file and --app-key-file are required together")
	}
	return &ValidatedPublishOptions{validatedPublishOptions: &validatedPublishOptions{RawPublishOptions: o}}, nil
}

// isMarker reports whether value is one standalone HTML comment.
func isMarker(value string) bool {
	return strings.HasPrefix(value, "<!-- ") &&
		strings.HasSuffix(value, " -->") &&
		!strings.ContainsAny(value, "\r\n") &&
		strings.Count(value, "<!--") == 1 &&
		strings.Count(value, "-->") == 1
}

// Complete reads the body, the publishable paths and the credential source.
func (v *ValidatedPublishOptions) Complete(_ context.Context) (*publish.Options, error) {
	body, err := os.ReadFile(v.BodyPath)
	if err != nil {
		return nil, fmt.Errorf("read the pull request body: %w", err)
	}
	paths, err := v.publishablePaths()
	if err != nil {
		return nil, err
	}
	token, err := v.tokenSource()
	if err != nil {
		return nil, err
	}
	opts, err := publish.NewOptions(publish.Input{
		Repo:          v.Repo,
		Base:          v.Base,
		BaseCommit:    v.BaseCommit,
		HeadRepo:      v.HeadRepo,
		Branch:        v.Branch,
		Title:         v.Title,
		Marker:        v.Marker,
		CommitMessage: v.CommitMessage,
		GitName:       v.GitName,
		GitEmail:      v.GitEmail,
		Actor:         v.Actor,
		Paths:         paths,
		Body:          body,
		DryRun:        v.DryRun,
	}, publish.WithToken(token), publish.WithWriter(os.Stdout))
	if err != nil {
		return nil, err
	}
	return opts, nil
}

// publishablePaths merges and normalizes the intended path list.
func (v *ValidatedPublishOptions) publishablePaths() ([]string, error) {
	entries := append([]string(nil), v.Paths...)
	if v.PathsFile != "" {
		data, err := os.ReadFile(v.PathsFile)
		if err != nil {
			return nil, fmt.Errorf("read the publishable paths: %w", err)
		}
		entries = append(entries, strings.Split(string(data), "\n")...)
	}
	return normalizePaths(entries)
}

// normalizePaths validates, cleans and deduplicates repository-relative paths.
func normalizePaths(entries []string) ([]string, error) {
	seen := make(map[string]struct{}, len(entries))
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		cleaned := path.Clean(entry)
		if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || path.IsAbs(cleaned) {
			return nil, fmt.Errorf("%s is not a repository-relative path", entry)
		}
		if _, duplicate := seen[cleaned]; duplicate {
			continue
		}
		seen[cleaned] = struct{}{}
		paths = append(paths, cleaned)
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no publishable paths were provided")
	}
	return paths, nil
}

// tokenSource returns the credential publication uses, read before connecting.
func (v *ValidatedPublishOptions) tokenSource() (publish.TokenSource, error) {
	if v.DryRun && v.TokenPath == "" && v.AppIDPath == "" {
		return func(context.Context) (string, error) {
			return "", fmt.Errorf("a dry run never authenticates")
		}, nil
	}
	if v.TokenPath != "" {
		data, err := os.ReadFile(v.TokenPath)
		if err != nil {
			return nil, fmt.Errorf("read the GitHub token: %w", err)
		}
		value := strings.TrimSpace(string(data))
		if value == "" {
			return nil, fmt.Errorf("the GitHub token file is empty")
		}
		return func(context.Context) (string, error) { return value, nil }, nil
	}
	appID, err := os.ReadFile(v.AppIDPath)
	if err != nil {
		return nil, fmt.Errorf("read the GitHub App ID: %w", err)
	}
	source := publish.AppTokenSource{AppID: strings.TrimSpace(string(appID)), KeyPath: v.AppKeyPath}
	return func(ctx context.Context) (string, error) {
		return source.Token(ctx, v.HeadRepo)
	}, nil
}
