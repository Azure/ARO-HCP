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

package fleetcosmosstorage

import (
	"context"
	"fmt"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/validation"
)

const fleetContainer = "Fleet"

// FleetDBClient is the database surface for the Fleet Cosmos container.
// It is intentionally separate from ResourcesDBClient because the Fleet
// container holds management cluster inventory data with its own access
// patterns and credential scoping.
type FleetDBClient interface {
	cosmosstorageutils.ChangeFeedClient
	Stamps() StampsCRUD
	GlobalListers() FleetGlobalListers
}

// StampsCRUD provides CRUD operations for stamps and access to their
// nested management cluster sub-resources.
type StampsCRUD interface {
	cosmosstorageutils.ValidatingResourceCRUD[fleet.Stamp, *fleet.Stamp]
	ManagementClusters(stampIdentifier string) ManagementClustersCRUD
}

// ManagementClustersCRUD provides CRUD operations for management clusters
// and access to their nested controller status documents.
type ManagementClustersCRUD interface {
	cosmosstorageutils.ValidatingResourceCRUD[fleet.ManagementCluster, *fleet.ManagementCluster]
	Controllers() cosmosstorageutils.ResourceCRUD[api.Controller, *api.Controller]
}

// FleetGlobalListers provides cross-partition listers for fleet resource types.
type FleetGlobalListers interface {
	Stamps() cosmosstorageutils.GlobalLister[fleet.Stamp]
	ManagementClusters() cosmosstorageutils.GlobalLister[fleet.ManagementCluster]
}

type cosmosFleetDBClient struct {
	container *azcosmos.ContainerClient
}

var _ FleetDBClient = &cosmosFleetDBClient{}

// NewFleetDBClient instantiates a FleetDBClient from a Cosmos DatabaseClient.
func NewFleetDBClient(database *azcosmos.DatabaseClient) (FleetDBClient, error) {
	container, err := database.NewContainer(fleetContainer)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	return &cosmosFleetDBClient{container: container}, nil
}

// NewFleetDBClientFromContainer wraps an already-opened container client.
func NewFleetDBClientFromContainer(container *azcosmos.ContainerClient) FleetDBClient {
	return &cosmosFleetDBClient{container: container}
}

func (c *cosmosFleetDBClient) ReadChangeFeed(ctx context.Context, options *azcosmos.ChangeFeedOptions) (azcosmos.ChangeFeedResponse, error) {
	return c.container.ReadChangeFeed(ctx, options)
}

func (c *cosmosFleetDBClient) ReadFeedRanges(ctx context.Context, options *azcosmos.FeedRangesOptions) ([]azcosmos.FeedRange, error) {
	return c.container.ReadFeedRanges(ctx, options)
}

func (c *cosmosFleetDBClient) Stamps() StampsCRUD {
	inner := cosmosstorageutils.NewCosmosResourceCRUDWithStrategies[fleet.Stamp, *fleet.Stamp, cosmosstorageutils.GenericDocument[fleet.Stamp]](
		c.container, nil, fleet.StampResourceType,
		cosmosstorageutils.FleetPartitionKeyDeriver{}, cosmosstorageutils.FleetResourceIDBuilder{})
	return &cosmosStampsCRUD{
		ValidatingResourceCRUD: cosmosstorageutils.NewValidatingCRUD(inner,
			validation.ValidateStampCreate,
			validation.ValidateStampUpdate,
		),
		containerClient: c.container,
	}
}

func (c *cosmosFleetDBClient) GlobalListers() FleetGlobalListers {
	return &cosmosFleetGlobalListers{container: c.container}
}

type cosmosStampsCRUD struct {
	cosmosstorageutils.ValidatingResourceCRUD[fleet.Stamp, *fleet.Stamp]
	containerClient *azcosmos.ContainerClient
}

func (s *cosmosStampsCRUD) ManagementClusters(stampIdentifier string) ManagementClustersCRUD {
	stampResourceID, err := fleet.ToStampResourceID(stampIdentifier)
	if err != nil {
		panic(fmt.Sprintf("invalid stamp identifier %q: %v", stampIdentifier, err))
	}
	inner := cosmosstorageutils.NewCosmosResourceCRUDWithStrategies[fleet.ManagementCluster, *fleet.ManagementCluster, cosmosstorageutils.GenericDocument[fleet.ManagementCluster]](
		s.containerClient, stampResourceID, fleet.ManagementClusterResourceType,
		cosmosstorageutils.FleetPartitionKeyDeriver{}, cosmosstorageutils.FleetResourceIDBuilder{})
	return &cosmosManagementClustersCRUD{
		ValidatingResourceCRUD: cosmosstorageutils.NewValidatingCRUD(inner,
			validation.ValidateManagementClusterCreate,
			validation.ValidateManagementClusterUpdate,
		),
		containerClient: s.containerClient,
		stampIdentifier: stampIdentifier,
	}
}

type cosmosManagementClustersCRUD struct {
	cosmosstorageutils.ValidatingResourceCRUD[fleet.ManagementCluster, *fleet.ManagementCluster]
	containerClient *azcosmos.ContainerClient
	stampIdentifier string
}

func (m *cosmosManagementClustersCRUD) Controllers() cosmosstorageutils.ResourceCRUD[api.Controller, *api.Controller] {
	mcResourceID, err := fleet.ToManagementClusterResourceID(m.stampIdentifier)
	if err != nil {
		panic(fmt.Sprintf("invalid stamp identifier %q: %v", m.stampIdentifier, err))
	}
	return cosmosstorageutils.NewCosmosResourceCRUDWithStrategies[api.Controller, *api.Controller, cosmosstorageutils.GenericDocument[api.Controller]](
		m.containerClient, mcResourceID, fleet.ManagementClusterControllerResourceType,
		cosmosstorageutils.FleetPartitionKeyDeriver{}, cosmosstorageutils.FleetResourceIDBuilder{})
}

type cosmosFleetGlobalListers struct {
	container *azcosmos.ContainerClient
}

var _ FleetGlobalListers = &cosmosFleetGlobalListers{}

func (g *cosmosFleetGlobalListers) Stamps() cosmosstorageutils.GlobalLister[fleet.Stamp] {
	return &cosmosstorageutils.CosmosGlobalLister[fleet.Stamp, cosmosstorageutils.GenericDocument[fleet.Stamp]]{
		ContainerClient: g.container,
		ResourceTypes:   []azcorearm.ResourceType{fleet.StampResourceType},
	}
}

func (g *cosmosFleetGlobalListers) ManagementClusters() cosmosstorageutils.GlobalLister[fleet.ManagementCluster] {
	return &cosmosstorageutils.CosmosGlobalLister[fleet.ManagementCluster, cosmosstorageutils.GenericDocument[fleet.ManagementCluster]]{
		ContainerClient: g.container,
		ResourceTypes:   []azcorearm.ResourceType{fleet.ManagementClusterResourceType},
	}
}
