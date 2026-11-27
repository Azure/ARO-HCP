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
	"path/filepath"
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
	pins, err := compilerPinDiff(ctx, repo, base)
	if err != nil {
		return fmt.Errorf("capture compiler pin edits: %w", err)
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
	after, err := compilerPinDiff(ctx, repo, base)
	if err != nil {
		return fmt.Errorf("check generated compiler pin changes: %w", err)
	}
	if !bytes.Equal(pins, after) {
		return fmt.Errorf("generation changed compiler pin files beyond the prepared edits")
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

// CompilerPinDiff isolates the files preparation edits from generated content.
func compilerPinDiff(ctx context.Context, repo *repository.Repo, base string) ([]byte, error) {
	return repo.NativeGit(ctx, nil, "", "diff", "--binary", "--no-ext-diff", base, "--",
		"*.mod", "*.work", generatorRecipe, devcontainerPath, nestedPodmanPath)
}

// AllowedCandidatePath permits scoped pins, checksum changes, and existing generator outputs.
func allowedCandidatePath(path string) bool {
	switch path {
	case "go.work", "go.work.sum", generatorRecipe, devcontainerPath, nestedPodmanPath, "vendor/modules.txt", "image-sync/oc-mirror/acrauth/vendor/modules.txt":
		return true
	}
	if strings.HasPrefix(path, ".bingo/") {
		return false
	}
	if versionPath, ok := strings.CutPrefix(path, "internal/azureapi/"); ok {
		versionName, generatedPath, versioned := strings.Cut(versionPath, "/")
		return versioned && versionName != "" && strings.HasPrefix(generatedPath, "generated/")
	}
	return strings.HasSuffix(path, "/go.mod") || strings.HasSuffix(path, "/go.sum") || strings.HasPrefix(path, "test/sdk/")
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
	actual, err := installedCompilerRelease(ctx, root)
	if err != nil {
		return fmt.Errorf("identify validation compiler: %w", err)
	}
	if actual != "go"+target {
		return fmt.Errorf("validation requires installed Go %s, found %s", target, actual)
	}
	if err := validateCompilerPins(root, target); err != nil {
		return fmt.Errorf("validate candidate compiler pins: %w", err)
	}
	if _, err := compilerCommandOutput(ctx, filepath.Join(root, standaloneModule), "build", "-mod=vendor", "-o", os.DevNull, "./..."); err != nil {
		return fmt.Errorf("compile standalone acrauth module: %w", err)
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

// RunCandidateStep runs workspace recipes with the candidate's Go workspace and the installed toolchain.
func runCandidateStep(ctx context.Context, root string, output io.Writer, program string, args ...string) error {
	workspace, err := filepath.Abs(filepath.Join(root, "go.work"))
	if err != nil {
		return fmt.Errorf("resolve candidate workspace: %w", err)
	}
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOWORK="+workspace)
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", program, strings.Join(args, " "), err)
	}
	return nil
}
