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

package placement

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/fleetcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/fleetlistertesting"
	"github.com/Azure/ARO-HCP/internal/kuberesources"
)

// mcForStamp builds an eligible/ineligible ManagementCluster for a stamp.
func mcForStamp(stamp string, schedulable, ready bool) *fleetapi.ManagementCluster {
	resourceID := metadataapi.Must(fleetapi.ToManagementClusterResourceID(stamp))
	policy := fleetapi.ManagementClusterSchedulingPolicyUnschedulable
	if schedulable {
		policy = fleetapi.ManagementClusterSchedulingPolicySchedulable
	}
	readyStatus := metav1.ConditionFalse
	if ready {
		readyStatus = metav1.ConditionTrue
	}
	return &fleetapi.ManagementCluster{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: resourceID, PartitionKey: strings.ToLower(stamp)},
		Spec:           fleetapi.ManagementClusterSpec{SchedulingPolicy: policy},
		Status: fleetapi.ManagementClusterStatus{
			Conditions: []metav1.Condition{{Type: string(fleetapi.ManagementClusterConditionReady), Status: readyStatus, Reason: "Test"}},
		},
	}
}

// swiftResourceList returns a ResourceList with the given swift-NIC quantity, or
// nil when count < 0 (to model absent capacity data).
func swiftResourceList(count int64) corev1.ResourceList {
	if count < 0 {
		return nil
	}
	return corev1.ResourceList{kuberesources.SwiftNICResourceName: *resource.NewQuantity(count, resource.DecimalSI)}
}

// dummyResourceIDs builds n distinct HCP-cluster resource IDs (for NotReady /
// Pending list length only; the exact values do not matter for capacity math).
func dummyResourceIDs(n int) []*azcorearm.ResourceID {
	ids := make([]*azcorearm.ResourceID, 0, n)
	for i := 0; i < n; i++ {
		ids = append(ids, metadataapi.Must(azcorearm.ParseResourceID(
			fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/pending-%d",
				testClusterSubscriptionID, testClusterResourceGroup, i))))
	}
	return ids
}

// clusterResourceIDWithName builds an HCP-cluster ARM resource ID with the given
// cluster name (the last segment); other segments are fixed test values.
func clusterResourceIDWithName(name string) *azcorearm.ResourceID {
	return metadataapi.Must(azcorearm.ParseResourceID(
		fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/%s",
			testClusterSubscriptionID, testClusterResourceGroup, name)))
}

// namedClusterResourceIDs builds n HCP-cluster resource IDs named "<prefix>-<i>".
func namedClusterResourceIDs(prefix string, n int) []*azcorearm.ResourceID {
	ids := make([]*azcorearm.ResourceID, 0, n)
	for i := 0; i < n; i++ {
		ids = append(ids, clusterResourceIDWithName(fmt.Sprintf("%s-%d", prefix, i)))
	}
	return ids
}

// clusterWithAvailability builds an HCPOpenShiftCluster with the given name and
// control-plane availability, for the informer-cache-backed swiftNICReserver.
func clusterWithAvailability(name string, availability coreapi.ControlPlaneAvailability) *coreapi.HCPOpenShiftCluster {
	rid := clusterResourceIDWithName(name)
	cluster := &coreapi.HCPOpenShiftCluster{}
	cluster.ID = rid
	cluster.Name = rid.Name
	cluster.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneAvailability = availability
	return cluster
}

// clusterListerForAvailability builds a cluster lister seeded with one
// HCPOpenShiftCluster per resource ID, inferring each cluster's control-plane
// availability from its name: "sr-*" clusters are SingleReplica (reserve 1),
// every other cluster is highly available (reserve swiftNICsPerHCP). This lets
// availableResources resolve per-cluster swift-NIC reservations from the cache.
func clusterListerForAvailability(ids []*azcorearm.ResourceID) *corelistertesting.SliceClusterLister {
	clusters := make([]*coreapi.HCPOpenShiftCluster, 0, len(ids))
	for _, id := range ids {
		availability := coreapi.DefaultControlPlaneAvailability
		if strings.HasPrefix(id.Name, "sr-") {
			availability = coreapi.SingleReplicaControlPlane
		}
		clusters = append(clusters, clusterWithAvailability(id.Name, availability))
	}
	return &corelistertesting.SliceClusterLister{Clusters: clusters}
}

func schedulingDoc(stamp string, ceiling, usage, notReady, pending int64) *fleetapi.ManagementClusterScheduling {
	resourceID := metadataapi.Must(fleetapi.ToManagementClusterSchedulingResourceID(stamp))
	notReadyIDs := make([]*azcorearm.ResourceID, notReady)
	for i := range notReadyIDs {
		notReadyIDs[i] = clusterResourceIDWithName(fmt.Sprintf("nr-%s-%d", stamp, i))
	}
	return &fleetapi.ManagementClusterScheduling{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: resourceID, PartitionKey: strings.ToLower(stamp)},
		Status: fleetapi.ManagementClusterSchedulingStatus{
			Conditions: []metav1.Condition{
				{Type: fleetapi.ConditionTypeCapacityDataCurrent, Status: metav1.ConditionTrue, Reason: "Test"},
				{Type: fleetapi.ConditionTypeScalingDataCurrent, Status: metav1.ConditionTrue, Reason: "Test"},
			},
			ObservedResources:       fleetapi.ObservedResources{Usage: swiftResourceList(usage)},
			ScaleCeiling:            fleetapi.ScaleCeiling{Capacity: swiftResourceList(ceiling)},
			NotReadyResourceIDs:     notReadyIDs,
			PendingAssignedClusters: dummyResourceIDs(int(pending)),
		},
	}
}

func TestAvailableResources(t *testing.T) {
	tests := []struct {
		name     string
		ceiling  int64
		usage    int64
		notReady int64
		pending  int64
		expected int64
	}{
		{name: "empty capacity data => 0", ceiling: -1, usage: -1, expected: 0},
		{name: "ceiling only", ceiling: 9, expected: 9},
		{name: "usage subtracted", ceiling: 9, usage: 3, expected: 6},
		{name: "notReady eats slack (3 each)", ceiling: 9, usage: 0, notReady: 2, expected: 3},
		{name: "pending reserved (3 each)", ceiling: 9, usage: 0, pending: 2, expected: 3},
		{name: "all combined", ceiling: 30, usage: 6, notReady: 2, pending: 1, expected: 30 - 6 - 6 - 3},
		{name: "can go negative when overcommitted", ceiling: 3, usage: 0, notReady: 2, expected: 3 - 6},
	}
	// An empty cluster lister makes every NotReady/Pending entry unresolvable, so
	// availableResources falls back to the conservative swiftNICsPerHCP (3) per
	// entry — the pre-SingleReplica "3 each" behaviour.
	syncer := &placementSyncer{clusterLister: &corelistertesting.SliceClusterLister{}}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			doc := schedulingDoc("s", tc.ceiling, tc.usage, tc.notReady, tc.pending)
			assert.Equal(t, tc.expected, swiftNICCount(syncer.availableResources(context.Background(), doc)))
		})
	}
}

// TestAvailableResources_MixedControlPlaneAvailability verifies the headroom math
// reserves per-cluster swift NICs (1 for SingleReplica, else swiftNICsPerHCP) for
// each pending / not-ready HCP, rather than assuming the full swiftNICsPerHCP for
// every entry. Each pending/not-ready cluster is resolved via the cluster lister.
func TestAvailableResources_MixedControlPlaneAvailability(t *testing.T) {
	tests := []struct {
		name       string
		ceiling    int64
		usage      int64
		haPending  int // highly-available pending clusters (reserve swiftNICsPerHCP each)
		srPending  int // single-replica pending clusters (reserve 1 each)
		haNotReady int // highly-available not-ready clusters (reserve swiftNICsPerHCP each)
		srNotReady int // single-replica not-ready clusters (reserve 1 each)
		expected   int64
	}{
		{name: "single-replica pending reserve 1 each", ceiling: 9, srPending: 3, expected: 9 - 3},
		{name: "highly-available pending reserve 3 each", ceiling: 9, haPending: 2, expected: 9 - 6},
		{name: "mixed pending", ceiling: 20, haPending: 2, srPending: 3, expected: 20 - 6 - 3},
		{name: "mixed not-ready", ceiling: 20, haNotReady: 2, srNotReady: 4, expected: 20 - 6 - 4},
		{
			name:      "mixed pending and not-ready with usage",
			ceiling:   30,
			usage:     3,
			haPending: 1, srPending: 2, haNotReady: 1, srNotReady: 2,
			expected: 30 - 3 - (3 + 2) - (3 + 2),
		},
		{name: "all single-replica", ceiling: 10, srPending: 4, srNotReady: 2, expected: 10 - 4 - 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pending := append(namedClusterResourceIDs("ha-p", tc.haPending), namedClusterResourceIDs("sr-p", tc.srPending)...)
			notReady := append(namedClusterResourceIDs("ha-nr", tc.haNotReady), namedClusterResourceIDs("sr-nr", tc.srNotReady)...)
			doc := &fleetapi.ManagementClusterScheduling{
				Status: fleetapi.ManagementClusterSchedulingStatus{
					ObservedResources:       fleetapi.ObservedResources{Usage: swiftResourceList(tc.usage)},
					ScaleCeiling:            fleetapi.ScaleCeiling{Capacity: swiftResourceList(tc.ceiling)},
					PendingAssignedClusters: pending,
					NotReadyResourceIDs:     notReady,
				},
			}
			// Seed the cluster lister with every referenced cluster so each resolves
			// to its control-plane availability (sr-* => SingleReplica, else HA).
			all := make([]*azcorearm.ResourceID, 0, len(pending)+len(notReady))
			all = append(all, pending...)
			all = append(all, notReady...)
			syncer := &placementSyncer{clusterLister: clusterListerForAvailability(all)}
			assert.Equal(t, tc.expected, swiftNICCount(syncer.availableResources(context.Background(), doc)))
		})
	}
}

// TestSwiftNICsForResourceID verifies the informer-cache-backed resolver maps a
// cluster's control-plane availability to its swift-NIC reservation and falls
// back to the conservative swiftNICsPerHCP when the cluster cannot be resolved.
func TestSwiftNICsForResourceID(t *testing.T) {
	haCluster := clusterWithAvailability("ha", coreapi.DefaultControlPlaneAvailability)
	srCluster := clusterWithAvailability("sr", coreapi.SingleReplicaControlPlane)

	syncer := &placementSyncer{
		clusterLister: &corelistertesting.SliceClusterLister{
			Clusters: []*coreapi.HCPOpenShiftCluster{haCluster, srCluster},
		},
	}
	ctx := context.Background()

	assert.Equal(t, swiftNICsPerHCP, syncer.swiftNICsForResourceID(ctx, haCluster.ID), "highly-available cluster reserves the full swiftNICsPerHCP")
	assert.Equal(t, singleReplicaSwiftNICsPerHCP, syncer.swiftNICsForResourceID(ctx, srCluster.ID), "single-replica cluster reserves 1")
	assert.Equal(t, swiftNICsPerHCP, syncer.swiftNICsForResourceID(ctx, clusterResourceIDWithName("missing")), "unresolvable cluster falls back to swiftNICsPerHCP")
	assert.Equal(t, swiftNICsPerHCP, syncer.swiftNICsForResourceID(ctx, nil), "nil resource ID falls back to swiftNICsPerHCP")
}

func TestAvailableResources_IgnoresNilEntries(t *testing.T) {
	doc := &fleetapi.ManagementClusterScheduling{
		Status: fleetapi.ManagementClusterSchedulingStatus{
			ScaleCeiling: fleetapi.ScaleCeiling{Capacity: swiftResourceList(9)},
			// One nil entry (must not reserve) + one real entry (reserves 3).
			PendingAssignedClusters: []*azcorearm.ResourceID{
				nil,
				metadataapi.Must(fleetapi.ToManagementClusterResourceID("x")),
			},
			// One nil entry (must not reserve) + one real entry (reserves 3).
			NotReadyResourceIDs: []*azcorearm.ResourceID{
				nil,
				metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/s/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/nr")),
			},
		},
	}
	// 9 - usage(0) - notReady(1*3) - pending(1*3) = 3
	syncer := &placementSyncer{clusterLister: &corelistertesting.SliceClusterLister{}}
	assert.Equal(t, int64(3), swiftNICCount(syncer.availableResources(context.Background(), doc)))
}

// eligibleCandidate builds an eligible managementClusterEvaluation whose
// resolved available capacity is `available` swift NICs.
func eligibleCandidate(stamp string, available int64) managementClusterEvaluation {
	return managementClusterEvaluation{
		resourceID:         metadataapi.Must(fleetapi.ToManagementClusterResourceID(stamp)),
		eligibility:        eligible,
		availableResources: swiftResourceList(available),
	}
}

// ineligibleCandidate builds a managementClusterEvaluation that selectByCapacity
// must eliminate, recording the given reason.
func ineligibleCandidate(stamp, reason string) managementClusterEvaluation {
	return managementClusterEvaluation{
		resourceID:  metadataapi.Must(fleetapi.ToManagementClusterResourceID(stamp)),
		eligibility: ineligible,
		reason:      reason,
	}
}

func unknownCandidate(stamp string) managementClusterEvaluation {
	candidate := ineligibleCandidate(stamp, "no scheduling/capacity data available")
	candidate.eligibility = unknownEligibility
	return candidate
}

// TestSelectByCapacity exercises the pure selection function on already-resolved
// candidates: eligibility and available capacity are inputs, so the cases
// cover both the ineligible/unknown-reason passthrough and capacity-based
// spread/tie-breaking.
func TestSelectByCapacity(t *testing.T) {
	rid := func(stamp string) *azcorearm.ResourceID {
		return metadataapi.Must(fleetapi.ToManagementClusterResourceID(stamp))
	}

	tests := []struct {
		name              string
		candidates        []managementClusterEvaluation
		requiredSwiftNICs int64  // swift NICs the new HCP needs; 0 => swiftNICsPerHCP
		expectedStamp     string // set when a fit is expected
		expectBlocked     bool   // true when no fit is expected
		expectUnknown     bool
		expectedReason    string
		messageContains   string
	}{
		{name: "no candidates - blocked, no eligible MC", candidates: nil, expectBlocked: true, expectedReason: coreapi.CapacityReasonNoEligibleManagementCluster},
		{
			name:            "not schedulable - blocked, no eligible MC",
			candidates:      []managementClusterEvaluation{ineligibleCandidate("1", `scheduling policy is "Unschedulable", not "Schedulable"`)},
			expectBlocked:   true,
			expectedReason:  coreapi.CapacityReasonNoEligibleManagementCluster,
			messageContains: "scheduling policy",
		},
		{
			name:            "not ready - blocked, no eligible MC",
			candidates:      []managementClusterEvaluation{ineligibleCandidate("1", "management cluster is not Ready")},
			expectBlocked:   true,
			expectedReason:  coreapi.CapacityReasonNoEligibleManagementCluster,
			messageContains: "not Ready",
		},
		{
			name:            "no scheduling data - availability unknown",
			candidates:      []managementClusterEvaluation{unknownCandidate("1")},
			expectBlocked:   true,
			expectUnknown:   true,
			expectedReason:  coreapi.CapacityReasonEvaluationIncomplete,
			messageContains: "no scheduling/capacity data",
		},
		{
			name:           "unknown candidate prevents declaring capacity exhausted",
			candidates:     []managementClusterEvaluation{eligibleCandidate("1", 0), unknownCandidate("2")},
			expectBlocked:  true,
			expectUnknown:  true,
			expectedReason: coreapi.CapacityReasonEvaluationIncomplete,
		},
		{
			name:          "known fit wins despite unknown candidate",
			candidates:    []managementClusterEvaluation{unknownCandidate("1"), eligibleCandidate("2", 3)},
			expectedStamp: "2",
		},
		{
			name:            "eligible but below threshold - blocked, insufficient capacity",
			candidates:      []managementClusterEvaluation{eligibleCandidate("1", 2)},
			expectBlocked:   true,
			expectedReason:  coreapi.CapacityReasonInsufficientCapacity,
			messageContains: "insufficient swift-NIC capacity",
		},
		{name: "exactly at threshold fits", candidates: []managementClusterEvaluation{eligibleCandidate("1", 3)}, expectedStamp: "1"},
		{
			name:          "highest available among fits (spread load)",
			candidates:    []managementClusterEvaluation{eligibleCandidate("1", 9), eligibleCandidate("2", 3), eligibleCandidate("3", 6)},
			expectedStamp: "1",
		},
		{
			name:          "skips those below threshold, picks highest fitting",
			candidates:    []managementClusterEvaluation{eligibleCandidate("1", 2), eligibleCandidate("2", 5), eligibleCandidate("3", 4)},
			expectedStamp: "2",
		},
		{
			name:          "tie on available - lowest resource ID wins (order independent)",
			candidates:    []managementClusterEvaluation{eligibleCandidate("3", 3), eligibleCandidate("1", 3), eligibleCandidate("2", 3)},
			expectedStamp: "1",
		},
		{
			name: "mix of ineligible and eligible - picks the eligible fit",
			candidates: []managementClusterEvaluation{
				ineligibleCandidate("1", "management cluster is not Ready"),
				eligibleCandidate("2", 3),
			},
			expectedStamp: "2",
		},
		{
			name:              "single-replica new cluster fits with only 1 available",
			candidates:        []managementClusterEvaluation{eligibleCandidate("1", 1)},
			requiredSwiftNICs: singleReplicaSwiftNICsPerHCP,
			expectedStamp:     "1",
		},
		{
			name:              "single-replica new cluster blocked when 0 available",
			candidates:        []managementClusterEvaluation{eligibleCandidate("1", 0)},
			requiredSwiftNICs: singleReplicaSwiftNICsPerHCP,
			expectBlocked:     true,
			expectedReason:    coreapi.CapacityReasonInsufficientCapacity,
			messageContains:   "insufficient swift-NIC capacity",
		},
		{
			name:              "highly-available new cluster blocked when only 1 available",
			candidates:        []managementClusterEvaluation{eligibleCandidate("1", 1)},
			requiredSwiftNICs: swiftNICsPerHCP,
			expectBlocked:     true,
			expectedReason:    coreapi.CapacityReasonInsufficientCapacity,
			messageContains:   "insufficient swift-NIC capacity",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requiredSwiftNICs := tc.requiredSwiftNICs
			if requiredSwiftNICs == 0 {
				requiredSwiftNICs = swiftNICsPerHCP
			}
			chosen, condition := selectByCapacity(tc.candidates, requiredSwiftNICs)
			assert.Equal(t, coreapi.CapacityAvailableConditionType, condition.Type)
			if tc.expectBlocked {
				assert.Nil(t, chosen)
				expectedStatus := metav1.ConditionFalse
				if tc.expectUnknown {
					expectedStatus = metav1.ConditionUnknown
				}
				assert.Equal(t, expectedStatus, condition.Status)
				assert.Equal(t, tc.expectedReason, condition.Reason, "blocked reason classification")
				if tc.messageContains != "" {
					assert.Contains(t, condition.Message, tc.messageContains, "condition message should enumerate the elimination reason")
				}
				return
			}
			require.NotNil(t, chosen)
			assert.Equal(t, rid(tc.expectedStamp).String(), chosen.String())
			assert.Equal(t, metav1.ConditionTrue, condition.Status)
			assert.Equal(t, coreapi.CapacityReasonAvailable, condition.Reason)
		})
	}
}

func TestPlacementSyncer_SyncOnce_FreshSelection(t *testing.T) {
	ctx := context.Background()

	existing := newTestSPC() // Spec nil => needsWork is satisfied
	// Seed a stale CapacityAvailable=False from a prior capacity-blocked reconcile so the
	// successful placement below must flip it to True on the same Replace that sets Spec.
	existing.Status.Placement = &coreapi.ServiceProviderClusterPlacementStatus{}
	meta.SetStatusCondition(&existing.Status.Placement.Conditions, metav1.Condition{
		Type:   coreapi.CapacityAvailableConditionType,
		Status: metav1.ConditionFalse,
		Reason: coreapi.CapacityReasonInsufficientCapacity,
	})

	mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
	spcCRUD := mockDB.ServiceProviderClusters(testClusterSubscriptionID, testClusterResourceGroup, testClusterName)
	created, err := spcCRUD.Create(ctx, existing, nil)
	require.NoError(t, err)

	// Two eligible management clusters; stamp "1" has less available capacity
	// (3) than stamp "2" (6), so spread (highest-available) must choose "2".
	// The scheduling documents are read from the informer-cache lister for
	// scoring; the fleet DB holds them too for the reservation write path.
	sched1 := schedulingDoc("1", 6, 3, 0, 0)
	sched2 := schedulingDoc("2", 6, 0, 0, 0)
	fleetDB := fleetcosmosstoragetesting.NewMockFleetDBClient()
	_, err = fleetDB.Stamps().ManagementClusters("1").Scheduling().Create(ctx, sched1, nil)
	require.NoError(t, err)
	_, err = fleetDB.Stamps().ManagementClusters("2").Scheduling().Create(ctx, sched2, nil)
	require.NoError(t, err)

	clusterLister := &corelistertesting.SliceClusterLister{}
	syncer := &placementSyncer{
		serviceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{created}},
		clusterLister:                clusterLister,
		managementClusterLister: &fleetlistertesting.SliceManagementClusterLister{ManagementClusters: []*fleetapi.ManagementCluster{
			mcForStamp("1", true, true),
			mcForStamp("2", true, true),
		}},
		managementClusterSchedulingLister: &fleetlistertesting.SliceManagementClusterSchedulingLister{Schedulings: []*fleetapi.ManagementClusterScheduling{sched1, sched2}},
		cosmosClient:                      mockDB,
		fleetDBClient:                     fleetDB,
	}

	key := controllerutils.HCPClusterKey{SubscriptionID: testClusterSubscriptionID, ResourceGroupName: testClusterResourceGroup, HCPClusterName: testClusterName}
	// The SPC can arrive before the cluster. Do not assign or reserve until
	// the cluster is cached and its lifecycle and NIC requirement are known.
	require.NoError(t, syncer.SyncOnce(ctx, key))
	waiting, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	require.Nil(t, waiting.Spec.ManagementClusterResourceID)
	for _, stamp := range []string{"1", "2"} {
		scheduling, err := fleetDB.Stamps().ManagementClusters(stamp).Scheduling().Get(ctx, fleetapi.SchedulingResourceName)
		require.NoError(t, err)
		assert.Empty(t, scheduling.Status.PendingAssignedClusters)
	}

	clusterLister.Clusters = []*coreapi.HCPOpenShiftCluster{newTestHCPCluster()}
	require.NoError(t, syncer.SyncOnce(ctx, key))

	// Spec set to the emptier eligible MC (stamp "2") per spread selection.
	updated, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	require.NotNil(t, updated.Spec.ManagementClusterResourceID)
	assert.Equal(t, metadataapi.Must(fleetapi.ToManagementClusterResourceID("2")).String(), updated.Spec.ManagementClusterResourceID.String())

	// Pending reservation recorded on the chosen MC's scheduling doc.
	scheduling, err := fleetDB.Stamps().ManagementClusters("2").Scheduling().Get(ctx, fleetapi.SchedulingResourceName)
	require.NoError(t, err)
	require.Len(t, scheduling.Status.PendingAssignedClusters, 1)

	// The successful placement flipped CapacityAvailable to True on the same write that set
	// Spec.ManagementClusterResourceID.
	require.NotNil(t, updated.Status.Placement)
	capacity := meta.FindStatusCondition(updated.Status.Placement.Conditions, coreapi.CapacityAvailableConditionType)
	require.NotNil(t, capacity)
	assert.Equal(t, metav1.ConditionTrue, capacity.Status)
	assert.Equal(t, coreapi.CapacityReasonAvailable, capacity.Reason)
	assert.Equal(t, strings.ToLower(key.GetResourceID().String()), strings.ToLower(scheduling.Status.PendingAssignedClusters[0].String()))
}

// fakeAfterEnqueuer captures EnqueueAfter calls so tests can assert the placement
// syncer schedules a fixed-cadence retry when no management cluster has capacity.
type fakeAfterEnqueuer struct {
	keys      []any
	durations []time.Duration
}

func (f *fakeAfterEnqueuer) EnqueueAfter(key any, d time.Duration) {
	f.keys = append(f.keys, key)
	f.durations = append(f.durations, d)
}

func TestPlacementSyncer_SyncOnce_NoCapacityRecordsBlockedAndRetries(t *testing.T) {
	ctx := context.Background()

	existing := newTestSPC()
	existing.Status.Placement = &coreapi.ServiceProviderClusterPlacementStatus{
		Conditions: []metav1.Condition{{
			Type: coreapi.CapacityAvailableConditionType, Status: metav1.ConditionUnknown,
			Reason: coreapi.CapacityReasonEvaluationIncomplete,
		}},
	}
	mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
	spcCRUD := mockDB.ServiceProviderClusters(testClusterSubscriptionID, testClusterResourceGroup, testClusterName)
	created, err := spcCRUD.Create(ctx, existing, nil)
	require.NoError(t, err)

	// Eligible MC (schedulable, ready, has a scheduling doc) whose available swift-NIC
	// capacity (2) is below the required 3 => insufficient capacity => CapacityAvailable=False with
	// reason InsufficientCapacity.
	sched1 := schedulingDoc("1", 2, 0, 0, 0)
	enqueuer := &fakeAfterEnqueuer{}
	syncer := &placementSyncer{
		serviceProviderClusterLister:      &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{created}},
		clusterLister:                     &corelistertesting.SliceClusterLister{Clusters: []*coreapi.HCPOpenShiftCluster{newTestHCPCluster()}},
		managementClusterLister:           &fleetlistertesting.SliceManagementClusterLister{ManagementClusters: []*fleetapi.ManagementCluster{mcForStamp("1", true, true)}},
		managementClusterSchedulingLister: &fleetlistertesting.SliceManagementClusterSchedulingLister{Schedulings: []*fleetapi.ManagementClusterScheduling{sched1}},
		cosmosClient:                      mockDB,
		fleetDBClient:                     fleetcosmosstoragetesting.NewMockFleetDBClient(),
		enqueueAfter:                      enqueuer,
	}

	key := controllerutils.HCPClusterKey{SubscriptionID: testClusterSubscriptionID, ResourceGroupName: testClusterResourceGroup, HCPClusterName: testClusterName}
	// A capacity shortfall is an expected transient, not a controller error: SyncOnce
	// returns nil and schedules a fixed-cadence retry instead.
	require.NoError(t, syncer.SyncOnce(ctx, key))

	updated, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	assert.Nil(t, updated.Spec.ManagementClusterResourceID)

	// The selection failure records CapacityAvailable=False (reason InsufficientCapacity) on
	// Status.Placement.Conditions, carrying the selection error's message, so the create
	// operation can surface a capacity signal while the scheduler retries.
	require.NotNil(t, updated.Status.Placement)
	blocked := meta.FindStatusCondition(updated.Status.Placement.Conditions, coreapi.CapacityAvailableConditionType)
	require.NotNil(t, blocked)
	assert.Equal(t, metav1.ConditionFalse, blocked.Status)
	assert.Equal(t, coreapi.CapacityReasonInsufficientCapacity, blocked.Reason)

	// A fixed-cadence retry is scheduled for this key rather than error backoff.
	require.Equal(t, []time.Duration{placementRetryInterval}, enqueuer.durations)
	assert.Equal(t, []any{key}, enqueuer.keys)
}

func TestPlacementSyncer_SyncOnce_NoEligibleManagementClusterRecordsBlockedAndRetries(t *testing.T) {
	ctx := context.Background()

	existing := newTestSPC()
	existing.Status.Placement = &coreapi.ServiceProviderClusterPlacementStatus{
		Conditions: []metav1.Condition{{
			Type: coreapi.CapacityAvailableConditionType, Status: metav1.ConditionFalse,
			Reason: coreapi.CapacityReasonInsufficientCapacity,
		}},
	}
	mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
	spcCRUD := mockDB.ServiceProviderClusters(testClusterSubscriptionID, testClusterResourceGroup, testClusterName)
	created, err := spcCRUD.Create(ctx, existing, nil)
	require.NoError(t, err)

	// Missing scheduling data replaces the previous capacity shortfall with Unknown,
	// rather than misclassifying missing observations as known unavailability.
	enqueuer := &fakeAfterEnqueuer{}
	syncer := &placementSyncer{
		serviceProviderClusterLister:      &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{created}},
		clusterLister:                     &corelistertesting.SliceClusterLister{Clusters: []*coreapi.HCPOpenShiftCluster{newTestHCPCluster()}},
		managementClusterLister:           &fleetlistertesting.SliceManagementClusterLister{ManagementClusters: []*fleetapi.ManagementCluster{mcForStamp("1", true, true)}},
		managementClusterSchedulingLister: &fleetlistertesting.SliceManagementClusterSchedulingLister{},
		cosmosClient:                      mockDB,
		fleetDBClient:                     fleetcosmosstoragetesting.NewMockFleetDBClient(),
		enqueueAfter:                      enqueuer,
	}

	key := controllerutils.HCPClusterKey{SubscriptionID: testClusterSubscriptionID, ResourceGroupName: testClusterResourceGroup, HCPClusterName: testClusterName}
	require.NoError(t, syncer.SyncOnce(ctx, key))

	updated, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	assert.Nil(t, updated.Spec.ManagementClusterResourceID)

	require.NotNil(t, updated.Status.Placement)
	capacity := meta.FindStatusCondition(updated.Status.Placement.Conditions, coreapi.CapacityAvailableConditionType)
	require.NotNil(t, capacity)
	assert.Equal(t, metav1.ConditionUnknown, capacity.Status)
	assert.Equal(t, coreapi.CapacityReasonEvaluationIncomplete, capacity.Reason)

	// A fixed-cadence retry is scheduled rather than error backoff.
	require.Equal(t, []time.Duration{placementRetryInterval}, enqueuer.durations)
}

// TestPlacementSyncer_SyncOnce_SkipsDeletingCluster proves a cluster whose
// deletion has been requested (HCPOpenShiftCluster.ServiceProviderProperties.
// DeletionTimestamp is set) is neither placed nor reserved, even though an
// eligible management cluster with capacity is available — so the skip is due to
// the deletion guard, not a lack of capacity.
func TestPlacementSyncer_SyncOnce_SkipsDeletingCluster(t *testing.T) {
	ctx := context.Background()

	// Spec and Status both nil: absent the deletion guard, SyncOnce would
	// fresh-select and reserve capacity.
	existing := newTestSPC()
	mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
	spcCRUD := mockDB.ServiceProviderClusters(testClusterSubscriptionID, testClusterResourceGroup, testClusterName)
	created, err := spcCRUD.Create(ctx, existing, nil)
	require.NoError(t, err)

	// The HCP is being deleted.
	deletionTime := metav1.Now()
	deletingCluster := newTestHCPCluster(func(c *coreapi.HCPOpenShiftCluster) {
		c.ServiceProviderProperties.DeletionTimestamp = &deletionTime
	})

	// An eligible MC with free capacity is available and seeded into the fleet DB
	// so a reservation WOULD succeed if placement ran.
	sched := schedulingDoc("1", 6, 0, 0, 0)
	fleetDB := fleetcosmosstoragetesting.NewMockFleetDBClient()
	_, err = fleetDB.Stamps().ManagementClusters("1").Scheduling().Create(ctx, sched, nil)
	require.NoError(t, err)

	syncer := &placementSyncer{
		serviceProviderClusterLister:      &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{created}},
		clusterLister:                     &corelistertesting.SliceClusterLister{Clusters: []*coreapi.HCPOpenShiftCluster{deletingCluster}},
		managementClusterLister:           &fleetlistertesting.SliceManagementClusterLister{ManagementClusters: []*fleetapi.ManagementCluster{mcForStamp("1", true, true)}},
		managementClusterSchedulingLister: &fleetlistertesting.SliceManagementClusterSchedulingLister{Schedulings: []*fleetapi.ManagementClusterScheduling{sched}},
		cosmosClient:                      mockDB,
		fleetDBClient:                     fleetDB,
	}

	key := controllerutils.HCPClusterKey{SubscriptionID: testClusterSubscriptionID, ResourceGroupName: testClusterResourceGroup, HCPClusterName: testClusterName}
	require.NoError(t, syncer.SyncOnce(ctx, key))

	// Not placed: Spec.ManagementClusterResourceID stays nil.
	updated, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	assert.Nil(t, updated.Spec.ManagementClusterResourceID, "a deleting cluster must not be placed")

	// Not reserved: no PendingAssignedClusters entry on the eligible MC.
	scheduling, err := fleetDB.Stamps().ManagementClusters("1").Scheduling().Get(ctx, fleetapi.SchedulingResourceName)
	require.NoError(t, err)
	assert.Empty(t, scheduling.Status.PendingAssignedClusters, "a deleting cluster must not reserve capacity")
}

// TestPlacementSyncer_recordPlacementDecision_PreconditionFailureReturnsError covers the
// optimistic-concurrency loser path: when the Replace fails a precondition (412)
// because another writer updated the ServiceProviderCluster after SyncOnce read
// the base, recordPlacementDecision must return the error so the workqueue retries and
// recomputes the placement decision against fresh state — never swallow it and
// commit on top of the concurrent transition.
func TestPlacementSyncer_recordPlacementDecision_PreconditionFailureReturnsError(t *testing.T) {
	ctx := context.Background()
	chosen := testMgmtClusterResourceID()
	condition := metav1.Condition{
		Type:    coreapi.CapacityAvailableConditionType,
		Status:  metav1.ConditionTrue,
		Reason:  coreapi.CapacityReasonAvailable,
		Message: "placed on " + chosen.Name,
	}
	key := controllerutils.HCPClusterKey{SubscriptionID: testClusterSubscriptionID, ResourceGroupName: testClusterResourceGroup, HCPClusterName: testClusterName}

	base := newTestSPC() // Spec nil => needsWork is satisfied
	mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
	spcCRUD := mockDB.ServiceProviderClusters(testClusterSubscriptionID, testClusterResourceGroup, testClusterName)
	created, err := spcCRUD.Create(ctx, base, nil)
	require.NoError(t, err)

	// Advance the stored document past base so a Replace using base's (now-stale)
	// etag fails the precondition. Pass a deep copy to the bump so `created` keeps
	// its stale etag for the recordPlacementDecision base below.
	_, err = spcCRUD.Replace(ctx, created.DeepCopy(), nil)
	require.NoError(t, err)

	syncer := &placementSyncer{cosmosClient: mockDB}

	require.Error(t, syncer.recordPlacementDecision(ctx, key, created, chosen, condition), "a precondition failure must be returned so the reconcile retries")

	// The losing write must not have applied: Spec stays as the winner left it (nil).
	updated, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	assert.Nil(t, updated.Spec.ManagementClusterResourceID, "the losing write must not have overwritten the winner")
}

// TestPlacementSyncer_reservePendingAssignment_CaseInsensitiveIdempotent proves
// the reservation dedup compares resource IDs case-insensitively
// (ResourceIDsEqual/EqualFold): an entry already present under a different casing
// must not be appended again.
func TestPlacementSyncer_reservePendingAssignment_CaseInsensitiveIdempotent(t *testing.T) {
	ctx := context.Background()
	const stamp = "1"
	mcResourceID := metadataapi.Must(fleetapi.ToManagementClusterResourceID(stamp))

	clusterResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testClusterSubscriptionID + "/resourceGroups/" + testClusterResourceGroup +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/MyCluster"))
	// The same ARM ID already reserved, stored under a different (lower) casing.
	alreadyReserved := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testClusterSubscriptionID + "/resourceGroups/" + testClusterResourceGroup +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/mycluster"))
	require.NotEqual(t, clusterResourceID.String(), alreadyReserved.String(), "test fixture: the two IDs must differ only in case")

	fleetDB := fleetcosmosstoragetesting.NewMockFleetDBClient()
	doc := &fleetapi.ManagementClusterScheduling{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   metadataapi.Must(fleetapi.ToManagementClusterSchedulingResourceID(stamp)),
			PartitionKey: stamp,
		},
		Status: fleetapi.ManagementClusterSchedulingStatus{PendingAssignedClusters: []*azcorearm.ResourceID{alreadyReserved}},
	}
	_, err := fleetDB.Stamps().ManagementClusters(stamp).Scheduling().Create(ctx, doc, nil)
	require.NoError(t, err)

	syncer := &placementSyncer{fleetDBClient: fleetDB}
	require.NoError(t, syncer.reservePendingAssignment(ctx, mcResourceID, clusterResourceID))

	updated, err := fleetDB.Stamps().ManagementClusters(stamp).Scheduling().Get(ctx, fleetapi.SchedulingResourceName)
	require.NoError(t, err)
	assert.Len(t, updated.Status.PendingAssignedClusters, 1, "a case-differing duplicate must not be appended")
}

func TestSchedulingObservationsDetermineCapacityAvailability(t *testing.T) {
	for _, tc := range []struct {
		name       string
		mutate     func(*fleetapi.ManagementCluster, *fleetapi.ManagementClusterScheduling)
		wantStatus metav1.ConditionStatus
	}{
		{
			name: "missing stamp identifier",
			mutate: func(mc *fleetapi.ManagementCluster, _ *fleetapi.ManagementClusterScheduling) {
				mc.ResourceID.Parent = nil
			},
			wantStatus: metav1.ConditionUnknown,
		},
		{
			name: "invalid scheduling policy",
			mutate: func(mc *fleetapi.ManagementCluster, _ *fleetapi.ManagementClusterScheduling) {
				mc.Spec.SchedulingPolicy = ""
			},
			wantStatus: metav1.ConditionUnknown,
		},
		{
			name: "readiness not observed",
			mutate: func(mc *fleetapi.ManagementCluster, _ *fleetapi.ManagementClusterScheduling) {
				mc.Status.Conditions = nil
			},
			wantStatus: metav1.ConditionUnknown,
		},
		{
			name: "capacity observations missing",
			mutate: func(_ *fleetapi.ManagementCluster, scheduling *fleetapi.ManagementClusterScheduling) {
				meta.RemoveStatusCondition(&scheduling.Status.Conditions, fleetapi.ConditionTypeCapacityDataCurrent)
			},
			wantStatus: metav1.ConditionUnknown,
		},
		{
			name: "scaling observations stale",
			mutate: func(_ *fleetapi.ManagementCluster, scheduling *fleetapi.ManagementClusterScheduling) {
				meta.FindStatusCondition(scheduling.Status.Conditions, fleetapi.ConditionTypeScalingDataCurrent).Status = metav1.ConditionFalse
			},
			wantStatus: metav1.ConditionUnknown,
		},
		{
			name: "known unschedulable does not require capacity observations",
			mutate: func(mc *fleetapi.ManagementCluster, scheduling *fleetapi.ManagementClusterScheduling) {
				mc.Spec.SchedulingPolicy = fleetapi.ManagementClusterSchedulingPolicyUnschedulable
				scheduling.Status.Conditions = nil
			},
			wantStatus: metav1.ConditionFalse,
		},
		{
			name: "known not ready does not require capacity observations",
			mutate: func(mc *fleetapi.ManagementCluster, scheduling *fleetapi.ManagementClusterScheduling) {
				meta.FindStatusCondition(mc.Status.Conditions, string(fleetapi.ManagementClusterConditionReady)).Status = metav1.ConditionFalse
				scheduling.Status.Conditions = nil
			},
			wantStatus: metav1.ConditionFalse,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mc := mcForStamp("1", true, true)
			scheduling := schedulingDoc("1", 6, 0, 0, 0)
			tc.mutate(mc, scheduling)
			syncer := &placementSyncer{
				managementClusterLister: &fleetlistertesting.SliceManagementClusterLister{
					ManagementClusters: []*fleetapi.ManagementCluster{mc},
				},
				managementClusterSchedulingLister: &fleetlistertesting.SliceManagementClusterSchedulingLister{
					Schedulings: []*fleetapi.ManagementClusterScheduling{scheduling},
				},
			}
			candidates, err := syncer.evaluateManagementClusters(context.Background())
			require.NoError(t, err)
			chosen, condition := selectByCapacity(candidates, swiftNICsPerHCP)
			assert.Nil(t, chosen)
			assert.Equal(t, tc.wantStatus, condition.Status)
		})
	}
}

type failingManagementClusterLister struct {
	*fleetlistertesting.SliceManagementClusterLister
	err error
}

func (l *failingManagementClusterLister) List(context.Context) ([]*fleetapi.ManagementCluster, error) {
	return nil, l.err
}

func TestPlacementSyncer_FailedEvaluationPreservesCapacityShortfall(t *testing.T) {
	for _, failure := range []string{"list candidates", "reserve assignment"} {
		t.Run(failure, func(t *testing.T) {
			ctx := context.Background()
			existing := newTestSPC()
			existing.Status.Placement = &coreapi.ServiceProviderClusterPlacementStatus{
				Conditions: []metav1.Condition{{
					Type: coreapi.CapacityAvailableConditionType, Status: metav1.ConditionFalse,
					Reason: coreapi.CapacityReasonInsufficientCapacity,
				}},
			}
			db := corecosmosstoragetesting.NewMockResourcesDBClient()
			spcCRUD := db.ServiceProviderClusters(testClusterSubscriptionID, testClusterResourceGroup, testClusterName)
			created, err := spcCRUD.Create(ctx, existing, nil)
			require.NoError(t, err)
			syncer := &placementSyncer{
				serviceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{
					ServiceProviderClusters: []*coreapi.ServiceProviderCluster{created},
				},
				clusterLister: &corelistertesting.SliceClusterLister{Clusters: []*coreapi.HCPOpenShiftCluster{newTestHCPCluster()}},
				managementClusterLister: &fleetlistertesting.SliceManagementClusterLister{
					ManagementClusters: []*fleetapi.ManagementCluster{mcForStamp("1", true, true)},
				},
				managementClusterSchedulingLister: &fleetlistertesting.SliceManagementClusterSchedulingLister{
					Schedulings: []*fleetapi.ManagementClusterScheduling{schedulingDoc("1", 6, 0, 0, 0)},
				},
				cosmosClient: db,
				// The cache has capacity but the scheduling document is absent from
				// storage, so reservation fails after a fitting candidate is found.
				fleetDBClient: fleetcosmosstoragetesting.NewMockFleetDBClient(),
			}
			listErr := errors.New("candidate list unavailable")
			if failure == "list candidates" {
				syncer.managementClusterLister = &failingManagementClusterLister{err: listErr}
			}
			key := controllerutils.HCPClusterKey{SubscriptionID: testClusterSubscriptionID, ResourceGroupName: testClusterResourceGroup, HCPClusterName: testClusterName}
			err = syncer.SyncOnce(ctx, key)
			require.Error(t, err)
			if failure == "list candidates" {
				assert.ErrorIs(t, err, listErr)
			} else {
				assert.True(t, cosmosstorageutils.IsNotFoundError(err))
			}
			updated, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
			require.NoError(t, err)
			assert.Nil(t, updated.Spec.ManagementClusterResourceID)
			assert.Equal(t, existing.Status.Placement, updated.Status.Placement)
		})
	}
}
