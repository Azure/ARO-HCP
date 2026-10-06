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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
)

type getOnlyClusterLister struct{ corelisters.ClusterLister }

func (getOnlyClusterLister) List(context.Context) ([]*coreapi.Cluster, error) {
	panic("event must not scan clusters")
}

type getOnlySPCLister struct {
	corelisters.ServiceProviderClusterLister
}

func (getOnlySPCLister) List(context.Context) ([]*coreapi.ServiceProviderCluster, error) {
	panic("event must not scan SPCs")
}

func TestReferenceWatchesMapAllReferences(t *testing.T) {
	cluster := newTestCluster("c1", "nightly", "4.21.0-0.nightly")
	cluster.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: statusTestNow}
	cluster.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneExactVersion = v("4.18.1")
	cluster.Status.ActiveVersions = []coreapi.HCPClusterActiveVersion{{Version: "4.17.1"}}
	spc := newTestServiceProviderCluster("c1", v("4.19.1"), []coreapi.ServiceProviderClusterActiveVersion{completed("4.16.1")}, nil)
	spc.Spec.PinnedVersion.ExactVersion = v("4.15.1")
	clusterInformer, spcInformer := &candidateNotifier{}, &candidateNotifier{}
	queue := &candidateQueue{}
	require.NoError(t, watchRolloutReferences(clusterInformer, spcInformer,
		getOnlyClusterLister{&corelistertesting.SliceClusterLister{Clusters: []*coreapi.Cluster{cluster}}},
		getOnlySPCLister{&corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{spc}}},
		queue, RolloutSeedingControllerName, 5*time.Minute))
	want := []string{"nightly-4.21", "nightly-4.19", "nightly-4.18", "nightly-4.17", "nightly-4.16", "nightly-4.15"}
	for _, notifier := range []*candidateNotifier{clusterInformer, spcInformer} {
		require.NotNil(t, notifier.options.ResyncPeriod)
		require.Equal(t, 5*time.Minute, *notifier.options.ResyncPeriod)
		require.NotNil(t, notifier.options.Logger)
	}
	for _, obj := range []any{cluster, spc} {
		for _, event := range []string{"add", "resync", "delete", "tombstone"} {
			t.Run(event, func(t *testing.T) {
				queue.channels = nil
				h := clusterInformer.handler
				if obj == spc {
					h = spcInformer.handler
				}
				switch event {
				case "add":
					h.OnAdd(obj, false)
				case "resync":
					h.OnUpdate(obj, obj)
				case "delete":
					h.OnDelete(obj)
				case "tombstone":
					h.OnDelete(cache.DeletedFinalStateUnknown{Obj: obj})
				}
				require.ElementsMatch(t, want, sets.New(queue.channels...).UnsortedList(), "every dependency maps regardless of floor, deletion or rollout existence")
			})
		}
	}
	queue.channels = nil
	updated := cluster.DeepCopy()
	updated.CustomerProperties.Version.ChannelGroup = "fast"
	clusterInformer.handler.OnUpdate(cluster, updated)
	require.Contains(t, queue.channels, "nightly-4.19", "old group is mapped")
	require.Contains(t, queue.channels, "fast-4.19", "new group is mapped")
	queue.channels = nil
	updatedSPC := spc.DeepCopy()
	updatedSPC.Spec.ControlPlaneVersion.DesiredVersion = v("4.14.1")
	spcInformer.handler.OnUpdate(spc, updatedSPC)
	require.Contains(t, queue.channels, "nightly-4.19", "old desired version is mapped")
	require.Contains(t, queue.channels, "nightly-4.14", "new desired version is mapped")
	queue.channels = nil
	updated = cluster.DeepCopy()
	updated.Tags = map[string]string{"unrelated": "change"}
	clusterInformer.handler.OnUpdate(cluster, updated)
	require.ElementsMatch(t, want, sets.New(queue.channels...).UnsortedList(), "unrelated updates must still map dependencies")
}

type failingSPCGet struct {
	corelisters.ServiceProviderClusterLister
}

func (failingSPCGet) Get(context.Context, string, string, string) (*coreapi.ServiceProviderCluster, error) {
	return nil, errors.New("SPC lookup failed")
}

func TestReferenceWatchErrorsAndMissingCounterpart(t *testing.T) {
	cluster := newTestCluster("c1", "stable", "4.21")
	spc := newTestServiceProviderCluster("c1", v("4.19.1"), nil, nil)
	clusters := &corelistertesting.SliceClusterLister{}
	spcs := &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{spc}}
	queue := &candidateQueue{}
	require.Error(t, enqueueRolloutReferences(t.Context(), spc, clusters, spcs, queue), "missing parent is reported")
	require.Empty(t, queue.channels)
	clusterInformer, spcInformer := &candidateNotifier{}, &candidateNotifier{}
	require.NoError(t, watchRolloutReferences(clusterInformer, spcInformer, clusters, spcs, queue, RolloutSeedingControllerName, 5*time.Minute))
	spcInformer.handler.OnAdd(spc, false)
	clusters.Clusters = []*coreapi.Cluster{cluster}
	spcInformer.handler.OnUpdate(spc, spc)
	require.ElementsMatch(t, []string{"stable-4.21", "stable-4.19"}, sets.New(queue.channels...).UnsortedList(), "unchanged resync recovers a previously missing parent")
	queue.channels = nil
	clusterInformer.handler.OnAdd(cluster, false)
	require.ElementsMatch(t, []string{"stable-4.21", "stable-4.19"}, queue.channels, "parent arrival maps the counterpart's references")
	queue.channels = nil
	require.ErrorContains(t, enqueueRolloutReferences(t.Context(), cluster, clusters, failingSPCGet{}, queue), "SPC lookup failed")
	require.Equal(t, []string{"stable-4.21"}, queue.channels, "known references survive lookup failure")
	queue.channels = nil
	require.NoError(t, enqueueRolloutReferences(t.Context(), cluster, clusters, &corelistertesting.SliceServiceProviderClusterLister{}, queue), "absent SPC does not prevent cluster mapping")
	require.Equal(t, []string{"stable-4.21"}, queue.channels)
	for _, obj := range []any{&coreapi.Cluster{}, &coreapi.ServiceProviderCluster{}, cache.DeletedFinalStateUnknown{Obj: "invalid"}} {
		require.Error(t, enqueueRolloutReferences(t.Context(), obj, clusters, spcs, queue))
	}
}
