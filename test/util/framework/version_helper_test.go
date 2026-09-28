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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
)

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

func TestPickAtLeastControlPlaneVersion(t *testing.T) {
	t.Parallel()

	const nightly = "4.22.0-0.nightly-multi-2026-09-03-080000"
	tests := []struct {
		name      string
		versionID string
		minimum   string
		pin       *string
		nilTags   bool
		wantID    string
		wantPin   string
		wantErr   string
		wantSkip  bool
	}{
		{
			name: "bare lower minor", versionID: "4.21", minimum: "4.22", wantID: "4.22",
		},
		{
			name: "bare same minor", versionID: "4.22", minimum: "4.22", wantID: "4.22",
		},
		{
			name: "bare higher minor", versionID: "4.23", minimum: "4.22", wantID: "4.23",
		},
		{
			name: "satisfying stable pin", versionID: "4.22", pin: to.Ptr("4.22.7"),
			minimum: "4.22", wantID: "4.22", wantPin: "4.22.7",
		},
		{
			name: "stable minor promotion clears obsolete pin", versionID: "4.21", pin: to.Ptr("4.21.7"),
			minimum: "4.22", wantID: "4.22",
		},
		{
			name: "stable patch must not decrease", versionID: "4.22", pin: to.Ptr("4.22.7"),
			minimum: "4.22.5", wantID: "4.22", wantPin: "4.22.7",
		},
		{
			name: "stable patch promotion", versionID: "4.22", pin: to.Ptr("4.22.3"),
			minimum: "4.22.5", wantID: "4.22", wantPin: "4.22.5",
		},
		{
			name: "raw exact version", versionID: "4.22.7",
			minimum: "4.22", wantID: "4.22", wantPin: "4.22.7",
		},
		{
			name: "nightly same minor", versionID: "4.22", pin: to.Ptr(nightly),
			minimum: "4.22", wantID: "4.22", wantPin: nightly,
		},
		{
			name: "nightly patch-zero minimum", versionID: "4.22", pin: to.Ptr(nightly),
			minimum: "4.22.0", wantID: "4.22", wantPin: nightly,
		},
		{
			name: "nightly lower minor skips", versionID: "4.21", pin: to.Ptr("4.21.0-0.nightly-multi-2026-09-03-080000"),
			minimum: "4.22", wantErr: "4.21.0-0.nightly-multi-2026-09-03-080000 does not satisfy minimum 4.22", wantSkip: true,
		},
		{
			name: "nightly higher patch minimum skips", versionID: "4.22", pin: to.Ptr(nightly),
			minimum: "4.22.1", wantErr: nightly + " does not satisfy minimum 4.22.1", wantSkip: true,
		},
		{
			name: "nightly higher major", versionID: "5.0", pin: to.Ptr("5.0.0-0.nightly-multi-2026-09-03-080000"),
			minimum: "4.22", wantID: "5.0", wantPin: "5.0.0-0.nightly-multi-2026-09-03-080000",
		},
		{
			name: "RC same minor", versionID: "4.22", pin: to.Ptr("4.22.0-rc.3"),
			minimum: "4.22", wantID: "4.22", wantPin: "4.22.0-rc.3",
		},
		{
			name: "RC lower minor skips", versionID: "4.21", pin: to.Ptr("4.21.0-rc.3"),
			minimum: "4.22", wantErr: "4.21.0-rc.3 does not satisfy minimum 4.22", wantSkip: true,
		},
		{
			name: "dev preview same minor", versionID: "4.22", pin: to.Ptr("4.22.0-ec.3"),
			minimum: "4.22", wantID: "4.22", wantPin: "4.22.0-ec.3",
		},
		{
			name: "dev preview lower minor skips", versionID: "4.21", pin: to.Ptr("4.21.0-ec.3"),
			minimum: "4.22", wantErr: "4.21.0-ec.3 does not satisfy minimum 4.22", wantSkip: true,
		},
		{
			name: "build metadata preserved", versionID: "4.22", pin: to.Ptr("4.22.7+build.1"),
			minimum: "4.22", wantID: "4.22", wantPin: "4.22.7+build.1",
		},
		{
			name: "invalid version ID with valid pin", versionID: "invalid", pin: to.Ptr(nightly),
			minimum: "4.22", wantErr: `failed to parse control plane version ID "invalid"`,
		},
		{
			name: "invalid minimum leaves pin alone", versionID: "4.22", pin: to.Ptr(nightly),
			minimum: "invalid", wantErr: `failed to parse minimal version "invalid"`,
		},
		{
			name: "invalid pin", versionID: "4.22", pin: to.Ptr("invalid"),
			minimum: "4.22", wantErr: `invalid control plane exact-version pin "invalid"`,
		},
		{
			name: "empty pin", versionID: "4.22", pin: to.Ptr(""),
			minimum: "4.22", wantErr: `invalid control plane exact-version pin ""`,
		},
		{
			name: "non-exact pin", versionID: "4.22", pin: to.Ptr("4.22"),
			minimum: "4.22", wantErr: `invalid control plane exact-version pin "4.22"`,
		},
		{
			name: "mismatched pin", versionID: "4.22", pin: to.Ptr("4.21.7"),
			minimum: "4.22", wantErr: `pin "4.21.7" does not match version ID "4.22"`,
		},
		{
			name: "nil tags for bare version", versionID: "4.21", nilTags: true,
			minimum: "4.22", wantID: "4.22",
		},
		{
			name: "nil tags cannot store exact version", versionID: nightly, nilTags: true,
			minimum: "4.22", wantErr: "with a nil tag map",
		},
		{
			name: "nil tags cannot store exact minimum", versionID: "4.21", nilTags: true,
			minimum: "4.22.5", wantErr: "with a nil tag map",
		},
		{
			name: "noncanonical selected version", versionID: "v4.22.7",
			minimum: "4.22", wantErr: "must be a bare major.minor or an exact semantic version",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tags := map[string]*string{
				"unrelated": to.Ptr("keep"),
				metadataapi.TagClusterControlPlaneExactVersion: tc.pin,
			}
			if tc.nilTags {
				tags = nil
			}
			original := maps.Clone(tags)

			got, err := PickAtLeastControlPlaneVersion(tc.versionID, tc.minimum, tags)
			assert.Equal(t, tc.wantSkip, IsIncompatibleNightlyVersionError(err), "skip classification")
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				assert.Empty(t, got, "errors must not return a usable version ID")
				assert.Equal(t, original, tags, "errors must not mutate tags")
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.wantID, got, "release line")
			if tc.wantPin == "" {
				assert.NotContains(t, tags, metadataapi.TagClusterControlPlaneExactVersion, "no exact pin expected")
			} else {
				assert.Equal(t, to.Ptr(tc.wantPin), tags[metadataapi.TagClusterControlPlaneExactVersion], "exact build")
			}
			assert.Equal(t, original["unrelated"], tags["unrelated"], "unrelated tags must survive")

			afterFirstCall := maps.Clone(tags)
			repeated, err := PickAtLeastControlPlaneVersion(got, tc.minimum, tags)
			require.NoError(t, err)
			assert.Equal(t, got, repeated, "repeat selection must keep the release line")
			assert.Equal(t, afterFirstCall, tags, "repeat selection must keep the exact pin")
		})
	}
}

func TestApplyControlPlaneExactVersionPin(t *testing.T) {
	t.Parallel()
	for _, exact := range []string{"4.22.7", "4.22.0-0.nightly-multi-2026-09-03-080000", "4.22.0-rc.3", "4.22.7+build.1"} {
		t.Run(exact, func(t *testing.T) {
			t.Parallel()
			tags := map[string]*string{
				"unrelated": to.Ptr("keep"),
				metadataapi.TagClusterControlPlaneExactVersion: to.Ptr("4.21.7"),
			}
			for range 2 {
				assert.Equal(t, "4.22", ApplyControlPlaneExactVersionPin(exact, tags))
				assert.Equal(t, to.Ptr(exact), tags[metadataapi.TagClusterControlPlaneExactVersion])
			}
			assert.Equal(t, "4.22", ApplyControlPlaneExactVersionPin("4.22", tags))
			assert.NotContains(t, tags, metadataapi.TagClusterControlPlaneExactVersion, "same-line override must unpin")
			assert.Equal(t, to.Ptr("keep"), tags["unrelated"])
		})
	}
}

func TestControlPlaneMinimumVersionPayload(t *testing.T) {
	t.Parallel()
	builders := map[string]func(string, string, map[string]*string) (any, error){
		"20251223": func(version, channel string, tags map[string]*string) (any, error) {
			return BuildHCPClusterFromParams20251223(ClusterParams20251223{
				OpenshiftVersionId: version, ChannelGroup: channel, Tags: tags,
				APIVisibility: "Private", VnetIntegrationSubnetID: "integration-subnet",
			}, "test-location", nil)
		},
		"20260901": func(version, channel string, tags map[string]*string) (any, error) {
			return BuildHCPClusterFromParams20260901(ClusterParams20260901{
				OpenshiftVersionId: version, ChannelGroup: channel, Tags: tags,
				DisableSwift: true,
			}, "test-location", nil)
		},
	}
	for apiVersion, build := range builders {
		t.Run(apiVersion, func(t *testing.T) {
			t.Parallel()
			for _, tc := range []struct {
				name, defaultVersion, channel, minimum, wantID, wantPin string
			}{
				{"nightly", "4.22.0-0.nightly-multi-2026-09-03-080000", "nightly", "4.22", "4.22", "4.22.0-0.nightly-multi-2026-09-03-080000"},
				{"stable exact", "4.22.7", "stable", "4.22", "4.22", "4.22.7"},
				{"no patch downgrade", "4.22.7", "stable", "4.22.5", "4.22", "4.22.7"},
				{"stable promotion", "4.21.7", "stable", "4.22", "4.22", ""},
				{"bare promotion", "4.21", "candidate", "4.22", "4.22", ""},
			} {
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()
					tags := map[string]*string{"unrelated": to.Ptr("keep")}
					// Reproduce the constructor-time split without resolving defaults over the network.
					versionID := ApplyControlPlaneExactVersionPin(tc.defaultVersion, tags)
					versionID, err := PickAtLeastControlPlaneVersion(versionID, tc.minimum, tags)
					require.NoError(t, err)
					cluster, err := build(versionID, tc.channel, tags)
					require.NoError(t, err)
					data, err := json.Marshal(cluster)
					require.NoError(t, err)

					var payload struct {
						Tags       map[string]*string `json:"tags"`
						Properties struct {
							Version struct {
								ID           string `json:"id"`
								ChannelGroup string `json:"channelGroup"`
							} `json:"version"`
						} `json:"properties"`
					}
					require.NoError(t, json.Unmarshal(data, &payload))
					assert.Equal(t, tc.wantID, payload.Properties.Version.ID)
					assert.Equal(t, tc.channel, payload.Properties.Version.ChannelGroup)
					if tc.wantPin == "" {
						assert.NotContains(t, payload.Tags, metadataapi.TagClusterControlPlaneExactVersion)
					} else {
						assert.Equal(t, to.Ptr(tc.wantPin), payload.Tags[metadataapi.TagClusterControlPlaneExactVersion])
					}
					assert.Equal(t, to.Ptr("keep"), payload.Tags["unrelated"])
				})
			}
		})
	}
}
