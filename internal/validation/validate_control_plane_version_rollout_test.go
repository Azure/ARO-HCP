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

package validation

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/fleetapihelpers"
)

func TestValidateControlPlaneVersionRollout(t *testing.T) {
	for _, tc := range []struct {
		channel string
		version coreapi.VersionProfile
		field   string
	}{
		{"", coreapi.VersionProfile{}, "cosmosMetadata.resourceID"},
		{"stable-4.21", coreapi.VersionProfile{ID: "4.21", ChannelGroup: "stable"}, ""},
		{"fast-4.22", coreapi.VersionProfile{ID: "4.22", ChannelGroup: "fast"}, ""},
		{"candidate-4.22", coreapi.VersionProfile{ID: "4.22", ChannelGroup: "candidate"}, ""},
		{"nightly-4.23", coreapi.VersionProfile{ID: "4.23", ChannelGroup: "nightly"}, ""},
		{"stable-4.21", coreapi.VersionProfile{}, "spec.version"},
		{"stable-4.21", coreapi.VersionProfile{ID: "4.21"}, "spec.version"},
		{"stable-4.21", coreapi.VersionProfile{ChannelGroup: "stable"}, "spec.version"},
		{"stable-4.21", coreapi.VersionProfile{ID: "4.22", ChannelGroup: "stable"}, "spec.version"},
		{"stable-4.21", coreapi.VersionProfile{ID: "4.21", ChannelGroup: "fast"}, "spec.version"},
		{"unsupported-4.21", coreapi.VersionProfile{ID: "4.21", ChannelGroup: "unsupported"}, "spec.version"},
		{"stable-invalid", coreapi.VersionProfile{ID: "invalid", ChannelGroup: "stable"}, "spec.version"},
		{"stable-4", coreapi.VersionProfile{ID: "4", ChannelGroup: "stable"}, "spec.version"},
		{"stable-4.21.0", coreapi.VersionProfile{ID: "4.21.0", ChannelGroup: "stable"}, "spec.version"},
		{"stable-4.21-rc.1", coreapi.VersionProfile{ID: "4.21-rc.1", ChannelGroup: "stable"}, "spec.version"},
		{"stable-04.21", coreapi.VersionProfile{ID: "04.21", ChannelGroup: "stable"}, "spec.version"},
		{"stable-4.021", coreapi.VersionProfile{ID: "4.021", ChannelGroup: "stable"}, "spec.version"},
		{"stable-18446744073709551616.21", coreapi.VersionProfile{ID: "18446744073709551616.21", ChannelGroup: "stable"}, "spec.version"},
	} {
		t.Run(tc.channel, func(t *testing.T) {
			rollout := &fleetapi.ControlPlaneVersionRollout{Spec: fleetapi.ControlPlaneVersionRolloutSpec{Version: tc.version}}
			if tc.channel != "" {
				id, err := fleetapihelpers.ToControlPlaneVersionRolloutResourceID(tc.channel)
				require.NoError(t, err)
				rollout.CosmosMetadata = coreapi.CosmosMetadata{ResourceID: id}
			}
			for name, errs := range map[string]field.ErrorList{
				"create": ValidateControlPlaneVersionRolloutCreate(context.Background(), rollout),
				"update": ValidateControlPlaneVersionRolloutUpdate(context.Background(), rollout, rollout.DeepCopy()),
			} {
				t.Run(name, func(t *testing.T) {
					if tc.field == "" || (name == "update" && tc.channel == "stable-4.21" && tc.version == (coreapi.VersionProfile{})) {
						require.Empty(t, errs)
					} else {
						require.Len(t, errs, 1)
						require.Equal(t, tc.field, errs[0].Field)
					}
				})
			}
		})
	}
}

func TestValidateRolloutLegacyUpdates(t *testing.T) {
	t.Parallel()
	id, err := fleetapihelpers.ToControlPlaneVersionRolloutResourceID("stable-4.21")
	require.NoError(t, err)
	legacy := &fleetapi.ControlPlaneVersionRollout{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: id}}
	populated, err := fleetapihelpers.NormalizeRolloutVersion(legacy)
	require.NoError(t, err)
	malformed := legacy.DeepCopy()
	malformed.ResourceID, err = fleetapihelpers.ToControlPlaneVersionRolloutResourceID("stable-invalid")
	require.NoError(t, err)
	renamed := legacy.DeepCopy()
	renamed.ResourceID, err = fleetapihelpers.ToControlPlaneVersionRolloutResourceID("stable-4.22")
	require.NoError(t, err)
	partial := legacy.DeepCopy()
	partial.Spec.Version.ID = "4.21"
	for _, tc := range []struct {
		name     string
		old, new *fleetapi.ControlPlaneVersionRollout
		valid    bool
	}{
		{"legacy writer", legacy, legacy.DeepCopy(), true},
		{"backfill", legacy, populated, true},
		{"structured writer", populated, populated.DeepCopy(), true},
		{"cannot clear profile", populated, legacy, false},
		{"cannot clear partial profile", partial, legacy, false},
		{"missing old snapshot", nil, legacy, false},
		{"malformed legacy name", malformed, malformed.DeepCopy(), false},
		{"legacy rename", legacy, renamed, false},
		{"partial replacement", legacy, partial, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := ValidateControlPlaneVersionRolloutUpdate(t.Context(), tc.new, tc.old)
			if tc.valid {
				require.Empty(t, errs)
			} else {
				require.NotEmpty(t, errs)
			}
		})
	}
}
