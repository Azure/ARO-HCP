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
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// HCPVersionPinHandler sets ServiceProviderClusterSpec PinnedVersion on the
// per-cluster ServiceProviderCluster. When an exact version is provided the
// ForcedClusterDesiredVersion controller holds the cluster at that z-stream
// until the fleet's bestExactVersion reaches the optional UntilExactVersion
// threshold, after which the pin auto-releases.
//
// Sending a request with a nil ExactVersion clears the pin and returns the
// cluster to normal rollout selection.
type HCPVersionPinHandler struct {
	resourcesDBClient corecosmosstorage.ResourcesDBClient
}

// NewHCPVersionPinHandler creates a new handler for the version pin endpoint.
func NewHCPVersionPinHandler(resourcesDBClient corecosmosstorage.ResourcesDBClient) *HCPVersionPinHandler {
	return &HCPVersionPinHandler{resourcesDBClient: resourcesDBClient}
}

// versionPinRequest is the wire shape for the version pin request body.
// ExactVersion is a pointer-string so callers can distinguish "set to value"
// (non-nil) from "clear the pin" (nil/absent).
type versionPinRequest struct {
	ExactVersion      *string `json:"exactVersion"`
	UntilExactVersion *string `json:"untilExactVersion"`
}

// versionPinResponse is the wire shape for the version pin response body.
type versionPinResponse struct {
	ExactVersion      *string `json:"exactVersion,omitempty"`
	UntilExactVersion *string `json:"untilExactVersion,omitempty"`
}

func (h *HCPVersionPinHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) error {
	resourceID, err := utils.ResourceIDFromContext(request.Context())
	if err != nil {
		return coreapi.NewCloudError(http.StatusBadRequest, coreapi.CloudErrorCodeInvalidRequestContent, "", "invalid resource identifier in request")
	}

	var body versionPinRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		return coreapi.NewCloudError(http.StatusBadRequest, coreapi.CloudErrorCodeInvalidRequestContent, "", "invalid JSON body: %v", err)
	}

	var exactVersion *semver.Version
	var untilExactVersion *semver.Version

	if body.ExactVersion != nil {
		if *body.ExactVersion == "" {
			return coreapi.NewCloudError(http.StatusBadRequest, coreapi.CloudErrorCodeInvalidRequestContent, "", "exactVersion must not be empty; omit the field to clear the pin")
		}
		parsed, err := semver.ParseTolerant(*body.ExactVersion)
		if err != nil {
			return coreapi.NewCloudError(http.StatusBadRequest, coreapi.CloudErrorCodeInvalidRequestContent, "", "exactVersion %q is not a valid semantic version: %v", *body.ExactVersion, err)
		}
		if parsed.Major == 0 && parsed.Minor == 0 && parsed.Patch == 0 {
			return coreapi.NewCloudError(http.StatusBadRequest, coreapi.CloudErrorCodeInvalidRequestContent, "", "exactVersion must be a non-zero version")
		}
		exactVersion = &parsed
	}

	if body.UntilExactVersion != nil {
		if exactVersion == nil {
			return coreapi.NewCloudError(http.StatusBadRequest, coreapi.CloudErrorCodeInvalidRequestContent, "", "untilExactVersion requires exactVersion to be set")
		}
		if *body.UntilExactVersion == "" {
			return coreapi.NewCloudError(http.StatusBadRequest, coreapi.CloudErrorCodeInvalidRequestContent, "", "untilExactVersion must not be empty; omit the field to pin without an auto-release threshold")
		}
		parsed, err := semver.ParseTolerant(*body.UntilExactVersion)
		if err != nil {
			return coreapi.NewCloudError(http.StatusBadRequest, coreapi.CloudErrorCodeInvalidRequestContent, "", "untilExactVersion %q is not a valid semantic version: %v", *body.UntilExactVersion, err)
		}
		if parsed.LT(*exactVersion) {
			return coreapi.NewCloudError(http.StatusBadRequest, coreapi.CloudErrorCodeInvalidRequestContent, "", "untilExactVersion %q must be greater than or equal to exactVersion %q", parsed.String(), exactVersion.String())
		}
		if parsed.Major != exactVersion.Major || parsed.Minor != exactVersion.Minor {
			return coreapi.NewCloudError(http.StatusBadRequest, coreapi.CloudErrorCodeInvalidRequestContent, "", "untilExactVersion %q must be in the same major.minor release line as exactVersion %q", parsed.String(), exactVersion.String())
		}
		untilExactVersion = &parsed
	}

	// Verify the cluster exists for both set and clear operations to prevent
	// creating orphan ServiceProviderCluster documents for nonexistent clusters.
	cluster, err := h.resourcesDBClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).Get(request.Context(), resourceID.Name)
	if err != nil {
		if cosmosstorageutils.IsNotFoundError(err) {
			return coreapi.NewCloudError(http.StatusNotFound, coreapi.CloudErrorCodeResourceNotFound, "", "HCP cluster %s not found", resourceID.String())
		}
		return fmt.Errorf("failed to get HCP cluster: %w", err)
	}

	// Validate that the pin is within the cluster's current release line to
	// prevent cross-minor downgrades. The cluster's Version.ID (e.g. "4.17")
	// is the authoritative source of the customer's requested release line.
	if exactVersion != nil {
		clusterVersion, err := semver.ParseTolerant(cluster.CustomerProperties.Version.ID)
		if err != nil {
			return fmt.Errorf("failed to parse cluster version %q: %w", cluster.CustomerProperties.Version.ID, err)
		}
		if exactVersion.Major != clusterVersion.Major || exactVersion.Minor != clusterVersion.Minor {
			return coreapi.NewCloudError(http.StatusBadRequest, coreapi.CloudErrorCodeInvalidRequestContent, "",
				"exactVersion %q must be in the cluster's release line %d.%d", exactVersion.String(), clusterVersion.Major, clusterVersion.Minor)
		}
		// Nightly channels have no ControlPlaneVersionRollout, so the forced
		// controller can never observe a bestExactVersion to auto-release the
		// pin. Reject untilExactVersion for nightly clusters.
		if untilExactVersion != nil && cluster.CustomerProperties.Version.ChannelGroup == metadataapi.ChannelGroupNightly {
			return coreapi.NewCloudError(http.StatusBadRequest, coreapi.CloudErrorCodeInvalidRequestContent, "",
				"untilExactVersion is not supported for nightly clusters; nightly pins must be cleared manually")
		}
	}

	existing, err := corecosmosstorage.GetOrCreateServiceProviderCluster(request.Context(), h.resourcesDBClient, resourceID)
	if err != nil {
		return fmt.Errorf("failed to get ServiceProviderCluster: %w", err)
	}

	replacement := existing.DeepCopy()
	replacement.Spec.PinnedVersion = coreapi.ServiceProviderClusterPinnedVersion{
		ExactVersion:      exactVersion,
		UntilExactVersion: untilExactVersion,
	}

	_, err = h.resourcesDBClient.ServiceProviderClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Name).Replace(request.Context(), replacement, nil)
	if err != nil {
		return fmt.Errorf("failed to replace ServiceProviderCluster: %w", err)
	}

	resp := versionPinResponse{}
	if exactVersion != nil {
		v := exactVersion.String()
		resp.ExactVersion = &v
	}
	if untilExactVersion != nil {
		v := untilExactVersion.String()
		resp.UntilExactVersion = &v
	}

	_, err = coreapihelpers.WriteJSONResponse(writer, http.StatusOK, resp)
	return utils.TrackError(err)
}
