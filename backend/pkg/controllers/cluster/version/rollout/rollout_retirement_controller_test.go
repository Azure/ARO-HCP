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
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

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

type failingRolloutList struct {
	fleetlisters.ControlPlaneVersionRolloutLister
}

func (f failingRolloutList) List(context.Context) ([]*fleetapi.ControlPlaneVersionRollout, error) {
	return nil, errors.New("rollout scan failed")
}

func TestReconcileRolloutRetirement(t *testing.T) {
	t.Parallel()
	missingVersion := newTestRollout("stable-4.19", nil, fleetapi.ControlPlaneVersionRolloutStatus{})
	missingVersion.Spec.Version = coreapi.VersionProfile{}
	invalidVersion := newTestRollout("stable-4.19", nil, fleetapi.ControlPlaneVersionRolloutStatus{})
	invalidVersion.Spec.Version.ID = "4.19.1"
	mismatchedVersion := newTestRollout("nightly-4.19", nil, fleetapi.ControlPlaneVersionRolloutStatus{})
	mismatchedVersion.Spec.Version.ID = "4.20"
	retiring := []*fleetapi.ControlPlaneVersionRollout{
		newTestRollout("stable-4.19", nil, fleetapi.ControlPlaneVersionRolloutStatus{}),
		newTestRollout("fast-4.19", nil, fleetapi.ControlPlaneVersionRolloutStatus{}),
		newTestRollout("stable-4.20", nil, fleetapi.ControlPlaneVersionRolloutStatus{}),
		newTestRollout("candidate-4.21", nil, fleetapi.ControlPlaneVersionRolloutStatus{}),
		newTestRollout("nightly-4.19", nil, fleetapi.ControlPlaneVersionRolloutStatus{}),
		newTestRollout("nightly-4.20", nil, fleetapi.ControlPlaneVersionRolloutStatus{}),
		newTestRollout("nightly-4.21", v("4.21.0-0.nightly"), fleetapi.ControlPlaneVersionRolloutStatus{}),
	}
	for _, tc := range []struct {
		name     string
		clusters []*coreapi.Cluster
		spcs     []*coreapi.ServiceProviderCluster
		rollouts []*fleetapi.ControlPlaneVersionRollout
		want     []string
		wantErr  string
	}{
		{name: "empty"},
		{name: "sort obsolete channels and retain floor", rollouts: retiring, want: []string{"fast-4.19", "nightly-4.19", "stable-4.19"}},
		{name: "referenced channel absent from rollouts", clusters: []*coreapi.Cluster{newTestCluster("c1", "stable", "4.18")}, rollouts: retiring, want: []string{"fast-4.19", "nightly-4.19", "stable-4.19"}},
		{name: "nightly reference retains only its channel", clusters: []*coreapi.Cluster{newTestCluster("c1", "nightly", "4.19.0-0.nightly")}, rollouts: retiring, want: []string{"fast-4.19", "stable-4.19"}},
		{name: "all obsolete channels referenced", clusters: []*coreapi.Cluster{newTestCluster("c1", "stable", "4.19"), newTestCluster("c2", "fast", "4.19"), newTestCluster("c3", "nightly", "4.19")}, rollouts: retiring},
		{
			name: "pin threshold is not a dependency", clusters: []*coreapi.Cluster{newTestCluster("c1", "stable", "4.21")},
			spcs:     []*coreapi.ServiceProviderCluster{newTestServiceProviderCluster("c1", v("4.18.1"), nil, &coreapi.ServiceProviderClusterPinnedVersion{ExactVersion: v("4.18.1"), UntilExactVersion: v("4.19.1")})},
			rollouts: []*fleetapi.ControlPlaneVersionRollout{newTestRollout("stable-4.18", nil, fleetapi.ControlPlaneVersionRolloutStatus{}), retiring[0]},
			want:     []string{"stable-4.19"},
		},
		{name: "orphan SPC blocks all retirement", spcs: []*coreapi.ServiceProviderCluster{newTestServiceProviderCluster("c1", v("4.19.1"), nil, nil)}, rollouts: retiring, wantErr: "cannot determine channel group"},
		{name: "missing cluster ID", clusters: []*coreapi.Cluster{{}}, rollouts: retiring, wantErr: "without resource ID"},
		{name: "missing SPC parent", spcs: []*coreapi.ServiceProviderCluster{{}}, rollouts: retiring, wantErr: "without parent"},
		{name: "unknown group blocks all retirement", clusters: []*coreapi.Cluster{newTestCluster("c1", "", "4.21")}, rollouts: retiring, wantErr: "unsupported channel group"},
		{name: "malformed requested version", clusters: []*coreapi.Cluster{newTestCluster("c1", "stable", "invalid")}, rollouts: retiring, wantErr: "invalid version"},
		{name: "malformed nightly version blocks all retirement", clusters: []*coreapi.Cluster{newTestCluster("c1", "nightly", "invalid")}, rollouts: retiring, wantErr: "invalid version"},
		{name: "known references do not excuse uncertain references", clusters: []*coreapi.Cluster{newTestCluster("c1", "stable", "4.19"), newTestCluster("c2", "stable", "invalid")}, rollouts: retiring, wantErr: "invalid version"},
		{name: "nil rollout after deletion candidate", rollouts: []*fleetapi.ControlPlaneVersionRollout{retiring[0], nil}, wantErr: "without resource ID"},
		{name: "missing rollout ID after deletion candidate", rollouts: []*fleetapi.ControlPlaneVersionRollout{retiring[0], {}}, wantErr: "without resource ID"},
		{name: "missing profile after deletion candidate", rollouts: []*fleetapi.ControlPlaneVersionRollout{retiring[0], missingVersion}, wantErr: "unsupported channel group"},
		{name: "malformed profile after deletion candidate", rollouts: []*fleetapi.ControlPlaneVersionRollout{retiring[0], invalidVersion}, wantErr: "invalid rollout minor version"},
		{name: "mismatched profile after deletion candidate", rollouts: []*fleetapi.ControlPlaneVersionRollout{retiring[0], mismatchedVersion}, wantErr: "does not match spec.version"},
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
			var beforeRollouts []*fleetapi.ControlPlaneVersionRollout
			for _, rollout := range tc.rollouts {
				beforeRollouts = append(beforeRollouts, rollout.DeepCopy())
			}
			retired, err := reconcileRolloutRetirement(tc.clusters, tc.spcs, tc.rollouts)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
			}
			require.Empty(t, cmp.Diff(tc.want, retired), "errors must never return partial deletion candidates")
			require.Empty(t, cmp.Diff(beforeClusters, tc.clusters, cmp.AllowUnexported(azcorearm.ResourceID{}, azcorearm.ResourceType{})), "clusters must not be mutated")
			require.Empty(t, cmp.Diff(beforeSPCs, tc.spcs, cmp.AllowUnexported(azcorearm.ResourceID{}, azcorearm.ResourceType{})), "SPCs must not be mutated")
			require.Empty(t, cmp.Diff(beforeRollouts, tc.rollouts, cmp.AllowUnexported(azcorearm.ResourceID{}, azcorearm.ResourceType{})), "rollouts must not be mutated or reordered")
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
				clock := clocktesting.NewFakeClock(statusTestNow)
				cluster := newTestCluster("c1", group, "4.21")
				cluster.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: clock.Now()}
				spc := newTestServiceProviderCluster("c1", nil, nil, nil)
				tc.set(cluster, spc)
				beforeCluster, beforeSPC := cluster.DeepCopy(), spc.DeepCopy()
				rollouts := []*fleetapi.ControlPlaneVersionRollout{
					newTestRollout(group+"-4.19", nil, fleetapi.ControlPlaneVersionRolloutStatus{}),
					newTestRollout("fast-4.19", nil, fleetapi.ControlPlaneVersionRolloutStatus{}),
					newTestRollout(group+"-4.20", nil, fleetapi.ControlPlaneVersionRolloutStatus{}),
				}
				retired, err := reconcileRolloutRetirement([]*coreapi.Cluster{cluster}, []*coreapi.ServiceProviderCluster{spc}, rollouts)
				require.NoError(t, err)
				require.Empty(t, cmp.Diff([]string{"fast-4.19"}, retired), "deleting clusters retain references, not unrelated channel groups")
				require.Empty(t, cmp.Diff(beforeCluster, cluster, cmp.AllowUnexported(azcorearm.ResourceID{}, azcorearm.ResourceType{})), "clusters must not be mutated")
				require.Empty(t, cmp.Diff(beforeSPC, spc, cmp.AllowUnexported(azcorearm.ResourceID{}, azcorearm.ResourceType{})), "SPCs must not be mutated")
				retired, err = reconcileRolloutRetirement(nil, []*coreapi.ServiceProviderCluster{spc}, rollouts)
				require.Error(t, err, "orphan SPC blocks all retirement")
				require.Nil(t, retired)
				retired, err = reconcileRolloutRetirement(nil, nil, rollouts)
				require.NoError(t, err)
				require.Empty(t, cmp.Diff([]string{"fast-4.19", group + "-4.19"}, retired), "retire only after documents disappear")
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
				syncer.serviceProviderClusterLister = failingSPCList{}
			case "rollouts":
				syncer.rolloutLister = failingRolloutList{}
			case "unknown group":
				syncer.clusterLister = &corelistertesting.SliceClusterLister{Clusters: []*coreapi.Cluster{newTestCluster("c1", "", "4.21")}}
			}
			require.Error(t, syncer.SyncOnce(t.Context(), rolloutRetirementKey{}))
			_, err := fleet.ControlPlaneVersionRollouts().Get(t.Context(), "stable-4.19")
			require.NoError(t, err)
		})
	}
}

func TestRetirementSupersedesLegacyBackfill(t *testing.T) {
	t.Parallel()
	legacy := newTestRollout("stable-4.19", v("4.19.1"), fleetapi.ControlPlaneVersionRolloutStatus{})
	fleet, lister := newTestRolloutStore(t, legacy)
	stored, err := lister.Get(t.Context(), "stable-4.19")
	require.NoError(t, err)
	data, ok := fleet.GetDocument(stored.GetCosmosUID())
	require.True(t, ok)
	var document cosmosstorageutils.GenericDocument[fleetapi.ControlPlaneVersionRollout]
	require.NoError(t, json.Unmarshal(data, &document))
	document.Content.Spec.Version = coreapi.VersionProfile{}
	data, err = json.Marshal(document)
	require.NoError(t, err)
	fleet.StoreDocument(document.ID, data)
	legacy, err = lister.Get(t.Context(), "stable-4.19")
	require.NoError(t, err)
	require.Empty(t, cmp.Diff(coreapi.VersionProfile{}, legacy.Spec.Version), "stored rollout must still require backfill")
	before := legacy.DeepCopy()
	syncer := &rolloutRetirementSyncer{
		clusterLister:                &corelistertesting.SliceClusterLister{},
		serviceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{},
		rolloutLister:                &fleetlistertesting.SliceControlPlaneVersionRolloutLister{ControlPlaneVersionRollouts: []*fleetapi.ControlPlaneVersionRollout{legacy}},
		fleetDBClient:                fleet,
	}
	require.NoError(t, syncer.SyncOnce(t.Context(), rolloutRetirementKey{}))
	_, err = fleet.ControlPlaneVersionRollouts().Get(t.Context(), "stable-4.19")
	require.True(t, cosmosstorageutils.IsNotFoundError(err), "retirement supersedes backfill for obsolete, unreferenced rollouts: %v", err)
	require.Empty(t, cmp.Diff(before, legacy, cmp.AllowUnexported(azcorearm.ResourceID{}, azcorearm.ResourceType{})), "retirement leaves informer-owned inputs unchanged")
	legacy.Spec.Version = coreapi.VersionProfile{ID: "4.20", ChannelGroup: "stable"}
	require.ErrorContains(t, syncer.SyncOnce(t.Context(), rolloutRetirementKey{}), "does not match spec.version")
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
	controller := newPeriodicRolloutController(RolloutRetirementControllerName, syncer.SyncOnce)
	require.ErrorContains(t, controller.SyncOnce(t.Context(), rolloutRetirementKey{}), "delete failed")
}
