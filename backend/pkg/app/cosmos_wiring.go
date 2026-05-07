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

	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// NewCosmosDBClients opens the shared async Cosmos database and returns data-plane clients for
// ARM resource documents (Resources container) and billing documents (Billing container).
func NewCosmosDBClients(ctx context.Context, cosmosDBURL string, cosmosDBName string, azCoreClientOptions azcore.ClientOptions) (database.ResourcesDBClient, database.BillingDBClient, error) {
	cosmosDatabaseClient, err := database.NewCosmosDatabaseClient(cosmosDBURL, cosmosDBName, azCoreClientOptions)
	if err != nil {
		return nil, nil, utils.TrackError(fmt.Errorf("failed to create Azure Cosmos database client: %w", err))
	}

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
