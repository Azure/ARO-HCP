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
	"strings"
	"time"
)

// QuayClient provides methods to interact with Quay.io API
type QuayClient struct {
	httpClient        *http.Client
	baseURL           string
	architectureCache map[string]string // Cache digest -> architecture mappings
}

// NewQuayClient creates a new Quay.io API client
func NewQuayClient() *QuayClient {
	return &QuayClient{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		baseURL:           "https://quay.io/api/v1",
		architectureCache: make(map[string]string),
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

	// For pattern-based searches, use an optimized approach
	if tagPattern != "" {
		return c.getLatestDigestWithPattern(repository, tagPattern)
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

			// Ensure latest tag is a container image, not metadata
			if !IsContainerImageTag(tag.Name) {
				return "", fmt.Errorf("latest tag is not a container image for repository %s", repository)
			}

			// Check architecture for latest tag
			arch, err := c.getArchitectureForDigest(repository, tag.ManifestDigest)
			if err != nil {
				return "", fmt.Errorf("failed to get architecture for latest tag: %w", err)
			}

			if !IsValidArchitecture(arch) {
				return "", fmt.Errorf("latest tag has unsupported architecture %s (need amd64 or x86_64)", arch)
			}

			return tag.ManifestDigest, nil
		}
	}

	return "", nil
}

// getLatestDigestWithPattern efficiently finds the latest tag matching pattern with valid architecture
func (c *QuayClient) getLatestDigestWithPattern(repository string, tagPattern string) (string, error) {
	regex, err := regexp.Compile(tagPattern)
	if err != nil {
		return "", fmt.Errorf("invalid tag pattern %s: %w", tagPattern, err)
	}

	page := 1
	for {
		url := fmt.Sprintf("%s/repository/%s/tag?page=%d", c.baseURL, repository, page)

		resp, err := c.httpClient.Get(url)
		if err != nil {
			return "", fmt.Errorf("failed to request Quay.io API page %d: %w", page, err)
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return "", fmt.Errorf("quay.io API returned status %d for repository %s (page %d)", resp.StatusCode, repository, page)
		}

		var tagsResp quayTagsResponse
		if err := json.NewDecoder(resp.Body).Decode(&tagsResp); err != nil {
			resp.Body.Close()
			return "", fmt.Errorf("failed to decode Quay.io API response (page %d): %w", page, err)
		}
		resp.Body.Close()

		// Process tags in chronological order (Quay returns newest first)
		for _, quayTag := range tagsResp.Tags {
			// Skip non-container image tags
			if !IsContainerImageTag(quayTag.Name) {
				continue
			}

			// Check if tag matches pattern
			if !regex.MatchString(quayTag.Name) {
				continue
			}

			// Check architecture for this tag
			architecture, err := c.getArchitectureForDigest(repository, quayTag.ManifestDigest)
			if err != nil {
				continue
			}

			// If this is a valid architecture, we found our latest tag!
			if IsValidArchitecture(architecture) {
				return quayTag.ManifestDigest, nil
			}
		}

		// If no more pages, we're done
		if !tagsResp.HasAdditional {
			break
		}

		page++
	}

	return "", fmt.Errorf("no valid tags found matching pattern %s for repository %s", tagPattern, repository)
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
			// Skip non-container image tags (signatures, attestations, etc.)
			if !IsContainerImageTag(quayTag.Name) {
				continue
			}

			timestamp, err := ParseTimestamp(quayTag.LastModified)
			if err != nil {
				timestamp = time.Time{}
			}

			// Check architecture for this tag
			architecture, err := c.getArchitectureForDigest(repository, quayTag.ManifestDigest)
			if err != nil {
				// Skip this tag if we can't determine architecture
				continue
			}

			// Only include amd64/x86_64 images
			if !IsValidArchitecture(architecture) {
				continue
			}

			tag := Tag{
				Name:         quayTag.Name,
				Digest:       quayTag.ManifestDigest,
				LastModified: timestamp,
				Architecture: architecture,
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

// getArchitectureForDigest efficiently determines the architecture for a given digest
func (c *QuayClient) getArchitectureForDigest(repository string, digest string) (string, error) {
	// Check cache first
	if arch, exists := c.architectureCache[digest]; exists {
		return arch, nil
	}

	// Remove sha256: prefix if present
	cleanDigest := strings.TrimPrefix(digest, "sha256:")

	// Construct the manifest URL using Docker Registry API v2
	registryURL := strings.Replace(c.baseURL, "/api/v1", "", 1)
	manifestURL := fmt.Sprintf("%s/v2/%s/manifests/sha256:%s", registryURL, repository, cleanDigest)

	req, err := http.NewRequest("GET", manifestURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create manifest request: %w", err)
	}

	// Set Accept headers to request only single-arch manifests (no manifest lists)
	req.Header.Set("Accept", "application/vnd.docker.distribution.manifest.v2+json, application/vnd.oci.image.manifest.v1+json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to request manifest: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("manifest request returned status %d", resp.StatusCode)
	}

	contentType := resp.Header.Get("Content-Type")

	// Skip multi-arch manifest lists - we only want single-arch images
	if strings.Contains(contentType, "manifest.list") || strings.Contains(contentType, "image.index") {
		return "", fmt.Errorf("skipping multi-arch manifest list - looking for single-arch images only")
	}

	// Handle single architecture manifest only
	var manifest Manifest
	if err := json.NewDecoder(resp.Body).Decode(&manifest); err != nil {
		return "", fmt.Errorf("failed to decode manifest: %w", err)
	}

	var architecture string

	// For single arch manifests, we might need to fetch the config to get architecture
	if manifest.Architecture != "" {
		architecture = manifest.Architecture
	} else if manifest.Config.Digest != "" {
		// If architecture is not in manifest, fetch it from config blob
		var err error
		architecture, err = c.getArchitectureFromConfig(repository, manifest.Config.Digest)
		if err != nil {
			return "", err
		}
	} else {
		return "", fmt.Errorf("could not determine architecture from manifest")
	}

	// Cache the result
	c.architectureCache[digest] = architecture

	return architecture, nil
}

// getArchitectureFromConfig fetches the config blob to get architecture information
func (c *QuayClient) getArchitectureFromConfig(repository string, configDigest string) (string, error) {
	cleanDigest := strings.TrimPrefix(configDigest, "sha256:")
	registryURL := strings.Replace(c.baseURL, "/api/v1", "", 1)
	configURL := fmt.Sprintf("%s/v2/%s/blobs/sha256:%s", registryURL, repository, cleanDigest)

	resp, err := c.httpClient.Get(configURL)
	if err != nil {
		return "", fmt.Errorf("failed to request config blob: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("config blob request returned status %d", resp.StatusCode)
	}

	var config struct {
		Architecture string `json:"architecture"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&config); err != nil {
		return "", fmt.Errorf("failed to decode config blob: %w", err)
	}

	if config.Architecture == "" {
		return "", fmt.Errorf("architecture not found in config blob")
	}

	return config.Architecture, nil
}
