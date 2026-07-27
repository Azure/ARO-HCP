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

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
)

const (
	testSubscriptionID    = "00000000-0000-0000-0000-000000000000"
	testResourceGroupName = "test-rg"
	testClusterName       = "test-cluster"
	testOperationName     = "test-operation-id"
	testAzureLocation     = "eastus"
	testRevocationName    = "testrevocation01"
)

func createTestRevocation(t *testing.T, db *corecosmosstoragetesting.MockResourcesDBClient, revocationName string, opts ...func(*coreapi.SystemAdminCredentialRevocation)) *coreapi.SystemAdminCredentialRevocation {
	t.Helper()

	revocationResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		coreapi.ToSystemAdminCredentialRevocationResourceIDString(testSubscriptionID, testResourceGroupName, testClusterName, revocationName),
	))

	revocation := &coreapi.SystemAdminCredentialRevocation{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   revocationResourceID,
			PartitionKey: strings.ToLower(testSubscriptionID),
		},
		Spec: coreapi.SystemAdminCredentialRevocationSpec{
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

func createTestCluster(t *testing.T, db *corecosmosstoragetesting.MockResourcesDBClient, opts ...func(*coreapi.HCPOpenShiftCluster)) *coreapi.HCPOpenShiftCluster {
	t.Helper()

	clusterResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName,
	))

	cluster := &coreapi.HCPOpenShiftCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   clusterResourceID,
			PartitionKey: strings.ToLower(clusterResourceID.SubscriptionID),
		},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID:   clusterResourceID,
				Name: testClusterName,
			},
			Location: testAzureLocation,
		},
		ServiceProviderProperties: coreapi.HCPOpenShiftClusterServiceProviderProperties{
			ProvisioningState: coreapi.ProvisioningStateSucceeded,
		},
	}

	for _, opt := range opts {
		opt(cluster)
	}

	_, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Create(context.Background(), cluster, nil)
	require.NoError(t, err)
	return cluster
}

func createTestCredentialRequest(t *testing.T, db *corecosmosstoragetesting.MockResourcesDBClient, credName string, opts ...func(*coreapi.SystemAdminCredentialRequest)) *coreapi.SystemAdminCredentialRequest {
	t.Helper()

	credResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		coreapi.ToSystemAdminCredentialRequestResourceIDString(testSubscriptionID, testResourceGroupName, testClusterName, credName),
	))

	cred := &coreapi.SystemAdminCredentialRequest{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   credResourceID,
			PartitionKey: strings.ToLower(testSubscriptionID),
		},
		Spec: coreapi.SystemAdminCredentialRequestSpec{
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
