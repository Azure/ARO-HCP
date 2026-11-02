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

package azureresourcescosmosstorage

import (
	"context"
	"errors"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api/azureresourcesapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosclient"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosratelimit"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const azureResourcesContainer = "AzureResources"

// AzureResourcesDBClient is the database surface for the AzureResources Cosmos
// container. It is intentionally separate from corecosmosstorage.ResourcesDBClient
// so Azure-resource reconciliation state (today: DeploymentStackDesire) has its
// own container, RU budget, and lifecycle independent of ARM resource documents.
type AzureResourcesDBClient interface {
	cosmosstorageutils.ChangeFeedClient

	// DeploymentStackDesiresForCluster returns a CRUD scoped to a cluster parent.
	DeploymentStackDesiresForCluster(subscriptionID, resourceGroupName, clusterName string) (cosmosstorageutils.ResourceCRUD[azureresourcesapi.DeploymentStackDesire, *azureresourcesapi.DeploymentStackDesire], error)

	// DeploymentStackDesiresFor returns a DeploymentStackDesire CRUD scoped to the given parent.
	DeploymentStackDesiresFor(parent DeploymentStackDesireScope) (cosmosstorageutils.ResourceCRUD[azureresourcesapi.DeploymentStackDesire, *azureresourcesapi.DeploymentStackDesire], error)

	// UntypedCRUD walks the container by resourceID prefix, returning
	// cosmosstorageutils.TypedDocument rows for cross-cutting cleanup.
	UntypedCRUD(parentResourceID azcorearm.ResourceID) (cosmosstorageutils.UntypedResourceCRUD, error)

	// GlobalListers returns interfaces for listing AzureResources documents across
	// all partitions, intended for feeding SharedInformers.
	GlobalListers() AzureResourcesGlobalListers
}

// AzureResourcesGlobalListers provides access to global listers for each
// resource type in the AzureResources container.
type AzureResourcesGlobalListers interface {
	DeploymentStackDesires() cosmosstorageutils.GlobalLister[azureresourcesapi.DeploymentStackDesire]
}

// AzureResourcesDeploymentStackDesireCRUD is the narrow per-type peer interface that the
// azuredeploymentstackdesires controller takes as its database dependency. The controller
// keys off a parent resource ID, so the single parent-agnostic accessor is all it calls.
// AzureResourcesDBClient satisfies this; tests can also provide a one-method fake.
// Mirrors kubeappliercosmosstorage.KubeApplierApplyDesireCRUD.
type AzureResourcesDeploymentStackDesireCRUD interface {
	DeploymentStackDesiresFor(parent DeploymentStackDesireScope) (cosmosstorageutils.ResourceCRUD[azureresourcesapi.DeploymentStackDesire, *azureresourcesapi.DeploymentStackDesire], error)
}

type azureResourcesCosmosDBClient struct {
	container *azcosmos.ContainerClient
}

var _ AzureResourcesDBClient = &azureResourcesCosmosDBClient{}

// NewAzureResourcesDBClient creates an AzureResources client with its own Cosmos
// pipeline, bound to bucket for all CRUDs and feeds.
func NewAzureResourcesDBClient(url, databaseName string, options cosmosclient.Options, bucket *cosmosratelimit.TokenBucket) (AzureResourcesDBClient, error) {
	database, err := cosmosclient.NewCosmosDatabaseClient(url, databaseName, options, bucket)
	if err != nil {
		return nil, err
	}
	container, err := database.NewContainer(azureResourcesContainer)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	return &azureResourcesCosmosDBClient{container: container}, nil
}

func (c *azureResourcesCosmosDBClient) DeploymentStackDesiresForCluster(subscriptionID, resourceGroupName, clusterName string) (cosmosstorageutils.ResourceCRUD[azureresourcesapi.DeploymentStackDesire, *azureresourcesapi.DeploymentStackDesire], error) {
	parent, err := ClusterScope(subscriptionID, resourceGroupName, clusterName)
	if err != nil {
		return nil, err
	}
	return c.DeploymentStackDesiresFor(parent)
}

func (c *azureResourcesCosmosDBClient) DeploymentStackDesiresFor(parent DeploymentStackDesireScope) (cosmosstorageutils.ResourceCRUD[azureresourcesapi.DeploymentStackDesire, *azureresourcesapi.DeploymentStackDesire], error) {
	if parent.resourceID == nil {
		return nil, utils.TrackError(errors.New("deployment stack desire scope is not initialized"))
	}
	return cosmosstorageutils.NewCosmosResourceCRUDWithStrategies[azureresourcesapi.DeploymentStackDesire, *azureresourcesapi.DeploymentStackDesire, cosmosstorageutils.GenericDocument[azureresourcesapi.DeploymentStackDesire]](
		c.container, parent.resourceID, azureresourcesapi.DeploymentStackDesireResourceTypeForParent(parent.resourceID),
		cosmosstorageutils.SubscriptionPartitionKeyDeriver{}, cosmosstorageutils.ClusterNestedResourceIDBuilder{},
	), nil
}

func (c *azureResourcesCosmosDBClient) UntypedCRUD(parentResourceID azcorearm.ResourceID) (cosmosstorageutils.UntypedResourceCRUD, error) {
	return cosmosstorageutils.NewUntypedCRUD(c.container, parentResourceID), nil
}

func (c *azureResourcesCosmosDBClient) GlobalListers() AzureResourcesGlobalListers {
	return &cosmosAzureResourcesGlobalListers{container: c.container}
}

func (c *azureResourcesCosmosDBClient) ReadChangeFeed(ctx context.Context, options *azcosmos.ChangeFeedOptions) (azcosmos.ChangeFeedResponse, error) {
	return c.container.ReadChangeFeed(ctx, options)
}

func (c *azureResourcesCosmosDBClient) ReadFeedRanges(ctx context.Context, options *azcosmos.FeedRangesOptions) ([]azcosmos.FeedRange, error) {
	return c.container.ReadFeedRanges(ctx, options)
}

type cosmosAzureResourcesGlobalListers struct {
	container *azcosmos.ContainerClient
}

var _ AzureResourcesGlobalListers = &cosmosAzureResourcesGlobalListers{}

func (g *cosmosAzureResourcesGlobalListers) DeploymentStackDesires() cosmosstorageutils.GlobalLister[azureresourcesapi.DeploymentStackDesire] {
	return &cosmosstorageutils.CosmosGlobalLister[azureresourcesapi.DeploymentStackDesire, cosmosstorageutils.GenericDocument[azureresourcesapi.DeploymentStackDesire]]{
		ContainerClient: g.container,
		// Append when more desire scopes are added.
		ResourceTypes: []azcorearm.ResourceType{azureresourcesapi.ClusterScopedDeploymentStackDesireResourceType},
	}
}
