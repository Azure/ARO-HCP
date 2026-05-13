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

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// kubeApplierUntypedCRUD is the kube-applier-container counterpart to untypedCRUD.
//
// It exists because the kube-applier container is partitioned by *management cluster name*,
// not by subscription, and cleanup callers (e.g. deleteOrphanedCosmosResources) walking a
// subscription have no way to know which management cluster owns a given desire. List and
// ListRecursive therefore issue *cross-partition* queries; deletion goes through
// DeleteByCosmosID using the partitionKey that came back on the listed document.
//
// Get and Delete(resourceID) intentionally return errors: both need a partition key derivable
// from the resourceID, and a *Desire's resourceID does not encode its management cluster.
// Callers that have the partition key in hand should reach for KubeApplier(mc).<Type>Desires(...)
// instead; cleanup callers should use DeleteByCosmosID.
type kubeApplierUntypedCRUD struct {
	containerClient  *azcosmos.ContainerClient
	parentResourceID azcorearm.ResourceID
}

var _ UntypedResourceCRUD = &kubeApplierUntypedCRUD{}

func newKubeApplierUntypedCRUD(containerClient *azcosmos.ContainerClient, parentResourceID azcorearm.ResourceID) *kubeApplierUntypedCRUD {
	return &kubeApplierUntypedCRUD{
		containerClient:  containerClient,
		parentResourceID: parentResourceID,
	}
}

func (d *kubeApplierUntypedCRUD) Get(ctx context.Context, resourceID *azcorearm.ResourceID) (*TypedDocument, error) {
	return nil, fmt.Errorf("kube-applier UntypedCRUD.Get is not supported: partitionKey is not derivable from a *Desire resourceID; use KubeApplier(managementCluster) for typed access")
}

func (d *kubeApplierUntypedCRUD) List(ctx context.Context, options *DBClientListResourceDocsOptions) (DBClientIterator[TypedDocument], error) {
	// Empty partitionKey → cross-partition query in list().
	return list[TypedDocument, TypedDocument](ctx, d.containerClient, "", nil, &d.parentResourceID, options, true)
}

func (d *kubeApplierUntypedCRUD) ListRecursive(ctx context.Context, options *DBClientListResourceDocsOptions) (DBClientIterator[TypedDocument], error) {
	return list[TypedDocument, TypedDocument](ctx, d.containerClient, "", nil, &d.parentResourceID, options, false)
}

func (d *kubeApplierUntypedCRUD) Delete(ctx context.Context, resourceID *azcorearm.ResourceID) error {
	return fmt.Errorf("kube-applier UntypedCRUD.Delete is not supported: partitionKey is not derivable from a *Desire resourceID; use DeleteByCosmosID with the row's partitionKey instead")
}

func (d *kubeApplierUntypedCRUD) DeleteByCosmosID(ctx context.Context, partitionKey, cosmosID string) error {
	return deleteByCosmosID(ctx, d.containerClient, partitionKey, cosmosID)
}

func (d *kubeApplierUntypedCRUD) Child(resourceType azcorearm.ResourceType, resourceName string) (UntypedResourceCRUD, error) {
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

	return newKubeApplierUntypedCRUD(d.containerClient, *newParentResourceID), nil
}
