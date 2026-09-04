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

package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"
)

type GrafanaProvider struct {
	subscriptionID string
	httpClient     *http.Client
}

func NewGrafanaProvider(subscriptionID string, httpClient *http.Client) *GrafanaProvider {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &GrafanaProvider{
		subscriptionID: subscriptionID,
		httpClient:     httpClient,
	}
}

// AvailableVersions discovers available Grafana major versions by querying the
// Grafana.com release API and extracting distinct major version numbers.
// Azure Managed Grafana tracks upstream Grafana releases; the locations parameter
// is accepted for interface compatibility but not used because Grafana version
// availability is not region-scoped in Azure.
func (p *GrafanaProvider) AvailableVersions(ctx context.Context, _ []string) ([]string, error) {
	return p.fetchGrafanaReleases(ctx)
}

type grafanaReleasesResponse struct {
	Items []grafanaReleaseItem `json:"items"`
}

type grafanaReleaseItem struct {
	Version string `json:"version"`
	Stable  bool   `json:"stable"`
}

// fetchGrafanaReleases queries the Grafana.com API for stable releases and
// returns the distinct major version numbers sorted ascending.
func (p *GrafanaProvider) fetchGrafanaReleases(ctx context.Context) ([]string, error) {
	url := "https://grafana.com/api/grafana/versions?orderBy=version&direction=desc"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("grafana.com API returned %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var response grafanaReleasesResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("parsing grafana releases response: %w", err)
	}

	majorSet := sets.NewString()
	for _, item := range response.Items {
		if !item.Stable {
			continue
		}
		major := extractMajor(item.Version)
		if major != "" {
			majorSet.Insert(major)
		}
	}

	result := majorSet.UnsortedList()
	sort.Slice(result, func(i, j int) bool {
		vi, _ := strconv.Atoi(result[i])
		vj, _ := strconv.Atoi(result[j])
		return vi < vj
	})
	return result, nil
}

func extractMajor(version string) string {
	parts := strings.SplitN(version, ".", 2)
	if len(parts) == 0 {
		return ""
	}
	if _, err := strconv.Atoi(parts[0]); err != nil {
		return ""
	}
	return parts[0]
}

func (p *GrafanaProvider) NextVersion(current string, available []string) string {
	currentNum, err := strconv.Atoi(current)
	if err != nil {
		return ""
	}

	next := strconv.Itoa(currentNum + 1)
	if slices.Contains(available, next) {
		return next
	}
	return ""
}
