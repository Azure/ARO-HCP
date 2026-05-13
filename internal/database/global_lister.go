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
	"strings"

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	resourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources"
	armresourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources/arm"
)

// GlobalLister lists all resources of a particular type across all partitions.
type GlobalLister[T any] interface {
	List(ctx context.Context, options *DBClientListResourceDocsOptions) (DBClientIterator[T], error)
}

// ResourcesGlobalListers provides access to global listers for each resource type.
// These are intended to feed SharedInformers via ListerWatchers.
type ResourcesGlobalListers interface {
	Subscriptions() GlobalLister[armresourcesapi.Subscription]
	Clusters() GlobalLister[resourcesapi.HCPOpenShiftCluster]
	NodePools() GlobalLister[resourcesapi.HCPOpenShiftClusterNodePool]
	ExternalAuths() GlobalLister[resourcesapi.HCPOpenShiftClusterExternalAuth]
	ServiceProviderClusters() GlobalLister[resourcesapi.ServiceProviderCluster]
	ServiceProviderNodePools() GlobalLister[resourcesapi.ServiceProviderNodePool]
	Controllers() GlobalLister[resourcesapi.Controller]
	// ManagementClusterContents lists ManagementClusterContent documents across
	// partitions for every Cosmos resource type where managementClusterContents
	// is nested as a direct child resource. Those types are registered on the lister implementation.
	ManagementClusterContents() GlobalLister[resourcesapi.ManagementClusterContent]
	Operations() GlobalLister[resourcesapi.Operation]
	ActiveOperations() GlobalLister[resourcesapi.Operation]
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

func (g *cosmosResourcesGlobalListers) Subscriptions() GlobalLister[armresourcesapi.Subscription] {
	return &cosmosGlobalLister[armresourcesapi.Subscription, GenericDocument[armresourcesapi.Subscription]]{
		containerClient: g.resources,
		resourceType:    azcorearm.SubscriptionResourceType,
	}
}

func (g *cosmosResourcesGlobalListers) Clusters() GlobalLister[resourcesapi.HCPOpenShiftCluster] {
	return &cosmosGlobalLister[resourcesapi.HCPOpenShiftCluster, HCPCluster]{
		containerClient: g.resources,
		resourceType:    resourcesapi.ClusterResourceType,
	}
}

func (g *cosmosResourcesGlobalListers) NodePools() GlobalLister[resourcesapi.HCPOpenShiftClusterNodePool] {
	return &cosmosGlobalLister[resourcesapi.HCPOpenShiftClusterNodePool, NodePool]{
		containerClient: g.resources,
		resourceType:    resourcesapi.NodePoolResourceType,
	}
}

func (g *cosmosResourcesGlobalListers) ExternalAuths() GlobalLister[resourcesapi.HCPOpenShiftClusterExternalAuth] {
	return &cosmosGlobalLister[resourcesapi.HCPOpenShiftClusterExternalAuth, ExternalAuth]{
		containerClient: g.resources,
		resourceType:    resourcesapi.ExternalAuthResourceType,
	}
}

func (g *cosmosResourcesGlobalListers) ServiceProviderClusters() GlobalLister[resourcesapi.ServiceProviderCluster] {
	return &cosmosGlobalLister[resourcesapi.ServiceProviderCluster, GenericDocument[resourcesapi.ServiceProviderCluster]]{
		containerClient: g.resources,
		resourceType:    resourcesapi.ServiceProviderClusterResourceType,
	}
}

func (g *cosmosResourcesGlobalListers) ServiceProviderNodePools() GlobalLister[resourcesapi.ServiceProviderNodePool] {
	return &cosmosGlobalLister[resourcesapi.ServiceProviderNodePool, GenericDocument[resourcesapi.ServiceProviderNodePool]]{
		containerClient: g.resources,
		resourceType:    resourcesapi.ServiceProviderNodePoolResourceType,
	}
}

func (g *cosmosResourcesGlobalListers) Controllers() GlobalLister[resourcesapi.Controller] {
	return &cosmosControllerGlobalLister{
		containerClient: g.resources,
		controllerResourceTypes: []azcorearm.ResourceType{
			resourcesapi.ClusterControllerResourceType,
			resourcesapi.NodePoolControllerResourceType,
			resourcesapi.ExternalAuthControllerResourceType,
		},
	}
}

func (g *cosmosResourcesGlobalListers) ManagementClusterContents() GlobalLister[resourcesapi.ManagementClusterContent] {
	return &cosmosManagementClusterContentGlobalLister{
		containerClient: g.resources,
		managementClusterContentResourceTypes: []azcorearm.ResourceType{
			resourcesapi.ClusterScopedManagementClusterContentResourceType,
			resourcesapi.NodePoolScopedManagementClusterContentResourceType,
		},
	}
}

func (g *cosmosResourcesGlobalListers) Operations() GlobalLister[resourcesapi.Operation] {
	return &cosmosGlobalLister[resourcesapi.Operation, GenericDocument[resourcesapi.Operation]]{
		containerClient: g.resources,
		resourceType:    resourcesapi.OperationStatusResourceType,
	}
}

func (g *cosmosResourcesGlobalListers) ActiveOperations() GlobalLister[resourcesapi.Operation] {
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

func (l *cosmosActiveOperationsGlobalLister) List(ctx context.Context, options *DBClientListResourceDocsOptions) (DBClientIterator[resourcesapi.Operation], error) {
	query := fmt.Sprintf(
		"SELECT * FROM c WHERE STRINGEQUALS(c.resourceType, %q, true) "+
			"AND NOT ARRAYCONTAINS([%q, %q, %q], c.properties.status)",
		resourcesapi.OperationStatusResourceType.String(),
		armresourcesapi.ProvisioningStateSucceeded,
		armresourcesapi.ProvisioningStateFailed,
		armresourcesapi.ProvisioningStateCanceled)

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
		return newQueryResourcesSinglePageIterator[resourcesapi.Operation, GenericDocument[resourcesapi.Operation]](pager), nil
	}
	return newQueryResourcesIterator[resourcesapi.Operation, GenericDocument[resourcesapi.Operation]](pager), nil
}

// cosmosControllerGlobalLister lists all controllers of specified resource types across all partitions.
type cosmosControllerGlobalLister struct {
	containerClient         *azcosmos.ContainerClient
	controllerResourceTypes []azcorearm.ResourceType
}

func (l *cosmosControllerGlobalLister) List(ctx context.Context, options *DBClientListResourceDocsOptions) (DBClientIterator[resourcesapi.Controller], error) {
	var resourceTypeConditions []string
	for _, resourceType := range l.controllerResourceTypes {
		resourceTypeConditions = append(resourceTypeConditions, fmt.Sprintf("STRINGEQUALS(c.resourceType, %q, true)", resourceType.String()))
	}
	whereClause := strings.Join(resourceTypeConditions, " OR ")
	query := fmt.Sprintf("SELECT * FROM c WHERE %s", whereClause)

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
		return newQueryResourcesSinglePageIterator[resourcesapi.Controller, GenericDocument[resourcesapi.Controller]](pager), nil
	}
	return newQueryResourcesIterator[resourcesapi.Controller, GenericDocument[resourcesapi.Controller]](pager), nil
}

// cosmosBillingGlobalLister lists all billing documents across all partitions.
type cosmosBillingGlobalLister struct {
	containerClient *azcosmos.ContainerClient
}

func (l *cosmosBillingGlobalLister) List(ctx context.Context, options *DBClientListResourceDocsOptions) (DBClientIterator[BillingDocument], error) {
	query := "SELECT * FROM c"

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
		return newQueryBillingSinglePageIterator(pager), nil
	}
	return newQueryBillingIterator(pager), nil
}

// cosmosManagementClusterContentGlobalLister lists managementClusterContents whether nested under a
// cluster or under a node pool.
type cosmosManagementClusterContentGlobalLister struct {
	containerClient                       *azcosmos.ContainerClient
	managementClusterContentResourceTypes []azcorearm.ResourceType
}

func (l *cosmosManagementClusterContentGlobalLister) List(ctx context.Context, options *DBClientListResourceDocsOptions) (DBClientIterator[resourcesapi.ManagementClusterContent], error) {
	var resourceTypeConditions []string
	for _, resourceType := range l.managementClusterContentResourceTypes {
		resourceTypeConditions = append(resourceTypeConditions, fmt.Sprintf("STRINGEQUALS(c.resourceType, %q, true)", resourceType.String()))
	}
	whereClause := strings.Join(resourceTypeConditions, " OR ")
	query := fmt.Sprintf("SELECT * FROM c WHERE %s", whereClause)

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
		return newQueryResourcesSinglePageIterator[resourcesapi.ManagementClusterContent, GenericDocument[resourcesapi.ManagementClusterContent]](pager), nil
	}
	return newQueryResourcesIterator[resourcesapi.ManagementClusterContent, GenericDocument[resourcesapi.ManagementClusterContent]](pager), nil
}
