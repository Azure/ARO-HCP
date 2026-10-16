// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package dns

import (
	"errors"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
)

var comparison = cmp.Options{cmp.Comparer(func(a, b *azcorearm.ResourceID) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.String() == b.String()
}), cmp.Comparer(func(a, b metadataapi.InternalID) bool { return a.String() == b.String() }), cmpopts.EquateErrors()}

func testCluster(profile coreapi.ServiceProviderDNSProfile, prefix string) *coreapi.Cluster {
	id := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test"))
	return &coreapi.Cluster{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: id, PartitionKey: "sub", CosmosETag: "cluster-etag", InstanceVersion: 3}, TrackedResource: coreapi.TrackedResource{Resource: coreapi.Resource{ID: id}}, CustomerProperties: coreapi.ClusterCustomerProperties{DNS: coreapi.CustomerDNSProfile{BaseDomainPrefix: prefix}}, ServiceProviderProperties: coreapi.ClusterServiceProviderProperties{ClusterUID: "aro-uid", DNS: profile}}
}
func testClaim(now time.Time) *fleetapi.DNSClaim {
	id := metadataapi.Must(azcorearm.ParseResourceID("/providers/Microsoft.RedHatOpenShift/dnsZones/example.com/dnsClaims/abcde"))
	return &fleetapi.DNSClaim{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: id, PartitionKey: "example.com"}, BaseDomain: "abcde.example.com", Owner: "aro-uid", ClusterResourceID: testCluster(coreapi.ServiceProviderDNSProfile{}, "").ID.String(), CreatedAt: metav1.NewTime(now)}
}

func TestDecideAllocation(t *testing.T) {
	now := time.Unix(10000, 0)
	pending := coreapi.ServiceProviderDNSProfile{PendingBaseDomain: "abcde.example.com"}
	committed := coreapi.ServiceProviderDNSProfile{BaseDomain: "abcde.example.com"}
	owned := testClaim(now)
	other := owned.DeepCopy()
	other.Owner = "other-uid"
	failure := errors.New("injected")
	tests := []struct {
		name    string
		input   *coreapi.Cluster
		claim   *fleetapi.DNSClaim
		readErr error
		want    decision
	}{
		{name: "default prefix and persist intent", input: testCluster(coreapi.ServiceProviderDNSProfile{}, ""), want: decision{Action: writeCluster, Cluster: testCluster(pending, "cluster")}},
		{name: "honor customer prefix", input: testCluster(coreapi.ServiceProviderDNSProfile{}, "payments"), want: decision{Action: writeCluster, Cluster: testCluster(pending, "payments")}},
		{name: "create claim with ARO UID and creation time", input: testCluster(pending, "cluster"), want: decision{Action: createClaim, Claim: testClaim(now)}},
		{name: "read failure", input: testCluster(pending, "cluster"), readErr: failure, want: decision{Action: noWrite, Err: failure}},
		{name: "observed owned claim commits", input: testCluster(pending, "cluster"), claim: owned, want: decision{Action: writeCluster, Cluster: testCluster(committed, "cluster")}},
		{name: "collision replaces candidate", input: testCluster(coreapi.ServiceProviderDNSProfile{PendingBaseDomain: "taken.example.com"}, "cluster"), claim: other, want: decision{Action: writeCluster, Cluster: testCluster(pending, "cluster")}},
		{name: "already committed", input: testCluster(committed, "cluster"), claim: owned, want: decision{Action: noWrite}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := tt.input.DeepCopy()
			got := decideAllocation(logr.Discard(), tt.input, "example.com", now, func(string) (*fleetapi.DNSClaim, error) { return tt.claim, tt.readErr }, func() string { return "abcde" })
			got.validate()
			if diff := cmp.Diff(tt.want, got, comparison...); diff != "" {
				t.Fatal(diff)
			}
			if diff := cmp.Diff(before, tt.input, comparison...); diff != "" {
				t.Fatal(diff)
			}
		})
	}
}

func TestDecideCleanup(t *testing.T) {
	now := time.Unix(10000, 0)
	matching := testCluster(coreapi.ServiceProviderDNSProfile{}, "cluster")
	replacement := matching.DeepCopy()
	replacement.ServiceProviderProperties.ClusterUID = "new-uid"
	unknown := matching.DeepCopy()
	unknown.ServiceProviderProperties.ClusterUID = ""
	deleting := matching.DeepCopy()
	stamp := metav1.NewTime(now)
	deleting.ServiceProviderProperties.DeletionTimestamp = &stamp
	completed := deleting.DeepCopy()
	completed.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &stamp
	csPending := completed.DeepCopy()
	pending := metadataapi.Must(metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/clusters/pending"))
	csPending.ServiceProviderProperties.PendingClusterServiceID = &pending
	failure := errors.New("cache failure")
	tests := []struct {
		name       string
		parent     *coreapi.Cluster
		age        time.Duration
		err        error
		wantAction action
		delay      time.Duration
	}{
		{name: "active owner", parent: matching, age: time.Hour, wantAction: noWrite},
		{name: "unknown parent UID", parent: unknown, age: time.Hour, wantAction: noWrite},
		{name: "deletion only", parent: deleting, age: time.Hour, wantAction: noWrite},
		{name: "CS cleanup done no age wait", parent: completed, age: time.Second, wantAction: deleteClaim},
		{name: "CS pending still blocks", parent: csPending, age: time.Hour, wantAction: noWrite},
		{name: "absent young", age: time.Minute, wantAction: noWrite, delay: 29 * time.Minute},
		{name: "absent before boundary", age: 30*time.Minute - time.Nanosecond, wantAction: noWrite, delay: time.Nanosecond},
		{name: "absent exact boundary", age: 30 * time.Minute, wantAction: deleteClaim},
		{name: "different UID young", parent: replacement, age: time.Minute, wantAction: noWrite, delay: 29 * time.Minute},
		{name: "different UID old", parent: replacement, age: time.Hour, wantAction: deleteClaim},
		{name: "future timestamp", age: -time.Minute, wantAction: noWrite, delay: 31 * time.Minute},
		{name: "observation failure", err: failure, age: time.Hour, wantAction: noWrite},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claim := testClaim(now.Add(-tt.age))
			got := decideCleanup(logr.Discard(), claim, tt.parent, tt.err, now)
			got.validate()
			want := decision{Action: tt.wantAction, RequeueAfter: tt.delay, Err: tt.err}
			if tt.wantAction == deleteClaim {
				want.Claim = claim
				if got.Claim != claim {
					t.Fatal("delete copied read-only claim")
				}
			}
			if diff := cmp.Diff(want, got, comparison...); diff != "" {
				t.Fatal(diff)
			}
		})
	}
}

func TestCleanupDecisionPreservesObservedOwnerAndETag(t *testing.T) {
	now := time.Unix(10000, 0)
	oldClaim := testClaim(now.Add(-time.Hour))
	oldClaim.CosmosETag = "old-etag"
	replacementClaim := oldClaim.DeepCopy()
	replacementClaim.Owner = "replacement-uid"
	replacementClaim.CosmosETag = "new-etag"
	replacementCluster := testCluster(coreapi.ServiceProviderDNSProfile{BaseDomain: oldClaim.BaseDomain}, "cluster")
	replacementCluster.ServiceProviderProperties.ClusterUID = "replacement-uid"
	stale := decideCleanup(logr.Discard(), oldClaim, nil, nil, now)
	want := decision{Action: deleteClaim, Claim: oldClaim}
	if diff := cmp.Diff(want, stale, comparison...); diff != "" {
		t.Fatal(diff)
	}
	// The decision must retain the stale ETag, never manufacture a delete targeting
	// the new owner. Enforcing that precondition is Cosmos's responsibility.
	fresh := decideCleanup(logr.Discard(), replacementClaim, replacementCluster, nil, now)
	if diff := cmp.Diff(decision{Action: noWrite}, fresh, comparison...); diff != "" {
		t.Fatal(diff)
	}
}

func TestAllocationEligibilityIgnoresIrrelevantReadErrors(t *testing.T) {
	failure := errors.New("unavailable lister")
	for _, state := range []string{"deleting", "already-created", "no-uid", "no-pending-id"} {
		t.Run(state, func(t *testing.T) {
			cluster := testCluster(coreapi.ServiceProviderDNSProfile{}, "")
			id := metadataapi.Must(metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/clusters/id"))
			cluster.ServiceProviderProperties.PendingClusterServiceID = &id
			switch state {
			case "deleting":
				stamp := metav1.Now()
				cluster.ServiceProviderProperties.DeletionTimestamp = &stamp
			case "already-created":
				cluster.ServiceProviderProperties.ClusterServiceID = &id
			case "no-uid":
				cluster.ServiceProviderProperties.ClusterUID = ""
			case "no-pending-id":
				cluster.ServiceProviderProperties.PendingClusterServiceID = nil
			}
			got := decideAllocationInputs(logr.Discard(), cluster, nil, failure, nil, failure, time.Time{}, nil, nil, nil, nil)
			if diff := cmp.Diff(decision{Action: noWrite}, got); diff != "" {
				t.Fatal(diff)
			}
		})
	}
}
