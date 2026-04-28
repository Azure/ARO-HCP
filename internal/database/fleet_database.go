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

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	fleetContainer = "Fleet"
)

// FleetDBClient provides database access to the Fleet CosmosDB container.
type FleetDBClient interface {
	ManagementClusterDeployments() ManagementClusterDeploymentCRUD
	FleetGlobalListers() FleetGlobalListers
}

var _ FleetDBClient = &fleetDBClient{}

type fleetDBClient struct {
	fleet *azcosmos.ContainerClient
}

// NewFleetDBClient creates a FleetDBClient that accesses the Fleet container.
func NewFleetDBClient(ctx context.Context, database *azcosmos.DatabaseClient) (FleetDBClient, error) {
	fleet, err := database.NewContainer(fleetContainer)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	return &fleetDBClient{
		fleet: fleet,
	}, nil
}

func (d *fleetDBClient) ManagementClusterDeployments() ManagementClusterDeploymentCRUD {
	return NewManagementClusterDeploymentCRUD(d.fleet)
}

func (d *fleetDBClient) FleetGlobalListers() FleetGlobalListers {
	return NewFleetGlobalListers(d.fleet)
}

// FleetGlobalListers provides cross-partition listers for fleet management resources.
type FleetGlobalListers interface {
	ManagementClusterDeployments() GlobalLister[api.ManagementClusterDeployment]
}

var _ FleetGlobalListers = &fleetGlobalListers{}

type fleetGlobalListers struct {
	fleet *azcosmos.ContainerClient
}

func NewFleetGlobalListers(fleet *azcosmos.ContainerClient) FleetGlobalListers {
	return &fleetGlobalListers{
		fleet: fleet,
	}
}

func (g *fleetGlobalListers) ManagementClusterDeployments() GlobalLister[api.ManagementClusterDeployment] {
	return &cosmosGlobalLister[api.ManagementClusterDeployment, GenericDocument[api.ManagementClusterDeployment]]{
		containerClient: g.fleet,
		resourceType:    api.ManagementClusterDeploymentResourceType,
	}
}
