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
	deleting := metav1.Unix(1000, 0)
	tests := []struct {
		name  string
		input coreapi.ClusterServiceProviderProperties
		want  *coreapi.ClusterServiceProviderProperties
	}{
		{name: "accepted POST with lost response", input: coreapi.ClusterServiceProviderProperties{DeletionTimestamp: &deleting, PendingClusterServiceID: &pending}, want: &coreapi.ClusterServiceProviderProperties{DeletionTimestamp: &deleting, ClusterServiceID: &pending}},
		{name: "failed identity persistence preserves other state", input: coreapi.ClusterServiceProviderProperties{DeletionTimestamp: &deleting, PendingClusterServiceID: &pending, ClusterUID: "aro-uid", DNS: coreapi.ServiceProviderDNSProfile{BaseDomain: "abcd.example.com"}}, want: &coreapi.ClusterServiceProviderProperties{DeletionTimestamp: &deleting, ClusterServiceID: &pending, ClusterUID: "aro-uid", DNS: coreapi.ServiceProviderDNSProfile{BaseDomain: "abcd.example.com"}}},
		{name: "delete before POST completes", input: coreapi.ClusterServiceProviderProperties{DeletionTimestamp: &deleting, PendingClusterServiceID: &pending}, want: &coreapi.ClusterServiceProviderProperties{DeletionTimestamp: &deleting, ClusterServiceID: &pending}},
		{name: "confirmed identity wins", input: coreapi.ClusterServiceProviderProperties{DeletionTimestamp: &deleting, PendingClusterServiceID: &pending, ClusterServiceID: &confirmed}},
		{name: "active cluster", input: coreapi.ClusterServiceProviderProperties{PendingClusterServiceID: &pending}},
		{name: "dispatch already recorded", input: coreapi.ClusterServiceProviderProperties{DeletionTimestamp: &deleting, ClusterServiceDeletionTimestamp: &deleting, PendingClusterServiceID: &pending}},
		{name: "never assigned", input: coreapi.ClusterServiceProviderProperties{DeletionTimestamp: &deleting}},
	}
	opts := cmp.Comparer(func(a, b metadataapi.InternalID) bool { return a.String() == b.String() })
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := &coreapi.Cluster{CosmosMetadata: coreapi.CosmosMetadata{CosmosETag: "observed-etag", InstanceVersion: 4}, ServiceProviderProperties: tt.input}
			before := input.DeepCopy()
			got := recoverPendingDeletionTarget(input)
			var want *coreapi.Cluster
			if tt.want != nil {
				want = &coreapi.Cluster{CosmosMetadata: coreapi.CosmosMetadata{CosmosETag: "observed-etag", InstanceVersion: 4}, ServiceProviderProperties: *tt.want}
			}
			if diff := cmp.Diff(want, got, opts); diff != "" {
				t.Fatal(diff)
			}
			if diff := cmp.Diff(before, input, opts); diff != "" {
				t.Fatalf("mutated observation: %s", diff)
			}
			if got != nil && recoverPendingDeletionTarget(got) != nil {
				t.Fatal("recovery should not repeat once target is persisted")
			}
		})
	}
}
