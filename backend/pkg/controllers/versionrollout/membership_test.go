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

package versionrollout

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
)

func TestServiceProviderClustersForChannel(t *testing.T) {
	for _, tc := range []struct {
		name, active, desired, requested, group string
		missingCluster, wantMatch               bool
	}{
		{name: "new cluster uses requested minor", requested: "4.21", group: "stable", wantMatch: true},
		{name: "new cluster uses requested exact version", requested: "4.21.6", group: "stable", wantMatch: true},
		{name: "active wins over desired and requested", active: "4.21.6", desired: "4.22.0", requested: "4.23", group: "stable", wantMatch: true},
		{name: "desired wins over requested without active", desired: "4.21.6", requested: "4.22", group: "stable", wantMatch: true},
		{name: "different active minor", active: "4.20.6", desired: "4.21.6", requested: "4.21", group: "stable"},
		{name: "different requested minor", requested: "4.22", group: "stable"},
		{name: "invalid requested version", requested: "invalid", group: "stable"},
		{name: "empty requested version", group: "stable"},
		{name: "different channel group", requested: "4.21", group: "fast"},
		{name: "missing channel group", requested: "4.21"},
		{name: "missing backing cluster", active: "4.21.6", missingCluster: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			spc := newTestSPC("c1", nil, nil, nil)
			if tc.active != "" {
				spc.Status.ControlPlaneVersion.ActiveVersions = []coreapi.HCPClusterActiveVersion{completed(tc.active)}
			}
			if tc.desired != "" {
				spc.Spec.ControlPlaneVersion.DesiredVersion = v(tc.desired)
			}
			resources := []any{spc}
			if !tc.missingCluster {
				resources = append(resources, newTestCluster("c1", tc.group, tc.requested))
			}
			db, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)
			matched, err := serviceProviderClustersForChannel(ctx, &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: db}, &corelistertesting.DBClusterLister{ResourcesDBClient: db}, "stable-4.21")
			require.NoError(t, err)
			if tc.wantMatch {
				require.Len(t, matched, 1)
				require.Equal(t, spc.ResourceID, matched[0].ResourceID)
			} else {
				require.Empty(t, matched)
			}
		})
	}
}
