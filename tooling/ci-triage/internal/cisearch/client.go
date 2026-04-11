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

// Package cisearch provides a client for search.dptools.openshift.org, which
// indexes JUnit failure strings and build logs across all OpenShift CI jobs.
// This allows answering "is this failure ARO-specific or platform-wide?"
package cisearch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const (
	// DefaultEndpoint is the search.dptools base URL.
	DefaultEndpoint = "https://search.dptools.openshift.org"
	// AROJobFilter matches only ARO-HCP jobs.
	AROJobFilter = "Azure-ARO-HCP"
)

// Client queries search.dptools.openshift.org for failure patterns across CI.
type Client struct {
	endpoint   string
	httpClient *http.Client
}

// NewClient creates a new CI search client.
func NewClient() *Client {
	return &Client{
		endpoint:   DefaultEndpoint,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// SearchResult represents a single match from the search API.
type SearchResult struct {
	JobName  string   `json:"name"`
	Filename string   `json:"filename"`
	Context  []string `json:"context"`
	URL      string   `json:"url"`
}

// SearchResponse is the v2 search API response structure.
// The top-level keys are search terms, each mapping to a matches array.
type SearchResponse struct {
	Results map[string]struct {
		Matches []SearchResult `json:"matches"`
	} `json:"results"`
}

// Search queries for a regex pattern across CI jobs.
// jobNameFilter limits results to matching job names (regex).
// maxAge is a Go duration string (e.g., "48h", "168h").
// resultType is "junit", "build-log", or "all".
func (c *Client) Search(ctx context.Context, query, jobNameFilter, maxAge, resultType string) ([]SearchResult, error) {
	reqURL, err := url.Parse(c.endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse endpoint: %w", err)
	}
	reqURL.Path = "/v2/search"

	q := reqURL.Query()
	q.Set("search", query)
	if jobNameFilter != "" {
		q.Set("name", jobNameFilter)
	}
	if maxAge != "" {
		q.Set("maxAge", maxAge)
	}
	if resultType != "" {
		q.Set("type", resultType)
	}
	q.Set("context", "2")
	q.Set("maxMatches", "5")
	reqURL.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("search request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("search returned %d: %s", resp.StatusCode, string(body))
	}

	var searchResp SearchResponse
	if err := json.Unmarshal(body, &searchResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	// Flatten results from all search terms
	var results []SearchResult
	for _, termResult := range searchResp.Results {
		results = append(results, termResult.Matches...)
	}

	return results, nil
}

// ScopeResult holds the comparison between ARO-only and all-CI matches.
type ScopeResult struct {
	AROSpecific bool   `json:"aro_specific"`
	AROMatches  int    `json:"aro_matches"`
	AllMatches  int    `json:"all_matches"`
	Assessment  string `json:"assessment"`
}

// IsAROSpecific searches for a failure message in ARO jobs and across all of CI,
// then compares. Returns whether the failure appears to be ARO-specific.
func (c *Client) IsAROSpecific(ctx context.Context, failureMsg string) (*ScopeResult, error) {
	// Search ARO jobs only
	aroResults, err := c.Search(ctx, failureMsg, AROJobFilter, "48h", "junit")
	if err != nil {
		return nil, fmt.Errorf("ARO search: %w", err)
	}

	// Search all of CI
	allResults, err := c.Search(ctx, failureMsg, "", "48h", "junit")
	if err != nil {
		return nil, fmt.Errorf("all-CI search: %w", err)
	}

	aroCount := len(aroResults)
	allCount := len(allResults)
	aroSpecific := allCount <= aroCount || allCount == 0

	assessment := "platform-wide — this error occurs in non-ARO jobs too"
	if aroSpecific {
		assessment = "ARO-specific — this error only appears in ARO-HCP jobs"
	} else if allCount > 0 && aroCount > 0 && float64(aroCount)/float64(allCount) > 0.5 {
		assessment = "mostly ARO — majority of occurrences are in ARO-HCP jobs"
	}

	return &ScopeResult{
		AROSpecific: aroSpecific,
		AROMatches:  aroCount,
		AllMatches:  allCount,
		Assessment:  assessment,
	}, nil
}
