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

	Child(resourceType azcorearm.ResourceType, resourceName string) (UntypedResourceCRUD, error)
}

type untypedCRUD struct {
	containerClient *azcosmos.ContainerClient

	// parentResourceID is relative to the storage we're using.  it can be as high as a subscription and as low as we go.
	// resources directly under a subscription or resourcegroup are handled a little specially when computing a resourceIDPath.
	parentResourceID azcorearm.ResourceID
}

var _ UntypedResourceCRUD = &untypedCRUD{}

func NewUntypedCRUD(containerClient *azcosmos.ContainerClient, parentResourceID azcorearm.ResourceID) UntypedResourceCRUD {
	ret := &untypedCRUD{
		containerClient:  containerClient,
		parentResourceID: parentResourceID,
	}

	return ret
}

func (d *untypedCRUD) GetByID(ctx context.Context, cosmosID string) (*TypedDocument, error) {
	panic("this function cannot work (yet) because we cannot guarantee that the item is under the parent")
}

func (d *untypedCRUD) Get(ctx context.Context, resourceID *azcorearm.ResourceID) (*TypedDocument, error) {
	if !strings.HasPrefix(strings.ToLower(resourceID.String()), strings.ToLower(d.parentResourceID.String())) {
		return nil, fmt.Errorf("resourceID %q must be a descendent of parentResourceID %q", resourceID.String(), d.parentResourceID.String())
	}
	partitionKey := strings.ToLower(d.parentResourceID.SubscriptionID)

	return get[TypedDocument, TypedDocument](ctx, d.containerClient, partitionKey, resourceID)
}

func (d *untypedCRUD) List(ctx context.Context, options *DBClientListResourceDocsOptions) (DBClientIterator[TypedDocument], error) {
	partitionKey := strings.ToLower(d.parentResourceID.SubscriptionID)
	return list[TypedDocument, TypedDocument](ctx, d.containerClient, partitionKey, nil, &d.parentResourceID, options, true)
}

func (d *untypedCRUD) ListRecursive(ctx context.Context, options *DBClientListResourceDocsOptions) (DBClientIterator[TypedDocument], error) {
	partitionKey := strings.ToLower(d.parentResourceID.SubscriptionID)
	return list[TypedDocument, TypedDocument](ctx, d.containerClient, partitionKey, nil, &d.parentResourceID, options, false)
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

	return NewUntypedCRUD(d.containerClient, *newParentResourceID), nil
}
