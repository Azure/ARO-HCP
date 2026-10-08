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

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/fleetlistertesting"
)

type failingClusterList struct{ corelisters.ClusterLister }

func (f failingClusterList) List(context.Context) ([]*coreapi.Cluster, error) {
	return nil, errors.New("cluster scan failed")
}

type failingRetirementSPCList struct {
	corelisters.ServiceProviderClusterLister
}

func (f failingRetirementSPCList) List(context.Context) ([]*coreapi.ServiceProviderCluster, error) {
	return nil, errors.New("SPC scan failed")
}

type failingRetirementRolloutGet struct {
	fleetlisters.ControlPlaneVersionRolloutLister
}

func (f failingRetirementRolloutGet) Get(context.Context, string) (*fleetapi.ControlPlaneVersionRollout, error) {
	return nil, errors.New("rollout get failed")
}

func TestReconcileRolloutRetirement(t *testing.T) {
	t.Parallel()
	missingVersion := newTestRollout("stable-4.19", nil, fleetapi.ControlPlaneVersionRolloutStatus{})
	missingVersion.Spec.Version = coreapi.VersionProfile{}
	retiring := newTestRollout("stable-4.19", nil, fleetapi.ControlPlaneVersionRolloutStatus{})
	for _, tc := range []struct {
		name     string
		clusters []*coreapi.Cluster
		spcs     []*coreapi.ServiceProviderCluster
		rollout  *fleetapi.ControlPlaneVersionRollout
		want     bool
		wantErr  string
	}{
		{name: "unreferenced obsolete target", rollout: retiring, want: true},
		{name: "floor retained", clusters: []*coreapi.Cluster{{}}, spcs: []*coreapi.ServiceProviderCluster{{}}, rollout: newTestRollout("stable-4.20", nil, fleetapi.ControlPlaneVersionRolloutStatus{})},
		{name: "supported retained", clusters: []*coreapi.Cluster{{}}, spcs: []*coreapi.ServiceProviderCluster{{}}, rollout: newTestRollout("candidate-4.21", nil, fleetapi.ControlPlaneVersionRolloutStatus{})},
		{name: "nightly floor retained", rollout: newTestRollout("nightly-4.20", nil, fleetapi.ControlPlaneVersionRolloutStatus{})},
		{name: "nightly supported retained", rollout: newTestRollout("nightly-4.21", v("4.21.0-0.nightly"), fleetapi.ControlPlaneVersionRolloutStatus{})},
		{name: "referenced channel absent from rollouts", clusters: []*coreapi.Cluster{newTestCluster("c1", "stable", "4.18")}, rollout: retiring, want: true},
		{name: "nightly reference retains only its channel", clusters: []*coreapi.Cluster{newTestCluster("c1", "nightly", "4.19.0-0.nightly")}, rollout: retiring, want: true},
		{name: "obsolete target referenced", clusters: []*coreapi.Cluster{newTestCluster("c1", "stable", "4.19")}, rollout: retiring},
		{
			name: "pin threshold permits retirement", clusters: []*coreapi.Cluster{newTestCluster("c1", "stable", "4.21")},
			spcs:    []*coreapi.ServiceProviderCluster{newTestServiceProviderCluster("c1", v("4.18.1"), nil, &coreapi.ServiceProviderClusterPinnedVersion{ExactVersion: v("4.18.1"), UntilExactVersion: v("4.19.1")})},
			rollout: retiring,
			want:    true,
		},
		{name: "orphan SPC blocks retirement", spcs: []*coreapi.ServiceProviderCluster{newTestServiceProviderCluster("c1", v("4.19.1"), nil, nil)}, rollout: retiring, wantErr: "cannot determine channel group"},
		{name: "missing cluster ID", clusters: []*coreapi.Cluster{{}}, rollout: retiring, wantErr: "without resource ID"},
		{name: "missing SPC parent", spcs: []*coreapi.ServiceProviderCluster{{}}, rollout: retiring, wantErr: "without parent"},
		{name: "unknown group blocks retirement", clusters: []*coreapi.Cluster{newTestCluster("c1", "", "4.21")}, rollout: retiring, wantErr: "unsupported channel group"},
		{name: "malformed requested version", clusters: []*coreapi.Cluster{newTestCluster("c1", "stable", "invalid")}, rollout: retiring, wantErr: "invalid version"},
		{name: "malformed nightly version blocks retirement", clusters: []*coreapi.Cluster{newTestCluster("c1", "nightly", "invalid")}, rollout: retiring, wantErr: "invalid version"},
		{name: "complete reference inventory required", clusters: []*coreapi.Cluster{newTestCluster("c1", "stable", "4.19"), newTestCluster("c2", "stable", "invalid")}, rollout: retiring, wantErr: "invalid version"},
		{name: "missing profile defers target", rollout: missingVersion},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var beforeClusters []*coreapi.Cluster
			for _, cluster := range tc.clusters {
				beforeClusters = append(beforeClusters, cluster.DeepCopy())
			}
			var beforeSPCs []*coreapi.ServiceProviderCluster
			for _, spc := range tc.spcs {
				beforeSPCs = append(beforeSPCs, spc.DeepCopy())
			}
			beforeRollout := tc.rollout.DeepCopy()
			retired, err := reconcileRolloutRetirement(tc.clusters, tc.spcs, tc.rollout)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tc.want, retired, "retirement requires a complete, drained reference inventory")
			require.Empty(t, cmp.Diff(beforeClusters, tc.clusters, cmp.AllowUnexported(azcorearm.ResourceID{}, azcorearm.ResourceType{})), "clusters remain unchanged")
			require.Empty(t, cmp.Diff(beforeSPCs, tc.spcs, cmp.AllowUnexported(azcorearm.ResourceID{}, azcorearm.ResourceType{})), "SPCs remain unchanged")
			require.Empty(t, cmp.Diff(beforeRollout, tc.rollout, cmp.AllowUnexported(azcorearm.ResourceID{}, azcorearm.ResourceType{})), "rollout remains unchanged")
		})
	}
}

func TestReconcileRolloutRetirementReferences(t *testing.T) {
	t.Parallel()
	for _, group := range []string{"stable", "nightly"} {
		for _, tc := range []struct {
			name string
			set  func(*coreapi.Cluster, *coreapi.ServiceProviderCluster)
		}{
			{"requested", func(c *coreapi.Cluster, _ *coreapi.ServiceProviderCluster) { c.CustomerProperties.Version.ID = "4.19" }},
			{"desired", func(_ *coreapi.Cluster, s *coreapi.ServiceProviderCluster) {
				s.Spec.ControlPlaneVersion.DesiredVersion = v("4.19.1")
			}},
			{"all active", func(_ *coreapi.Cluster, s *coreapi.ServiceProviderCluster) {
				s.Status.ControlPlaneVersion.ActiveVersions = []coreapi.ServiceProviderClusterActiveVersion{{Version: v("4.21.1")}, {Version: v("4.19.1")}}
			}},
			{"pin", func(_ *coreapi.Cluster, s *coreapi.ServiceProviderCluster) {
				s.Spec.PinnedVersion.ExactVersion = v("4.19.1")
			}},
			{"override", func(c *coreapi.Cluster, _ *coreapi.ServiceProviderCluster) {
				c.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneExactVersion = v("4.19.1")
			}},
			{"cluster active", func(c *coreapi.Cluster, _ *coreapi.ServiceProviderCluster) {
				c.Status.ActiveVersions = []coreapi.HCPClusterActiveVersion{{Version: "4.19"}}
			}},
		} {
			t.Run(group+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				cluster := newTestCluster("c1", group, "4.21")
				cluster.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: statusTestNow}
				spc := newTestServiceProviderCluster("c1", nil, nil, nil)
				tc.set(cluster, spc)
				beforeCluster, beforeSPC := cluster.DeepCopy(), spc.DeepCopy()
				rollout := newTestRollout(group+"-4.19", nil, fleetapi.ControlPlaneVersionRolloutStatus{})
				retired, err := reconcileRolloutRetirement([]*coreapi.Cluster{cluster}, []*coreapi.ServiceProviderCluster{spc}, rollout)
				require.NoError(t, err)
				require.False(t, retired, "deleting clusters retain references")
				retired, err = reconcileRolloutRetirement([]*coreapi.Cluster{cluster}, []*coreapi.ServiceProviderCluster{spc}, newTestRollout("fast-4.19", nil, fleetapi.ControlPlaneVersionRolloutStatus{}))
				require.NoError(t, err)
				require.True(t, retired, "unrelated channel groups qualify for retirement")
				require.Empty(t, cmp.Diff(beforeCluster, cluster, cmp.AllowUnexported(azcorearm.ResourceID{}, azcorearm.ResourceType{})), "clusters remain unchanged")
				require.Empty(t, cmp.Diff(beforeSPC, spc, cmp.AllowUnexported(azcorearm.ResourceID{}, azcorearm.ResourceType{})), "SPCs remain unchanged")
				retired, err = reconcileRolloutRetirement(nil, []*coreapi.ServiceProviderCluster{spc}, rollout)
				require.Error(t, err, "orphan SPC blocks all retirement")
				require.False(t, retired)
				retired, err = reconcileRolloutRetirement(nil, nil, rollout)
				require.NoError(t, err)
				require.True(t, retired, "retire only after documents disappear")
			})
		}
	}
}

func TestRetirementScanErrorsBlockAllDeletion(t *testing.T) {
	for _, failure := range []string{"clusters", "SPCs", "rollouts", "unknown group"} {
		t.Run(failure, func(t *testing.T) {
			fleet, lister := newTestRolloutStore(t, newTestRollout("stable-4.19", nil, fleetapi.ControlPlaneVersionRolloutStatus{}))
			syncer := &rolloutRetirementSyncer{clusterLister: &corelistertesting.SliceClusterLister{}, serviceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{}, rolloutLister: lister, fleetDBClient: fleet}
			switch failure {
			case "clusters":
				syncer.clusterLister = failingClusterList{}
			case "SPCs":
				syncer.serviceProviderClusterLister = failingRetirementSPCList{}
			case "rollouts":
				syncer.rolloutLister = failingRetirementRolloutGet{}
			case "unknown group":
				syncer.clusterLister = &corelistertesting.SliceClusterLister{Clusters: []*coreapi.Cluster{newTestCluster("c1", "", "4.21")}}
			}
			require.Error(t, syncer.SyncOnce(t.Context(), controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: "stable-4.19"}))
			_, err := fleet.ControlPlaneVersionRollouts().Get(t.Context(), "stable-4.19")
			require.NoError(t, err)
		})
	}
}

func TestRetirementWaitsForProfile(t *testing.T) {
	t.Parallel()
	rollout := newTestRollout("stable-4.19", v("4.19.1"), fleetapi.ControlPlaneVersionRolloutStatus{})
	fleet, lister := newTestRolloutStore(t, rollout)
	rollout, err := lister.Get(t.Context(), "stable-4.19")
	require.NoError(t, err)
	before := rollout.DeepCopy()
	syncer := &rolloutRetirementSyncer{
		clusterLister:                &corelistertesting.SliceClusterLister{},
		serviceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{},
		rolloutLister:                &fleetlistertesting.SliceControlPlaneVersionRolloutLister{ControlPlaneVersionRollouts: []*fleetapi.ControlPlaneVersionRollout{rollout}},
		fleetDBClient:                fleet,
	}
	rollout.Spec.Version = coreapi.VersionProfile{}
	require.NoError(t, syncer.SyncOnce(t.Context(), controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: "stable-4.19"}))
	_, err = fleet.ControlPlaneVersionRollouts().Get(t.Context(), "stable-4.19")
	require.NoError(t, err, "absent profile must defer retirement")
	rollout.Spec.Version = before.Spec.Version
	require.NoError(t, syncer.SyncOnce(t.Context(), controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: "stable-4.19"}))
	_, err = fleet.ControlPlaneVersionRollouts().Get(t.Context(), "stable-4.19")
	require.True(t, cosmosstorageutils.IsNotFoundError(err), "obsolete, unreferenced rollouts must be retired: %v", err)
	require.Empty(t, cmp.Diff(before, rollout, cmp.AllowUnexported(azcorearm.ResourceID{}, azcorearm.ResourceType{})), "retirement leaves informer-owned inputs unchanged")
}

func TestRetirementPerKeyIndependence(t *testing.T) {
	t.Parallel()
	target := newTestRollout("stable-4.19", nil, fleetapi.ControlPlaneVersionRolloutStatus{})
	other := newTestRollout("fast-4.19", nil, fleetapi.ControlPlaneVersionRolloutStatus{})
	incomplete := newTestRollout("nightly-4.19", nil, fleetapi.ControlPlaneVersionRolloutStatus{})
	fleet, _ := newTestRolloutStore(t, target, other, incomplete)
	incomplete.Spec.Version = coreapi.VersionProfile{}
	syncer := &rolloutRetirementSyncer{
		clusterLister:                &corelistertesting.SliceClusterLister{},
		serviceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{},
		rolloutLister: &fleetlistertesting.SliceControlPlaneVersionRolloutLister{
			ControlPlaneVersionRollouts: []*fleetapi.ControlPlaneVersionRollout{target, other, incomplete},
		},
		fleetDBClient: fleet,
	}
	key := controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: "stable-4.19"}
	require.NoError(t, syncer.SyncOnce(t.Context(), key), "target retirement depends on its own profile")
	_, err := fleet.ControlPlaneVersionRollouts().Get(t.Context(), key.YStreamChannel)
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
	syncer := &rolloutRetirementSyncer{
		rolloutLister:                lister,
		clusterLister:                failingClusterList{},
		serviceProviderClusterLister: failingRetirementSPCList{},
		fleetDBClient:                failingRetirementDB{},
	}
	require.NoError(t, syncer.SyncOnce(t.Context(), controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: "stable-4.19"}), "missing target completes retirement immediately")
}

func TestRetirementRereadsQueuedTarget(t *testing.T) {
	t.Parallel()
	for _, version := range []string{"4.20", "4.21"} {
		t.Run(version, func(t *testing.T) {
			t.Parallel()
			rollout := newTestRollout("stable-4.19", nil, fleetapi.ControlPlaneVersionRolloutStatus{})
			fleet, _ := newTestRolloutStore(t, rollout)
			lister := &fleetlistertesting.SliceControlPlaneVersionRolloutLister{ControlPlaneVersionRollouts: []*fleetapi.ControlPlaneVersionRollout{rollout}}
			syncer := &rolloutRetirementSyncer{rolloutLister: lister}
			key := controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: "stable-4.19"}
			// The current profile determines retirement eligibility for queued work.
			updated := rollout.DeepCopy()
			updated.Spec.Version.ID = version
			lister.ControlPlaneVersionRollouts[0] = updated
			require.NoError(t, syncer.SyncOnce(t.Context(), key), "supported profiles complete with nil reference listers and database client")
			_, err := fleet.ControlPlaneVersionRollouts().Get(t.Context(), key.YStreamChannel)
			require.NoError(t, err, "current target profile protects a rollout from stale queued work")
		})
	}
}

func TestRetirementRereadsReferencesAfterNotification(t *testing.T) {
	for _, source := range []string{"cluster", "SPC"} {
		t.Run(source, func(t *testing.T) {
			oldCluster := newTestCluster("c1", "stable", "4.19")
			currentCluster := newTestCluster("c1", "stable", "4.20")
			oldSPC := newTestServiceProviderCluster("c1", v("4.19.1"), nil, nil)
			currentSPC := newTestServiceProviderCluster("c1", v("4.20.1"), nil, nil)
			clusters := &corelistertesting.SliceClusterLister{Clusters: []*coreapi.Cluster{currentCluster}}
			spcs := &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{currentSPC}}
			clusterNotifier, spcNotifier := &candidateNotifier{}, &candidateNotifier{}
			queue := &candidateQueue{}
			require.NoError(t, watchRolloutReferences(clusterNotifier, spcNotifier, clusters, spcs, queue, RolloutRetirementControllerName, 5*time.Minute))
			require.Equal(t, 5*time.Minute, *clusterNotifier.options.ResyncPeriod)
			require.Equal(t, 5*time.Minute, *spcNotifier.options.ResyncPeriod)
			if source == "cluster" {
				clusterNotifier.handler.OnUpdate(oldCluster, currentCluster)
			} else {
				spcNotifier.handler.OnUpdate(oldSPC, currentSPC)
			}
			require.Contains(t, queue.channels, "stable-4.19", "releasing a reference must enqueue its old channel")
			require.Contains(t, queue.channels, "stable-4.20", "new references must also be mapped")
			fleet, lister := newTestRolloutStore(t, newTestRollout("stable-4.19", nil, fleetapi.ControlPlaneVersionRolloutStatus{}))
			syncer := &rolloutRetirementSyncer{clusterLister: clusters, serviceProviderClusterLister: spcs, rolloutLister: lister, fleetDBClient: fleet}
			// Restore a reference to verify that reconciliation uses the current inventory.
			if source == "cluster" {
				clusters.Clusters = []*coreapi.Cluster{oldCluster}
			} else {
				spcs.ServiceProviderClusters = []*coreapi.ServiceProviderCluster{oldSPC}
			}
			key := controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: "stable-4.19"}
			require.NoError(t, syncer.SyncOnce(t.Context(), key))
			_, err := fleet.ControlPlaneVersionRollouts().Get(t.Context(), key.YStreamChannel)
			require.NoError(t, err, "current references must protect against stale queued work")
			clusters.Clusters = []*coreapi.Cluster{currentCluster}
			spcs.ServiceProviderClusters = []*coreapi.ServiceProviderCluster{currentSPC}
			require.NoError(t, syncer.SyncOnce(t.Context(), key))
			_, err = fleet.ControlPlaneVersionRollouts().Get(t.Context(), key.YStreamChannel)
			require.True(t, cosmosstorageutils.IsNotFoundError(err), "current drained references permit retirement: %v", err)
		})
	}
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

func (f failingRetirementCRUD) Delete(context.Context, string) error {
	return errors.New("delete failed")
}

func TestRetirementDeleteFailureReachesQueue(t *testing.T) {
	_, lister := newTestRolloutStore(t, newTestRollout("stable-4.19", nil, fleetapi.ControlPlaneVersionRolloutStatus{}))
	syncer := &rolloutRetirementSyncer{
		clusterLister:                &corelistertesting.SliceClusterLister{},
		serviceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{},
		rolloutLister:                lister, fleetDBClient: failingRetirementDB{},
	}
	controller := controllerutils.NewControlPlaneVersionRolloutWatchingController(RolloutRetirementControllerName, nil, 5*time.Minute, syncer)
	require.ErrorContains(t, controller.SyncOnce(t.Context(), controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: "stable-4.19"}), "delete failed")
}
