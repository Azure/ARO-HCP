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
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const fleetContainer = "Fleet"

// FleetDBClient is the database surface for the fleet Cosmos container.
// It is intentionally separate from DBClient because the fleet container
// holds management cluster inventory data with its own access patterns
// and credential scoping.
type FleetDBClient interface {
	ManagementClusters(subscriptionID, resourceGroupName string) ManagementClusterCRUD
	ManagementClusterDeployments() ManagementClusterDeploymentCRUD
	GlobalListers() FleetGlobalListers
}

// FleetGlobalListers provides cross-partition listers for fleet resource types.
type FleetGlobalListers interface {
	ManagementClusters() GlobalLister[fleet.ManagementCluster]
	ManagementClusterDeployments() GlobalLister[fleet.ManagementClusterDeployment]
}

type cosmosFleetDBClient struct {
	container *azcosmos.ContainerClient
}

var _ FleetDBClient = &cosmosFleetDBClient{}

// NewFleetDBClient instantiates a FleetDBClient from a Cosmos DatabaseClient.
// It opens only the fleet container.
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

func (c *cosmosFleetDBClient) ManagementClusters(subscriptionID, resourceGroupName string) ManagementClusterCRUD {
	return NewManagementClusterCRUD(c.container, subscriptionID, resourceGroupName)
}

func (c *cosmosFleetDBClient) ManagementClusterDeployments() ManagementClusterDeploymentCRUD {
	return NewManagementClusterDeploymentCRUD(c.container)
}

func (c *cosmosFleetDBClient) GlobalListers() FleetGlobalListers {
	return &cosmosFleetGlobalListers{container: c.container}
}

type cosmosFleetGlobalListers struct {
	container *azcosmos.ContainerClient
}

var _ FleetGlobalListers = &cosmosFleetGlobalListers{}

func (g *cosmosFleetGlobalListers) ManagementClusters() GlobalLister[fleet.ManagementCluster] {
	return &cosmosGlobalLister[fleet.ManagementCluster, GenericDocument[fleet.ManagementCluster]]{
		containerClient: g.container,
		resourceType:    fleet.ManagementClusterResourceType,
	}
}

func (g *cosmosFleetGlobalListers) ManagementClusterDeployments() GlobalLister[fleet.ManagementClusterDeployment] {
	return &cosmosGlobalLister[fleet.ManagementClusterDeployment, GenericDocument[fleet.ManagementClusterDeployment]]{
		containerClient: g.container,
		resourceType:    fleet.ManagementClusterDeploymentResourceType,
	}
}
