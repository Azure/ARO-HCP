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
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
)

func TestStatusCollectorWatches(t *testing.T) {
	cluster := newTestCluster("c1", "stable", "4.21")
	cluster.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: statusTestNow}
	serviceProviderCluster := newTestServiceProviderCluster("c1", v("4.20.1"), []coreapi.ServiceProviderClusterActiveVersion{completed("4.19.1"), partial("4.18.1")}, nil)
	serviceProviderCluster.Spec.PinnedVersion.ExactVersion = v("4.17.1")
	c := &statusCollectorSyncer{
		clusterLister:                &corelistertesting.SliceClusterLister{Clusters: []*coreapi.Cluster{cluster}},
		serviceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{serviceProviderCluster}},
	}
	q := &candidateQueue{}
	clusters, serviceProviderClusters := &candidateNotifier{}, &candidateNotifier{}
	require.NoError(t, c.watchStatusInputs(clusters, serviceProviderClusters, q))
	want := []string{"stable-4.21", "stable-4.20", "stable-4.19", "stable-4.18", "stable-4.17"}
	for _, notifier := range []*candidateNotifier{clusters, serviceProviderClusters} {
		require.NotNil(t, notifier.options.ResyncPeriod)
		require.Equal(t, 5*time.Minute, *notifier.options.ResyncPeriod)
		var obj any = cluster
		if notifier == serviceProviderClusters {
			obj = serviceProviderCluster
		}
		for _, event := range []string{"add", "update", "delete", "tombstone"} {
			q.channels = nil
			switch event {
			case "add":
				notifier.handler.OnAdd(obj, false)
			case "update":
				notifier.handler.OnUpdate(obj, obj)
			case "delete":
				notifier.handler.OnDelete(obj)
			case "tombstone":
				notifier.handler.OnDelete(cache.DeletedFinalStateUnknown{Obj: obj})
			}
			require.ElementsMatch(t, want, sets.New(q.channels...).UnsortedList(), "%s must enqueue every reference, including deleting clusters", event)
		}
	}
	for _, mutate := range []func(*coreapi.ServiceProviderCluster){
		func(serviceProviderCluster *coreapi.ServiceProviderCluster) {
			serviceProviderCluster.Spec.PinnedVersion.ExactVersion = v("4.16.1")
		},
		func(serviceProviderCluster *coreapi.ServiceProviderCluster) {
			serviceProviderCluster.Status.ControlPlaneVersion.ActiveVersions[1].Version = v("4.16.1")
		},
		func(serviceProviderCluster *coreapi.ServiceProviderCluster) {
			serviceProviderCluster.Spec.ControlPlaneVersion.DesiredVersionLastTransitionTime = &metav1.Time{Time: statusTestNow}
		},
		func(serviceProviderCluster *coreapi.ServiceProviderCluster) {
			serviceProviderCluster.Status.ControlPlaneVersion.ActiveVersions[0].LastTransitionTime = metav1.Time{Time: statusTestNow}
		},
	} {
		q.channels = nil
		updated := serviceProviderCluster.DeepCopy()
		mutate(updated)
		serviceProviderClusters.handler.OnUpdate(serviceProviderCluster, updated)
		for _, channel := range want {
			require.Contains(t, q.channels, channel, "old references and progress inputs must enqueue")
		}
		if updated.Spec.PinnedVersion.ExactVersion.String() == "4.16.1" || updated.Status.ControlPlaneVersion.ActiveVersions[1].Version.String() == "4.16.1" {
			require.Contains(t, q.channels, "stable-4.16", "new pin and active references must enqueue")
		}
	}
}
