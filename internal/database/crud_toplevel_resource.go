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

	"github.com/Azure/ARO-HCP/internal/api"
)

type ResourceCRUD[InternalAPIType any] interface {
	Get(ctx context.Context, resourceID string) (*InternalAPIType, error)
	List(ctx context.Context, opts *DBClientListResourceDocsOptions) (DBClientIterator[InternalAPIType], error)
	AddCreateToTransaction(ctx context.Context, transaction DBTransaction, newObj *InternalAPIType, opts *azcosmos.TransactionalBatchItemOptions) (string, error)
}

type topLevelCosmosResourceCRUD[InternalAPIType, CosmosAPIType any] struct {
	containerClient   *azcosmos.ContainerClient
	resourceType      azcorearm.ResourceType
	subscriptionID    string
	resourceGroupName string
}

func newTopLevelResourceCRUD[InternalAPIType, CosmosAPIType any](resources *azcosmos.ContainerClient, resourceType azcorearm.ResourceType, subscriptionID, resourceGroupName string) *topLevelCosmosResourceCRUD[InternalAPIType, CosmosAPIType] {
	return &topLevelCosmosResourceCRUD[InternalAPIType, CosmosAPIType]{
		containerClient:   resources,
		resourceType:      resourceType,
		subscriptionID:    subscriptionID,
		resourceGroupName: resourceGroupName,
	}
}

var _ ResourceCRUD[api.HCPOpenShiftCluster] = &topLevelCosmosResourceCRUD[api.HCPOpenShiftCluster, HCPCluster]{}

func (d *topLevelCosmosResourceCRUD[InternalAPIType, CosmosAPIType]) makeResourceIDPath(subscriptionID, resourceGroupID, resourceID string) (*azcorearm.ResourceID, error) {
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
	}

	if len(resourceID) > 0 {
		parts = append(parts,
			"providers",
			d.resourceType.Namespace,
			d.resourceType.Type,
			resourceID)
	}

	return azcorearm.ParseResourceID(path.Join(parts...))
}

func (d *topLevelCosmosResourceCRUD[InternalAPIType, CosmosAPIType]) Get(ctx context.Context, resourceID string) (*InternalAPIType, error) {
	completeResourceID, err := d.makeResourceIDPath(d.subscriptionID, d.resourceGroupName, resourceID)
	if err != nil {
		return nil, fmt.Errorf("failed to make ResourceID path for '%s': %w", resourceID, err)
	}

	return get[InternalAPIType, CosmosAPIType](ctx, d.containerClient, completeResourceID)
}

func (d *topLevelCosmosResourceCRUD[InternalAPIType, CosmosAPIType]) List(ctx context.Context, options *DBClientListResourceDocsOptions) (DBClientIterator[InternalAPIType], error) {
	// when resourceGroupName is empty, this lists all in the subscription
	prefix, err := d.makeResourceIDPath(d.subscriptionID, d.resourceGroupName, "")
	if err != nil {
		return nil, fmt.Errorf("failed to make ResourceID path for %q: %w", d.resourceGroupName, err)
	}

	return list[InternalAPIType, CosmosAPIType](ctx, d.containerClient, d.resourceType, prefix, options)
}

func (d *topLevelCosmosResourceCRUD[InternalAPIType, CosmosAPIType]) AddCreateToTransaction(ctx context.Context, transaction DBTransaction, newObj *InternalAPIType, opts *azcosmos.TransactionalBatchItemOptions) (string, error) {
	return addCreateToTransaction[InternalAPIType, CosmosAPIType](ctx, transaction, newObj, opts)
}
