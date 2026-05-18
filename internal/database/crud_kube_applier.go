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

	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
)

// kubeApplierContainer is the Cosmos container name used by the kube-applier
// component. It is partitioned by the lower-cased management cluster name so
// that a kube-applier pod's Cosmos credentials can be scoped to its own
// management cluster, in contrast to every other container which is
// partitioned by subscription ID.
const kubeApplierContainer = "kube-applier"

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
type kubeApplierResourceCRUD[InternalAPIType, CosmosAPIType any] struct {
	containerClient  *azcosmos.ContainerClient
	parentResourceID *azcorearm.ResourceID
	resourceType     azcorearm.ResourceType
	// partitionKey is the lowercased management cluster name shared by every item
	// reachable through this CRUD scope.
	partitionKey string
}

func newKubeApplierResourceCRUD[InternalAPIType, CosmosAPIType any](
	containerClient *azcosmos.ContainerClient,
	managementCluster string,
	parentResourceID *azcorearm.ResourceID,
	resourceType azcorearm.ResourceType,
) *kubeApplierResourceCRUD[InternalAPIType, CosmosAPIType] {
	return &kubeApplierResourceCRUD[InternalAPIType, CosmosAPIType]{
		containerClient:  containerClient,
		parentResourceID: parentResourceID,
		resourceType:     resourceType,
		partitionKey:     strings.ToLower(managementCluster),
	}
}

func (d *kubeApplierResourceCRUD[InternalAPIType, CosmosAPIType]) makeResourceIDPath(
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

func (d *kubeApplierResourceCRUD[InternalAPIType, CosmosAPIType]) GetByID(
	ctx context.Context, cosmosID string,
) (*InternalAPIType, error) {
	if strings.ToLower(cosmosID) != cosmosID {
		return nil, fmt.Errorf("cosmosID must be lowercase, not: %q", cosmosID)
	}
	return getByItemID[InternalAPIType, CosmosAPIType](ctx, d.containerClient, d.partitionKey, cosmosID)
}

func (d *kubeApplierResourceCRUD[InternalAPIType, CosmosAPIType]) Get(
	ctx context.Context, resourceID string,
) (*InternalAPIType, error) {
	completeResourceID, err := d.makeResourceIDPath(resourceID)
	if err != nil {
		return nil, fmt.Errorf("failed to make ResourceID path for '%s': %w", resourceID, err)
	}
	return get[InternalAPIType, CosmosAPIType](ctx, d.containerClient, d.partitionKey, completeResourceID)
}

func (d *kubeApplierResourceCRUD[InternalAPIType, CosmosAPIType]) List(
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

func (d *kubeApplierResourceCRUD[InternalAPIType, CosmosAPIType]) Create(
	ctx context.Context, newObj *InternalAPIType, options *azcosmos.ItemOptions,
) (*InternalAPIType, error) {
	return createKubeApplier[InternalAPIType, CosmosAPIType](
		ctx, d.containerClient, d.partitionKey, newObj, options,
	)
}

func (d *kubeApplierResourceCRUD[InternalAPIType, CosmosAPIType]) Replace(
	ctx context.Context, newObj *InternalAPIType, options *azcosmos.ItemOptions,
) (*InternalAPIType, error) {
	return replaceKubeApplier[InternalAPIType, CosmosAPIType](
		ctx, d.containerClient, d.partitionKey, newObj, options,
	)
}

func (d *kubeApplierResourceCRUD[InternalAPIType, CosmosAPIType]) Delete(
	ctx context.Context, resourceName string,
) error {
	completeResourceID, err := d.makeResourceIDPath(resourceName)
	if err != nil {
		return fmt.Errorf("failed to make ResourceID path for '%s': %w", resourceName, err)
	}
	return deleteResource(ctx, d.containerClient, d.partitionKey, completeResourceID)
}

func (d *kubeApplierResourceCRUD[InternalAPIType, CosmosAPIType]) AddCreateToTransaction(
	ctx context.Context,
	transaction DBTransaction,
	newObj *InternalAPIType,
	opts *azcosmos.TransactionalBatchItemOptions,
) (string, error) {
	return addKubeApplierCreateToTransaction[InternalAPIType, CosmosAPIType](ctx, transaction, newObj, opts)
}

func (d *kubeApplierResourceCRUD[InternalAPIType, CosmosAPIType]) AddReplaceToTransaction(
	ctx context.Context,
	transaction DBTransaction,
	newObj *InternalAPIType,
	opts *azcosmos.TransactionalBatchItemOptions,
) (string, error) {
	return addKubeApplierReplaceToTransaction[InternalAPIType, CosmosAPIType](ctx, transaction, newObj, opts)
}

// Compile-time assertion that *kubeApplierResourceCRUD implements ResourceCRUD.
var _ ResourceCRUD[kubeapplier.ApplyDesire] = &kubeApplierResourceCRUD[kubeapplier.ApplyDesire, GenericDocument[kubeapplier.ApplyDesire]]{}
