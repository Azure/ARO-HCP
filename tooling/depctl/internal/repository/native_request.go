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

package repository

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var nativeRequestFilterKey = regexp.MustCompile(`^filter\..+\.(clean|process|smudge)$`)

// NativeRequestPatch reads the complete tracked candidate without changing the index.
func (r *Repo) NativeRequestPatch(ctx context.Context, base string) ([]byte, error) {
	if !IsCommit(base) {
		return nil, fmt.Errorf("native request base must be a full lowercase commit SHA")
	}
	settings := []string{
		"-c", "core.fsmonitor=false",
		"-c", "core.quotePath=false",
		"-c", "diff.orderFile=/dev/null",
		"-c", "diff.suppressBlankEmpty=false",
	}
	// Isolate user configuration while retaining local filter names to disable.
	environment := append(sanitizedEnvironment(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "GIT_NO_REPLACE_OBJECTS=1", "GIT_DIFF_OPTS=", "LC_ALL=C")
	config := r.command(ctx, append(settings, "config", "--null", "--list", "--name-only")...)
	config.Env = environment
	keys, err := config.Output()
	if err != nil {
		return nil, fmt.Errorf("read native request Git configuration: %w", err)
	}
	filters := map[string]bool{}
	for _, key := range strings.Split(string(keys), "\x00") {
		if nativeRequestFilterKey.MatchString(key) {
			filters[key] = true
		}
	}
	ordered := make([]string, 0, len(filters))
	for key := range filters {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	for _, key := range ordered {
		settings = append(settings, "-c", key+"=")
	}
	verify := r.command(ctx, append(settings, "rev-parse", "--verify", base+"^{commit}")...)
	verify.Env = environment
	resolved, err := verify.Output()
	if err != nil || strings.TrimSpace(string(resolved)) != base {
		return nil, fmt.Errorf("inventory base commit is unavailable")
	}
	arguments := append(settings, "diff", "--no-ext-diff", "--no-textconv", "--no-renames", "--no-color", "--no-relative", "--full-index", "--binary", "--diff-algorithm=myers", "--no-indent-heuristic", "--unified=3", "--inter-hunk-context=0", "--src-prefix=a/", "--dst-prefix=b/", "--output-indicator-new=+", "--output-indicator-old=-", "--output-indicator-context= ", base, "--", ".")
	command := r.command(ctx, arguments...)
	command.Env = environment
	var stderr bytes.Buffer
	command.Stderr = &stderr
	patch, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("native request diff: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return patch, nil
}
