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

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// GlobalLister lists all resources of a particular type across all partitions.
type GlobalLister[T any] interface {
	List(ctx context.Context, options *DBClientListResourceDocsOptions) (DBClientIterator[T], error)
}

// GlobalListers provides access to global listers for each resource type.
// These are intended to feed SharedInformers via ListerWatchers.
type GlobalListers interface {
	Subscriptions() GlobalLister[arm.Subscription]
	Clusters() GlobalLister[api.HCPOpenShiftCluster]
	NodePools() GlobalLister[api.HCPOpenShiftClusterNodePool]
	ExternalAuths() GlobalLister[api.HCPOpenShiftClusterExternalAuth]
	ServiceProviderClusters() GlobalLister[api.ServiceProviderCluster]
	Operations() GlobalLister[api.Operation]
	ActiveOperations() GlobalLister[api.Operation]
}

// cosmosGlobalListers implements GlobalListers using a Cosmos DB container client.
type cosmosGlobalListers struct {
	resources *azcosmos.ContainerClient
}

var _ GlobalListers = &cosmosGlobalListers{}

func NewCosmosGlobalListers(resources *azcosmos.ContainerClient) GlobalListers {
	return &cosmosGlobalListers{resources: resources}
}

func (g *cosmosGlobalListers) Subscriptions() GlobalLister[arm.Subscription] {
	return &cosmosGlobalLister[arm.Subscription, Subscription]{
		containerClient: g.resources,
		resourceType:    azcorearm.SubscriptionResourceType,
	}
}

func (g *cosmosGlobalListers) Clusters() GlobalLister[api.HCPOpenShiftCluster] {
	return &cosmosGlobalLister[api.HCPOpenShiftCluster, HCPCluster]{
		containerClient: g.resources,
		resourceType:    api.ClusterResourceType,
	}
}

func (g *cosmosGlobalListers) NodePools() GlobalLister[api.HCPOpenShiftClusterNodePool] {
	return &cosmosGlobalLister[api.HCPOpenShiftClusterNodePool, NodePool]{
		containerClient: g.resources,
		resourceType:    api.NodePoolResourceType,
	}
}

func (g *cosmosGlobalListers) ExternalAuths() GlobalLister[api.HCPOpenShiftClusterExternalAuth] {
	return &cosmosGlobalLister[api.HCPOpenShiftClusterExternalAuth, ExternalAuth]{
		containerClient: g.resources,
		resourceType:    api.ExternalAuthResourceType,
	}
}

func (g *cosmosGlobalListers) ServiceProviderClusters() GlobalLister[api.ServiceProviderCluster] {
	return &cosmosGlobalLister[api.ServiceProviderCluster, GenericDocument[api.ServiceProviderCluster]]{
		containerClient: g.resources,
		resourceType:    api.ServiceProviderClusterResourceType,
	}
}

func (g *cosmosGlobalListers) Operations() GlobalLister[api.Operation] {
	return &cosmosGlobalLister[api.Operation, Operation]{
		containerClient: g.resources,
		resourceType:    api.OperationStatusResourceType,
	}
}

func (g *cosmosGlobalListers) ActiveOperations() GlobalLister[api.Operation] {
	return &cosmosActiveOperationsGlobalLister{
		containerClient: g.resources,
	}
}

// cosmosGlobalLister is a generic cross-partition lister for a single resource type.
type cosmosGlobalLister[InternalAPIType, CosmosAPIType any] struct {
	containerClient *azcosmos.ContainerClient
	resourceType    azcorearm.ResourceType
}

func (l *cosmosGlobalLister[InternalAPIType, CosmosAPIType]) List(ctx context.Context, options *DBClientListResourceDocsOptions) (DBClientIterator[InternalAPIType], error) {
	// Empty partition key string triggers cross-partition query, nil prefix lists all.
	return list[InternalAPIType, CosmosAPIType](ctx, l.containerClient, "", &l.resourceType, nil, options, false)
}

// cosmosActiveOperationsGlobalLister lists operations with non-terminal status
// across all partitions.
type cosmosActiveOperationsGlobalLister struct {
	containerClient *azcosmos.ContainerClient
}

func (l *cosmosActiveOperationsGlobalLister) List(ctx context.Context, options *DBClientListResourceDocsOptions) (DBClientIterator[api.Operation], error) {
	query := fmt.Sprintf(
		"SELECT * FROM c WHERE STRINGEQUALS(c.resourceType, %q, true) "+
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
		return newQueryResourcesSinglePageIterator[api.Operation, Operation](pager), nil
	}
	return newQueryResourcesIterator[api.Operation, Operation](pager), nil
}
