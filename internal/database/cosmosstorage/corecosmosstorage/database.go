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

package corecosmosstorage

import (
	"context"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const resourcesContainer = "Resources"

// ResourcesDBClientListActiveOperationDocsOptions allows for limiting the results of ResourcesDBClient.ListActiveOperationDocs.
type ResourcesDBClientListActiveOperationDocsOptions struct {
	// Request matches the type of asynchronous operation requested
	Request *cosmosstorageutils.OperationRequest
	// ExternalID matches (case-insensitively) the Azure resource ID of the cluster or node pool
	ExternalID *azcorearm.ResourceID
	// IncludeNestedResources includes nested resources under ExternalID
	IncludeNestedResources bool
	// IncludeTerminal includes operations in terminal states (Succeeded, Failed, Canceled)
	IncludeTerminal bool
}

// ResourcesDBClient provides a customized interface to the Cosmos DB containers used by the
// ARO-HCP resource provider.
type ResourcesDBClient interface {
	// NewTransaction initiates a new transactional batch for the given partition key.
	NewTransaction(pk string) cosmosstorageutils.DBTransaction

	// UntypedCRUD provides access documents in the subscription
	UntypedCRUD(parentResourceID azcorearm.ResourceID) (cosmosstorageutils.UntypedResourceCRUD, error)

	// HCPClusters retrieves a CRUD interface for managing HCPCluster resources and their nested resources.
	HCPClusters(subscriptionID, resourceGroupName string) HCPClusterCRUD

	// Operations retrieves a CRUD interface for managing operations.  Remember that operations are not directly accessible
	// to end users via ARM.  They must also survive the thing they are deleting, so they live under a subscription directly.
	Operations(subscriptionID string) OperationCRUD

	Subscriptions() cosmosstorageutils.ResourceCRUD[coreapi.Subscription, *coreapi.Subscription]

	ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName string) cosmosstorageutils.ResourceCRUD[coreapi.ServiceProviderCluster, *coreapi.ServiceProviderCluster]

	// ListMissingResourceID returns documents in the Resources container that lack a resourceID field.
	// These are typically old records created before the resourceID field was introduced.
	// The returned iterator yields raw TypedDocuments without CosmosToInternal conversion.
	ListMissingResourceID(ctx context.Context, options *cosmosstorageutils.DBClientListResourceDocsOptions) (cosmosstorageutils.DBClientIterator[cosmosstorageutils.TypedDocument], error)

	// ResourcesGlobalListers returns interfaces for listing ARM resource documents across all partitions
	// (Resources container only), intended for feeding SharedInformers.
	ResourcesGlobalListers() ResourcesGlobalListers

	ServiceProviderNodePools(subscriptionID, resourceGroupName, clusterName, nodePoolName string) cosmosstorageutils.ResourceCRUD[coreapi.ServiceProviderNodePool, *coreapi.ServiceProviderNodePool]

	cosmosstorageutils.ChangeFeedClient
}

var _ ResourcesDBClient = &resourcesCosmosDBClient{}

// resourcesCosmosDBClient defines the needed values to perform CRUD operations against Cosmos DB.
type resourcesCosmosDBClient struct {
	database  *azcosmos.DatabaseClient
	resources *azcosmos.ContainerClient
}

// NewResourcesDBClient instantiates a ResourcesDBClient from a Cosmos DatabaseClient instance
// targeting the Frontends async database (Resources container).
func NewResourcesDBClient(database *azcosmos.DatabaseClient) (ResourcesDBClient, error) {
	resources, err := database.NewContainer(resourcesContainer)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	return &resourcesCosmosDBClient{
		database:  database,
		resources: resources,
	}, nil
}

func (d *resourcesCosmosDBClient) NewTransaction(pk string) cosmosstorageutils.DBTransaction {
	return newCosmosDBTransaction(pk, d.resources)
}

func (d *resourcesCosmosDBClient) HCPClusters(subscriptionID, resourceGroupName string) HCPClusterCRUD {
	return NewHCPClusterCRUD(d.resources, subscriptionID, resourceGroupName)
}

func (d *resourcesCosmosDBClient) Operations(subscriptionID string) OperationCRUD {
	return NewOperationCRUD(d.resources, subscriptionID)
}

func (d *resourcesCosmosDBClient) Subscriptions() cosmosstorageutils.ResourceCRUD[coreapi.Subscription, *coreapi.Subscription] {
	return cosmosstorageutils.NewCosmosResourceCRUD[coreapi.Subscription, *coreapi.Subscription, cosmosstorageutils.GenericDocument[coreapi.Subscription]](
		d.resources, nil, azcorearm.SubscriptionResourceType)
}

func (d *resourcesCosmosDBClient) ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName string) cosmosstorageutils.ResourceCRUD[coreapi.ServiceProviderCluster, *coreapi.ServiceProviderCluster] {
	clusterResourceID := metadataapi.Must(coreapi.ToClusterResourceID(subscriptionID, resourceGroupName, clusterName))
	return cosmosstorageutils.NewCosmosResourceCRUD[coreapi.ServiceProviderCluster, *coreapi.ServiceProviderCluster, cosmosstorageutils.GenericDocument[coreapi.ServiceProviderCluster]](
		d.resources, clusterResourceID, coreapi.ServiceProviderClusterResourceType)
}

func (d *resourcesCosmosDBClient) ServiceProviderNodePools(subscriptionID, resourceGroupName, clusterName, nodePoolName string) cosmosstorageutils.ResourceCRUD[coreapi.ServiceProviderNodePool, *coreapi.ServiceProviderNodePool] {
	nodePoolResourceID := metadataapi.Must(coreapi.ToNodePoolResourceID(subscriptionID, resourceGroupName, clusterName, nodePoolName))
	return cosmosstorageutils.NewCosmosResourceCRUD[coreapi.ServiceProviderNodePool, *coreapi.ServiceProviderNodePool, cosmosstorageutils.GenericDocument[coreapi.ServiceProviderNodePool]](
		d.resources, nodePoolResourceID, coreapi.ServiceProviderNodePoolResourceType)
}

func (d *resourcesCosmosDBClient) UntypedCRUD(parentResourceID azcorearm.ResourceID) (cosmosstorageutils.UntypedResourceCRUD, error) {
	return cosmosstorageutils.NewUntypedCRUD(d.resources, parentResourceID), nil
}

func (d *resourcesCosmosDBClient) ListMissingResourceID(ctx context.Context, options *cosmosstorageutils.DBClientListResourceDocsOptions) (cosmosstorageutils.DBClientIterator[cosmosstorageutils.TypedDocument], error) {
	query := "SELECT * FROM c WHERE (NOT IS_DEFINED(c.resourceID) OR IS_NULL(c.resourceID))"

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
	pager := d.resources.NewQueryItemsPager(query, partitionKey, &queryOptions)

	if options != nil && ptr.Deref(options.PageSizeHint, -1) > 0 {
		return cosmosstorageutils.NewQueryTypedDocumentSinglePageIterator(pager), nil
	}
	return cosmosstorageutils.NewQueryTypedDocumentIterator(pager), nil
}

func (d *resourcesCosmosDBClient) ResourcesGlobalListers() ResourcesGlobalListers {
	return NewCosmosResourcesGlobalListers(d.resources)
}

func (d *resourcesCosmosDBClient) ReadChangeFeed(ctx context.Context, options *azcosmos.ChangeFeedOptions) (azcosmos.ChangeFeedResponse, error) {
	return d.resources.ReadChangeFeed(ctx, options)
}

func (d *resourcesCosmosDBClient) ReadFeedRanges(ctx context.Context, options *azcosmos.FeedRangesOptions) ([]azcosmos.FeedRange, error) {
	resourcesFeedRanges, err := d.resources.ReadFeedRanges(ctx, options)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	return resourcesFeedRanges, nil
}

// NewCosmosDatabaseClient instantiates a generic Cosmos database client.
func NewCosmosDatabaseClient(url string, dbName string, clientOptions azcore.ClientOptions) (*azcosmos.DatabaseClient, error) {
	credential, err := azidentity.NewDefaultAzureCredential(
		&azidentity.DefaultAzureCredentialOptions{
			ClientOptions:                clientOptions,
			RequireAzureTokenCredentials: true,
		})
	if err != nil {
		return nil, utils.TrackError(err)
	}

	client, err := azcosmos.NewClient(
		url,
		credential,
		&azcosmos.ClientOptions{
			ClientOptions: clientOptions,
		})
	if err != nil {
		return nil, utils.TrackError(err)
	}

	return client.NewDatabase(dbName)
}
