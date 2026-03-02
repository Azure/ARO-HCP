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

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/containers/azcontainerregistry"
)

// ACRClient provides methods to interact with Azure Container Registry
type ACRClient struct {
	client      *azcontainerregistry.Client
	registryURL string
	useAuth     bool
}

// NewACRClient creates a new Azure Container Registry client
// If useAuth is true, creates an authenticated client
// If useAuth is false, creates an anonymous client
func NewACRClient(registryURL string, useAuth bool) (*ACRClient, error) {
	acr := &ACRClient{
		registryURL: registryURL,
		useAuth:     useAuth,
	}

	var client *azcontainerregistry.Client
	var err error

	if useAuth {
		cred, err := azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{RequireAzureTokenCredentials: true})
		if err != nil {
			return nil, fmt.Errorf("failed to create Azure credential: %w", err)
		}

		client, err = azcontainerregistry.NewClient("https://"+registryURL, cred, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create authenticated ACR client: %w", err)
		}
	} else {
		// Create anonymous client (no credentials)
		client, err = azcontainerregistry.NewClient("https://"+registryURL, nil, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create anonymous ACR client: %w", err)
		}
	}

	acr.client = client
	return acr, nil
}

func (c *ACRClient) getAllTags(ctx context.Context, repository string) ([]Tag, error) {
	return c.getAllTagsWithClient(ctx, repository, c.client)
}

func (c *ACRClient) getAllTagsWithClient(ctx context.Context, repository string, client *azcontainerregistry.Client) ([]Tag, error) {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("logger not found in context: %w", err)
	}
	var allTags []Tag

	pager := client.NewListTagsPager(repository, nil)

	pageCount := 0

	for pager.More() {
		// Check if context is cancelled before fetching next page
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("operation cancelled while fetching ACR tags: %w", ctx.Err())
		default:
		}

		pageCount++
		pageResp, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get ACR tags page %d for repository %s: %w", pageCount, repository, err)
		}

		logger.V(2).Info("fetched ACR tags page", "repository", repository, "page", pageCount, "tagsInPage", len(pageResp.Tags))

		for _, tagAttributes := range pageResp.Tags {
			if tagAttributes.Name == nil {
				continue
			}

			tag := Tag{
				Name: *tagAttributes.Name,
			}

			if tagAttributes.Digest != nil {
				tag.Digest = *tagAttributes.Digest
			}

			tagProps, err := client.GetTagProperties(ctx, repository, *tagAttributes.Name, nil)
			if err != nil {
				logger.V(2).Info("could not get tag properties", "tag", *tagAttributes.Name, "error", err)
				tag.LastModified = time.Time{}
			} else {
				if tagProps.Tag.CreatedOn != nil {
					tag.LastModified = *tagProps.Tag.CreatedOn
				} else {
					tag.LastModified = time.Time{}
				}
			}

			allTags = append(allTags, tag)
		}

	}

	return allTags, nil
}

func (c *ACRClient) getClient() *azcontainerregistry.Client {
	return c.client
}

func (c *ACRClient) GetArchSpecificDigest(ctx context.Context, repository string, tagPattern string, arch string, multiArch bool, versionLabel string) (*Tag, error) {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("logger not found in context: %w", err)
	}

	logger.V(2).Info("fetching tags from ACR", "registry", c.registryURL, "repository", repository)

	allTags, err := c.getAllTags(ctx, repository)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch all tags: %w", err)
	}

	logger.V(2).Info("fetched tags from ACR", "registry", c.registryURL, "repository", repository, "totalTags", len(allTags))

	tags, err := PrepareTagsForArchValidation(allTags, repository, tagPattern)
	if err != nil {
		logger.V(2).Error(err, "failed to prepare tags for arch validation", "registry", c.registryURL, "repository", repository, "tagPattern", tagPattern, "totalTags", len(allTags))
		return nil, err
	}

	logger.V(2).Info("filtered tags by pattern", "registry", c.registryURL, "repository", repository, "tagPattern", tagPattern, "matchingTags", len(tags))

	client := c.getClient()

	for _, tag := range tags {
		// Check if context is cancelled before processing each tag
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("operation cancelled while checking tags: %w", ctx.Err())
		default:
		}

		logger.V(2).Info("checking tag", "tag", tag.Name, "digest", tag.Digest)

		manifestProps, err := client.GetManifestProperties(ctx, repository, tag.Digest, nil)
		if err != nil {
			logger.V(2).Error(err, "failed to fetch manifest properties", "tag", tag.Name, "digest", tag.Digest)
			continue
		}

		if manifestProps.Manifest == nil {
			logger.V(2).Info("manifest properties has no manifest info, skipping", "tag", tag.Name)
			continue
		}

		manifest := manifestProps.Manifest

		// If multiArch is requested and this is a multi-arch manifest, return it
		if multiArch && len(manifest.RelatedArtifacts) > 0 {
			logger.V(2).Info("found multi-arch manifest", "tag", tag.Name, "relatedArtifacts", len(manifest.RelatedArtifacts), "digest", tag.Digest)
			tag.Version = extractVersionLabel(ctx, c.registryURL, repository, tag.Name, versionLabel, c.useAuth)
			return &tag, nil
		}

		if len(manifest.RelatedArtifacts) > 0 {
			logger.V(2).Info("skipping multi-arch manifest", "tag", tag.Name, "relatedArtifacts", len(manifest.RelatedArtifacts))
			continue
		}

		if manifest.Architecture == nil || manifest.OperatingSystem == nil {
			logger.V(2).Info("manifest missing architecture or OS info, skipping", "tag", tag.Name)
			continue
		}

		normalizedArch := NormalizeArchitecture(string(*manifest.Architecture))

		if normalizedArch == arch && string(*manifest.OperatingSystem) == "linux" {
			tag.Version = extractVersionLabel(ctx, c.registryURL, repository, tag.Name, versionLabel, c.useAuth)
			return &tag, nil
		}

		logger.V(2).Info("skipping non-matching architecture", "tag", tag.Name, "arch", string(*manifest.Architecture), "os", string(*manifest.OperatingSystem), "wantArch", arch)
	}

	if multiArch {
		return nil, fmt.Errorf("no multi-arch manifest found for repository %s", repository)
	}
	return nil, fmt.Errorf("no single-arch %s/linux image found for repository %s (all tags are either multi-arch or different architecture)", arch, repository)
}

// GetDigestForTag fetches the digest for a specific tag without pagination
func (c *ACRClient) GetDigestForTag(ctx context.Context, repository string, tagName string, arch string, multiArch bool, versionLabel string) (*Tag, error) {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("logger not found in context: %w", err)
	}

	logger.V(2).Info("fetching digest for specific tag", "registry", c.registryURL, "repository", repository, "tag", tagName)

	// Check if context is cancelled before processing
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("operation cancelled: %w", ctx.Err())
	default:
	}

	client := c.getClient()

	// Get tag properties to get the digest
	tagProps, err := client.GetTagProperties(ctx, repository, tagName, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get tag properties for tag %s: %w", tagName, err)
	}

	if tagProps.Tag == nil || tagProps.Tag.Digest == nil {
		return nil, fmt.Errorf("tag %s has no digest information", tagName)
	}

	tag := Tag{
		Name:   tagName,
		Digest: *tagProps.Tag.Digest,
	}

	if tagProps.Tag.CreatedOn != nil {
		tag.LastModified = *tagProps.Tag.CreatedOn
	}

	// Get manifest properties to check architecture
	manifestProps, err := client.GetManifestProperties(ctx, repository, tag.Digest, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch manifest properties for tag %s (digest: %s): %w", tagName, tag.Digest, err)
	}

	if manifestProps.Manifest == nil {
		return nil, fmt.Errorf("tag %s has no manifest information", tagName)
	}

	manifest := manifestProps.Manifest

	// If multiArch is requested, verify this is a multi-arch manifest
	if multiArch {
		if len(manifest.RelatedArtifacts) == 0 {
			return nil, fmt.Errorf("tag %s is not a multi-arch manifest", tagName)
		}
		logger.V(2).Info("found multi-arch manifest", "tag", tagName, "relatedArtifacts", len(manifest.RelatedArtifacts), "digest", tag.Digest)
		return &tag, nil
	}

	// For single-arch, verify it's not a multi-arch manifest
	if len(manifest.RelatedArtifacts) > 0 {
		return nil, fmt.Errorf("tag %s is a multi-arch manifest, but single-arch was requested (use multiArch: true)", tagName)
	}

	if manifest.Architecture == nil || manifest.OperatingSystem == nil {
		return nil, fmt.Errorf("tag %s is missing architecture or OS information", tagName)
	}

	normalizedArch := NormalizeArchitecture(string(*manifest.Architecture))

	if normalizedArch != arch || string(*manifest.OperatingSystem) != "linux" {
		return nil, fmt.Errorf("tag %s has architecture %s/%s, but %s/linux was requested", tagName, string(*manifest.Architecture), string(*manifest.OperatingSystem), arch)
	}

	tag.Version = extractVersionLabel(ctx, c.registryURL, repository, tagName, versionLabel, c.useAuth)
	logger.V(2).Info("found matching image", "tag", tagName, "arch", normalizedArch, "digest", tag.Digest)

	return &tag, nil
}
