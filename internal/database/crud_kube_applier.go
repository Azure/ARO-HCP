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
	"path"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
)

// NewKubeApplierPartitionKey creates a partition key for the kube-applier container,
// which is partitioned by the lower-cased management cluster name. This deviates
// from the subscription-ID partitioning used by every other container so that a
// kube-applier pod's Cosmos credentials can be scoped to its own management cluster.
func NewKubeApplierPartitionKey(managementCluster string) azcosmos.PartitionKey {
	return azcosmos.NewPartitionKeyString(strings.ToLower(managementCluster))
}

// kubeApplierResourceCRUD is the kube-applier counterpart to nestedCosmosResourceCRUD.
// The single behavioral difference is that the partition key is the lowercased
// management cluster name rather than the resource ID's subscription ID.
type kubeApplierResourceCRUD[InternalAPIType any, InternalAPITypePointer arm.CosmosMetadataAccessorPtr[InternalAPIType], CosmosAPIType any] struct {
	containerClient  *azcosmos.ContainerClient
	parentResourceID *azcorearm.ResourceID
	resourceType     azcorearm.ResourceType
	// partitionKey is the lowercased management cluster name shared by every item
	// reachable through this CRUD scope.
	partitionKey string
}

func newKubeApplierResourceCRUD[InternalAPIType any, InternalAPITypePointer arm.CosmosMetadataAccessorPtr[InternalAPIType], CosmosAPIType any](
	containerClient *azcosmos.ContainerClient,
	managementCluster string,
	parentResourceID *azcorearm.ResourceID,
	resourceType azcorearm.ResourceType,
) *kubeApplierResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType] {
	return &kubeApplierResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType]{
		containerClient:  containerClient,
		parentResourceID: parentResourceID,
		resourceType:     resourceType,
		partitionKey:     strings.ToLower(managementCluster),
	}
}

func (d *kubeApplierResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) makeResourceIDPath(
	resourceName string,
) (*azcorearm.ResourceID, error) {
	if d.parentResourceID == nil {
		return nil, fmt.Errorf("parentResourceID is required")
	}
	parts := []string{d.parentResourceID.String()}
	parts = append(parts, d.resourceType.Types[len(d.resourceType.Types)-1])
	if len(resourceName) > 0 {
		parts = append(parts, resourceName)
	}
	return azcorearm.ParseResourceID(strings.ToLower(path.Join(parts...)))
}

func (d *kubeApplierResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) GetByID(
	ctx context.Context, cosmosID string,
) (*InternalAPIType, error) {
	return getByItemID[InternalAPIType, CosmosAPIType](ctx, d.containerClient, d.partitionKey, cosmosID)
}

func (d *kubeApplierResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) Get(
	ctx context.Context, resourceID string,
) (*InternalAPIType, error) {
	completeResourceID, err := d.makeResourceIDPath(resourceID)
	if err != nil {
		return nil, fmt.Errorf("failed to make ResourceID path for '%s': %w", resourceID, err)
	}
	return get[InternalAPIType, CosmosAPIType](ctx, d.containerClient, d.partitionKey, completeResourceID)
}

func (d *kubeApplierResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) List(
	ctx context.Context, options *DBClientListResourceDocsOptions,
) (DBClientIterator[InternalAPIType], error) {
	prefix, err := d.makeResourceIDPath("")
	if err != nil {
		return nil, fmt.Errorf("failed to make ResourceID prefix: %w", err)
	}
	return list[InternalAPIType, CosmosAPIType](
		ctx, d.containerClient, d.partitionKey, &d.resourceType, prefix, options, false,
	)
}

// Create writes a new *Desire. The caller is responsible for setting both
// Spec.ManagementCluster (so the management cluster identity is on the
// document) and CosmosMetadata.PartitionKey (so the document lands in the
// right partition). SerializeItem refuses to write a document with an empty
// PartitionKey.
func (d *kubeApplierResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) Create(
	ctx context.Context, newObj *InternalAPIType, options *azcosmos.ItemOptions,
) (*InternalAPIType, error) {
	return create[InternalAPIType, CosmosAPIType, InternalAPITypePointer](
		ctx, d.containerClient, newObj, options,
	)
}

// Replace updates an existing *Desire. The caller must hand in an object
// whose CosmosMetadata is a fresh copy (not aliased with a cached/fetched
// object) — PrepareForReplace mutates InstanceVersion on the metadata and
// would otherwise leak that into the caller's reference. The desire
// controllers achieve this via desirestatuswriter, which DeepCopy()'s the
// fetched object (and therefore its embedded CosmosMetadata) before passing
// it back in here.
func (d *kubeApplierResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) Replace(
	ctx context.Context, newObj *InternalAPIType, options *azcosmos.ItemOptions,
) (*InternalAPIType, error) {
	return replace[InternalAPIType, CosmosAPIType, InternalAPITypePointer](
		ctx, d.containerClient, newObj, options,
	)
}

func (d *kubeApplierResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) Delete(
	ctx context.Context, resourceName string,
) error {
	completeResourceID, err := d.makeResourceIDPath(resourceName)
	if err != nil {
		return fmt.Errorf("failed to make ResourceID path for '%s': %w", resourceName, err)
	}
	return deleteResource(ctx, d.containerClient, d.partitionKey, completeResourceID)
}

func (d *kubeApplierResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) AddCreateToTransaction(
	ctx context.Context,
	transaction DBTransaction,
	newObj *InternalAPIType,
	opts *azcosmos.TransactionalBatchItemOptions,
) (string, error) {
	return addCreateToTransaction[InternalAPIType, CosmosAPIType, InternalAPITypePointer](ctx, transaction, newObj, opts)
}

func (d *kubeApplierResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) AddReplaceToTransaction(
	ctx context.Context,
	transaction DBTransaction,
	newObj *InternalAPIType,
	opts *azcosmos.TransactionalBatchItemOptions,
) (string, error) {
	return addReplaceToTransaction[InternalAPIType, CosmosAPIType, InternalAPITypePointer](ctx, transaction, newObj, opts)
}

// Compile-time assertion that *kubeApplierResourceCRUD implements ResourceCRUD.
var _ ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire] = &kubeApplierResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire, GenericDocument[kubeapplier.ApplyDesire]]{}
