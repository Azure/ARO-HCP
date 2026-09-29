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

package framework

import (
	"encoding/json"
	"errors"
	"maps"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/v20240610preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
)

func TestBuildControlPlaneVersion(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, version, wantID string
		tags, wantTags        map[string]*string
	}{
		{name: "nil bare", version: "4.22", wantID: "4.22"},
		{name: "empty bare", version: "4.22", wantID: "4.22", tags: map[string]*string{}, wantTags: map[string]*string{}},
		{name: "nil exact", version: "4.22.7", wantID: "4.22", wantTags: map[string]*string{metadataapi.TagClusterControlPlaneExactVersion: to.Ptr("4.22.7")}},
		{name: "explicit pin", version: "4.22", wantID: "4.22",
			tags:     map[string]*string{metadataapi.TagClusterControlPlaneExactVersion: to.Ptr("4.22.5")},
			wantTags: map[string]*string{metadataapi.TagClusterControlPlaneExactVersion: to.Ptr("4.22.5")}},
		{name: "explicit noncanonical pin", version: "4.22", wantID: "4.22",
			tags:     map[string]*string{strings.ToUpper(metadataapi.TagClusterControlPlaneExactVersion): to.Ptr("4.22.5")},
			wantTags: map[string]*string{strings.ToUpper(metadataapi.TagClusterControlPlaneExactVersion): to.Ptr("4.22.5")}},
		{name: "replace pins", version: "4.22.7", wantID: "4.22",
			tags: map[string]*string{
				metadataapi.TagClusterControlPlaneExactVersion:                  to.Ptr("4.21.7"),
				strings.ToUpper(metadataapi.TagClusterControlPlaneExactVersion): to.Ptr("4.20.7"),
				"user": to.Ptr("keep"),
			},
			wantTags: map[string]*string{metadataapi.TagClusterControlPlaneExactVersion: to.Ptr("4.22.7"), "user": to.Ptr("keep")}},
		{name: "empty ID", tags: map[string]*string{"user": to.Ptr("keep")}, wantTags: map[string]*string{"user": to.Ptr("keep")}},
		{name: "invalid ID", version: "invalid", wantID: "invalid",
			tags:     map[string]*string{metadataapi.TagClusterControlPlaneExactVersion: to.Ptr("invalid")},
			wantTags: map[string]*string{metadataapi.TagClusterControlPlaneExactVersion: to.Ptr("invalid")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			original := maps.Clone(tc.tags)
			for range 2 {
				id, tags := buildControlPlaneVersion(tc.version, tc.tags)
				assert.Equal(t, tc.wantID, id)
				assert.Equal(t, tc.wantTags, tags)
				if tags != nil {
					tags["user"] = to.Ptr("changed")
				}
				assert.Equal(t, original, tc.tags, "building must not mutate or alias caller tags")
			}
		})
	}
}

func TestControlPlaneVersionPayload(t *testing.T) {
	t.Parallel()
	builders := map[string]struct {
		cluster  func(string, string, map[string]*string, bool) (any, error)
		nodePool func(string) any
	}{
		"20240610": {
			cluster: func(version, channel string, tags map[string]*string, _ bool) (any, error) {
				return BuildHCPClusterFromParams20240610(ClusterParams20240610{OpenshiftVersionId: version, ChannelGroup: channel, Tags: tags}, "test-location"), nil
			},
			nodePool: func(version string) any {
				return BuildNodePoolFromParams20240610(NodePoolParams20240610{OpenshiftVersionId: version}, "test-location")
			},
		},
		"20251223": {
			cluster: func(version, channel string, tags map[string]*string, disableSwift bool) (any, error) {
				return BuildHCPClusterFromParams20251223(ClusterParams20251223{OpenshiftVersionId: version, ChannelGroup: channel, Tags: tags, DisableSwift: disableSwift, VnetIntegrationSubnetID: "integration-subnet"}, "test-location", nil)
			},
			nodePool: func(version string) any {
				return BuildNodePoolFromParams20251223(NodePoolParams20251223{OpenshiftVersionId: version}, "test-location")
			},
		},
		"20260630": {
			cluster: func(version, channel string, tags map[string]*string, disableSwift bool) (any, error) {
				return BuildHCPClusterFromParams20260630(ClusterParams20260630{OpenshiftVersionId: version, ChannelGroup: channel, Tags: tags, DisableSwift: disableSwift, VnetIntegrationSubnetID: "integration-subnet"}, "test-location", nil)
			},
			nodePool: func(version string) any {
				return BuildNodePoolFromParams20260630(NodePoolParams20260630{OpenshiftVersionId: version}, "test-location")
			},
		},
		"20260901": {
			cluster: func(version, channel string, tags map[string]*string, disableSwift bool) (any, error) {
				return BuildHCPClusterFromParams20260901(ClusterParams20260901{OpenshiftVersionId: version, ChannelGroup: channel, Tags: tags, DisableSwift: disableSwift, VnetIntegrationSubnetID: "integration-subnet"}, "test-location", nil)
			},
			nodePool: func(version string) any {
				return BuildNodePoolFromParams20260901(NodePoolParams20260901{OpenshiftVersionId: version}, "test-location")
			},
		},
		"20261001": {
			cluster: func(version, channel string, tags map[string]*string, disableSwift bool) (any, error) {
				return BuildHCPClusterFromParams20261001(ClusterParams20261001{OpenshiftVersionId: version, ChannelGroup: channel, Tags: tags, DisableSwift: disableSwift, VnetIntegrationSubnetID: "integration-subnet"}, "test-location", nil)
			},
			nodePool: func(version string) any {
				return BuildNodePoolFromParams20261001(NodePoolParams20261001{OpenshiftVersionId: version}, "test-location")
			},
		},
	}
	type versionPayload struct {
		Tags       map[string]*string `json:"tags"`
		Properties struct {
			Version struct {
				ID, ChannelGroup string
			} `json:"version"`
			Platform struct {
				Subnet *string `json:"vnetIntegrationSubnetId"`
			} `json:"platform"`
		} `json:"properties"`
	}
	readPayload := func(t *testing.T, model any) versionPayload {
		t.Helper()
		data, err := json.Marshal(model)
		require.NoError(t, err)
		var payload versionPayload
		require.NoError(t, json.Unmarshal(data, &payload))
		return payload
	}
	const nightly = "4.22.0-0.nightly-multi-2026-09-03-080000"
	for apiVersion, build := range builders {
		t.Run(apiVersion, func(t *testing.T) {
			t.Parallel()
			for _, tc := range []struct {
				version, channel, minimum, wantID, wantPin string
			}{
				{"4.22.7", "stable", "", "4.22", "4.22.7"},
				{nightly, "nightly", "", "4.22", nightly},
				{"4.22.0-rc.3", "candidate", "", "4.22", "4.22.0-rc.3"},
				{"4.22.0-ec.3", "candidate", "", "4.22", "4.22.0-ec.3"},
				{"4.22.7+build.1", "stable", "", "4.22", "4.22.7+build.1"},
				{"4.22", "candidate", "", "4.22", ""},
				{nightly, "nightly", "4.22", "4.22", nightly},
				{"4.22.7", "stable", "4.22.5", "4.22", "4.22.7"},
				{"4.21.7", "stable", "4.22", "4.22", ""},
			} {
				t.Run(tc.version+"/minimum="+tc.minimum, func(t *testing.T) {
					version := tc.version
					if tc.minimum != "" {
						var err error
						version, err = PickAtLeastOpenshiftVersionId(version, tc.minimum)
						require.NoError(t, err)
					}
					for _, disableSwift := range []bool{true, false} {
						tags := map[string]*string{"user": to.Ptr("keep"), metadataapi.TagClusterCPOImageOverride: to.Ptr("test-image")}
						original := maps.Clone(tags)
						for range 2 {
							cluster, err := build.cluster(version, tc.channel, tags, disableSwift)
							require.NoError(t, err)
							payload := readPayload(t, cluster)
							assert.Equal(t, tc.wantID, payload.Properties.Version.ID)
							assert.Equal(t, tc.channel, payload.Properties.Version.ChannelGroup)
							if tc.wantPin == "" {
								assert.NotContains(t, payload.Tags, metadataapi.TagClusterControlPlaneExactVersion)
							} else {
								assert.Equal(t, to.Ptr(tc.wantPin), payload.Tags[metadataapi.TagClusterControlPlaneExactVersion])
							}
							for key, value := range original {
								assert.Equal(t, value, payload.Tags[key], "unrelated tag %s", key)
							}
							if apiVersion != "20240610" {
								if disableSwift {
									assert.Equal(t, to.Ptr("true"), payload.Tags[metadataapi.TagClusterDisableSwift])
									assert.Nil(t, payload.Properties.Platform.Subnet)
								} else {
									assert.NotContains(t, payload.Tags, metadataapi.TagClusterDisableSwift)
									assert.Equal(t, to.Ptr("integration-subnet"), payload.Properties.Platform.Subnet)
								}
							}
							assert.Equal(t, original, tags, "builder must preserve raw input tags")
						}
						bare, err := build.cluster("4.23", tc.channel, tags, disableSwift)
						require.NoError(t, err)
						assert.NotContains(t, readPayload(t, bare).Tags, metadataapi.TagClusterControlPlaneExactVersion, "a later bare override must not inherit a generated pin")
					}
				})
			}
			for _, inputTags := range []map[string]*string{
				nil,
				{},
				{metadataapi.TagClusterControlPlaneExactVersion: to.Ptr("4.22.5")},
			} {
				original := maps.Clone(inputTags)
				for _, version := range []string{"4.22.7", "4.23.8", "4.22", "", "invalid"} {
					cluster, err := build.cluster(version, "candidate", inputTags, false)
					require.NoError(t, err)
					payload := readPayload(t, cluster)
					switch version {
					case "4.22.7":
						assert.Equal(t, "4.22", payload.Properties.Version.ID)
						assert.Equal(t, to.Ptr(version), payload.Tags[metadataapi.TagClusterControlPlaneExactVersion])
					case "4.23.8":
						assert.Equal(t, "4.23", payload.Properties.Version.ID)
						assert.Equal(t, to.Ptr(version), payload.Tags[metadataapi.TagClusterControlPlaneExactVersion])
					default:
						assert.Equal(t, version, payload.Properties.Version.ID)
						wantPin, hasPin := inputTags[metadataapi.TagClusterControlPlaneExactVersion]
						gotPin, gotHasPin := payload.Tags[metadataapi.TagClusterControlPlaneExactVersion]
						assert.Equal(t, hasPin, gotHasPin, "non-exact IDs must preserve explicit tags")
						assert.Equal(t, wantPin, gotPin)
					}
					assert.Equal(t, original, inputTags)
				}
			}
			for _, version := range []string{"4.22.7", nightly} {
				payload := readPayload(t, build.nodePool(version))
				assert.Equal(t, version, payload.Properties.Version.ID, "node pools must retain concrete version IDs")
				assert.NotContains(t, payload.Tags, metadataapi.TagClusterControlPlaneExactVersion)
			}
		})
	}
}

func TestControlPlaneExactVersionPatchTags(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		version, wantID string
		wantPin         *string
	}{
		{"4.22.7", "4.22", to.Ptr("4.22.7")},
		{"4.22.0-0.nightly-multi-2026-09-03-080000", "4.22", to.Ptr("4.22.0-0.nightly-multi-2026-09-03-080000")},
		{"4.22", "4.22", nil},
	} {
		t.Run(tc.version, func(t *testing.T) {
			id, tags := ControlPlaneExactVersionPatchTags(tc.version)
			update := hcpsdk20240610preview.HcpOpenShiftClusterUpdate{
				Tags: tags,
				Properties: &hcpsdk20240610preview.HcpOpenShiftClusterPropertiesUpdate{
					Version: &hcpsdk20240610preview.VersionProfile{ID: to.Ptr(id)},
				},
			}
			data, err := json.Marshal(update)
			require.NoError(t, err)
			var payload struct {
				Tags       map[string]json.RawMessage `json:"tags"`
				Properties struct {
					Version struct{ ID string } `json:"version"`
				} `json:"properties"`
			}
			require.NoError(t, json.Unmarshal(data, &payload))
			assert.Equal(t, tc.wantID, payload.Properties.Version.ID)
			require.Contains(t, payload.Tags, metadataapi.TagClusterControlPlaneExactVersion, "unpinning must not omit the tag")
			expected, err := json.Marshal(tc.wantPin)
			require.NoError(t, err)
			assert.JSONEq(t, string(expected), string(payload.Tags[metadataapi.TagClusterControlPlaneExactVersion]))
		})
	}
}

func TestPickAtLeastOpenshiftVersionId(t *testing.T) {
	t.Parallel()

	// For examples of latest OpenShift versions, see OpenShift Release Status
	// page at https://openshift-release.apps.ci.l2s4.p1.openshiftapps.com/
	const (
		// examples of OCP Nightly versions
		nightly419 = "4.19.0-0.nightly-multi-2026-09-01-142156"
		nightly421 = "4.21.0-0.nightly-multi-2026-09-03-080000"
		nightly500 = "5.0.0-0.nightly-multi-2026-09-09-181844"
		// examples of OCP Dev Preview versions
		devprev419 = "4.19.0-ec.1"
		devprev421 = "4.21.0-ec.3"
		devprev500 = "5.0.0-ec.6"
		// examples of OCP Release Candidate versions
		rc419 = "4.19.0-rc.1"
		rc421 = "4.21.0-rc.3"
		rc500 = "5.0.0-rc.0"
	)

	tests := []struct {
		name           string
		defaultVersion string
		minimalVersion string
		wantVersion    string
		wantErr        bool
		wantSkippable  bool // error should satisfy IsIncompatibleNightlyVersionError
	}{
		//
		// nightly: defaultVersion satisfies the minimum
		//
		{
			name:           "nightly default higher minor satisfies minimum",
			defaultVersion: nightly421,
			minimalVersion: "4.19",
			wantVersion:    nightly421,
		},
		{
			name:           "nightly default same minor satisfies minimum",
			defaultVersion: nightly419,
			minimalVersion: "4.19",
			wantVersion:    nightly419,
		},
		{
			name:           "nightly default same minor satisfies patch-zero minimum",
			defaultVersion: nightly419,
			minimalVersion: "4.19.0",
			wantVersion:    nightly419,
		},
		{
			name:           "nightly default patch 0 does not satisfy patch-qualified minimum",
			defaultVersion: nightly419,
			minimalVersion: "4.19.1",
			wantErr:        true,
			wantSkippable:  true,
		},
		{
			name:           "nightly default higher major satisfies minimum",
			defaultVersion: nightly500,
			minimalVersion: "4.21",
			wantVersion:    nightly500,
		},

		//
		// dev preview: defaultVersion satisfies the minimum
		//
		{
			name:           "dev preview default higher minor satisfies minimum",
			defaultVersion: devprev421,
			minimalVersion: "4.19",
			wantVersion:    devprev421,
		},
		{
			name:           "dev prevew default same minor satisfies minimum",
			defaultVersion: devprev419,
			minimalVersion: "4.19",
			wantVersion:    devprev419,
		},
		{
			name:           "dev preview default same minor satisfies patch-zero minimum",
			defaultVersion: devprev419,
			minimalVersion: "4.19.0",
			wantVersion:    devprev419,
		},
		{
			name:           "dev preview default patch 0 does not satisfy patch-qualified minimum",
			defaultVersion: devprev419,
			minimalVersion: "4.19.1",
			wantErr:        true,
			wantSkippable:  true,
		},
		{
			name:           "dev preview default higher major satisfies minimum",
			defaultVersion: devprev500,
			minimalVersion: "4.21",
			wantVersion:    devprev500,
		},

		//
		// release candidate: defaultVersion satisfies the minimum
		//
		{
			name:           "rc default higher minor satisfies minimum",
			defaultVersion: rc421,
			minimalVersion: "4.19",
			wantVersion:    rc421,
		},
		{
			name:           "rc default same minor satisfies minimum",
			defaultVersion: rc419,
			minimalVersion: "4.19",
			wantVersion:    rc419,
		},
		{
			name:           "rc default same minor satisfies patch-zero minimum",
			defaultVersion: rc419,
			minimalVersion: "4.19.0",
			wantVersion:    rc419,
		},
		{
			name:           "rc default patch 0 does not satisfy patch-qualified minimum",
			defaultVersion: rc419,
			minimalVersion: "4.19.1",
			wantErr:        true,
			wantSkippable:  true,
		},
		{
			name:           "rc default higher major satisfies minimum",
			defaultVersion: rc500,
			minimalVersion: "4.21",
			wantVersion:    rc500,
		},

		//
		// nightly: defaultVersion does NOT satisfy the minimum → skippable error
		//
		{
			name:           "nightly default lower minor does not satisfy minimum",
			defaultVersion: nightly500,
			minimalVersion: "5.1",
			wantErr:        true,
			wantSkippable:  true,
		},
		{
			name:           "nightly default lower major does not satisfy minimum",
			defaultVersion: nightly419,
			minimalVersion: "5.0",
			wantErr:        true,
			wantSkippable:  true,
		},

		//
		// rc: defaultVersion does NOT satisfy the minimum → skippable error
		//
		{
			name:           "rc default lower minor does not satisfy minimum",
			defaultVersion: rc500,
			minimalVersion: "5.1",
			wantErr:        true,
			wantSkippable:  true,
		},
		{
			name:           "rc default lower major does not satisfy minimum",
			defaultVersion: rc419,
			minimalVersion: "5.0",
			wantErr:        true,
			wantSkippable:  true,
		},

		//
		// stable: defaultVersion satisfies the minimum
		//
		{
			name:           "candidate default higher version satisfies minimum",
			defaultVersion: "4.21.5",
			minimalVersion: "4.19.3",
			wantVersion:    "4.21.5",
		},
		{
			name:           "candidate default equal version satisfies minimum",
			defaultVersion: "4.19.3",
			minimalVersion: "4.19.3",
			wantVersion:    "4.19.3",
		},
		{
			name:           "candidate default higher patch satisfies minimum",
			defaultVersion: "4.19.5",
			minimalVersion: "4.19.3",
			wantVersion:    "4.19.5",
		},

		//
		// stable: defaultVersion does NOT satisfy the minimum → fallback to minimal
		//
		{
			name:           "candidate default lower minor falls back to minimal",
			defaultVersion: "4.18.5",
			minimalVersion: "4.19.3",
			wantVersion:    "4.19.3",
		},
		{
			name:           "candidate default lower patch falls back to minimal",
			defaultVersion: "4.19.2",
			minimalVersion: "4.19.3",
			wantVersion:    "4.19.3",
		},

		//
		// bad inputs
		//
		{
			name:           "unparseable defaultVersion",
			defaultVersion: "not-a-version",
			minimalVersion: "4.19",
			wantErr:        true,
			wantSkippable:  false,
		},
		{
			name:           "unparseable minimalVersion",
			defaultVersion: nightly419,
			minimalVersion: "not-a-version",
			wantErr:        true,
			wantSkippable:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := PickAtLeastOpenshiftVersionId(tc.defaultVersion, tc.minimalVersion)

			if tc.wantErr {
				require.Error(t, err, "expected an error")
				assert.Empty(t, got, "version should be empty on error")
				if tc.wantSkippable {
					assert.True(t, IsIncompatibleNightlyVersionError(err),
						"error should satisfy IsIncompatibleNightlyVersionError that so test cases can Skip; got: %v", err)
					assert.True(t, errors.Is(err, ErrNightlyVersionTooOld),
						"nightly-too-old error should wrap ErrNightlyVersionTooOld); got: %v", err)
				} else {
					assert.False(t, IsIncompatibleNightlyVersionError(err),
						"parse error should not satisfy IsIncompatibleNightlyVersionError")
				}
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.wantVersion, got)
		})
	}
}
