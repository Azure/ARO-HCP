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

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
)

// ResourcesGlobalListers provides access to global listers for each resource type.
// These are intended to feed SharedInformers via ListerWatchers.
type ResourcesGlobalListers interface {
	Subscriptions() cosmosstorageutils.GlobalLister[arm.Subscription]
	Clusters() cosmosstorageutils.GlobalLister[api.HCPOpenShiftCluster]
	NodePools() cosmosstorageutils.GlobalLister[api.HCPOpenShiftClusterNodePool]
	ExternalAuths() cosmosstorageutils.GlobalLister[api.HCPOpenShiftClusterExternalAuth]
	ServiceProviderClusters() cosmosstorageutils.GlobalLister[api.ServiceProviderCluster]
	ServiceProviderNodePools() cosmosstorageutils.GlobalLister[api.ServiceProviderNodePool]
	Controllers() cosmosstorageutils.GlobalLister[api.Controller]
	// ManagementClusterContents lists ManagementClusterContent documents across
	// partitions for every Cosmos resource type where managementClusterContents
	// is nested as a direct child resource. Those types are registered on the lister implementation.
	ManagementClusterContents() cosmosstorageutils.GlobalLister[api.ManagementClusterContent]
	SystemAdminCredentialRequests() cosmosstorageutils.GlobalLister[api.SystemAdminCredentialRequest]
	SystemAdminCredentialRevocations() cosmosstorageutils.GlobalLister[api.SystemAdminCredentialRevocation]
	Operations() cosmosstorageutils.GlobalLister[api.Operation]
	ActiveOperations() cosmosstorageutils.GlobalLister[api.Operation]
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

func (g *cosmosResourcesGlobalListers) Subscriptions() cosmosstorageutils.GlobalLister[arm.Subscription] {
	return &cosmosstorageutils.CosmosGlobalLister[arm.Subscription, cosmosstorageutils.GenericDocument[arm.Subscription]]{
		ContainerClient: g.resources,
		ResourceTypes:   []azcorearm.ResourceType{azcorearm.SubscriptionResourceType},
	}
}

func (g *cosmosResourcesGlobalListers) Clusters() cosmosstorageutils.GlobalLister[api.HCPOpenShiftCluster] {
	return &cosmosstorageutils.CosmosGlobalLister[api.HCPOpenShiftCluster, cosmosstorageutils.GenericDocument[api.HCPOpenShiftCluster]]{
		ContainerClient: g.resources,
		ResourceTypes:   []azcorearm.ResourceType{api.ClusterResourceType},
	}
}

func (g *cosmosResourcesGlobalListers) NodePools() cosmosstorageutils.GlobalLister[api.HCPOpenShiftClusterNodePool] {
	return &cosmosstorageutils.CosmosGlobalLister[api.HCPOpenShiftClusterNodePool, cosmosstorageutils.GenericDocument[api.HCPOpenShiftClusterNodePool]]{
		ContainerClient: g.resources,
		ResourceTypes:   []azcorearm.ResourceType{api.NodePoolResourceType},
	}
}

func (g *cosmosResourcesGlobalListers) ExternalAuths() cosmosstorageutils.GlobalLister[api.HCPOpenShiftClusterExternalAuth] {
	return &cosmosstorageutils.CosmosGlobalLister[api.HCPOpenShiftClusterExternalAuth, cosmosstorageutils.GenericDocument[api.HCPOpenShiftClusterExternalAuth]]{
		ContainerClient: g.resources,
		ResourceTypes:   []azcorearm.ResourceType{api.ExternalAuthResourceType},
	}
}

func (g *cosmosResourcesGlobalListers) ServiceProviderClusters() cosmosstorageutils.GlobalLister[api.ServiceProviderCluster] {
	return &cosmosstorageutils.CosmosGlobalLister[api.ServiceProviderCluster, cosmosstorageutils.GenericDocument[api.ServiceProviderCluster]]{
		ContainerClient: g.resources,
		ResourceTypes:   []azcorearm.ResourceType{api.ServiceProviderClusterResourceType},
	}
}

func (g *cosmosResourcesGlobalListers) ServiceProviderNodePools() cosmosstorageutils.GlobalLister[api.ServiceProviderNodePool] {
	return &cosmosstorageutils.CosmosGlobalLister[api.ServiceProviderNodePool, cosmosstorageutils.GenericDocument[api.ServiceProviderNodePool]]{
		ContainerClient: g.resources,
		ResourceTypes:   []azcorearm.ResourceType{api.ServiceProviderNodePoolResourceType},
	}
}

func (g *cosmosResourcesGlobalListers) SystemAdminCredentialRequests() cosmosstorageutils.GlobalLister[api.SystemAdminCredentialRequest] {
	return &cosmosstorageutils.CosmosGlobalLister[api.SystemAdminCredentialRequest, cosmosstorageutils.GenericDocument[api.SystemAdminCredentialRequest]]{
		ContainerClient: g.resources,
		ResourceTypes:   []azcorearm.ResourceType{api.SystemAdminCredentialRequestResourceType},
	}
}

func (g *cosmosResourcesGlobalListers) SystemAdminCredentialRevocations() cosmosstorageutils.GlobalLister[api.SystemAdminCredentialRevocation] {
	return &cosmosstorageutils.CosmosGlobalLister[api.SystemAdminCredentialRevocation, cosmosstorageutils.GenericDocument[api.SystemAdminCredentialRevocation]]{
		ContainerClient: g.resources,
		ResourceTypes:   []azcorearm.ResourceType{api.SystemAdminCredentialRevocationResourceType},
	}
}

func (g *cosmosResourcesGlobalListers) Controllers() cosmosstorageutils.GlobalLister[api.Controller] {
	return &cosmosstorageutils.CosmosGlobalLister[api.Controller, cosmosstorageutils.GenericDocument[api.Controller]]{
		ContainerClient: g.resources,
		ResourceTypes: []azcorearm.ResourceType{
			api.ClusterControllerResourceType,
			api.NodePoolControllerResourceType,
			api.ExternalAuthControllerResourceType,
			api.SystemAdminCredentialRequestControllerResourceType,
			api.SystemAdminCredentialRevocationControllerResourceType,
		},
	}
}

func (g *cosmosResourcesGlobalListers) ManagementClusterContents() cosmosstorageutils.GlobalLister[api.ManagementClusterContent] {
	return &cosmosstorageutils.CosmosGlobalLister[api.ManagementClusterContent, cosmosstorageutils.GenericDocument[api.ManagementClusterContent]]{
		ContainerClient: g.resources,
		ResourceTypes: []azcorearm.ResourceType{
			api.ClusterScopedManagementClusterContentResourceType,
			api.NodePoolScopedManagementClusterContentResourceType,
		},
	}
}

func (g *cosmosResourcesGlobalListers) Operations() cosmosstorageutils.GlobalLister[api.Operation] {
	return &cosmosstorageutils.CosmosGlobalLister[api.Operation, cosmosstorageutils.GenericDocument[api.Operation]]{
		ContainerClient: g.resources,
		ResourceTypes:   []azcorearm.ResourceType{api.OperationStatusResourceType},
	}
}

func (g *cosmosResourcesGlobalListers) ActiveOperations() cosmosstorageutils.GlobalLister[api.Operation] {
	return &cosmosActiveOperationsGlobalLister{
		containerClient: g.resources,
	}
}

// cosmosActiveOperationsGlobalLister lists operations with non-terminal status
// across all partitions.
type cosmosActiveOperationsGlobalLister struct {
	containerClient *azcosmos.ContainerClient
}

func (l *cosmosActiveOperationsGlobalLister) List(ctx context.Context, options *cosmosstorageutils.DBClientListResourceDocsOptions) (cosmosstorageutils.DBClientIterator[api.Operation], error) {
	query := fmt.Sprintf(
		"SELECT * FROM c WHERE STRINGEQUALS(c.resourceType, %q, true) "+
			"AND LENGTH(c.resourceID) > 0 "+
			"AND (NOT IS_DEFINED(c.deletionTimestamp)) "+
			"AND NOT ARRAYCONTAINS([%q, %q, %q], c.properties.status)",
		api.OperationStatusResourceType.String(),
		arm.ProvisioningStateSucceeded,
		arm.ProvisioningStateFailed,
		arm.ProvisioningStateCanceled)

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
		return cosmosstorageutils.NewQueryResourcesSinglePageIterator[api.Operation, cosmosstorageutils.GenericDocument[api.Operation]](pager), nil
	}
	return cosmosstorageutils.NewQueryResourcesIterator[api.Operation, cosmosstorageutils.GenericDocument[api.Operation]](pager), nil
}
