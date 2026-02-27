// Copyright 2025 Microsoft Corporation
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

package clients

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

var githubAPIBaseURL = "https://api.github.com"

func setGitHubAPIBase(url string) { githubAPIBaseURL = url }

type githubLatestReleaseResponse struct {
	TagName string `json:"tag_name"`
}

// GetLatestReleaseTag fetches the latest release tag from a GitHub repository.
// It uses the GITHUB_TOKEN environment variable for authentication if available,
// which increases the API rate limit from 60 to 5,000 requests per hour.
func GetLatestReleaseTag(ctx context.Context, ownerRepo string) (string, error) {
	url := githubAPIBaseURL + "/repos/" + ownerRepo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("github latest release: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "ARO-HCP-image-updater")
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("github latest release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github latest release: %s returned %d", url, resp.StatusCode)
	}
	var r githubLatestReleaseResponse
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", fmt.Errorf("github latest release: decode: %w", err)
	}
	if r.TagName == "" {
		return "", fmt.Errorf("github latest release: empty tag_name for %s", ownerRepo)
	}
	version := strings.TrimPrefix(r.TagName, "v")
	return version, nil
}
