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
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/blang/semver/v4"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"

	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apitesting/coreapitesting"
	"github.com/Azure/ARO-HCP/internal/errorutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestClusterPatchExactVersionIntent(t *testing.T) {
	apiVersions := slices.Sorted(maps.Keys(NewTestFrontend(t).apiRegistry.ListVersions()))
	require.Len(t, apiVersions, 5)
	for _, apiVersion := range apiVersions {
		for _, minor := range []string{"4.20"} {
			for _, tc := range []struct {
				name           string
				body           string
				oldTag         string
				replace        bool
				deregistered   bool
				inconsistent   bool
				wantExact      string
				changesPin     bool
				validationPath string
			}{
				{name: "unrelated tags", body: `{"tags":{"owner":"new"}}`, wantExact: ".1"},
				{name: "empty patch", body: `{}`, wantExact: ".1"},
				{name: "null tags retain ID pin", body: `{"tags":null}`, wantExact: ".1"},
				{name: "null tags retain tag pin", body: `{"tags":null}`, oldTag: metadataapi.TagClusterControlPlaneExactVersion, wantExact: ".1"},
				{name: "empty version", body: `{"properties":{"version":{}}}`, wantExact: ".1"},
				{name: "same channel", body: `{"properties":{"version":{"channelGroup":"stable"}}}`, wantExact: ".1"},
				{name: "explicit minor unpins", body: `{"properties":{"version":{"id":"%s"}}}`, changesPin: true},
				{name: "explicit same exact", body: `{"properties":{"version":{"id":"%s.1"}}}`, wantExact: ".1"},
				{name: "explicit changed exact", body: `{"properties":{"version":{"id":"%s.2"}}}`, wantExact: ".2", changesPin: true},
				{name: "tag pins new exact", body: `{"tags":{"aro-hcp.experimental.cluster.control-plane-exact-version":"%s.2"}}`, wantExact: ".2", changesPin: true},
				{name: "tag overrides explicit ID", body: `{"tags":{"ARO-HCP.Experimental.Cluster.Control-Plane-Exact-Version":"%[1]s.2"},"properties":{"version":{"id":"%[1]s.3"}}}`, wantExact: ".2", changesPin: true},
				{name: "unrelated tags retain tag pin", body: `{"tags":{"owner":"new"}}`, oldTag: metadataapi.TagClusterControlPlaneExactVersion, wantExact: ".1"},
				{name: "remove exact tag unpins", body: `{"tags":{"aro-hcp.experimental.cluster.control-plane-exact-version":null}}`, oldTag: metadataapi.TagClusterControlPlaneExactVersion, changesPin: true},
				{name: "remove mixed case exact tag unpins", body: `{"tags":{"ARO-HCP.Experimental.Cluster.Control-Plane-Exact-Version":null}}`, oldTag: "ARO-HCP.Experimental.Cluster.Control-Plane-Exact-Version", changesPin: true},
				{name: "remove tag and explicit full ID pins", body: `{"tags":{"aro-hcp.experimental.cluster.control-plane-exact-version":null},"properties":{"version":{"id":"%s.2"}}}`, oldTag: metadataapi.TagClusterControlPlaneExactVersion, wantExact: ".2", changesPin: true},
				{name: "remove mixed case tag and explicit full ID pins", body: `{"tags":{"ARO-HCP.Experimental.Cluster.Control-Plane-Exact-Version":null},"properties":{"version":{"id":"%s.2"}}}`, oldTag: "ARO-HCP.Experimental.Cluster.Control-Plane-Exact-Version", wantExact: ".2", changesPin: true},
				{name: "existing tag overrides explicit minor", body: `{"properties":{"version":{"id":"%s"}}}`, oldTag: metadataapi.TagClusterControlPlaneExactVersion, wantExact: ".1"},
				{name: "existing mixed case tag overrides explicit full ID", body: `{"properties":{"version":{"id":"%s.2"}}}`, oldTag: "ARO-HCP.Experimental.Cluster.Control-Plane-Exact-Version", wantExact: ".1"},
				{name: "null ID is invalid", body: `{"properties":{"version":{"id":null}}}`, validationPath: "properties.version.id"},
				{name: "null version is invalid", body: `{"properties":{"version":null}}`, validationPath: "properties.version.id"},
				{name: "null properties is invalid", body: `{"properties":null}`, validationPath: "properties.version.id"},
				{name: "tag supplies null ID", body: `{"properties":{"version":{"id":null}}}`, oldTag: metadataapi.TagClusterControlPlaneExactVersion, wantExact: ".1"},
				{name: "tag supplies null version", body: `{"properties":{"version":null}}`, oldTag: metadataapi.TagClusterControlPlaneExactVersion, wantExact: ".1", validationPath: "properties.version.channelGroup"},
				{name: "tag supplies null properties version only", body: `{"properties":null}`, oldTag: metadataapi.TagClusterControlPlaneExactVersion, wantExact: ".1", validationPath: "properties.version.channelGroup"},
				{name: "deregistered AFEC clears ID pin", body: `{"tags":{"owner":"new"}}`, deregistered: true, changesPin: true},
				{name: "deregistered AFEC clears tag pin", body: `{"tags":{"owner":"new"}}`, oldTag: metadataapi.TagClusterControlPlaneExactVersion, deregistered: true, changesPin: true},
				{name: "inconsistent stored pin fails", body: `{"tags":{"owner":"new"}}`, inconsistent: true},
				{name: "PUT minor still unpins", replace: true, changesPin: true},
			} {
				t.Run(apiVersion+"/"+minor+"/"+tc.name, func(t *testing.T) {
					f := NewTestFrontend(t)
					f.clock = clocktesting.NewFakeClock(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
					cluster := coreapitesting.MinimumValidClusterTestCase()
					cluster.CustomerProperties.Version = coreapi.VersionProfile{ID: minor, ChannelGroup: metadataapi.ChannelGroupStable}
					cluster.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneExactVersion = ptr.To(semver.MustParse(minor + ".1"))
					cluster.Tags = map[string]string{"owner": "old"}
					if tc.oldTag != "" {
						cluster.Tags[tc.oldTag] = minor + ".1"
					}
					if tc.inconsistent {
						cluster.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneExactVersion = ptr.To(semver.MustParse("4.21.1"))
					}
					cluster.SetResourceID(cluster.ID)
					cluster.SetPartitionKey(cluster.ID.SubscriptionID)
					cluster, err := f.resourcesDBClient.HCPClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName).Create(t.Context(), cluster, nil)
					require.NoError(t, err)
					subscription := newTestSubscription(cluster.ID.SubscriptionID, coreapi.SubscriptionStateRegistered, &coreapi.SubscriptionProperties{
						RegisteredFeatures: &[]coreapi.Feature{{Name: ptr.To(metadataapi.FeatureExperimentalReleaseFeatures), State: ptr.To("Registered")}},
					})
					if tc.deregistered {
						*(*subscription.Properties.RegisteredFeatures)[0].State = "Unregistered"
					}
					// The middleware snapshot supplies feature registrations for the update.
					informer, _ := f.informers.ServiceProviderClusters()
					require.NoError(t, informer.GetStore().Add(newTestServiceProviderCluster(cluster.ID)))
					version, ok := f.apiRegistry.Lookup(apiVersion)
					require.True(t, ok)
					body := tc.body
					if strings.Contains(body, "%") {
						body = fmt.Sprintf(body, minor)
					}
					method := http.MethodPatch
					if tc.replace {
						method = http.MethodPut
						serialized, err := coreapi.MarshalJSON(version.NewCluster(cluster))
						require.NoError(t, err)
						body = string(serialized)
					}
					ctx := ContextWithVersion(t.Context(), version)
					ctx = ContextWithSubscription(ctx, subscription)
					ctx = ContextWithCorrelationData(ctx, &coreapi.CorrelationData{})
					ctx = ContextWithSystemData(ctx, cluster.SystemData)
					ctx = ContextWithBody(ctx, []byte(body))
					ctx = utils.ContextWithResourceID(ctx, cluster.ID)
					request := httptest.NewRequestWithContext(ctx, method, cluster.ID.String(), nil)
					response := httptest.NewRecorder()
					original := cluster.DeepCopy()
					errorutils.ReportError(func(w http.ResponseWriter, r *http.Request) error {
						if tc.replace {
							return f.updateHCPCluster(w, r, cluster)
						}
						return f.patchHCPCluster(w, r, cluster)
					})(response, request)
					require.Empty(t, cmp.Diff(original, cluster, coreapi.CmpDiffOptions...), "the stored input must remain unchanged")
					stored, err := f.resourcesDBClient.HCPClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName).Get(t.Context(), cluster.ID.Name)
					require.NoError(t, err)
					if tc.inconsistent {
						require.Equal(t, http.StatusInternalServerError, response.Code, response.Body.String())
						require.Empty(t, cmp.Diff(original, stored, coreapi.CmpDiffOptions...), "inconsistent state must not be rewritten")
						_, err := decodeDesiredClusterPatch(ctx, cluster)
						require.ErrorContains(t, err, "does not match exact version")
						return
					}
					if tc.validationPath != "" {
						require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
						require.Contains(t, response.Body.String(), tc.validationPath)
						if tc.oldTag != "" {
							require.NotContains(t, response.Body.String(), "properties.version.id: Required value", "the authoritative tag supplies the version ID")
						}
						require.Empty(t, cmp.Diff(original, stored, coreapi.CmpDiffOptions...), "rejected update must leave storage unchanged")
						return
					}
					if minor == "4.19" && tc.changesPin {
						require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
						require.Contains(t, response.Body.String(), "must be at least 4.20")
						require.Empty(t, cmp.Diff(original, stored, coreapi.CmpDiffOptions...), "rejected update must leave storage unchanged")
						return
					}
					wantStatus := http.StatusAccepted
					if tc.replace {
						wantStatus = http.StatusOK
					}
					require.Equal(t, wantStatus, response.Code, response.Body.String())
					require.Equal(t, minor, stored.CustomerProperties.Version.ID)
					exact := stored.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneExactVersion
					if tc.wantExact == "" {
						require.Nil(t, exact, "explicit unpin must clear the exact version")
					} else {
						require.NotNil(t, exact)
						require.Equal(t, minor+tc.wantExact, exact.String())
					}
					if strings.Contains(body, `"owner":"new"`) {
						require.Equal(t, "new", stored.Tags["owner"], "unrelated tag update must be persisted")
					}
				})
			}
		}
	}
}
