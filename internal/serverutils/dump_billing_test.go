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
	"github.com/google/go-cmp/cmp"
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

func TestBillingDocumentWatermarks(t *testing.T) {
	t.Run("preserved in snapshots and independent in deep copies", func(t *testing.T) {
		const persisted = `{"lastBillingTimeOfVMEvent":"2026-10-09T10:00:00Z","lastBillingTimeOfMngtEvent":"2026-10-09T11:00:00Z"}`
		var doc billingcosmosstorage.BillingDocument
		require.NoError(t, json.Unmarshal([]byte(persisted), &doc))
		require.NotNil(t, doc.LastBillingTimeOfVMEvent)
		require.NotNil(t, doc.LastBillingTimeOfMngtEvent)
		require.Equal(t, time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC), *doc.LastBillingTimeOfVMEvent)
		require.Equal(t, time.Date(2026, 10, 9, 11, 0, 0, 0, time.UTC), *doc.LastBillingTimeOfMngtEvent)

		// Diagnostic snapshots serialize the typed billing document back to JSON.
		snapshot, err := json.Marshal(&doc)
		require.NoError(t, err)
		var fields map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(snapshot, &fields))
		require.JSONEq(t, `"2026-10-09T10:00:00Z"`, string(fields["lastBillingTimeOfVMEvent"]))
		require.JSONEq(t, `"2026-10-09T11:00:00Z"`, string(fields["lastBillingTimeOfMngtEvent"]))

		copied := doc.DeepCopy()
		*copied.LastBillingTimeOfVMEvent = copied.LastBillingTimeOfVMEvent.Add(time.Hour)
		*copied.LastBillingTimeOfMngtEvent = copied.LastBillingTimeOfMngtEvent.Add(time.Hour)
		require.Equal(t, time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC), *doc.LastBillingTimeOfVMEvent)
		require.Equal(t, time.Date(2026, 10, 9, 11, 0, 0, 0, time.UTC), *doc.LastBillingTimeOfMngtEvent)
	})

	for _, persisted := range []string{`{}`, `{"lastBillingTimeOfVMEvent":null,"lastBillingTimeOfMngtEvent":null}`} {
		t.Run(persisted, func(t *testing.T) {
			var doc billingcosmosstorage.BillingDocument
			require.NoError(t, json.Unmarshal([]byte(persisted), &doc))
			copied := doc.DeepCopy()
			require.Nil(t, copied.LastBillingTimeOfVMEvent)
			require.Nil(t, copied.LastBillingTimeOfMngtEvent)
			snapshot, err := json.Marshal(copied)
			require.NoError(t, err)
			require.NotContains(t, string(snapshot), "lastBillingTimeOfVMEvent")
			require.NotContains(t, string(snapshot), "lastBillingTimeOfMngtEvent")
		})
	}
}

func TestBillingDocumentSharedContract(t *testing.T) {
	const fullDocument = `{
		"id":"billing-doc", "subscriptionId":"sub", "creationTime":"2026-10-09T09:00:00Z",
		"deletionTime":"2026-10-09T12:00:00Z", "location":"eastus", "tenantId":"tenant",
		"resourceId":"/subscriptions/sub/resourceGroups/RG/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/Cluster",
		"managedResourceGroup":"/subscriptions/sub/resourceGroups/managed-rg",
		"lastBillingTimeOfVMEvent":"2026-10-09T10:00:00Z", "lastBillingTimeOfMngtEvent":"2026-10-09T11:00:00Z",
		"orphanedSince":"2026-10-09T11:30:00Z",
		"ttl":3600, "_rid":"cosmos-rid", "_self":"self", "_etag":"etag", "_attachments":"attachments", "_ts":123
	}`
	for _, tc := range []struct {
		name     string
		input    string
		wantJSON string
		wantErr  bool
	}{
		{name: "complete flat document", input: fullDocument, wantJSON: fullDocument},
		{name: "missing optional fields", input: `{"id":"legacy","creationTime":"2026-10-09T09:00:00Z"}`, wantJSON: `{"id":"legacy","creationTime":"2026-10-09T09:00:00Z"}`},
		{name: "null optional fields", input: `{"creationTime":"2026-10-09T09:00:00Z","resourceId":null,"deletionTime":null,"lastBillingTimeOfVMEvent":null,"lastBillingTimeOfMngtEvent":null,"orphanedSince":null}`, wantJSON: `{"creationTime":"2026-10-09T09:00:00Z"}`},
		{name: "empty resource ID", input: `{"resourceId":""}`, wantErr: true},
		{name: "malformed resource ID", input: `{"resourceId":"not-an-arm-id"}`, wantErr: true},
		{name: "object resource ID", input: `{"resourceId":{"name":"cluster"}}`, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stored billingcosmosstorage.BillingDocument
			var shared billingapi.BillingDocument
			storedErr := json.Unmarshal([]byte(tc.input), &stored)
			sharedErr := json.Unmarshal([]byte(tc.input), &shared)
			if tc.wantErr {
				require.Error(t, storedErr)
				require.Error(t, sharedErr)
				return
			}
			require.NoError(t, storedErr)
			require.NoError(t, sharedErr)
			compareOptions := cmp.AllowUnexported(azcorearm.ResourceID{}, azcorearm.ResourceType{})
			if diff := cmp.Diff(shared, stored.BillingDocument, compareOptions); diff != "" {
				t.Fatalf("shared contract (-want +got):\n%s", diff)
			}
			snapshot, err := json.Marshal(&stored)
			require.NoError(t, err)
			require.JSONEq(t, tc.wantJSON, string(snapshot), "preserve the flat wire contract and RP metadata")
			var legacy struct {
				ResourceID string `json:"resourceId"`
			}
			require.NoError(t, json.Unmarshal([]byte(tc.input), &legacy))
			if shared.ResourceID != nil {
				if diff := cmp.Diff(legacy.ResourceID, shared.ResourceID.String()); diff != "" {
					t.Fatalf("Billing resource ID string (-want +got):\n%s", diff)
				}
			}
			copied := stored.DeepCopy()
			if diff := cmp.Diff(&stored, copied, compareOptions); diff != "" {
				t.Fatalf("deep copy (-want +got):\n%s", diff)
			}
			for _, timestamps := range []struct {
				name     string
				original *time.Time
				copied   *time.Time
			}{
				{"deletion", stored.DeletionTime, copied.DeletionTime},
				{"compute watermark", stored.LastBillingTimeOfVMEvent, copied.LastBillingTimeOfVMEvent},
				{"hosting watermark", stored.LastBillingTimeOfMngtEvent, copied.LastBillingTimeOfMngtEvent},
				{"orphan candidacy", stored.OrphanedSince, copied.OrphanedSince},
			} {
				if timestamps.original != nil && timestamps.original == timestamps.copied {
					t.Errorf("%s timestamp aliases the original", timestamps.name)
				}
			}
			checkResourceIDCopy(t, stored.ResourceID, copied.ResourceID)
		})
	}
	require.Nil(t, (*billingapi.BillingDocument)(nil).DeepCopy())
}

func checkResourceIDCopy(t *testing.T, original, copied *azcorearm.ResourceID) {
	t.Helper()
	if original == nil {
		return
	}
	if original == copied {
		t.Fatal("resource ID aliases the original")
	}
	if len(original.ResourceType.Types) > 0 && &original.ResourceType.Types[0] == &copied.ResourceType.Types[0] {
		t.Fatal("resource type slice aliases the original")
	}
	checkResourceIDCopy(t, original.Parent, copied.Parent)
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
