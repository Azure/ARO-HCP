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
