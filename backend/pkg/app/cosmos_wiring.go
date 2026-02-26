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

func NewCosmosDBClient(ctx context.Context, cosmosDBURL string, cosmosDBName string, azCoreClientOptions azcore.ClientOptions) (database.DBClient, error) {
	cosmosDatabaseClient, err := database.NewCosmosDatabaseClient(
		cosmosDBURL,
		cosmosDBName,
		azCoreClientOptions,
	)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to create Azure Cosmos database client: %w", err))
	}

	dbClient, err := database.NewDBClient(ctx, cosmosDatabaseClient)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to create Cosmos DBClient: %w", err))
	}

	return dbClient, nil
}
