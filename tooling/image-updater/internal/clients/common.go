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
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// FilterTagsByPattern filters tags by a regex pattern
func FilterTagsByPattern(tags []Tag, tagPattern string) ([]Tag, error) {
	if tagPattern == "" {
		return tags, nil
	}

	regex, err := regexp.Compile(tagPattern)
	if err != nil {
		return nil, fmt.Errorf("invalid tag pattern %s: %w", tagPattern, err)
	}

	var matchingTags []Tag
	for _, tag := range tags {
		if regex.MatchString(tag.Name) {
			matchingTags = append(matchingTags, tag)
		}
	}

	return matchingTags, nil
}

// ParseTimestamp attempts to parse a timestamp string using the Quay.io format
func ParseTimestamp(timestampStr string) (time.Time, error) {
	if t, err := time.Parse(time.RFC1123Z, timestampStr); err == nil {
		return t, nil
	}

	return time.Time{}, fmt.Errorf("unable to parse timestamp: %s", timestampStr)
}

// NormalizeArchitecture converts x86_64 to amd64 for consistent comparison
func NormalizeArchitecture(arch string) string {
	if arch == "x86_64" {
		return "amd64"
	}
	return arch
}

// PrepareTagsForArchValidation filters and sorts tags for architecture validation
func PrepareTagsForArchValidation(tags []Tag, repository string, tagPattern string) ([]Tag, error) {
	if len(tags) == 0 {
		return nil, fmt.Errorf("no tags found for repository %s", repository)
	}

	if tagPattern != "" {
		filteredTags, err := FilterTagsByPattern(tags, tagPattern)
		if err != nil {
			return nil, err
		}
		tags = filteredTags
	}

	if len(tags) == 0 {
		if tagPattern != "" {
			return nil, fmt.Errorf("no tags matching pattern %s found for repository %s", tagPattern, repository)
		}
		return nil, fmt.Errorf("no valid tags found for repository %s", repository)
	}

	sort.Slice(tags, func(i, j int) bool {
		return tags[i].LastModified.After(tags[j].LastModified)
	})

	return tags, nil
}

// validateArchSpecificDigestWithGoContainerRegistry contains the common logic for validating
// architecture-specific digests using the go-containerregistry library.
// This is shared between QuayClient and GenericRegistryClient.
func validateArchSpecificDigestWithGoContainerRegistry(ctx context.Context, registryURL, repository, tagPattern, arch string, multiArch bool, tags []Tag) (string, error) {
	logger := logr.FromContextOrDiscard(ctx)

	preparedTags, err := PrepareTagsForArchValidation(tags, repository, tagPattern)
	if err != nil {
		return "", err
	}

	for _, tag := range preparedTags {
		ref, err := name.ParseReference(fmt.Sprintf("%s/%s:%s", registryURL, repository, tag.Name))
		if err != nil {
			logger.Error(err, "failed to parse reference", "tag", tag.Name)
			continue
		}

		desc, err := remote.Get(ref)
		if err != nil {
			logger.Error(err, "failed to fetch image descriptor", "tag", tag.Name)
			continue
		}

		// If multiArch is requested, return the multi-arch manifest list digest
		if multiArch && desc.MediaType.IsIndex() {
			logger.Info("found multi-arch manifest", "tag", tag.Name, "mediaType", desc.MediaType, "digest", desc.Digest.String())
			return desc.Digest.String(), nil
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

	if multiArch {
		return "", fmt.Errorf("no multi-arch manifest found for repository %s", repository)
	}
	return "", fmt.Errorf("no single-arch %s/linux image found for repository %s (all tags are either multi-arch or different architecture)", arch, repository)
}

// NewRegistryClient creates a new registry client based on the registry URL
func NewRegistryClient(registryURL string, useAuth bool) (RegistryClient, error) {
	switch {
	case strings.Contains(registryURL, "quay.io"):
		// Quay has a proprietary API with better tag discovery
		return NewQuayClient(), nil
	case strings.Contains(registryURL, "azurecr.io"):
		return NewACRClient(registryURL, useAuth)
	default:
		// Use generic client for other Docker Registry v2 compatible registries (mcr.microsoft.com, etc.)
		return NewGenericRegistryClient(registryURL), nil
	}
}
