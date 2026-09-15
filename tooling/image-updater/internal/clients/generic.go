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
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/go-logr/logr"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"golang.org/x/sync/errgroup"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
)

const maxConcurrentRepositoryMetadataRequests = 8

// GenericRegistryClient provides methods to interact with any Docker Registry HTTP API v2 compatible registry
type GenericRegistryClient struct {
	httpClient  *http.Client
	registryURL string
	useAuth     bool
	retryConfig retryConfig
}

type fetchedTagMetadata struct {
	tag        Tag
	descriptor *remote.Descriptor
}

func runMetadataWorker(label string, worker func() error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if utilruntime.ReallyCrash {
				panic(recovered)
			}
			err = fmt.Errorf("panic while enriching tag %s: %v", label, recovered)
		}
	}()
	return worker()
}

func fetchTagMetadataConcurrently(ctx context.Context, tags []Tag, fetch func(context.Context, Tag) (fetchedTagMetadata, error)) ([]fetchedTagMetadata, error) {
	results := make([]fetchedTagMetadata, len(tags))
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(maxConcurrentRepositoryMetadataRequests)
	for i, candidate := range tags {
		i, candidate := i, candidate
		group.Go(func() error {
			defer utilruntime.HandleCrashWithContext(groupCtx)
			return runMetadataWorker(candidate.Name, func() error {
				release, err := acquireMetadataRequest(groupCtx)
				if err != nil {
					return err
				}
				defer release()

				result, err := fetch(groupCtx, candidate)
				if err != nil {
					return err
				}
				results[i] = result
				return nil
			})
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}
	return results, nil
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

// getToken resolves Docker credentials and exchanges them for a bearer token.
func (c *GenericRegistryClient) getToken(ctx context.Context, repository string) (string, error) {
	ref, err := name.NewRepository(fmt.Sprintf("%s/%s", c.registryURL, repository))
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

	return token, nil
}

// getBearerToken exchanges credentials for a bearer token following the Docker Registry V2 auth spec.
// Unlike the QuayClient which hardcodes quay.io's token endpoint, this method dynamically discovers
// the token endpoint by making an unauthenticated request to /v2/ and parsing the WWW-Authenticate
// challenge header returned by the registry.
func (c *GenericRegistryClient) getBearerToken(ctx context.Context, repository string, authConfig authn.AuthConfig) (string, error) {
	// Make an unauthenticated request to discover the auth challenge.
	challengeURL := fmt.Sprintf("https://%s/v2/", c.registryURL)
	challengeReq, err := http.NewRequestWithContext(ctx, "GET", challengeURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create auth challenge request: %w", err)
	}
	challengeResp, err := c.httpClient.Do(challengeReq)
	if err != nil {
		return "", fmt.Errorf("failed to request auth challenge from %s: %w", challengeURL, err)
	}
	defer challengeResp.Body.Close()

	wwwAuth := challengeResp.Header.Get("WWW-Authenticate")
	if wwwAuth == "" {
		return "", fmt.Errorf("no WWW-Authenticate header in challenge response from %s", challengeURL)
	}

	realm, service, err := parseWwwAuthenticate(wwwAuth)
	if err != nil {
		return "", fmt.Errorf("failed to parse WWW-Authenticate header: %w", err)
	}

	// Build the token request URL using the discovered realm and service
	tokenURL := fmt.Sprintf("%s?service=%s&scope=repository:%s:pull", realm, service, repository)

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
		return "", fmt.Errorf("no credentials found in Docker config for %s", c.registryURL)
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

// parseWwwAuthenticate parses a WWW-Authenticate header value like:
//
//	Bearer realm="https://host/v2/auth",service="registry",scope="repository:repo:pull"
//
// and returns the realm and service values.
func parseWwwAuthenticate(header string) (realm, service string, err error) {
	if !strings.HasPrefix(header, "Bearer ") {
		return "", "", fmt.Errorf("unsupported WWW-Authenticate scheme: %s", header)
	}
	params := header[len("Bearer "):]

	for _, part := range strings.Split(params, ",") {
		part = strings.TrimSpace(part)
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		v = strings.Trim(v, "\"")
		switch k {
		case "realm":
			realm = v
		case "service":
			service = v
		}
	}

	if realm == "" {
		return "", "", fmt.Errorf("no realm found in WWW-Authenticate header: %s", header)
	}
	return realm, service, nil
}

// doRequestWithRetry performs an HTTP request with exponential backoff retry logic.
// It retries on temporary network errors and 5xx server errors.
// The operation can be cancelled via context (e.g., Ctrl+C).
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

// extractMetadataFromMultiArchManifest returns reliable metadata for a multi-arch image.
func extractMetadataFromMultiArchManifest(desc *remote.Descriptor, tagName string, currentTimestamp time.Time, versionLabel string) (time.Time, string, error) {
	unixEpoch := time.Unix(0, 0).UTC()
	hasTimestamp := !currentTimestamp.IsZero() && !currentTimestamp.Equal(unixEpoch)
	if hasTimestamp && versionLabel == "" {
		return currentTimestamp, "", nil
	}

	idx, err := desc.ImageIndex()
	if err != nil {
		return time.Time{}, "", fmt.Errorf("failed to read image index for tag %s: %w", tagName, err)
	}
	manifest, err := idx.IndexManifest()
	if err != nil {
		return time.Time{}, "", fmt.Errorf("failed to read image index manifest for tag %s: %w", tagName, err)
	}
	var platformDigest v1.Hash
	foundPlatformImage := false
	for i := range manifest.Manifests {
		if manifest.Manifests[i].MediaType.IsImage() {
			platformDigest = manifest.Manifests[i].Digest
			foundPlatformImage = true
			break
		}
	}
	if !foundPlatformImage {
		return time.Time{}, "", fmt.Errorf("image index for tag %s contains no platform images", tagName)
	}
	platformImg, err := idx.Image(platformDigest)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("failed to read platform image for tag %s: %w", tagName, err)
	}
	configFile, err := platformImg.ConfigFile()
	if err != nil {
		return time.Time{}, "", fmt.Errorf("failed to read platform image config for tag %s: %w", tagName, err)
	}

	timestamp := currentTimestamp
	if !hasTimestamp {
		timestamp = configFile.Created.Time
		if timestamp.IsZero() || timestamp.Equal(unixEpoch) {
			if parsedDate, ok := ParseDateFromTag(tagName); ok {
				timestamp = parsedDate
			} else {
				return time.Time{}, "", fmt.Errorf("multi-arch tag %s has no creation timestamp", tagName)
			}
		}
	}
	return timestamp, extractVersionFromConfigLabels(configFile.Config.Labels, versionLabel), nil
}

func (c *GenericRegistryClient) getAllTags(ctx context.Context, repository string) ([]Tag, error) {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("logger not found in context: %w", err)
	}

	var authHeader string
	if c.useAuth {
		token, err := c.getToken(ctx, repository)
		if err != nil {
			return nil, fmt.Errorf("failed to get authentication token: %w", err)
		}
		authHeader = fmt.Sprintf("Bearer %s", token)
	}

	var allTags []Tag
	nextURL := fmt.Sprintf("https://%s/v2/%s/tags/list", c.registryURL, repository)

	for nextURL != "" {
		req, err := http.NewRequestWithContext(ctx, "GET", nextURL, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request (url: %s): %w", nextURL, err)
		}

		if authHeader != "" {
			req.Header.Set("Authorization", authHeader)
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
			allTags = append(allTags, Tag{
				Name:         tagName,
				LastModified: time.Time{},
			})
		}

		nextURL = parseNextLink(resp.Header.Get("Link"), c.registryURL)
		resp.Body.Close()
	}

	logger.V(2).Info("fetched tags from generic registry", "registry", c.registryURL, "repository", repository, "totalTags", len(allTags))

	return allTags, nil
}

// parseNextLink extracts the next page URL from a Docker Registry V2 Link header.
// The header format is: `</v2/repo/tags/list?n=100&last=tag>; rel="next"`
func parseNextLink(linkHeader string, registryURL string) string {
	if linkHeader == "" {
		return ""
	}
	for _, part := range strings.Split(linkHeader, ",") {
		part = strings.TrimSpace(part)
		if !strings.Contains(part, `rel="next"`) {
			continue
		}
		start := strings.Index(part, "<")
		end := strings.Index(part, ">")
		if start == -1 || end == -1 || end <= start {
			continue
		}
		path := part[start+1 : end]
		if strings.HasPrefix(path, "/") {
			return fmt.Sprintf("https://%s%s", registryURL, path)
		}
		return path
	}
	return ""
}

func (c *GenericRegistryClient) GetArchSpecificDigest(ctx context.Context, repository string, tagPattern string, arch string, wantMultiArch bool, versionLabel string) (*Tag, error) {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("logger not found in context: %w", err)
	}

	logger.V(2).Info("fetching tags from generic registry", "registry", c.registryURL, "repository", repository, "useAuth", c.useAuth, "versionLabel", versionLabel)

	allTags, err := c.getAllTags(ctx, repository)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch all tags: %w", err)
	}

	// Pre-filter tags by pattern before enrichment to avoid costly remote.Get
	// calls on thousands of non-matching tags
	if tagPattern != "" {
		totalCount := len(allTags)
		filtered, err := FilterTagsByPattern(allTags, tagPattern)
		if err != nil {
			return nil, fmt.Errorf("failed to pre-filter tags: %w", err)
		}
		if len(filtered) == 0 {
			var sampleTags []string
			for i := 0; i < min(5, totalCount); i++ {
				sampleTags = append(sampleTags, allTags[i].Name)
			}
			return nil, fmt.Errorf("no tags matching pattern %s found for repository %s (sample tags from %d total: %v)", tagPattern, repository, totalCount, sampleTags)
		}
		logger.V(2).Info("pre-filtered tags by pattern", "registry", c.registryURL, "repository", repository, "tagPattern", tagPattern, "totalTags", totalCount, "matchingTags", len(filtered))
		allTags = filtered
	}

	descriptorCache := make(map[string]*remote.Descriptor, len(allTags))
	enrichedTags := allTags
	if !usesSemanticVersionOrdering(allTags, tagPattern) || hasEquivalentSemanticVersions(allTags) || versionLabel != "" {
		metadata, err := fetchTagMetadataConcurrently(ctx, allTags, func(fetchCtx context.Context, candidate Tag) (fetchedTagMetadata, error) {
			ref, err := name.ParseReference(fmt.Sprintf("%s/%s:%s", c.registryURL, repository, candidate.Name))
			if err != nil {
				return fetchedTagMetadata{}, fmt.Errorf("failed to parse reference for tag %s: %w", candidate.Name, err)
			}
			desc, err := remote.Get(ref, append(GetRemoteOptions(c.useAuth), remote.WithContext(fetchCtx))...)
			if err != nil {
				return fetchedTagMetadata{}, fmt.Errorf("failed to fetch image descriptor for tag %s: %w", candidate.Name, err)
			}

			tag := candidate
			if desc.MediaType.IsIndex() {
				tag.LastModified, tag.Version, err = extractMetadataFromMultiArchManifest(desc, tag.Name, tag.LastModified, versionLabel)
			} else {
				var img v1.Image
				img, err = desc.Image()
				if err == nil {
					var configFile *v1.ConfigFile
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
			return fetchedTagMetadata{tag: tag, descriptor: desc}, nil
		})
		if err != nil {
			return nil, fmt.Errorf("failed to enrich tag metadata: %w", err)
		}

		descriptorCache = make(map[string]*remote.Descriptor, len(metadata))
		enrichedTags = make([]Tag, 0, len(metadata))
		for _, result := range metadata {
			descriptorCache[result.tag.Name] = result.descriptor
			enrichedTags = append(enrichedTags, result.tag)
		}
	}

	tags, err := PrepareTagsForArchValidation(enrichedTags, repository, tagPattern)
	if err != nil {
		logger.V(2).Error(err, "failed to prepare tags for arch validation", "registry", c.registryURL, "repository", repository, "tagPattern", tagPattern, "totalTags", len(enrichedTags))
		return nil, err
	}

	logger.V(2).Info("filtered tags by pattern", "registry", c.registryURL, "repository", repository, "tagPattern", tagPattern, "matchingTags", len(tags))

	selectionRemoteOpts := append(GetRemoteOptions(c.useAuth), remote.WithContext(ctx))

	for _, tag := range tags {
		// Check if context is cancelled before processing each tag
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("operation cancelled while checking tags: %w", ctx.Err())
		default:
		}

		desc, ok := descriptorCache[tag.Name]
		if !ok {
			ref, err := name.ParseReference(fmt.Sprintf("%s/%s:%s", c.registryURL, repository, tag.Name))
			if err != nil {
				return nil, fmt.Errorf("failed to parse reference for candidate tag %s: %w", tag.Name, err)
			}
			desc, err = remote.Get(ref, selectionRemoteOpts...)
			if err != nil {
				return nil, fmt.Errorf("failed to fetch image descriptor for candidate tag %s: %w", tag.Name, err)
			}
		}

		isMultiArch := desc.MediaType.IsIndex()

		if wantMultiArch && isMultiArch {
			logger.V(2).Info("found multi-arch manifest", "tag", tag.Name, "mediaType", desc.MediaType, "digest", desc.Digest.String())
			tag.Digest = desc.Digest.String()
			tag.LastModified, tag.Version, err = extractMetadataFromMultiArchManifest(desc, tag.Name, tag.LastModified, versionLabel)
			if err != nil {
				return nil, err
			}
			return &tag, nil
		} else if wantMultiArch != isMultiArch {
			logger.V(2).Info("skipping manifest due to multiArch mismatch", "tag", tag.Name, "wantMultiArch", wantMultiArch, "isMultiArch", isMultiArch)
			continue
		}

		img, err := desc.Image()
		if err != nil {
			return nil, fmt.Errorf("failed to read image for candidate tag %s: %w", tag.Name, err)
		}

		configFile, err := img.ConfigFile()
		if err != nil {
			return nil, fmt.Errorf("failed to read image config for candidate tag %s: %w", tag.Name, err)
		}
		tag.LastModified, err = validateCreationTimestamp(tag.Name, configFile.Created.Time)
		if err != nil {
			return nil, err
		}
		tag.Version = extractVersionFromConfigLabels(configFile.Config.Labels, versionLabel)

		normalizedArch := NormalizeArchitecture(configFile.Architecture)

		if normalizedArch == arch && configFile.OS == "linux" {
			digest, err := img.Digest()
			if err != nil {
				return nil, fmt.Errorf("failed to read image digest for candidate tag %s: %w", tag.Name, err)
			}
			tag.Digest = digest.String()
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
func (c *GenericRegistryClient) GetDigestForTag(ctx context.Context, repository string, tagName string, arch string, wantMultiArch bool, versionLabel string) (*Tag, error) {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("logger not found in context: %w", err)
	}

	logger.V(2).Info("fetching digest for specific tag", "registry", c.registryURL, "repository", repository, "tag", tagName, "useAuth", c.useAuth, "versionLabel", versionLabel)
	remoteOpts := append(GetRemoteOptions(c.useAuth), remote.WithContext(ctx))
	ref, err := name.ParseReference(fmt.Sprintf("%s/%s:%s", c.registryURL, repository, tagName))
	if err != nil {
		return nil, fmt.Errorf("failed to parse reference for tag %s: %w", tagName, err)
	}
	desc, err := remote.Get(ref, remoteOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch image descriptor for tag %s: %w", tagName, err)
	}

	tag := Tag{Name: tagName, Digest: desc.Digest.String()}
	if wantMultiArch {
		if !desc.MediaType.IsIndex() {
			return nil, fmt.Errorf("tag %s is not a multi-arch manifest (mediaType: %s)", tagName, desc.MediaType)
		}
		tag.LastModified, tag.Version, err = extractMetadataFromMultiArchManifest(desc, tagName, tag.LastModified, versionLabel)
		if err != nil {
			return nil, err
		}
		logger.V(2).Info("found multi-arch manifest", "tag", tagName, "mediaType", desc.MediaType, "digest", desc.Digest.String())
		return &tag, nil
	}

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
	logger.V(2).Info("found matching image", "tag", tagName, "arch", normalizedArch, "digest", tag.Digest)
	return &tag, nil
}
