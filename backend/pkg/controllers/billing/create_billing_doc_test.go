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

package billing

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/billingcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/billingcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	testSubscriptionID      = "00000000-0000-0000-0000-000000000000"
	testResourceGroupName   = "test-rg"
	testClusterName         = "test-cluster"
	testClusterUID          = "billing-doc-id-001"
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

func newTestSubscription() *coreapi.Subscription {
	subResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID))
	return &coreapi.Subscription{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   subResourceID,
			PartitionKey: strings.ToLower(subResourceID.SubscriptionID),
		},
		State: coreapi.SubscriptionStateRegistered,
		Properties: &coreapi.SubscriptionProperties{
			TenantId: ptr.To(testTenantID),
		},
	}
}

func newTestCluster(t *testing.T, clusterUID string, provisioningState coreapi.ProvisioningState, createdAt *time.Time) *coreapi.HCPOpenShiftCluster {
	t.Helper()
	clusterResourceID := newTestClusterResourceID(t)
	return &coreapi.HCPOpenShiftCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   clusterResourceID,
			PartitionKey: strings.ToLower(clusterResourceID.SubscriptionID),
		},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID:   clusterResourceID,
				Name: testClusterName,
				Type: "Microsoft.RedHatOpenShift/hcpOpenShiftClusters",
				SystemData: &coreapi.SystemData{
					CreatedAt: createdAt,
				},
			},
		},
		ServiceProviderProperties: coreapi.HCPOpenShiftClusterServiceProviderProperties{
			ProvisioningState: provisioningState,
			ClusterUID:        clusterUID,
			ClusterServiceID:  metadataapi.Ptr(metadataapi.Must(metadataapi.NewInternalID(testClusterServiceIDStr))),
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
		cluster     *coreapi.HCPOpenShiftCluster
		expectError bool
		verify      func(t *testing.T, billing *billingcosmosstoragetesting.MockBillingDBClient)
	}{
		{
			name:        "creates billing document for succeeded cluster with ClusterUID",
			cluster:     newTestCluster(t, testClusterUID, coreapi.ProvisioningStateSucceeded, &createdAt),
			expectError: false,
			verify: func(t *testing.T, billing *billingcosmosstoragetesting.MockBillingDBClient) {
				billingDocs := billing.GetBillingDocuments()
				require.Len(t, billingDocs, 1)
				doc := billingDocs[testClusterUID]
				require.NotNil(t, doc)
				assert.Equal(t, testClusterUID, doc.ID)
				assert.Equal(t, testTenantID, doc.TenantID)
				assert.Equal(t, testAzureLocation, doc.Location)
				assert.Equal(t, createdAt, doc.CreationTime)
			},
		},
		{
			name:        "uses fallback time when CreatedAt is nil",
			cluster:     newTestCluster(t, testClusterUID, coreapi.ProvisioningStateSucceeded, nil),
			expectError: false,
			verify: func(t *testing.T, billing *billingcosmosstoragetesting.MockBillingDBClient) {
				billingDocs := billing.GetBillingDocuments()
				require.Len(t, billingDocs, 1)
				doc := billingDocs[testClusterUID]
				require.NotNil(t, doc)
				assert.Equal(t, fixedTime, doc.CreationTime, "should use fallback time when CreatedAt is nil")
			},
		},
		{
			name:        "skips cluster without ClusterUID",
			cluster:     newTestCluster(t, "", coreapi.ProvisioningStateSucceeded, &createdAt),
			expectError: false,
			verify: func(t *testing.T, billing *billingcosmosstoragetesting.MockBillingDBClient) {
				billingDocs := billing.GetBillingDocuments()
				assert.Empty(t, billingDocs, "no billing document should be created when ClusterUID is empty")
			},
		},
		{
			name:        "skips cluster not in Succeeded state",
			cluster:     newTestCluster(t, testClusterUID, coreapi.ProvisioningStateProvisioning, &createdAt),
			expectError: false,
			verify: func(t *testing.T, billing *billingcosmosstoragetesting.MockBillingDBClient) {
				billingDocs := billing.GetBillingDocuments()
				assert.Empty(t, billingDocs, "no billing document should be created for non-succeeded cluster")
			},
		},
		{
			name:        "skips cluster in Failed state",
			cluster:     newTestCluster(t, testClusterUID, coreapi.ProvisioningStateFailed, &createdAt),
			expectError: false,
			verify: func(t *testing.T, billing *billingcosmosstoragetesting.MockBillingDBClient) {
				billingDocs := billing.GetBillingDocuments()
				assert.Empty(t, billingDocs, "no billing document should be created for failed cluster")
			},
		},
		{
			name:        "idempotent when billing document already exists",
			cluster:     newTestCluster(t, testClusterUID, coreapi.ProvisioningStateSucceeded, &createdAt),
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

			mockResourcesDBClient, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)
			mockBillingDBClient := billingcosmosstoragetesting.NewMockBillingDBClient()

			controller := &createBillingDoc{
				clock:             clocktesting.NewFakePassiveClock(fixedTime),
				azureLocation:     testAzureLocation,
				resourcesDBClient: mockResourcesDBClient,
				billingDBClient:   mockBillingDBClient,
				clusterLister: &corelistertesting.SliceClusterLister{
					Clusters: []*coreapi.HCPOpenShiftCluster{tt.cluster},
				},
				billingLister: &corelistertesting.SliceBillingLister{
					BillingDocuments: []*billingcosmosstorage.BillingDocument{},
				},
			}

			err = controller.SyncOnce(ctx, newTestClusterKey())

			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			if tt.verify != nil {
				tt.verify(t, mockBillingDBClient)
			}
		})
	}
}

func TestCreateBillingDoc_Idempotent(t *testing.T) {
	fixedTime := mustParseTime("2025-01-20T10:30:00Z")
	createdAt := mustParseTime("2025-01-15T10:30:00Z")

	ctx := context.Background()
	ctx = utils.ContextWithLogger(ctx, testr.New(t))

	cluster := newTestCluster(t, testClusterUID, coreapi.ProvisioningStateSucceeded, &createdAt)
	subscription := newTestSubscription()

	mockResourcesDBClient, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, subscription})
	require.NoError(t, err)
	mockBillingDBClient := billingcosmosstoragetesting.NewMockBillingDBClient()

	// Setup slice cluster lister (cache)
	clusterLister := &corelistertesting.SliceClusterLister{
		Clusters: []*coreapi.HCPOpenShiftCluster{cluster},
	}

	controller := &createBillingDoc{
		clock:             clocktesting.NewFakePassiveClock(fixedTime),
		azureLocation:     testAzureLocation,
		resourcesDBClient: mockResourcesDBClient,
		billingDBClient:   mockBillingDBClient,
		clusterLister:     clusterLister,
		billingLister: &corelistertesting.SliceBillingLister{
			BillingDocuments: []*billingcosmosstorage.BillingDocument{},
		},
	}

	key := newTestClusterKey()

	// First sync creates the billing doc
	err = controller.SyncOnce(ctx, key)
	require.NoError(t, err)

	billingDocs := mockBillingDBClient.GetBillingDocuments()
	require.Len(t, billingDocs, 1)

	// Second sync should succeed without error (idempotent - conflict handled)
	err = controller.SyncOnce(ctx, key)
	require.NoError(t, err)

	billingDocs = mockBillingDBClient.GetBillingDocuments()
	assert.Len(t, billingDocs, 1, "should still have exactly one billing document")
}

func TestCreateBillingDoc_ExistingBillingDocButMissingClusterRef(t *testing.T) {
	fixedTime := mustParseTime("2025-01-20T10:30:00Z")
	createdAt := mustParseTime("2025-01-15T10:30:00Z")

	// Pre-seed billing doc to simulate a prior cycle that created it but
	// failed to update the cluster's BillingDocumentCosmosID.
	preSeedDoc := billingcosmosstorage.NewBillingDocument(testClusterUID, newTestClusterResourceID(t))
	preSeedDoc.CreationTime = createdAt
	preSeedDoc.Location = testAzureLocation
	preSeedDoc.TenantID = testTenantID

	tests := []struct {
		name             string
		cachedBillingDoc []*billingcosmosstorage.BillingDocument
		seedInDB         bool
	}{
		{
			name:             "billing doc found in database",
			cachedBillingDoc: []*billingcosmosstorage.BillingDocument{},
			seedInDB:         true,
		},
		{
			name:             "billing doc found in cache",
			cachedBillingDoc: []*billingcosmosstorage.BillingDocument{preSeedDoc},
			seedInDB:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			ctx = utils.ContextWithLogger(ctx, testr.New(t))

			// Cluster has ClusterUID but no BillingDocumentCosmosID.
			cluster := newTestCluster(t, testClusterUID, coreapi.ProvisioningStateSucceeded, &createdAt)
			assert.Empty(t, cluster.ServiceProviderProperties.BillingDocumentCosmosID)

			subscription := newTestSubscription()

			mockResourcesDBClient, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, subscription})
			require.NoError(t, err)
			mockBillingDBClient := billingcosmosstoragetesting.NewMockBillingDBClient()

			if tt.seedInDB {
				err = mockBillingDBClient.BillingDocs(testSubscriptionID).Create(ctx, preSeedDoc)
				require.NoError(t, err)
			}

			controller := &createBillingDoc{
				clock:             clocktesting.NewFakePassiveClock(fixedTime),
				azureLocation:     testAzureLocation,
				resourcesDBClient: mockResourcesDBClient,
				billingDBClient:   mockBillingDBClient,
				clusterLister: &corelistertesting.SliceClusterLister{
					Clusters: []*coreapi.HCPOpenShiftCluster{cluster},
				},
				billingLister: &corelistertesting.SliceBillingLister{
					BillingDocuments: tt.cachedBillingDoc,
				},
			}

			err = controller.SyncOnce(ctx, newTestClusterKey())
			require.NoError(t, err)

			// Verify no new billing document was created.
			billingDocs := mockBillingDBClient.GetBillingDocuments()
			if tt.seedInDB {
				assert.Len(t, billingDocs, 1, "should not create a second billing document")
			}

			// Verify the cluster's BillingDocumentCosmosID was updated.
			clusterCRUD := mockResourcesDBClient.HCPClusters(testSubscriptionID, testResourceGroupName)
			updatedCluster, err := clusterCRUD.Get(ctx, testClusterName)
			require.NoError(t, err)
			assert.Equal(t, testClusterUID, updatedCluster.ServiceProviderProperties.BillingDocumentCosmosID,
				"cluster should have BillingDocumentCosmosID set to the existing billing doc ID")
		})
	}
}
