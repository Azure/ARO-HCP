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

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
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
	HCPResourceRequirements() cosmosstorageutils.ResourceCRUD[fleetapi.HCPResourceRequirements, *fleetapi.HCPResourceRequirements]
	GlobalListers() FleetGlobalListers
}

// StampsCRUD provides CRUD operations for stamps and access to their
// nested management cluster sub-resources.
type StampsCRUD interface {
	cosmosstorageutils.ValidatingResourceCRUD[fleetapi.Stamp, *fleetapi.Stamp]
	ManagementClusters(stampIdentifier string) ManagementClustersCRUD
}

// ManagementClustersCRUD provides CRUD operations for management clusters
// and access to their nested controller status and scheduling documents.
type ManagementClustersCRUD interface {
	cosmosstorageutils.ValidatingResourceCRUD[fleetapi.ManagementCluster, *fleetapi.ManagementCluster]
	Controllers() cosmosstorageutils.ResourceCRUD[coreapi.Controller, *coreapi.Controller]
	Scheduling() cosmosstorageutils.ResourceCRUD[fleetapi.ManagementClusterScheduling, *fleetapi.ManagementClusterScheduling]
}

// FleetGlobalListers provides cross-partition listers for fleet resource types.
type FleetGlobalListers interface {
	Stamps() cosmosstorageutils.GlobalLister[fleetapi.Stamp]
	ManagementClusters() cosmosstorageutils.GlobalLister[fleetapi.ManagementCluster]
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
	inner := cosmosstorageutils.NewCosmosResourceCRUDWithStrategies[fleetapi.Stamp, *fleetapi.Stamp, cosmosstorageutils.GenericDocument[fleetapi.Stamp]](
		c.container, nil, fleetapi.StampResourceType,
		cosmosstorageutils.FleetPartitionKeyDeriver{}, cosmosstorageutils.FleetResourceIDBuilder{})
	return &cosmosStampsCRUD{
		ValidatingResourceCRUD: cosmosstorageutils.NewValidatingCRUD(inner,
			validation.ValidateStampCreate,
			validation.ValidateStampUpdate,
		),
		containerClient: c.container,
	}
}

func (c *cosmosFleetDBClient) HCPResourceRequirements() cosmosstorageutils.ResourceCRUD[fleetapi.HCPResourceRequirements, *fleetapi.HCPResourceRequirements] {
	return cosmosstorageutils.NewCosmosResourceCRUDWithStrategies[fleetapi.HCPResourceRequirements, *fleetapi.HCPResourceRequirements, cosmosstorageutils.GenericDocument[fleetapi.HCPResourceRequirements]](
		c.container, nil, fleetapi.HCPResourceRequirementsResourceType,
		cosmosstorageutils.FleetPartitionKeyDeriver{}, cosmosstorageutils.FleetResourceIDBuilder{})
}

func (c *cosmosFleetDBClient) GlobalListers() FleetGlobalListers {
	return &cosmosFleetGlobalListers{container: c.container}
}

type cosmosStampsCRUD struct {
	cosmosstorageutils.ValidatingResourceCRUD[fleetapi.Stamp, *fleetapi.Stamp]
	containerClient *azcosmos.ContainerClient
}

func (s *cosmosStampsCRUD) ManagementClusters(stampIdentifier string) ManagementClustersCRUD {
	stampResourceID, err := fleetapi.ToStampResourceID(stampIdentifier)
	if err != nil {
		panic(fmt.Sprintf("invalid stamp identifier %q: %v", stampIdentifier, err))
	}
	inner := cosmosstorageutils.NewCosmosResourceCRUDWithStrategies[fleetapi.ManagementCluster, *fleetapi.ManagementCluster, cosmosstorageutils.GenericDocument[fleetapi.ManagementCluster]](
		s.containerClient, stampResourceID, fleetapi.ManagementClusterResourceType,
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
	cosmosstorageutils.ValidatingResourceCRUD[fleetapi.ManagementCluster, *fleetapi.ManagementCluster]
	containerClient *azcosmos.ContainerClient
	stampIdentifier string
}

func (m *cosmosManagementClustersCRUD) Controllers() cosmosstorageutils.ResourceCRUD[coreapi.Controller, *coreapi.Controller] {
	managementClusterResourceID, err := fleetapi.ToManagementClusterResourceID(m.stampIdentifier)
	if err != nil {
		panic(fmt.Sprintf("invalid stamp identifier %q: %v", m.stampIdentifier, err))
	}
	return cosmosstorageutils.NewCosmosResourceCRUDWithStrategies[coreapi.Controller, *coreapi.Controller, cosmosstorageutils.GenericDocument[coreapi.Controller]](
		m.containerClient, managementClusterResourceID, fleetapi.ManagementClusterControllerResourceType,
		cosmosstorageutils.FleetPartitionKeyDeriver{}, cosmosstorageutils.FleetResourceIDBuilder{})
}

func (m *cosmosManagementClustersCRUD) Scheduling() cosmosstorageutils.ResourceCRUD[fleetapi.ManagementClusterScheduling, *fleetapi.ManagementClusterScheduling] {
	managementClusterResourceID, err := fleetapi.ToManagementClusterResourceID(m.stampIdentifier)
	if err != nil {
		panic(fmt.Sprintf("invalid stamp identifier %q: %v", m.stampIdentifier, err))
	}
	return cosmosstorageutils.NewCosmosResourceCRUDWithStrategies[fleetapi.ManagementClusterScheduling, *fleetapi.ManagementClusterScheduling, cosmosstorageutils.GenericDocument[fleetapi.ManagementClusterScheduling]](
		m.containerClient, managementClusterResourceID, fleetapi.ManagementClusterSchedulingResourceType,
		cosmosstorageutils.FleetPartitionKeyDeriver{}, cosmosstorageutils.FleetResourceIDBuilder{})
}

type cosmosFleetGlobalListers struct {
	container *azcosmos.ContainerClient
}

var _ FleetGlobalListers = &cosmosFleetGlobalListers{}

func (g *cosmosFleetGlobalListers) Stamps() cosmosstorageutils.GlobalLister[fleetapi.Stamp] {
	return &cosmosstorageutils.CosmosGlobalLister[fleetapi.Stamp, cosmosstorageutils.GenericDocument[fleetapi.Stamp]]{
		ContainerClient: g.container,
		ResourceTypes:   []azcorearm.ResourceType{fleetapi.StampResourceType},
	}
}

func (g *cosmosFleetGlobalListers) ManagementClusters() cosmosstorageutils.GlobalLister[fleetapi.ManagementCluster] {
	return &cosmosstorageutils.CosmosGlobalLister[fleetapi.ManagementCluster, cosmosstorageutils.GenericDocument[fleetapi.ManagementCluster]]{
		ContainerClient: g.container,
		ResourceTypes:   []azcorearm.ResourceType{fleetapi.ManagementClusterResourceType},
	}
}
