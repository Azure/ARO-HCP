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
	"sort"
	"strings"
)

// NativeGit runs compiled local Git arguments without executable configuration.
// Index selects only a caller-created private index; an empty index uses Git's
// normal index. Input is passed verbatim to plumbing commands, never a shell.
func (r *Repo) NativeGit(ctx context.Context, input []byte, index string, args ...string) ([]byte, error) {
	settings := []string{
		"-c", "core.fsmonitor=false",
		"-c", "core.quotePath=false",
		"-c", "credential.helper=",
		"-c", "commit.gpgSign=false",
		"-c", "tag.gpgSign=false",
		"-c", "diff.orderFile=/dev/null",
		"-c", "diff.suppressBlankEmpty=false",
	}
	environment := nativeGitEnvironment(index)
	config := r.command(ctx, append(settings, "config", "--null", "--list", "--name-only")...)
	config.Env = environment
	keys, err := config.Output()
	if err != nil {
		return nil, fmt.Errorf("read native Git configuration: %w", err)
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
	command := r.command(ctx, append(settings, args...)...)
	command.Env = environment
	if input != nil {
		command.Stdin = bytes.NewReader(input)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("native git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return output, nil
}

// NativeGitEnvironment removes inherited redirection and executable controls.
func nativeGitEnvironment(index string) []string {
	environment := sanitizedEnvironment()
	filtered := environment[:0]
	for _, item := range environment {
		name, _, _ := strings.Cut(item, "=")
		if strings.HasPrefix(name, "GIT_CONFIG_") {
			continue
		}
		switch name {
		case "GIT_CONFIG", "GIT_TERMINAL_PROMPT", "GIT_NO_REPLACE_OBJECTS", "GIT_DIFF_OPTS", "GIT_EXTERNAL_DIFF", "LC_ALL":
			continue
		case "GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL":
			continue
		}
		filtered = append(filtered, item)
	}
	environment = append(filtered, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "GIT_NO_REPLACE_OBJECTS=1", "GIT_DIFF_OPTS=", "LC_ALL=C")
	environment = append(environment,
		"GIT_AUTHOR_NAME=aro-hcp-robot[bot]",
		"GIT_AUTHOR_EMAIL=aro-hcp-robot[bot]@users.noreply.github.com",
		"GIT_COMMITTER_NAME=aro-hcp-robot[bot]",
		"GIT_COMMITTER_EMAIL=aro-hcp-robot[bot]@users.noreply.github.com",
	)
	if index != "" {
		environment = append(environment, "GIT_INDEX_FILE="+index)
	}
	return environment
}
