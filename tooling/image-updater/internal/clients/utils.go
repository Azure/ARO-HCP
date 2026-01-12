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
	"time"

	"github.com/go-logr/logr"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// RegistryClient defines the interface for container registry clients
type RegistryClient interface {
	// GetArchSpecificDigest fetches the latest digest matching a tag pattern
	// Requires pagination through all tags to find the latest match
	GetArchSpecificDigest(ctx context.Context, repository string, tagPattern string, arch string, multiArch bool, versionLabel string) (*Tag, error)

	// GetDigestForTag fetches the digest for a specific tag name
	// More efficient as it doesn't require pagination
	GetDigestForTag(ctx context.Context, repository string, tag string, arch string, multiArch bool, versionLabel string) (*Tag, error)
}

type Tag struct {
	Name         string
	Digest       string
	LastModified time.Time
	Version      string // Human-friendly version extracted from container label (if configured)
}

// extractVersionLabel extracts a version string from container image labels.
// This function makes a network call to fetch the image config, so it should only be used
// when the config is not already available (e.g., ACR client).
//
// Returns the version string if found, empty string otherwise.
// Errors are logged but do not fail the operation - version extraction is optional.
func extractVersionLabel(ctx context.Context, registryURL, repository, tagName, versionLabel string, useAuth bool) string {
	if versionLabel == "" {
		return ""
	}

	// Check context cancellation before making network calls
	select {
	case <-ctx.Done():
		return ""
	default:
	}

	logger, err := logr.FromContext(ctx)
	if err != nil {
		return ""
	}

	ref, err := name.ParseReference(fmt.Sprintf("%s/%s:%s", registryURL, repository, tagName))
	if err != nil {
		// Parsing errors are unexpected and should be logged
		logger.V(2).Info("failed to parse reference for version label extraction", "tag", tagName, "error", err)
		return ""
	}

	remoteOpts := GetRemoteOptions(useAuth)
	desc, err := remote.Get(ref, remoteOpts...)
	if err != nil {
		// Network errors during version extraction are logged but don't fail the operation
		logger.V(2).Info("failed to fetch descriptor for version label extraction", "tag", tagName, "error", err)
		return ""
	}

	img, err := desc.Image()
	if err != nil {
		logger.V(2).Info("failed to get image for version label extraction", "tag", tagName, "error", err)
		return ""
	}

	configFile, err := img.ConfigFile()
	if err != nil {
		logger.V(2).Info("failed to get config file for version label extraction", "tag", tagName, "error", err)
		return ""
	}

	if configFile.Config.Labels == nil {
		// No labels is expected, don't log
		return ""
	}

	version, ok := configFile.Config.Labels[versionLabel]
	if !ok {
		// Label not found is expected (not all images have the label), don't log
		return ""
	}

	logger.V(2).Info("extracted version from label", "tag", tagName, "label", versionLabel, "version", version)
	return version
}

// extractVersionFromConfigLabels extracts a version string from image config labels.
// This is an inline helper for use when the config labels are already available (e.g., during
// tag enrichment in Generic and Quay clients). No network calls are made.
//
// Returns the version string if found, empty string otherwise.
func extractVersionFromConfigLabels(configLabels map[string]string, versionLabel string) string {
	if versionLabel == "" || configLabels == nil {
		return ""
	}

	version := configLabels[versionLabel]
	return version
}
