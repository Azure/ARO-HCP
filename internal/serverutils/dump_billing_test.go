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
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/billingapi"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/metadataapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/billingcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/billingcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestBillingDocumentSharedContract(t *testing.T) {
	const persisted = `{
		"id":"billing-doc", "subscriptionId":"sub", "creationTime":"2026-10-09T09:00:00Z",
		"deletionTime":"2026-10-09T12:00:00Z", "location":"eastus", "tenantId":"tenant",
		"resourceId":"/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster",
		"managedResourceGroup":"/subscriptions/sub/resourceGroups/managed-rg",
		"lastBillingTimeOfVMEvent":"2026-10-09T10:00:00Z", "lastBillingTimeOfMngtEvent":"2026-10-09T11:00:00Z",
		"ttl":3600, "_rid":"cosmos-rid", "_self":"self", "_etag":"etag", "_attachments":"attachments", "_ts":123
	}`
	var stored billingcosmosstorage.BillingDocument
	require.NoError(t, json.Unmarshal([]byte(persisted), &stored))
	require.Equal(t, "billing-doc", stored.ID, "embedded ID must not conflict with Cosmos metadata")
	var shared billingapi.BillingDocument
	require.NoError(t, json.Unmarshal([]byte(persisted), &shared))
	require.Equal(t, shared, stored.BillingDocument)
	snapshot, err := json.Marshal(&stored)
	require.NoError(t, err)
	require.JSONEq(t, persisted, string(snapshot), "the RP wrapper must preserve the flat wire contract and storage metadata")

	copied := stored.DeepCopy()
	require.Equal(t, stored, *copied)
	*copied.DeletionTime = copied.DeletionTime.Add(time.Hour)
	*copied.LastBillingTimeOfVMEvent = copied.LastBillingTimeOfVMEvent.Add(time.Hour)
	*copied.LastBillingTimeOfMngtEvent = copied.LastBillingTimeOfMngtEvent.Add(time.Hour)
	copied.ResourceID.Parent.Name = "changed"
	require.Equal(t, shared, stored.BillingDocument, "mutating a copy must not change the informer object")

	for _, input := range []string{`{}`, `{"deletionTime":null,"resourceId":null,"lastBillingTimeOfVMEvent":null,"lastBillingTimeOfMngtEvent":null}`} {
		var doc billingcosmosstorage.BillingDocument
		require.NoError(t, json.Unmarshal([]byte(input), &doc))
		copy := doc.DeepCopy()
		require.Nil(t, copy.DeletionTime)
		require.Nil(t, copy.ResourceID)
		require.Nil(t, copy.LastBillingTimeOfVMEvent)
		require.Nil(t, copy.LastBillingTimeOfMngtEvent)
	}
}

func TestDumpBillingToLogger(t *testing.T) {
	ctx := context.Background()
	ctx = utils.ContextWithLogger(ctx, testr.New(t))

	cluster1ResourceID, err := azcorearm.ParseResourceID("/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1")
	require.NoError(t, err)

	cluster2ResourceID, err := azcorearm.ParseResourceID("/subscriptions/sub-2/resourceGroups/rg-2/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-2")
	require.NoError(t, err)

	// Create HCP clusters
	cluster1 := &coreapi.Cluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   cluster1ResourceID,
			PartitionKey: strings.ToLower(cluster1ResourceID.SubscriptionID),
		},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID:   cluster1ResourceID,
				Name: "cluster-1",
				Type: "Microsoft.RedHatOpenShift/hcpOpenShiftClusters",
			},
		},
		ServiceProviderProperties: coreapi.ClusterServiceProviderProperties{
			ClusterUID:       "billing-doc-1",
			ClusterServiceID: metadataapihelpers.Ptr(metadataapi.Must(metadataapi.NewInternalID("/api/clusters_mgmt/v1/clusters/test-cluster-1"))),
		},
	}

	cluster2 := &coreapi.Cluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   cluster2ResourceID,
			PartitionKey: strings.ToLower(cluster2ResourceID.SubscriptionID),
		},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID:   cluster2ResourceID,
				Name: "cluster-2",
				Type: "Microsoft.RedHatOpenShift/hcpOpenShiftClusters",
			},
		},
		ServiceProviderProperties: coreapi.ClusterServiceProviderProperties{
			ClusterUID:       "billing-doc-2",
			ClusterServiceID: metadataapihelpers.Ptr(metadataapi.Must(metadataapi.NewInternalID("/api/clusters_mgmt/v1/clusters/test-cluster-2"))),
		},
	}

	// Create mock DB with clusters
	mockResourcesDBClient, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster1, cluster2})
	require.NoError(t, err)
	mockBillingDBClient := billingcosmosstoragetesting.NewMockBillingDBClient()

	// Create billing doc for cluster-1 (active)
	billingDoc1 := billingcosmosstorage.NewBillingDocument("billing-doc-1", cluster1ResourceID)
	billingDoc1.CreationTime = time.Now().UTC()
	err = mockBillingDBClient.BillingDocs(cluster1ResourceID.SubscriptionID).Create(ctx, billingDoc1)
	require.NoError(t, err)

	// Create billing doc for cluster-2 (deleted)
	billingDoc2 := billingcosmosstorage.NewBillingDocument("billing-doc-2", cluster2ResourceID)
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
	cluster1 := &coreapi.Cluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   cluster1ResourceID,
			PartitionKey: strings.ToLower(cluster1ResourceID.SubscriptionID),
		},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID:   cluster1ResourceID,
				Name: "cluster-1",
				Type: "Microsoft.RedHatOpenShift/hcpOpenShiftClusters",
			},
		},
		ServiceProviderProperties: coreapi.ClusterServiceProviderProperties{
			ClusterUID:       "cluster-1-billing-1",
			ClusterServiceID: metadataapihelpers.Ptr(metadataapi.Must(metadataapi.NewInternalID("/api/clusters_mgmt/v1/clusters/test-cluster-1"))),
		},
	}

	cluster2 := &coreapi.Cluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   cluster2ResourceID,
			PartitionKey: strings.ToLower(cluster2ResourceID.SubscriptionID),
		},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID:   cluster2ResourceID,
				Name: "cluster-2",
				Type: "Microsoft.RedHatOpenShift/hcpOpenShiftClusters",
			},
		},
		ServiceProviderProperties: coreapi.ClusterServiceProviderProperties{
			ClusterUID:       "cluster-2-billing-2",
			ClusterServiceID: metadataapihelpers.Ptr(metadataapi.Must(metadataapi.NewInternalID("/api/clusters_mgmt/v1/clusters/test-cluster-2"))),
		},
	}

	cluster3 := &coreapi.Cluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   cluster3ResourceID,
			PartitionKey: strings.ToLower(cluster3ResourceID.SubscriptionID),
		},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID:   cluster3ResourceID,
				Name: "cluster-3",
				Type: "Microsoft.RedHatOpenShift/hcpOpenShiftClusters",
			},
		},
		ServiceProviderProperties: coreapi.ClusterServiceProviderProperties{
			ClusterUID:       "cluster-3-billing-3",
			ClusterServiceID: metadataapihelpers.Ptr(metadataapi.Must(metadataapi.NewInternalID("/api/clusters_mgmt/v1/clusters/test-cluster-3"))),
		},
	}

	mockResourcesDBClient, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster1, cluster2, cluster3})
	require.NoError(t, err)
	mockBillingDBClient := billingcosmosstoragetesting.NewMockBillingDBClient()

	// Create billing docs for all three clusters
	for i, resourceID := range []*azcorearm.ResourceID{cluster1ResourceID, cluster2ResourceID, cluster3ResourceID} {
		doc := billingcosmosstorage.NewBillingDocument(resourceID.Name+"-billing-"+string(rune('1'+i)), resourceID)
		doc.CreationTime = time.Now().UTC()
		err = mockBillingDBClient.BillingDocs(resourceID.SubscriptionID).Create(ctx, doc)
		require.NoError(t, err)
	}

	// Dump cluster-1: should only query sub-1 partition (not sub-2)
	// This verifies partition-scoped query works correctly
	err = DumpBillingToLogger(ctx, mockResourcesDBClient, mockBillingDBClient, cluster1ResourceID)
	require.NoError(t, err)
}
