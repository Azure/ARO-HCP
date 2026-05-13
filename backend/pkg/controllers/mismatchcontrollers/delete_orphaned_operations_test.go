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

package mismatchcontrollers

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	testSubscriptionID = "a433a095-1277-44f1-8453-8d61a4d848c2"
)

// TestSynchronizeSubscription_BrokenOperation_Deleted drives the controller through its
// public entry point: an operation document with no top-level resourceID is loaded into the
// store, synchronizeSubscription is invoked, and we verify the broken row is gone and the
// healthy row is left alone. The broken row is the case the data-dumper had to be made
// nil-safe for (commit 6eb9565be) — this controller is what actually deletes it.
func TestSynchronizeSubscription_BrokenOperation_Deleted(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

	const (
		brokenCosmosID  = "37332504-70b3-4796-9af6-301a5d47d127"
		healthyCosmosID = "ab5d298a-2267-432a-ad71-313bbdae0349"
	)

	mockClient := databasetesting.NewMockResourcesDBClient()
	mockClient.StoreDocument(brokenCosmosID, brokenOperationDoc(t, brokenCosmosID))
	mockClient.StoreDocument(healthyCosmosID, healthyOperationDoc(t, healthyCosmosID))

	c := &deleteOrphanedOperations{
		name:              "DeleteOrphanedOperations",
		resourcesDBClient: mockClient,
	}

	require.NoError(t, c.synchronizeSubscription(ctx, testSubscriptionID))

	_, brokenStill := mockClient.GetDocument(brokenCosmosID)
	assert.False(t, brokenStill, "broken operation must be deleted by synchronizeSubscription")

	_, healthyStill := mockClient.GetDocument(healthyCosmosID)
	assert.True(t, healthyStill, "healthy operation must survive synchronizeSubscription")
}

// TestSynchronizeSubscription_StrayNonOperation_LeftAlone confirms scope: a non-operation
// document with no top-level resourceID is out of scope for this controller and must not
// be deleted (deleteOrphanedCosmosResources owns those).
func TestSynchronizeSubscription_StrayNonOperation_LeftAlone(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

	const strayCosmosID = "stray-cluster-with-no-resource-id"

	mockClient := databasetesting.NewMockResourcesDBClient()
	mockClient.StoreDocument(strayCosmosID, mustMarshal(t, map[string]any{
		"id":           strayCosmosID,
		"partitionKey": testSubscriptionID,
		"resourceID":   nil,
		"resourceType": api.ClusterResourceType.String(),
	}))

	c := &deleteOrphanedOperations{
		name:              "DeleteOrphanedOperations",
		resourcesDBClient: mockClient,
	}

	require.NoError(t, c.synchronizeSubscription(ctx, testSubscriptionID))

	_, stillThere := mockClient.GetDocument(strayCosmosID)
	assert.True(t, stillThere, "non-operation documents are out of scope for this controller")
}

// TestSynchronizeSubscription_PreMigrationClusterDoc_LeftAlone covers three coexisting cases
// in the same partition:
//
//  1. A pre-migration *cluster* doc (pipe-delimited cosmosID, no top-level resourceID). The
//     convert back-fills its resourceID on read; the operations controller must leave it alone
//     because cluster cleanup is out of scope (owned by deleteOrphanedCosmosResources).
//  2. A pre-migration *operation* doc (pipe-delimited cosmosID, no top-level resourceID). The
//     convert back-fills its resourceID too, but the row is still stale cruft under the old
//     cosmosID scheme — the controller deletes it so the frontend can re-write under the new
//     UUID-cosmosID scheme on next access.
//  3. A genuinely-broken operation (UUID cosmosID, no top-level resourceID) — unreachable via
//     resourceID, deleted via DeleteByCosmosID.
func TestSynchronizeSubscription_PreMigrationClusterDoc_LeftAlone(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

	// Pipe-delimited cosmosIDs — what pre-migration docs look like in cosmos. The convert layer
	// parses these into a ResourceID by replacing "|" with "/".
	const (
		preMigrationClusterCosmosID   = "|subscriptions|a433a095-1277-44f1-8453-8d61a4d848c2|resourcegroups|unimportantpostponement|providers|microsoft.redhatopenshift|hcpopenshiftclusters|monstrousprecinct"
		preMigrationOperationCosmosID = "|subscriptions|a433a095-1277-44f1-8453-8d61a4d848c2|providers|microsoft.redhatopenshift|hcpoperationstatuses|ab5d298a-2267-432a-ad71-313bbdae0349"
		brokenOperationCosmosID       = "37332504-70b3-4796-9af6-301a5d47d127"
	)

	mockClient := databasetesting.NewMockResourcesDBClient()

	mockClient.StoreDocument(preMigrationClusterCosmosID, mustMarshal(t, map[string]any{
		"id":           preMigrationClusterCosmosID,
		"partitionKey": testSubscriptionID,
		// no top-level resourceID — the "not serialized yet" case.
		"resourceType": api.ClusterResourceType.String(),
		"properties": map[string]any{
			"cosmosMetadata": map[string]any{
				"resourceID": "/subscriptions/" + testSubscriptionID + "/resourceGroups/unimportantPostponement/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/monstrousPrecinct",
			},
		},
	}))

	mockClient.StoreDocument(preMigrationOperationCosmosID, mustMarshal(t, map[string]any{
		"id":           preMigrationOperationCosmosID,
		"partitionKey": testSubscriptionID,
		// no top-level resourceID — convert will back-fill from the pipe-style cosmosID.
		"resourceType": api.OperationStatusResourceType.String(),
		"properties": map[string]any{
			"request": "Create",
			"status":  "Succeeded",
		},
	}))

	mockClient.StoreDocument(brokenOperationCosmosID, brokenOperationDoc(t, brokenOperationCosmosID))

	c := &deleteOrphanedOperations{
		name:              "DeleteOrphanedOperations",
		resourcesDBClient: mockClient,
	}

	require.NoError(t, c.synchronizeSubscription(ctx, testSubscriptionID))

	_, clusterStill := mockClient.GetDocument(preMigrationClusterCosmosID)
	assert.True(t, clusterStill, "pre-migration cluster doc must not be deleted by the operations controller")

	_, preMigrationOpStill := mockClient.GetDocument(preMigrationOperationCosmosID)
	assert.False(t, preMigrationOpStill, "pre-migration operation (pipe-style cosmosID) must be deleted")

	_, brokenStill := mockClient.GetDocument(brokenOperationCosmosID)
	assert.False(t, brokenStill, "broken operation (missing top-level resourceID) must be deleted")
}

func brokenOperationDoc(t *testing.T, cosmosID string) json.RawMessage {
	return mustMarshal(t, map[string]any{
		"id":           cosmosID,
		"partitionKey": testSubscriptionID,
		"resourceID":   nil, // <-- the bug we're cleaning up
		"resourceType": api.OperationStatusResourceType.String(),
		"properties": map[string]any{
			"request":            "Create",
			"status":             "Accepted",
			"tenantId":           "00000000-0000-0000-0000-000000000000",
			"startTime":          "2026-05-13T00:00:00Z",
			"lastTransitionTime": "2026-05-13T00:00:00Z",
		},
	})
}

func healthyOperationDoc(t *testing.T, cosmosID string) json.RawMessage {
	return mustMarshal(t, map[string]any{
		"id":           cosmosID,
		"partitionKey": testSubscriptionID,
		"resourceID":   api.ToOperationResourceIDString(testSubscriptionID, cosmosID),
		"resourceType": api.OperationStatusResourceType.String(),
		"properties": map[string]any{
			"request": "Create",
			"status":  "Succeeded",
		},
	})
}

func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return data
}
