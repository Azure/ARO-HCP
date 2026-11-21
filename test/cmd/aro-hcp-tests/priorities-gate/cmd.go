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

package prioritiesgate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"

	gh "github.com/google/go-github/v69/github"
	"github.com/spf13/cobra"

	githublib "github.com/Azure/ARO-HCP/tooling/github"
)

// repositoryPattern matches the owner/name form the publication target takes.
var repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// Gate reports whether a run may publish, and why not when it may not.
type Gate struct {
	Blocked bool   `json:"blocked"`
	Reason  string `json:"reason,omitempty"`
	Numbers []int  `json:"numbers,omitempty"`
}

// RawOptions holds the flag values before validation.
type RawOptions struct {
	Repository   string
	Base         string
	BaseCommit   string
	Actor        string
	Marker       string
	BranchPrefix string
	TokenPath    string
}

// validatedOptions holds the raw values that passed validation.
type validatedOptions struct {
	raw *RawOptions
}

// completedOptions holds the authenticated client the gate reads through.
type completedOptions struct {
	repository string
	base       string
	baseCommit string
	actor      string
	marker     string
	prefix     string
	out        io.Writer
	actorOf    func(context.Context) (string, error)
	contains   func(context.Context, string, string) error
	proposals  func(context.Context, string, string, string) ([]int, error)
}

// NewCommand builds the priorities-gate command.
func NewCommand() *cobra.Command {
	raw := &RawOptions{}
	cmd := &cobra.Command{
		Use:   "priorities-gate",
		Short: "Report whether a start-priority refresh may publish",
		Long: `Check the preconditions publication needs: a usable installation token, a
checkout commit that is part of the base branch, and no generated pull request
already open for this repository.

The result is one JSON object on stdout. A blocked gate still exits zero: being
blocked is an ordinary outcome for a scheduled run, not a failure.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGate(cmd.Context(), cmd.OutOrStdout(), raw)
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&raw.Repository, "repo", "Azure/ARO-HCP", "Repository the proposal is opened in")
	flags.StringVar(&raw.Base, "base", "main", "Base branch of the proposal")
	flags.StringVar(&raw.BaseCommit, "base-commit", "", "Commit the refresh was computed against (required)")
	flags.StringVar(&raw.Actor, "actor", "aro-hcp-robot[bot]", "Login the proposal is expected to be authored by")
	flags.StringVar(&raw.Marker, "marker", "", "Marker line identifying generated proposals")
	flags.StringVar(&raw.BranchPrefix, "branch-prefix", "", "Prefix of a generated proposal branch")
	flags.StringVar(&raw.TokenPath, "token-path", "", "File holding the GitHub App installation token (required)")
	return cmd
}

// runGate runs the command phases in order.
func runGate(ctx context.Context, out io.Writer, raw *RawOptions) error {
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

// Validate rejects incomplete input before any file is read or request is made.
func (r *RawOptions) Validate() (*validatedOptions, error) {
	for _, required := range []struct {
		name  string
		value string
	}{
		{"--repo", r.Repository},
		{"--base", r.Base},
		{"--base-commit", r.BaseCommit},
		{"--actor", r.Actor},
		{"--token-path", r.TokenPath},
	} {
		if required.value == "" {
			return nil, fmt.Errorf("%s is required", required.name)
		}
	}
	if r.Marker == "" && r.BranchPrefix == "" {
		return nil, fmt.Errorf("--marker or --branch-prefix is required")
	}
	if !repositoryPattern.MatchString(r.Repository) {
		return nil, fmt.Errorf("--repo must be owner/name, got %q", r.Repository)
	}
	return &validatedOptions{raw: r}, nil
}

// Complete reads the token and builds the authenticated client.
func (v *validatedOptions) Complete(out io.Writer) (*completedOptions, error) {
	raw := v.raw
	completed := &completedOptions{
		repository: raw.Repository, base: raw.Base, baseCommit: raw.BaseCommit,
		actor: raw.Actor, marker: raw.Marker, prefix: raw.BranchPrefix, out: out,
	}
	client, err := newClient(raw.Repository, raw.TokenPath)
	if err != nil {
		return nil, err
	}
	completed.actorOf = func(ctx context.Context) (string, error) { return client.Actor(ctx) }
	completed.contains = func(ctx context.Context, commit, base string) error {
		return client.BaseContains(ctx, commit, base)
	}
	completed.proposals = func(ctx context.Context, actor, marker, prefix string) ([]int, error) {
		return client.OpenProposals(ctx, actor, marker, prefix)
	}
	return completed, nil
}

// newClient authenticates one repository client with the installation token.
func newClient(repository, tokenPath string) (*githublib.Client, error) {
	token, err := os.ReadFile(tokenPath)
	if err != nil {
		return nil, fmt.Errorf("read the GitHub token: %w", err)
	}
	api := gh.NewClient(&http.Client{Transport: &tokenTransport{token: strings.TrimSpace(string(token))}})
	client, err := githublib.NewClient(repository, api)
	if err != nil {
		return nil, fmt.Errorf("create the GitHub client: %w", err)
	}
	return client, nil
}

// Run evaluates the gate and reports the outcome as JSON.
func (c *completedOptions) Run(ctx context.Context) error {
	actor, err := c.actorOf(ctx)
	if err != nil {
		return fmt.Errorf("the installation token is not usable: %w", err)
	}
	if actor != c.actor {
		return fmt.Errorf("the token authenticates as %q, not %q", actor, c.actor)
	}
	if err := c.contains(ctx, c.baseCommit, c.base); err != nil {
		return err
	}
	numbers, err := c.proposals(ctx, actor, c.marker, c.prefix)
	if err != nil {
		return fmt.Errorf("list open proposals: %w", err)
	}
	gate := Gate{Numbers: numbers}
	if len(numbers) > 0 {
		gate.Blocked = true
		gate.Reason = fmt.Sprintf("an open generated pull request #%d already exists", numbers[0])
	}
	return writeGate(c.out, gate)
}

// writeGate prints the gate outcome as one JSON object.
func writeGate(out io.Writer, gate Gate) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(gate); err != nil {
		return fmt.Errorf("write the gate result: %w", err)
	}
	return nil
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
