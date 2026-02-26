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
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/go-logr/logr"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// GenericRegistryClient provides methods to interact with any Docker Registry HTTP API v2 compatible registry
type GenericRegistryClient struct {
	httpClient  *http.Client
	registryURL string
	useAuth     bool
	retryConfig retryConfig
}

// NewGenericRegistryClient creates a new generic registry client
func NewGenericRegistryClient(registryURL string, useAuth bool) *GenericRegistryClient {
	return &GenericRegistryClient{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		registryURL: registryURL,
		useAuth:     useAuth,
		retryConfig: retryConfig{
			initialInterval:     1 * time.Second,
			maxInterval:         30 * time.Second,
			maxElapsedTime:      2 * time.Minute,
			multiplier:          2.0,
			randomizationFactor: 0.5,
		},
	}
}

type dockerRegistryTagsResponse struct {
	Name string   `json:"name"`
	Tags []string `json:"tags"`
}

// doRequestWithRetry performs an HTTP request with exponential backoff retry logic
// It retries on temporary network errors and 5xx server errors
// The operation can be cancelled via context (e.g., Ctrl+C)
func (c *GenericRegistryClient) doRequestWithRetry(ctx context.Context, req *http.Request) (*http.Response, error) {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("logger not found in context: %w", err)
	}
	var resp *http.Response

	// Create a new backoff instance for this request
	expBackoff := backoff.NewExponentialBackOff()
	expBackoff.InitialInterval = c.retryConfig.initialInterval
	expBackoff.MaxInterval = c.retryConfig.maxInterval
	expBackoff.MaxElapsedTime = c.retryConfig.maxElapsedTime
	expBackoff.Multiplier = c.retryConfig.multiplier
	expBackoff.RandomizationFactor = c.retryConfig.randomizationFactor

	// Wrap with context to respect cancellation (Ctrl+C)
	contextBackoff := backoff.WithContext(expBackoff, ctx)

	operation := func() error {
		// Check if context is already cancelled before making the request
		select {
		case <-ctx.Done():
			return backoff.Permanent(ctx.Err())
		default:
		}

		var err error
		resp, err = c.httpClient.Do(req)
		if err != nil {
			logger.V(2).Info("request failed, will retry", "url", req.URL.String(), "error", err.Error())
			return err
		}

		// Retry on 5xx server errors and 429 (rate limiting)
		if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
			resp.Body.Close()
			err = fmt.Errorf("server returned status %d", resp.StatusCode)
			logger.V(2).Info("request failed with retryable status code", "url", req.URL.String(), "status", resp.StatusCode)
			return err
		}

		// Success or non-retryable error
		return nil
	}

	notify := func(err error, duration time.Duration) {
		logger.V(2).Info("retrying request after backoff", "url", req.URL.String(), "error", err.Error(), "backoff", duration.String())
	}

	// Use backoff.RetryNotify with context to respect cancellation
	if err := backoff.RetryNotify(operation, contextBackoff, notify); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("request cancelled: %w", ctx.Err())
		}
		return nil, fmt.Errorf("request failed after retries: %w", err)
	}

	return resp, nil
}

// extractTimestampFromMultiArchManifest attempts to get a meaningful timestamp for a multi-arch manifest
// It tries three approaches in order:
// 1. Use the provided timestamp if it's already set and not Unix epoch
// 2. Extract from the first platform-specific image's config
// 3. Parse from the tag name if it contains an embedded date (e.g., master.251204.1)
func extractTimestampFromMultiArchManifest(desc *remote.Descriptor, tagName string, currentTimestamp time.Time) time.Time {
	unixEpoch := time.Unix(0, 0).UTC()

	// If we already have a valid timestamp, use it
	if !currentTimestamp.IsZero() && !currentTimestamp.Equal(unixEpoch) {
		return currentTimestamp
	}

	// Try to get timestamp from a platform-specific image in the manifest
	if idx, err := desc.ImageIndex(); err == nil {
		if manifest, err := idx.IndexManifest(); err == nil && len(manifest.Manifests) > 0 {
			if platformImg, err := idx.Image(manifest.Manifests[0].Digest); err == nil {
				if configFile, err := platformImg.ConfigFile(); err == nil {
					ts := configFile.Created.Time
					if !ts.IsZero() && !ts.Equal(unixEpoch) {
						return ts
					}
				}
			}
		}
	}

	// Fallback: try parsing date from tag name
	if parsedDate, ok := ParseDateFromTag(tagName); ok {
		return parsedDate
	}

	return currentTimestamp
}

func (c *GenericRegistryClient) getAllTags(ctx context.Context, repository string) ([]Tag, error) {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("logger not found in context: %w", err)
	}
	// Use Docker Registry HTTP API v2 for listing tags
	url := fmt.Sprintf("https://%s/v2/%s/tags/list", c.registryURL, repository)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request (url: %s): %w", url, err)
	}

	resp, err := c.doRequestWithRetry(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to request registry API (url: %s): %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registry API returned status %d for repository %s (url: %s)", resp.StatusCode, repository, url)
	}

	var tagsResp dockerRegistryTagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&tagsResp); err != nil {
		return nil, fmt.Errorf("failed to decode registry API response (url: %s): %w", url, err)
	}

	logger.V(2).Info("fetched tags from generic registry", "registry", c.registryURL, "repository", repository, "totalTags", len(tagsResp.Tags))

	var allTags []Tag
	for _, tagName := range tagsResp.Tags {
		tag := Tag{
			Name: tagName,
			// We'll get digest and last modified from manifests later
			LastModified: time.Time{},
		}
		allTags = append(allTags, tag)
	}

	return allTags, nil
}

func (c *GenericRegistryClient) GetArchSpecificDigest(ctx context.Context, repository string, tagPattern string, arch string, multiArch bool, versionLabel string) (*Tag, error) {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("logger not found in context: %w", err)
	}

	logger.V(2).Info("fetching tags from generic registry", "registry", c.registryURL, "repository", repository, "useAuth", c.useAuth, "versionLabel", versionLabel)

	allTags, err := c.getAllTags(ctx, repository)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch all tags: %w", err)
	}

	remoteOpts := GetRemoteOptions(c.useAuth)

	// Cache for remote descriptors to avoid duplicate remote.Get calls
	descriptorCache := make(map[string]*remote.Descriptor)

	// Enrich tags with digest and timestamp information before filtering
	var enrichedTags []Tag
	for _, tag := range allTags {
		// Check if context is cancelled before processing each tag
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("operation cancelled while enriching tags: %w", ctx.Err())
		default:
		}

		ref, err := name.ParseReference(fmt.Sprintf("%s/%s:%s", c.registryURL, repository, tag.Name))
		if err != nil {
			logger.V(2).Info("failed to parse reference, skipping", "tag", tag.Name, "error", err)
			continue
		}

		desc, err := remote.Get(ref, remoteOpts...)
		if err != nil {
			logger.V(2).Info("failed to fetch image descriptor, skipping", "tag", tag.Name, "error", err)
			continue
		}

		// Cache the descriptor for later use
		descriptorCache[tag.Name] = desc

		// Try to get creation time and version label from config
		if img, err := desc.Image(); err == nil {
			if configFile, err := img.ConfigFile(); err == nil {
				tag.LastModified = configFile.Created.Time
				tag.Version = extractVersionFromConfigLabels(configFile.Config.Labels, versionLabel)
				if tag.Version != "" {
					logger.V(2).Info("extracted version from label", "tag", tag.Name, "label", versionLabel, "version", tag.Version)
				}
			}
		}

		tag.Digest = desc.Digest.String()
		enrichedTags = append(enrichedTags, tag)
	}

	tags, err := PrepareTagsForArchValidation(enrichedTags, repository, tagPattern)
	if err != nil {
		logger.V(2).Error(err, "failed to prepare tags for arch validation", "registry", c.registryURL, "repository", repository, "tagPattern", tagPattern, "totalTags", len(enrichedTags))
		return nil, err
	}

	logger.V(2).Info("filtered tags by pattern", "registry", c.registryURL, "repository", repository, "tagPattern", tagPattern, "matchingTags", len(tags))

	for _, tag := range tags {
		// Check if context is cancelled before processing each tag
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("operation cancelled while checking tags: %w", ctx.Err())
		default:
		}

		// Use cached descriptor instead of calling remote.Get again
		desc, ok := descriptorCache[tag.Name]
		if !ok {
			logger.V(2).Error(fmt.Errorf("descriptor not found in cache"), "missing descriptor for tag", "tag", tag.Name)
			continue
		}

		// If multiArch is requested, return the multi-arch manifest list digest
		if multiArch && desc.MediaType.IsIndex() {
			logger.V(2).Info("found multi-arch manifest", "tag", tag.Name, "mediaType", desc.MediaType, "digest", desc.Digest.String())
			tag.Digest = desc.Digest.String()
			tag.LastModified = extractTimestampFromMultiArchManifest(desc, tag.Name, tag.LastModified)
			return &tag, nil
		}

		if desc.MediaType.IsIndex() {
			logger.V(2).Info("skipping multi-arch manifest", "tag", tag.Name, "mediaType", desc.MediaType)
			continue
		}

		img, err := desc.Image()
		if err != nil {
			logger.V(2).Error(err, "failed to get image", "tag", tag.Name)
			continue
		}

		configFile, err := img.ConfigFile()
		if err != nil {
			logger.V(2).Error(err, "failed to get config", "tag", tag.Name)
			continue
		}

		normalizedArch := NormalizeArchitecture(configFile.Architecture)

		if normalizedArch == arch && configFile.OS == "linux" {
			digest, err := img.Digest()
			if err != nil {
				logger.V(2).Error(err, "failed to get image digest", "tag", tag.Name)
				continue
			}
			tag.Digest = digest.String()
			return &tag, nil
		}

		logger.V(2).Info("skipping non-matching architecture", "tag", tag.Name, "arch", configFile.Architecture, "os", configFile.OS, "wantArch", arch)
	}

	if multiArch {
		return nil, fmt.Errorf("no multi-arch manifest found for repository %s", repository)
	}
	return nil, fmt.Errorf("no single-arch %s/linux image found for repository %s (all tags are either multi-arch or different architecture)", arch, repository)
}

// GetDigestForTag fetches the digest for a specific tag without pagination
func (c *GenericRegistryClient) GetDigestForTag(ctx context.Context, repository string, tagName string, arch string, multiArch bool, versionLabel string) (*Tag, error) {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("logger not found in context: %w", err)
	}

	logger.V(2).Info("fetching digest for specific tag", "registry", c.registryURL, "repository", repository, "tag", tagName, "useAuth", c.useAuth, "versionLabel", versionLabel)

	// Check if context is cancelled before processing
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("operation cancelled: %w", ctx.Err())
	default:
	}

	remoteOpts := GetRemoteOptions(c.useAuth)
	ref, err := name.ParseReference(fmt.Sprintf("%s/%s:%s", c.registryURL, repository, tagName))
	if err != nil {
		return nil, fmt.Errorf("failed to parse reference for tag %s: %w", tagName, err)
	}

	desc, err := remote.Get(ref, remoteOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch image descriptor for tag %s: %w", tagName, err)
	}

	tag := Tag{
		Name:   tagName,
		Digest: desc.Digest.String(),
	}

	// Try to get creation time and version label from config
	if img, err := desc.Image(); err == nil {
		if configFile, err := img.ConfigFile(); err == nil {
			tag.LastModified = configFile.Created.Time
			tag.Version = extractVersionFromConfigLabels(configFile.Config.Labels, versionLabel)
			if tag.Version != "" {
				logger.V(2).Info("extracted version from label", "tag", tagName, "label", versionLabel, "version", tag.Version)
			}
		}
	}

	// If multiArch is requested, return the multi-arch manifest list digest
	if multiArch {
		if !desc.MediaType.IsIndex() {
			return nil, fmt.Errorf("tag %s is not a multi-arch manifest (mediaType: %s)", tagName, desc.MediaType)
		}
		logger.V(2).Info("found multi-arch manifest", "tag", tagName, "mediaType", desc.MediaType, "digest", desc.Digest.String())
		tag.LastModified = extractTimestampFromMultiArchManifest(desc, tagName, tag.LastModified)
		return &tag, nil
	}

	// For single-arch, verify architecture matches
	if desc.MediaType.IsIndex() {
		return nil, fmt.Errorf("tag %s is a multi-arch manifest, but single-arch was requested (use multiArch: true)", tagName)
	}

	img, err := desc.Image()
	if err != nil {
		return nil, fmt.Errorf("failed to get image for tag %s: %w", tagName, err)
	}

	configFile, err := img.ConfigFile()
	if err != nil {
		return nil, fmt.Errorf("failed to get config for tag %s: %w", tagName, err)
	}

	normalizedArch := NormalizeArchitecture(configFile.Architecture)

	if normalizedArch != arch || configFile.OS != "linux" {
		return nil, fmt.Errorf("tag %s has architecture %s/%s, but %s/linux was requested", tagName, configFile.Architecture, configFile.OS, arch)
	}

	digest, err := img.Digest()
	if err != nil {
		return nil, fmt.Errorf("failed to get image digest for tag %s: %w", tagName, err)
	}

	tag.Digest = digest.String()
	logger.V(2).Info("found matching image", "tag", tagName, "arch", normalizedArch, "digest", tag.Digest)

	return &tag, nil
}
