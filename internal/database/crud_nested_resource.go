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
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

type ResourceCRUD[InternalAPIType any, InternalAPITypePointer arm.CosmosMetadataAccessorPtr[InternalAPIType]] interface {
	GetByID(ctx context.Context, cosmosID string) (*InternalAPIType, error)
	Get(ctx context.Context, resourceID string) (*InternalAPIType, error)
	List(ctx context.Context, opts *DBClientListResourceDocsOptions) (DBClientIterator[InternalAPIType], error)
	Create(ctx context.Context, newObj *InternalAPIType, options *azcosmos.ItemOptions) (*InternalAPIType, error)
	Replace(ctx context.Context, newObj *InternalAPIType, options *azcosmos.ItemOptions) (*InternalAPIType, error)
	Delete(ctx context.Context, resourceID string) error

	AddCreateToTransaction(ctx context.Context, transaction DBTransaction, newObj *InternalAPIType, opts *azcosmos.TransactionalBatchItemOptions) (string, error)
	AddReplaceToTransaction(ctx context.Context, transaction DBTransaction, newObj *InternalAPIType, opts *azcosmos.TransactionalBatchItemOptions) (string, error)
}

type ValidatingResourceCRUD[InternalAPIType any, InternalAPITypePointer arm.CosmosMetadataAccessorPtr[InternalAPIType]] interface {
	GetByID(ctx context.Context, cosmosID string) (*InternalAPIType, error)
	Get(ctx context.Context, resourceID string) (*InternalAPIType, error)
	List(ctx context.Context, opts *DBClientListResourceDocsOptions) (DBClientIterator[InternalAPIType], error)
	Create(ctx context.Context, newObj *InternalAPIType, options *azcosmos.ItemOptions) (*InternalAPIType, error)
	Replace(ctx context.Context, newObj *InternalAPIType, oldObj *InternalAPIType, options *azcosmos.ItemOptions) (*InternalAPIType, error)
	Delete(ctx context.Context, resourceID string) error
}

type nestedCosmosResourceCRUD[InternalAPIType any, InternalAPITypePointer arm.CosmosMetadataAccessorPtr[InternalAPIType], CosmosAPIType any] struct {
	containerClient *azcosmos.ContainerClient

	// parentResourceID is relative to the storage we're using.  it can be as high as a subscription and as low as we go.
	// resources directly under a subscription or resourcegroup are handled a little specially when computing a resourceIDPath.
	parentResourceID *azcorearm.ResourceID
	resourceType     azcorearm.ResourceType
}

var _ ResourceCRUD[api.HCPOpenShiftClusterNodePool, *api.HCPOpenShiftClusterNodePool] = &nestedCosmosResourceCRUD[api.HCPOpenShiftClusterNodePool, *api.HCPOpenShiftClusterNodePool, GenericDocument[api.HCPOpenShiftClusterNodePool]]{}

func NewCosmosResourceCRUD[InternalAPIType any, InternalAPITypePointer arm.CosmosMetadataAccessorPtr[InternalAPIType], CosmosAPIType any](
	containerClient *azcosmos.ContainerClient, parentResourceID *azcorearm.ResourceID, resourceType azcorearm.ResourceType) *nestedCosmosResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType] {

	ret := &nestedCosmosResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType]{
		containerClient:  containerClient,
		parentResourceID: parentResourceID,
		resourceType:     resourceType,
	}

	return ret
}

func (d *nestedCosmosResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) makeResourceIDPath(resourceName string) (*azcorearm.ResourceID, error) {
	if d.parentResourceID == nil {
		return arm.ToSubscriptionResourceID(resourceName)
	}

	if len(d.parentResourceID.SubscriptionID) == 0 {
		return nil, fmt.Errorf("subscriptionID is required")
	}
	parts := []string{d.parentResourceID.String()}

	if !strings.EqualFold(d.parentResourceID.ResourceType.Namespace, api.ProviderNamespace) {
		if len(resourceName) == 0 {
			// in this case, adding the actual provider type results in an illegal resourceID
			// for instance /subscriptions/0465bc32-c654-41b8-8d87-9815d7abe8f6/resourceGroups/some-resource-group/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters does not parse
			resourcePathString := path.Join(parts...)
			return azcorearm.ParseResourceID(resourcePathString)
		}

		parts = append(parts,
			"providers",
			d.resourceType.Namespace,
		)

	} else {
		// for non-top level resources, we must have a resourceGroup
		if len(d.parentResourceID.ResourceGroupName) == 0 {
			return nil, fmt.Errorf("resourceGroup is required")
		}
	}
	parts = append(parts, d.resourceType.Types[len(d.resourceType.Types)-1])

	if len(resourceName) > 0 {
		parts = append(parts, resourceName)
	}

	resourcePathString := path.Join(parts...)
	return azcorearm.ParseResourceID(resourcePathString)
}

func (d *nestedCosmosResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) GetByID(ctx context.Context, cosmosID string) (*InternalAPIType, error) {
	if strings.ToLower(cosmosID) != cosmosID {
		return nil, fmt.Errorf("cosmosID must be lowercase, not: %q", cosmosID)
	}
	partitionKey := ""
	if d.parentResourceID != nil {
		partitionKey = strings.ToLower(d.parentResourceID.SubscriptionID)
	}

	return getByItemID[InternalAPIType, CosmosAPIType](ctx, d.containerClient, partitionKey, cosmosID)
}

func (d *nestedCosmosResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) Get(ctx context.Context, resourceID string) (*InternalAPIType, error) {
	completeResourceID, err := d.makeResourceIDPath(resourceID)
	if err != nil {
		return nil, fmt.Errorf("failed to make ResourceID path for '%s': %w", resourceID, err)
	}
	partitionKey := strings.ToLower(completeResourceID.SubscriptionID)

	return get[InternalAPIType, CosmosAPIType](ctx, d.containerClient, partitionKey, completeResourceID)
}

func (d *nestedCosmosResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) List(ctx context.Context, options *DBClientListResourceDocsOptions) (DBClientIterator[InternalAPIType], error) {
	if d.parentResourceID == nil {
		return list[InternalAPIType, CosmosAPIType](ctx, d.containerClient, "", &azcorearm.SubscriptionResourceType, nil, options, false)
	}

	prefix, err := d.makeResourceIDPath("")
	if err != nil {
		return nil, fmt.Errorf("failed to make ResourceID path for '%s': %w", d.parentResourceID.ResourceGroupName, err)
	}
	partitionKey := strings.ToLower(d.parentResourceID.SubscriptionID)

	return list[InternalAPIType, CosmosAPIType](ctx, d.containerClient, partitionKey, &d.resourceType, prefix, options, false)
}

func (d *nestedCosmosResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) AddCreateToTransaction(ctx context.Context, transaction DBTransaction, newObj *InternalAPIType, opts *azcosmos.TransactionalBatchItemOptions) (string, error) {
	if err := ensureSubscriptionPartitionKey[InternalAPIType, InternalAPITypePointer](newObj); err != nil {
		return "", err
	}
	return addCreateToTransaction[InternalAPIType, CosmosAPIType, InternalAPITypePointer](ctx, transaction, newObj, opts)
}

func (d *nestedCosmosResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) AddReplaceToTransaction(ctx context.Context, transaction DBTransaction, newObj *InternalAPIType, opts *azcosmos.TransactionalBatchItemOptions) (string, error) {
	if err := ensureSubscriptionPartitionKey[InternalAPIType, InternalAPITypePointer](newObj); err != nil {
		return "", err
	}
	return addReplaceToTransaction[InternalAPIType, CosmosAPIType, InternalAPITypePointer](ctx, transaction, newObj, opts)
}

// ensureSubscriptionPartitionKey populates CosmosMetadata.PartitionKey with
// the subscriptionID from the object's resource ID. SetPartitionKey lowercases
// internally; the conversion layer rejects empty / non-normalized values at
// serialize time.
func ensureSubscriptionPartitionKey[InternalAPIType any, InternalAPITypePointer arm.CosmosMetadataAccessorPtr[InternalAPIType]](newObj InternalAPITypePointer) error {
	rid := newObj.GetResourceID()
	if rid == nil {
		return fmt.Errorf("type %T has no ResourceID — cannot derive subscription partition key", newObj)
	}
	if len(rid.SubscriptionID) == 0 {
		return fmt.Errorf("type %T has an empty SubscriptionID in its resource ID", newObj)
	}
	newObj.SetPartitionKey(rid.SubscriptionID)
	return nil
}

func (d *nestedCosmosResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) Create(ctx context.Context, newObj *InternalAPIType, options *azcosmos.ItemOptions) (*InternalAPIType, error) {
	if err := ensureSubscriptionPartitionKey[InternalAPIType, InternalAPITypePointer](newObj); err != nil {
		return nil, err
	}
	return create[InternalAPIType, CosmosAPIType, InternalAPITypePointer](ctx, d.containerClient, newObj, options)
}

func (d *nestedCosmosResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) Replace(ctx context.Context, newObj *InternalAPIType, options *azcosmos.ItemOptions) (*InternalAPIType, error) {
	if err := ensureSubscriptionPartitionKey[InternalAPIType, InternalAPITypePointer](newObj); err != nil {
		return nil, err
	}
	return replace[InternalAPIType, CosmosAPIType, InternalAPITypePointer](ctx, d.containerClient, newObj, options)
}

func (d *nestedCosmosResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) Delete(ctx context.Context, resourceName string) error {
	completeResourceID, err := d.makeResourceIDPath(resourceName)
	if err != nil {
		return fmt.Errorf("failed to make ResourceID path for '%s': %w", resourceName, err)
	}
	partitionKey := strings.ToLower(completeResourceID.SubscriptionID)

	return deleteResource(ctx, d.containerClient, partitionKey, completeResourceID)
}
