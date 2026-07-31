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

	"github.com/blang/semver/v4"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// HCPMinimumNodePoolVersionHandler sets ServiceProviderNodePoolSpecVersion
// MinimumVersion on the per-nodepool ServiceProviderNodePool.
// It intentionally writes only that one field — every other Spec/Status value
// is left as-is — so SRE callers can adjust the version of a particular nodepool
// without touching anything else on the document.
type HCPMinimumNodePoolVersionHandler struct {
	resourcesDBClient database.ResourcesDBClient
}

func NewHCPMinimumNodePoolVersionHandler(resourcesDBClient database.ResourcesDBClient) *HCPMinimumNodePoolVersionHandler {
	return &HCPMinimumNodePoolVersionHandler{resourcesDBClient: resourcesDBClient}
}

// minimumNodePoolVersionRequest is the wire shape for the request body. The
// MinimumVersion field is a pointer-string so callers can distinguish "set to value"
// (non-nil) from "clear the version" (nil/absent). An explicit empty string is
// rejected.
type minimumNodePoolVersionRequest struct {
	MinimumVersion *string `json:"minimumVersion"`
}

func (h *HCPMinimumNodePoolVersionHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) error {
	clusterResourceID, err := utils.ResourceIDFromContext(request.Context())
	if err != nil {
		return arm.NewCloudError(http.StatusBadRequest, arm.CloudErrorCodeInvalidRequestContent, "", "invalid resource identifier in request")
	}

	nodepoolName := request.PathValue("nodepoolName")
	if nodepoolName == "" {
		return arm.NewCloudError(http.StatusBadRequest, arm.CloudErrorCodeInvalidRequestContent, "", "nodepoolName parameter is required")
	}

	resourceID, err := api.ToNodePoolResourceID(clusterResourceID.SubscriptionID, clusterResourceID.ResourceGroupName, clusterResourceID.Name, nodepoolName)
	if err != nil {
		return arm.NewCloudError(http.StatusBadRequest, arm.CloudErrorCodeInvalidRequestContent, "", "failed to construct nodepool resource ID: %v", err)
	}

	var body minimumNodePoolVersionRequest
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		return arm.NewCloudError(http.StatusBadRequest, arm.CloudErrorCodeInvalidRequestContent, "", "invalid JSON body: %v", err)
	}
	// A nil body.MinimumVersion means the caller is clearing the SRE-selected version.
	// The nodepool upgrade version controller applies the effective version desired
	// by the customer instead, if that version is a valid one.
	// An explicit empty string is not a valid tier and is rejected separately from
	// the omitted case.
	if body.MinimumVersion != nil && *body.MinimumVersion == "" {
		return arm.NewCloudError(http.StatusBadRequest, arm.CloudErrorCodeInvalidRequestContent, "", "minimumVersion must not be empty; omit the field to clear")
	}

	var versionPtr *semver.Version
	if body.MinimumVersion != nil {
		version, err := semver.Parse(*body.MinimumVersion)
		if err != nil {
			return arm.NewCloudError(http.StatusBadRequest, arm.CloudErrorCodeInvalidRequestContent, "", "invalid semver %q: %v", *body.MinimumVersion, err)
		}
		versionPtr = &version
	}

	existing, err := database.GetOrCreateServiceProviderNodePool(request.Context(), h.resourcesDBClient, resourceID)
	if err != nil {
		return fmt.Errorf("failed to get ServiceProviderNodePool: %w", err)
	}

	replacement := existing.DeepCopy()
	replacement.Spec.NodePoolVersion.MinimumVersion = versionPtr

	_, err = h.resourcesDBClient.ServiceProviderNodePools(resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Parent.Name, resourceID.Name).Replace(request.Context(), replacement, nil)
	if err != nil {
		return fmt.Errorf("failed to replace ServiceProviderNodePool: %w", err)
	}

	_, err = arm.WriteJSONResponse(writer, http.StatusOK, minimumNodePoolVersionRequest{MinimumVersion: body.MinimumVersion})
	return utils.TrackError(err)
}
