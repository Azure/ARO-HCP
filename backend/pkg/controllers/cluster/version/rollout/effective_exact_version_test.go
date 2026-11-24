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

package rollout

import (
	"encoding/json"
	"testing"

	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
)

func TestEffectiveExactVersion(t *testing.T) {
	version := func(s string) *semver.Version {
		if s == "" {
			return nil
		}
		v := semver.MustParse(s)
		return &v
	}
	for _, tc := range []struct{ name, best, exact, until, want string }{
		{"no pin", "4.21.6", "", "", "4.21.6"},
		{"no selection", "", "", "", ""},
		{"below cutoff", "4.21.5", "4.21.3", "4.21.6", "4.21.3"},
		{"equal cutoff", "4.21.6", "4.21.3", "4.21.6", "4.21.3"},
		{"above cutoff", "4.21.7", "4.21.3", "4.21.6", "4.21.7"},
		{"indefinite", "4.21.7", "4.21.3", "", "4.21.3"},
		{"missing best", "", "4.21.3", "4.21.6", "4.21.3"},
		{"until only", "4.21.7", "", "4.21.8", "4.21.7"},
		{"nightly", "4.21.5", "4.21.0-0.nightly-2026-09-10-123456", "4.21.6", "4.21.0-0.nightly-2026-09-10-123456"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := fleetapi.ControlPlaneVersionRolloutSpec{BestExactVersion: version(tc.best), PinnedVersion: fleetapi.ServiceProviderClusterPinnedVersion{ExactVersion: version(tc.exact), UntilExactVersion: version(tc.until)}}
			data, err := json.Marshal(spec)
			require.NoError(t, err)
			var decoded fleetapi.ControlPlaneVersionRolloutSpec
			require.NoError(t, json.Unmarshal(data, &decoded))
			require.Equal(t, version(tc.want), effectiveExactVersion(&decoded))
			require.Equal(t, version(tc.best), decoded.BestExactVersion)
		})
	}
	data, err := json.Marshal(fleetapi.ServiceProviderClusterPinnedVersion{})
	require.NoError(t, err)
	require.JSONEq(t, "{}", string(data))
}

// TestEffectiveExactVersion_PinReactivatesWhenBestRegresses documents that a
// rollout pin is not cleared when BestExactVersion passes UntilExactVersion.
// The pin stays stored, so if BestExactVersion later moves back to or below
// the threshold the pin becomes active again. That can happen when a version
// that was >= UntilExactVersion is later found to carry known risk, and when
// ARO-HCP evaluates conditional upgrade edges (e.g.
// https://github.com/openshift/enhancements/pull/2152) BestExactVersion may
// move back. Per-cluster pins clear on release; rollout pins do not — a
// service admin (SRE) must clear them explicitly.
func TestEffectiveExactVersion_PinReactivatesWhenBestRegresses(t *testing.T) {
	version := func(s string) *semver.Version {
		v := semver.MustParse(s)
		return &v
	}
	spec := fleetapi.ControlPlaneVersionRolloutSpec{
		PinnedVersion: fleetapi.ServiceProviderClusterPinnedVersion{
			ExactVersion:      version("4.21.3"),
			UntilExactVersion: version("4.21.6"),
		},
	}

	spec.BestExactVersion = version("4.21.7")
	require.False(t, hasActiveRolloutPin(&spec), "best above until releases the pin")
	require.Equal(t, version("4.21.7"), effectiveExactVersion(&spec))
	require.Equal(t, version("4.21.3"), spec.PinnedVersion.ExactVersion, "pin remains stored while inactive")

	spec.BestExactVersion = version("4.21.6")
	require.True(t, hasActiveRolloutPin(&spec), "best equal to until reactivates the stored pin")
	require.Equal(t, version("4.21.3"), effectiveExactVersion(&spec))

	spec.BestExactVersion = version("4.21.5")
	require.True(t, hasActiveRolloutPin(&spec), "best below until keeps the pin active")
	require.Equal(t, version("4.21.3"), effectiveExactVersion(&spec))
}
