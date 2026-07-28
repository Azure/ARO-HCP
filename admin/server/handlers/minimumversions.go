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

package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/blang/semver/v4"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// AllClustersMinimumVersionsHandler sets ServiceProviderClusterSpecVersion
// MinimumVersions on every ServiceProviderCluster in the database.
// It is a best-effort operation: precondition failures on individual SPCs
// are logged and skipped.
type AllClustersMinimumVersionsHandler struct {
	resourcesDBClient database.ResourcesDBClient
}

func NewAllClustersMinimumVersionsHandler(resourcesDBClient database.ResourcesDBClient) *AllClustersMinimumVersionsHandler {
	return &AllClustersMinimumVersionsHandler{resourcesDBClient: resourcesDBClient}
}

type allClustersMinimumVersionsRequest struct {
	MinimumVersions []string `json:"minimumVersions"`
}

type allClustersMinimumVersionsResponse struct {
	UpdatedCount int `json:"updatedCount"`
}

func (h *AllClustersMinimumVersionsHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) error {
	ctx := request.Context()
	logger := utils.LoggerFromContext(ctx)

	var body allClustersMinimumVersionsRequest
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		return arm.NewCloudError(http.StatusBadRequest, arm.CloudErrorCodeInvalidRequestContent, "", "invalid JSON body: %v", err)
	}

	parsed, err := parseAndValidateVersions(body.MinimumVersions)
	if err != nil {
		return err
	}

	iterator, err := h.resourcesDBClient.ResourcesGlobalListers().ServiceProviderClusters().List(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to list ServiceProviderClusters: %w", err)
	}

	updatedCount := 0
	for _, spc := range iterator.Items(ctx) {
		replacement := spc.DeepCopy()
		replacement.Spec.ControlPlaneVersion.MinimumVersions = parsed

		clusterResourceID := spc.ResourceID.Parent
		_, err := h.resourcesDBClient.ServiceProviderClusters(
			clusterResourceID.SubscriptionID,
			clusterResourceID.ResourceGroupName,
			clusterResourceID.Name,
		).Replace(ctx, replacement, nil)
		if err != nil {
			if database.IsPreconditionFailedError(err) {
				logger.Info("skipping SPC due to precondition failure", "resourceID", spc.ResourceID.String())
				continue
			}
			return fmt.Errorf("failed to replace ServiceProviderCluster %s: %w", spc.ResourceID.String(), err)
		}
		updatedCount++
	}
	if err := iterator.GetError(); err != nil {
		return fmt.Errorf("failed to iterate ServiceProviderClusters: %w", err)
	}

	_, err = arm.WriteJSONResponse(writer, http.StatusOK, allClustersMinimumVersionsResponse{UpdatedCount: updatedCount})
	return utils.TrackError(err)
}

// parseAndValidateVersions validates and converts string versions to semver.Version.
// Returns nil (not an empty slice) when the input is nil or empty, which clears
// the field on the SPC.
func parseAndValidateVersions(versions []string) ([]semver.Version, error) {
	if len(versions) == 0 {
		return nil, nil
	}
	parsed := make([]semver.Version, 0, len(versions))
	for _, s := range versions {
		v, err := semver.Parse(s)
		if err != nil {
			return nil, arm.NewCloudError(http.StatusBadRequest, arm.CloudErrorCodeInvalidRequestContent, "", "invalid semver %q: %v", s, err)
		}
		parsed = append(parsed, v)
	}
	return parsed, nil
}
