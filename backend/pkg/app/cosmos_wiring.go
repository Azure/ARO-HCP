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

package app

import (
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// NewCosmosDatabaseClient creates the shared Cosmos DatabaseClient that
// is passed into the per-container wiring functions below.
func NewCosmosDatabaseClient(cosmosDBURL string, cosmosDBName string, azCoreClientOptions azcore.ClientOptions) (*azcosmos.DatabaseClient, error) {
	client, err := database.NewCosmosDatabaseClient(cosmosDBURL, cosmosDBName, azCoreClientOptions)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to create Azure Cosmos database client: %w", err))
	}
	return client, nil
}

// NewCosmosDBClients returns data-plane clients for
// ARM resource documents (Resources container) and billing documents (Billing container).
func NewCosmosDBClients(cosmosDatabaseClient *azcosmos.DatabaseClient) (database.ResourcesDBClient, database.BillingDBClient, error) {
	resourcesDBClient, err := database.NewResourcesDBClient(cosmosDatabaseClient)
	if err != nil {
		return nil, nil, utils.TrackError(fmt.Errorf("failed to create resources database client: %w", err))
	}

	billingDBClient, err := database.NewBillingDBClient(cosmosDatabaseClient)
	if err != nil {
		return nil, nil, utils.TrackError(fmt.Errorf("failed to create billing database client: %w", err))
	}

	return resourcesDBClient, billingDBClient, nil
}

func NewFleetDBClient(cosmosDatabaseClient *azcosmos.DatabaseClient) (database.FleetDBClient, error) {
	fleetClient, err := database.NewFleetDBClient(cosmosDatabaseClient)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to create Fleet DBClient: %w", err))
	}

	return fleetClient, nil
}
