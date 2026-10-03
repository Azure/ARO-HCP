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
	"errors"
	"net/http"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/versionpolicy"
)

func (f *Frontend) serveOpenShiftVersions(writer http.ResponseWriter, request *http.Request, single bool) error {
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
	var requestedName string
	if single {
		resourceID, err := utils.ResourceIDFromContext(ctx)
		if err != nil {
			return utils.TrackError(err)
		}
		requestedName = resourceID.Name
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
	pagedResponse, err := openShiftVersionsResponse(catalog, allowedChannelGroups, versionpolicy.MinimumPublicVersion,
		request.PathValue(PathSegmentSubscriptionID), request.PathValue(PathSegmentLocation), versionedInterface, requestedName)
	if err != nil {
		var cloudError *coreapi.CloudError
		if errors.As(err, &cloudError) {
			coreapihelpers.WriteCloudError(writer, cloudError)
			return nil
		}
		return utils.TrackError(err)
	}
	if single {
		_, err = coreapihelpers.WriteJSONResponse(writer, http.StatusOK, pagedResponse.Value[0])
	} else {
		_, err = coreapihelpers.WriteJSONResponse(writer, http.StatusOK, pagedResponse)
	}
	return utils.TrackError(err)
}

// openShiftVersionsResponse projects visible, available catalog entries into an
// API-versioned page. A requestedName selects a single public version name.
func openShiftVersionsResponse(catalog *coreapi.OpenShiftVersionCatalog, allowedChannelGroups sets.Set[string], publicFloor,
	subscriptionID, location string, apiVersion coreapi.Version, requestedName string,
) (coreapi.PagedResponse, error) {
	if catalog == nil {
		return coreapi.PagedResponse{}, coreapi.NewCloudError(http.StatusServiceUnavailable, coreapi.CloudErrorCodeServiceUnavailable, "", "OpenShift version availability is not yet resolved. Please retry later.")
	}
	// Filter visibility first so only visible channels determine availability.
	// Hidden channels receive the same GET response as absent channels.
	eligible := 0
	available := make([]coreapi.VersionProfile, 0, len(catalog.Entries))
	for _, entry := range catalog.Entries {
		profile := entry.Version
		if !allowedChannelGroups.Has(profile.ChannelGroup) ||
			!versionpolicy.AtLeast(profile.ID, publicFloor) {
			continue
		}
		if requestedName != "" && requestedName != versionpolicy.PublicName(profile) {
			continue
		}
		eligible++
		if entry.Available {
			available = append(available, profile)
		}
	}
	if requestedName != "" && eligible == 0 {
		return coreapi.PagedResponse{}, coreapi.NewCloudError(http.StatusNotFound, coreapi.CloudErrorCodeNotFound, "", "The requested OpenShift version could not be found.")
	}
	if eligible > 0 && len(available) == 0 {
		return coreapi.PagedResponse{}, coreapi.NewCloudError(http.StatusServiceUnavailable, coreapi.CloudErrorCodeServiceUnavailable, "", "OpenShift version availability is not yet resolved. Please retry later.")
	}
	slices.SortFunc(available, func(a, b coreapi.VersionProfile) int {
		return cmp.Compare(versionpolicy.PublicName(a), versionpolicy.PublicName(b))
	})

	pagedResponse := coreapi.NewPagedResponse()
	for _, profile := range available {
		resourceID, err := azcorearm.ParseResourceID("/subscriptions/" + subscriptionID +
			"/providers/" + coreapi.ProviderNamespace + "/locations/" + location +
			"/" + coreapi.VersionResourceTypeName + "/" + versionpolicy.PublicName(profile))
		if err != nil {
			return coreapi.PagedResponse{}, err
		}
		version := &coreapi.OpenShiftVersion{
			ProxyResource: coreapi.NewProxyResource(resourceID),
			Properties: coreapi.OpenShiftVersionProperties{
				ChannelGroup: profile.ChannelGroup,
				Enabled:      true,
			},
		}
		value, err := coreapi.MarshalJSON(apiVersion.NewOpenShiftVersion(version))
		if err != nil {
			return coreapi.PagedResponse{}, err
		}
		pagedResponse.AddValue(value)
	}
	return pagedResponse, nil
}
