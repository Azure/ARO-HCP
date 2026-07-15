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
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"

	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// NewKubeApplierDBClient builds a KubeApplierDBClient bound to a single
// management cluster's Cosmos container. Each kube-applier pod opens its own
// MC's container; the backend opens all of them via KubeApplierDBClients.
// Credentials resolve via the Azure default credential chain (workload identity
// in production).
func NewKubeApplierDBClient(cosmosDBURL, cosmosDBName, cosmosContainerName string, managementClusterPartitionKey *azcorearm.ResourceID) (database.KubeApplierDBClient, error) {
	clientOptions := azcore.ClientOptions{Cloud: cloud.AzurePublic}

	cosmosDatabaseClient, err := database.NewCosmosDatabaseClient(cosmosDBURL, cosmosDBName, clientOptions)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to create Azure Cosmos database client: %w", err))
	}
	client, err := database.NewKubeApplierDBClientFromDatabase(cosmosDatabaseClient, cosmosContainerName, managementClusterPartitionKey)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to create KubeApplierDBClient: %w", err))
	}
	return client, nil
}
