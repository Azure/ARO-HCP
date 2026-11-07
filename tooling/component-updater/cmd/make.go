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

package cmd

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

var gnuMakeVersion = regexp.MustCompile(`^GNU Make (\d+)\.`)

// findGNUMake returns a GNU Make 4+ executable, preferring gmake: on macOS the
// system make is GNU Make 3.81 while Homebrew installs GNU Make 4 as gmake.
func findGNUMake(ctx context.Context, lookPath func(string) (string, error), version func(context.Context, string) (string, error)) (string, error) {
	var rejected []string
	for _, name := range []string{"gmake", "make"} {
		path, err := lookPath(name)
		if err != nil {
			continue
		}
		out, err := version(ctx, path)
		if err != nil {
			rejected = append(rejected, fmt.Sprintf("%s (%v)", path, err))
			continue
		}
		firstLine, _, _ := strings.Cut(out, "\n")
		if match := gnuMakeVersion.FindStringSubmatch(firstLine); match != nil {
			if major, _ := strconv.Atoi(match[1]); major >= 4 {
				return path, nil
			}
		}
		rejected = append(rejected, fmt.Sprintf("%s (%s)", path, strings.TrimSpace(firstLine)))
	}
	if len(rejected) == 0 {
		return "", fmt.Errorf("GNU Make 4+ is required for materialize: neither gmake nor make found on PATH")
	}
	return "", fmt.Errorf("GNU Make 4+ is required for materialize; found %s", strings.Join(rejected, ", "))
}

func makeVersion(ctx context.Context, path string) (string, error) {
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	return string(out), err
}
