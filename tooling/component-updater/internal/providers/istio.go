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
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"
)

var asmRevisionPattern = regexp.MustCompile(`^asm-(\d+)-(\d+)$`)

type IstioProvider struct {
	subscriptionID string
	httpClient     *http.Client
	apiVersion     string
}

func NewIstioProvider(subscriptionID string, httpClient *http.Client) *IstioProvider {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &IstioProvider{
		subscriptionID: subscriptionID,
		httpClient:     httpClient,
		apiVersion:     "2024-09-01",
	}
}

type meshRevisionProfilesResponse struct {
	Value []meshRevisionProfileEntry `json:"value"`
}

type meshRevisionProfileEntry struct {
	Name       string `json:"name"`
	Properties struct {
		MeshRevisions []meshRevision `json:"meshRevisions"`
	} `json:"properties"`
}

type meshRevision struct {
	Revision        string `json:"revision"`
	Compatibilities []struct {
		Revisions []string `json:"revisions"`
	} `json:"compatibilities"`
}

func (p *IstioProvider) AvailableVersions(ctx context.Context, locations []string) ([]string, error) {
	var commonVersions sets.String

	for _, location := range locations {
		versions, err := p.fetchVersions(ctx, location)
		if err != nil {
			return nil, fmt.Errorf("fetching Istio mesh revisions for %s: %w", location, err)
		}

		locationSet := sets.NewString(versions...)

		if commonVersions == nil {
			commonVersions = locationSet
		} else {
			commonVersions = commonVersions.Intersection(locationSet)
		}
	}

	result := commonVersions.UnsortedList()
	sortASMRevisions(result)
	return result, nil
}

func (p *IstioProvider) fetchVersions(ctx context.Context, location string) ([]string, error) {
	url := fmt.Sprintf(
		"https://management.azure.com/subscriptions/%s/providers/Microsoft.ContainerService/locations/%s/meshRevisionProfiles?api-version=%s",
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
		return nil, fmt.Errorf("mesh revision profiles API returned %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var response meshRevisionProfilesResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("parsing mesh revision profiles response: %w", err)
	}

	revisionSet := sets.NewString()
	for _, profile := range response.Value {
		for _, rev := range profile.Properties.MeshRevisions {
			if asmRevisionPattern.MatchString(rev.Revision) {
				revisionSet.Insert(rev.Revision)
			}
		}
	}

	return revisionSet.UnsortedList(), nil
}

func (p *IstioProvider) NextVersion(current string, available []string) string {
	curMajor, curMinor, ok := parseASMRevision(current)
	if !ok {
		return ""
	}

	next := fmt.Sprintf("asm-%d-%d", curMajor, curMinor+1)
	if slices.Contains(available, next) {
		return next
	}
	return ""
}

func parseASMRevision(revision string) (major, minor int, ok bool) {
	m := asmRevisionPattern.FindStringSubmatch(revision)
	if m == nil {
		return 0, 0, false
	}
	major, _ = strconv.Atoi(m[1])
	minor, _ = strconv.Atoi(m[2])
	return major, minor, true
}

func sortASMRevisions(revisions []string) {
	sort.Slice(revisions, func(i, j int) bool {
		return compareASMRevision(revisions[i], revisions[j]) < 0
	})
}

func compareASMRevision(a, b string) int {
	aMaj, aMin, aOK := parseASMRevision(a)
	bMaj, bMin, bOK := parseASMRevision(b)
	if !aOK || !bOK {
		return strings.Compare(a, b)
	}
	if aMaj != bMaj {
		return aMaj - bMaj
	}
	return aMin - bMin
}
