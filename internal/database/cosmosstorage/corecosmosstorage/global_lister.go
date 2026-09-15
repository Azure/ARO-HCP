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

package corecosmosstorage

import (
	"context"
	"fmt"

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
)

// ResourcesGlobalListers provides access to global listers for each resource type.
// These are intended to feed SharedInformers via ListerWatchers.
type ResourcesGlobalListers interface {
	Subscriptions() cosmosstorageutils.GlobalLister[coreapi.Subscription]
	Clusters() cosmosstorageutils.GlobalLister[coreapi.HCPOpenShiftCluster]
	NodePools() cosmosstorageutils.GlobalLister[coreapi.HCPOpenShiftClusterNodePool]
	ExternalAuths() cosmosstorageutils.GlobalLister[coreapi.HCPOpenShiftClusterExternalAuth]
	ServiceProviderClusters() cosmosstorageutils.GlobalLister[coreapi.ServiceProviderCluster]
	ServiceProviderNodePools() cosmosstorageutils.GlobalLister[coreapi.ServiceProviderNodePool]
	ServiceProviderExternalAuths() cosmosstorageutils.GlobalLister[coreapi.ServiceProviderExternalAuth]
	Controllers() cosmosstorageutils.GlobalLister[coreapi.Controller]
	// ManagementClusterContents lists ManagementClusterContent documents across
	// partitions for every Cosmos resource type where managementClusterContents
	// is nested as a direct child resource. Those types are registered on the lister implementation.
	ManagementClusterContents() cosmosstorageutils.GlobalLister[coreapi.ManagementClusterContent]
	SystemAdminCredentialRequests() cosmosstorageutils.GlobalLister[coreapi.SystemAdminCredentialRequest]
	SystemAdminCredentialRevocations() cosmosstorageutils.GlobalLister[coreapi.SystemAdminCredentialRevocation]
	Operations() cosmosstorageutils.GlobalLister[coreapi.Operation]
	ActiveOperations() cosmosstorageutils.GlobalLister[coreapi.Operation]
}

// cosmosResourcesGlobalListers implements ResourcesGlobalListers using the Resources Cosmos container.
type cosmosResourcesGlobalListers struct {
	resources *azcosmos.ContainerClient
}

var _ ResourcesGlobalListers = &cosmosResourcesGlobalListers{}

func NewCosmosResourcesGlobalListers(resources *azcosmos.ContainerClient) ResourcesGlobalListers {
	return &cosmosResourcesGlobalListers{
		resources: resources,
	}
}

func (g *cosmosResourcesGlobalListers) Subscriptions() cosmosstorageutils.GlobalLister[coreapi.Subscription] {
	return &cosmosstorageutils.CosmosGlobalLister[coreapi.Subscription, cosmosstorageutils.GenericDocument[coreapi.Subscription]]{
		ContainerClient: g.resources,
		ResourceTypes:   []azcorearm.ResourceType{azcorearm.SubscriptionResourceType},
	}
}

func (g *cosmosResourcesGlobalListers) Clusters() cosmosstorageutils.GlobalLister[coreapi.HCPOpenShiftCluster] {
	return &cosmosstorageutils.CosmosGlobalLister[coreapi.HCPOpenShiftCluster, cosmosstorageutils.GenericDocument[coreapi.HCPOpenShiftCluster]]{
		ContainerClient: g.resources,
		ResourceTypes:   []azcorearm.ResourceType{coreapi.ClusterResourceType},
	}
}

func (g *cosmosResourcesGlobalListers) NodePools() cosmosstorageutils.GlobalLister[coreapi.HCPOpenShiftClusterNodePool] {
	return &cosmosstorageutils.CosmosGlobalLister[coreapi.HCPOpenShiftClusterNodePool, cosmosstorageutils.GenericDocument[coreapi.HCPOpenShiftClusterNodePool]]{
		ContainerClient: g.resources,
		ResourceTypes:   []azcorearm.ResourceType{coreapi.NodePoolResourceType},
	}
}

func (g *cosmosResourcesGlobalListers) ExternalAuths() cosmosstorageutils.GlobalLister[coreapi.HCPOpenShiftClusterExternalAuth] {
	return &cosmosstorageutils.CosmosGlobalLister[coreapi.HCPOpenShiftClusterExternalAuth, cosmosstorageutils.GenericDocument[coreapi.HCPOpenShiftClusterExternalAuth]]{
		ContainerClient: g.resources,
		ResourceTypes:   []azcorearm.ResourceType{coreapi.ExternalAuthResourceType},
	}
}

func (g *cosmosResourcesGlobalListers) ServiceProviderClusters() cosmosstorageutils.GlobalLister[coreapi.ServiceProviderCluster] {
	return &cosmosstorageutils.CosmosGlobalLister[coreapi.ServiceProviderCluster, cosmosstorageutils.GenericDocument[coreapi.ServiceProviderCluster]]{
		ContainerClient: g.resources,
		ResourceTypes:   []azcorearm.ResourceType{coreapi.ServiceProviderClusterResourceType},
	}
}

func (g *cosmosResourcesGlobalListers) ServiceProviderNodePools() cosmosstorageutils.GlobalLister[coreapi.ServiceProviderNodePool] {
	return &cosmosstorageutils.CosmosGlobalLister[coreapi.ServiceProviderNodePool, cosmosstorageutils.GenericDocument[coreapi.ServiceProviderNodePool]]{
		ContainerClient: g.resources,
		ResourceTypes:   []azcorearm.ResourceType{coreapi.ServiceProviderNodePoolResourceType},
	}
}

func (g *cosmosResourcesGlobalListers) ServiceProviderExternalAuths() cosmosstorageutils.GlobalLister[coreapi.ServiceProviderExternalAuth] {
	return &cosmosstorageutils.CosmosGlobalLister[coreapi.ServiceProviderExternalAuth, cosmosstorageutils.GenericDocument[coreapi.ServiceProviderExternalAuth]]{
		ContainerClient: g.resources,
		ResourceTypes:   []azcorearm.ResourceType{coreapi.ServiceProviderExternalAuthResourceType},
	}
}

func (g *cosmosResourcesGlobalListers) SystemAdminCredentialRequests() cosmosstorageutils.GlobalLister[coreapi.SystemAdminCredentialRequest] {
	return &cosmosstorageutils.CosmosGlobalLister[coreapi.SystemAdminCredentialRequest, cosmosstorageutils.GenericDocument[coreapi.SystemAdminCredentialRequest]]{
		ContainerClient: g.resources,
		ResourceTypes:   []azcorearm.ResourceType{coreapi.SystemAdminCredentialRequestResourceType},
	}
}

func (g *cosmosResourcesGlobalListers) SystemAdminCredentialRevocations() cosmosstorageutils.GlobalLister[coreapi.SystemAdminCredentialRevocation] {
	return &cosmosstorageutils.CosmosGlobalLister[coreapi.SystemAdminCredentialRevocation, cosmosstorageutils.GenericDocument[coreapi.SystemAdminCredentialRevocation]]{
		ContainerClient: g.resources,
		ResourceTypes:   []azcorearm.ResourceType{coreapi.SystemAdminCredentialRevocationResourceType},
	}
}

func (g *cosmosResourcesGlobalListers) Controllers() cosmosstorageutils.GlobalLister[coreapi.Controller] {
	return &cosmosstorageutils.CosmosGlobalLister[coreapi.Controller, cosmosstorageutils.GenericDocument[coreapi.Controller]]{
		ContainerClient: g.resources,
		ResourceTypes: []azcorearm.ResourceType{
			coreapi.ClusterControllerResourceType,
			coreapi.NodePoolControllerResourceType,
			coreapi.ExternalAuthControllerResourceType,
			coreapi.SystemAdminCredentialRequestControllerResourceType,
			coreapi.SystemAdminCredentialRevocationControllerResourceType,
		},
	}
}

func (g *cosmosResourcesGlobalListers) ManagementClusterContents() cosmosstorageutils.GlobalLister[coreapi.ManagementClusterContent] {
	return &cosmosstorageutils.CosmosGlobalLister[coreapi.ManagementClusterContent, cosmosstorageutils.GenericDocument[coreapi.ManagementClusterContent]]{
		ContainerClient: g.resources,
		ResourceTypes: []azcorearm.ResourceType{
			coreapi.ClusterScopedManagementClusterContentResourceType,
			coreapi.NodePoolScopedManagementClusterContentResourceType,
		},
	}
}

func (g *cosmosResourcesGlobalListers) Operations() cosmosstorageutils.GlobalLister[coreapi.Operation] {
	return &cosmosstorageutils.CosmosGlobalLister[coreapi.Operation, cosmosstorageutils.GenericDocument[coreapi.Operation]]{
		ContainerClient: g.resources,
		ResourceTypes:   []azcorearm.ResourceType{coreapi.OperationStatusResourceType},
	}
}

func (g *cosmosResourcesGlobalListers) ActiveOperations() cosmosstorageutils.GlobalLister[coreapi.Operation] {
	return &cosmosActiveOperationsGlobalLister{
		containerClient: g.resources,
	}
}

// cosmosActiveOperationsGlobalLister lists operations with non-terminal status
// across all partitions.
type cosmosActiveOperationsGlobalLister struct {
	containerClient *azcosmos.ContainerClient
}

func (l *cosmosActiveOperationsGlobalLister) List(ctx context.Context, options *cosmosstorageutils.DBClientListResourceDocsOptions) (cosmosstorageutils.DBClientIterator[coreapi.Operation], error) {
	query := fmt.Sprintf(
		"SELECT * FROM c WHERE STRINGEQUALS(c.resourceType, %q, true) "+
			"AND LENGTH(c.resourceID) > 0 "+
			"AND (NOT IS_DEFINED(c.deletionTimestamp)) "+
			"AND NOT ARRAYCONTAINS([%q, %q, %q], c.properties.status)",
		coreapi.OperationStatusResourceType.String(),
		coreapi.ProvisioningStateSucceeded,
		coreapi.ProvisioningStateFailed,
		coreapi.ProvisioningStateCanceled)

	queryOptions := azcosmos.QueryOptions{
		PageSizeHint: -1,
	}
	if options != nil {
		if options.PageSizeHint != nil {
			queryOptions.PageSizeHint = max(*options.PageSizeHint, -1)
		}
		queryOptions.ContinuationToken = options.ContinuationToken
	}

	partitionKey := azcosmos.NewPartitionKey()
	pager := l.containerClient.NewQueryItemsPager(query, partitionKey, &queryOptions)

	if options != nil && ptr.Deref(options.PageSizeHint, -1) > 0 {
		return cosmosstorageutils.NewQueryResourcesSinglePageIterator[coreapi.Operation, cosmosstorageutils.GenericDocument[coreapi.Operation]](pager), nil
	}
	return cosmosstorageutils.NewQueryResourcesIterator[coreapi.Operation, cosmosstorageutils.GenericDocument[coreapi.Operation]](pager), nil
}
