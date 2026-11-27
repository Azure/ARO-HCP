// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0.

package toolchain

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/repository"
)

// PrepareCandidate applies the patch in a clean checkout and proves regeneration reaches a fixpoint.
func PrepareCandidate(ctx context.Context, root, base, target string, output io.Writer) error {
	repo, err := repository.Open(root)
	if err != nil {
		return fmt.Errorf("open preparation checkout: %w", err)
	}
	head, err := repo.Head(ctx)
	if err != nil {
		return fmt.Errorf("read preparation checkout HEAD: %w", err)
	}
	if base != head {
		return fmt.Errorf("checkout HEAD does not match the readiness base")
	}
	if err := requireCleanCandidate(ctx, repo); err != nil {
		return err
	}
	if err := PrepareManifests(ctx, root, target); err != nil {
		return fmt.Errorf("prepare scoped compiler pins: %w", err)
	}
	manifests, err := manifestDiff(ctx, repo, base)
	if err != nil {
		return fmt.Errorf("capture compiler-only manifest edits: %w", err)
	}
	if err := runCandidateStep(ctx, root, output, "npm", "ci", "--prefix", "api", "--legacy-peer-deps"); err != nil {
		return err
	}
	var first []byte
	for pass := range 2 {
		if err := runCandidateStep(ctx, root, output, "make", "-C", "api", "models", "testsdk"); err != nil {
			return err
		}
		if err := runCandidateStep(ctx, root, output, "make", "all-tidy"); err != nil {
			return err
		}
		if err := runCandidateStep(ctx, root, output, "make", "vendor"); err != nil {
			return err
		}
		diff, err := repo.NativeGit(ctx, nil, "", "diff", "--binary", "--no-ext-diff", base, "--")
		if err != nil {
			return fmt.Errorf("capture generated candidate: %w", err)
		}
		if pass == 0 {
			first = diff
			continue
		}
		if len(diff) == 0 || !bytes.Equal(first, diff) {
			return fmt.Errorf("toolchain regeneration did not produce a stable candidate")
		}
	}
	after, err := manifestDiff(ctx, repo, base)
	if err != nil {
		return fmt.Errorf("check generated manifest changes: %w", err)
	}
	if !bytes.Equal(manifests, after) {
		return fmt.Errorf("generation changed module or workspace declarations beyond compiler pins")
	}
	changed, err := repo.ChangedPaths(ctx, base)
	if err != nil {
		return fmt.Errorf("read candidate change scope: %w", err)
	}
	for _, path := range changed {
		if !allowedCandidatePath(path) {
			return fmt.Errorf("generation changed a path outside the Go upgrade scope: %s", path)
		}
	}
	untracked, err := repo.NativeGit(ctx, nil, "", "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return fmt.Errorf("list untracked candidate paths: %w", err)
	}
	if len(untracked) != 0 {
		return fmt.Errorf("toolchain generation created unexpected untracked files")
	}
	return nil
}

// ManifestDiff isolates module and workspace declarations from generated content.
func manifestDiff(ctx context.Context, repo *repository.Repo, base string) ([]byte, error) {
	return repo.NativeGit(ctx, nil, "", "diff", "--binary", "--no-ext-diff", base, "--", "*.mod", "*.work")
}

// AllowedCandidatePath permits scoped pins, checksum changes, and existing generator outputs.
func allowedCandidatePath(path string) bool {
	switch path {
	case "go.work", "go.work.sum", "api/Makefile", ".devcontainer/devcontainer.json", "dev-infrastructure/openshift-ci/Dockerfile.nested-podman", "vendor/modules.txt", "image-sync/oc-mirror/acrauth/vendor/modules.txt":
		return true
	}
	if strings.HasPrefix(path, ".bingo/") {
		return false
	}
	return strings.HasSuffix(path, "/go.mod") || strings.HasSuffix(path, "/go.sum") ||
		strings.HasPrefix(path, "internal/azureapi/") || strings.HasPrefix(path, "test/sdk/")
}

// ValidateCandidate runs the regular repository gates with the exact target compiler and a clean candidate commit.
func ValidateCandidate(ctx context.Context, root, target string, output io.Writer) error {
	repo, err := repository.Open(root)
	if err != nil {
		return fmt.Errorf("open candidate checkout: %w", err)
	}
	if err := requireCleanCandidate(ctx, repo); err != nil {
		return err
	}
	actual, err := compilerCommandOutput(ctx, root, "env", "GOVERSION")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(actual)) != "go"+target {
		return fmt.Errorf("validation requires installed Go %s, found %s", target, strings.TrimSpace(string(actual)))
	}
	for _, args := range [][]string{{"test-compile"}, {"lint"}, {"verify-yamlfmt"}, {"verify-vendor"}, {"-C", "config", "detect-change"}} {
		if err := runCandidateStep(ctx, root, output, "make", args...); err != nil {
			return err
		}
	}
	return requireCleanCandidate(ctx, repo)
}

// RequireCleanCandidate rejects validation of an uncommitted or changed candidate.
func requireCleanCandidate(ctx context.Context, repo *repository.Repo) error {
	dirty, err := repo.NativeGit(ctx, nil, "", "status", "--porcelain=v1", "--untracked-files=normal")
	if err != nil {
		return fmt.Errorf("inspect candidate status: %w", err)
	}
	if len(dirty) != 0 {
		return fmt.Errorf("candidate validation requires an unchanged commit")
	}
	return nil
}

// RunCandidateStep executes a fixed validation recipe without auto-selecting another Go toolchain.
func runCandidateStep(ctx context.Context, root string, output io.Writer, program string, args ...string) error {
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", program, strings.Join(args, " "), err)
	}
	return nil
}
