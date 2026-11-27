// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0.

package toolchain

import (
	"bytes"
	"context"
	"go/version"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/repository"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/testkit"
)

func TestInstalledCompilerReleaseWithBuildMetadata(t *testing.T) {
	for _, test := range []struct{ name, reported, want string }{
		{"distribution metadata", "go1.26.10 (Red Hat) X:strictfipsruntime", "go1.26.10"},
		{"experiment suffix", "go1.26.11-X:nodwarf5", "go1.26.11"},
		{"prerelease unchanged", "go1.27rc1-X:nodwarf5", "go1.27rc1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "go"), []byte("#!/bin/sh\nprintf '%s\\n' '"+test.reported+"'\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
			if got, err := installedCompilerRelease(t.Context(), root); err != nil || got != test.want {
				t.Fatalf("installed compiler release = %q, %v; want %s", got, err, test.want)
			}
		})
	}
}

func TestPrepareManifests(t *testing.T) {
	ctx := context.Background()
	compiler, err := installedCompilerRelease(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target := strings.TrimPrefix(compiler, "go")
	minimum := strings.TrimPrefix(version.Lang(compiler), "go") + ".0"
	root := t.TempDir()
	const manifest = "module example.com/test\n\ngo %s\n"
	paths := map[string]string{
		"go.work":                             "go " + minimum + "\n\nuse ./application\n",
		"application/go.mod":                  strings.ReplaceAll(manifest, "%s", minimum) + "\ntoolchain go" + minimum + "\n",
		"image-sync/oc-mirror/acrauth/go.mod": strings.ReplaceAll(manifest, "%s", minimum),
		"api/Makefile":                        "printf 'module github.com/Azure/ARO-HCP/internal/azureapi/$(VERSION)/generated\\n\\ngo " + minimum + "\\n' > $(TYPESPEC_GO_OUTPUT_DIR)/go.mod; \\\n",
		".devcontainer/devcontainer.json":     "{\n \"ghcr.io/devcontainers/features/go:1\": {\n \"version\": \"" + minimum + "\"\n }\n}\n",
		"dev-infrastructure/openshift-ci/Dockerfile.nested-podman": "ENV GOTOOLCHAIN=go" + minimum + "+auto\n",
		".bingo/tool.mod":           strings.ReplaceAll(manifest, "%s", minimum),
		"test/sdk/generated/go.mod": strings.ReplaceAll(manifest, "%s", minimum),
	}
	for name, data := range paths {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := PrepareManifests(ctx, root, target); err != nil {
		t.Fatal(err)
	}
	for name, original := range paths {
		text, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(name, ".bingo/") || strings.HasPrefix(name, "test/sdk/") {
			if string(text) != original {
				t.Fatalf("protected %s changed: %s", name, text)
			}
			continue
		}
		if name == "go.work" || strings.HasSuffix(name, "/go.mod") {
			if !strings.Contains("\n"+string(text), "\ngo "+target+"\n") {
				t.Fatalf("%s lacks target Go pin: %s", name, text)
			}
			continue
		}
		if string(text) != strings.Replace(original, minimum, target, 1) {
			t.Fatalf("%s compiler edit differs: %s", name, text)
		}
	}
	if err := validateCompilerPins(root, target); err != nil {
		t.Fatalf("prepared pins rejected: %v", err)
	}
	for _, stale := range []string{"go.work", "application/go.mod", "image-sync/oc-mirror/acrauth/go.mod", "api/Makefile"} {
		t.Run("stale "+stale, func(t *testing.T) {
			path := filepath.Join(root, stale)
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := os.WriteFile(path, content, 0600); err != nil {
					t.Error(err)
				}
			})
			if err := os.WriteFile(path, []byte(strings.ReplaceAll(string(content), target, "1.0.0")), 0600); err != nil {
				t.Fatal(err)
			}
			if err := validateCompilerPins(root, target); err == nil || !strings.Contains(err.Error(), stale) {
				t.Fatalf("expected stale compiler pin in %s, got %v", stale, err)
			}
		})
	}
}

func TestAllowedCandidatePathAzureModels(t *testing.T) {
	for path, want := range map[string]bool{
		"internal/azureapi/v20261001/generated/models.go": true,
		"internal/azureapi/readonly_test.go":              false,
		"internal/azureapi/v20261001/go.mod":              false,
	} {
		if got := allowedCandidatePath(path); got != want {
			t.Errorf("allowedCandidatePath(%q) = %t, want %t", path, got, want)
		}
	}
}

func TestCandidateStepUsesCandidateWorkspace(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "go.work")
	t.Setenv("GOWORK", filepath.Join(t.TempDir(), "go.work"))
	if err := os.WriteFile(workspace, []byte("go 1.26.0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runCandidateStep(t.Context(), root, &out, "go", "env", "GOWORK"); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != workspace {
		t.Fatalf("step used workspace %q, want %q", got, workspace)
	}
}

func TestCompilerPinDiffIncludesNeighboringSettings(t *testing.T) {
	fixture, err := testkit.CreateFixture(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo, err := repository.Open(fixture.Dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{generatorRecipe, devcontainerPath, nestedPodmanPath} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(fixture.Dir, name)
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("go=1.26.0\nneighbor=original\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := fixture.Commit(t.Context(), "record compiler pin file"); err != nil {
				t.Fatal(err)
			}
			base, err := repo.Head(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			before, err := compilerPinDiff(t.Context(), repo, base)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("go=1.26.0\nneighbor=changed\n"), 0600); err != nil {
				t.Fatal(err)
			}
			after, err := compilerPinDiff(t.Context(), repo, base)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Equal(before, after) {
				t.Fatalf("pin diff missed a neighboring edit in %s", name)
			}
		})
	}
}
