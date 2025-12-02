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

// QuayClient provides methods to interact with Quay.io
// Note: For private repositories, this client falls back to using the Docker Registry V2 API
// instead of Quay's proprietary API, as the latter requires different credentials
type QuayClient struct {
	httpClient     *http.Client
	baseURL        string
	useAuth        bool
	retryConfig    retryConfig
}

type retryConfig struct {
	initialInterval    time.Duration
	maxInterval        time.Duration
	maxElapsedTime     time.Duration
	multiplier         float64
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
			initialInterval:    1 * time.Second,
			maxInterval:        30 * time.Second,
			maxElapsedTime:     2 * time.Minute,
			multiplier:         2.0,
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
func (c *QuayClient) doRequestWithRetry(ctx context.Context, req *http.Request) (*http.Response, error) {
	logger := logr.FromContextOrDiscard(ctx)
	var resp *http.Response

	// Create a new backoff instance for this request
	expBackoff := backoff.NewExponentialBackOff()
	expBackoff.InitialInterval = c.retryConfig.initialInterval
	expBackoff.MaxInterval = c.retryConfig.maxInterval
	expBackoff.MaxElapsedTime = c.retryConfig.maxElapsedTime
	expBackoff.Multiplier = c.retryConfig.multiplier
	expBackoff.RandomizationFactor = c.retryConfig.randomizationFactor

	operation := func() error {
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

	// Use backoff.RetryNotify to perform the operation with retries
	if err := backoff.RetryNotify(operation, expBackoff, notify); err != nil {
		return nil, fmt.Errorf("request failed after retries: %w", err)
	}

	return resp, nil
}

func (c *QuayClient) getAllTags(ctx context.Context, repository string) ([]Tag, error) {
	// If authentication is required, use Docker Registry V2 API instead of Quay's proprietary API
	// This is because Quay's API requires different credentials (OAuth2 tokens) than registry access
	if c.useAuth {
		return c.getAllTagsViaRegistryAPI(ctx, repository)
	}

	logger := logr.FromContextOrDiscard(ctx)
	// For public repositories, use Quay's proprietary API which provides timestamps
	var allTags []Tag
	page := 1

	for {
		url := fmt.Sprintf("%s/repository/%s/tag?page=%d", c.baseURL, repository, page)

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

// getAllTagsViaRegistryAPI uses the Docker Registry V2 API to list tags
// This works with Docker credentials and is used for private repositories
// Note: Docker Registry V2 API uses Link header for pagination but Quay may return all tags in one response
func (c *QuayClient) getAllTagsViaRegistryAPI(ctx context.Context, repository string) ([]Tag, error) {
	logger := logr.FromContextOrDiscard(ctx)
	var allTags []Tag
	url := fmt.Sprintf("https://quay.io/v2/%s/tags/list", repository)

	for url != "" {
		pageCount++
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request (page %d): %w", pageCount, err)
	}

	if err := c.addAuth(req, repository); err != nil {
			return nil, fmt.Errorf("failed to add authentication (page %d): %w", pageCount, err)
	}

		resp, err := c.doRequestWithRetry(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("failed to request registry API (page %d): %w", pageCount, err)
	}

	if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("registry API returned status %d for repository %s (page %d)", resp.StatusCode, repository, pageCount)
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
			// Note: Registry V2 API doesn't provide timestamps, they'll be fetched later if needed
			LastModified: time.Time{},
		}
		resp.Body.Close()

		for _, tagName := range tagsResp.Tags {
			tag := Tag{
				Name: tagName,
				// Note: Registry V2 API doesn't provide timestamps, they'll be fetched later if needed
				LastModified: time.Time{},
			}
			allTags = append(allTags, tag)
		}

		// If no Link header or couldn't parse next URL, we're done
		if nextURL != "" {
			logger.V(1).Info("fetching next page via registry API", "page", pageCount+1, "repository", repository)
		}
		url = nextURL
	}

	return allTags, nil
}

func (c *QuayClient) GetArchSpecificDigest(ctx context.Context, repository string, tagPattern string, arch string, multiArch bool) (*Tag, error) {
	logger := logr.FromContextOrDiscard(ctx)

	logger.V(1).Info("fetching tags from Quay", "repository", repository, "useAuth", c.useAuth)

	allTags, err := c.getAllTags(ctx, repository)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch all tags: %w", err)
	}

	logger.V(1).Info("fetched tags from Quay", "repository", repository, "totalTags", len(allTags))

	tags, err := PrepareTagsForArchValidation(allTags, repository, tagPattern)
	if err != nil {
		logger.Error(err, "failed to prepare tags for arch validation", "repository", repository, "tagPattern", tagPattern, "totalTags", len(allTags))
		return nil, err
	}

	logger.V(1).Info("filtered tags by pattern", "repository", repository, "tagPattern", tagPattern, "matchingTags", len(tags))

	remoteOpts := GetRemoteOptions(c.useAuth)

	for _, tag := range tags {
		ref, err := name.ParseReference(fmt.Sprintf("quay.io/%s:%s", repository, tag.Name))
		if err != nil {
			logger.Error(err, "failed to parse reference", "tag", tag.Name)
			continue
		}

		desc, err := remote.Get(ref, remoteOpts...)
		if err != nil {
			logger.Error(err, "failed to fetch image descriptor", "tag", tag.Name)
			continue
		}

		// If multiArch is requested, return the multi-arch manifest list digest
		if multiArch && desc.MediaType.IsIndex() {
			logger.Info("found multi-arch manifest", "tag", tag.Name, "mediaType", desc.MediaType, "digest", desc.Digest.String())
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
			return &tag, nil
		}

		logger.Info("skipping non-matching architecture", "tag", tag.Name, "arch", configFile.Architecture, "os", configFile.OS, "wantArch", arch)
	}

	if multiArch {
		return nil, fmt.Errorf("no multi-arch manifest found for repository %s", repository)
	}
	return nil, fmt.Errorf("no single-arch %s/linux image found for repository %s (all tags are either multi-arch or different architecture)", arch, repository)
}
