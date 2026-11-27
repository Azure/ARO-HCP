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
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	devcontainerPath = ".devcontainer/devcontainer.json"
	generatorRecipe  = "api/Makefile"
	nestedPodmanPath = "dev-infrastructure/openshift-ci/Dockerfile.nested-podman"
)

var compilerLine = regexp.MustCompile(`^[ \t]*(go|toolchain)(?:[ \t]|$)`)

// ValidCheckoutPath rejects paths that are absolute, empty, or contain traversal.
func validCheckoutPath(name string) bool {
	return name != "." && utf8.ValidString(name) && path.Clean(name) == name && filepath.IsLocal(filepath.FromSlash(name))
}

// ReadRegularFile refuses non-regular files and paths outside the checkout.
func readRegularFile(checkout *os.Root, name string) (string, error) {
	if !validCheckoutPath(name) {
		return "", fmt.Errorf("unsafe tracked path %q", name)
	}
	info, err := checkout.Lstat(name)
	if err != nil {
		return "", fmt.Errorf("not a regular checkout file %q: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("not a regular checkout file %q", name)
	}
	content, err := checkout.ReadFile(name)
	if err != nil {
		return "", fmt.Errorf("read checkout file %q: %w", name, err)
	}
	if !utf8.Valid(content) {
		return "", fmt.Errorf("checkout file %q is not valid UTF-8", name)
	}
	return string(content), nil
}

// StableDirectiveVersion normalizes a bare Go language minimum to a patch version.
func stableDirectiveVersion(value string) string {
	if strings.Count(value, ".") == 1 {
		return value + ".0"
	}
	return value
}
