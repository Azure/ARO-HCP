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

package discovery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// CLiResolver resolves upstream versions with the tools the repository already
// uses: gh for GitHub releases and go list for module versions.
//
// It is constructed only by an online command; offline discovery never reaches
// this type.
type CLiResolver struct {
	// Directory is the Go module directory used for module lookups.
	Directory string
}

// NewCLIResolver returns a resolver rooted at the checkout.
func NewCLIResolver(root string) *CLiResolver {
	return &CLiResolver{Directory: filepath.Join(root, ".bingo")}
}

// LatestRelease returns the newest stable GitHub release tag.
func (r *CLiResolver) LatestRelease(ctx context.Context, repository string) (string, error) {
	runContext, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var stdout, stderr bytes.Buffer
	command := exec.CommandContext(runContext, "gh", "api", "repos/"+repository+"/releases/latest")
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("gh api releases for %s: %w: %s", repository, err, strings.TrimSpace(stderr.String()))
	}
	var release struct {
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
		TagName    string `json:"tag_name"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &release); err != nil {
		return "", fmt.Errorf("decode release for %s: %w", repository, err)
	}
	if release.Draft || release.Prerelease || release.TagName == "" {
		return "", fmt.Errorf("latest release for %s is missing or not stable", repository)
	}
	return strings.TrimPrefix(release.TagName, "v"), nil
}

// LatestModule returns the newest module version known to the Go toolchain.
func (r *CLiResolver) LatestModule(ctx context.Context, modulePath string) (string, error) {
	runContext, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	var stdout, stderr bytes.Buffer
	command := exec.CommandContext(runContext, "go", "list", "-m", "-json", modulePath+"@latest")
	command.Dir = r.Directory
	command.Env = append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local")
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("go list %s@latest: %w: %s", modulePath, err, strings.TrimSpace(stderr.String()))
	}
	var module struct {
		Path    string `json:"Path"`
		Version string `json:"Version"`
		Error   string `json:"Error"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &module); err != nil {
		return "", fmt.Errorf("decode module %s: %w", modulePath, err)
	}
	if module.Error != "" {
		return "", fmt.Errorf("module lookup for %s failed: %s", modulePath, module.Error)
	}
	if module.Path != modulePath || module.Version == "" {
		return "", fmt.Errorf("module lookup for %s returned an unexpected result", modulePath)
	}
	return module.Version, nil
}
