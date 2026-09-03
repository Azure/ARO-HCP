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

type AKSProvider struct {
	subscriptionID string
	httpClient     *http.Client
	apiVersion     string
}

func NewAKSProvider(subscriptionID string, httpClient *http.Client) *AKSProvider {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &AKSProvider{
		subscriptionID: subscriptionID,
		httpClient:     httpClient,
		apiVersion:     "2023-11-01",
	}
}

type orchestratorsResponse struct {
	Properties struct {
		Orchestrators []orchestratorEntry `json:"orchestrators"`
	} `json:"properties"`
}

type orchestratorEntry struct {
	OrchestratorType    string `json:"orchestratorType"`
	OrchestratorVersion string `json:"orchestratorVersion"`
}

func (p *AKSProvider) AvailableVersions(ctx context.Context, locations []string) ([]string, error) {
	var commonVersions sets.String

	for _, location := range locations {
		versions, err := p.fetchVersions(ctx, location)
		if err != nil {
			return nil, fmt.Errorf("fetching AKS versions for %s: %w", location, err)
		}

		locationSet := sets.NewString(versions...)

		if commonVersions == nil {
			commonVersions = locationSet
		} else {
			commonVersions = commonVersions.Intersection(locationSet)
		}
	}

	result := commonVersions.UnsortedList()
	sortMinorVersions(result)
	return result, nil
}

func (p *AKSProvider) fetchVersions(ctx context.Context, location string) ([]string, error) {
	url := fmt.Sprintf(
		"https://management.azure.com/subscriptions/%s/providers/Microsoft.ContainerService/locations/%s/orchestrators?api-version=%s&resource-type=managedClusters",
		p.subscriptionID, location, p.apiVersion,
	)

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
		return nil, fmt.Errorf("AKS orchestrators API returned %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var response orchestratorsResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("parsing AKS orchestrators response: %w", err)
	}

	minorSet := sets.NewString()
	for _, o := range response.Properties.Orchestrators {
		if o.OrchestratorType != "Kubernetes" {
			continue
		}
		minor := extractMinor(o.OrchestratorVersion)
		if minor != "" {
			minorSet.Insert(minor)
		}
	}

	return minorSet.UnsortedList(), nil
}

// extractMinor extracts the major.minor portion from a semver string (e.g. "1.35.2" -> "1.35").
func extractMinor(version string) string {
	parts := strings.SplitN(version, ".", 3)
	if len(parts) < 2 {
		return ""
	}
	return parts[0] + "." + parts[1]
}

func (p *AKSProvider) NextVersion(current string, available []string) string {
	parts := strings.SplitN(current, ".", 2)
	if len(parts) != 2 {
		return ""
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return ""
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return ""
	}

	next := fmt.Sprintf("%d.%d", major, minor+1)
	if slices.Contains(available, next) {
		return next
	}
	return ""
}

func sortMinorVersions(versions []string) {
	sort.Slice(versions, func(i, j int) bool {
		return compareMinorVersion(versions[i], versions[j]) < 0
	})
}

func compareMinorVersion(a, b string) int {
	aParts := strings.SplitN(a, ".", 2)
	bParts := strings.SplitN(b, ".", 2)
	if len(aParts) != 2 || len(bParts) != 2 {
		return strings.Compare(a, b)
	}
	aMajor, _ := strconv.Atoi(aParts[0])
	bMajor, _ := strconv.Atoi(bParts[0])
	if aMajor != bMajor {
		return aMajor - bMajor
	}
	aMinor, _ := strconv.Atoi(aParts[1])
	bMinor, _ := strconv.Atoi(bParts[1])
	return aMinor - bMinor
}
