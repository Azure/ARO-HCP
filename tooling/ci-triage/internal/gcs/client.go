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

package gcs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/cache"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/config"
)

var buildIDRE = regexp.MustCompile(`^\d{19}$`)

// Client is an HTTP client for the GCS storage API used by Prow.
type Client struct {
	http  *http.Client
	cache *cache.FileCache
	mu    sync.Mutex
	errs  map[string]int
}

// NewClient creates a new GCS client with the given HTTP client.
// Artifacts are cached locally — GCS objects for finished jobs are immutable.
func NewClient(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{
		http:  httpClient,
		cache: cache.New(cache.Dir()),
		errs:  make(map[string]int),
	}
}

// Errors returns a copy of the current error counts.
func (c *Client) Errors() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return maps.Clone(c.errs)
}

func (c *Client) recordError(kind string) {
	c.mu.Lock()
	c.errs[kind]++
	c.mu.Unlock()
}

// isCacheable returns true for GCS artifact URLs (immutable objects).
// API listing URLs and dynamic endpoints are NOT cached.
func isCacheable(u string) bool {
	return strings.HasPrefix(u, config.GCSDirect) || strings.HasPrefix(u, config.GCSWebBase)
}

// fetchBytes fetches raw bytes from a URL, using the local cache for
// immutable GCS artifacts to avoid redundant network calls.
func (c *Client) fetchBytes(ctx context.Context, u string, timeout time.Duration) ([]byte, error) {
	// Check cache first for GCS artifact URLs
	if isCacheable(u) {
		if data := c.cache.Get(u); data != nil {
			return data, nil
		}
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			c.recordError("timeout")
		} else {
			c.recordError("network")
		}
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		c.recordError("not_found")
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		c.recordError("http")
		return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, u)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// Cache the result for immutable GCS objects
	if isCacheable(u) && len(data) > 0 {
		c.cache.Set(u, data)
	}

	return data, nil
}

// fetchJSON fetches and parses JSON from a URL.
func (c *Client) fetchJSON(ctx context.Context, u string, timeout time.Duration, out any) error {
	data, err := c.fetchBytes(ctx, u, timeout)
	if err != nil {
		return err
	}
	if data == nil {
		return fmt.Errorf("not found: %s", u)
	}

	if err := json.Unmarshal(data, out); err != nil {
		c.recordError("parse")
		return fmt.Errorf("parsing JSON from %s: %w", u, err)
	}
	return nil
}

// fetchText fetches text from a URL, using cache for GCS artifacts.
func (c *Client) fetchText(ctx context.Context, u string, timeout time.Duration) (string, error) {
	data, err := c.fetchBytes(ctx, u, timeout)
	if err != nil {
		return "", err
	}
	if data == nil {
		return "", nil
	}
	// Reject HTML responses (gcsweb error pages)
	if len(data) > 0 && data[0] == '<' {
		return "", nil
	}
	return string(data), nil
}

// gcsListResponse represents the GCS JSON API list response.
type gcsListResponse struct {
	Prefixes []string       `json:"prefixes"`
	Items    []gcsListItem  `json:"items"`
}

type gcsListItem struct {
	Name     string            `json:"name"`
	Metadata map[string]string `json:"metadata"`
}

// ListPRBuilds lists build IDs for a specific PR's presubmit job.
func (c *Client) ListPRBuilds(ctx context.Context, prNumber int, jobName string) ([]string, error) {
	prefix := fmt.Sprintf("pr-logs/pull/%s/%d/%s/", config.GCSPROrgRepo, prNumber, jobName)
	params := url.Values{
		"prefix":     {prefix},
		"delimiter":  {"/"},
		"maxResults": {"100"},
		"fields":     {"prefixes"},
	}

	apiURL := config.GCSAPI + "?" + params.Encode()
	var resp gcsListResponse
	if err := c.fetchJSON(ctx, apiURL, 15*time.Second, &resp); err != nil {
		return nil, err
	}

	var buildIDs []string
	for _, p := range resp.Prefixes {
		bid := strings.TrimRight(p, "/")
		if idx := strings.LastIndex(bid, "/"); idx >= 0 {
			bid = bid[idx+1:]
		}
		if buildIDRE.MatchString(bid) {
			buildIDs = append(buildIDs, bid)
		}
	}
	return buildIDs, nil
}

// FinishedJSON represents the finished.json file from a Prow job.
type FinishedJSON struct {
	Result   string `json:"result"`
	Revision string `json:"revision"`
}

// FetchFinished fetches the finished.json for a job.
func (c *Client) FetchFinished(ctx context.Context, gcsURL string) (*FinishedJSON, error) {
	var result FinishedJSON
	if err := c.fetchJSON(ctx, gcsURL+"/finished.json", 5*time.Second, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// FetchJUnit fetches junit.xml from the primary location, falling back to step-level.
func (c *Client) FetchJUnit(ctx context.Context, baseURL string, step, container string) ([]byte, error) {
	primary := fmt.Sprintf("%s/artifacts/%s/%s/artifacts/junit.xml", baseURL, step, container)
	data, err := c.fetchBytes(ctx, primary, 20*time.Second)
	if err == nil && data != nil {
		return data, nil
	}

	// Fallback: step-level junit_operator.xml
	fallback := fmt.Sprintf("%s/artifacts/junit_operator.xml", baseURL)
	return c.fetchBytes(ctx, fallback, 20*time.Second)
}

// FetchBuildLog fetches build-log.txt from a job.
func (c *Client) FetchBuildLog(ctx context.Context, baseURL string, step, container string) (string, error) {
	logURL := fmt.Sprintf("%s/artifacts/%s/%s/build-log.txt", baseURL, step, container)
	return c.fetchText(ctx, logURL, 30*time.Second)
}

// ExtensionTestResult represents a single test from extension_test_result_e2e_*.json.
type ExtensionTestResult struct {
	Name      string `json:"name"`
	Lifecycle string `json:"lifecycle"`
	Duration  int    `json:"duration"`
	StartTime string `json:"startTime"`
	EndTime   string `json:"endTime"`
	Result    string `json:"result"` // "passed", "failed", "skipped"
	Output    string `json:"output"`
	Error     string `json:"error"`
}

// FetchExtensionResults fetches and parses the extension_test_result_e2e_*.json file.
// This is richer than JUnit: full error, full output, per-test timing.
func (c *Client) FetchExtensionResults(ctx context.Context, baseURL string, step, container string) ([]ExtensionTestResult, error) {
	// List files in the artifacts directory to find the extension result file
	artifactsPrefix := extractGCSPath(baseURL) + "/artifacts/" + step + "/" + container + "/artifacts/"
	params := url.Values{
		"prefix":     {artifactsPrefix},
		"delimiter":  {"/"},
		"maxResults": {"100"},
		"fields":     {"items(name,size)"},
	}
	apiURL := config.GCSAPI + "?" + params.Encode()

	var listResp gcsListResponse
	if err := c.fetchJSON(ctx, apiURL, 10*time.Second, &listResp); err != nil {
		return nil, fmt.Errorf("listing artifacts: %w", err)
	}

	// Find the extension_test_result_e2e_*.json file
	var resultFile string
	for _, item := range listResp.Items {
		name := item.Name
		if idx := strings.LastIndex(name, "/"); idx >= 0 {
			name = name[idx+1:]
		}
		if strings.HasPrefix(name, "extension_test_result_e2e_") && strings.HasSuffix(name, ".json") {
			resultFile = config.GCSDirect + "/" + item.Name
			break
		}
	}
	if resultFile == "" {
		return nil, nil // not found — not all jobs have this
	}

	var results []ExtensionTestResult
	if err := c.fetchJSON(ctx, resultFile, 20*time.Second, &results); err != nil {
		return nil, err
	}
	return results, nil
}

// FetchAzureLog fetches the azure.log for a specific test from GCS.
// testName should be sanitized (non-alphanumeric → underscore).
func (c *Client) FetchAzureLog(ctx context.Context, baseURL string, step, container, testName string) (string, error) {
	sanitized := sanitizeTestName(testName)
	logURL := fmt.Sprintf("%s/artifacts/%s/%s/artifacts/%s/azure.log",
		baseURL, step, container, sanitized)
	return c.fetchText(ctx, logURL, 20*time.Second)
}

// sanitizeTestName converts test names to the GCS directory format.
// The test framework only replaces spaces with underscores — commas, dots,
// and other special characters are preserved as-is.
func sanitizeTestName(name string) string {
	return strings.ReplaceAll(name, " ", "_")
}

// extractGCSPath strips the web base URL to get the raw GCS object path prefix.
func extractGCSPath(baseURL string) string {
	if strings.HasPrefix(baseURL, config.GCSWebBase) {
		return baseURL[len(config.GCSWebBase)+1:] // strip prefix + leading slash
	}
	if strings.HasPrefix(baseURL, config.GCSDirect+"/") {
		return baseURL[len(config.GCSDirect)+1:]
	}
	// Try stripping common prefixes
	if idx := strings.Index(baseURL, "logs/"); idx >= 0 {
		return baseURL[idx:]
	}
	return baseURL
}

// GCSDirectURL builds a GCS direct URL from a gs:// link.
func GCSDirectURL(gsLink string) string {
	if strings.HasPrefix(gsLink, "gs://") {
		rel := strings.Replace(gsLink, "gs://"+config.GCSBucket+"/", "", 1)
		return config.GCSDirect + "/" + rel
	}
	return gsLink
}

// GCSWebURL converts a GCS direct URL to a gcsweb URL.
func GCSWebURL(gcsURL string) string {
	return strings.Replace(gcsURL, config.GCSDirect, config.GCSWebBase, 1)
}
