// Copyright 2026 Microsoft Corporation
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

package arm

import (
	"context"
	"fmt"
	"strings"
	"time"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armlocks"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

func ListByType(
	ctx context.Context,
	client *armresources.Client,
	resourceGroupName string,
	resourceType string,
) ([]*armresources.GenericResourceExpanded, error) {
	filter := fmt.Sprintf("resourceType eq '%s'", resourceType)
	pager := client.NewListByResourceGroupPager(resourceGroupName, &armresources.ClientListByResourceGroupOptions{
		Filter: &filter,
	})

	var resources []*armresources.GenericResourceExpanded
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list resources of type %s: %w", resourceType, err)
		}
		resources = append(resources, page.Value...)
	}
	return resources, nil
}

func HasLocks(ctx context.Context, locksClient *armlocks.ManagementLocksClient, resourceID string) bool {
	parsedID, err := azcorearm.ParseResourceID(resourceID)
	if err != nil {
		return false
	}

	parentResourcePath := ""
	if parsedID.Parent != nil {
		parentResourcePath = parsedID.Parent.String()
	}

	pager := locksClient.NewListAtResourceLevelPager(
		parsedID.ResourceGroupName,
		parsedID.ResourceType.Namespace,
		parentResourcePath,
		parsedID.ResourceType.Type,
		parsedID.Name,
		nil,
	)

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return false
		}
		if len(page.Value) > 0 {
			return true
		}
	}
	return false
}

func ResolveAPIVersion(
	ctx context.Context,
	providersClient *armresources.ProvidersClient,
	resourceType string,
) (string, error) {
	var providerNamespace, resourceTypeName string
	if idx := strings.Index(resourceType, "/"); idx > 0 {
		providerNamespace = resourceType[:idx]
		resourceTypeName = resourceType[idx+1:]
	} else {
		return "", fmt.Errorf("invalid resource type format: %s", resourceType)
	}

	provider, err := providersClient.Get(ctx, providerNamespace, nil)
	if err != nil {
		return "", fmt.Errorf("failed to get provider metadata for %s: %w", providerNamespace, err)
	}

	if provider.ResourceTypes != nil {
		for _, rt := range provider.ResourceTypes {
			if rt.ResourceType != nil && *rt.ResourceType == resourceTypeName && len(rt.APIVersions) > 0 {
				for _, version := range rt.APIVersions {
					if version != nil && !strings.Contains(*version, "preview") {
						return *version, nil
					}
				}
				if rt.APIVersions[0] != nil {
					return *rt.APIVersions[0], nil
				}
			}
		}
	}
	return "", fmt.Errorf("resource type %s not found in provider %s metadata", resourceTypeName, providerNamespace)
}

func DeleteByIDWithCache(
	ctx context.Context,
	client *armresources.Client,
	providersClient *armresources.ProvidersClient,
	resourceID string,
	resourceType string,
	wait bool,
	cache *apiVersionCache,
) error {
	apiVersion, ok := cache.Get(resourceType)
	if !ok {
		var err error
		apiVersion, err = ResolveAPIVersion(ctx, providersClient, resourceType)
		if err != nil {
			return fmt.Errorf("failed to get API version for %s: %w", resourceType, err)
		}
		cache.Set(resourceType, apiVersion)
	}
	return DeleteByID(ctx, client, resourceID, apiVersion, wait)
}

func DeleteByID(
	ctx context.Context,
	client *armresources.Client,
	resourceID string,
	apiVersion string,
	wait bool,
) error {
	poller, err := client.BeginDeleteByID(ctx, resourceID, apiVersion, nil)
	if err != nil {
		return err
	}
	if wait {
		return PollUntilDone(ctx, poller)
	}
	return nil
}

func PollUntilDone[T any](ctx context.Context, poller *runtime.Poller[T]) error {
	_, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
		Frequency: 10 * time.Second,
	})
	return err
}
