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

	"github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func newTestBillingDocument(billingDocID, subscriptionID, resourceGroupName, clusterName string, deletedAt *time.Time) *database.BillingDocument {
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/resourceGroups/" + resourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName))

	return &database.BillingDocument{
		BaseDocument: database.BaseDocument{
			ID: billingDocID,
		},
		SubscriptionID: subscriptionID,
		TenantID:       testTenantID,
		Location:       testAzureLocation,
		ResourceID:     resourceID,
		CreationTime:   mustParseTime("2025-01-15T10:30:00Z"),
		DeletionTime:   deletedAt,
	}
}

func TestOrphanedBillingCleanup_SyncOnce(t *testing.T) {
	fixedTime := mustParseTime("2025-01-20T10:30:00Z")
	createdAt := mustParseTime("2025-01-15T10:30:00Z")

	tests := []struct {
		name             string
		billingDocuments []*database.BillingDocument
		clusters         []*api.HCPOpenShiftCluster
		expectError      bool
		verify           func(t *testing.T, billingDBClient *databasetesting.MockBillingDBClient)
	}{
		{
			name: "marks orphaned billing document as deleted",
			billingDocuments: []*database.BillingDocument{
				newTestBillingDocument("billing-doc-1", testSubscriptionID, testResourceGroupName, testClusterName, nil),
			},
			clusters:    []*api.HCPOpenShiftCluster{}, // No clusters
			expectError: false,
			verify: func(t *testing.T, billingDBClient *databasetesting.MockBillingDBClient) {
				billingDocs := billingDBClient.GetBillingDocuments()
				require.Len(t, billingDocs, 1)
				doc := billingDocs["billing-doc-1"]
				require.NotNil(t, doc)
				assert.NotNil(t, doc.DeletionTime, "orphaned billing document should be marked as deleted")
			},
		},
		{
			name: "skips billing document when cluster still exists",
			billingDocuments: []*database.BillingDocument{
				newTestBillingDocument("billing-doc-1", testSubscriptionID, testResourceGroupName, testClusterName, nil),
			},
			clusters: []*api.HCPOpenShiftCluster{
				newTestCluster(t, "billing-doc-1", arm.ProvisioningStateSucceeded, &createdAt),
			},
			expectError: false,
			verify: func(t *testing.T, billingDBClient *databasetesting.MockBillingDBClient) {
				billingDocs := billingDBClient.GetBillingDocuments()
				require.Len(t, billingDocs, 1)
				doc := billingDocs["billing-doc-1"]
				require.NotNil(t, doc)
				assert.Nil(t, doc.DeletionTime, "billing document should not be marked as deleted when cluster exists")
			},
		},
		{
			name: "skips billing document already marked as deleted",
			billingDocuments: []*database.BillingDocument{
				newTestBillingDocument("billing-doc-1", testSubscriptionID, testResourceGroupName, "cluster-1", ptr.To(mustParseTime("2025-01-19T10:30:00Z"))),
			},
			clusters:    []*api.HCPOpenShiftCluster{}, // No clusters
			expectError: false,
			verify: func(t *testing.T, billingDBClient *databasetesting.MockBillingDBClient) {
				billingDocs := billingDBClient.GetBillingDocuments()
				require.Len(t, billingDocs, 1)
				doc := billingDocs["billing-doc-1"]
				require.NotNil(t, doc)
				assert.NotNil(t, doc.DeletionTime)
				// Verify the deletion time hasn't changed (should be the original time, not fixedTime)
				assert.Equal(t, mustParseTime("2025-01-19T10:30:00Z"), *doc.DeletionTime)
			},
		},

		{
			name: "handles multiple billing documents",
			billingDocuments: []*database.BillingDocument{
				newTestBillingDocument("billing-doc-1", testSubscriptionID, testResourceGroupName, "cluster-1", nil),
				newTestBillingDocument("billing-doc-2", testSubscriptionID, testResourceGroupName, "cluster-2", nil),
				newTestBillingDocument("billing-doc-3", testSubscriptionID, testResourceGroupName, "cluster-3", nil),
			},
			clusters: []*api.HCPOpenShiftCluster{
				// Only cluster-2 exists
				{
					TrackedResource: arm.TrackedResource{
						Resource: arm.Resource{
							ID:   api.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-2")),
							Name: "cluster-2",
							Type: "Microsoft.RedHatOpenShift/hcpOpenShiftClusters",
							SystemData: &arm.SystemData{
								CreatedAt: &createdAt,
							},
						},
					},
					ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
						ProvisioningState: arm.ProvisioningStateSucceeded,
						ClusterUID:        "billing-doc-2",
						ClusterServiceID:  api.Ptr(api.Must(api.NewInternalID(testClusterServiceIDStr))),
					},
				},
			},
			expectError: false,
			verify: func(t *testing.T, billingDBClient *databasetesting.MockBillingDBClient) {
				billingDocs := billingDBClient.GetBillingDocuments()
				require.Len(t, billingDocs, 3)

				// billing-doc-1 should be deleted (cluster-1 doesn't exist)
				doc1 := billingDocs["billing-doc-1"]
				require.NotNil(t, doc1)
				assert.NotNil(t, doc1.DeletionTime, "billing-doc-1 should be marked as deleted")

				// billing-doc-2 should not be deleted (cluster-2 exists)
				doc2 := billingDocs["billing-doc-2"]
				require.NotNil(t, doc2)
				assert.Nil(t, doc2.DeletionTime, "billing-doc-2 should not be marked as deleted")

				// billing-doc-3 should be deleted (cluster-3 doesn't exist)
				doc3 := billingDocs["billing-doc-3"]
				require.NotNil(t, doc3)
				assert.NotNil(t, doc3.DeletionTime, "billing-doc-3 should be marked as deleted")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			ctx = utils.ContextWithLogger(ctx, testr.New(t))

			// Create mock DB and add billing documents
			mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()

			// Add billing documents directly
			mockBillingDBClient := databasetesting.NewMockBillingDBClient()
			for _, doc := range tt.billingDocuments {
				billingCRUD := mockBillingDBClient.BillingDocs(doc.SubscriptionID)
				err := billingCRUD.Create(ctx, doc)
				require.NoError(t, err)
			}

			// Add clusters
			for _, cluster := range tt.clusters {
				if cluster.ID != nil {
					clusterCRUD := mockResourcesDBClient.HCPClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName)
					_, err := clusterCRUD.Create(ctx, cluster, nil)
					require.NoError(t, err)
				}
			}

			controller := &orphanedBillingCleanup{
				name:  "OrphanedBillingCleanup",
				clock: clocktesting.NewFakePassiveClock(fixedTime),
				clusterLister: &listertesting.SliceClusterLister{
					Clusters: tt.clusters,
				},
				billingLister: &listertesting.SliceBillingLister{
					BillingDocuments: tt.billingDocuments,
				},
				billingDBClient: mockBillingDBClient,
			}

			err := controller.SyncOnce(ctx, "default")

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
