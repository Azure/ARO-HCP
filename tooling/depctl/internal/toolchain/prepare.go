// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0.

package toolchain

import (
	"context"
	"fmt"
	"go/version"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/mod/modfile"
)

const standaloneModule = "image-sync/oc-mirror/acrauth"

type compilerManifest struct {
	kind, path, goVersion, toolchain string
}

// PrepareManifests adjusts workspace and standalone application module pins with the target compiler.
// The caller provides a disposable, validated checkout; this operation does not publish it.
func PrepareManifests(ctx context.Context, root, target string) error {
	want := "go" + target
	if !version.IsValid(want) || version.Lang(want) == want || strings.Count(target, ".") != 2 || strings.ContainsAny(target, "-abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ") {
		return fmt.Errorf("invalid Go patch %q", target)
	}
	actual, err := installedCompilerRelease(ctx, root)
	if err != nil {
		return fmt.Errorf("identify preparation compiler: %w", err)
	}
	if actual != want {
		return fmt.Errorf("preparation needs %s, found %s", want, actual)
	}
	updates, err := compilerManifests(root)
	if err != nil {
		return err
	}
	workspace := updates[0].goVersion
	if version.Lang("go"+workspace) != version.Lang(want) || version.Compare("go"+workspace, want) > 0 {
		return fmt.Errorf("target %s is not an upgrade on the workspace's Go release line", target)
	}
	for _, item := range updates {
		if version.Compare("go"+item.goVersion, want) > 0 || version.Compare(item.toolchain, want) > 0 {
			return fmt.Errorf("refusing to downgrade %s to %s", item.path, target)
		}
	}
	pins, err := prepareConfigPins(root, target)
	if err != nil {
		return err
	}
	for _, item := range updates {
		args := []string{item.kind, "edit", "-go=" + target}
		if item.toolchain != "" && item.toolchain != "default" {
			args = append(args, "-toolchain="+want)
		}
		if _, err := compilerCommandOutput(ctx, root, append(args, item.path)...); err != nil {
			return err
		}
	}
	for name, content := range pins {
		if err := os.WriteFile(filepath.Join(root, name), content, 0644); err != nil {
			return fmt.Errorf("write compiler pin %s: %w", name, err)
		}
	}
	return nil
}

// CompilerManifests reads the same declarations for preparation and validation without Go command normalization.
func compilerManifests(root string) ([]compilerManifest, error) {
	data, err := os.ReadFile(filepath.Join(root, "go.work"))
	if err != nil {
		return nil, fmt.Errorf("read go.work: %w", err)
	}
	workspace, err := modfile.ParseWork("go.work", data, nil)
	if err != nil {
		return nil, fmt.Errorf("parse go.work: %w", err)
	}
	if workspace.Go == nil {
		return nil, fmt.Errorf("missing Go directive in go.work")
	}
	manifests := []compilerManifest{{kind: "work", path: "go.work", goVersion: workspace.Go.Version}}
	if workspace.Toolchain != nil {
		manifests[0].toolchain = workspace.Toolchain.Name
	}
	paths := make([]string, 0, len(workspace.Use)+1)
	for _, use := range workspace.Use {
		paths = append(paths, filepath.Join(use.Path, "go.mod"))
	}
	paths = append(paths, filepath.Join(standaloneModule, "go.mod"))
	seen := make(map[string]bool, len(paths))
	for _, name := range paths {
		if !filepath.IsLocal(name) || strings.HasPrefix(name, ".bingo/") || strings.HasPrefix(name, "test/sdk/") || strings.HasPrefix(name, "vendor/") {
			return nil, fmt.Errorf("workspace module is outside editable application scope: %s", name)
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		module, err := modfile.Parse(name, data, nil)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}
		if module.Go == nil {
			return nil, fmt.Errorf("missing Go directive in %s", name)
		}
		pin := compilerManifest{kind: "mod", path: name, goVersion: module.Go.Version}
		if module.Toolchain != nil {
			pin.toolchain = module.Toolchain.Name
		}
		manifests = append(manifests, pin)
	}
	return manifests, nil
}

// ValidateCompilerPins rejects any scoped declaration that does not match the installed target patch.
func validateCompilerPins(root, target string) error {
	manifests, err := compilerManifests(root)
	if err != nil {
		return err
	}
	for _, item := range manifests {
		if item.goVersion != target || item.toolchain != "" && item.toolchain != "default" && item.toolchain != "go"+target {
			return fmt.Errorf("%s does not declare target Go %s", item.path, target)
		}
	}
	edits, err := prepareConfigPins(root, target)
	if err != nil {
		return err
	}
	for name := range edits {
		return fmt.Errorf("%s does not declare target Go %s", name, target)
	}
	return nil
}

// InstalledCompilerRelease reads the Go release without the toolchain's build metadata.
func installedCompilerRelease(ctx context.Context, root string) (string, error) {
	output, err := compilerCommandOutput(ctx, root, "env", "GOVERSION")
	if err != nil {
		return "", fmt.Errorf("read installed Go release: %w", err)
	}
	release, _, _ := strings.Cut(strings.TrimSpace(string(output)), " ")
	release, _, _ = strings.Cut(release, "-X:")
	return release, nil
}

// CompilerCommandOutput runs the installed compiler without auto-selecting or downloading another version.
func compilerCommandOutput(ctx context.Context, root string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOWORK=off")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go %s: %w", strings.Join(args, " "), err)
	}
	return output, nil
}

// PrepareConfigPins computes exact compiler-pin edits without touching neighboring settings.
func prepareConfigPins(root, target string) (map[string][]byte, error) {
	patterns := map[string]*regexp.Regexp{
		"api/Makefile":                    regexp.MustCompile(`(?m)^(.*printf 'module github\.com/Azure/ARO-HCP/internal/azureapi/\$\(VERSION\)/generated\\n\\ngo )([0-9]+\.[0-9]+\.[0-9]+)(\\n'.*)$`),
		".devcontainer/devcontainer.json": regexp.MustCompile(`(?m)^(\s*"ghcr\.io/devcontainers/features/go:1": \{\s*"version": ")([0-9]+\.[0-9]+\.[0-9]+)(")`),
		"dev-infrastructure/openshift-ci/Dockerfile.nested-podman": regexp.MustCompile(`(?m)^(ENV GOTOOLCHAIN=go)([0-9]+\.[0-9]+\.[0-9]+)(\+auto)$`),
	}
	files := make(map[string][]byte, len(patterns))
	for name, pattern := range patterns {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			return nil, fmt.Errorf("read compiler pin %s: %w", name, err)
		}
		matches := pattern.FindAllSubmatchIndex(data, 2)
		if len(matches) != 1 {
			return nil, fmt.Errorf("expected one compiler pin in %s", name)
		}
		start, end := matches[0][4], matches[0][5]
		if version.Compare("go"+string(data[start:end]), "go"+target) > 0 {
			return nil, fmt.Errorf("refusing to downgrade compiler pin in %s", name)
		}
		if string(data[start:end]) == target {
			continue
		}
		files[name] = []byte(string(data[:start]) + target + string(data[end:]))
	}
	return files, nil
}
