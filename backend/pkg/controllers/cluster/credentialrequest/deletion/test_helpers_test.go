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

package deletion

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
)

const (
	testSubscriptionID    = "00000000-0000-0000-0000-000000000000"
	testResourceGroupName = "test-rg"
	testClusterName       = "test-cluster"
	testOperationName     = "test-operation-id"
	testCredentialName    = "testcred00000001"
)

func createTestCredentialRequest(t *testing.T, db *corecosmosstoragetesting.MockResourcesDBClient, credName string, opts ...func(*api.SystemAdminCredentialRequest)) *api.SystemAdminCredentialRequest {
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

func withCondition(condType string) func(*api.SystemAdminCredentialRequest) {
	return func(cred *api.SystemAdminCredentialRequest) {
		meta.SetStatusCondition(&cred.Status.Conditions, metav1.Condition{
			Type:    condType,
			Status:  metav1.ConditionTrue,
			Reason:  condType,
			Message: "test",
		})
	}
}

func withCreationTimestamp(ts metav1.Time) func(*api.SystemAdminCredentialRequest) {
	return func(cred *api.SystemAdminCredentialRequest) {
		cred.Spec.CreationTimestamp = ts
	}
}
