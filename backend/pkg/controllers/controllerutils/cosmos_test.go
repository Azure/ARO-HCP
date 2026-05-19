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

package controllerutils

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// TestDeleteRecursively_NilResourceIDChild_DeletedByCosmosID verifies that when a descendent
// row has no top-level resourceID (so we can't address it via Delete(*ResourceID) — that would
// panic on the nil pointer), DeleteRecursively falls back to DeleteByCosmosID instead of
// skipping. Otherwise the malformed row would survive the recursive delete and become an
// orphan under a tree whose parent is gone.
func TestDeleteRecursively_NilResourceIDChild_DeletedByCosmosID(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

	const (
		subscriptionID    = "a433a095-1277-44f1-8453-8d61a4d848c2"
		resourceGroupName = "unimportantpostponement"
		clusterName       = "monstrousprecinct"
		// UUID cosmosID — does NOT parse as a pipe-style resource path, so the lenient convert
		// surfaces this row with ResourceID == nil rather than back-filling it.
		malformedChildCosmosID = "07412d96-d4e7-4f4e-a3ab-aa7c0fe7df0f"
	)

	clusterResourceID := api.Must(api.ToClusterResourceID(subscriptionID, resourceGroupName, clusterName))
	clusterCosmosID := api.Must(arm.ResourceIDToCosmosID(clusterResourceID))

	healthyChildResourceID := api.Must(azcorearm.ParseResourceID(
		clusterResourceID.String() + "/" + api.ControllerResourceTypeName + "/healthy",
	))
	healthyChildCosmosID := api.Must(arm.ResourceIDToCosmosID(healthyChildResourceID))

	mockClient := databasetesting.NewMockResourcesDBClient()

	// Root: a valid cluster doc.
	mockClient.StoreDocument(clusterCosmosID, mustMarshalDoc(t, map[string]any{
		"id":           clusterCosmosID,
		"partitionKey": subscriptionID,
		"resourceID":   clusterResourceID.String(),
		"resourceType": api.ClusterResourceType.String(),
		"properties":   map[string]any{},
	}))

	// Healthy child: well-formed resourceID under the cluster. Should be deleted via Delete.
	mockClient.StoreDocument(healthyChildCosmosID, mustMarshalDoc(t, map[string]any{
		"id":           healthyChildCosmosID,
		"partitionKey": subscriptionID,
		"resourceID":   healthyChildResourceID.String(),
		"resourceType": api.ClusterControllerResourceType.String(),
		"properties":   map[string]any{},
	}))

	// Malformed child: missing top-level resourceID, UUID cosmosID. Must be deleted via DeleteByCosmosID.
	mockClient.StoreDocument(malformedChildCosmosID, mustMarshalDoc(t, map[string]any{
		"id":           malformedChildCosmosID,
		"partitionKey": subscriptionID,
		"resourceID":   nil,
		"resourceType": api.ClusterControllerResourceType.String(),
		"properties":   map[string]any{},
	}))

	require.NoError(t, DeleteRecursively(ctx, mockClient, clusterResourceID))

	_, healthyStill := mockClient.GetDocument(healthyChildCosmosID)
	assert.False(t, healthyStill, "healthy child should be deleted via Delete(resourceID)")

	_, malformedStill := mockClient.GetDocument(malformedChildCosmosID)
	assert.False(t, malformedStill, "malformed child with nil resourceID should be deleted via DeleteByCosmosID fallback")

	_, rootStill := mockClient.GetDocument(clusterCosmosID)
	assert.False(t, rootStill, "root cluster doc should be deleted last")
}

func mustMarshalDoc(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return data
}
