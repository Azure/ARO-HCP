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

package operations

import (
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

const (
	testSubscriptionID    = "00000000-0000-0000-0000-000000000000"
	testResourceGroupName = "test-rg"
	testClusterName       = "test-cluster"
	testOperationName     = "test-operation-id"
	testAzureLocation     = "eastus"
)

func newTestOperation(request api.OperationRequest) *api.Operation {
	clusterResourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName,
	))

	operationID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/providers/Microsoft.RedHatOpenShift/locations/" + testAzureLocation +
			"/operationstatuses/" + testOperationName,
	))

	cosmosOperationResourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/providers/Microsoft.RedHatOpenShift/hcpOperationStatuses/" + testOperationName,
	))

	op := &api.Operation{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID:   cosmosOperationResourceID,
			PartitionKey: strings.ToLower(cosmosOperationResourceID.SubscriptionID),
		},
		OperationID: operationID,
		ExternalID:  clusterResourceID,
		Request:     request,
		Status:      arm.ProvisioningStateAccepted,
	}

	return op
}

func newTestCluster(revokeOpID string) *api.HCPOpenShiftCluster {
	clusterResourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName,
	))

	return &api.HCPOpenShiftCluster{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID:   clusterResourceID,
			PartitionKey: strings.ToLower(clusterResourceID.SubscriptionID),
		},
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   clusterResourceID,
				Name: testClusterName,
			},
			Location: testAzureLocation,
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ProvisioningState:            arm.ProvisioningStateSucceeded,
			RevokeCredentialsOperationID: revokeOpID,
		},
	}
}
