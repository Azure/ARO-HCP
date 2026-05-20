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
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/validation"
)

const fleetContainer = "Fleet"

// FleetDBClient is the database surface for the Fleet Cosmos container.
// It is intentionally separate from ResourcesDBClient because the Fleet
// container holds management cluster inventory data with its own access
// patterns and credential scoping.
type FleetDBClient interface {
	Stamps() StampsCRUD
	GlobalListers() FleetGlobalListers
}

// StampsCRUD provides CRUD operations for stamps and access to their
// nested management cluster sub-resources.
type StampsCRUD interface {
	ValidatingResourceCRUD[fleet.Stamp]
	ManagementClusters(stampIdentifier string) ManagementClustersCRUD
}

// ManagementClustersCRUD provides CRUD operations for management clusters
// and access to their nested controller status documents.
type ManagementClustersCRUD interface {
	ValidatingResourceCRUD[fleet.ManagementCluster]
	Controllers() ResourceCRUD[api.Controller]
}

// FleetGlobalListers provides cross-partition listers for fleet resource types.
type FleetGlobalListers interface {
	Stamps() GlobalLister[fleet.Stamp]
	ManagementClusters() GlobalLister[fleet.ManagementCluster]
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

func (c *cosmosFleetDBClient) Stamps() StampsCRUD {
	inner := &fleetResourceCRUD[fleet.Stamp, GenericDocument[fleet.Stamp]]{
		containerClient: c.container,
		resourceType:    fleet.StampResourceType,
	}
	return &cosmosStampsCRUD{
		ValidatingResourceCRUD: NewValidatingCRUD(inner,
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
	ValidatingResourceCRUD[fleet.Stamp]
	containerClient *azcosmos.ContainerClient
}

func (s *cosmosStampsCRUD) ManagementClusters(stampIdentifier string) ManagementClustersCRUD {
	stampResourceID, err := fleet.ToStampResourceID(stampIdentifier)
	if err != nil {
		panic(fmt.Sprintf("invalid stamp identifier %q: %v", stampIdentifier, err))
	}
	inner := &fleetResourceCRUD[fleet.ManagementCluster, GenericDocument[fleet.ManagementCluster]]{
		containerClient:  s.containerClient,
		parentResourceID: stampResourceID,
		resourceType:     fleet.ManagementClusterResourceType,
	}
	return &cosmosManagementClustersCRUD{
		ValidatingResourceCRUD: NewValidatingCRUD(inner,
			validation.ValidateManagementClusterCreate,
			validation.ValidateManagementClusterUpdate,
		),
		containerClient: s.containerClient,
		stampIdentifier: stampIdentifier,
	}
}

type cosmosManagementClustersCRUD struct {
	ValidatingResourceCRUD[fleet.ManagementCluster]
	containerClient *azcosmos.ContainerClient
	stampIdentifier string
}

func (m *cosmosManagementClustersCRUD) Controllers() ResourceCRUD[api.Controller] {
	mcResourceID, err := fleet.ToManagementClusterResourceID(m.stampIdentifier)
	if err != nil {
		panic(fmt.Sprintf("invalid stamp identifier %q: %v", m.stampIdentifier, err))
	}
	return &fleetResourceCRUD[api.Controller, GenericDocument[api.Controller]]{
		containerClient:  m.containerClient,
		parentResourceID: mcResourceID,
		resourceType:     fleet.ManagementClusterControllerResourceType,
	}
}

type cosmosFleetGlobalListers struct {
	container *azcosmos.ContainerClient
}

var _ FleetGlobalListers = &cosmosFleetGlobalListers{}

func (g *cosmosFleetGlobalListers) Stamps() GlobalLister[fleet.Stamp] {
	return &cosmosGlobalLister[fleet.Stamp, GenericDocument[fleet.Stamp]]{
		containerClient: g.container,
		resourceType:    fleet.StampResourceType,
	}
}

func (g *cosmosFleetGlobalListers) ManagementClusters() GlobalLister[fleet.ManagementCluster] {
	return &cosmosGlobalLister[fleet.ManagementCluster, GenericDocument[fleet.ManagementCluster]]{
		containerClient: g.container,
		resourceType:    fleet.ManagementClusterResourceType,
	}
}
