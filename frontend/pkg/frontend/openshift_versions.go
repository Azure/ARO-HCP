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

package frontend

import (
	"cmp"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/blang/semver/v4"

	"k8s.io/apimachinery/pkg/util/sets"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func (f *Frontend) ArmResourceListVersion(writer http.ResponseWriter, request *http.Request) error {
	if !strings.EqualFold(request.PathValue(PathSegmentLocation), f.azureLocation) {
		coreapihelpers.WriteError(writer, http.StatusNotFound, coreapi.CloudErrorCodeNotFound, "", "OpenShift versions are not served for the requested location.")
		return nil
	}
	ctx := request.Context()
	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		return utils.TrackError(err)
	}
	subscription, err := SubscriptionFromContext(ctx)
	if err != nil {
		return utils.TrackError(err)
	}
	if subscription == nil {
		return utils.TrackError(errors.New("subscription is required for version discovery"))
	}

	catalog, err := f.resourcesDBClient.OpenShiftVersionCatalogs().Get(ctx, coreapi.OpenShiftVersionCatalogName)
	if cosmosstorageutils.IsNotFoundError(err) {
		catalog = nil
	} else if err != nil {
		return utils.TrackError(err)
	}

	allowedChannelGroups := metadataapi.AllowedChannelGroups
	if subscription.HasRegisteredFeature(metadataapi.FeatureExperimentalReleaseFeatures) {
		allowedChannelGroups = metadataapi.AllowedChannelGroupsWithExperimentalFlag
	}
	pagedResponse, err := listOpenShiftVersionsResponse(catalog, allowedChannelGroups, coreapi.MinimumPublicVersion,
		request.PathValue(PathSegmentSubscriptionID), request.PathValue(PathSegmentLocation), versionedInterface)
	if err != nil {
		var cloudError *coreapi.CloudError
		if errors.As(err, &cloudError) {
			coreapihelpers.WriteCloudError(writer, cloudError)
			return nil
		}
		return utils.TrackError(err)
	}
	_, err = coreapihelpers.WriteJSONResponse(writer, http.StatusOK, pagedResponse)
	return utils.TrackError(err)
}

// GetOpenshiftVersions implements the GET single resource API contract for ARM
// * 200 If the resource exists
// * 404 If the resource does not exist
// * 503 If the catalog or the visible version's availability is unresolved
func (f *Frontend) GetOpenshiftVersions(writer http.ResponseWriter, request *http.Request) error {
	if !strings.EqualFold(request.PathValue(PathSegmentLocation), f.azureLocation) {
		coreapihelpers.WriteError(writer, http.StatusNotFound, coreapi.CloudErrorCodeNotFound, "", "OpenShift versions are not served for the requested location.")
		return nil
	}
	ctx := request.Context()
	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		return utils.TrackError(err)
	}
	subscription, err := SubscriptionFromContext(ctx)
	if err != nil {
		return utils.TrackError(err)
	}
	if subscription == nil {
		return utils.TrackError(errors.New("subscription is required for version discovery"))
	}
	resourceID, err := utils.ResourceIDFromContext(ctx)
	if err != nil {
		return utils.TrackError(err)
	}

	catalog, err := f.resourcesDBClient.OpenShiftVersionCatalogs().Get(ctx, coreapi.OpenShiftVersionCatalogName)
	if cosmosstorageutils.IsNotFoundError(err) {
		catalog = nil
	} else if err != nil {
		return utils.TrackError(err)
	}

	allowedChannelGroups := metadataapi.AllowedChannelGroups
	if subscription.HasRegisteredFeature(metadataapi.FeatureExperimentalReleaseFeatures) {
		allowedChannelGroups = metadataapi.AllowedChannelGroupsWithExperimentalFlag
	}
	response, err := getOpenShiftVersionResponse(catalog, allowedChannelGroups, coreapi.MinimumPublicVersion,
		request.PathValue(PathSegmentSubscriptionID), request.PathValue(PathSegmentLocation), versionedInterface, resourceID.Name)
	if err != nil {
		var cloudError *coreapi.CloudError
		if errors.As(err, &cloudError) {
			coreapihelpers.WriteCloudError(writer, cloudError)
			return nil
		}
		return utils.TrackError(err)
	}
	_, err = coreapihelpers.WriteJSONResponse(writer, http.StatusOK, response)
	return utils.TrackError(err)
}

// listOpenShiftVersionsResponse projects visible, available catalog entries into an API-versioned page.
func listOpenShiftVersionsResponse(catalog *coreapi.OpenShiftVersionCatalog, allowedChannelGroups sets.Set[string], publicFloor,
	subscriptionID, location string, apiVersion coreapi.Version,
) (coreapi.PagedResponse, error) {
	if catalog == nil {
		return coreapi.PagedResponse{}, coreapi.NewCloudError(http.StatusServiceUnavailable, coreapi.CloudErrorCodeServiceUnavailable, "", "OpenShift version availability is not yet resolved. Please retry later.")
	}
	floor, err := semver.Parse(publicFloor + ".0")
	if err != nil {
		return coreapi.PagedResponse{}, err
	}
	// Filter visibility first so hidden channels cannot affect availability.
	eligible := 0
	available := make([]coreapi.VersionProfile, 0, len(catalog.Entries))
	for _, entry := range catalog.Entries {
		profile := entry.Version
		if !openShiftVersionVisible(profile, allowedChannelGroups, floor) {
			continue
		}
		eligible++
		if entry.Available {
			available = append(available, profile)
		}
	}
	if eligible > 0 && len(available) == 0 {
		return coreapi.PagedResponse{}, coreapi.NewCloudError(http.StatusServiceUnavailable, coreapi.CloudErrorCodeServiceUnavailable, "", "OpenShift version availability is not yet resolved. Please retry later.")
	}
	slices.SortFunc(available, func(a, b coreapi.VersionProfile) int {
		return cmp.Compare(openShiftVersionPublicName(a), openShiftVersionPublicName(b))
	})

	pagedResponse := coreapi.NewPagedResponse()
	for _, profile := range available {
		value, err := openShiftVersionResource(profile, subscriptionID, location, apiVersion)
		if err != nil {
			return coreapi.PagedResponse{}, err
		}
		pagedResponse.AddValue(value)
	}
	return pagedResponse, nil
}

func getOpenShiftVersionResponse(catalog *coreapi.OpenShiftVersionCatalog, allowedChannelGroups sets.Set[string], publicFloor,
	subscriptionID, location string, apiVersion coreapi.Version, requestedName string,
) (json.RawMessage, error) {
	if catalog == nil {
		return nil, coreapi.NewCloudError(http.StatusServiceUnavailable, coreapi.CloudErrorCodeServiceUnavailable, "", "OpenShift version availability is not yet resolved. Please retry later.")
	}
	floor, err := semver.Parse(publicFloor + ".0")
	if err != nil {
		return nil, err
	}
	for _, entry := range catalog.Entries {
		profile := entry.Version
		if !openShiftVersionVisible(profile, allowedChannelGroups, floor) || !strings.EqualFold(requestedName, openShiftVersionPublicName(profile)) {
			continue
		}
		if !entry.Available {
			return nil, coreapi.NewCloudError(http.StatusServiceUnavailable, coreapi.CloudErrorCodeServiceUnavailable, "", "OpenShift version availability is not yet resolved. Please retry later.")
		}
		return openShiftVersionResource(profile, subscriptionID, location, apiVersion)
	}
	return nil, coreapi.NewCloudError(http.StatusNotFound, coreapi.CloudErrorCodeNotFound, "", "The requested OpenShift version could not be found.")
}

func openShiftVersionVisible(profile coreapi.VersionProfile, allowedChannelGroups sets.Set[string], floor semver.Version) bool {
	version, err := semver.Parse(profile.ID + ".0")
	return allowedChannelGroups.Has(profile.ChannelGroup) && err == nil && !version.LT(floor)
}

func openShiftVersionPublicName(profile coreapi.VersionProfile) string {
	if profile.ChannelGroup == "stable" {
		return profile.ID
	}
	return profile.ID + "-" + profile.ChannelGroup
}

func openShiftVersionResource(profile coreapi.VersionProfile, subscriptionID, location string, apiVersion coreapi.Version) (json.RawMessage, error) {
	resourceID, err := azcorearm.ParseResourceID("/subscriptions/" + subscriptionID +
		"/providers/" + coreapi.ProviderNamespace + "/locations/" + location +
		"/" + coreapi.VersionResourceTypeName + "/" + openShiftVersionPublicName(profile))
	if err != nil {
		return nil, err
	}
	version := &coreapi.OpenShiftVersion{
		ProxyResource: coreapi.NewProxyResource(resourceID),
		Properties: coreapi.OpenShiftVersionProperties{
			ChannelGroup: profile.ChannelGroup,
			Enabled:      true,
		},
	}
	return coreapi.MarshalJSON(apiVersion.NewOpenShiftVersion(version))
}
