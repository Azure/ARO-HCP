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

	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
)

func TestSelectBestExactVersion(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		graphBest *semver.Version
		minimum   *semver.Version
		want      *semver.Version
	}{
		{name: "nothing selectable", graphBest: nil, minimum: nil, want: nil},
		{name: "graph only", graphBest: v("4.21.6"), minimum: nil, want: v("4.21.6")},
		{name: "minimum floor only", graphBest: nil, minimum: v("4.21.6"), want: v("4.21.6")},
		{name: "graph above floor wins", graphBest: v("4.21.8"), minimum: v("4.21.6"), want: v("4.21.8")},
		{name: "floor above graph wins", graphBest: v("4.21.4"), minimum: v("4.21.6"), want: v("4.21.6")},
		{name: "equal", graphBest: v("4.21.6"), minimum: v("4.21.6"), want: v("4.21.6")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := selectBestExactVersion(tc.graphBest, tc.minimum)
			if tc.want == nil {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.True(t, got.EQ(*tc.want), "got %v want %v", got, tc.want)
		})
	}
}

func TestBestVersionSelectionSyncer_SyncOnce(t *testing.T) {
	t.Parallel()

	const channel = "stable-4.21"

	t.Run("writes graph best when above floor", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		mockFleet, lister := newTestRolloutStore(t, newTestRollout(channel, nil, fleetapi.ControlPlaneVersionRolloutStatus{}))
		cfg := NewDefaultRolloutConfig()
		cfg.MinimumVersions[channel] = *v("4.21.2")

		syncer := &bestVersionSelectionSyncer{rolloutLister: lister, fleetDBClient: mockFleet, selector: fakeBestVersionSelector{best: v("4.21.6")}, config: cfg}
		require.NoError(t, syncer.SyncOnce(ctx, controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: channel}))

		got, err := mockFleet.ControlPlaneVersionRollouts().Get(ctx, channel)
		require.NoError(t, err)
		require.NotNil(t, got.Spec.BestExactVersion)
		assert.True(t, got.Spec.BestExactVersion.EQ(*v("4.21.6")))
	})

	t.Run("applies minimum floor when graph is lower", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		mockFleet, lister := newTestRolloutStore(t, newTestRollout(channel, nil, fleetapi.ControlPlaneVersionRolloutStatus{}))
		cfg := NewDefaultRolloutConfig()
		cfg.MinimumVersions[channel] = *v("4.21.6")

		syncer := &bestVersionSelectionSyncer{rolloutLister: lister, fleetDBClient: mockFleet, selector: fakeBestVersionSelector{best: v("4.21.4")}, config: cfg}
		require.NoError(t, syncer.SyncOnce(ctx, controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: channel}))

		got, err := mockFleet.ControlPlaneVersionRollouts().Get(ctx, channel)
		require.NoError(t, err)
		require.NotNil(t, got.Spec.BestExactVersion)
		assert.True(t, got.Spec.BestExactVersion.EQ(*v("4.21.6")))
	})

	t.Run("no-op when unchanged", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		mockFleet, lister := newTestRolloutStore(t, newTestRollout(channel, v("4.21.6"), fleetapi.ControlPlaneVersionRolloutStatus{}))
		cfg := NewDefaultRolloutConfig()

		syncer := &bestVersionSelectionSyncer{rolloutLister: lister, fleetDBClient: mockFleet, selector: fakeBestVersionSelector{best: v("4.21.6")}, config: cfg}
		require.NoError(t, syncer.SyncOnce(ctx, controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: channel}))

		got, err := mockFleet.ControlPlaneVersionRollouts().Get(ctx, channel)
		require.NoError(t, err)
		assert.Equal(t, int64(1), got.GetInstanceVersion(), "expected no write when best is unchanged")
	})

	t.Run("no-op when nothing selectable", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		mockFleet, lister := newTestRolloutStore(t, newTestRollout(channel, nil, fleetapi.ControlPlaneVersionRolloutStatus{}))
		cfg := NewDefaultRolloutConfig()

		syncer := &bestVersionSelectionSyncer{rolloutLister: lister, fleetDBClient: mockFleet, selector: fakeBestVersionSelector{best: nil}, config: cfg}
		require.NoError(t, syncer.SyncOnce(ctx, controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: channel}))

		got, err := mockFleet.ControlPlaneVersionRollouts().Get(ctx, channel)
		require.NoError(t, err)
		assert.Nil(t, got.Spec.BestExactVersion)
		assert.Equal(t, int64(1), got.GetInstanceVersion(), "expected no write when nothing is selectable")
	})

	t.Run("missing rollout is a no-op", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		mockFleet, lister := newTestRolloutStore(t)
		syncer := &bestVersionSelectionSyncer{rolloutLister: lister, fleetDBClient: mockFleet, selector: fakeBestVersionSelector{best: v("4.21.6")}, config: NewDefaultRolloutConfig()}
		require.NoError(t, syncer.SyncOnce(ctx, controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: channel}))
	})
}
