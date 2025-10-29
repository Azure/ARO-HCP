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

type QuayClient struct {
	httpClient *http.Client
	baseURL    string
}

func NewQuayClient() *QuayClient {
	return &QuayClient{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		baseURL: "https://quay.io/api/v1",
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

func (c *QuayClient) GetArchSpecificDigest(ctx context.Context, repository string, tagPattern string, arch string) (string, error) {
	logger := logr.FromContextOrDiscard(ctx)

	allTags, err := c.getAllTags(repository)
	if err != nil {
		return "", fmt.Errorf("failed to fetch all tags: %w", err)
	}

	tags, err := PrepareTagsForArchValidation(allTags, repository, tagPattern)
	if err != nil {
		return "", err
	}

	for _, tag := range tags {
		ref, err := name.ParseReference(fmt.Sprintf("quay.io/%s:%s", repository, tag.Name))
		if err != nil {
			logger.Error(err, "failed to parse reference", "tag", tag.Name)
			continue
		}

		desc, err := remote.Get(ref)
		if err != nil {
			logger.Error(err, "failed to fetch image descriptor", "tag", tag.Name)
			continue
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
			return digest.String(), nil
		}

		logger.Info("skipping non-matching architecture", "tag", tag.Name, "arch", configFile.Architecture, "os", configFile.OS, "wantArch", arch)
	}

	return "", fmt.Errorf("no single-arch %s/linux image found for repository %s (all tags are either multi-arch or different architecture)", arch, repository)
}
