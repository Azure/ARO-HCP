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
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestProbeBuildersBlocksEmptyInventory prevents readiness without any compiler evidence.
func TestProbeBuildersBlocksEmptyInventory(t *testing.T) {
	_, err := probeBuilders(t.Context(), "1.26.7", nil, nil)
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("empty builder inventory must block readiness, got %v", err)
	}
}

// TestPublishedBuilder verifies that execution uses only the correct immutable image and platform.
func TestPublishedBuilder(t *testing.T) {
	const image = "registry.example.org/ci/build:tag"
	const target = "1.26.7"
	const platform = "linux/arm64"
	sha := "sha256:" + strings.Repeat("a", 64)
	digest := "registry.example.org/ci/build@" + sha
	pull := "Digest: " + sha
	tests := []struct {
		name      string
		pull      string
		version   string
		toolchain string
		blocked   bool
	}{
		{"matching arm64 digest", pull, "go version go1.26.7 linux/arm64", "", false},
		{"explicit Podman toolchain", pull, "go version go1.26.7 linux/arm64", "go1.26.7", false},
		{"no digest", "", "", "", true},
		{"multiple repository digests", pull + "\nDigest: sha256:" + strings.Repeat("b", 64), "", "", true},
		{"wrong platform", pull, "go version go1.26.7 linux/amd64", "", true},
		{"prerelease compiler", pull, "go version go1.26rc1 linux/arm64", "", true},
		{"different patch", pull, "go version go1.26.6 linux/arm64", "", true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := func(_ context.Context, _ time.Duration, program string, args ...string) (string, error) {
				if program != "docker" {
					t.Fatalf("unexpected probe program %q", program)
				}
				switch args[0] {
				case "pull":
					if !slices.Equal(args, []string{"pull", "--platform", "linux/arm64", image}) {
						t.Fatalf("unexpected pull: %q", args)
					}
					return test.pull, nil
				case "run":
					if test.version == "" || slices.Contains(args, image) || !slices.Equal(args[len(args)-3:], []string{"go", digest, "version"}) {
						t.Fatalf("attempted to execute without an unambiguous digest: %q", args)
					}
					required := []string{"--pull=never", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "GOENV=off"}
					if test.toolchain == "" {
						required = append(required, "--network=none", "GOTOOLCHAIN=local")
					}
					if test.toolchain != "" {
						required = append(required, "--network=bridge", "--tmpfs", "GOTOOLCHAIN="+test.toolchain,
							"GOAUTH=off", "GOPROXY=https://proxy.golang.org", "GOSUMDB=sum.golang.org", "HTTPS_PROXY=")
					}
					for _, option := range required {
						if !slices.Contains(args, option) {
							t.Fatalf("missing runtime safeguard %q in %q", option, args)
						}
					}
					return test.version, nil
				default:
					t.Fatalf("unexpected probe command %q", args)
				}
				return "", nil
			}
			root := compilerImage{image, platform, test.toolchain}
			evidence, err := probeBuilders(t.Context(), target, map[compilerImage][]string{root: {"prow.yaml"}}, command)
			if test.blocked {
				if !errors.Is(err, ErrBlocked) {
					t.Fatalf("wanted verified blocker, got evidence %+v, err %v", evidence, err)
				}
				if test.version == "go version go1.26.6 linux/arm64" &&
					(len(evidence) != 1 || evidence[0].Digest != digest || evidence[0].Version != "1.26.6") {
					t.Fatalf("missing evidence for wrong patch: %+v", evidence)
				}
				return
			}
			if err != nil || len(evidence) != 1 || evidence[0].Digest != digest || evidence[0].Version != target ||
				evidence[0].Platform != platform || evidence[0].Toolchain != test.toolchain || !slices.Equal(evidence[0].Sources, []string{"prow.yaml"}) {
				t.Fatalf("unexpected evidence %+v, err %v", evidence, err)
			}
		})
	}
}

// TestProwCompilerRoots checks the recognized config shapes and rejects changed compiler provenance.
func TestProwCompilerRoots(t *testing.T) {
	const local = "registry.ci.openshift.org/ocp/builder:upstream"
	const stream = "build_root:\n  image_stream_tag:\n    name: aro-hcp-ci-images\n    namespace: aro-hcp\n    tag: "
	const tests = "tests:\n- as: default\n- as: arm\n  capabilities: [arm64]\n"
	cases := []struct {
		name, config string
		want         []compilerImage
	}{
		{"repository root", "build_root: {from_repository: true}\n" + tests, []compilerImage{{local, "linux/amd64", ""}, {local, "linux/arm64", ""}}},
		{"promoted root", stream + "aro-hcp-e2e-base-ci\n" + tests, []compilerImage{{promotedRoot, "linux/amd64", ""}, {promotedRoot, "linux/arm64", ""}}},
		{"nested root", stream + "aro-hcp-nested-podman-ci\ntests: [{capabilities: [arm64]}]\n", []compilerImage{{promotedPodman, "linux/arm64", "go1.26.7"}}},
		{"prebuilt periodic", "tests: [{as: periodic}]\n", []compilerImage{}},
		{"unexpected build work", "binary_build_commands: make\n", nil},
		{"changed promoted name", stream + "other\n", nil},
		{"alternate registry", stream + "aro-hcp-e2e-base-ci\n    cluster: example\n", nil},
		{"unknown architecture", stream + "aro-hcp-e2e-base-ci\ntests: [{capabilities: [s390x]}]\n", nil},
		{"changed CAPZ recipe", "build_root:\n  project_image:\n    dockerfile_literal: |\n      FROM registry.ci.openshift.org/ocp/builder:upstream\n      RUN dnf install golang\n", nil},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			roots, err := prowCompilerRoots([]byte(test.config), local, "1.26.7")
			if test.want == nil {
				if !errors.Is(err, ErrBlocked) {
					t.Fatalf("want blocked, got roots %+v, err %v", roots, err)
				}
				return
			}
			if err != nil || !slices.Equal(roots, test.want) {
				t.Fatalf("want roots %+v, got roots %+v, err %v", test.want, roots, err)
			}
		})
	}
}
