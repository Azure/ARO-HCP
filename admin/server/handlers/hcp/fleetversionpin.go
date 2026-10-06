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
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/blang/semver/v4"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// FleetVersionPinHandler updates pins in the regional Resources container.
// It validates the entire selection before starting independent ETag writes.
type FleetVersionPinHandler struct {
	resourcesDBClient corecosmosstorage.ResourcesDBClient
}

// NewFleetVersionPinHandler creates the regional fleet version pin handler.
func NewFleetVersionPinHandler(resourcesDBClient corecosmosstorage.ResourcesDBClient) *FleetVersionPinHandler {
	return &FleetVersionPinHandler{resourcesDBClient: resourcesDBClient}
}

// fleetVersionPinFailure identifies a cluster whose provider document could not be updated.
type fleetVersionPinFailure struct {
	// ResourceID is the failed HCP cluster's ARM resource ID.
	ResourceID string `json:"resourceId"`
	// Code is the CloudError code for the write failure.
	Code string `json:"code"`
	// Message explains the write failure and possible retry.
	Message string `json:"message"`
}

// Counts refer to pin document writes, not completion of control-plane rollback.
type fleetVersionPinResponse struct {
	// Channel is the requested regional y-stream channel.
	Channel string `json:"channel"`
	// MatchedCount is the number of provider documents in the selected channel.
	MatchedCount int `json:"matchedCount"`
	// AffectedCount counts successful provider document writes.
	AffectedCount int `json:"affectedCount"`
	// UnchangedCount counts already-identical pins that were not rewritten.
	UnchangedCount int `json:"unchangedCount"`
	// FailedCount counts provider document writes that failed.
	FailedCount int `json:"failedCount"`
	// Failures identifies each failed cluster and the associated write error.
	Failures []fleetVersionPinFailure `json:"failures,omitempty"`
}

func (h *FleetVersionPinHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	logger := utils.LoggerFromContext(ctx)
	group, minor, err := parseFleetVersionPinChannel(r.PathValue("channel"))
	if err != nil {
		return err
	}
	pin, err := decodeVersionPinRequest(r.Body)
	if err != nil {
		return err
	}
	// Validate the request against the channel even when it has no members.
	channelVersion := coreapi.VersionProfile{ID: minor, ChannelGroup: group}
	if err := validateClusterVersionPin(channelVersion, pin); err != nil {
		return err
	}

	listStart := time.Now()
	logger.Info("Listing fleet clusters", "channel", r.PathValue("channel"))
	clusterIter, err := h.resourcesDBClient.ResourcesGlobalListers().Clusters().List(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to list fleet clusters: %w", err)
	}
	// Keys are lowercased ARM resource IDs so they match provider parent IDs.
	clustersByResourceID := map[string]*coreapi.Cluster{}
	clusterCount := 0
	for _, cluster := range clusterIter.Items(ctx) {
		clusterCount++
		if cluster.ID != nil && cluster.ServiceProviderProperties.DeletionTimestamp == nil && cluster.CustomerProperties.Version.ChannelGroup == group {
			clustersByResourceID[strings.ToLower(cluster.ID.String())] = cluster
		}
	}
	if err := clusterIter.GetError(); err != nil {
		return fmt.Errorf("failed to iterate fleet clusters: %w", err)
	}
	logger.Info("Listed fleet clusters", "listedCount", clusterCount, "eligibleCount", len(clustersByResourceID), "duration", time.Since(listStart))
	listStart = time.Now()
	logger.Info("Listing fleet provider clusters", "channel", r.PathValue("channel"))
	providerIter, err := h.resourcesDBClient.ResourcesGlobalListers().ServiceProviderClusters().List(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to list fleet provider clusters: %w", err)
	}
	var selected []*coreapi.ServiceProviderCluster
	providerCount := 0
	for _, provider := range providerIter.Items(ctx) {
		providerCount++
		if provider.ResourceID == nil || provider.ResourceID.Parent == nil {
			return fmt.Errorf("listed ServiceProviderCluster has no resource ID or parent")
		}
		if clustersByResourceID[strings.ToLower(provider.ResourceID.Parent.String())] != nil && fleetVersionPinMinor(provider) == minor {
			selected = append(selected, provider)
		}
	}
	if err := providerIter.GetError(); err != nil {
		return fmt.Errorf("failed to iterate fleet provider clusters: %w", err)
	}
	logger.Info("Listed fleet provider clusters", "listedCount", providerCount, "selectedCount", len(selected), "duration", time.Since(listStart))
	// Sort ascending by ARM resource ID so failures and writes have stable order.
	slices.SortFunc(selected, func(a, b *coreapi.ServiceProviderCluster) int {
		return strings.Compare(a.ResourceID.String(), b.ResourceID.String())
	})
	response := fleetVersionPinResponse{Channel: r.PathValue("channel"), MatchedCount: len(selected)}
	var pending []*coreapi.ServiceProviderCluster
	validationError := coreapi.NewCloudError(http.StatusBadRequest, coreapi.CloudErrorCodeInvalidRequestContent, "", "fleet version pin validation failed; no clusters were modified")
	// Prevalidate every changed pin before writing any provider document. Collect
	// all per-cluster failures so the caller can inspect the whole selection.
	for _, provider := range selected {
		if sameVersionPin(provider.Spec.PinnedVersion, *pin) {
			// An identical persisted pin is not a new rollback. It remains a
			// no-op even if its rollback has completed since a partial request.
			response.UnchangedCount++
			continue
		}
		clusterID := provider.ResourceID.Parent
		cluster := clustersByResourceID[strings.ToLower(clusterID.String())]
		validationErr := validateClusterVersionPin(cluster.CustomerProperties.Version, pin)
		if validationErr == nil && pin.ExactVersion != nil {
			validationErr = validateVersionPinTarget(provider, *pin.ExactVersion)
		}
		if validationErr != nil {
			var cloudErr *coreapi.CloudError
			if !errors.As(validationErr, &cloudErr) {
				return fmt.Errorf("failed to validate cluster %s: %w", clusterID, validationErr)
			}
			detail := *cloudErr.CloudErrorBody
			detail.Target = clusterID.String()
			validationError.Details = append(validationError.Details, detail)
			continue
		}
		pending = append(pending, provider)
	}
	if len(validationError.Details) > 0 {
		return validationError
	}

	status := http.StatusOK
	// Write validated pins independently; ETag conflicts can still make this a
	// partial success, so report each failure and continue with other clusters.
	for _, provider := range pending {
		clusterID := provider.ResourceID.Parent
		replacement := provider.DeepCopy()
		replacement.Spec.PinnedVersion = *pin.DeepCopy()
		if _, err := h.resourcesDBClient.ServiceProviderClusters(clusterID.SubscriptionID, clusterID.ResourceGroupName, clusterID.Name).Replace(ctx, replacement, nil); err != nil {
			failure := fleetVersionPinFailure{ResourceID: clusterID.String(), Code: coreapi.CloudErrorCodeInternalServerError, Message: "failed to update version pin; inspect cluster state before retrying"}
			if cosmosstorageutils.IsPreconditionFailedError(err) {
				failure.Code = coreapi.CloudErrorCodeConflict
				failure.Message = "ETag conflict, retry the operation"
				if status == http.StatusOK {
					status = http.StatusConflict
				}
			} else {
				status = http.StatusInternalServerError
			}
			logger.Error(err, "Failed to update fleet version pin", "resourceID", clusterID.String())
			response.Failures = append(response.Failures, failure)
			continue
		}
		response.AffectedCount++
	}
	response.FailedCount = len(response.Failures)
	_, err = coreapihelpers.WriteJSONResponse(w, status, response)
	return utils.TrackError(err)
}

// parseFleetVersionPinChannel parses a channel name such as "stable-4.22".
// It returns the channel group and major.minor release line, or an error.
func parseFleetVersionPinChannel(channel string) (string, string, error) {
	invalidChannel := coreapi.NewCloudError(http.StatusBadRequest, coreapi.CloudErrorCodeInvalidRequestContent, "channel", "invalid y-stream channel %q; expected channel group and major.minor, e.g. stable-4.22", channel)
	parts := strings.SplitN(channel, "-", 2)
	if len(parts) != 2 {
		return "", "", invalidChannel
	}
	if !metadataapi.AllowedChannelGroupsWithExperimentalFlag.Has(parts[0]) {
		return "", "", invalidChannel
	}
	version, err := semver.Parse(parts[1] + ".0")
	if err != nil {
		return "", "", invalidChannel
	}
	if parts[1] != fmt.Sprintf("%d.%d", version.Major, version.Minor) {
		return "", "", invalidChannel
	}
	return parts[0], parts[1], nil
}

// fleetVersionPinMinor returns the effective major.minor release line used for
// rollout membership: desired version first, otherwise the oldest completed
// active version. It returns an empty string when neither is known. ActiveVersions
// is only used for membership, never rollback validation.
func fleetVersionPinMinor(provider *coreapi.ServiceProviderCluster) string {
	version := provider.Spec.ControlPlaneVersion.DesiredVersion
	if version == nil {
		for _, active := range slices.Backward(provider.Status.ControlPlaneVersion.ActiveVersions) {
			// The mirror writes parsed, non-nil versions. Skip an incomplete legacy
			// entry rather than using it to determine channel membership.
			if active.State == configv1.CompletedUpdate && active.Version != nil {
				version = active.Version
				break
			}
		}
	}
	if version == nil {
		return ""
	}
	return fmt.Sprintf("%d.%d", version.Major, version.Minor)
}

func sameVersionPin(a, b coreapi.ServiceProviderClusterPinnedVersion) bool {
	return samePinVersion(a.ExactVersion, b.ExactVersion) && samePinVersion(a.UntilExactVersion, b.UntilExactVersion)
}

func samePinVersion(a, b *semver.Version) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.String() == b.String()
}
