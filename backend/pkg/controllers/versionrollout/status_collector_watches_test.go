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
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
)

func statusWatchFixture(t *testing.T) (*candidateQueue, cache.ResourceEventHandler, cache.ResourceEventHandler) {
	t.Helper()
	_, lister := newTestRolloutStore(t,
		newTestRollout("stable-4.21", nil, fleetapi.ControlPlaneVersionRolloutStatus{}),
		newTestRollout("candidate-4.21", nil, fleetapi.ControlPlaneVersionRolloutStatus{}),
		newTestRollout("fast-4.22", nil, fleetapi.ControlPlaneVersionRolloutStatus{}),
		newTestRollout("stable-4.23", nil, fleetapi.ControlPlaneVersionRolloutStatus{}))
	c := &statusCollectorSyncer{rolloutLister: lister}
	q := &candidateQueue{}
	clusters, serviceProviderClusters := &candidateNotifier{}, &candidateNotifier{}
	require.NoError(t, c.watchStatusInputs(clusters, serviceProviderClusters, q))
	require.NotNil(t, clusters.options.ResyncPeriod)
	require.Zero(t, *clusters.options.ResyncPeriod)
	require.NotNil(t, serviceProviderClusters.options.ResyncPeriod)
	require.Zero(t, *serviceProviderClusters.options.ResyncPeriod)
	return q, clusters.handler, serviceProviderClusters.handler
}

func TestStatusCollectorServiceProviderClusterUpdates(t *testing.T) {
	all := []string{"stable-4.21", "candidate-4.21", "fast-4.22", "stable-4.23"}
	minor21 := []string{"stable-4.21", "candidate-4.21"}
	for _, tc := range []struct {
		name   string
		mutate func(*coreapi.ServiceProviderCluster)
		want   []string
	}{
		{"unrelated pinned version", func(s *coreapi.ServiceProviderCluster) { s.Spec.PinnedVersion.ExactVersion = v("4.21.1") }, nil},
		{"unrelated desired channels", func(s *coreapi.ServiceProviderCluster) { s.Status.DesiredVersionChannels = []string{"fast-4.22"} }, nil},
		{"desired version", func(s *coreapi.ServiceProviderCluster) { s.Spec.ControlPlaneVersion.DesiredVersion = v("4.22.0") }, []string{"stable-4.21", "candidate-4.21", "fast-4.22"}},
		{"desired transition time", func(s *coreapi.ServiceProviderCluster) {
			s.Spec.ControlPlaneVersion.DesiredVersionLastTransitionTime = &metav1.Time{Time: time.Unix(100, 0)}
		}, minor21},
		{"active patch", func(s *coreapi.ServiceProviderCluster) {
			s.Status.ControlPlaneVersion.ActiveVersions[0].Version = v("4.21.7")
		}, minor21},
		{"active minor", func(s *coreapi.ServiceProviderCluster) {
			s.Status.ControlPlaneVersion.ActiveVersions[0].Version = v("4.22.0")
		}, minor21},
		{"active transition time", func(s *coreapi.ServiceProviderCluster) {
			s.Status.ControlPlaneVersion.ActiveVersions[0].LastTransitionTime = metav1.Time{Time: time.Unix(100, 0)}
		}, minor21},
		{"newer active entry ignored", func(s *coreapi.ServiceProviderCluster) {
			s.Status.ControlPlaneVersion.ActiveVersions = append([]coreapi.HCPClusterActiveVersion{completed("4.22.0")}, s.Status.ControlPlaneVersion.ActiveVersions...)
		}, nil},
		{"versions cleared", func(s *coreapi.ServiceProviderCluster) {
			s.Status.ControlPlaneVersion.ActiveVersions = nil
			s.Spec.ControlPlaneVersion.DesiredVersion = nil
		}, all},
		{"identical resync", func(s *coreapi.ServiceProviderCluster) {}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, _, h := statusWatchFixture(t)
			old := newTestSPC("c1", v("4.21.6"), []coreapi.HCPClusterActiveVersion{completed("4.21.6")}, nil)
			updated := old.DeepCopy()
			tc.mutate(updated)
			h.OnUpdate(old, updated)
			require.ElementsMatch(t, tc.want, q.channels)
		})
	}
}

func TestStatusCollectorClusterUpdates(t *testing.T) {
	for _, tc := range []struct {
		name, group, version string
		want                 bool
	}{
		{"unchanged", "stable", "4.21", false},
		{"patch only", "stable", "4.21.5", false},
		{"minor changed", "stable", "4.22", true},
		{"group changed", "fast", "4.21", true},
		{"version cleared", "stable", "", true},
		{"group cleared", "", "4.21", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, h, _ := statusWatchFixture(t)
			old := newTestCluster("c1", "stable", "4.21")
			updated := old.DeepCopy()
			updated.CustomerProperties.Version.ChannelGroup = tc.group
			updated.CustomerProperties.Version.ID = tc.version
			updated.Tags = map[string]string{"unrelated": "change"}
			h.OnUpdate(old, updated)
			if tc.want {
				require.Len(t, q.channels, 4)
			} else {
				require.Empty(t, q.channels)
			}
		})
	}
}

func TestStatusCollectorAddAndDelete(t *testing.T) {
	for _, kind := range []string{"cluster", "serviceProviderCluster", "serviceProviderCluster unknown minor"} {
		for _, event := range []string{"add", "delete", "tombstone"} {
			t.Run(kind+"/"+event, func(t *testing.T) {
				q, clusterHandler, serviceProviderClusterHandler := statusWatchFixture(t)
				h := serviceProviderClusterHandler
				var obj any = newTestSPC("c1", v("4.21.6"), nil, nil)
				wantCount := 2
				if kind == "cluster" {
					h = clusterHandler
					obj = newTestCluster("c1", "stable", "4.21")
					wantCount = 4
				}
				if kind == "serviceProviderCluster unknown minor" {
					obj = newTestSPC("c1", nil, nil, nil)
					wantCount = 4
				}
				switch event {
				case "add":
					h.OnAdd(obj, true)
				case "delete":
					h.OnDelete(obj)
				case "tombstone":
					h.OnDelete(cache.DeletedFinalStateUnknown{Obj: obj})
				}
				require.Len(t, q.channels, wantCount)
			})
		}
	}
}
