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

	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/config"
)

var buildIDRE = regexp.MustCompile(`^\d{19}$`)

// Client is an HTTP client for the GCS storage API used by Prow.
type Client struct {
	http *http.Client
	mu   sync.Mutex
	errs map[string]int
}

// NewClient creates a new GCS client with the given HTTP client.
func NewClient(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{
		http: httpClient,
		errs: make(map[string]int),
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

// fetchBytes fetches raw bytes from a URL.
func (c *Client) fetchBytes(ctx context.Context, u string, timeout time.Duration) ([]byte, error) {
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

	return io.ReadAll(resp.Body)
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

// fetchText fetches text from a URL, rejecting HTML responses.
func (c *Client) fetchText(ctx context.Context, u string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return "", err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			c.recordError("timeout")
		} else {
			c.recordError("network")
		}
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		c.recordError("not_found")
		return "", nil
	}
	if resp.StatusCode != http.StatusOK {
		c.recordError("http")
		return "", fmt.Errorf("HTTP %d for %s", resp.StatusCode, u)
	}

	ct := resp.Header.Get("Content-Type")
	if strings.Contains(ct, "text/html") {
		return "", nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
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

// ListPeriodicBuilds lists build IDs for a periodic job from GCS.
func (c *Client) ListPeriodicBuilds(ctx context.Context, jobName string, startBID string, limit int) ([]string, error) {
	prefix := fmt.Sprintf("logs/%s/", jobName)
	params := url.Values{
		"prefix":    {prefix},
		"delimiter": {"/"},
		"maxResults": {fmt.Sprintf("%d", limit)},
		"fields":    {"prefixes,nextPageToken"},
	}
	if startBID != "" {
		params.Set("startOffset", prefix+startBID+"/")
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

// PresubmitBuild holds a build ID and its GCS link.
type PresubmitBuild struct {
	BuildID string
	GSLink  string
}

// ListPresubmitBuilds lists build IDs for a presubmit job from GCS.
func (c *Client) ListPresubmitBuilds(ctx context.Context, jobName string, startBID string, limit int) ([]PresubmitBuild, error) {
	prefix := fmt.Sprintf("pr-logs/directory/%s/", jobName)
	params := url.Values{
		"prefix":     {prefix},
		"maxResults": {fmt.Sprintf("%d", limit)},
		"fields":     {"items(name,metadata)"},
	}
	if startBID != "" {
		params.Set("startOffset", prefix+startBID)
	}

	apiURL := config.GCSAPI + "?" + params.Encode()
	var resp gcsListResponse
	if err := c.fetchJSON(ctx, apiURL, 15*time.Second, &resp); err != nil {
		return nil, err
	}

	var results []PresubmitBuild
	for _, item := range resp.Items {
		fname := item.Name
		if idx := strings.LastIndex(fname, "/"); idx >= 0 {
			fname = fname[idx+1:]
		}
		if fname == "latest-build.txt" || !strings.HasSuffix(fname, ".txt") {
			continue
		}
		bid := strings.TrimSuffix(fname, ".txt")
		if !buildIDRE.MatchString(bid) {
			continue
		}
		gsLink := item.Metadata["x-goog-meta-link"]
		results = append(results, PresubmitBuild{BuildID: bid, GSLink: gsLink})
	}
	return results, nil
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
