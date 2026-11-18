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

package orchestration

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/model"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/repository"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/schema"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/workspace"
)

func TestCheckScopePermitsOnlyPlannedBingoModules(t *testing.T) {
	plan := model.Plan{Families: []model.PlannedFamily{{
		Family: "github.com/mikefarah/yq/v4", Kind: model.FamilyGoModule,
		Targets: []model.PinTarget{{Kind: model.EditBingoModule, Source: ".bingo/yq.mod", To: "4.54.2"}},
	}}}
	allowed := []string{".bingo/yq.mod", ".bingo/yq.sum", ".bingo/variables.env", workspace.BingoHelperPath}
	if err := checkScope(allowed, plan); err != nil {
		t.Fatalf("checkScope() refused a planned change set: %v", err)
	}
	for _, changed := range [][]string{{
		".bingo/yamlfmt.mod",
	}, {
		".bingo/yamlfmt.sum",
	}, {
		".bingo/yq.mod", ".bingo/other/tool.sum",
	}} {
		if err := checkScope(changed, plan); !errors.Is(err, ErrGeneration) {
			t.Fatalf("checkScope(%v) = %v, want ErrGeneration", changed, err)
		}
	}
	// A CI-only plan does not derive the shared Bingo helpers.
	ciPlan := model.Plan{Families: []model.PlannedFamily{{
		Family: "Azure/bicep", Kind: model.FamilyGitHubRelease,
		Targets: []model.PinTarget{{Kind: model.EditMakeVariable,
			Source: "dev-infrastructure/openshift-ci/versions.mk", Variable: "BICEP_VERSION", To: "0.49.0"}},
	}}}
	for _, changed := range []string{workspace.BingoHelperPath, ".bingo/variables.env"} {
		if err := checkScope([]string{changed}, ciPlan); !errors.Is(err, ErrGeneration) {
			t.Fatalf("checkScope(%q) for a CI-only plan = %v, want ErrGeneration", changed, err)
		}
	}
}

func TestVerifyCandidateBindingRequiresACommittedCleanCandidate(t *testing.T) {
	ctx := context.Background()
	checkout, base := newCommittedCheckout(t)
	candidate, err := workspace.Prepare(ctx, checkout, filepath.Join(t.TempDir(), "candidate"), base)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	t.Cleanup(func() { _ = candidate.Close(ctx, checkout) })
	worktreeFile := filepath.Join(candidate.Path(), "file.txt")
	if err := os.WriteFile(worktreeFile, []byte("upgrade"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A decoded record that keeps the base as its commit and matches the dirty
	// worktree must not pass: nothing would be published.
	uncommitted := candidateRecord(t, ctx, candidate, base, base)
	if err := VerifyCandidateBinding(ctx, candidate, uncommitted); !errors.Is(err, ErrCandidateDrift) {
		t.Fatalf("VerifyCandidateBinding() = %v, want ErrCandidateDrift for a base-revision candidate", err)
	}
	commit, err := candidate.Root.CommitAll(ctx, "candidate")
	if err != nil {
		t.Fatalf("CommitAll() error = %v", err)
	}
	committed := candidateRecord(t, ctx, candidate, base, commit)
	if err := VerifyCandidateBinding(ctx, candidate, committed); err != nil {
		t.Fatalf("VerifyCandidateBinding() refused a committed candidate: %v", err)
	}
	if err := os.WriteFile(worktreeFile, []byte("edited after commit"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCandidateBinding(ctx, candidate, committed); !errors.Is(err, ErrCandidateDrift) {
		t.Fatalf("VerifyCandidateBinding() = %v, want ErrCandidateDrift for an uncommitted edit", err)
	}
}

// Creates a repository with one committed file and returns its base revision.
func newCommittedCheckout(t *testing.T) (*repository.Repo, string) {
	t.Helper()
	directory := t.TempDir()
	if output, err := exec.Command("git", "init", "--quiet", directory).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	checkout, err := repository.Open(directory)
	if err != nil {
		t.Fatalf("repository.Open() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "file.txt"), []byte("base"), 0o644); err != nil {
		t.Fatal(err)
	}
	base, err := checkout.CommitAll(context.Background(), "base")
	if err != nil {
		t.Fatalf("CommitAll() error = %v", err)
	}
	return checkout, base
}

// Builds a candidate record that matches the current worktree.
func candidateRecord(t *testing.T, ctx context.Context, candidate *workspace.Candidate, base, commit string) model.Candidate {
	t.Helper()
	tree, diff, changed, err := candidateDigest(ctx, candidate, base)
	if err != nil {
		t.Fatalf("candidateDigest() error = %v", err)
	}
	record := model.Candidate{
		PlanID: "plan", BaseSHA: base, CommitSHA: commit,
		TreeDigest: tree, DiffDigest: diff, ChangedPaths: changed,
	}
	record.CandidateID, err = schema.DigestsWithPrefix("depctl/candidate/v1",
		record.PlanID, base, tree, diff, changed, commit)
	if err != nil {
		t.Fatalf("DigestsWithPrefix() error = %v", err)
	}
	return record
}
