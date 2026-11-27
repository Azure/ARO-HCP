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
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

const (
	localCIConfig        = ".ci-operator.yaml"
	releaseRepository    = "openshift/release"
	releaseConfigDir     = "ci-operator/config/" + Repository
	mainReleaseConfig    = "Azure-ARO-HCP-main.yaml"
	compilerPlatform     = "linux/amd64"
	armCompilerPlatform  = "linux/arm64"
	compilerCommandLimit = 2 * time.Minute
	compilerPullLimit    = 5 * time.Minute
	ciRegistry           = "registry.ci.openshift.org/"
	promotedRoot         = ciRegistry + "aro-hcp/aro-hcp-ci-images:aro-hcp-e2e-base-ci"
	promotedPodman       = ciRegistry + "aro-hcp/aro-hcp-ci-images:aro-hcp-nested-podman-ci"
	// CAPZ installs ancillary tools but does not replace Go from FROM. Any
	// recipe change other than the builder tag requires reviewing provenance.
	capzRecipeBodySHA256 = "9004f739de9a31d3df4e591201e9add7ff92fc0c4cc70de669f750aba4d30dc1"
)

var (
	imageComponentPattern   = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	goCompilerOutputPattern = regexp.MustCompile(`^go version go([^ ]+) linux/(amd64|arm64)$`)
	imageIDPattern          = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	probeAuthorization      = regexp.MustCompile(`(?i)unauthorized|forbidden|denied|insufficient[_ ]scope|authentication required|\b40[13]\b`)
	probeUnavailable        = regexp.MustCompile(`(?i)manifest unknown|manifest[^\n]*not found|no matching manifest|registry unavailable|` +
		`connection refused|connection reset|timed out|i/o timeout|temporary failure|no such host|could not resolve|` +
		`cannot connect to the docker daemon|executable file not found|go: not found|toolchain not available`)
	capzBasePattern = regexp.MustCompile(`^FROM (registry\.ci\.openshift\.org/ocp/builder:[a-zA-Z0-9_.-]+)$`)
)

type builderEvidence struct {
	Image     string   `json:"image"`
	Digest    string   `json:"digest,omitempty"`
	Version   string   `json:"version,omitempty"`
	Platform  string   `json:"platform,omitempty"`
	Sources   []string `json:"sources"`
	Toolchain string   `json:"toolchain_override,omitempty"`
}

type compilerImage struct {
	reference string
	platform  string
	toolchain string
}

type prowCompilerConfig struct {
	BuildRoot  map[string]any               `yaml:"build_root"`
	Binary     string                       `yaml:"binary_build_commands"`
	TestBuild  string                       `yaml:"test_binary_build_commands"`
	Images     map[string]any               `yaml:"images"`
	BaseImages map[string]map[string]string `yaml:"base_images"`
	Tests      []struct {
		Capabilities []string `yaml:"capabilities"`
	} `yaml:"tests"`
}

type compilerCommand func(context.Context, time.Duration, string, ...string) (string, error)

// BuilderReadiness checks known live Prow compiler roots and reuses the CI metadata verification.
func builderReadiness(ctx context.Context, checkout *os.Root, target string) ([]builderEvidence, []string, error) {
	localText, err := readRegularFile(checkout, localCIConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", localCIConfig, err)
	}
	var local struct {
		BuildRoot map[string]string `yaml:"build_root_image"`
	}
	if err := yaml.Unmarshal([]byte(localText), &local); err != nil {
		return nil, nil, fmt.Errorf("decode %s: %w", localCIConfig, err)
	}
	localRoot, err := ciImageStream(local.BuildRoot)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve %s: %w", localCIConfig, err)
	}
	if local.BuildRoot["namespace"] != "ocp" || local.BuildRoot["name"] != "builder" {
		return nil, nil, blockedf("unsupported repository build root %s", localRoot)
	}
	images, configs, err := liveProwCompilers(ctx, localRoot, target)
	if err != nil {
		return nil, configs, fmt.Errorf("resolve live Prow compiler roots: %w", err)
	}
	if _, err := runProbeCommand(ctx, runCompilerCommand, compilerCommandLimit, "make", "-C",
		checkout.Name()+"/dev-infrastructure/openshift-ci", "verify", "BUILDER_IMAGE_TAG="+local.BuildRoot["tag"]); err != nil {
		return nil, configs, fmt.Errorf("verify OpenShift CI image metadata: %w", err)
	}
	evidence, err := probeBuilders(ctx, target, images, runCompilerCommand)
	return evidence, configs, err
}

// CIImageStream resolves only explicit OpenShift ImageStreamTags in the CI registry.
func ciImageStream(stream map[string]string) (string, error) {
	if len(stream) != 3 || !imageComponentPattern.MatchString(stream["namespace"]) ||
		!imageComponentPattern.MatchString(stream["name"]) || !imageComponentPattern.MatchString(stream["tag"]) {
		return "", blockedf("unsupported CI image stream reference: %v", stream)
	}
	return ciRegistry + stream["namespace"] + "/" + stream["name"] + ":" + stream["tag"], nil
}

// LiveProwCompilers discovers compiler roots across main configs at one immutable release commit.
func liveProwCompilers(ctx context.Context, localRoot, target string) (map[compilerImage][]string, []string, error) {
	revision, err := defaultHead(ctx, releaseRepository, readGitHub)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve %s default branch: %w", releaseRepository, err)
	}
	var items []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	if err := readGitHub(ctx, "repos/"+releaseRepository+"/contents/"+releaseConfigDir+"?ref="+revision, &items, false); err != nil {
		return nil, nil, fmt.Errorf("list Prow config variants: %w", err)
	}
	names := make(map[string]struct{})
	prefix := strings.TrimSuffix(mainReleaseConfig, ".yaml")
	for _, item := range items {
		if item.Name != mainReleaseConfig && (!strings.HasPrefix(item.Name, prefix+"__") || !strings.HasSuffix(item.Name, ".yaml")) {
			continue
		}
		_, duplicate := names[item.Name]
		if item.Type != "file" || duplicate {
			return nil, nil, blockedf("invalid or duplicate Prow config %s", item.Name)
		}
		names[item.Name] = struct{}{}
	}
	if _, ok := names[mainReleaseConfig]; !ok {
		return nil, nil, blockedf("missing main Prow config")
	}
	images := map[compilerImage][]string{}
	configs := make([]string, 0, len(names))
	for _, name := range slices.Sorted(maps.Keys(names)) {
		source := releaseConfigDir + "/" + name
		text, err := readGitHubFile(ctx, readGitHub, releaseRepository, source, revision)
		if err != nil {
			return nil, configs, fmt.Errorf("read Prow config %s: %w", source, err)
		}
		content := []byte(text)
		configs = append(configs, fmt.Sprintf("%s@%s#sha256:%x", source, revision, sha256.Sum256(content)))
		roots, err := prowCompilerRoots(content, localRoot, target)
		if err != nil {
			return nil, configs, fmt.Errorf("resolve compiler in %s: %w", source, err)
		}
		for _, root := range roots {
			images[root] = append(images[root], source)
		}
	}
	return images, configs, nil
}

// ProwCompilerRoots resolves only known image-stream, repository and CAPZ root shapes.
func prowCompilerRoots(content []byte, localRoot, target string) ([]compilerImage, error) {
	var config prowCompilerConfig
	if err := yaml.Unmarshal(content, &config); err != nil {
		return nil, fmt.Errorf("decode Prow config: %w", err)
	}
	var image, toolchain string
	_, projectRoot := config.BuildRoot["project_image"]
	switch {
	case config.BuildRoot == nil:
		if config.Binary != "" || config.TestBuild != "" || len(config.Images) != 0 {
			return nil, blockedf("compiler work appeared in a rootless Prow config")
		}
		return nil, nil
	case config.BuildRoot["from_repository"] == true:
		if len(config.BuildRoot) != 1 {
			return nil, blockedf("expected a repository build_root")
		}
		image = localRoot
	case projectRoot:
		project, _ := config.BuildRoot["project_image"].(map[string]any)
		literal, _ := project["dockerfile_literal"].(string)
		from, body, _ := strings.Cut(literal, "\n")
		base := capzBasePattern.FindStringSubmatch(from)
		if len(config.BuildRoot) != 1 || len(project) != 1 || base == nil ||
			fmt.Sprintf("%x", sha256.Sum256([]byte(body))) != capzRecipeBodySHA256 {
			return nil, blockedf("CAPZ build_root compiler recipe changed; review compiler provenance")
		}
		image = base[1]
	default:
		stream, _ := config.BuildRoot["image_stream_tag"].(map[string]any)
		parts := make(map[string]string, len(stream))
		for key, value := range stream {
			parts[key], _ = value.(string)
		}
		resolved, err := ciImageStream(parts)
		if err != nil {
			return nil, fmt.Errorf("resolve Prow build_root: %w", err)
		}
		if len(config.BuildRoot) != 1 || (resolved != promotedRoot && resolved != promotedPodman) {
			return nil, blockedf("unsupported Prow build_root %s", resolved)
		}
		image = resolved
		if image == promotedPodman {
			// The proposal changes the explicit pin, not the image's bootstrap Go.
			toolchain = "go" + target
		}
	}
	amd64, arm64 := len(config.Tests) == 0, false
	for _, test := range config.Tests {
		for _, capability := range test.Capabilities {
			switch capability {
			case "arm64", "gcp", "nested-podman":
			default:
				return nil, blockedf("unsupported Prow capability %q; review compiler platform", capability)
			}
		}
		if slices.Contains(test.Capabilities, "arm64") {
			arm64 = true
			continue
		}
		amd64 = true
	}
	var roots []compilerImage
	if amd64 {
		roots = append(roots, compilerImage{image, compilerPlatform, toolchain})
	}
	if arm64 {
		if projectRoot {
			return nil, blockedf("CAPZ build_root recipe only supports amd64")
		}
		roots = append(roots, compilerImage{image, armCompilerPlatform, toolchain})
	}
	if alias, ok := config.BaseImages["nested-podman"]; ok && image == promotedRoot {
		image, err := ciImageStream(alias)
		if err != nil {
			return nil, fmt.Errorf("resolve nested Podman alias: %w", err)
		}
		if image != promotedPodman {
			return nil, blockedf("unsupported nested Podman compiler image %s", image)
		}
		roots = append(roots, compilerImage{image, armCompilerPlatform, "go" + target})
	}
	return roots, nil
}

// ProbeBuilders records immutable digest evidence and requires the exact patch on each platform.
func probeBuilders(ctx context.Context, target string, images map[compilerImage][]string, command compilerCommand) ([]builderEvidence, error) {
	if len(images) == 0 {
		return nil, blockedf("no Prow compiler build roots found")
	}
	var reasons []string
	keys := slices.SortedFunc(maps.Keys(images), func(a, b compilerImage) int {
		if order := strings.Compare(a.reference, b.reference); order != 0 {
			return order
		}
		return strings.Compare(a.platform, b.platform)
	})
	builders := make([]builderEvidence, 0, len(keys))
	for _, image := range keys {
		builder, err := probePublishedBuilder(ctx, image, command)
		builder.Sources = images[image]
		builders = append(builders, builder)
		switch {
		case errors.Is(err, ErrBlocked):
			reasons = append(reasons, err.Error())
		case err != nil:
			return builders, fmt.Errorf("probe builder %s for %s: %w", image.reference, image.platform, err)
		case builder.Version != target:
			reasons = append(reasons, fmt.Sprintf("builder %s for %s has Go %s, needs %s", image.reference, image.platform, builder.Version, target))
		}
	}
	if len(reasons) != 0 {
		return builders, blockedf("%s", strings.Join(reasons, "; "))
	}
	return builders, nil
}

// ProbePublishedBuilder pulls the requested platform and executes only its repository digest.
func probePublishedBuilder(ctx context.Context, root compilerImage, command compilerCommand) (builderEvidence, error) {
	image, platform := root.reference, root.platform
	result := builderEvidence{Image: image, Platform: platform, Toolchain: root.toolchain}
	output, err := runProbeCommand(ctx, command, compilerPullLimit, "docker", "pull", "--platform", platform, image)
	if err != nil {
		return result, fmt.Errorf("pull builder %s: %w", image, err)
	}
	var digest string
	for line := range strings.SplitSeq(output, "\n") {
		candidate, found := strings.CutPrefix(line, "Digest: ")
		if !found {
			continue
		}
		if digest != "" || !imageIDPattern.MatchString(candidate) {
			return result, blockedf("builder %s produced invalid or ambiguous repository digest output", image)
		}
		digest = candidate
	}
	repository, _, tagged := strings.Cut(image, ":")
	if digest == "" || !tagged {
		return result, blockedf("builder %s has no immutable repository digest", image)
	}
	result.Digest = repository + "@" + digest
	actual, err := executeCompiler(ctx, result.Digest, root, command)
	if err != nil {
		return result, fmt.Errorf("execute compiler in %s: %w", image, err)
	}
	result.Version = actual
	return result, nil
}

// ExecuteCompiler runs the pinned image without host data and uses a scratch cache for an explicit toolchain fetch.
func executeCompiler(ctx context.Context, immutable string, root compilerImage, command compilerCommand) (string, error) {
	args := []string{"run", "--rm", "--pull=never", "--platform", root.platform,
		"--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--workdir=/",
		"--pids-limit=32", "--cpus=1", "--user=65534:65534", "--env", "GOENV=off", "--env", "GOWORK=off"}
	limit := compilerCommandLimit
	switch root.toolchain {
	case "":
		args = append(args, "--memory=256m", "--network=none", "--env", "GOTOOLCHAIN=local")
	default:
		limit = compilerPullLimit
		args = append(args, "--memory=768m", "--network=bridge", "--tmpfs", "/tmp:rw,nosuid,nodev,size=512m",
			"--env", "HOME=/tmp", "--env", "GOPATH=/tmp/go", "--env", "GOMODCACHE=/tmp/go/pkg/mod",
			"--env", "GOCACHE=/tmp/cache", "--env", "GOTOOLCHAIN="+root.toolchain,
			"--env", "GOPROXY=https://proxy.golang.org", "--env", "GOSUMDB=sum.golang.org",
			"--env", "GOPRIVATE=", "--env", "GONOPROXY=", "--env", "GONOSUMDB=", "--env", "GOINSECURE=",
			"--env", "GOAUTH=off", "--env", "HTTP_PROXY=", "--env", "HTTPS_PROXY=", "--env", "ALL_PROXY=",
			"--env", "http_proxy=", "--env", "https_proxy=", "--env", "all_proxy=")
	}
	args = append(args, "--entrypoint", "go", immutable, "version")
	output, err := runProbeCommand(ctx, command, limit, "docker", args...)
	if err != nil {
		return "", fmt.Errorf("run Go version from %s: %w", immutable, err)
	}
	match := goCompilerOutputPattern.FindStringSubmatch(output)
	if match == nil || "linux/"+match[2] != root.platform || !patchVersion.MatchString("go"+match[1]) {
		return "", blockedf("builder %s returned unsupported compiler: %q", immutable, output)
	}
	return match[1], nil
}

// RunProbeCommand treats missing artifacts and execution deadlines as waits, not authorization or configuration errors.
func runProbeCommand(ctx context.Context, command compilerCommand, limit time.Duration, program string, args ...string) (string, error) {
	output, err := command(ctx, limit, program, args...)
	if err == nil {
		return output, nil
	}
	if ctx.Err() != nil {
		return "", fmt.Errorf("probe canceled: %w", ctx.Err())
	}
	if errors.Is(err, exec.ErrNotFound) {
		return "", blockedf("required probe executable %s is not installed: %v", program, err)
	}
	if probeAuthorization.MatchString(err.Error()) {
		return "", fmt.Errorf("authentication/authorization failure: %w", err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "", blockedf("probe exceeded its execution deadline: %v", err)
	}
	if probeUnavailable.MatchString(err.Error()) {
		return "", blockedf("%v", err)
	}
	return "", fmt.Errorf("execute probe command: %w", err)
}

// RunCompilerCommand keeps stderr separate from structured stdout and bounds child process execution.
func runCompilerCommand(ctx context.Context, limit time.Duration, program string, args ...string) (string, error) {
	commandCtx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	command := exec.CommandContext(commandCtx, program, args...)
	command.WaitDelay = 5 * time.Second
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		if commandCtx.Err() != nil {
			return "", fmt.Errorf("run %s %s: %w", program, args[0], commandCtx.Err())
		}
		detail := strings.TrimSpace(stdout.String() + "\n" + stderr.String())
		if detail == "" {
			return "", fmt.Errorf("run %s %s: %w", program, args[0], err)
		}
		return "", fmt.Errorf("run %s %s: %s: %w", program, args[0], detail, err)
	}
	return strings.TrimSpace(stdout.String()), nil
}
