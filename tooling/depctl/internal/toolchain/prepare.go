// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0.

package toolchain

import (
	"context"
	"encoding/json"
	"fmt"
	"go/version"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

type compilerManifest struct {
	Go        string
	Toolchain string
	Use       []struct{ DiskPath string }
}

// PrepareManifests adjusts workspace and standalone application module pins with the target compiler.
// The caller provides a disposable, validated checkout; this operation does not publish it.
func PrepareManifests(ctx context.Context, root, target string) error {
	want := "go" + target
	if !version.IsValid(want) || version.Lang(want) == want || strings.Count(target, ".") != 2 || strings.ContainsAny(target, "-abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ") {
		return fmt.Errorf("invalid Go patch %q", target)
	}
	actual, err := compilerCommandOutput(ctx, root, "env", "GOVERSION")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(actual)) != want {
		return fmt.Errorf("preparation needs %s, found %s", want, strings.TrimSpace(string(actual)))
	}
	workspace, err := inspectManifest(ctx, root, "work", "go.work")
	if err != nil {
		return err
	}
	if version.Lang("go"+workspace.Go) != version.Lang(want) || version.Compare("go"+workspace.Go, want) > 0 {
		return fmt.Errorf("target %s is not an upgrade on the workspace's Go release line", target)
	}
	type update struct {
		kind, path string
		manifest   compilerManifest
	}
	updates := []update{{"work", "go.work", workspace}}
	seen := make(map[string]bool, len(workspace.Use)+1)
	paths := make([]string, 0, len(workspace.Use)+1)
	for _, use := range workspace.Use {
		paths = append(paths, filepath.Join(use.DiskPath, "go.mod"))
	}
	paths = append(paths, "image-sync/oc-mirror/acrauth/go.mod")
	for _, name := range paths {
		if !filepath.IsLocal(name) || strings.HasPrefix(name, ".bingo/") || strings.HasPrefix(name, "test/sdk/") || strings.HasPrefix(name, "vendor/") {
			return fmt.Errorf("workspace module is outside editable application scope: %s", name)
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		manifest, err := inspectManifest(ctx, root, "mod", name)
		if err != nil {
			return err
		}
		updates = append(updates, update{"mod", name, manifest})
	}
	for _, item := range updates {
		if version.Compare("go"+item.manifest.Go, want) > 0 || version.Compare(item.manifest.Toolchain, want) > 0 {
			return fmt.Errorf("refusing to downgrade %s to %s", item.path, target)
		}
	}
	pins, err := prepareConfigPins(root, target)
	if err != nil {
		return err
	}
	for _, item := range updates {
		args := []string{item.kind, "edit", "-go=" + target}
		if item.manifest.Toolchain != "" {
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

// InspectManifest delegates workspace and module parsing to the Go command.
func inspectManifest(ctx context.Context, root, kind, name string) (compilerManifest, error) {
	var manifest compilerManifest
	output, err := compilerCommandOutput(ctx, root, kind, "edit", "-json", name)
	if err != nil {
		return manifest, err
	}
	if err := json.Unmarshal(output, &manifest); err != nil {
		return manifest, fmt.Errorf("decode %s: %w", name, err)
	}
	if !version.IsValid("go" + manifest.Go) {
		return manifest, fmt.Errorf("invalid Go directive in %s", name)
	}
	return manifest, nil
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
		files[name] = []byte(string(data[:start]) + target + string(data[end:]))
	}
	return files, nil
}
