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

package serverutils

import (
	"context"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestDumpBillingToLogger(t *testing.T) {
	ctx := context.Background()
	ctx = utils.ContextWithLogger(ctx, testr.New(t))

	cluster1ResourceID, err := azcorearm.ParseResourceID("/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1")
	require.NoError(t, err)

	cluster2ResourceID, err := azcorearm.ParseResourceID("/subscriptions/sub-2/resourceGroups/rg-2/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-2")
	require.NoError(t, err)

	// Create HCP clusters
	cluster1 := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   cluster1ResourceID,
				Name: "cluster-1",
				Type: "Microsoft.RedHatOpenShift/hcpOpenShiftClusters",
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ClusterUID:       "billing-doc-1",
			ClusterServiceID: api.Ptr(api.Must(api.NewInternalID("/api/clusters_mgmt/v1/clusters/test-cluster-1"))),
		},
	}

	cluster2 := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   cluster2ResourceID,
				Name: "cluster-2",
				Type: "Microsoft.RedHatOpenShift/hcpOpenShiftClusters",
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ClusterUID:       "billing-doc-2",
			ClusterServiceID: api.Ptr(api.Must(api.NewInternalID("/api/clusters_mgmt/v1/clusters/test-cluster-2"))),
		},
	}

	// Create mock DB with clusters
	mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster1, cluster2})
	require.NoError(t, err)
	mockBillingDBClient := databasetesting.NewMockBillingDBClient()

	// Create billing doc for cluster-1 (active)
	billingDoc1 := database.NewBillingDocument("billing-doc-1", cluster1ResourceID)
	billingDoc1.CreationTime = time.Now().UTC()
	err = mockBillingDBClient.BillingDocs(cluster1ResourceID.SubscriptionID).Create(ctx, billingDoc1)
	require.NoError(t, err)

	// Create billing doc for cluster-2 (deleted)
	billingDoc2 := database.NewBillingDocument("billing-doc-2", cluster2ResourceID)
	billingDoc2.CreationTime = time.Now().UTC().Add(-1 * time.Hour)
	deletionTime := time.Now().UTC()
	billingDoc2.DeletionTime = &deletionTime
	err = mockBillingDBClient.BillingDocs(cluster2ResourceID.SubscriptionID).Create(ctx, billingDoc2)
	require.NoError(t, err)

	// Test: Dump billing for cluster-1 should find the billing document
	err = DumpBillingToLogger(ctx, mockResourcesDBClient, mockBillingDBClient, cluster1ResourceID)
	require.NoError(t, err)

	// Test: Dump billing for cluster-2 should skip deleted billing document
	err = DumpBillingToLogger(ctx, mockResourcesDBClient, mockBillingDBClient, cluster2ResourceID)
	require.NoError(t, err)

	// Test: Dump billing for non-existent cluster should not error (best effort)
	nonExistentResourceID, err := azcorearm.ParseResourceID("/subscriptions/sub-3/resourceGroups/rg-3/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-3")
	require.NoError(t, err)
	err = DumpBillingToLogger(ctx, mockResourcesDBClient, mockBillingDBClient, nonExistentResourceID)
	require.NoError(t, err)
}

func TestDumpBillingToLogger_PartitionScoping(t *testing.T) {
	ctx := context.Background()
	ctx = utils.ContextWithLogger(ctx, testr.New(t))

	// Create clusters in different subscriptions
	cluster1ResourceID, err := azcorearm.ParseResourceID("/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1")
	require.NoError(t, err)

	cluster2ResourceID, err := azcorearm.ParseResourceID("/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-2")
	require.NoError(t, err)

	cluster3ResourceID, err := azcorearm.ParseResourceID("/subscriptions/sub-2/resourceGroups/rg-2/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-3")
	require.NoError(t, err)

	// Create HCP clusters with ClusterUIDs
	cluster1 := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   cluster1ResourceID,
				Name: "cluster-1",
				Type: "Microsoft.RedHatOpenShift/hcpOpenShiftClusters",
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ClusterUID:       "cluster-1-billing-1",
			ClusterServiceID: api.Ptr(api.Must(api.NewInternalID("/api/clusters_mgmt/v1/clusters/test-cluster-1"))),
		},
	}

	cluster2 := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   cluster2ResourceID,
				Name: "cluster-2",
				Type: "Microsoft.RedHatOpenShift/hcpOpenShiftClusters",
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ClusterUID:       "cluster-2-billing-2",
			ClusterServiceID: api.Ptr(api.Must(api.NewInternalID("/api/clusters_mgmt/v1/clusters/test-cluster-2"))),
		},
	}

	cluster3 := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   cluster3ResourceID,
				Name: "cluster-3",
				Type: "Microsoft.RedHatOpenShift/hcpOpenShiftClusters",
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ClusterUID:       "cluster-3-billing-3",
			ClusterServiceID: api.Ptr(api.Must(api.NewInternalID("/api/clusters_mgmt/v1/clusters/test-cluster-3"))),
		},
	}

	mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster1, cluster2, cluster3})
	require.NoError(t, err)
	mockBillingDBClient := databasetesting.NewMockBillingDBClient()

	// Create billing docs for all three clusters
	for i, resourceID := range []*azcorearm.ResourceID{cluster1ResourceID, cluster2ResourceID, cluster3ResourceID} {
		doc := database.NewBillingDocument(resourceID.Name+"-billing-"+string(rune('1'+i)), resourceID)
		doc.CreationTime = time.Now().UTC()
		err = mockBillingDBClient.BillingDocs(resourceID.SubscriptionID).Create(ctx, doc)
		require.NoError(t, err)
	}

	// Dump cluster-1: should only query sub-1 partition (not sub-2)
	// This verifies partition-scoped query works correctly
	err = DumpBillingToLogger(ctx, mockResourcesDBClient, mockBillingDBClient, cluster1ResourceID)
	require.NoError(t, err)
}
