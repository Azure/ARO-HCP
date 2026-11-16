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

package dependabot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/cli"
	dependabotpolicy "github.com/Azure/ARO-HCP/tooling/depctl/internal/dependabot"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/forge/ghapi"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/policy"
)

// rawReader is the raw JSON surface the guard needs.
type rawReader interface {
	REST(ctx context.Context, endpoint string) ([]byte, error)
}

// guardOptions are the unvalidated publication-guard flags.
type guardOptions struct {
	Repository  string
	AgentOutput string
}

// guardCreate validates proposals and rewrites the agent output in place.
func guardCreate(ctx context.Context, options guardOptions) error {
	if options.Repository == "" || options.AgentOutput == "" {
		return cli.Invalid(errors.New("--repository OWNER/REPO and --agent-output FILE are required"))
	}
	reader, err := readerFactory(options.Repository)
	if err != nil {
		return cli.Invalid(err)
	}
	raw, ok := reader.(rawReader)
	if !ok {
		return cli.Internal(errors.New("reader does not expose the raw API"))
	}
	pulls, err := reader.OpenPullRequests(ctx, policy.Robot)
	if err != nil {
		return cli.MapEngineError(err)
	}
	records, err := dependabotpolicy.OwnedSecurityProposals(pulls, options.Repository)
	if err != nil {
		return cli.Incomplete("%v", err)
	}
	var numbers []int
	for _, record := range records {
		numbers = append(numbers, record.Number)
	}
	content, err := os.ReadFile(options.AgentOutput)
	if err != nil {
		return cli.Internal(fmt.Errorf("read agent output: %w", err))
	}
	var output map[string]any
	if err := json.Unmarshal(content, &output); err != nil {
		return cli.Internal(fmt.Errorf("decode agent output: %w", err))
	}
	if err := dependabotpolicy.GuardCreate(ctx, options.Repository, output, numbers, raw.REST,
		func(message string) { fmt.Fprintf(os.Stderr, "::warning::%s\n", message) }); err != nil {
		return cli.Incomplete("%v", err)
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		return cli.Internal(err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(options.AgentOutput), "dependabot-guard-*.json")
	if err != nil {
		return cli.Internal(err)
	}
	name := temporary.Name()
	if _, err := temporary.Write(append(encoded, '\n')); err != nil {
		temporary.Close()
		os.Remove(name)
		return cli.Internal(err)
	}
	if err := temporary.Close(); err != nil {
		os.Remove(name)
		return cli.Internal(err)
	}
	if err := os.Rename(name, options.AgentOutput); err != nil {
		os.Remove(name)
		return cli.Internal(err)
	}
	return nil
}

// validateProposal checks one dependency proposal and prints its canonical form.
func validateProposal(path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return cli.Internal(fmt.Errorf("read proposal: %w", err))
	}
	var item map[string]any
	if err := json.Unmarshal(content, &item); err != nil {
		return cli.Invalid(err)
	}
	candidate, err := dependabotpolicy.Group(item)
	if err != nil {
		return cli.Invalid(err)
	}
	item["title"] = candidate.Title
	encoded, err := json.Marshal(item)
	if err != nil {
		return cli.Internal(err)
	}
	fmt.Println(string(encoded))
	return nil
}

// retestOptions are the unvalidated retest flags.
type retestOptions struct {
	Repository  string
	PullRequest int
	Head        string
	Evidence    string
}

// guardRetest verifies a retest request against current CI evidence.
func guardRetest(ctx context.Context, options retestOptions) error {
	if options.Repository == "" || options.PullRequest <= 0 || options.Head == "" || options.Evidence == "" {
		return cli.Invalid(errors.New("--repository, --pull-request, --head and --evidence are required"))
	}
	token := os.Getenv("CI_TOKEN")
	if token == "" {
		token = os.Getenv("GH_TOKEN")
	}
	reader, err := ghapi.NewWithToken(options.Repository, token)
	if err != nil {
		return cli.Incomplete("retest evidence reader: %v", err)
	}
	if err := dependabotpolicy.GuardRetest(ctx, options.Repository, options.PullRequest,
		options.Head, options.Evidence, reader.REST, time.Now().UTC()); err != nil {
		return cli.Incomplete("%v", err)
	}
	fmt.Printf("PR #%d: current failing check and comparison run verified; retest cooldown clear\n", options.PullRequest)
	return nil
}
