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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
)

var statusTestNow = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

// completedAgo builds a completed active-version entry whose LastTransitionTime
// is ago before statusTestNow.
func completedAgo(version string, ago time.Duration) coreapi.ServiceProviderClusterActiveVersion {
	entry := completed(version)
	entry.LastTransitionTime = metav1.Time{Time: statusTestNow.Add(-ago)}
	return entry
}

// desiredSince sets the cluster's DesiredVersionLastTransitionTime to ago before
// statusTestNow and returns it.
func desiredSince(serviceProviderCluster *coreapi.ServiceProviderCluster, ago time.Duration) *coreapi.ServiceProviderCluster {
	t := metav1.Time{Time: statusTestNow.Add(-ago)}
	serviceProviderCluster.Spec.ControlPlaneVersion.DesiredVersionLastTransitionTime = &t
	return serviceProviderCluster
}

func TestComputeRolloutStatusCounts(t *testing.T) {
	t.Parallel()

	cfg := NewDefaultRolloutConfig()
	cfg.MinVersionReadyDuration = time.Hour
	cfg.MaxUpgradeDuration["4.21"] = 2 * time.Hour

	serviceProviderClusters := []*coreapi.ServiceProviderCluster{
		// achieved 4.21.6, stable > MinVersionReadyDuration -> successful
		newTestServiceProviderCluster("done-stable", v("4.21.6"), []coreapi.ServiceProviderClusterActiveVersion{completedAgo("4.21.6", 2*time.Hour)}, nil),
		// achieved 4.21.6, but not stable long enough -> achieved, not successful
		newTestServiceProviderCluster("done-fresh", v("4.21.6"), []coreapi.ServiceProviderClusterActiveVersion{completedAgo("4.21.6", 10*time.Minute)}, nil),
		// desires 4.21.6, in flight (base 4.21.4) within max upgrade duration -> mismatched
		desiredSince(newTestServiceProviderCluster("upgrading", v("4.21.6"), []coreapi.ServiceProviderClusterActiveVersion{partial("4.21.6"), completedAgo("4.21.4", 5*time.Hour)}, nil), 30*time.Minute),
		// desires 4.21.6, in flight beyond max upgrade duration -> failed
		desiredSince(newTestServiceProviderCluster("stuck", v("4.21.6"), []coreapi.ServiceProviderClusterActiveVersion{partial("4.21.6"), completedAgo("4.21.4", 5*time.Hour)}, nil), 3*time.Hour),
	}

	counts := computeRolloutStatusCounts(serviceProviderClusters, cfg, statusTestNow)

	assert.Equal(t, int64(4), counts.Desired["4.21.6"], "all four desire 4.21.6")
	assert.Equal(t, int64(2), counts.Achieved["4.21.6"], "two have achieved 4.21.6")
	assert.Equal(t, int64(1), counts.Successful["4.21.6"], "one has been stable long enough")
	assert.Equal(t, int64(2), counts.Mismatched["4.21.6"], "two are still upgrading")
	assert.Equal(t, int64(1), counts.Failed["4.21.6"], "one is stuck beyond the max upgrade duration")
	// bases of in-flight clusters also count as achieved 4.21.4
	assert.Equal(t, int64(2), counts.Achieved["4.21.4"])
}

func TestComputeRolloutStatusCounts_ZeroTransitionTimesLeaveFailedAndSuccessfulEmpty(t *testing.T) {
	t.Parallel()
	cfg := NewDefaultRolloutConfig()
	cfg.MaxUpgradeDuration["4.21"] = time.Hour

	serviceProviderClusters := []*coreapi.ServiceProviderCluster{
		// no LastTransitionTime / DesiredVersionLastTransitionTime set
		newTestServiceProviderCluster("done", v("4.21.6"), []coreapi.ServiceProviderClusterActiveVersion{completed("4.21.6")}, nil),
		newTestServiceProviderCluster("upgrading", v("4.21.6"), []coreapi.ServiceProviderClusterActiveVersion{partial("4.21.6"), completed("4.21.4")}, nil),
	}

	counts := computeRolloutStatusCounts(serviceProviderClusters, cfg, statusTestNow)

	assert.Equal(t, int64(1), counts.Achieved["4.21.6"])
	assert.Equal(t, int64(1), counts.Mismatched["4.21.6"])
	assert.Empty(t, counts.Successful, "zero transition times leave successful empty")
	assert.Empty(t, counts.Failed, "zero transition times leave failed empty")
}

func TestComputeRolloutStatusCounts_PartialOnlyIsNotAchieved(t *testing.T) {
	entry := partial("4.21.6")
	entry.LastTransitionTime = metav1.NewTime(statusTestNow.Add(-5 * time.Hour))
	cluster := desiredSince(newTestServiceProviderCluster("partial-only", v("4.21.6"), []coreapi.ServiceProviderClusterActiveVersion{entry}, nil), 3*time.Hour)
	counts := computeRolloutStatusCounts([]*coreapi.ServiceProviderCluster{cluster}, NewDefaultRolloutConfig(), statusTestNow)
	require.Empty(t, counts.Achieved)
	require.Empty(t, counts.Successful)
	require.Equal(t, int64(1), counts.Mismatched["4.21.6"])
	require.Equal(t, int64(1), counts.Failed["4.21.6"])
}

func TestStatusCollectorSyncer_SyncOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const yStreamChannel = "stable-4.21"

	clusters := []*coreapi.Cluster{
		newTestCluster("c1", "stable", "4.21"),
		newTestCluster("c2", "stable", "4.21"),
		newTestCluster("other-group", "fast", "4.21"), // different channel group, excluded
		newTestCluster("other-minor", "stable", "4.22"),
	}
	serviceProviderClusters := []*coreapi.ServiceProviderCluster{
		newTestServiceProviderCluster("c1", v("4.21.6"), []coreapi.ServiceProviderClusterActiveVersion{completed("4.21.6")}, nil),
		newTestServiceProviderCluster("c2", v("4.21.6"), []coreapi.ServiceProviderClusterActiveVersion{partial("4.21.6"), completed("4.21.4")}, nil),
		newTestServiceProviderCluster("other-group", v("4.21.6"), nil, nil),
		newTestServiceProviderCluster("other-minor", v("4.22.0"), nil, nil),
	}

	mockFleet, lister := newTestRolloutStore(t, newTestRollout(yStreamChannel, v("4.21.6"), fleetapi.ControlPlaneVersionRolloutStatus{}))
	clusterLister := &statusClusterLister{ClusterLister: &corelistertesting.SliceClusterLister{Clusters: clusters}}
	serviceProviderClusterLister := &statusServiceProviderClusterLister{ServiceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: serviceProviderClusters}}
	syncer := &statusCollectorSyncer{
		clock:                        clocktesting.NewFakeClock(statusTestNow),
		rolloutLister:                lister,
		fleetDBClient:                mockFleet,
		serviceProviderClusterLister: serviceProviderClusterLister,
		clusterLister:                clusterLister,
		config:                       NewDefaultRolloutConfig(),
	}

	require.NoError(t, syncer.SyncOnce(ctx, controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: yStreamChannel}))
	require.Equal(t, 1, clusterLister.calls)
	require.Equal(t, 1, serviceProviderClusterLister.calls)

	got, err := mockFleet.ControlPlaneVersionRollouts().Get(ctx, yStreamChannel)
	require.NoError(t, err)
	// Only c1 and c2 are in the stable-4.21 channel.
	assert.Equal(t, int64(2), got.Status.ClusterCountByDesiredExactVersion["4.21.6"])
	assert.Equal(t, int64(1), got.Status.ClusterCountByAchievedExactVersion["4.21.6"], "only c1 fully achieved 4.21.6")
	assert.Equal(t, int64(1), got.Status.MismatchedClusterCountByDesiredExactVersion["4.21.6"], "c2 still upgrading")
	require.Equal(t, ptr.To(int64(2)), got.Status.ReferencingClusterCount)

	require.NoError(t, syncer.SyncOnce(ctx, controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: yStreamChannel}))
	after, err := mockFleet.ControlPlaneVersionRollouts().Get(ctx, yStreamChannel)
	require.NoError(t, err)
	require.Equal(t, got, after, "unchanged status must not be rewritten")
}

type statusClusterLister struct {
	corelisters.ClusterLister
	calls int
	err   error
}

func (l *statusClusterLister) List(ctx context.Context) ([]*coreapi.Cluster, error) {
	l.calls++
	if l.err != nil {
		return nil, l.err
	}
	return l.ClusterLister.List(ctx)
}

type statusServiceProviderClusterLister struct {
	corelisters.ServiceProviderClusterLister
	calls int
	err   error
}

func (l *statusServiceProviderClusterLister) List(ctx context.Context) ([]*coreapi.ServiceProviderCluster, error) {
	l.calls++
	if l.err != nil {
		return nil, l.err
	}
	return l.ServiceProviderClusterLister.List(ctx)
}

func TestStatusCollectorReferenceInventory(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(*statusCollectorSyncer)
		want    *int64
		wantErr string
	}{
		{name: "empty inventory publishes explicit zero", want: ptr.To(int64(0))},
		{name: "requested without SPC", want: ptr.To(int64(1)), mutate: func(c *statusCollectorSyncer) {
			c.clusterLister = &corelistertesting.SliceClusterLister{Clusters: []*coreapi.Cluster{newTestCluster("c1", "stable", "4.21")}}
		}},
		{name: "deleting references remain outside progress", want: ptr.To(int64(1)), mutate: func(c *statusCollectorSyncer) {
			cluster := newTestCluster("c1", "stable", "4.21")
			cluster.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: statusTestNow}
			c.clusterLister = &corelistertesting.SliceClusterLister{Clusters: []*coreapi.Cluster{cluster}}
			c.serviceProviderClusterLister = &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{
				newTestServiceProviderCluster("c1", v("4.21.1"), []coreapi.ServiceProviderClusterActiveVersion{completed("4.21.1")}, nil),
			}}
		}},
		{name: "orphan retains status", wantErr: "cannot determine channel group", mutate: func(c *statusCollectorSyncer) {
			c.serviceProviderClusterLister = &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{
				newTestServiceProviderCluster("orphan", v("4.21.1"), nil, nil),
			}}
		}},
		{name: "cluster list failure retains status", wantErr: "cluster inventory unavailable", mutate: func(c *statusCollectorSyncer) {
			c.clusterLister = &statusClusterLister{err: errors.New("cluster inventory unavailable")}
		}},
		{name: "service provider cluster list failure retains status", wantErr: "service provider cluster inventory unavailable", mutate: func(c *statusCollectorSyncer) {
			c.serviceProviderClusterLister = &statusServiceProviderClusterLister{err: errors.New("service provider cluster inventory unavailable")}
		}},
		{name: "invalid reference retains status", wantErr: "invalid version", mutate: func(c *statusCollectorSyncer) {
			c.clusterLister = &corelistertesting.SliceClusterLister{Clusters: []*coreapi.Cluster{newTestCluster("c1", "stable", "invalid")}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, initial := range []*int64{nil, ptr.To(int64(0)), ptr.To(int64(3))} {
				db, lister := newTestRolloutStore(t, newTestRollout("stable-4.21", nil, fleetapi.ControlPlaneVersionRolloutStatus{
					ReferencingClusterCount:                      initial,
					ClusterCountByDesiredExactVersion:            map[string]int64{"4.21.6": 5},
					MismatchedClusterCountByDesiredExactVersion:  map[string]int64{"4.21.6": 3},
					FailedClusterCountByDesiredExactVersion:      map[string]int64{"4.21.6": 1},
					ClusterCountByAchievedExactVersion:           map[string]int64{"4.21.6": 2},
					SuccessfulClusterCountByAchievedExactVersion: map[string]int64{"4.21.6": 1},
				}))
				before, err := db.ControlPlaneVersionRollouts().Get(t.Context(), "stable-4.21")
				require.NoError(t, err)
				c := &statusCollectorSyncer{
					clock: clocktesting.NewFakeClock(statusTestNow), rolloutLister: lister, fleetDBClient: db,
					clusterLister: &corelistertesting.SliceClusterLister{}, serviceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{},
					config: NewDefaultRolloutConfig(),
				}
				if tc.mutate != nil {
					tc.mutate(c)
				}
				clusterLister := &statusClusterLister{ClusterLister: c.clusterLister}
				serviceProviderClusterLister := &statusServiceProviderClusterLister{ServiceProviderClusterLister: c.serviceProviderClusterLister}
				c.clusterLister = clusterLister
				c.serviceProviderClusterLister = serviceProviderClusterLister
				err = c.SyncOnce(t.Context(), controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: "stable-4.21"})
				if tc.wantErr == "" {
					require.NoError(t, err)
				} else {
					require.ErrorContains(t, err, tc.wantErr)
				}
				require.Equal(t, 1, clusterLister.calls)
				if tc.wantErr == "cluster inventory unavailable" {
					require.Zero(t, serviceProviderClusterLister.calls, "cluster list failure must stop collection immediately")
				} else {
					require.Equal(t, 1, serviceProviderClusterLister.calls)
				}
				got, err := db.ControlPlaneVersionRollouts().Get(t.Context(), "stable-4.21")
				require.NoError(t, err)
				if tc.wantErr != "" {
					require.Equal(t, before, got, "collection errors must leave persisted status and ETag unchanged")
					continue
				}
				require.Equal(t, tc.want, got.Status.ReferencingClusterCount)
				require.Empty(t, got.Status.ClusterCountByDesiredExactVersion)
				require.Empty(t, got.Status.ClusterCountByAchievedExactVersion)
				require.Empty(t, got.Status.MismatchedClusterCountByDesiredExactVersion)
				require.Empty(t, got.Status.FailedClusterCountByDesiredExactVersion)
				require.Empty(t, got.Status.SuccessfulClusterCountByAchievedExactVersion)
			}
		})
	}
}

func TestReferencingClusterCountSerializationAndDeepCopy(t *testing.T) {
	status := fleetapi.ControlPlaneVersionRolloutStatus{}
	data, err := json.Marshal(status)
	require.NoError(t, err)
	require.JSONEq(t, `{}`, string(data))
	status.ReferencingClusterCount = ptr.To(int64(0))
	data, err = json.Marshal(status)
	require.NoError(t, err)
	require.JSONEq(t, `{"referencingClusterCount":0}`, string(data))
	copy := status.DeepCopy()
	*copy.ReferencingClusterCount = 1
	require.Zero(t, *status.ReferencingClusterCount, "deep copy must not share count storage")
}
