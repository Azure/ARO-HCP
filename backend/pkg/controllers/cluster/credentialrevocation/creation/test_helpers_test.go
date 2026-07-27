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

package creation

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
)

const (
	testSubscriptionID    = "00000000-0000-0000-0000-000000000000"
	testResourceGroupName = "test-rg"
	testClusterName       = "test-cluster"
	testOperationName     = "test-operation-id"
	testAzureLocation     = "eastus"
	testRevocationName    = "testrevocation01"
)

func createTestRevocation(t *testing.T, db *databasetesting.MockResourcesDBClient, revocationName string, opts ...func(*api.SystemAdminCredentialRevocation)) *api.SystemAdminCredentialRevocation {
	t.Helper()

	revocationResourceID := api.Must(azcorearm.ParseResourceID(
		api.ToSystemAdminCredentialRevocationResourceIDString(testSubscriptionID, testResourceGroupName, testClusterName, revocationName),
	))

	revocation := &api.SystemAdminCredentialRevocation{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID:   revocationResourceID,
			PartitionKey: strings.ToLower(testSubscriptionID),
		},
		Spec: api.SystemAdminCredentialRevocationSpec{
			OperationID:    testOperationName,
			RevokeOpSuffix: revocationName,
		},
	}

	for _, opt := range opts {
		opt(revocation)
	}

	revocationCRUD := db.HCPClusters(testSubscriptionID, testResourceGroupName).SystemAdminCredentialRevocations(testClusterName)
	_, err := revocationCRUD.Create(context.Background(), revocation, nil)
	require.NoError(t, err)
	return revocation
}

func createTestCluster(t *testing.T, db *databasetesting.MockResourcesDBClient, opts ...func(*api.HCPOpenShiftCluster)) *api.HCPOpenShiftCluster {
	t.Helper()

	clusterResourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName,
	))

	cluster := &api.HCPOpenShiftCluster{
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
			ProvisioningState: arm.ProvisioningStateSucceeded,
		},
	}

	for _, opt := range opts {
		opt(cluster)
	}

	_, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Create(context.Background(), cluster, nil)
	require.NoError(t, err)
	return cluster
}

func createTestCredentialRequest(t *testing.T, db *databasetesting.MockResourcesDBClient, credName string, opts ...func(*api.SystemAdminCredentialRequest)) *api.SystemAdminCredentialRequest {
	t.Helper()

	credResourceID := api.Must(azcorearm.ParseResourceID(
		api.ToSystemAdminCredentialRequestResourceIDString(testSubscriptionID, testResourceGroupName, testClusterName, credName),
	))

	cred := &api.SystemAdminCredentialRequest{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID:   credResourceID,
			PartitionKey: strings.ToLower(testSubscriptionID),
		},
		Spec: api.SystemAdminCredentialRequestSpec{
			Username:    "test-user",
			OperationID: testOperationName,
		},
	}

	for _, opt := range opts {
		opt(cred)
	}

	credCRUD := db.HCPClusters(testSubscriptionID, testResourceGroupName).SystemAdminCredentialRequests(testClusterName)
	_, err := credCRUD.Create(context.Background(), cred, nil)
	require.NoError(t, err)
	return cred
}
