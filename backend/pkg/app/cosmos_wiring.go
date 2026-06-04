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
	"context"
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
func NewCosmosDBClients(ctx context.Context, cosmosDatabaseClient *azcosmos.DatabaseClient) (database.ResourcesDBClient, database.BillingDBClient, error) {
	resourcesDBClient, err := database.NewResourcesDBClient(ctx, cosmosDatabaseClient)
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

// NewKubeApplierDBClients returns a thread-safe registry of per-management-cluster
// KubeApplierDBClients. The backend holds one of these so it can talk to every
// management cluster's kube-applier container; the kube-applier sidecar binary
// opens its own single container directly.
//
// The registry resolves container names by walking the provided
// ManagementClusterLister: each fleet.ManagementCluster carries its container
// name in Status.KubeApplierCosmosContainerName, and its partition key in
// Status.MaestroConsumerName. Adding or removing an MC from the lister (via
// fleet sync) is picked up by For() / ManagementClusterResourceIDs() on the
// next call without restarting the backend.
func NewKubeApplierDBClients(
	cosmosDatabaseClient *azcosmos.DatabaseClient,
	mcLister database.ManagementClusterLister,
) database.KubeApplierDBClients {
	return database.NewKubeApplierDBClients(cosmosDatabaseClient, mcLister)
}
