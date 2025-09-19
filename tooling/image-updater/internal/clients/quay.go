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

// quayTag represents a tag from the Quay.io API response (internal type)
type quayTag struct {
	Name           string `json:"name"`
	ManifestDigest string `json:"manifest_digest"`
	LastModified   string `json:"last_modified"`
}

// quayTagsResponse represents the response from Quay.io tags API (internal type)
type quayTagsResponse struct {
	Tags          []quayTag `json:"tags"`
	Page          int       `json:"page"`
	HasAdditional bool      `json:"has_additional"`
}

func (c *QuayClient) GetLatestDigest(repository string, tagPattern string) (string, error) {
	if tagPattern == "" {
		if digest, err := c.tryGetLatestTag(repository); err == nil && digest != "" {
			return digest, nil
		}
	}

	tags, err := c.getAllTags(repository)
	if err != nil {
		return "", fmt.Errorf("failed to fetch all tags: %w", err)
	}

	if len(tags) == 0 {
		return "", fmt.Errorf("no tags found for repository %s", repository)
	}

	return ProcessTags(tags, repository, tagPattern)
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

	var tagsResp quayTagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&tagsResp); err != nil {
		return "", fmt.Errorf("failed to decode Quay.io API response: %w", err)
	}

	for _, tag := range tagsResp.Tags {
		if tag.Name == "latest" {
			if tag.ManifestDigest == "" {
				return "", fmt.Errorf("latest tag found but no manifest digest available for repository %s", repository)
			}
			return tag.ManifestDigest, nil
		}
	}

	return "", nil
}

// getAllTags fetches all tags from all pages for the specified repository
func (c *QuayClient) getAllTags(repository string) ([]Tag, error) {
	var allTags []Tag
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

		var tagsResp quayTagsResponse
		if err := json.NewDecoder(resp.Body).Decode(&tagsResp); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("failed to decode Quay.io API response (page %d): %w", page, err)
		}
		resp.Body.Close()

		for _, quayTag := range tagsResp.Tags {
			timestamp, err := ParseTimestamp(quayTag.LastModified)
			if err != nil {
				timestamp = time.Time{}
			}

			tag := Tag{
				Name:         quayTag.Name,
				Digest:       quayTag.ManifestDigest,
				LastModified: timestamp,
			}
			allTags = append(allTags, tag)
		}

		if !tagsResp.HasAdditional {
			break
		}

		page++
	}

	return allTags, nil
}
