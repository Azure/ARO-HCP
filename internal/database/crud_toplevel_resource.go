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

package database

import (
	"context"
	"fmt"
	"path"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
)

type TopLevelResourceCRUD[T any] interface {
	Get(ctx context.Context, subscriptionID, resourceGroup, resourceID string) (*T, error)
	ListAll(ctx context.Context, subscriptionID string, opts *DBClientListResourceDocsOptions) (DBClientIterator[T], error)
	List(ctx context.Context, subscriptionID, resourceGroup string, opts *DBClientListResourceDocsOptions) (DBClientIterator[T], error)
}

type topLevelCosmosResourceCRUD[T any] struct {
	containerClient *azcosmos.ContainerClient
	resourceType    azcorearm.ResourceType
}

func newTopLevelResourceCRUD[T any](resources *azcosmos.ContainerClient, resourceType azcorearm.ResourceType) *topLevelCosmosResourceCRUD[T] {
	return &topLevelCosmosResourceCRUD[T]{
		containerClient: resources,
		resourceType:    resourceType,
	}
}

var _ TopLevelResourceCRUD[HCPCluster] = &topLevelCosmosResourceCRUD[HCPCluster]{}

func (d *topLevelCosmosResourceCRUD[T]) makeResourceIDPath(subscriptionID, resourceGroupID, resourceID string) (*azcorearm.ResourceID, error) {
	if len(subscriptionID) == 0 {
		return nil, fmt.Errorf("subscriptionID is required")
	}

	// this is valid for top level resource in azure.
	if len(resourceGroupID) == 0 {
		parts := []string{
			"/subscriptions",
			subscriptionID,
		}
		return azcorearm.ParseResourceID(path.Join(parts...))
	}

	parts := []string{
		"/subscriptions",
		subscriptionID,
		"resourceGroups",
		resourceGroupID,
		"providers",
		d.resourceType.Namespace,
	}

	parts = append(parts, d.resourceType.Type)

	if len(resourceID) > 0 {
		parts = append(parts, resourceID)
	}

	return azcorearm.ParseResourceID(path.Join(parts...))
}

func (d *topLevelCosmosResourceCRUD[T]) Get(ctx context.Context, subscriptionID, resourceGroup, resourceID string) (*T, error) {
	completeResourceID, err := d.makeResourceIDPath(subscriptionID, resourceGroup, resourceID)
	if err != nil {
		return nil, fmt.Errorf("failed to make ResourceID path for '%s': %w", resourceID, err)
	}

	return get[T](ctx, d.containerClient, completeResourceID)
}

func (d *topLevelCosmosResourceCRUD[T]) ListAll(ctx context.Context, subscriptionID string, options *DBClientListResourceDocsOptions) (DBClientIterator[T], error) {
	prefix, err := d.makeResourceIDPath(subscriptionID, "", "")
	if err != nil {
		return nil, fmt.Errorf("failed to make ResourceID path for '%s': %w", subscriptionID, err)
	}

	return list[T](ctx, d.containerClient, d.resourceType, prefix, options)
}

func (d *topLevelCosmosResourceCRUD[T]) List(ctx context.Context, subscriptionID, resourceGroup string, options *DBClientListResourceDocsOptions) (DBClientIterator[T], error) {
	prefix, err := d.makeResourceIDPath(subscriptionID, resourceGroup, "")
	if err != nil {
		return nil, fmt.Errorf("failed to make ResourceID path for '%s': %w", resourceGroup, err)
	}

	return list[T](ctx, d.containerClient, d.resourceType, prefix, options)
}
