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
	"strings"
	"time"
)

// RegistryClient defines the interface for container registry clients
type RegistryClient interface {
	GetLatestDigest(repository string, tagPattern string) (string, error)
}

// Tag represents a container image tag with metadata
type Tag struct {
	Name         string
	Digest       string
	LastModified time.Time
	Architecture string
}

// Manifest represents a container image manifest
type Manifest struct {
	MediaType string `json:"mediaType"`
	Config    struct {
		MediaType string `json:"mediaType"`
		Digest    string `json:"digest"`
	} `json:"config"`
	Architecture string `json:"architecture"`
}

// IsValidArchitecture checks if the architecture is amd64 or x86_64
func IsValidArchitecture(arch string) bool {
	return arch == "amd64" || arch == "x86_64"
}

// IsContainerImageTag checks if a tag name represents a container image
// and not metadata artifacts like signatures, attestations, or SBOMs
func IsContainerImageTag(tagName string) bool {
	// Skip tags that are clearly metadata artifacts
	metadataExtensions := []string{
		".sig",  // Signatures
		".att",  // Attestations
		".sbom", // Software Bill of Materials
		".prov", // Provenance
		".vuln", // Vulnerability scan results
	}

	tagLower := strings.ToLower(tagName)
	for _, ext := range metadataExtensions {
		if strings.HasSuffix(tagLower, ext) {
			return false
		}
	}

	// Skip tags that look like SHA-based metadata (e.g., "sha256-abc123.att")
	if strings.HasPrefix(tagLower, "sha256-") && strings.Contains(tagLower, ".") {
		return false
	}

	return true
}
