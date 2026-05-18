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

package database

import (
	"context"
	"fmt"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// fleetResourceCRUD is a ResourceCRUD for Cosmos containers
// partitioned by the name of the top-level ancestor resource. The partition
// key is never stored — it is derived at operation time from the object's
// resource ID hierarchy or from the resource name parameter.
//
// This CRUD will be replaced once https://github.com/Azure/ARO-HCP/pull/5094
// lands, which generalizes partition key handling in CosmosMetadata. At that
// point partition key derivation moves into the shared infrastructure and
// this type can be merged with nestedCosmosResourceCRUD.
type fleetResourceCRUD[InternalAPIType, CosmosAPIType any] struct {
	containerClient  *azcosmos.ContainerClient
	parentResourceID *azcorearm.ResourceID
	resourceType     azcorearm.ResourceType
}

// topLevelResourceName walks a resource ID to its root ancestor and returns
// its name. This is the partition key for containers partitioned by their
// top-level resource name.
func topLevelResourceName(rid *azcorearm.ResourceID) string {
	if rid == nil {
		return ""
	}
	curr := rid
	for curr.Parent != nil && len(curr.Parent.Name) > 0 {
		curr = curr.Parent
	}
	return strings.ToLower(curr.Name)
}

// partitionKeyFromObject extracts the partition key from an object's
// CosmosMetadata resource ID by walking to the top-level ancestor.
func partitionKeyFromObject[InternalAPIType any](obj *InternalAPIType) (string, error) {
	persistable, ok := any(obj).(arm.CosmosPersistable)
	if !ok {
		return "", fmt.Errorf("type %T does not implement CosmosPersistable", obj)
	}
	partitionKey := topLevelResourceName(persistable.GetCosmosData().GetResourceID())
	if len(partitionKey) == 0 {
		return "", fmt.Errorf("cannot derive partition key from type %T: no top-level resource name", obj)
	}
	return partitionKey, nil
}

// partitionKeyFromParentOrName derives the partition key for read/delete
// operations. For child resources the top-level ancestor is in the parent
// resource ID; for top-level resources the resource name IS the partition key.
func (d *fleetResourceCRUD[InternalAPIType, CosmosAPIType]) partitionKeyFromParentOrName(resourceName string) string {
	if partitionKey := topLevelResourceName(d.parentResourceID); len(partitionKey) > 0 {
		return partitionKey
	}
	return strings.ToLower(resourceName)
}

func (d *fleetResourceCRUD[InternalAPIType, CosmosAPIType]) makeResourceIDPath(
	resourceName string,
) (*azcorearm.ResourceID, error) {
	var base string
	if d.parentResourceID != nil {
		base = d.parentResourceID.String() + "/" + d.resourceType.Types[len(d.resourceType.Types)-1]
	} else {
		base = "/providers/" + d.resourceType.String()
	}
	if len(resourceName) > 0 {
		base += "/" + resourceName
	}
	return azcorearm.ParseResourceID(strings.ToLower(base))
}

func (d *fleetResourceCRUD[InternalAPIType, CosmosAPIType]) GetByID(
	ctx context.Context, cosmosID string,
) (*InternalAPIType, error) {
	if strings.ToLower(cosmosID) != cosmosID {
		return nil, fmt.Errorf("cosmosID must be lowercase, not: %q", cosmosID)
	}
	partitionKey := topLevelResourceName(d.parentResourceID)
	if len(partitionKey) == 0 {
		return nil, fmt.Errorf("GetByID requires a parent-scoped CRUD with a known partition key")
	}
	return getByItemID[InternalAPIType, CosmosAPIType](ctx, d.containerClient, partitionKey, cosmosID)
}

func (d *fleetResourceCRUD[InternalAPIType, CosmosAPIType]) Get(
	ctx context.Context, resourceName string,
) (*InternalAPIType, error) {
	partitionKey := d.partitionKeyFromParentOrName(resourceName)
	resourceID, err := d.makeResourceIDPath(resourceName)
	if err != nil {
		return nil, fmt.Errorf("failed to make ResourceID path for '%s': %w", resourceName, err)
	}
	return get[InternalAPIType, CosmosAPIType](ctx, d.containerClient, partitionKey, resourceID)
}

func (d *fleetResourceCRUD[InternalAPIType, CosmosAPIType]) List(
	ctx context.Context, options *DBClientListResourceDocsOptions,
) (DBClientIterator[InternalAPIType], error) {
	partitionKey := topLevelResourceName(d.parentResourceID)
	if len(partitionKey) == 0 {
		return nil, fmt.Errorf("List requires a parent-scoped CRUD with a known partition key")
	}
	prefix, err := d.makeResourceIDPath("")
	if err != nil {
		return nil, fmt.Errorf("failed to make ResourceID prefix: %w", err)
	}
	return list[InternalAPIType, CosmosAPIType](
		ctx, d.containerClient, partitionKey, &d.resourceType, prefix, options, false,
	)
}

func (d *fleetResourceCRUD[InternalAPIType, CosmosAPIType]) Create(
	ctx context.Context, newObj *InternalAPIType, options *azcosmos.ItemOptions,
) (*InternalAPIType, error) {
	partitionKey, err := partitionKeyFromObject(newObj)
	if err != nil {
		return nil, err
	}
	return createFleetItem[InternalAPIType, CosmosAPIType](ctx, d.containerClient, partitionKey, newObj, options)
}

func (d *fleetResourceCRUD[InternalAPIType, CosmosAPIType]) Replace(
	ctx context.Context, newObj *InternalAPIType, options *azcosmos.ItemOptions,
) (*InternalAPIType, error) {
	partitionKey, err := partitionKeyFromObject(newObj)
	if err != nil {
		return nil, err
	}
	return replaceFleetItem[InternalAPIType, CosmosAPIType](ctx, d.containerClient, partitionKey, newObj, options)
}

func (d *fleetResourceCRUD[InternalAPIType, CosmosAPIType]) Delete(
	ctx context.Context, resourceName string,
) error {
	partitionKey := d.partitionKeyFromParentOrName(resourceName)
	resourceID, err := d.makeResourceIDPath(resourceName)
	if err != nil {
		return fmt.Errorf("failed to make ResourceID path for '%s': %w", resourceName, err)
	}
	return deleteResource(ctx, d.containerClient, partitionKey, resourceID)
}

func (d *fleetResourceCRUD[InternalAPIType, CosmosAPIType]) AddCreateToTransaction(
	_ context.Context,
	_ DBTransaction,
	_ *InternalAPIType,
	_ *azcosmos.TransactionalBatchItemOptions,
) (string, error) {
	return "", fmt.Errorf("AddCreateToTransaction is not implemented for fleet resources")
}

func (d *fleetResourceCRUD[InternalAPIType, CosmosAPIType]) AddReplaceToTransaction(
	_ context.Context,
	_ DBTransaction,
	_ *InternalAPIType,
	_ *azcosmos.TransactionalBatchItemOptions,
) (string, error) {
	return "", fmt.Errorf("AddReplaceToTransaction is not implemented for fleet resources")
}
