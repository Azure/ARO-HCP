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

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
	testCredentialName    = "testcred00000001"
)

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

func withCondition(condType string) func(*coreapi.SystemAdminCredentialRequest) {
	return func(cred *coreapi.SystemAdminCredentialRequest) {
		meta.SetStatusCondition(&cred.Status.Conditions, metav1.Condition{
			Type:    condType,
			Status:  metav1.ConditionTrue,
			Reason:  condType,
			Message: "test",
		})
	}
}
