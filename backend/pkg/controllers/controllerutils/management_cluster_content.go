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
	"fmt"
	"net/http"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// newInitialManagementClusterContent returns a new ManagementClusterContent with
// the given resource ID as its parent. The resource ID is assumed to be a
// cluster resource ID.
// The returned value can be used to consistently initialize a new ManagementClusterContent
func newInitialManagementClusterContent(managementClusterContentResourceID *azcorearm.ResourceID) *api.ManagementClusterContent {
	return &api.ManagementClusterContent{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: managementClusterContentResourceID,
		},
		ResourceID: *managementClusterContentResourceID,
	}
}

// managementClusterContentResourceIDFromClusterResourceID returns the resource ID for the
// ManagementClusterContent associated to the given cluster resource ID and maestro bundle internal name.
func managementClusterContentResourceIDFromClusterResourceID(clusterResourceID *azcorearm.ResourceID, maestroBundleInternalName api.MaestroBundleInternalName) *azcorearm.ResourceID {
	return api.Must(azcorearm.ParseResourceID(fmt.Sprintf("%s/%s/%s", clusterResourceID.String(), api.ManagementClusterContentResourceTypeName, maestroBundleInternalName)))
}

// GetOrCreateManagementClusterContent gets the ManagementClusterContent
// instance for the given cluster resource ID.
// If it doesn't exist, it creates a new one.
// clusterResourceID is assumed to be a cluster resource ID.
func GetOrCreateManagementClusterContent(
	ctx context.Context, dbClient database.DBClient, clusterResourceID *azcorearm.ResourceID, maestroBundleInternalName api.MaestroBundleInternalName,
) (*api.ManagementClusterContent, error) {
	// Azure resource types are case-insensitive; ToClusterResourceIDString lowercases the path so parsed IDs may have lowercase type.
	if !strings.EqualFold(clusterResourceID.ResourceType.String(), api.ClusterResourceType.String()) {
		return nil, utils.TrackError(fmt.Errorf("expected resource type %s, got %s", api.ClusterResourceType, clusterResourceID.ResourceType))
	}

	managementClusterContentsDBClient := dbClient.ManagementClusterContents(
		clusterResourceID.SubscriptionID,
		clusterResourceID.ResourceGroupName,
		clusterResourceID.Name,
	)

	resourceID := managementClusterContentResourceIDFromClusterResourceID(clusterResourceID, maestroBundleInternalName)
	managementClusterContentName := string(maestroBundleInternalName)
	existingManagementClusterContent, err := managementClusterContentsDBClient.Get(ctx, managementClusterContentName)
	if err == nil {
		return existingManagementClusterContent, nil
	}

	if !database.IsResponseError(err, http.StatusNotFound) {
		return nil, utils.TrackError(fmt.Errorf("failed to get ManagementClusterContent: %w", err))
	}

	initialManagementClusterContent := newInitialManagementClusterContent(resourceID)
	existingManagementClusterContent, err = managementClusterContentsDBClient.Create(ctx, initialManagementClusterContent, nil)
	if err == nil {
		return existingManagementClusterContent, nil
	}

	// We optimize here and if creation failed because it already exists, we try
	// to get again one last time.
	// According to the Cosmos DB API documentation, a HTTP 409 Conflict error
	// is returned when the item already exists: https://learn.microsoft.com/en-us/rest/api/cosmos-db/create-a-document#status-codes
	if !database.IsResponseError(err, http.StatusConflict) {
		return nil, utils.TrackError(fmt.Errorf("failed to create ManagementClusterContent: %w", err))
	}

	existingManagementClusterContent, err = managementClusterContentsDBClient.Get(ctx, managementClusterContentName)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get ManagementClusterContent: %w", err))
	}

	return existingManagementClusterContent, nil
}
