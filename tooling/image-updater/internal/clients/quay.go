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
	"regexp"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/go-logr/logr"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
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

// getAuthorizationHeader resolves Docker credentials and exchanges them for a bearer token.
func (c *QuayClient) getAuthorizationHeader(ctx context.Context, repository string) (string, error) {
	ref, err := name.NewRepository(fmt.Sprintf("quay.io/%s", repository))
	if err != nil {
		return "", fmt.Errorf("failed to parse repository: %w", err)
	}
	authenticator, err := authn.DefaultKeychain.Resolve(ref.Registry)
	if err != nil {
		return "", fmt.Errorf("failed to resolve authenticator: %w", err)
	}
	authConfig, err := authenticator.Authorization()
	if err != nil {
		return "", fmt.Errorf("failed to get authorization: %w", err)
	}
	token, err := c.getBearerToken(ctx, repository, *authConfig)
	if err != nil {
		return "", fmt.Errorf("failed to get bearer token: %w", err)
	}
	return fmt.Sprintf("Bearer %s", token), nil
}

// getBearerToken exchanges credentials for a bearer token following the Docker Registry V2 auth spec
func (c *QuayClient) getBearerToken(ctx context.Context, repository string, authConfig authn.AuthConfig) (string, error) {
	// The auth endpoint for Quay.io
	tokenURL := fmt.Sprintf("https://quay.io/v2/auth?service=quay.io&scope=repository:%s:pull", repository)

	tokenReq, err := http.NewRequestWithContext(ctx, "GET", tokenURL, nil)
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

func (c *QuayClient) getAllTags(ctx context.Context, repository, tagPattern string) ([]Tag, error) {
	// If authentication is required, use Docker Registry V2 API instead of Quay's proprietary API.
	if c.useAuth {
		return c.getAllTagsViaRegistryAPI(ctx, repository)
	}

	var pattern *regexp.Regexp
	if tagPattern != "" {
		var err error
		pattern, err = regexp.Compile(tagPattern)
		if err != nil {
			return nil, fmt.Errorf("invalid tag pattern %s: %w", tagPattern, err)
		}
	}

	logger, err := logr.FromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("logger not found in context: %w", err)
	}
	// For public repositories, use Quay's proprietary API which provides timestamps.
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

		logger.V(2).Info("fetched page", "page", page, "repository", repository, "tags", len(tagsResp.Tags))

		for _, quayTag := range tagsResp.Tags {
			timestamp, err := ParseTimestamp(quayTag.LastModified)
			isCandidate := pattern == nil || pattern.MatchString(quayTag.Name)
			if err != nil && isCandidate {
				return nil, fmt.Errorf("failed to parse timestamp for candidate tag %s: %w", quayTag.Name, err)
			}
			if isCandidate {
				timestamp, err = validateCreationTimestamp(quayTag.Name, timestamp)
				if err != nil {
					return nil, err
				}
			}

			allTags = append(allTags, Tag{
				Name:         quayTag.Name,
				Digest:       quayTag.ManifestDigest,
				LastModified: timestamp,
			})
		}

		if !tagsResp.HasAdditional {
			break
		}

		page++
		logger.V(2).Info("fetching next page", "page", page, "repository", repository)
	}

	return allTags, nil
}

func (c *QuayClient) getAllTagsViaRegistryAPI(ctx context.Context, repository string) ([]Tag, error) {
	var authorizationHeader string
	if c.useAuth {
		var err error
		authorizationHeader, err = c.getAuthorizationHeader(ctx, repository)
		if err != nil {
			return nil, err
		}
	}

	var allTags []Tag
	nextURL := fmt.Sprintf("https://quay.io/v2/%s/tags/list", repository)
	for nextURL != "" {
		req, err := http.NewRequestWithContext(ctx, "GET", nextURL, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request (url: %s): %w", nextURL, err)
		}
		if authorizationHeader != "" {
			req.Header.Set("Authorization", authorizationHeader)
		}

		resp, err := c.doRequestWithRetry(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("failed to request registry API (url: %s): %w", nextURL, err)
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("registry API returned status %d for repository %s (url: %s)", resp.StatusCode, repository, nextURL)
		}

		var tagsResp dockerRegistryTagsResponse
		if err := json.NewDecoder(resp.Body).Decode(&tagsResp); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("failed to decode registry API response (url: %s): %w", nextURL, err)
		}
		for _, tagName := range tagsResp.Tags {
			allTags = append(allTags, Tag{Name: tagName})
		}
		nextURL = parseNextLink(resp.Header.Get("Link"), "quay.io")
		resp.Body.Close()
	}
	return allTags, nil
}

func (c *QuayClient) enrichTagsViaRegistryAPI(ctx context.Context, repository string, tags []Tag, versionLabel string) ([]Tag, map[string]*remote.Descriptor, map[string]*v1.ConfigFile, error) {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("logger not found in context: %w", err)
	}

	logger.V(2).Info("enriching tags with timestamp information", "repository", repository, "totalTags", len(tags))
	metadata, err := fetchTagMetadataConcurrently(ctx, tags, func(fetchCtx context.Context, candidate Tag) (fetchedTagMetadata, error) {
		ref, err := name.ParseReference(fmt.Sprintf("quay.io/%s:%s", repository, candidate.Name))
		if err != nil {
			return fetchedTagMetadata{}, fmt.Errorf("failed to parse reference for tag %s: %w", candidate.Name, err)
		}
		desc, err := remote.Get(ref, append(GetRemoteOptions(c.useAuth), remote.WithContext(fetchCtx))...)
		if err != nil {
			return fetchedTagMetadata{}, fmt.Errorf("failed to fetch image descriptor for tag %s: %w", candidate.Name, err)
		}

		tag := candidate
		var configFile *v1.ConfigFile
		if desc.MediaType.IsIndex() {
			tag.LastModified, tag.Version, err = extractMetadataFromMultiArchManifest(desc, tag.Name, tag.LastModified, versionLabel)
		} else {
			var img v1.Image
			img, err = desc.Image()
			if err == nil {
				configFile, err = img.ConfigFile()
				if err == nil {
					tag.LastModified, err = validateCreationTimestamp(tag.Name, configFile.Created.Time)
					tag.Version = extractVersionFromConfigLabels(configFile.Config.Labels, versionLabel)
				}
			}
		}
		if err != nil {
			return fetchedTagMetadata{}, fmt.Errorf("failed to enrich metadata for tag %s: %w", tag.Name, err)
		}
		tag.Digest = desc.Digest.String()
		return fetchedTagMetadata{tag: tag, descriptor: desc, configFile: configFile}, nil
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to enrich tag metadata: %w", err)
	}

	enrichedTags := make([]Tag, 0, len(metadata))
	descriptorCache := make(map[string]*remote.Descriptor, len(metadata))
	configFileCache := make(map[string]*v1.ConfigFile, len(metadata))
	for _, result := range metadata {
		enrichedTags = append(enrichedTags, result.tag)
		descriptorCache[result.tag.Name] = result.descriptor
		if result.configFile != nil {
			configFileCache[result.tag.Name] = result.configFile
		}
	}
	logger.V(2).Info("enriched tags with timestamp information", "repository", repository, "enrichedTags", len(enrichedTags))
	return enrichedTags, descriptorCache, configFileCache, nil
}

func (c *QuayClient) GetArchSpecificDigest(ctx context.Context, repository string, tagPattern string, arch string, wantMultiArch bool, versionLabel string) (*Tag, error) {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("logger not found in context: %w", err)
	}

	logger.V(2).Info("fetching tags from Quay", "image", repository, "repository", repository, "useAuth", c.useAuth)

	// Cache for remote descriptors to avoid duplicate remote.Get calls
	descriptorCache := make(map[string]*remote.Descriptor)
	configFileCache := make(map[string]*v1.ConfigFile)

	allTags, err := c.getAllTags(ctx, repository, tagPattern)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch all tags: %w", err)
	}
	if c.useAuth {
		candidates := allTags
		if tagPattern != "" {
			candidates, err = FilterTagsByPattern(allTags, tagPattern)
			if err != nil {
				return nil, fmt.Errorf("failed to pre-filter tags: %w", err)
			}
			if len(candidates) == 0 {
				_, err := PrepareTagsForArchValidation(allTags, repository, tagPattern)
				return nil, err
			}
			logger.V(2).Info("pre-filtered tags by pattern", "repository", repository, "tagPattern", tagPattern, "totalTags", len(allTags), "matchingTags", len(candidates))
		}

		allTags, descriptorCache, configFileCache, err = c.enrichTagsViaRegistryAPI(ctx, repository, candidates, versionLabel)
		if err != nil {
			return nil, err
		}
	}

	logger.V(2).Info("fetched tags from Quay", "image", repository, "repository", repository, "totalTags", len(allTags))

	tags, err := PrepareTagsForArchValidation(allTags, repository, tagPattern)
	if err != nil {
		logger.V(2).Error(err, "failed to prepare tags for arch validation", "repository", repository, "tagPattern", tagPattern, "totalTags", len(allTags))
		return nil, err
	}

	logger.V(2).Info("filtered tags by pattern", "repository", repository, "tagPattern", tagPattern, "matchingTags", len(tags))

	remoteOpts := append(GetRemoteOptions(c.useAuth), remote.WithContext(ctx))

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
				return nil, fmt.Errorf("failed to parse reference for candidate tag %s: %w", tag.Name, err)
			}

			desc, err = remote.Get(ref, remoteOpts...)
			if err != nil {
				return nil, fmt.Errorf("failed to fetch image descriptor for candidate tag %s: %w", tag.Name, err)
			}
			descriptorCache[tag.Name] = desc
		}

		isMultiArch := desc.MediaType.IsIndex()

		if wantMultiArch && isMultiArch {
			logger.V(2).Info("found multi-arch manifest", "image", repository, "tag", tag.Name, "mediaType", desc.MediaType, "digest", desc.Digest.String(), "date", tag.LastModified.Format("2006-01-02 15:04"))
			tag.Digest = desc.Digest.String()
			return &tag, nil
		} else if wantMultiArch != isMultiArch {
			logger.V(2).Info("skipping manifest due to multiArch mismatch", "tag", tag.Name, "wantMultiArch", wantMultiArch, "isMultiArch", isMultiArch)
			continue
		}

		img, err := desc.Image()
		if err != nil {
			return nil, fmt.Errorf("failed to read image for candidate tag %s: %w", tag.Name, err)
		}

		// Prefer a configFile fetched during concurrent enrichment: cached
		// descriptors carry the errgroup's fetchCtx, which is already
		// canceled by the time we get here (group.Wait() has returned), so
		// calling img.ConfigFile() again on a cache hit would fail.
		configFile, ok := configFileCache[tag.Name]
		if !ok {
			configFile, err = img.ConfigFile()
			if err != nil {
				return nil, fmt.Errorf("failed to read image config for candidate tag %s: %w", tag.Name, err)
			}
		}

		normalizedArch := NormalizeArchitecture(configFile.Architecture)

		if normalizedArch == arch && configFile.OS == "linux" {
			digest, err := img.Digest()
			if err != nil {
				return nil, fmt.Errorf("failed to read image digest for candidate tag %s: %w", tag.Name, err)
			}
			tag.Digest = digest.String()
			logger.V(2).Info("found matching image", "image", repository, "tag", tag.Name, "arch", arch, "digest", digest.String(), "date", tag.LastModified.Format("2006-01-02 15:04"))
			return &tag, nil
		}

		logger.V(2).Info("skipping non-matching architecture", "tag", tag.Name, "arch", configFile.Architecture, "os", configFile.OS, "wantArch", arch)
	}

	if wantMultiArch {
		return nil, fmt.Errorf("no multi-arch manifest found for repository %s", repository)
	}
	return nil, fmt.Errorf("no single-arch %s/linux image found for repository %s (all tags are either multi-arch or different architecture)", arch, repository)
}

// GetDigestForTag fetches the digest for a specific tag without pagination
func (c *QuayClient) GetDigestForTag(ctx context.Context, repository string, tagName string, arch string, wantMultiArch bool, versionLabel string) (*Tag, error) {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("logger not found in context: %w", err)
	}

	logger.V(2).Info("fetching digest for specific tag", "repository", repository, "tag", tagName, "useAuth", c.useAuth, "versionLabel", versionLabel)

	// Check if context is cancelled before processing
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("operation cancelled: %w", ctx.Err())
	default:
	}

	remoteOpts := append(GetRemoteOptions(c.useAuth), remote.WithContext(ctx))
	ref, err := name.ParseReference(fmt.Sprintf("quay.io/%s:%s", repository, tagName))
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

	if wantMultiArch {
		if !desc.MediaType.IsIndex() {
			return nil, fmt.Errorf("tag %s is not a multi-arch manifest (mediaType: %s)", tagName, desc.MediaType)
		}
		tag.LastModified, tag.Version, err = extractMetadataFromMultiArchManifest(desc, tagName, tag.LastModified, versionLabel)
		if err != nil {
			return nil, err
		}
		logger.V(2).Info("found multi-arch manifest", "repository", repository, "tag", tagName, "mediaType", desc.MediaType, "digest", desc.Digest.String())
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
	tag.LastModified, err = validateCreationTimestamp(tagName, configFile.Created.Time)
	if err != nil {
		return nil, err
	}
	tag.Version = extractVersionFromConfigLabels(configFile.Config.Labels, versionLabel)

	normalizedArch := NormalizeArchitecture(configFile.Architecture)

	if normalizedArch != arch || configFile.OS != "linux" {
		return nil, fmt.Errorf("tag %s has architecture %s/%s, but %s/linux was requested", tagName, configFile.Architecture, configFile.OS, arch)
	}

	digest, err := img.Digest()
	if err != nil {
		return nil, fmt.Errorf("failed to get image digest for tag %s: %w", tagName, err)
	}

	tag.Digest = digest.String()
	logger.V(2).Info("found matching image", "repository", repository, "tag", tagName, "arch", normalizedArch, "digest", tag.Digest)

	return &tag, nil
}
