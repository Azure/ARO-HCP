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

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// GlobalLister lists all resources of a particular type across all partitions.
type GlobalLister[T any] interface {
	List(ctx context.Context, options *DBClientListResourceDocsOptions) (DBClientIterator[T], error)
}

// ResourcesGlobalListers provides access to global listers for each resource type.
// These are intended to feed SharedInformers via ListerWatchers.
type ResourcesGlobalListers interface {
	Subscriptions() GlobalLister[arm.Subscription]
	Clusters() GlobalLister[api.HCPOpenShiftCluster]
	NodePools() GlobalLister[api.HCPOpenShiftClusterNodePool]
	ExternalAuths() GlobalLister[api.HCPOpenShiftClusterExternalAuth]
	ServiceProviderClusters() GlobalLister[api.ServiceProviderCluster]
	ServiceProviderNodePools() GlobalLister[api.ServiceProviderNodePool]
	Controllers() GlobalLister[api.Controller]
	// ManagementClusterContents lists ManagementClusterContent documents across
	// partitions for every Cosmos resource type where managementClusterContents
	// is nested as a direct child resource. Those types are registered on the lister implementation.
	ManagementClusterContents() GlobalLister[api.ManagementClusterContent]
	SystemAdminCredentialRequests() GlobalLister[api.SystemAdminCredentialRequest]
	SystemAdminCredentialRevocations() GlobalLister[api.SystemAdminCredentialRevocation]
	Operations() GlobalLister[api.Operation]
	ActiveOperations() GlobalLister[api.Operation]
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

func (g *cosmosResourcesGlobalListers) Subscriptions() GlobalLister[arm.Subscription] {
	return &cosmosGlobalLister[arm.Subscription, GenericDocument[arm.Subscription]]{
		containerClient: g.resources,
		resourceTypes:   []azcorearm.ResourceType{azcorearm.SubscriptionResourceType},
	}
}

func (g *cosmosResourcesGlobalListers) Clusters() GlobalLister[api.HCPOpenShiftCluster] {
	return &cosmosGlobalLister[api.HCPOpenShiftCluster, GenericDocument[api.HCPOpenShiftCluster]]{
		containerClient: g.resources,
		resourceTypes:   []azcorearm.ResourceType{api.ClusterResourceType},
	}
}

func (g *cosmosResourcesGlobalListers) NodePools() GlobalLister[api.HCPOpenShiftClusterNodePool] {
	return &cosmosGlobalLister[api.HCPOpenShiftClusterNodePool, GenericDocument[api.HCPOpenShiftClusterNodePool]]{
		containerClient: g.resources,
		resourceTypes:   []azcorearm.ResourceType{api.NodePoolResourceType},
	}
}

func (g *cosmosResourcesGlobalListers) ExternalAuths() GlobalLister[api.HCPOpenShiftClusterExternalAuth] {
	return &cosmosGlobalLister[api.HCPOpenShiftClusterExternalAuth, GenericDocument[api.HCPOpenShiftClusterExternalAuth]]{
		containerClient: g.resources,
		resourceTypes:   []azcorearm.ResourceType{api.ExternalAuthResourceType},
	}
}

func (g *cosmosResourcesGlobalListers) ServiceProviderClusters() GlobalLister[api.ServiceProviderCluster] {
	return &cosmosGlobalLister[api.ServiceProviderCluster, GenericDocument[api.ServiceProviderCluster]]{
		containerClient: g.resources,
		resourceTypes:   []azcorearm.ResourceType{api.ServiceProviderClusterResourceType},
	}
}

func (g *cosmosResourcesGlobalListers) ServiceProviderNodePools() GlobalLister[api.ServiceProviderNodePool] {
	return &cosmosGlobalLister[api.ServiceProviderNodePool, GenericDocument[api.ServiceProviderNodePool]]{
		containerClient: g.resources,
		resourceTypes:   []azcorearm.ResourceType{api.ServiceProviderNodePoolResourceType},
	}
}

func (g *cosmosResourcesGlobalListers) SystemAdminCredentialRequests() GlobalLister[api.SystemAdminCredentialRequest] {
	return &cosmosGlobalLister[api.SystemAdminCredentialRequest, GenericDocument[api.SystemAdminCredentialRequest]]{
		containerClient: g.resources,
		resourceTypes:   []azcorearm.ResourceType{api.SystemAdminCredentialRequestResourceType},
	}
}

func (g *cosmosResourcesGlobalListers) SystemAdminCredentialRevocations() GlobalLister[api.SystemAdminCredentialRevocation] {
	return &cosmosGlobalLister[api.SystemAdminCredentialRevocation, GenericDocument[api.SystemAdminCredentialRevocation]]{
		containerClient: g.resources,
		resourceTypes:   []azcorearm.ResourceType{api.SystemAdminCredentialRevocationResourceType},
	}
}

func (g *cosmosResourcesGlobalListers) Controllers() GlobalLister[api.Controller] {
	return &cosmosGlobalLister[api.Controller, GenericDocument[api.Controller]]{
		containerClient: g.resources,
		resourceTypes: []azcorearm.ResourceType{
			api.ClusterControllerResourceType,
			api.NodePoolControllerResourceType,
			api.ExternalAuthControllerResourceType,
			api.SystemAdminCredentialRequestControllerResourceType,
		},
	}
}

func (g *cosmosResourcesGlobalListers) ManagementClusterContents() GlobalLister[api.ManagementClusterContent] {
	return &cosmosGlobalLister[api.ManagementClusterContent, GenericDocument[api.ManagementClusterContent]]{
		containerClient: g.resources,
		resourceTypes: []azcorearm.ResourceType{
			api.ClusterScopedManagementClusterContentResourceType,
			api.NodePoolScopedManagementClusterContentResourceType,
		},
	}
}

func (g *cosmosResourcesGlobalListers) Operations() GlobalLister[api.Operation] {
	return &cosmosGlobalLister[api.Operation, GenericDocument[api.Operation]]{
		containerClient: g.resources,
		resourceTypes:   []azcorearm.ResourceType{api.OperationStatusResourceType},
	}
}

func (g *cosmosResourcesGlobalListers) ActiveOperations() GlobalLister[api.Operation] {
	return &cosmosActiveOperationsGlobalLister{
		containerClient: g.resources,
	}
}

// cosmosActiveOperationsGlobalLister lists operations with non-terminal status
// across all partitions.
type cosmosActiveOperationsGlobalLister struct {
	containerClient *azcosmos.ContainerClient
}

func (l *cosmosActiveOperationsGlobalLister) List(ctx context.Context, options *DBClientListResourceDocsOptions) (DBClientIterator[api.Operation], error) {
	query := fmt.Sprintf(
		"SELECT * FROM c WHERE STRINGEQUALS(c.resourceType, %q, true) "+
			"AND LENGTH(c.resourceID) > 0 "+
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
		return newQueryResourcesSinglePageIterator[api.Operation, GenericDocument[api.Operation]](pager), nil
	}
	return newQueryResourcesIterator[api.Operation, GenericDocument[api.Operation]](pager), nil
}

// cosmosGlobalLister lists documents whose resourceType matches any of resourceTypes.
// An empty partitionKey triggers a cross-partition query; a non-empty value scopes the query
// to that single partition. The query also filters out partition-header documents that lack
// a resourceID — the kube-applier container has no such documents, so the filter is a no-op
// there, while the Resources container relies on it.
type cosmosGlobalLister[InternalAPIType, CosmosAPIType any] struct {
	containerClient *azcosmos.ContainerClient
	resourceTypes   []azcorearm.ResourceType
	partitionKey    string
}

func (l *cosmosGlobalLister[InternalAPIType, CosmosAPIType]) List(ctx context.Context, options *DBClientListResourceDocsOptions) (DBClientIterator[InternalAPIType], error) {
	var resourceTypeConditions []string
	for _, resourceType := range l.resourceTypes {
		resourceTypeConditions = append(resourceTypeConditions, fmt.Sprintf("STRINGEQUALS(c.resourceType, %q, true)", resourceType.String()))
	}
	whereClause := strings.Join(resourceTypeConditions, " OR ")
	query := fmt.Sprintf("SELECT * FROM c WHERE LENGTH(c.resourceID) > 0 AND (%s)", whereClause)

	queryOptions := azcosmos.QueryOptions{
		PageSizeHint: -1,
	}
	if options != nil {
		if options.PageSizeHint != nil {
			queryOptions.PageSizeHint = max(*options.PageSizeHint, -1)
		}
		queryOptions.ContinuationToken = options.ContinuationToken
	}

	var partitionKey azcosmos.PartitionKey
	if l.partitionKey == "" {
		partitionKey = azcosmos.NewPartitionKey()
	} else {
		partitionKey = azcosmos.NewPartitionKeyString(l.partitionKey)
	}
	pager := l.containerClient.NewQueryItemsPager(query, partitionKey, &queryOptions)

	if options != nil && ptr.Deref(options.PageSizeHint, -1) > 0 {
		return newQueryResourcesSinglePageIterator[InternalAPIType, CosmosAPIType](pager), nil
	}
	return newQueryResourcesIterator[InternalAPIType, CosmosAPIType](pager), nil
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
