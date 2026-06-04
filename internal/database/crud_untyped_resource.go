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
	"github.com/Azure/ARO-HCP/internal/utils"
)

type UntypedResourceCRUD interface {
	Get(ctx context.Context, resourceID *azcorearm.ResourceID) (*TypedDocument, error)
	// List returns back only direct descendents from the parent.
	List(ctx context.Context, opts *DBClientListResourceDocsOptions) (DBClientIterator[TypedDocument], error)
	// ListRecursive returns back every descendent from the parent.  For instance, if you ListRecursive on a cluster,
	// you will get the controllers for the cluster, the nodepools, the controllers for each nodepool, the external auths,
	// the controllers for the external auths, etc.
	ListRecursive(ctx context.Context, opts *DBClientListResourceDocsOptions) (DBClientIterator[TypedDocument], error)
	Delete(ctx context.Context, resourceID *azcorearm.ResourceID) error
	DeleteByCosmosID(ctx context.Context, partitionKey, cosmosID string) error

	Child(resourceType azcorearm.ResourceType, resourceName string) (UntypedResourceCRUD, error)
}

type untypedCRUD struct {
	containerClient *azcosmos.ContainerClient

	// parentResourceID is relative to the storage we're using.  it can be as high as a subscription and as low as we go.
	// resources directly under a subscription or resourcegroup are handled a little specially when computing a resourceIDPath.
	parentResourceID    azcorearm.ResourceID
	partitionKeyDeriver PartitionKeyDeriver
}

var _ UntypedResourceCRUD = &untypedCRUD{}

// NewUntypedCRUD builds an UntypedResourceCRUD that scopes all operations to
// descendents of parentResourceID and derives partition keys from the
// subscription embedded in the resource hierarchy. For containers that
// partition differently use NewUntypedCRUDWithPartitionKey.
func NewUntypedCRUD(containerClient *azcosmos.ContainerClient, parentResourceID azcorearm.ResourceID) UntypedResourceCRUD {
	return NewUntypedCRUDWithPartitionKey(containerClient, parentResourceID, SubscriptionPartitionKeyDeriver{})
}

// NewUntypedCRUDWithPartitionKey builds an UntypedResourceCRUD with a
// caller-supplied partition-key policy. Single-resource operations (GetByID,
// Get, Delete) call PartitionKey with a non-empty resourceName so a deriver
// that can't compute a key per resource can return an error; List /
// ListRecursive call PartitionKey with an empty resourceName so the same
// deriver can return "" to opt into a cross-partition query.
func NewUntypedCRUDWithPartitionKey(containerClient *azcosmos.ContainerClient, parentResourceID azcorearm.ResourceID, partitionKeyDeriver PartitionKeyDeriver) UntypedResourceCRUD {
	return &untypedCRUD{
		containerClient:     containerClient,
		parentResourceID:    parentResourceID,
		partitionKeyDeriver: partitionKeyDeriver,
	}
}

func (d *untypedCRUD) GetByID(ctx context.Context, cosmosID string) (*TypedDocument, error) {
	partitionKey, err := d.partitionKeyDeriver.PartitionKey(&d.parentResourceID, cosmosID)
	if err != nil {
		return nil, err
	}
	return getByItemID[TypedDocument, TypedDocument](ctx, d.containerClient, partitionKey, cosmosID)
}

func (d *untypedCRUD) Get(ctx context.Context, resourceID *azcorearm.ResourceID) (*TypedDocument, error) {
	if !strings.HasPrefix(strings.ToLower(resourceID.String()), strings.ToLower(d.parentResourceID.String())) {
		return nil, fmt.Errorf("resourceID %q must be a descendent of parentResourceID %q", resourceID.String(), d.parentResourceID.String())
	}
	partitionKey, err := d.partitionKeyDeriver.PartitionKey(&d.parentResourceID, resourceID.Name)
	if err != nil {
		return nil, err
	}

	return get[TypedDocument, TypedDocument](ctx, d.containerClient, partitionKey, resourceID)
}

func (d *untypedCRUD) List(ctx context.Context, options *DBClientListResourceDocsOptions) (DBClientIterator[TypedDocument], error) {
	partitionKey, err := d.partitionKeyDeriver.PartitionKey(&d.parentResourceID, "")
	if err != nil {
		return nil, err
	}
	return list[TypedDocument, TypedDocument](ctx, d.containerClient, partitionKey, nil, &d.parentResourceID, options, true)
}

func (d *untypedCRUD) ListRecursive(ctx context.Context, options *DBClientListResourceDocsOptions) (DBClientIterator[TypedDocument], error) {
	partitionKey, err := d.partitionKeyDeriver.PartitionKey(&d.parentResourceID, "")
	if err != nil {
		return nil, err
	}
	return list[TypedDocument, TypedDocument](ctx, d.containerClient, partitionKey, nil, &d.parentResourceID, options, false)
}

func (d *untypedCRUD) Delete(ctx context.Context, resourceID *azcorearm.ResourceID) error {
	if !strings.HasPrefix(strings.ToLower(resourceID.String()), strings.ToLower(d.parentResourceID.String())) {
		return fmt.Errorf("resourceID %q must be a descendent of parentResourceID %q", resourceID.String(), d.parentResourceID.String())
	}
	partitionKey, err := d.partitionKeyDeriver.PartitionKey(&d.parentResourceID, resourceID.Name)
	if err != nil {
		return err
	}

	return deleteResource(ctx, d.containerClient, partitionKey, resourceID)
}

func (d *untypedCRUD) DeleteByCosmosID(ctx context.Context, partitionKey, cosmosID string) error {
	return deleteByCosmosID(ctx, d.containerClient, partitionKey, cosmosID)
}

func (d *untypedCRUD) Child(resourceType azcorearm.ResourceType, resourceName string) (UntypedResourceCRUD, error) {
	if len(resourceName) == 0 {
		return nil, fmt.Errorf("resourceName is required")
	}

	parts := []string{d.parentResourceID.String()}

	switch {
	case strings.EqualFold(resourceType.Type, "resourcegroups"):
		// no provider needed here.
	case resourceType.Namespace == api.ProviderNamespace && d.parentResourceID.ResourceType.Namespace != api.ProviderNamespace:
		parts = append(parts,
			"providers",
			resourceType.Namespace,
		)
	case resourceType.Namespace != api.ProviderNamespace && d.parentResourceID.ResourceType.Namespace == api.ProviderNamespace:
		return nil, fmt.Errorf("cannot switch to a non-RH provider: %q", resourceType.Namespace)
	}
	parts = append(parts, resourceType.Types[len(resourceType.Types)-1])
	parts = append(parts, resourceName)

	resourcePathString := path.Join(parts...)
	newParentResourceID, err := azcorearm.ParseResourceID(resourcePathString)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	return NewUntypedCRUDWithPartitionKey(d.containerClient, *newParentResourceID, d.partitionKeyDeriver), nil
}
