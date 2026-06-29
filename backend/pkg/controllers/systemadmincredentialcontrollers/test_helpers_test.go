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

package systemadmincredentialcontrollers

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
)

const (
	testSubscriptionID    = "00000000-0000-0000-0000-000000000000"
	testResourceGroupName = "test-rg"
	testClusterName       = "test-cluster"
	testOperationName     = "test-operation-id"
	testAzureLocation     = "eastus"
	testCredentialName    = "testcred00000001"
)

// newTestOperation creates a test Operation with CosmosMetadata set.
func newTestOperation(internalIDStr string, request database.OperationRequest) *api.Operation {
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

	if internalIDStr != "" {
		op.InternalID = api.Must(api.NewInternalID(internalIDStr))
	}

	return op
}

// newTestCluster creates a test HCP cluster with CosmosMetadata set.
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

// createTestCredentialRequest creates a SystemAdminCredentialRequest document using the CRUD interface.
// Pass condition types to set them to True before persisting. Pass option functions for additional mutations.
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

	credCRUD := db.SystemAdminCredentialRequests(testSubscriptionID, testResourceGroupName, testClusterName)
	_, err := credCRUD.Create(context.Background(), cred, nil)
	require.NoError(t, err)
	return cred
}

// withCondition returns an option function that sets a condition to True on the credential request.
func withCondition(condType string) func(*api.SystemAdminCredentialRequest) {
	return func(cred *api.SystemAdminCredentialRequest) {
		cred.Status.SetCondition(condType, metav1.ConditionTrue, condType, "test")
	}
}

// withRevokedAt returns an option function that sets the RevokedAt timestamp on a credential request.
func withRevokedAt(ts metav1.Time) func(*api.SystemAdminCredentialRequest) {
	return func(cred *api.SystemAdminCredentialRequest) {
		cred.Status.RevokedAt = &ts
	}
}
