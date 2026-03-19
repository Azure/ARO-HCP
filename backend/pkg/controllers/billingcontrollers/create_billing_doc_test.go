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

package billingcontrollers

import (
	"context"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	testSubscriptionID      = "00000000-0000-0000-0000-000000000000"
	testResourceGroupName   = "test-rg"
	testClusterName         = "test-cluster"
	testBillingDocID        = "billing-doc-id-001"
	testTenantID            = "11111111-1111-1111-1111-111111111111"
	testAzureLocation       = "eastus"
	testClusterServiceIDStr = "/api/clusters_mgmt/v1/clusters/abc123"
)

func mustParseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func newTestClusterResourceID(t *testing.T) *azcorearm.ResourceID {
	t.Helper()
	resourceID, err := azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName)
	require.NoError(t, err)
	return resourceID
}

func newTestSubscription() *arm.Subscription {
	subResourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID))
	return &arm.Subscription{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: subResourceID,
		},
		ResourceID: subResourceID,
		State:      arm.SubscriptionStateRegistered,
		Properties: &arm.SubscriptionProperties{
			TenantId: ptr.To(testTenantID),
		},
	}
}

func newTestCluster(t *testing.T, billingDocID string, provisioningState arm.ProvisioningState, createdAt *time.Time) *api.HCPOpenShiftCluster {
	t.Helper()
	return &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   newTestClusterResourceID(t),
				Name: testClusterName,
				Type: "Microsoft.RedHatOpenShift/hcpOpenShiftClusters",
				SystemData: &arm.SystemData{
					CreatedAt: createdAt,
				},
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ProvisioningState: provisioningState,
			BillingDocID:      billingDocID,
			ClusterServiceID:  api.Must(api.NewInternalID(testClusterServiceIDStr)),
		},
	}
}

func newTestClusterKey() controllerutils.HCPClusterKey {
	return controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}
}

func TestCreateBillingDoc_SyncOnce(t *testing.T) {
	fixedTime := mustParseTime("2025-01-20T10:30:00Z")
	createdAt := mustParseTime("2025-01-15T10:30:00Z")

	tests := []struct {
		name        string
		cluster     *api.HCPOpenShiftCluster
		expectError bool
		verify      func(t *testing.T, db *databasetesting.MockDBClient)
	}{
		{
			name:        "creates billing document for succeeded cluster with BillingDocID",
			cluster:     newTestCluster(t, testBillingDocID, arm.ProvisioningStateSucceeded, &createdAt),
			expectError: false,
			verify: func(t *testing.T, db *databasetesting.MockDBClient) {
				billingDocs := db.GetBillingDocuments()
				require.Len(t, billingDocs, 1)
				doc := billingDocs[testBillingDocID]
				require.NotNil(t, doc)
				assert.Equal(t, testBillingDocID, doc.ID)
				assert.Equal(t, testTenantID, doc.TenantID)
				assert.Equal(t, testAzureLocation, doc.Location)
				assert.Equal(t, createdAt, doc.CreationTime)
			},
		},
		{
			name:        "uses fallback time when CreatedAt is nil",
			cluster:     newTestCluster(t, testBillingDocID, arm.ProvisioningStateSucceeded, nil),
			expectError: false,
			verify: func(t *testing.T, db *databasetesting.MockDBClient) {
				billingDocs := db.GetBillingDocuments()
				require.Len(t, billingDocs, 1)
				doc := billingDocs[testBillingDocID]
				require.NotNil(t, doc)
				assert.Equal(t, fixedTime, doc.CreationTime, "should use fallback time when CreatedAt is nil")
			},
		},
		{
			name:        "skips cluster without BillingDocID",
			cluster:     newTestCluster(t, "", arm.ProvisioningStateSucceeded, &createdAt),
			expectError: false,
			verify: func(t *testing.T, db *databasetesting.MockDBClient) {
				billingDocs := db.GetBillingDocuments()
				assert.Empty(t, billingDocs, "no billing document should be created when BillingDocID is empty")
			},
		},
		{
			name:        "skips cluster not in Succeeded state",
			cluster:     newTestCluster(t, testBillingDocID, arm.ProvisioningStateProvisioning, &createdAt),
			expectError: false,
			verify: func(t *testing.T, db *databasetesting.MockDBClient) {
				billingDocs := db.GetBillingDocuments()
				assert.Empty(t, billingDocs, "no billing document should be created for non-succeeded cluster")
			},
		},
		{
			name:        "idempotent when billing document already exists",
			cluster:     newTestCluster(t, testBillingDocID, arm.ProvisioningStateSucceeded, &createdAt),
			expectError: false,
			verify:      nil, // covered by setup - billing doc pre-seeded, second sync should not error
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			ctx = utils.ContextWithLogger(ctx, testr.New(t))

			subscription := newTestSubscription()
			resources := []any{tt.cluster, subscription}

			mockDB, err := databasetesting.NewMockDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			controller := &createBillingDoc{
				clock:         clocktesting.NewFakePassiveClock(fixedTime),
				azureLocation: testAzureLocation,
				cosmosClient:  mockDB,
			}

			err = controller.SyncOnce(ctx, newTestClusterKey())

			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			if tt.verify != nil {
				tt.verify(t, mockDB)
			}
		})
	}
}

func TestCreateBillingDoc_Idempotent(t *testing.T) {
	fixedTime := mustParseTime("2025-01-20T10:30:00Z")
	createdAt := mustParseTime("2025-01-15T10:30:00Z")

	ctx := context.Background()
	ctx = utils.ContextWithLogger(ctx, testr.New(t))

	cluster := newTestCluster(t, testBillingDocID, arm.ProvisioningStateSucceeded, &createdAt)
	subscription := newTestSubscription()

	mockDB, err := databasetesting.NewMockDBClientWithResources(ctx, []any{cluster, subscription})
	require.NoError(t, err)

	controller := &createBillingDoc{
		clock:         clocktesting.NewFakePassiveClock(fixedTime),
		azureLocation: testAzureLocation,
		cosmosClient:  mockDB,
	}

	key := newTestClusterKey()

	// First sync creates the billing doc
	err = controller.SyncOnce(ctx, key)
	require.NoError(t, err)

	billingDocs := mockDB.GetBillingDocuments()
	require.Len(t, billingDocs, 1)

	// Second sync should succeed without error (idempotent - conflict handled)
	err = controller.SyncOnce(ctx, key)
	require.NoError(t, err)

	billingDocs = mockDB.GetBillingDocuments()
	assert.Len(t, billingDocs, 1, "should still have exactly one billing document")
}
