// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package deletion

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
)

func TestRecoverPendingDeletionTarget(t *testing.T) {
	pending := metadataapi.Must(metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/clusters/pending"))
	confirmed := metadataapi.Must(metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/clusters/confirmed"))
	stamp := metav1.Unix(1000, 0)
	tests := []struct {
		name                 string
		deleting, dispatched bool
		pending, final       *metadataapi.InternalID
		recover              bool
	}{
		{name: "delete after interrupted POST", deleting: true, pending: &pending, recover: true},
		{name: "delete before POST completes", deleting: true, pending: &pending, recover: true},
		{name: "confirmed identity wins", deleting: true, pending: &pending, final: &confirmed},
		{name: "active cluster", pending: &pending},
		{name: "dispatch already recorded", deleting: true, dispatched: true, pending: &pending},
		{name: "never assigned", deleting: true},
	}
	opts := cmp.Comparer(func(left, right metadataapi.InternalID) bool { return left.String() == right.String() })
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			cluster := &coreapi.Cluster{ServiceProviderProperties: coreapi.ClusterServiceProviderProperties{ClusterUID: "aro-uid", DNS: coreapi.ServiceProviderDNSProfile{BaseDomain: "abcd.example.com"}}}
			if testCase.deleting {
				cluster.ServiceProviderProperties.DeletionTimestamp = &stamp
			}
			if testCase.dispatched {
				cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &stamp
			}
			provider := &coreapi.ServiceProviderCluster{CosmosMetadata: coreapi.CosmosMetadata{CosmosETag: "observed-etag", InstanceVersion: 4}, PendingClusterServiceID: testCase.pending, ClusterServiceID: testCase.final}
			beforeCluster, beforeProvider := cluster.DeepCopy(), provider.DeepCopy()
			got := recoverPendingDeletionTarget(cluster, provider)
			var want *coreapi.ServiceProviderCluster
			if testCase.recover {
				want = provider.DeepCopy()
				want.ClusterServiceID = want.PendingClusterServiceID
				want.PendingClusterServiceID = nil
			}
			if diff := cmp.Diff(want, got, opts); diff != "" {
				t.Fatal(diff)
			}
			if diff := cmp.Diff(beforeCluster, cluster, opts); diff != "" {
				t.Fatalf("mutated cluster: %s", diff)
			}
			if diff := cmp.Diff(beforeProvider, provider, opts); diff != "" {
				t.Fatalf("mutated provider: %s", diff)
			}
			if got != nil && recoverPendingDeletionTarget(cluster, got) != nil {
				t.Fatal("recovery must not repeat")
			}
		})
	}
}
