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
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"

	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// NewKubeApplierDBClient builds a KubeApplierDBClient bound to the named Cosmos
// account/database. It uses the Azure default credential chain, which in
// production resolves to the pod's workload-identity federated token.
func NewKubeApplierDBClient(
	ctx context.Context, cosmosDBURL, cosmosDBName string,
) (database.KubeApplierDBClient, error) {
	clientOptions := azcore.ClientOptions{Cloud: cloud.AzurePublic}

	cosmosDatabaseClient, err := database.NewCosmosDatabaseClient(cosmosDBURL, cosmosDBName, clientOptions)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to create Azure Cosmos database client: %w", err))
	}
	client, err := database.NewKubeApplierDBClient(cosmosDatabaseClient)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to create KubeApplierDBClient: %w", err))
	}
	_ = ctx
	return client, nil
}
