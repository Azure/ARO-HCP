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

package hcp

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// HCPDesiredControlPlaneSizeHandler sets ServiceProviderClusterSpec
// DesiredHostedClusterControlPlaneSize on the per-cluster ServiceProviderCluster.
// It intentionally writes only that one field — every other Spec/Status value
// is left as-is — so SRE callers can adjust the sizing tier without touching
// anything else on the document.
type HCPDesiredControlPlaneSizeHandler struct {
	resourcesDBClient database.ResourcesDBClient
}

func NewHCPDesiredControlPlaneSizeHandler(resourcesDBClient database.ResourcesDBClient) *HCPDesiredControlPlaneSizeHandler {
	return &HCPDesiredControlPlaneSizeHandler{resourcesDBClient: resourcesDBClient}
}

// desiredControlPlaneSizeRequest is the wire shape for the request body. We
// use a pointer-string so a missing field is distinguishable from an
// explicit empty string (which we reject).
type desiredControlPlaneSizeRequest struct {
	Size *string `json:"size"`
}

func (h *HCPDesiredControlPlaneSizeHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) error {
	resourceID, err := utils.ResourceIDFromContext(request.Context())
	if err != nil {
		return fmt.Errorf("failed to get resource ID: %w", err)
	}

	var body desiredControlPlaneSizeRequest
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		_, _ = arm.WriteJSONResponse(writer, http.StatusBadRequest, map[string]any{
			"error": fmt.Sprintf("invalid JSON body: %v", err),
		})
		return nil
	}
	if body.Size == nil {
		_, _ = arm.WriteJSONResponse(writer, http.StatusBadRequest, map[string]any{
			"error": "size is required",
		})
		return nil
	}
	if !api.IsValidHostedClusterControlPlaneSize(*body.Size) {
		_, _ = arm.WriteJSONResponse(writer, http.StatusBadRequest, map[string]any{
			"error": fmt.Sprintf("size %q must be one of Small, Medium, Large", *body.Size),
		})
		return nil
	}

	existing, err := database.GetOrCreateServiceProviderCluster(request.Context(), h.resourcesDBClient, resourceID)
	if err != nil {
		return fmt.Errorf("failed to get ServiceProviderCluster: %w", err)
	}

	replacement := existing.DeepCopy()
	replacement.Spec.DesiredHostedClusterControlPlaneSize = body.Size

	_, err = h.resourcesDBClient.ServiceProviderClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Name).Replace(request.Context(), replacement, nil)
	if err != nil {
		return fmt.Errorf("failed to replace ServiceProviderCluster: %w", err)
	}

	_, err = arm.WriteJSONResponse(writer, http.StatusOK, desiredControlPlaneSizeRequest{Size: body.Size})
	return utils.TrackError(err)
}
