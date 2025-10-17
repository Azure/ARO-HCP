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

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/containers/azcontainerregistry"
)

// ACRClient provides methods to interact with Azure Container Registry
type ACRClient struct {
	client *azcontainerregistry.Client
}

// NewACRClient creates a new Azure Container Registry client
func NewACRClient(registryURL string) (*ACRClient, error) {
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure credential: %w", err)
	}

	client, err := azcontainerregistry.NewClient("https://"+registryURL, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create ACR client: %w", err)
	}

	return &ACRClient{
		client: client,
	}, nil
}

// GetLatestDigest gets the most recent tag's digest from ACR, optionally filtering by tag pattern
func (c *ACRClient) GetLatestDigest(repository string, tagPattern string) (string, error) {
	ctx := context.Background()

	tags, err := c.getAllTags(ctx, repository)
	if err != nil {
		return "", fmt.Errorf("failed to fetch all tags: %w", err)
	}

	if len(tags) == 0 {
		return "", fmt.Errorf("no tags found for repository %s", repository)
	}

	return ProcessTags(tags, repository, tagPattern)
}

// getAllTags fetches all tags from all pages for the specified repository
func (c *ACRClient) getAllTags(ctx context.Context, repository string) ([]Tag, error) {
	var allTags []Tag

	pager := c.client.NewListTagsPager(repository, nil)

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

			tagProps, err := c.client.GetTagProperties(ctx, repository, *tagAttributes.Name, nil)
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
