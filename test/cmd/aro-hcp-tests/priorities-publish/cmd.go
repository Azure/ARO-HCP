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

// Package prioritiespublish commits a verified refresh and publishes it as one
// pull request through the shared GitHub library.
package prioritiespublish

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	gh "github.com/google/go-github/v69/github"
	"github.com/spf13/cobra"

	githublib "github.com/Azure/ARO-HCP/tooling/github"
)

// allowlist matches the only files a refresh may change.
var allowlist = regexp.MustCompile(`^(test/e2e/[A-Za-z0-9_./-]+\.go|test/testdata/zz_fixture_TestMainListSuitesForEachSuite_[A-Za-z0-9_.-]+\.txt)$`)

// RawOptions holds the flag values before validation.
type RawOptions struct {
	Repo      string
	Base      string
	HeadRepo  string
	Branch    string
	Title     string
	BodyPath  string
	Marker    string
	Changes   string
	GitName   string
	GitEmail  string
	TokenPath string
	Actor     string
	DryRun    bool
}

// validatedOptions holds the raw values that passed validation.
type validatedOptions struct {
	raw *RawOptions
}

// completedOptions holds everything Run needs, with local inputs already read.
type completedOptions struct {
	repo     string
	base     string
	headRepo string
	branch   string
	title    string
	marker   string
	actor    string
	gitName  string
	gitEmail string
	files    []string
	body     []byte
	token    string
	dryRun   bool
	out      io.Writer
	git      func(context.Context, ...string) error
	publish  func(context.Context, githublib.PublicationRequest) (githublib.Receipt, error)
}

// DefaultRawOptions returns the flag defaults.
func DefaultRawOptions() *RawOptions {
	return &RawOptions{
		Repo:     ".",
		Base:     "main",
		HeadRepo: "Azure/ARO-HCP",
		Title:    "test(e2e): refresh start priority bands",
		GitName:  "aro-hcp-robot[bot]",
		GitEmail: "aro-hcp-robot[bot]@users.noreply.github.com",
		Actor:    "aro-hcp-robot[bot]",
	}
}

// NewCommand builds the priorities-publish command.
func NewCommand() *cobra.Command {
	raw := DefaultRawOptions()
	cmd := &cobra.Command{
		Use:   "priorities-publish",
		Short: "Commit a verified refresh and open its pull request",
		Long: `Stage only the reviewed paths, commit them as the bot, push a create-only branch
and publish one pull request through the shared GitHub library, which verifies
the head, the metadata and the complete file set before reporting success.

The branch must not exist yet: the push uses an empty lease, so a rerun can never
replace work under review. With --dry-run the commit and push are prepared but
nothing is published.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return publish(cmd.Context(), cmd.OutOrStdout(), raw)
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&raw.Repo, "repo", raw.Repo, "Checkout holding the verified changes")
	flags.StringVar(&raw.Base, "base", raw.Base, "Base branch of the pull request")
	flags.StringVar(&raw.HeadRepo, "head-repo", raw.HeadRepo, "Repository the branch is pushed to")
	flags.StringVar(&raw.Branch, "branch", "", "Create-only branch name (required)")
	flags.StringVar(&raw.Title, "title", raw.Title, "Pull request title")
	flags.StringVar(&raw.BodyPath, "body-file", "", "File holding the pull request body (required)")
	flags.StringVar(&raw.Marker, "marker", "", "Exact marker line identifying generated pull requests (required)")
	flags.StringVar(&raw.Changes, "changes", "", "Change summary produced by the refresh (required)")
	flags.StringVar(&raw.GitName, "git-name", raw.GitName, "Commit author name")
	flags.StringVar(&raw.GitEmail, "git-email", raw.GitEmail, "Commit author email")
	flags.StringVar(&raw.TokenPath, "token-path", "", "File holding the GitHub App installation token (required unless --dry-run)")
	flags.StringVar(&raw.Actor, "actor", raw.Actor, "Login the pull request is published as")
	flags.BoolVar(&raw.DryRun, "dry-run", false, "Commit locally and skip the push and publication")
	return cmd
}

// publish runs the command phases in order.
func publish(ctx context.Context, out io.Writer, raw *RawOptions) error {
	validated, err := raw.Validate()
	if err != nil {
		return err
	}
	completed, err := validated.Complete(out)
	if err != nil {
		return err
	}
	return completed.Run(ctx)
}

// Validate rejects incomplete invocations before any file or process access.
func (r *RawOptions) Validate() (*validatedOptions, error) {
	for _, required := range []struct {
		name  string
		value string
	}{
		{"--branch", r.Branch},
		{"--body-file", r.BodyPath},
		{"--marker", r.Marker},
		{"--changes", r.Changes},
	} {
		if required.value == "" {
			return nil, fmt.Errorf("%s is required", required.name)
		}
	}
	if !r.DryRun && r.TokenPath == "" {
		return nil, fmt.Errorf("--token-path is required unless --dry-run")
	}
	return &validatedOptions{raw: r}, nil
}

// Complete reads the local inputs and enforces the refresh allowlist.
func (v *validatedOptions) Complete(out io.Writer) (*completedOptions, error) {
	raw := v.raw
	files, err := changedFiles(raw.Changes)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("the change summary lists no files to publish")
	}
	for _, file := range files {
		if !allowlist.MatchString(file) {
			return nil, fmt.Errorf("%s is outside the refresh allowlist", file)
		}
	}
	body, err := os.ReadFile(raw.BodyPath)
	if err != nil {
		return nil, fmt.Errorf("read the pull request body: %w", err)
	}
	token, err := readToken(raw.TokenPath, raw.DryRun)
	if err != nil {
		return nil, err
	}
	completed := &completedOptions{
		repo: raw.Repo, base: raw.Base, headRepo: raw.HeadRepo, branch: raw.Branch,
		title: raw.Title, marker: raw.Marker, actor: raw.Actor,
		gitName: raw.GitName, gitEmail: raw.GitEmail,
		files: files, body: body, token: token, dryRun: raw.DryRun, out: out,
	}
	completed.git = completed.runGit
	return completed, nil
}

// readToken loads the installation token, tolerating its absence in a dry run.
func readToken(path string, dryRun bool) (string, error) {
	if dryRun {
		return "", nil
	}
	token, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read the GitHub token: %w", err)
	}
	return strings.TrimSpace(string(token)), nil
}

// changedFiles reads and normalizes the file list from the refresh summary.
func changedFiles(summaryPath string) ([]string, error) {
	data, err := os.ReadFile(summaryPath)
	if err != nil {
		return nil, fmt.Errorf("read the change summary: %w", err)
	}
	var summary struct {
		Changes []struct {
			Path string `json:"path"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(data, &summary); err != nil {
		return nil, fmt.Errorf("decode the change summary: %w", err)
	}
	seen := make(map[string]struct{}, len(summary.Changes))
	files := make([]string, 0, len(summary.Changes))
	for _, change := range summary.Changes {
		file := filepath.Clean(change.Path)
		if _, duplicate := seen[file]; duplicate {
			continue
		}
		seen[file] = struct{}{}
		files = append(files, file)
	}
	return files, nil
}

// Run commits the verified paths and publishes them.
func (c *completedOptions) Run(ctx context.Context) error {
	if err := c.git(ctx, append([]string{"add", "--"}, c.files...)...); err != nil {
		return fmt.Errorf("stage the reviewed files: %w", err)
	}
	if err := c.git(ctx, "-c", "user.name="+c.gitName, "-c", "user.email="+c.gitEmail, "commit", "--quiet", "--message", c.title); err != nil {
		return fmt.Errorf("commit the reviewed files: %w", err)
	}
	if c.dryRun {
		fmt.Fprintf(c.out, "Dry run: committed %d files to %s and stopped before publishing.\n", len(c.files), c.branch)
		return nil
	}
	if err := c.push(ctx); err != nil {
		return err
	}
	request, err := c.publicationRequest(ctx)
	if err != nil {
		return err
	}
	receipt, err := c.publishRequest(ctx, request)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(c.out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(receipt)
}

// push creates the branch, refusing to replace an existing one.
func (c *completedOptions) push(ctx context.Context) error {
	askpass, err := createAskpass(c.token)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(askpass) }()
	url := fmt.Sprintf("https://x-access-token@github.com/%s.git", c.headRepo)
	args := []string{
		"-c", "credential.helper=",
		"push", "--force-with-lease=refs/heads/" + c.branch + ":",
		url, "HEAD:refs/heads/" + c.branch,
	}
	command := exec.CommandContext(ctx, "git", append([]string{"-C", c.repo}, args...)...)
	command.Env = append(os.Environ(), "GIT_ASKPASS="+askpass, "GITHUB_TOKEN="+c.token, "GIT_TERMINAL_PROMPT=0")
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("push %s: %w: %s", c.branch, err, strings.TrimSpace(string(output)))
	}
	return nil
}

// createAskpass writes the helper that feeds the token to git.
func createAskpass(token string) (string, error) {
	file, err := os.CreateTemp("", "askpass-*.sh")
	if err != nil {
		return "", fmt.Errorf("create the askpass helper: %w", err)
	}
	script := "#!/bin/sh\nprintf '%s' \"${GITHUB_TOKEN}\"\n"
	if _, err := file.WriteString(script); err != nil {
		return "", fmt.Errorf("write the askpass helper: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close the askpass helper: %w", err)
	}
	if err := os.Chmod(file.Name(), 0o700); err != nil {
		return "", fmt.Errorf("secure the askpass helper: %w", err)
	}
	return file.Name(), nil
}

// publicationRequest builds the verified request the library consumes.
func (c *completedOptions) publicationRequest(ctx context.Context) (githublib.PublicationRequest, error) {
	headSHA, err := c.output(ctx, "rev-parse", "HEAD")
	if err != nil {
		return githublib.PublicationRequest{}, err
	}
	baseSHA, err := c.remoteBranchSHA(ctx)
	if err != nil {
		return githublib.PublicationRequest{}, err
	}
	return githublib.PublicationRequest{
		HeadRepository: c.headRepo,
		HeadRef:        c.branch,
		ExpectedHead:   headSHA,
		BaseRef:        c.base,
		ExpectedBase:   baseSHA,
		Actor:          c.actor,
		Marker:         c.marker,
		Title:          c.title,
		Body:           string(c.body) + "\n" + c.marker + "\n",
		ExpectedFiles:  c.files,
		ReadyForReview: true,
	}, nil
}

// publishRequest wires the shared client to the installation token.
func (c *completedOptions) publishRequest(ctx context.Context, request githublib.PublicationRequest) (githublib.Receipt, error) {
	if c.publish != nil {
		return c.publish(ctx, request)
	}
	client, err := newPublicationClient(c.headRepo, c.token, c.actor)
	if err != nil {
		return githublib.Receipt{}, err
	}
	return client.Publish(ctx, request)
}

// newPublicationClient builds the publishing client with the trusted writer
// identity bound. App installation tokens cannot read /user, so discovery would
// fail the first request instead of publishing.
func newPublicationClient(repository, token, actor string) (*githublib.Client, error) {
	api := gh.NewClient(&http.Client{Transport: &tokenTransport{token: token}})
	client, err := githublib.NewClient(repository, api)
	if err != nil {
		return nil, err
	}
	bound, err := client.WithActor(actor)
	if err != nil {
		return nil, fmt.Errorf("bind the publication actor: %w", err)
	}
	return bound, nil
}

// remoteBranchSHA reads the base branch tip the publication must match.
func (c *completedOptions) remoteBranchSHA(ctx context.Context) (string, error) {
	url := fmt.Sprintf("https://github.com/%s.git", c.headRepo)
	command := exec.CommandContext(ctx, "git", "ls-remote", url, "refs/heads/"+c.base)
	out, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("read the %s tip: %w", c.base, err)
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return "", fmt.Errorf("the %s tip is unknown", c.base)
	}
	return fields[0], nil
}

// tokenTransport adds the installation token to every request.
type tokenTransport struct {
	token string
}

// RoundTrip attaches the bearer token and delegates to the default transport.
func (t *tokenTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header.Set("Authorization", "Bearer "+t.token)
	clone.Header.Set("Accept", "application/vnd.github+json")
	return http.DefaultTransport.RoundTrip(clone)
}

// output runs git in the checkout and returns its trimmed output.
func (c *completedOptions) output(ctx context.Context, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", c.repo}, args...)...)
	out, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// runGit runs one git command in the checkout.
func (c *completedOptions) runGit(ctx context.Context, args ...string) error {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", c.repo}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
