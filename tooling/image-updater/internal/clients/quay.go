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
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

const (
	// quayPageSize is the number of tags to fetch per page from Quay API
	quayPageSize = 100
)

// QuayClient provides methods to interact with Quay.io
// Note: For private repositories, this client falls back to using the Docker Registry V2 API
// instead of Quay's proprietary API, as the latter requires different credentials
type QuayClient struct {
	httpClient  *http.Client
	baseURL     string
	useAuth     bool
	retryConfig retryConfig
}

type retryConfig struct {
	initialInterval     time.Duration
	maxInterval         time.Duration
	maxElapsedTime      time.Duration
	multiplier          float64
	randomizationFactor float64
}

// NewQuayClient creates a new Quay.io client
func NewQuayClient(useAuth bool) *QuayClient {
	return &QuayClient{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		baseURL: "https://quay.io/api/v1",
		useAuth: useAuth,
		retryConfig: retryConfig{
			initialInterval:     1 * time.Second,
			maxInterval:         30 * time.Second,
			maxElapsedTime:      2 * time.Minute,
			multiplier:          2.0,
			randomizationFactor: 0.5,
		},
	}
}

type quayTag struct {
	Name           string `json:"name"`
	ManifestDigest string `json:"manifest_digest"`
	LastModified   string `json:"last_modified"`
}

type quayTagsResponse struct {
	Tags          []quayTag `json:"tags"`
	Page          int       `json:"page"`
	HasAdditional bool      `json:"has_additional"`
}

// addAuth adds authentication headers to the request using Docker credentials
// It follows the Docker Registry V2 authentication flow: get token, then use it
func (c *QuayClient) addAuth(req *http.Request, repository string) error {
	// Parse the registry reference to get the resource name
	ref, err := name.NewRepository(fmt.Sprintf("quay.io/%s", repository))
	if err != nil {
		return fmt.Errorf("failed to parse repository: %w", err)
	}

	// Get authenticator from the default keychain (reads from ~/.docker/config.json)
	authenticator, err := authn.DefaultKeychain.Resolve(ref.Registry)
	if err != nil {
		return fmt.Errorf("failed to resolve authenticator: %w", err)
	}

	// Get the auth config
	authConfig, err := authenticator.Authorization()
	if err != nil {
		return fmt.Errorf("failed to get authorization: %w", err)
	}

	// Get a bearer token using the Registry V2 auth flow
	token, err := c.getBearerToken(repository, *authConfig)
	if err != nil {
		return fmt.Errorf("failed to get bearer token: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	return nil
}

// getBearerToken exchanges credentials for a bearer token following the Docker Registry V2 auth spec
func (c *QuayClient) getBearerToken(repository string, authConfig authn.AuthConfig) (string, error) {
	// The auth endpoint for Quay.io
	tokenURL := fmt.Sprintf("https://quay.io/v2/auth?service=quay.io&scope=repository:%s:pull", repository)

	tokenReq, err := http.NewRequest("GET", tokenURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create token request: %w", err)
	}

	// Use basic auth to get the bearer token
	if authConfig.Username != "" && authConfig.Password != "" {
		tokenReq.SetBasicAuth(authConfig.Username, authConfig.Password)
	} else if authConfig.Auth != "" {
		// Auth is already base64 encoded username:password
		tokenReq.Header.Set("Authorization", fmt.Sprintf("Basic %s", authConfig.Auth))
	} else {
		return "", fmt.Errorf("no credentials found in Docker config")
	}

	tokenResp, err := c.httpClient.Do(tokenReq)
	if err != nil {
		return "", fmt.Errorf("failed to request token: %w", err)
	}
	defer tokenResp.Body.Close()

	if tokenResp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token request returned status %d", tokenResp.StatusCode)
	}

	var tokenData struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(tokenResp.Body).Decode(&tokenData); err != nil {
		return "", fmt.Errorf("failed to decode token response: %w", err)
	}

	// Return whichever field is populated
	if tokenData.Token != "" {
		return tokenData.Token, nil
	}
	if tokenData.AccessToken != "" {
		return tokenData.AccessToken, nil
	}

	return "", fmt.Errorf("no token in response")
}

// doRequestWithRetry performs an HTTP request with exponential backoff retry logic
// It retries on temporary network errors and 5xx server errors
// The operation can be cancelled via context (e.g., Ctrl+C)
func (c *QuayClient) doRequestWithRetry(ctx context.Context, req *http.Request) (*http.Response, error) {
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
			logger.V(1).Info("request failed, will retry", "url", req.URL.String(), "error", err.Error())
			return err
		}

		// Retry on 5xx server errors and 429 (rate limiting)
		if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
			resp.Body.Close()
			err = fmt.Errorf("server returned status %d", resp.StatusCode)
			logger.V(1).Info("request failed with retryable status code", "url", req.URL.String(), "status", resp.StatusCode)
			return err
		}

		// Success or non-retryable error
		return nil
	}

	notify := func(err error, duration time.Duration) {
		logger.Info("retrying request after backoff", "url", req.URL.String(), "error", err.Error(), "backoff", duration.String())
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

func (c *QuayClient) getAllTagsWithCache(ctx context.Context, repository string, descriptorCache map[string]*remote.Descriptor) ([]Tag, error) {
	// If authentication is required, use Docker Registry V2 API instead of Quay's proprietary API
	// This is because Quay's API requires different credentials (OAuth2 tokens) than registry access
	if c.useAuth {
		return c.getAllTagsViaRegistryAPIWithCache(ctx, repository, descriptorCache)
	}

	logger, err := logr.FromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("logger not found in context: %w", err)
	}
	// For public repositories, use Quay's proprietary API which provides timestamps
	var allTags []Tag
	page := 1

	for {
		// Check if context is cancelled before fetching next page
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("operation cancelled while fetching Quay tags: %w", ctx.Err())
		default:
		}

		url := fmt.Sprintf("%s/repository/%s/tag?page=%d&limit=%d", c.baseURL, repository, page, quayPageSize)

		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request for page %d (url: %s): %w", page, url, err)
		}

		resp, err := c.doRequestWithRetry(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("failed to request Quay.io API page %d (url: %s): %w", page, url, err)
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("quay.io API returned status %d for repository %s (page %d, url: %s)", resp.StatusCode, repository, page, url)
		}

		var tagsResp quayTagsResponse
		if err := json.NewDecoder(resp.Body).Decode(&tagsResp); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("failed to decode Quay.io API response (page %d, url: %s): %w", page, url, err)
		}
		resp.Body.Close()

		logger.V(1).Info("fetched page", "page", page, "repository", repository, "tags", len(tagsResp.Tags))

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
		logger.V(1).Info("fetching next page", "page", page, "repository", repository)
	}

	return allTags, nil
}

func (c *QuayClient) getAllTagsViaRegistryAPIWithCache(ctx context.Context, repository string, descriptorCache map[string]*remote.Descriptor) ([]Tag, error) {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("logger not found in context: %w", err)
	}
	url := fmt.Sprintf("https://quay.io/v2/%s/tags/list", repository)

	// Check if context is cancelled
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("operation cancelled while fetching Quay tags via registry API: %w", ctx.Err())
	default:
	}

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if err := c.addAuth(req, repository); err != nil {
		return nil, fmt.Errorf("failed to add authentication: %w", err)
	}

	resp, err := c.doRequestWithRetry(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to request registry API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registry API returned status %d for repository %s", resp.StatusCode, repository)
	}

	var tagsResp struct {
		Name string   `json:"name"`
		Tags []string `json:"tags"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tagsResp); err != nil {
		return nil, fmt.Errorf("failed to decode registry API response: %w", err)
	}

	var allTags []Tag
	for _, tagName := range tagsResp.Tags {
		tag := Tag{
			Name: tagName,
			// Registry V2 API doesn't provide timestamps in tags list
			// They will be enriched from image config
			LastModified: time.Time{},
		}
		allTags = append(allTags, tag)
	}

	// Enrich tags with timestamp information from image configs
	logger.V(1).Info("enriching tags with timestamp information", "repository", repository, "totalTags", len(allTags))
	remoteOpts := GetRemoteOptions(c.useAuth)
	enrichedTags := make([]Tag, 0, len(allTags))

	for _, tag := range allTags {
		// Check if context is cancelled before processing each tag
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("operation cancelled while enriching tags: %w", ctx.Err())
		default:
		}

		ref, err := name.ParseReference(fmt.Sprintf("quay.io/%s:%s", repository, tag.Name))
		if err != nil {
			logger.V(1).Info("failed to parse reference, skipping", "tag", tag.Name, "error", err)
			continue
		}

		desc, err := remote.Get(ref, remoteOpts...)
		if err != nil {
			logger.V(1).Info("failed to fetch image descriptor, skipping", "tag", tag.Name, "error", err)
			continue
		}

		// Cache the descriptor for later use if cache is provided
		if descriptorCache != nil {
			descriptorCache[tag.Name] = desc
		}

		// Try to get creation time from config
		// For multi-arch manifests, get the timestamp from the first platform-specific image
		if desc.MediaType.IsIndex() {
			// Multi-arch manifest - get timestamp from first platform image
			logger.V(1).Info("processing multi-arch manifest", "tag", tag.Name, "mediaType", desc.MediaType)
			if idx, err := desc.ImageIndex(); err == nil {
				if manifest, err := idx.IndexManifest(); err == nil && len(manifest.Manifests) > 0 {
					// Try to get the config from the first manifest
					if platformDesc := manifest.Manifests[0]; platformDesc.MediaType.IsImage() {
						platformRef, err := name.ParseReference(fmt.Sprintf("quay.io/%s@%s", repository, platformDesc.Digest.String()))
						if err == nil {
							if platformDescriptor, err := remote.Get(platformRef, remoteOpts...); err == nil {
								if platformImg, err := platformDescriptor.Image(); err == nil {
									if configFile, err := platformImg.ConfigFile(); err == nil {
										tag.LastModified = configFile.Created.Time
										logger.V(1).Info("got timestamp from multi-arch manifest", "tag", tag.Name, "timestamp", tag.LastModified)
									}
								}
							}
						}
					}
				}
			}
		} else {
			// Single-arch image
			logger.V(1).Info("processing single-arch image", "tag", tag.Name, "mediaType", desc.MediaType)
			if img, err := desc.Image(); err == nil {
				if configFile, err := img.ConfigFile(); err == nil {
					tag.LastModified = configFile.Created.Time
					logger.V(1).Info("got timestamp from single-arch image", "tag", tag.Name, "timestamp", tag.LastModified)
				} else {
					logger.V(1).Info("failed to get config file", "tag", tag.Name, "error", err)
				}
			} else {
				logger.V(1).Info("failed to get image", "tag", tag.Name, "error", err)
			}
		}

		if tag.LastModified.IsZero() {
			logger.V(1).Info("warning: tag has zero timestamp after enrichment", "tag", tag.Name, "mediaType", desc.MediaType)
		}

		tag.Digest = desc.Digest.String()
		enrichedTags = append(enrichedTags, tag)
	}

	logger.V(1).Info("enriched tags with timestamp information", "repository", repository, "enrichedTags", len(enrichedTags))
	return enrichedTags, nil
}

func (c *QuayClient) GetArchSpecificDigest(ctx context.Context, repository string, tagPattern string, arch string, multiArch bool) (*Tag, error) {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("logger not found in context: %w", err)
	}

	logger.V(1).Info("fetching tags from Quay", "image", repository, "repository", repository, "useAuth", c.useAuth)

	// Cache for remote descriptors to avoid duplicate remote.Get calls
	descriptorCache := make(map[string]*remote.Descriptor)

	allTags, err := c.getAllTagsWithCache(ctx, repository, descriptorCache)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch all tags: %w", err)
	}

	logger.V(1).Info("fetched tags from Quay", "image", repository, "repository", repository, "totalTags", len(allTags))

	tags, err := PrepareTagsForArchValidation(allTags, repository, tagPattern)
	if err != nil {
		logger.Error(err, "failed to prepare tags for arch validation", "repository", repository, "tagPattern", tagPattern, "totalTags", len(allTags))
		return nil, err
	}

	logger.V(1).Info("filtered tags by pattern", "repository", repository, "tagPattern", tagPattern, "matchingTags", len(tags))

	remoteOpts := GetRemoteOptions(c.useAuth)

	for _, tag := range tags {
		// Check if context is cancelled before processing each tag
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("operation cancelled while checking tags: %w", ctx.Err())
		default:
		}

		// Use cached descriptor if available, otherwise fetch it
		desc, ok := descriptorCache[tag.Name]
		if !ok {
			ref, err := name.ParseReference(fmt.Sprintf("quay.io/%s:%s", repository, tag.Name))
			if err != nil {
				logger.Error(err, "failed to parse reference", "tag", tag.Name)
				continue
			}

			desc, err = remote.Get(ref, remoteOpts...)
			if err != nil {
				logger.Error(err, "failed to fetch image descriptor", "tag", tag.Name)
				continue
			}
			descriptorCache[tag.Name] = desc
		}

		// If multiArch is requested, return the multi-arch manifest list digest
		if multiArch && desc.MediaType.IsIndex() {
			logger.Info("found multi-arch manifest", "image", repository, "tag", tag.Name, "mediaType", desc.MediaType, "digest", desc.Digest.String(), "date", tag.LastModified.Format("2006-01-02 15:04"))
			tag.Digest = desc.Digest.String()
			return &tag, nil
		}

		if desc.MediaType.IsIndex() {
			logger.Info("skipping multi-arch manifest", "tag", tag.Name, "mediaType", desc.MediaType)
			continue
		}

		img, err := desc.Image()
		if err != nil {
			logger.Error(err, "failed to get image", "tag", tag.Name)
			continue
		}

		configFile, err := img.ConfigFile()
		if err != nil {
			logger.Error(err, "failed to get config", "tag", tag.Name)
			continue
		}

		normalizedArch := NormalizeArchitecture(configFile.Architecture)

		if normalizedArch == arch && configFile.OS == "linux" {
			digest, err := img.Digest()
			if err != nil {
				logger.Error(err, "failed to get image digest", "tag", tag.Name)
				continue
			}
			tag.Digest = digest.String()
			logger.V(1).Info("found matching image", "image", repository, "tag", tag.Name, "arch", arch, "digest", digest.String(), "date", tag.LastModified.Format("2006-01-02 15:04"))
			return &tag, nil
		}

		logger.Info("skipping non-matching architecture", "tag", tag.Name, "arch", configFile.Architecture, "os", configFile.OS, "wantArch", arch)
	}

	if multiArch {
		return nil, fmt.Errorf("no multi-arch manifest found for repository %s", repository)
	}
	return nil, fmt.Errorf("no single-arch %s/linux image found for repository %s (all tags are either multi-arch or different architecture)", arch, repository)
}
