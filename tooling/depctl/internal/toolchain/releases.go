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
	"encoding/json"
	"fmt"
	"go/version"
	"net/http"
	"regexp"
	"strings"
)

const officialGoReleases = "https://go.dev/dl/?mode=json&include=all"

var patchVersion = regexp.MustCompile(`^go[1-9][0-9]*\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

type goRelease struct {
	Version string `json:"version"`
	Stable  bool   `json:"stable"`
}

// LatestPatch selects the highest official stable patch in the current Go release line.
func latestPatch(current string, releases []goRelease) (string, error) {
	current = "go" + strings.TrimPrefix(current, "go")
	if !patchVersion.MatchString(current) {
		return "", fmt.Errorf("invalid current Go patch %q", current)
	}
	line := version.Lang(current)
	var latest string
	for _, release := range releases {
		if !release.Stable || !patchVersion.MatchString(release.Version) || version.Lang(release.Version) != line {
			continue
		}
		if latest == "" || version.Compare(release.Version, latest) > 0 {
			latest = release.Version
		}
	}
	if latest == "" || version.Compare(latest, current) < 0 {
		return "", fmt.Errorf("official release index has no stable %s patch at or above %s", line, current)
	}
	return strings.TrimPrefix(latest, "go"), nil
}

// FetchLatestPatch reads the official Go release index before selecting a target.
func fetchLatestPatch(ctx context.Context, client *http.Client, current string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, officialGoReleases, nil)
	if err != nil {
		return "", fmt.Errorf("build Go release index request: %w", err)
	}
	response, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("read official Go releases: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("read official Go releases: HTTP %d", response.StatusCode)
	}
	var releases []goRelease
	if err := json.NewDecoder(response.Body).Decode(&releases); err != nil {
		return "", fmt.Errorf("decode official Go releases: %w", err)
	}
	return latestPatch(current, releases)
}
