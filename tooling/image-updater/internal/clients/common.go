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
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
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

// NewRegistryClient creates a new registry client based on the registry URL
func NewRegistryClient(registryURL string) (RegistryClient, error) {
	switch {
	case strings.Contains(registryURL, "quay.io"):
		return NewQuayClient(), nil
	case strings.Contains(registryURL, "azurecr.io"):
		return NewACRClient(registryURL)
	default:
		return nil, fmt.Errorf("unsupported registry: %s", registryURL)
	}
}
