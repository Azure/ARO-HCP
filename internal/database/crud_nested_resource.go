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

type nestedCosmosResourceCRUD[T any] struct {
	containerClient   *azcosmos.ContainerClient
	providerNamespace string
	subscriptionID    string
	resourceGroupID   string

	// intermediateResources is optional and present when the resourceType is under another.  Think NodePools is under
	// an HCPCluster, so the intermediate resource is the HCPCluster
	intermediateResources []intermediateResource
	resourceType          azcorearm.ResourceType
}

type intermediateResource struct {
	resourceType azcorearm.ResourceType
	resourceID   string
}

var _ ResourceCRUD[NodePool] = &nestedCosmosResourceCRUD[NodePool]{}

func newNestedCosmosResourceCRUD[T any, V any](parent *topLevelCosmosResourceCRUD[V], subscriptionID, resourceGroupID, parentResourceID string, resourceType azcorearm.ResourceType) *nestedCosmosResourceCRUD[T] {
	ret := &nestedCosmosResourceCRUD[T]{
		containerClient:   parent.containerClient,
		providerNamespace: parent.resourceType.Namespace,
		subscriptionID:    subscriptionID,
		resourceGroupID:   resourceGroupID,
		resourceType:      resourceType,
	}
	ret.intermediateResources = append(ret.intermediateResources, intermediateResource{
		resourceType: parent.resourceType,
		resourceID:   parentResourceID,
	})
	return ret
}

func (d *nestedCosmosResourceCRUD[T]) makeResourceIDPath(resourceID string) (*azcorearm.ResourceID, error) {
	if len(d.subscriptionID) == 0 {
		return nil, fmt.Errorf("subscriptionID is required")
	}
	if len(d.resourceGroupID) == 0 && len(d.intermediateResources) > 0 {
		return nil, fmt.Errorf("resourceGroupID is required for all subresources")
	}

	parts := []string{
		"/subscriptions",
		d.subscriptionID,
		"resourceGroups",
		d.resourceGroupID,
		"providers",
		d.providerNamespace,
	}

	for _, currIntermediateResource := range d.intermediateResources {
		parts = append(parts, currIntermediateResource.resourceType.Type, currIntermediateResource.resourceID)
	}
	parts = append(parts, d.resourceType.Type)

	if len(resourceID) > 0 {
		parts = append(parts, resourceID)
	}

	return azcorearm.ParseResourceID(path.Join(parts...))
}

func (d *nestedCosmosResourceCRUD[T]) Get(ctx context.Context, resourceID string) (*T, error) {
	completeResourceID, err := d.makeResourceIDPath(resourceID)
	if err != nil {
		return nil, fmt.Errorf("failed to make ResourceID path for '%s': %w", resourceID, err)
	}

	return get[T](ctx, d.containerClient, completeResourceID)
}

func (d *nestedCosmosResourceCRUD[T]) List(ctx context.Context, options *DBClientListResourceDocsOptions) (DBClientIterator[T], error) {
	prefix, err := d.makeResourceIDPath("")
	if err != nil {
		return nil, fmt.Errorf("failed to make ResourceID path for '%s': %w", d.resourceGroupID, err)
	}

	return list[T](ctx, d.containerClient, d.resourceType, prefix, options)
}

func (d *nestedCosmosResourceCRUD[T]) Replace(ctx context.Context, desiredObj *T) (*T, error) {
	return replace[T](ctx, d.containerClient, desiredObj)
}
