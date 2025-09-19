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
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"time"
)

// QuayClient provides methods to interact with Quay.io API
type QuayClient struct {
	httpClient *http.Client
	baseURL    string
}

// NewQuayClient creates a new Quay.io API client
func NewQuayClient() *QuayClient {
	return &QuayClient{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		baseURL: "https://quay.io/api/v1",
	}
}

// QuayTag represents a tag from the Quay.io API response
type QuayTag struct {
	Name           string `json:"name"`
	ManifestDigest string `json:"manifest_digest"`
	LastModified   string `json:"last_modified"`
}

// QuayTagsResponse represents the response from Quay.io tags API
type QuayTagsResponse struct {
	Tags          []QuayTag `json:"tags"`
	Page          int       `json:"page"`
	HasAdditional bool      `json:"has_additional"`
}

func (c *QuayClient) GetLatestDigest(repository string, tagPattern string) (string, error) {
	tag, err := c.tryGetLatestTag(repository)
	if err != nil {
		return "", err
	} else if tag != "" {
		return tag, nil
	}
	return c.getDigestByTagPattern(repository, tagPattern)
}

// tryGetLatestTag efficiently checks for a "latest" tag without full pagination
func (c *QuayClient) tryGetLatestTag(repository string) (string, error) {
	url := fmt.Sprintf("%s/repository/%s/tag?page=1", c.baseURL, repository)

	resp, err := c.httpClient.Get(url)
	if err != nil {
		return "", fmt.Errorf("failed to request Quay.io API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("quay.io API returned status %d for repository %s", resp.StatusCode, repository)
	}

	var tagsResp QuayTagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&tagsResp); err != nil {
		return "", fmt.Errorf("failed to decode Quay.io API response: %w", err)
	}

	// Look for the "latest" tag in the first page
	for _, tag := range tagsResp.Tags {
		if tag.Name == "latest" {
			if tag.ManifestDigest == "" {
				return "", fmt.Errorf("latest tag found but no manifest digest available for repository %s", repository)
			}
			return tag.ManifestDigest, nil
		}
	}

	return "", nil // "latest" tag not found
}

// getDigestByTagPattern fetches the latest digest for tags matching the given regex pattern
func (c *QuayClient) getDigestByTagPattern(repository string, tagPattern string) (string, error) {
	// Compile the regex pattern
	regex, err := regexp.Compile(tagPattern)
	if err != nil {
		return "", fmt.Errorf("invalid tag pattern %s: %w", tagPattern, err)
	}

	// Get all tags and filter by pattern
	tags, err := c.getAllTags(repository)
	if err != nil {
		return "", fmt.Errorf("failed to fetch all tags: %w", err)
	}

	// Filter tags by pattern and exclude metadata tags
	var matchingTags []QuayTag
	for _, tag := range tags {
		// Check if tag matches the pattern
		if !regex.MatchString(tag.Name) {
			continue
		}

		// Skip signature and attestation tags
		if isMetadataTag(tag.Name) {
			continue
		}

		if tag.ManifestDigest == "" {
			continue
		}

		matchingTags = append(matchingTags, tag)
	}

	if len(matchingTags) == 0 {
		return "", fmt.Errorf("no tags matching pattern %s found for repository %s", tagPattern, repository)
	}

	// Sort tags by last modified date (newest first)
	sort.Slice(matchingTags, func(i, j int) bool {
		// For descending sort (newest first), we want i > j in terms of time
		return c.compareTimestamps(matchingTags[i].LastModified, matchingTags[j].LastModified)
	})

	mostRecent := &matchingTags[0]
	return mostRecent.ManifestDigest, nil
}

// getAllTags fetches all tags from all pages for the specified repository
func (c *QuayClient) getAllTags(repository string) ([]QuayTag, error) {
	var allTags []QuayTag
	page := 1

	for {
		url := fmt.Sprintf("%s/repository/%s/tag?page=%d", c.baseURL, repository, page)

		resp, err := c.httpClient.Get(url)
		if err != nil {
			return nil, fmt.Errorf("failed to request Quay.io API page %d: %w", page, err)
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("quay.io API returned status %d for repository %s (page %d)", resp.StatusCode, repository, page)
		}

		var tagsResp QuayTagsResponse
		if err := json.NewDecoder(resp.Body).Decode(&tagsResp); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("failed to decode Quay.io API response (page %d): %w", page, err)
		}
		resp.Body.Close()

		// Add tags from this page
		allTags = append(allTags, tagsResp.Tags...)

		// Check if there are more pages
		if !tagsResp.HasAdditional {
			break
		}

		page++
	}

	return allTags, nil
}

// compareTimestamps compares two timestamp strings, returning true if the first is newer
// Falls back to string comparison if parsing fails
func (c *QuayClient) compareTimestamps(timestamp1, timestamp2 string) bool {
	// Quay.io uses RFC1123 format: "Wed, 25 Dec 2024 14:43:12 -0000"
	time1, err1 := time.Parse(time.RFC1123Z, timestamp1)
	time2, err2 := time.Parse(time.RFC1123Z, timestamp2)

	// If both parsed successfully, compare times
	if err1 == nil && err2 == nil {
		return time1.After(time2)
	}

	// Try alternative formats if RFC1123Z fails
	formats := []string{
		time.RFC1123,
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05.000Z",
		"2006-01-02 15:04:05",
	}

	for _, format := range formats {
		time1, err1 := time.Parse(format, timestamp1)
		time2, err2 := time.Parse(format, timestamp2)
		if err1 == nil && err2 == nil {
			return time1.After(time2)
		}
	}

	// Fall back to string comparison (works for ISO 8601 formatted strings)
	return timestamp1 > timestamp2
}
