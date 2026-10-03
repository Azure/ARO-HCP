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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/azureapi/v20240610preview"
	"github.com/Azure/ARO-HCP/internal/azureapi/v20251223preview"
	"github.com/Azure/ARO-HCP/internal/azureapi/v20260630preview"
	"github.com/Azure/ARO-HCP/internal/azureapi/v20260901preview"
	"github.com/Azure/ARO-HCP/internal/azureapi/v20261001preview"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// versionCatalogDB records singleton catalog reads by the HTTP handlers.
type versionCatalogDB struct {
	corecosmosstorage.ResourcesDBClient
	cosmosstorageutils.ResourceCRUD[coreapi.OpenShiftVersionCatalog, *coreapi.OpenShiftVersionCatalog]
	snapshot *coreapi.OpenShiftVersionCatalog
	err      error
	reads    []string
}

func (db *versionCatalogDB) OpenShiftVersionCatalogs() cosmosstorageutils.ResourceCRUD[coreapi.OpenShiftVersionCatalog, *coreapi.OpenShiftVersionCatalog] {
	return db
}

func (db *versionCatalogDB) Get(_ context.Context, name string) (*coreapi.OpenShiftVersionCatalog, error) {
	db.reads = append(db.reads, name)
	return db.snapshot, db.err
}

func openShiftVersionAPIRegistry(t *testing.T) coreapi.APIRegistry {
	t.Helper()
	registry := coreapi.NewAPIRegistry()
	for _, register := range []func(coreapi.APIRegistry) error{
		v20240610preview.RegisterVersion,
		v20251223preview.RegisterVersion,
		v20260630preview.RegisterVersion,
		v20260901preview.RegisterVersion,
		v20261001preview.RegisterVersion,
	} {
		require.NoError(t, register(registry))
	}
	return registry
}

func TestOpenShiftVersionsResponse(t *testing.T) {
	entry := func(id, group string, available bool) coreapi.OpenShiftVersionCatalogEntry {
		return coreapi.OpenShiftVersionCatalogEntry{Version: coreapi.VersionProfile{ID: id, ChannelGroup: group}, Available: available}
	}
	mixed := []coreapi.OpenShiftVersionCatalogEntry{
		entry("4.22", "fast", true),
		entry("4.21", "candidate", true),
		entry("4.19", "stable", true),
		entry("4.21", "nightly", true),
		entry("4.21", "fast", true),
		entry("4.21", "stable", true),
		entry("4.22", "stable", false),
		entry("5.0", "stable", true),
		entry("4.18", "fast", false),
		entry("4.21", "unknown", true),
	}
	cases := []struct {
		name         string
		entries      []coreapi.OpenShiftVersionCatalogEntry
		experimental bool
		get          string
		missing      bool
		status       int
		names        []string
		location     string
		publicFloor  string
	}{
		{name: "list sorted normal", entries: mixed, status: 200, names: []string{"4.21", "4.21-fast", "4.22-fast", "5.0"}},
		{name: "list sorted experimental", entries: mixed, experimental: true, status: 200, names: []string{"4.21", "4.21-candidate", "4.21-fast", "4.21-nightly", "4.22-fast", "5.0"}},
		{name: "list empty snapshot", status: 200, names: []string{}},
		{name: "list hidden unresolved", entries: []coreapi.OpenShiftVersionCatalogEntry{entry("4.21", "candidate", false), entry("4.21", "nightly", false), entry("4.19", "stable", false)}, status: 200, names: []string{}},
		{name: "list hidden available does not mask unresolved", entries: []coreapi.OpenShiftVersionCatalogEntry{entry("4.21", "candidate", true), entry("4.21", "nightly", true), entry("4.19", "stable", true), entry("4.21", "fast", false)}, status: 503},
		{name: "list candidate unresolved experimental", entries: []coreapi.OpenShiftVersionCatalogEntry{entry("4.21", "candidate", false)}, experimental: true, status: 503},
		{name: "list nightly unresolved experimental", entries: []coreapi.OpenShiftVersionCatalogEntry{entry("4.21", "nightly", false)}, experimental: true, status: 503},
		{name: "list nightly resolved experimental", entries: []coreapi.OpenShiftVersionCatalogEntry{entry("4.21", "nightly", true)}, experimental: true, status: 200, names: []string{"4.21-nightly"}},
		{name: "list unknown unresolved experimental", entries: []coreapi.OpenShiftVersionCatalogEntry{entry("4.21", "unknown", false)}, experimental: true, status: 200, names: []string{}},
		{name: "list floor inclusive", entries: []coreapi.OpenShiftVersionCatalogEntry{entry("4.20", "stable", true)}, status: 200, names: []string{"4.20"}},
		{name: "list raised floor", entries: mixed, publicFloor: "4.22", status: 200, names: []string{"4.22-fast", "5.0"}},
		{name: "list missing snapshot", missing: true, status: 503},
		{name: "get missing snapshot", get: "4.21", missing: true, status: 503},
		{name: "get stable", entries: mixed, get: "4.21", status: 200, names: []string{"4.21"}},
		{name: "get fast", entries: mixed, get: "4.21-fast", status: 200, names: []string{"4.21-fast"}},
		{name: "get candidate experimental", entries: mixed, experimental: true, get: "4.21-candidate", status: 200, names: []string{"4.21-candidate"}},
		{name: "get candidate hidden", entries: mixed, get: "4.21-candidate", status: 404},
		{name: "get unresolved candidate hidden", entries: []coreapi.OpenShiftVersionCatalogEntry{entry("4.21", "candidate", false)}, get: "4.21-candidate", status: 404},
		{name: "get unresolved candidate experimental", entries: []coreapi.OpenShiftVersionCatalogEntry{entry("4.21", "candidate", false)}, experimental: true, get: "4.21-candidate", status: 503},
		{name: "get nightly experimental", entries: mixed, experimental: true, get: "4.21-nightly", status: 200, names: []string{"4.21-nightly"}},
		{name: "get nightly hidden", entries: mixed, get: "4.21-nightly", status: 404},
		{name: "get unresolved nightly hidden", entries: []coreapi.OpenShiftVersionCatalogEntry{entry("4.21", "nightly", false)}, get: "4.21-nightly", status: 404},
		{name: "get unresolved nightly experimental", entries: []coreapi.OpenShiftVersionCatalogEntry{entry("4.21", "nightly", false)}, experimental: true, get: "4.21-nightly", status: 503},
		{name: "get unknown hidden", entries: mixed, get: "4.21-unknown", status: 404},
		{name: "get unknown hidden experimental", entries: mixed, experimental: true, get: "4.21-unknown", status: 404},
		{name: "get below floor", entries: mixed, get: "4.19", status: 404},
		{name: "get below floor unresolved", entries: mixed, get: "4.18-fast", status: 404},
		{name: "get unresolved", entries: mixed, get: "4.22", status: 503},
		{name: "get absent", entries: mixed, get: "4.23", status: 404},
		{name: "get empty snapshot", get: "4.21", status: 404},
		{name: "get stable suffix is not alias", entries: mixed, get: "4.21-stable", status: 404},
		{name: "get zstream is not alias", entries: mixed, get: "4.21.0", status: 404},
		{name: "get fast zstream is not alias", entries: mixed, get: "4.21.1-fast", status: 404},
		{name: "get channel is not alias", entries: mixed, get: "fast-4.21", status: 404},
		{name: "get prefix is not alias", entries: mixed, get: "openshift-v4.21", status: 404},
		{name: "get case is not alias", entries: mixed, get: "4.21-FAST", status: 404},
		{name: "list preserves location case", entries: mixed, location: "UKSouth", status: 200, names: []string{"4.21", "4.21-fast", "4.22-fast", "5.0"}},
		{name: "get preserves location case", entries: mixed, get: "4.21", location: "UKSouth", status: 200, names: []string{"4.21"}},
	}
	registry := openShiftVersionAPIRegistry(t)
	for _, apiVersion := range slices.Sorted(maps.Keys(registry.ListVersions())) {
		t.Run(apiVersion, func(t *testing.T) {
			version, ok := registry.Lookup(apiVersion)
			require.True(t, ok)
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					catalog := &coreapi.OpenShiftVersionCatalog{Entries: tc.entries}
					if tc.missing {
						catalog = nil
					}
					original := catalog.DeepCopy()
					allowedGroups := metadataapi.AllowedChannelGroups
					if tc.experimental {
						allowedGroups = metadataapi.AllowedChannelGroupsWithExperimentalFlag
					}
					floor := tc.publicFloor
					if floor == "" {
						floor = "4.20"
					}
					const subscriptionID = "11111111-2222-4333-8444-555555555555"
					location := tc.location
					if location == "" {
						location = "uksouth"
					}
					base := "/subscriptions/" + subscriptionID + "/providers/Microsoft.RedHatOpenShift/locations/" + location + "/hcpOpenShiftVersions"
					got, err := openShiftVersionsResponse(catalog, allowedGroups, floor, subscriptionID, location, version, tc.get)
					require.Empty(t, cmp.Diff(original, catalog), "catalog remains unchanged")
					if tc.status != http.StatusOK {
						var cloudError *coreapi.CloudError
						require.ErrorAs(t, err, &cloudError)
						want := coreapi.NewCloudError(http.StatusServiceUnavailable, coreapi.CloudErrorCodeServiceUnavailable, "", "OpenShift version availability is not yet resolved. Please retry later.")
						if tc.status == http.StatusNotFound {
							want = coreapi.NewCloudError(http.StatusNotFound, coreapi.CloudErrorCodeNotFound, "", "The requested OpenShift version could not be found.")
						}
						require.Empty(t, cmp.Diff(want, cloudError))
						require.Empty(t, cmp.Diff(coreapi.PagedResponse{}, got))
						return
					}
					require.NoError(t, err)
					want := coreapi.NewPagedResponse()
					for _, name := range tc.names {
						group := "stable"
						for _, entry := range tc.entries {
							if name == entry.Version.ID+"-"+entry.Version.ChannelGroup {
								group = entry.Version.ChannelGroup
							}
						}
						value := json.RawMessage(fmt.Sprintf(`{"id":%q,"name":%q,"type":"Microsoft.RedHatOpenShift/locations/hcpOpenShiftVersions","properties":{"channelGroup":%q,"enabled":true}}`, base+"/"+name, name, group))
						want.AddValue(value)
					}
					// Compare JSON structurally while preserving empty arrays and omitted fields.
					jsonValue := func(page coreapi.PagedResponse) any {
						data, err := json.Marshal(page)
						require.NoError(t, err)
						var value any
						require.NoError(t, json.Unmarshal(data, &value))
						return value
					}
					require.Empty(t, cmp.Diff(jsonValue(want), jsonValue(got)))
				})
			}
		})
	}
}

func TestOpenShiftVersionHandlers(t *testing.T) {
	registry := openShiftVersionAPIRegistry(t)
	version, ok := registry.Lookup(string(metadataapi.APIVersionV20261001Preview))
	require.True(t, ok)
	storageFailure := errors.New("snapshot read failed")
	for _, single := range []bool{false, true} {
		for _, tc := range []struct {
			name         string
			available    bool
			experimental bool
			readError    error
			location     string
			subscription string
			status       int
		}{
			{name: "available experimental", available: true, experimental: true, location: "UKSouth", status: http.StatusOK},
			{name: "hidden", available: true, status: http.StatusOK},
			{name: "unresolved", experimental: true, status: http.StatusServiceUnavailable},
			{name: "missing catalog", readError: cosmosstorageutils.NewNotFoundError(), status: http.StatusServiceUnavailable},
			{name: "storage failure", readError: storageFailure},
			{name: "wrong location", location: "eastus", status: http.StatusNotFound},
			{name: "missing subscription", subscription: "missing"},
			{name: "nil subscription", subscription: "nil"},
		} {
			t.Run(fmt.Sprintf("single=%t/%s", single, tc.name), func(t *testing.T) {
				store := &versionCatalogDB{
					snapshot: &coreapi.OpenShiftVersionCatalog{Entries: []coreapi.OpenShiftVersionCatalogEntry{{
						Version: coreapi.VersionProfile{ID: "4.21", ChannelGroup: "nightly"}, Available: tc.available,
					}}},
					err: tc.readError,
				}
				frontend := &Frontend{azureLocation: "uksouth", resourcesDBClient: store}
				subscription := &coreapi.Subscription{Properties: &coreapi.SubscriptionProperties{}}
				if tc.experimental {
					subscription.Properties.RegisteredFeatures = &[]coreapi.Feature{{Name: ptr.To(metadataapi.FeatureExperimentalReleaseFeatures), State: ptr.To("Registered")}}
				}
				ctx := ContextWithVersion(t.Context(), version)
				if tc.subscription == "nil" {
					subscription = nil
				}
				if tc.subscription != "missing" {
					ctx = ContextWithSubscription(ctx, subscription)
				}
				const subscriptionID = "11111111-2222-4333-8444-555555555555"
				location := tc.location
				if location == "" {
					location = "uksouth"
				}
				url := "/subscriptions/" + subscriptionID + "/providers/Microsoft.RedHatOpenShift/locations/" + location + "/hcpOpenShiftVersions"
				if single {
					url += "/4.21-nightly"
					id, err := azcorearm.ParseResourceID(url)
					require.NoError(t, err)
					ctx = utils.ContextWithResourceID(ctx, id)
				}
				request := httptest.NewRequestWithContext(ctx, http.MethodGet, url, nil)
				request.SetPathValue(PathSegmentSubscriptionID, subscriptionID)
				request.SetPathValue(PathSegmentLocation, location)
				response := httptest.NewRecorder()
				var err error
				if single {
					err = frontend.GetOpenshiftVersions(response, request)
				} else {
					err = frontend.ArmResourceListVersion(response, request)
				}
				wantReads := []string{coreapi.OpenShiftVersionCatalogName}
				if tc.location == "eastus" || tc.subscription != "" {
					wantReads = nil
				}
				require.Empty(t, cmp.Diff(wantReads, store.reads))
				if tc.readError == storageFailure || tc.subscription != "" {
					require.Error(t, err)
					if tc.readError == storageFailure {
						require.ErrorIs(t, err, storageFailure)
					}
					require.Empty(t, response.Body.String())
					return
				}
				require.NoError(t, err)
				status := tc.status
				if single && tc.name == "hidden" {
					status = http.StatusNotFound
				}
				require.Equal(t, status, response.Code, response.Body.String())
				require.Equal(t, "application/json", response.Header().Get("Content-Type"))
				if status == http.StatusServiceUnavailable {
					require.Equal(t, "59", response.Header().Get("Retry-After"))
				} else {
					require.Empty(t, response.Header().Get("Retry-After"))
				}
				if status != http.StatusOK {
					want := coreapi.NewCloudError(status, coreapi.CloudErrorCodeNotFound, "", "The requested OpenShift version could not be found.")
					if status == http.StatusServiceUnavailable {
						want = coreapi.NewCloudError(status, coreapi.CloudErrorCodeServiceUnavailable, "", "OpenShift version availability is not yet resolved. Please retry later.")
					} else if tc.location == "eastus" {
						want.Message = "OpenShift versions are not served for the requested location."
					}
					got := &coreapi.CloudError{StatusCode: response.Code}
					require.NoError(t, json.Unmarshal(response.Body.Bytes(), got))
					require.Empty(t, cmp.Diff(want, got))
					require.Equal(t, want.Code, response.Header().Get(coreapi.HeaderNameErrorCode))
					return
				}
				require.Empty(t, response.Header().Get(coreapi.HeaderNameErrorCode))
				var got map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(response.Body.Bytes(), &got))
				if single {
					var name string
					require.NoError(t, json.Unmarshal(got["name"], &name))
					require.Equal(t, "4.21-nightly", name)
				} else {
					var values []json.RawMessage
					require.NoError(t, json.Unmarshal(got["value"], &values))
					if tc.experimental {
						require.Len(t, values, 1)
					} else {
						require.Empty(t, cmp.Diff([]json.RawMessage{}, values))
					}
				}
			})
		}
	}
}
