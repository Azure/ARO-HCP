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

package toolchain

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"

	gh "github.com/google/go-github/v69/github"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/repository"
	githublib "github.com/Azure/ARO-HCP/tooling/github"
)

// Upgrade identifies an existing compiler upgrade that prevents a duplicate proposal.
type Upgrade struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
}

// Blockedf describes a normal readiness blocker.
func blockedf(format string, args ...any) error {
	return fmt.Errorf("%s: %w", fmt.Sprintf(format, args...), ErrBlocked)
}

// ReadGitHub decodes a trusted repository GET, preserving errors as unavailable evidence.
func readGitHub(ctx context.Context, endpoint string, result any, pages bool) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	args := []string{"api", "--hostname", "github.com", "--method", "GET"}
	if pages {
		args = append(args, "--paginate", "--slurp")
	}
	out, err := exec.CommandContext(ctx, "gh", append(args, endpoint)...).Output()
	if err != nil {
		return fmt.Errorf("GitHub GET %s: %w", endpoint, err)
	}
	if err := json.Unmarshal(out, result); err != nil {
		return fmt.Errorf("decode GitHub GET %s: %w", endpoint, err)
	}
	return nil
}

// DefaultHead resolves the live default branch to an immutable commit.
func defaultHead(ctx context.Context, repo string, read func(context.Context, string, any, bool) error) (string, error) {
	var metadata gh.Repository
	if err := read(ctx, "repos/"+repo, &metadata, false); err != nil {
		return "", fmt.Errorf("resolve GitHub default branch: %w", err)
	}
	if metadata.GetDefaultBranch() == "" {
		return "", fmt.Errorf("missing default branch for %s", repo)
	}
	var commit gh.RepositoryCommit
	if err := read(ctx, "repos/"+repo+"/commits/"+url.PathEscape(metadata.GetDefaultBranch()), &commit, false); err != nil {
		return "", fmt.Errorf("resolve default branch revision: %w", err)
	}
	if !repository.IsCommit(commit.GetSHA()) {
		return "", fmt.Errorf("invalid default commit for %s", repo)
	}
	return commit.GetSHA(), nil
}

// VerifyDefaultHead rejects a stale checkout rather than preparing changes on it.
func verifyDefaultHead(ctx context.Context, repo, expected string) error {
	sha, err := defaultHead(ctx, repo, readGitHub)
	if err != nil {
		return fmt.Errorf("verify default branch: %w", err)
	}
	if sha != expected {
		return blockedf("checkout is not the current default-branch HEAD")
	}
	return nil
}

// readGitHubFile reads a file at an immutable commit for comparison.
func readGitHubFile(ctx context.Context, read func(context.Context, string, any, bool) error, repo, name, sha string) (string, error) {
	var file gh.RepositoryContent
	location := url.URL{Path: "repos/" + repo + "/contents/" + name, RawQuery: "ref=" + sha}
	if err := read(ctx, location.String(), &file, false); err != nil {
		return "", fmt.Errorf("read %s at %s: %w", name, sha, err)
	}
	if file.GetType() != "file" || file.GetEncoding() != "base64" || file.Content == nil || !repository.IsCommit(file.GetSHA()) {
		return "", fmt.Errorf("incomplete content for %s at %s", name, sha)
	}
	body, err := file.GetContent()
	if err != nil {
		return "", fmt.Errorf("decode GitHub file %s: %w", name, err)
	}
	return body, nil
}

// openUpgrades checks every open PR, regardless of author, for compiler overlap.
func openUpgrades(ctx context.Context, repo string) ([]Upgrade, error) {
	credential := strings.TrimSpace(os.Getenv("GH_TOKEN"))
	if credential == "" {
		authCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		token, err := exec.CommandContext(authCtx, "gh", "auth", "token", "--hostname", "github.com").Output()
		if err != nil {
			return nil, fmt.Errorf("read GitHub credential for PR inventory: %w", err)
		}
		credential = strings.TrimSpace(string(token))
	}
	if credential == "" {
		return nil, fmt.Errorf("empty GitHub credential for PR inventory")
	}
	api := gh.NewClient(&http.Client{Timeout: 2 * time.Minute}).WithAuthToken(credential)
	client, err := githublib.NewClient(repo, api)
	if err != nil {
		return nil, fmt.Errorf("configure GitHub PR inventory: %w", err)
	}
	return findOpenUpgrade(ctx, repo, client.OpenPullRequests, readGitHub)
}

// findOpenUpgrade screens the complete inventory and stops on confirmed overlap or incomplete evidence.
func findOpenUpgrade(ctx context.Context, repo string, list func(context.Context) ([]githublib.PullRequest, error), read func(context.Context, string, any, bool) error) ([]Upgrade, error) {
	pulls, err := list(ctx)
	if err != nil {
		return nil, fmt.Errorf("list open upgrade candidates: %w", err)
	}
	for _, pull := range pulls {
		overlaps, err := upgradePull(ctx, repo, pull, read)
		if err != nil {
			return nil, fmt.Errorf("inspect open PR #%d: %w", pull.Number, err)
		}
		if overlaps {
			return []Upgrade{{Number: pull.Number, URL: pull.URL(repo)}}, nil
		}
	}
	return []Upgrade{}, nil
}

// UpgradePull checks revision-bound changed-file evidence for possible compiler overlap.
func upgradePull(ctx context.Context, repo string, pull githublib.PullRequest, read func(context.Context, string, any, bool) error) (bool, error) {
	endpoint := fmt.Sprintf("repos/%s/pulls/%d", repo, pull.Number)
	var detail gh.PullRequest
	if err := read(ctx, endpoint, &detail, false); err != nil {
		return false, fmt.Errorf("read PR revisions: %w", err)
	}
	base, head := detail.GetBase().GetSHA(), detail.GetHead().GetSHA()
	if detail.GetNumber() != pull.Number || detail.GetState() != "open" || detail.GetBase().GetRepo().GetFullName() != repo ||
		!repository.IsCommit(base) || !repository.IsCommit(head) || head != pull.HeadSHA {
		return false, fmt.Errorf("PR identity or revisions do not match the open inventory")
	}
	var pages [][]gh.CommitFile
	if err := read(ctx, endpoint+"/files?per_page=100", &pages, true); err != nil {
		return false, fmt.Errorf("read PR changed files: %w", err)
	}
	if detail.ChangedFiles == nil || len(pages) == 0 || detail.GetChangedFiles() < 0 || detail.GetChangedFiles() >= 3000 {
		return false, fmt.Errorf("cannot inspect complete PR file list")
	}
	seen := make(map[string]bool, detail.GetChangedFiles())
	for _, page := range pages {
		if page == nil {
			return false, fmt.Errorf("PR file list contains an incomplete page")
		}
		for _, file := range page {
			name := file.GetFilename()
			previous := file.GetPreviousFilename()
			if !validCheckoutPath(name) || seen[name] || previous != "" && !validCheckoutPath(previous) {
				return false, fmt.Errorf("incomplete or duplicate PR file metadata for %q", name)
			}
			seen[name] = true
			switch file.GetStatus() {
			case "added", "removed", "modified", "changed":
			case "renamed", "copied":
				if previous == "" {
					return false, fmt.Errorf("missing previous PR file path for %q", name)
				}
			default:
				return false, fmt.Errorf("unrecognized PR file status for %q", name)
			}
			if !compilerPath(name) && !compilerPath(previous) {
				continue
			}
			if !goManifestPath(name) || file.GetStatus() != "modified" || manifestOverlaps(file) {
				return true, nil
			}
		}
	}
	if len(seen) != detail.GetChangedFiles() {
		return false, fmt.Errorf("cannot inspect complete PR file list")
	}
	var current gh.PullRequest
	if err := read(ctx, endpoint, &current, false); err != nil {
		return false, fmt.Errorf("recheck PR revisions: %w", err)
	}
	if current.GetNumber() != pull.Number || current.GetState() != "open" || current.GetBase().GetRepo().GetFullName() != repo ||
		current.GetBase().GetSHA() != base || current.GetHead().GetSHA() != head || current.ChangedFiles == nil || current.GetChangedFiles() != len(seen) {
		return false, fmt.Errorf("PR changed while inspecting compiler inputs")
	}
	return false, nil
}

// CompilerPath recognizes changes which could overlap the compiler proposal.
func compilerPath(name string) bool {
	return name == devcontainerPath || name == generatorRecipe || name == nestedPodmanPath ||
		name == localCIConfig || goManifestPath(name)
}

// GoManifestPath excludes tool metadata and vendor copies from workspace overlap.
func goManifestPath(name string) bool {
	return !strings.HasPrefix(name, ".bingo/") && !strings.HasPrefix(name, "vendor/") &&
		(path.Base(name) == "go.mod" || path.Base(name) == "go.work")
}

// ManifestOverlaps treats unavailable or incomplete patches as potential compiler changes.
func manifestOverlaps(file gh.CommitFile) bool {
	if file.Patch == nil || file.Additions == nil || file.Deletions == nil {
		return true
	}
	var added, removed int
	for line := range strings.SplitSeq(file.GetPatch(), "\n") {
		if line == "" {
			continue
		}
		switch line[0] {
		case '+':
			added++
		case '-':
			removed++
		default:
			continue
		}
		if compilerLine.MatchString(line[1:]) {
			return true
		}
	}
	return added+removed == 0 || added != file.GetAdditions() || removed != file.GetDeletions()
}
