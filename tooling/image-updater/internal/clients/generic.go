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

	"github.com/go-logr/logr"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// GenericRegistryClient provides methods to interact with any Docker Registry HTTP API v2 compatible registry
type GenericRegistryClient struct {
	httpClient  *http.Client
	registryURL string
	useAuth     bool
}

// NewGenericRegistryClient creates a new generic registry client
func NewGenericRegistryClient(registryURL string, useAuth bool) *GenericRegistryClient {
	return &GenericRegistryClient{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		registryURL: registryURL,
		useAuth:     useAuth,
	}
}

type dockerRegistryTagsResponse struct {
	Name string   `json:"name"`
	Tags []string `json:"tags"`
}

func (c *GenericRegistryClient) getAllTags(repository string) ([]Tag, error) {
	// Use Docker Registry HTTP API v2 for listing tags
	url := fmt.Sprintf("https://%s/v2/%s/tags/list", c.registryURL, repository)

	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to request registry API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registry API returned status %d for repository %s", resp.StatusCode, repository)
	}

	var tagsResp dockerRegistryTagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&tagsResp); err != nil {
		return nil, fmt.Errorf("failed to decode registry API response: %w", err)
	}

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

func (c *GenericRegistryClient) GetArchSpecificDigest(ctx context.Context, repository string, tagPattern string, arch string, multiArch bool) (*Tag, error) {
	logger := logr.FromContextOrDiscard(ctx)

	allTags, err := c.getAllTags(repository)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch all tags: %w", err)
	}

	remoteOpts := GetRemoteOptions(c.useAuth)

	// Enrich tags with digest and timestamp information before filtering
	var enrichedTags []Tag
	for _, tag := range allTags {
		ref, err := name.ParseReference(fmt.Sprintf("%s/%s:%s", c.registryURL, repository, tag.Name))
		if err != nil {
			logger.Info("failed to parse reference, skipping", "tag", tag.Name, "error", err)
			continue
		}

		desc, err := remote.Get(ref, remoteOpts...)
		if err != nil {
			logger.Info("failed to fetch image descriptor, skipping", "tag", tag.Name, "error", err)
			continue
		}

		// Try to get creation time from config
		if img, err := desc.Image(); err == nil {
			if configFile, err := img.ConfigFile(); err == nil {
				tag.LastModified = configFile.Created.Time
			}
		}

		tag.Digest = desc.Digest.String()
		enrichedTags = append(enrichedTags, tag)
	}

	tags, err := PrepareTagsForArchValidation(enrichedTags, repository, tagPattern)
	if err != nil {
		return nil, err
	}

	for _, tag := range tags {
		ref, err := name.ParseReference(fmt.Sprintf("%s/%s:%s", c.registryURL, repository, tag.Name))
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
