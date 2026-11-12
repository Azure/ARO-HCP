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
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/fleetlistertesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestReconcileRolloutRetirement(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name           string
		channel        string
		count          *int64
		missingProfile bool
		want           bool
	}{
		{name: "unknown count defers", channel: "stable-4.19"},
		{name: "zero count retires", channel: "stable-4.19", count: ptr.To[int64](0), want: true},
		{name: "positive count retains", channel: "stable-4.19", count: ptr.To[int64](1)},
		{name: "negative count retains", channel: "stable-4.19", count: ptr.To[int64](-1)},
		{name: "floor retained", channel: "stable-4.20", count: ptr.To[int64](0)},
		{name: "supported retained", channel: "candidate-4.21", count: ptr.To[int64](0)},
		{name: "nightly floor retained", channel: "nightly-4.20", count: ptr.To[int64](0)},
		{name: "nightly supported retained", channel: "nightly-4.21", count: ptr.To[int64](0)},
		{name: "nightly obsolete retires", channel: "nightly-4.19", count: ptr.To[int64](0), want: true},
		{name: "missing profile defers", channel: "stable-4.19", count: ptr.To[int64](0), missingProfile: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rollout := newTestRollout(tc.channel, nil, fleetapi.ControlPlaneVersionRolloutStatus{ReferencingClusterCount: tc.count})
			if tc.missingProfile {
				rollout.Spec.Version = coreapi.VersionProfile{}
			}
			before := rollout.DeepCopy()
			require.Equal(t, tc.want, reconcileRolloutRetirement(rollout), "retirement requires an obsolete profile and exactly zero referencing clusters")
			require.Equal(t, before, rollout, "rollout remains unchanged")
		})
	}
}

func TestRetirementRereadsQueuedTarget(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name           string
		count          *int64
		version        string
		missingProfile bool
	}{
		{name: "unknown count", version: "4.19"},
		{name: "positive count", count: ptr.To[int64](2), version: "4.19"},
		{name: "negative count", count: ptr.To[int64](-1), version: "4.19"},
		{name: "floor", count: ptr.To[int64](0), version: "4.20"},
		{name: "supported", count: ptr.To[int64](0), version: "4.21"},
		{name: "missing profile", count: ptr.To[int64](0), missingProfile: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rollout := newTestRollout("stable-4.19", nil, fleetapi.ControlPlaneVersionRolloutStatus{ReferencingClusterCount: ptr.To[int64](0)})
			fleet, _ := newTestRolloutStore(t, rollout)
			rollout, err := fleet.ControlPlaneVersionRollouts().Get(t.Context(), "stable-4.19")
			require.NoError(t, err)
			lister := &fleetlistertesting.SliceControlPlaneVersionRolloutLister{ControlPlaneVersionRollouts: []*fleetapi.ControlPlaneVersionRollout{rollout}}
			syncer := &rolloutRetirementSyncer{rolloutLister: lister}
			key := controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: "stable-4.19"}
			// Replace the queued snapshot with the current cached profile and count.
			updated := rollout.DeepCopy()
			updated.Status.ReferencingClusterCount = tc.count
			updated.Spec.Version.ID = tc.version
			if tc.missingProfile {
				updated.Spec.Version = coreapi.VersionProfile{}
			}
			lister.ControlPlaneVersionRollouts[0] = updated
			before := updated.DeepCopy()
			require.NoError(t, syncer.SyncOnce(t.Context(), key), "ineligible targets complete without a database client")
			_, err = fleet.ControlPlaneVersionRollouts().Get(t.Context(), key.YStreamChannel)
			require.NoError(t, err, "current target state protects against stale queued work")
			require.Equal(t, before, updated, "retirement leaves cached inputs unchanged")

			lister.ControlPlaneVersionRollouts[0] = rollout.DeepCopy()
			syncer.fleetDBClient = fleet
			require.NoError(t, syncer.SyncOnce(t.Context(), key))
			_, err = fleet.ControlPlaneVersionRollouts().Get(t.Context(), key.YStreamChannel)
			require.True(t, cosmosstorageutils.IsNotFoundError(err), "refreshed zero count permits retirement: %v", err)
		})
	}
}

func TestRetirementPerKeyIndependence(t *testing.T) {
	t.Parallel()
	target := newTestRollout("stable-4.19", nil, fleetapi.ControlPlaneVersionRolloutStatus{ReferencingClusterCount: ptr.To[int64](0)})
	other := newTestRollout("fast-4.19", nil, fleetapi.ControlPlaneVersionRolloutStatus{ReferencingClusterCount: ptr.To[int64](0)})
	incomplete := newTestRollout("nightly-4.19", nil, fleetapi.ControlPlaneVersionRolloutStatus{})
	fleet, _ := newTestRolloutStore(t, target, other, incomplete)
	target, err := fleet.ControlPlaneVersionRollouts().Get(t.Context(), "stable-4.19")
	require.NoError(t, err)
	incomplete.Spec.Version = coreapi.VersionProfile{}
	syncer := &rolloutRetirementSyncer{
		rolloutLister: &fleetlistertesting.SliceControlPlaneVersionRolloutLister{
			ControlPlaneVersionRollouts: []*fleetapi.ControlPlaneVersionRollout{target, other, incomplete},
		},
		fleetDBClient: fleet,
	}
	key := controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: "stable-4.19"}
	require.NoError(t, syncer.SyncOnce(t.Context(), key), "target retirement depends on its own profile and count")
	_, err = fleet.ControlPlaneVersionRollouts().Get(t.Context(), key.YStreamChannel)
	require.True(t, cosmosstorageutils.IsNotFoundError(err), "target must be retired: %v", err)
	for _, channel := range []string{"fast-4.19", "nightly-4.19"} {
		_, err := fleet.ControlPlaneVersionRollouts().Get(t.Context(), channel)
		require.NoError(t, err, "other rollouts remain available")
	}
	require.NoError(t, syncer.SyncOnce(t.Context(), key), "retirement is idempotent for stale cache entries")
}

func TestRetirementMissingTarget(t *testing.T) {
	t.Parallel()
	_, lister := newTestRolloutStore(t)
	syncer := &rolloutRetirementSyncer{rolloutLister: lister}
	require.NoError(t, syncer.SyncOnce(t.Context(), controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: "stable-4.19"}), "missing target completes retirement without a database client")
}

type failingRetirementRolloutGet struct {
	fleetlisters.ControlPlaneVersionRolloutLister
}

func (f failingRetirementRolloutGet) Get(context.Context, string) (*fleetapi.ControlPlaneVersionRollout, error) {
	return nil, errors.New("rollout get failed")
}

func TestRetirementGetFailureBlocksDeletion(t *testing.T) {
	t.Parallel()
	syncer := &rolloutRetirementSyncer{rolloutLister: failingRetirementRolloutGet{}}
	err := syncer.SyncOnce(t.Context(), controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: "stable-4.19"})
	require.ErrorContains(t, err, "get rollout \"stable-4.19\" for retirement: rollout get failed")
	require.IsType(t, &utils.LineTrackingError{}, err)
}

type failingRetirementDB struct {
	fleetcosmosstorage.FleetDBClient
}

func (f failingRetirementDB) ControlPlaneVersionRollouts() cosmosstorageutils.ValidatingResourceCRUD[fleetapi.ControlPlaneVersionRollout, *fleetapi.ControlPlaneVersionRollout] {
	return failingRetirementCRUD{}
}

type failingRetirementCRUD struct {
	cosmosstorageutils.ValidatingResourceCRUD[fleetapi.ControlPlaneVersionRollout, *fleetapi.ControlPlaneVersionRollout]
}

func (f failingRetirementCRUD) DeleteWithETag(context.Context, string, azcore.ETag) error {
	return errors.New("delete failed")
}

func TestRetirementStaleCacheCannotDeleteUpdatedRollout(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		count *int64
	}{
		{name: "positive count", count: ptr.To[int64](1)},
		{name: "unknown count"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fleet, _ := newTestRolloutStore(t, newTestRollout("stable-4.19", nil, fleetapi.ControlPlaneVersionRolloutStatus{ReferencingClusterCount: ptr.To[int64](0)}))
			crud := fleet.ControlPlaneVersionRollouts()
			cached, err := crud.Get(t.Context(), "stable-4.19")
			require.NoError(t, err)
			updated := cached.DeepCopy()
			updated.Status.ReferencingClusterCount = tc.count
			updated, err = crud.Replace(t.Context(), updated, cached, nil)
			require.NoError(t, err)
			require.NotEqual(t, cached.CosmosETag, updated.CosmosETag)
			before := fleet.GetAllDocuments()
			lister := &fleetlistertesting.SliceControlPlaneVersionRolloutLister{ControlPlaneVersionRollouts: []*fleetapi.ControlPlaneVersionRollout{cached}}
			syncer := &rolloutRetirementSyncer{rolloutLister: lister, fleetDBClient: fleet}
			key := controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: "stable-4.19"}
			require.NoError(t, syncer.SyncOnce(t.Context(), key), "a stale decision waits for the watch update")
			require.Equal(t, before, fleet.GetAllDocuments(), "stale zero membership must not delete newer state")
			lister.ControlPlaneVersionRollouts[0] = updated
			require.NoError(t, syncer.SyncOnce(t.Context(), key))
			require.Equal(t, before, fleet.GetAllDocuments(), "the refreshed count prevents retirement")
		})
	}
}

func TestRetirementDeleteFailureReachesQueue(t *testing.T) {
	t.Parallel()
	_, lister := newTestRolloutStore(t, newTestRollout("stable-4.19", nil, fleetapi.ControlPlaneVersionRolloutStatus{ReferencingClusterCount: ptr.To[int64](0)}))
	syncer := &rolloutRetirementSyncer{rolloutLister: lister, fleetDBClient: failingRetirementDB{}}
	controller := controllerutils.NewControlPlaneVersionRolloutWatchingController(RolloutRetirementControllerName, nil, 5*time.Minute, syncer)
	err := controller.SyncOnce(t.Context(), controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: "stable-4.19"})
	require.ErrorContains(t, err, "retire rollout \"stable-4.19\": delete failed")
	require.IsType(t, &utils.LineTrackingError{}, err)
}
