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
	"errors"
	"fmt"
	"go/version"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/repository"
)

// Repository is the only repository supported by the readiness plan.
const Repository = "Azure/ARO-HCP"

// ErrBlocked distinguishes a verified blocker from unavailable evidence.
var ErrBlocked = errors.New("Go toolchain upgrade blocked")

// Plan binds an official stable patch to the exact base and its readiness evidence.
type Plan struct {
	Schema         int               `json:"schema"`
	Status         string            `json:"status"`
	Repo           string            `json:"repo"`
	Current        string            `json:"current"`
	Target         string            `json:"target"`
	BaseSHA        string            `json:"base_sha"`
	Reason         string            `json:"reason"`
	Builders       []builderEvidence `json:"builders"`
	ReleaseConfigs []string          `json:"release_configs"`
	OverlappingPRs []Upgrade         `json:"overlapping_prs"`
}

// MakePlan discovers toolchain readiness without changing the checkout or remote state.
func MakePlan(ctx context.Context, root, repo string, client *http.Client) (Plan, error) {
	plan := Plan{Schema: 1, Repo: repo, Status: "blocked", Builders: []builderEvidence{}, ReleaseConfigs: []string{}, OverlappingPRs: []Upgrade{}}
	if repo != Repository || client == nil {
		return plan, fmt.Errorf("toolchain planning requires %s and a release HTTP client", Repository)
	}
	if err := populatePlan(ctx, root, client, &plan); err != nil {
		if errors.Is(err, ErrBlocked) {
			plan.Reason = err.Error()
			return plan, nil
		}
		return plan, fmt.Errorf("determine Go toolchain readiness: %w", err)
	}
	return plan, nil
}

// PopulatePlan stops at the first unverified prerequisite.
func populatePlan(ctx context.Context, root string, client *http.Client, plan *Plan) error {
	repo, err := repository.Open(root)
	if err != nil {
		return fmt.Errorf("open checkout: %w", err)
	}
	status, err := repo.NativeGit(ctx, nil, "", "--no-optional-locks", "status", "--porcelain", "--untracked-files=no", "--ignore-submodules=none")
	if err != nil {
		return fmt.Errorf("inspect tracked checkout: %w", err)
	}
	if len(status) != 0 {
		return fmt.Errorf("plan requires a clean tracked checkout")
	}
	head, err := repo.NativeGit(ctx, nil, "", "rev-parse", "--show-toplevel", "HEAD")
	if err != nil {
		return fmt.Errorf("resolve checkout revision: %w", err)
	}
	top, sha, _ := strings.Cut(strings.TrimSuffix(string(head), "\n"), "\n")
	if top != root || !repository.IsCommit(sha) {
		return fmt.Errorf("--root must name the top-level Git checkout")
	}
	plan.BaseSHA = sha
	if err := verifyDefaultHead(ctx, plan.Repo, plan.BaseSHA); err != nil {
		return fmt.Errorf("verify checkout revision: %w", err)
	}
	checkout, err := os.OpenRoot(root)
	if err != nil {
		return fmt.Errorf("open checkout files: %w", err)
	}
	defer checkout.Close()
	workspace, err := readWorkspaceVersion(ctx, root, checkout)
	if err != nil {
		return err
	}
	plan.Current = stableDirectiveVersion(workspace.Go)
	plan.Target, err = fetchLatestPatch(ctx, client, plan.Current)
	if err != nil {
		return fmt.Errorf("select official Go patch: %w", err)
	}
	if version.Compare(workspace.Toolchain, "go"+plan.Target) > 0 {
		return blockedf("go.work already requests newer toolchain %s", workspace.Toolchain)
	}
	if workspace.Go == plan.Target && (workspace.Toolchain == "" || workspace.Toolchain == "go"+plan.Target) {
		plan.Status, plan.Reason = "current", fmt.Sprintf("workspace already requests Go %s", plan.Target)
		return nil
	}
	plan.OverlappingPRs, err = openUpgrades(ctx, plan.Repo)
	if err != nil {
		return fmt.Errorf("inspect existing upgrades: %w", err)
	}
	if len(plan.OverlappingPRs) != 0 {
		return blockedf("possible compiler configuration overlap with PR #%d", plan.OverlappingPRs[0].Number)
	}
	builders, configs, err := builderReadiness(ctx, checkout, plan.Target)
	plan.Builders = append(plan.Builders, builders...)
	plan.ReleaseConfigs = append(plan.ReleaseConfigs, configs...)
	if err != nil {
		return fmt.Errorf("verify CI builders: %w", err)
	}
	plan.Status, plan.Reason = "ready", fmt.Sprintf("official Go %s and all discovered builders are ready", plan.Target)
	return nil
}

type workspaceVersion struct {
	Go        string
	Toolchain string
}

// ReadWorkspaceVersion delegates Go workspace parsing to the installed compiler without downloading tools.
func readWorkspaceVersion(ctx context.Context, root string, checkout *os.Root) (workspaceVersion, error) {
	var result workspaceVersion
	info, err := checkout.Lstat("go.work")
	if err != nil {
		return result, fmt.Errorf("inspect go.work: %w", err)
	}
	if !info.Mode().IsRegular() {
		return result, fmt.Errorf("go.work must be a regular file")
	}
	cmd := exec.CommandContext(ctx, "go", "work", "edit", "-json", "go.work")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local")
	data, err := cmd.Output()
	if err != nil {
		return result, fmt.Errorf("read workspace compiler directives: %w", err)
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return result, fmt.Errorf("decode workspace compiler directives: %w", err)
	}
	return result, nil
}
