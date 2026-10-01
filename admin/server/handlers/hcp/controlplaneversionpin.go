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
	"io"
	"net/http"

	"github.com/blang/semver/v4"

	"k8s.io/utils/ptr"

	configv1 "github.com/openshift/api/config/v1"
	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// HCPVersionPinHandler sets ServiceProviderClusterSpec PinnedVersion on the
// per-cluster ServiceProviderCluster. When an exact version is provided the
// ForcedClusterDesiredVersion controller holds the cluster at that version.
// For graph-backed channels, the pin auto-releases when the fleet's
// bestExactVersion reaches the optional UntilExactVersion threshold.
//
// Sending a request with a nil ExactVersion clears the pin and returns the
// cluster to normal rollout selection. A pin at or above the latest observed
// control-plane version can hold or advance the cluster. Rolling back below
// that version requires the immediately previous successfully installed version
// from the mirrored HostedCluster's control-plane history.
type HCPVersionPinHandler struct {
	resourcesDBClient corecosmosstorage.ResourcesDBClient
}

// NewHCPVersionPinHandler creates a new handler for the version pin endpoint.
func NewHCPVersionPinHandler(resourcesDBClient corecosmosstorage.ResourcesDBClient) *HCPVersionPinHandler {
	return &HCPVersionPinHandler{resourcesDBClient: resourcesDBClient}
}

// controlPlaneVersionPinRequest is the wire shape for the version pin request body.
type controlPlaneVersionPinRequest struct {
	// ExactVersion pins the control plane to this version.
	// Nil (omitted or null) clears the pin; an empty string is invalid.
	ExactVersion *string `json:"exactVersion"`
	// UntilExactVersion auto-releases the pin when the fleet's best version
	// reaches this threshold. Nil leaves the pin in place until explicitly
	// cleared. A non-nil value requires ExactVersion, must be in the same
	// major.minor release line and >= ExactVersion. Nightly channels have no
	// fleet best version, so they cannot use this threshold. An empty string is
	// invalid.
	UntilExactVersion *string `json:"untilExactVersion"`
}

// controlPlaneVersionPinResponse is the wire shape for the version pin response body.
// Versions are returned in canonical semantic-version form. Nil fields are
// omitted from the JSON response rather than serialized as null.
type controlPlaneVersionPinResponse struct {
	// ExactVersion is the active control-plane pin. It is omitted after a clear.
	ExactVersion *string `json:"exactVersion,omitempty"`
	// UntilExactVersion is the auto-release threshold. It is omitted if the
	// pin has no threshold or has been cleared, and is present only with
	// ExactVersion.
	UntilExactVersion *string `json:"untilExactVersion,omitempty"`
}

func (h *HCPVersionPinHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) error {
	resourceID, err := utils.ResourceIDFromContext(request.Context())
	if err != nil {
		return coreapi.NewCloudError(http.StatusBadRequest, coreapi.CloudErrorCodeInvalidRequestContent, "", "invalid resource identifier in request")
	}

	// Decode through a pointer so top-level null remains nil rather than
	// becoming an empty struct and silently clearing an existing pin.
	var body *controlPlaneVersionPinRequest
	decoder := json.NewDecoder(request.Body)
	// An omitted exactVersion clears the pin. Reject unknown fields so a typo in
	// exactVersion cannot silently clear it, and a typo in untilExactVersion
	// cannot leave a new pin without its intended auto-release threshold.
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		return coreapi.NewCloudError(http.StatusBadRequest, coreapi.CloudErrorCodeInvalidRequestContent, "", "invalid JSON body: %v", err)
	}
	if body == nil {
		return coreapi.NewCloudError(http.StatusBadRequest, coreapi.CloudErrorCodeInvalidRequestContent, "", "invalid JSON body: expected an object")
	}
	// A second decode must reach EOF: the request may have trailing whitespace,
	// but it must not contain another JSON value or any other trailing data.
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		return coreapi.NewCloudError(http.StatusBadRequest, coreapi.CloudErrorCodeInvalidRequestContent, "", "invalid JSON body: expected a single object with no trailing data")
	}

	var exactVersion *semver.Version
	var untilExactVersion *semver.Version

	if body.ExactVersion != nil {
		if *body.ExactVersion == "" {
			return coreapi.NewCloudError(http.StatusBadRequest, coreapi.CloudErrorCodeInvalidRequestContent, "", "exactVersion must not be empty; omit the field to clear the pin")
		}
		parsed, err := semver.Parse(*body.ExactVersion)
		if err != nil {
			return coreapi.NewCloudError(http.StatusBadRequest, coreapi.CloudErrorCodeInvalidRequestContent, "", "exactVersion %q is not a valid semantic version: %v", *body.ExactVersion, err)
		}
		if len(parsed.Build) > 0 {
			return coreapi.NewCloudError(http.StatusBadRequest, coreapi.CloudErrorCodeInvalidRequestContent, "", "exactVersion must not contain build metadata")
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
		parsed, err := semver.Parse(*body.UntilExactVersion)
		if err != nil {
			return coreapi.NewCloudError(http.StatusBadRequest, coreapi.CloudErrorCodeInvalidRequestContent, "", "untilExactVersion %q is not a valid semantic version: %v", *body.UntilExactVersion, err)
		}
		if len(parsed.Build) > 0 {
			return coreapi.NewCloudError(http.StatusBadRequest, coreapi.CloudErrorCodeInvalidRequestContent, "", "untilExactVersion must not contain build metadata")
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
	if err != nil && cosmosstorageutils.IsNotFoundError(err) {
		return coreapi.NewCloudError(http.StatusNotFound, coreapi.CloudErrorCodeResourceNotFound, "", "HCP cluster %s not found", resourceID.String())
	}
	if err != nil {
		return fmt.Errorf("failed to get HCP cluster: %w", err)
	}
	if cluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return coreapi.NewCloudError(http.StatusConflict, coreapi.CloudErrorCodeConflict, "", "HCP cluster %s is being deleted", resourceID.String())
	}

	// Cluster version validation owns the supported minimum. Here, validate the
	// pin against its requested release line and observed installed history
	// rather than duplicating that minimum. The cluster's Version.ID (e.g.
	// "4.20") is the authoritative source of the requested release line.
	if exactVersion != nil {
		// Nightly channels have no rollout best version to trigger auto-release.
		// A nightly pin without a threshold can still be cleared explicitly.
		if untilExactVersion != nil && cluster.CustomerProperties.Version.ChannelGroup == metadataapi.ChannelGroupNightly {
			return coreapi.NewCloudError(http.StatusBadRequest, coreapi.CloudErrorCodeInvalidRequestContent, "",
				"untilExactVersion is not supported for nightly clusters; nightly pins must be cleared manually")
		}
		clusterVersion, err := semver.ParseTolerant(cluster.CustomerProperties.Version.ID)
		if err != nil {
			return fmt.Errorf("failed to parse cluster version %q: %w", cluster.CustomerProperties.Version.ID, err)
		}
		if exactVersion.Major != clusterVersion.Major || exactVersion.Minor != clusterVersion.Minor {
			return coreapi.NewCloudError(http.StatusBadRequest, coreapi.CloudErrorCodeInvalidRequestContent, "",
				"exactVersion %q must be in the cluster's release line %d.%d", exactVersion.String(), clusterVersion.Major, clusterVersion.Minor)
		}
	}

	existingServiceProviderCluster, err := h.resourcesDBClient.ServiceProviderClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Name).Get(request.Context(), coreapi.ServiceProviderClusterResourceName)
	if err != nil && cosmosstorageutils.IsNotFoundError(err) {
		return coreapi.NewCloudError(http.StatusConflict, coreapi.CloudErrorCodeConflict, "", "ServiceProviderCluster for HCP cluster %s is not available", resourceID.String())
	}
	if err != nil {
		return fmt.Errorf("failed to get ServiceProviderCluster: %w", err)
	}
	if exactVersion != nil {
		if err := validateVersionPinTarget(existingServiceProviderCluster, *exactVersion); err != nil {
			return err
		}
	}

	replacement := existingServiceProviderCluster.DeepCopy()
	replacement.Spec.PinnedVersion = coreapi.ServiceProviderClusterPinnedVersion{
		ExactVersion:      exactVersion,
		UntilExactVersion: untilExactVersion,
	}

	_, err = h.resourcesDBClient.ServiceProviderClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Name).Replace(request.Context(), replacement, nil)
	if err != nil && cosmosstorageutils.IsPreconditionFailedError(err) {
		return coreapi.NewCloudError(http.StatusConflict, coreapi.CloudErrorCodeConflict, "", "ETag conflict, retry the operation")
	}
	if err != nil {
		return fmt.Errorf("failed to replace ServiceProviderCluster: %w", err)
	}

	resp := controlPlaneVersionPinResponse{}
	if exactVersion != nil {
		resp.ExactVersion = ptr.To(exactVersion.String())
	}
	if untilExactVersion != nil {
		resp.UntilExactVersion = ptr.To(untilExactVersion.String())
	}

	_, err = coreapihelpers.WriteJSONResponse(writer, http.StatusOK, resp)
	return utils.TrackError(err)
}

// validateVersionPinTarget allows holding or advancing a version without
// requiring prior installation. A rollback uses the full mirrored control-plane
// history: the distilled ActiveVersions stops at the first completed entry and
// cannot identify the previous installed version after a successful upgrade.
func validateVersionPinTarget(cluster *coreapi.ServiceProviderCluster, target semver.Version) error {
	// The backend mirrors HostedCluster status asynchronously. Without an
	// observed version, the pin can still hold the requested target while the
	// controller and HostedCluster catch up.
	if cluster.Status.ActualHostedCluster == nil {
		return nil
	}
	history := cluster.Status.ActualHostedCluster.Status.ControlPlaneVersion.History
	if len(history) == 0 {
		return nil
	}
	// The newest history entry can be Partial during an upgrade. Its version is
	// the latest attempted control-plane version, not necessarily an installed one.
	latestHistoryVersion, err := semver.Parse(history[0].Version)
	if err != nil {
		return coreapi.NewCloudError(http.StatusBadRequest, coreapi.CloudErrorCodeInvalidRequestContent, "", "cannot validate version pin: latest control-plane history version %q is invalid", history[0].Version)
	}
	if len(latestHistoryVersion.Build) > 0 {
		return coreapi.NewCloudError(http.StatusBadRequest, coreapi.CloudErrorCodeInvalidRequestContent, "", "cannot validate version pin: latest control-plane history version %q contains build metadata", history[0].Version)
	}
	if !target.LT(latestHistoryVersion) {
		return nil
	}
	previous, err := findPreviousSuccessfullyInstalledControlPlaneVersion(history[1:], latestHistoryVersion)
	if err != nil {
		return err
	}
	// A history whose previous version is newer represents a rollback that has
	// already happened. Only the immediately previous lower completed version
	// can authorize another rollback.
	if !previous.LT(latestHistoryVersion) || !target.EQ(previous) {
		return coreapi.NewCloudError(http.StatusBadRequest, coreapi.CloudErrorCodeInvalidRequestContent, "", "exactVersion %q must be the immediately previous successfully installed control-plane version %q and older than the latest control-plane history version %q", target.String(), previous.String(), latestHistoryVersion.String())
	}
	return nil
}

// findPreviousSuccessfullyInstalledControlPlaneVersion scans history after the
// latest entry. Partial updates are not installed rollback targets, and repeated
// entries for the latest version do not establish a distinct previous version.
func findPreviousSuccessfullyInstalledControlPlaneVersion(history []hsv1beta1.ControlPlaneUpdateHistory, latest semver.Version) (semver.Version, error) {
	// History is newest first. Completed marks a successfully installed version.
	for _, entry := range history {
		if entry.State != configv1.CompletedUpdate {
			continue
		}
		previous, err := semver.Parse(entry.Version)
		if err != nil {
			return semver.Version{}, coreapi.NewCloudError(http.StatusBadRequest, coreapi.CloudErrorCodeInvalidRequestContent, "", "cannot validate version pin: previous control-plane history version %q is invalid", entry.Version)
		}
		if len(previous.Build) > 0 {
			return semver.Version{}, coreapi.NewCloudError(http.StatusBadRequest, coreapi.CloudErrorCodeInvalidRequestContent, "", "cannot validate version pin: previous control-plane history version %q contains build metadata", entry.Version)
		}
		if previous.EQ(latest) {
			continue
		}
		return previous, nil
	}
	return semver.Version{}, coreapi.NewCloudError(http.StatusBadRequest, coreapi.CloudErrorCodeInvalidRequestContent, "", "cannot validate version pin: no previous successfully installed control-plane version is available")
}
