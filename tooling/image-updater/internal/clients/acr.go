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
	"strings"
	"time"

	"github.com/go-logr/logr"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/containers/azcontainerregistry"
)

// ACRClient provides methods to interact with Azure Container Registry
type ACRClient struct {
	client          *azcontainerregistry.Client
	anonymousClient *azcontainerregistry.Client
	registryURL     string
	useAnonymous    bool // Track if we've fallen back to anonymous
}

// NewACRClient creates a new Azure Container Registry client
// If useAuth is false, only anonymous client is created
// If useAuth is true, both authenticated and anonymous clients are created for fallback
func NewACRClient(registryURL string, useAuth bool) (*ACRClient, error) {
	acr := &ACRClient{
		registryURL: registryURL,
	}

	// Only try to create authenticated client if useAuth is true
	if useAuth {
		cred, err := azidentity.NewDefaultAzureCredential(nil)
		if err == nil {
			client, err := azcontainerregistry.NewClient("https://"+registryURL, cred, nil)
			if err == nil {
				acr.client = client
			}
		}
	} else {
		// If authentication is disabled, use anonymous from the start
		acr.useAnonymous = true
	}

	// Always create an anonymous client as fallback (or primary if useAuth=false)
	anonymousClient, err := azcontainerregistry.NewClient("https://"+registryURL, nil, nil)
	if err != nil {
		if acr.client == nil {
			return nil, fmt.Errorf("failed to create ACR client (neither authenticated nor anonymous worked): %w", err)
		}
		// If we have an authenticated client, we can continue without anonymous fallback
	} else {
		acr.anonymousClient = anonymousClient
	}

	// If neither client was created, fail
	if acr.client == nil && acr.anonymousClient == nil {
		return nil, fmt.Errorf("failed to create any ACR client for %s", registryURL)
	}

	return acr, nil
}

func (c *ACRClient) getAllTags(ctx context.Context, repository string) ([]Tag, error) {
	// Try authenticated client first if available and not already failed
	if c.client != nil && !c.useAnonymous {
		tags, err := c.getAllTagsWithClient(ctx, repository, c.client)
		if err == nil {
			return tags, nil
		}
		// Check if error is auth-related
		if isAuthError(err) && c.anonymousClient != nil {
			fmt.Printf("  Authentication failed for %s, retrying with anonymous access...\n", c.registryURL)
			c.useAnonymous = true // Switch to anonymous for all future calls
			return c.getAllTagsWithClient(ctx, repository, c.anonymousClient)
		}
		return nil, err
	}

	// Fall back to anonymous client
	if c.anonymousClient != nil {
		return c.getAllTagsWithClient(ctx, repository, c.anonymousClient)
	}

	return nil, fmt.Errorf("no ACR client available")
}

func (c *ACRClient) getAllTagsWithClient(ctx context.Context, repository string, client *azcontainerregistry.Client) ([]Tag, error) {
	var allTags []Tag

	pager := client.NewListTagsPager(repository, nil)

	pageCount := 0

	for pager.More() {
		pageCount++
		pageResp, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get ACR tags page %d: %w", pageCount, err)
		}

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
				fmt.Printf("  Warning: Could not get tag properties for %s: %v\n", *tagAttributes.Name, err)
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

func isAuthError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "UNAUTHORIZED") ||
		strings.Contains(errStr, "401") ||
		strings.Contains(errStr, "token validation failed") ||
		strings.Contains(errStr, "unknown tenantId")
}

func (c *ACRClient) getClient() *azcontainerregistry.Client {
	if c.useAnonymous && c.anonymousClient != nil {
		return c.anonymousClient
	}
	if c.client != nil {
		return c.client
	}
	return c.anonymousClient
}

func (c *ACRClient) GetArchSpecificDigest(ctx context.Context, repository string, tagPattern string, arch string) (string, error) {
	logger := logr.FromContextOrDiscard(ctx)

	allTags, err := c.getAllTags(ctx, repository)
	if err != nil {
		return "", fmt.Errorf("failed to fetch all tags: %w", err)
	}

	tags, err := PrepareTagsForArchValidation(allTags, repository, tagPattern)
	if err != nil {
		return "", err
	}

	client := c.getClient()

	for _, tag := range tags {
		logger.Info("checking tag", "tag", tag.Name, "digest", tag.Digest)

		manifestProps, err := client.GetManifestProperties(ctx, repository, tag.Digest, nil)
		if err != nil {
			logger.Error(err, "failed to fetch manifest properties", "tag", tag.Name, "digest", tag.Digest)
			continue
		}

		if manifestProps.Manifest == nil {
			logger.Info("manifest properties has no manifest info, skipping", "tag", tag.Name)
			continue
		}

		manifest := manifestProps.Manifest

		if len(manifest.RelatedArtifacts) > 0 {
			logger.Info("skipping multi-arch manifest", "tag", tag.Name, "relatedArtifacts", len(manifest.RelatedArtifacts))
			continue
		}

		if manifest.Architecture == nil || manifest.OperatingSystem == nil {
			logger.Info("manifest missing architecture or OS info, skipping", "tag", tag.Name)
			continue
		}

		normalizedArch := NormalizeArchitecture(string(*manifest.Architecture))

		if normalizedArch == arch && string(*manifest.OperatingSystem) == "linux" {
			return tag.Digest, nil
		}

		logger.Info("skipping non-matching architecture", "tag", tag.Name, "arch", string(*manifest.Architecture), "os", string(*manifest.OperatingSystem), "wantArch", arch)
	}

	return "", fmt.Errorf("no single-arch %s/linux image found for repository %s (all tags are either multi-arch or different architecture)", arch, repository)
}
