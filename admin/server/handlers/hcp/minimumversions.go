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

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// HCPMinimumVersionsHandler sets ServiceProviderClusterSpecVersion
// MinimumVersions on the per-cluster ServiceProviderCluster.
// It intentionally writes only that one field -- every other Spec/Status value
// is left as-is -- so SRE callers can adjust minimum versions without touching
// anything else on the document.
type HCPMinimumVersionsHandler struct {
	resourcesDBClient corecosmosstorage.ResourcesDBClient
}

func NewHCPMinimumVersionsHandler(resourcesDBClient corecosmosstorage.ResourcesDBClient) *HCPMinimumVersionsHandler {
	return &HCPMinimumVersionsHandler{resourcesDBClient: resourcesDBClient}
}

type minimumVersionsRequest struct {
	MinimumVersions []string `json:"minimumVersions"`
}

type minimumVersionsResponse struct {
	MinimumVersions []string `json:"minimumVersions"`
}

func (h *HCPMinimumVersionsHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) error {
	resourceID, err := utils.ResourceIDFromContext(request.Context())
	if err != nil {
		return coreapi.NewCloudError(http.StatusBadRequest, coreapi.CloudErrorCodeInvalidRequestContent, "", "invalid resource identifier in request")
	}

	var body minimumVersionsRequest
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		return coreapi.NewCloudError(http.StatusBadRequest, coreapi.CloudErrorCodeInvalidRequestContent, "", "invalid JSON body: %v", err)
	}

	parsed, err := parseMinimumVersions(body.MinimumVersions)
	if err != nil {
		return err
	}

	existing, err := corecosmosstorage.GetOrCreateServiceProviderCluster(request.Context(), h.resourcesDBClient, resourceID)
	if err != nil {
		return fmt.Errorf("failed to get ServiceProviderCluster: %w", err)
	}

	replacement := existing.DeepCopy()
	replacement.Spec.ControlPlaneVersion.MinimumVersions = parsed

	_, err = h.resourcesDBClient.ServiceProviderClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Name).Replace(request.Context(), replacement, nil)
	if err != nil {
		return fmt.Errorf("failed to replace ServiceProviderCluster: %w", err)
	}

	resp := minimumVersionsResponse{MinimumVersions: versionsToStrings(parsed)}
	_, err = coreapi.WriteJSONResponse(writer, http.StatusOK, resp)
	return utils.TrackError(err)
}

// parseMinimumVersions validates and converts string versions to semver.Version.
// Returns nil (not an empty slice) when the input is nil or empty, which clears
// the field on the SPC.
func parseMinimumVersions(versions []string) ([]semver.Version, error) {
	if len(versions) == 0 {
		return nil, nil
	}
	parsed := make([]semver.Version, 0, len(versions))
	for _, s := range versions {
		v, err := semver.Parse(s)
		if err != nil {
			return nil, coreapi.NewCloudError(http.StatusBadRequest, coreapi.CloudErrorCodeInvalidRequestContent, "", "invalid semver %q: %v", s, err)
		}
		parsed = append(parsed, v)
	}
	return parsed, nil
}

func versionsToStrings(versions []semver.Version) []string {
	if versions == nil {
		return nil
	}
	out := make([]string, 0, len(versions))
	for _, v := range versions {
		out = append(out, v.String())
	}
	return out
}
