// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0.

package toolchain

import (
	"context"
	"go/version"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareManifests(t *testing.T) {
	ctx := context.Background()
	actual, err := compilerCommandOutput(ctx, t.TempDir(), "env", "GOVERSION")
	if err != nil {
		t.Fatal(err)
	}
	compiler := strings.TrimSpace(string(actual))
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
