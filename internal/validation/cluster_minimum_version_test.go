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
	"testing"

	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/api/operation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/versionpolicy"
)

func TestClusterMinimumVersion(t *testing.T) {
	for _, tc := range []struct {
		name       string
		version    string
		oldVersion string
		exact      string
		oldExact   string
		channel    string
		oldChannel string
		invalid    bool
	}{
		{name: "create below floor", version: "4.19", invalid: true},
		{name: "create at floor", version: versionpolicy.MinimumPublicVersion},
		{name: "create future major", version: "5.0"},
		{name: "create prerelease at floor", version: "4.20.0-rc.1"},
		{name: "create exact prerelease at floor", version: "4.20", exact: "4.20.0-0.nightly-2026-10-01"},
		{name: "create exact below floor", version: "4.19", exact: "4.19.99", invalid: true},
		{name: "unchanged below floor", version: "4.19", oldVersion: "4.19"},
		{name: "unchanged exact below floor", version: "4.19", oldVersion: "4.19", exact: "4.19.1", oldExact: "4.19.1"},
		{name: "changed minor below floor", version: "4.19", oldVersion: "4.18", invalid: true},
		{name: "changed channel below floor", version: "4.19", oldVersion: "4.19", channel: "fast", oldChannel: "stable", invalid: true},
		{name: "new exact below floor", version: "4.19", oldVersion: "4.19", exact: "4.19.1", invalid: true},
		{name: "changed exact below floor", version: "4.19", oldVersion: "4.19", exact: "4.19.2", oldExact: "4.19.1", invalid: true},
		{name: "changed build below floor", version: "4.19", oldVersion: "4.19", exact: "4.19.0+new", oldExact: "4.19.0+old", invalid: true},
		{name: "removed exact below floor", version: "4.19", oldVersion: "4.19", oldExact: "4.19.1", invalid: true},
		{name: "upgrade out of retirement", version: "4.20", oldVersion: "4.19"},
		{name: "changed exact at floor", version: "4.20", oldVersion: "4.20", exact: "4.20.0-rc.2", oldExact: "4.20.0-rc.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			newCluster := &coreapi.Cluster{}
			newCluster.CustomerProperties.Version = coreapi.VersionProfile{ID: tc.version, ChannelGroup: tc.channel}
			if tc.exact != "" {
				newCluster.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneExactVersion = ptr.To(semver.MustParse(tc.exact))
			}
			var oldCluster *coreapi.Cluster
			op := operation.Operation{Type: operation.Create}
			if tc.oldVersion != "" {
				op.Type = operation.Update
				oldCluster = &coreapi.Cluster{}
				oldCluster.CustomerProperties.Version = coreapi.VersionProfile{ID: tc.oldVersion, ChannelGroup: tc.oldChannel}
				if tc.oldExact != "" {
					oldCluster.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneExactVersion = ptr.To(semver.MustParse(tc.oldExact))
				}
			}
			for _, options := range [][]string{nil, testFeatureOptions(metadataapi.FeatureExperimentalReleaseFeatures)} {
				op.Options = options
				errs := validateClusterMinimumVersion(t.Context(), op, newCluster, oldCluster)
				if tc.invalid {
					require.Len(t, errs, 1)
					require.Equal(t, "customerProperties.version.id", errs[0].Field)
					require.Contains(t, errs[0].Detail, "must be at least "+versionpolicy.MinimumPublicVersion)
				} else {
					require.Empty(t, errs)
				}
			}
		})
	}
}

func TestVersionMinimumHelpersRetainStrictFloor(t *testing.T) {
	op := operation.Operation{Type: operation.Update}
	path := field.NewPath("version")
	// Both general-purpose helpers enforce the floor for unchanged node pool versions.
	require.NotEmpty(t, VersionMustBeAtLeast(t.Context(), op, path, ptr.To("4.20.7"), ptr.To("4.20.7"), "4.20.8"))
	require.NotEmpty(t, VersionMustBeAtLeastMajorMinor(t.Context(), op, path, ptr.To("4.19"), ptr.To("4.19"), "4.20"))
}
