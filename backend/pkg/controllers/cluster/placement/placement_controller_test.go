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
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/fleetcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/fleetlistertesting"
	"github.com/Azure/ARO-HCP/internal/kuberesources"
	"github.com/Azure/ARO-HCP/internal/ocm"
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
		ResourceID:     resourceID,
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

func schedulingDoc(stamp string, ceiling, usage, notReady, pending int64) *fleetapi.ManagementClusterScheduling {
	resourceID := metadataapi.Must(fleetapi.ToManagementClusterSchedulingResourceID(stamp))
	notReadyIDs := make([]string, notReady)
	for i := range notReadyIDs {
		notReadyIDs[i] = fmt.Sprintf("/subscriptions/x/resourceGroups/y/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/nr-%s-%d", stamp, i)
	}
	return &fleetapi.ManagementClusterScheduling{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: resourceID, PartitionKey: strings.ToLower(stamp)},
		Status: fleetapi.ManagementClusterSchedulingStatus{
			ObservedResources:       fleetapi.ObservedResources{Usage: swiftResourceList(usage)},
			ScaleCeiling:            fleetapi.ScaleCeiling{Capacity: swiftResourceList(ceiling)},
			NotReadyResourceIDs:     notReadyIDs,
			PendingAssignedClusters: dummyResourceIDs(int(pending)),
		},
	}
}

func TestComputeAvailableSwiftNICs(t *testing.T) {
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
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			doc := schedulingDoc("s", tc.ceiling, tc.usage, tc.notReady, tc.pending)
			assert.Equal(t, tc.expected, computeAvailableSwiftNICs(doc))
		})
	}
}

func TestComputeAvailableSwiftNICs_IgnoresNilAndEmptyEntries(t *testing.T) {
	doc := &fleetapi.ManagementClusterScheduling{
		Status: fleetapi.ManagementClusterSchedulingStatus{
			ScaleCeiling: fleetapi.ScaleCeiling{Capacity: swiftResourceList(9)},
			// One nil entry (must not reserve) + one real entry (reserves 3).
			PendingAssignedClusters: []*azcorearm.ResourceID{
				nil,
				metadataapi.Must(fleetapi.ToManagementClusterResourceID("x")),
			},
			// One empty string (must not count) + one real entry (reserves 3).
			NotReadyResourceIDs: []string{"", "/subscriptions/s/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/nr"},
		},
	}
	// 9 - usage(0) - notReady(1*3) - pending(1*3) = 3
	assert.Equal(t, int64(3), computeAvailableSwiftNICs(doc))
}

// eligibleCandidate builds a schedulingCandidate for an eligible (schedulable +
// ready) management cluster with a scheduling document exposing `available`
// swift NICs (ceiling=available, no usage/notReady/pending).
func eligibleCandidate(stamp string, available int64) schedulingCandidate {
	return schedulingCandidate{
		managementCluster: mcForStamp(stamp, true, true),
		scheduling:        schedulingDoc(stamp, available, 0, 0, 0),
	}
}

// TestSelectByCapacity exercises the single, pure elimination+selection function:
// all candidate elimination (eligibility AND capacity) now lives here, so the
// cases cover both ineligibility reasons and capacity-based spread/tie-breaking.
func TestSelectByCapacity(t *testing.T) {
	rid := func(stamp string) *azcorearm.ResourceID {
		return metadataapi.Must(fleetapi.ToManagementClusterResourceID(stamp))
	}

	tests := []struct {
		name          string
		candidates    []schedulingCandidate
		expectedStamp string // "" => expect error
		expectError   bool
		errContains   string // substring the error must enumerate (elimination reason)
	}{
		{name: "no candidates - error", candidates: nil, expectError: true},
		{
			name:        "not schedulable - eliminated with reason",
			candidates:  []schedulingCandidate{{managementCluster: mcForStamp("1", false, true), scheduling: schedulingDoc("1", 9, 0, 0, 0)}},
			expectError: true,
			errContains: "scheduling policy",
		},
		{
			name:        "not ready - eliminated with reason",
			candidates:  []schedulingCandidate{{managementCluster: mcForStamp("1", true, false), scheduling: schedulingDoc("1", 9, 0, 0, 0)}},
			expectError: true,
			errContains: "not Ready",
		},
		{
			name:        "no scheduling data - eliminated with reason",
			candidates:  []schedulingCandidate{{managementCluster: mcForStamp("1", true, true), scheduling: nil}},
			expectError: true,
			errContains: "no scheduling/capacity data",
		},
		{
			name:        "eligible but below threshold - eliminated with reason",
			candidates:  []schedulingCandidate{eligibleCandidate("1", 2)},
			expectError: true,
			errContains: "insufficient swift-NIC capacity",
		},
		{name: "single fit", candidates: []schedulingCandidate{eligibleCandidate("1", 3)}, expectedStamp: "1"},
		{name: "exactly at threshold fits", candidates: []schedulingCandidate{eligibleCandidate("1", 3)}, expectedStamp: "1"},
		{
			name:          "highest available among fits (spread load)",
			candidates:    []schedulingCandidate{eligibleCandidate("1", 9), eligibleCandidate("2", 3), eligibleCandidate("3", 6)},
			expectedStamp: "1",
		},
		{
			name:          "skips those below threshold, picks highest fitting",
			candidates:    []schedulingCandidate{eligibleCandidate("1", 2), eligibleCandidate("2", 5), eligibleCandidate("3", 4)},
			expectedStamp: "2",
		},
		{
			name:          "tie on available - lowest resource ID wins (order independent)",
			candidates:    []schedulingCandidate{eligibleCandidate("3", 3), eligibleCandidate("1", 3), eligibleCandidate("2", 3)},
			expectedStamp: "1",
		},
		{
			name: "mix of ineligible and eligible - picks the eligible fit",
			candidates: []schedulingCandidate{
				{managementCluster: mcForStamp("1", true, false), scheduling: schedulingDoc("1", 9, 0, 0, 0)}, // not ready
				eligibleCandidate("2", 3),
			},
			expectedStamp: "2",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			chosen, err := selectByCapacity(tc.candidates)
			if tc.expectError {
				require.Error(t, err)
				assert.Nil(t, chosen)
				if tc.errContains != "" {
					assert.Contains(t, err.Error(), tc.errContains, "error should enumerate the elimination reason")
				}
				return
			}
			require.NoError(t, err)
			require.NotNil(t, chosen)
			assert.Equal(t, rid(tc.expectedStamp).String(), chosen.String())
		})
	}
}

func TestPlacementSyncer_SyncOnce_Backfill(t *testing.T) {
	ctx := context.Background()

	existing := newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
		spc.Status.ManagementClusterResourceID = testMgmtClusterResourceID()
	})

	mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
	spcCRUD := mockDB.ServiceProviderClusters(testClusterSubscriptionID, testClusterResourceGroup, testClusterName)
	created, err := spcCRUD.Create(ctx, existing, nil)
	require.NoError(t, err)

	// No eligible MC and no fleet capacity data: proves backfill does NOT run
	// fresh selection (which would fail).
	syncer := &placementSyncer{
		serviceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{created}},
		managementClusterLister:      &fleetlistertesting.SliceManagementClusterLister{},
		cosmosClient:                 mockDB,
		fleetDBClient:                fleetcosmosstoragetesting.NewMockFleetDBClient(),
	}

	key := controllerutils.HCPClusterKey{SubscriptionID: testClusterSubscriptionID, ResourceGroupName: testClusterResourceGroup, HCPClusterName: testClusterName}
	require.NoError(t, syncer.SyncOnce(ctx, key))

	updated, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	require.NotNil(t, updated.Spec.ManagementClusterResourceID, "Spec must be backfilled from Status")
	assert.Equal(t, testMgmtClusterResourceID().String(), updated.Spec.ManagementClusterResourceID.String())
	require.NotNil(t, updated.Spec.ManagementClusterPlacementTime, "placement time must be recorded on backfill")
}

func TestPlacementSyncer_SyncOnce_FreshSelection(t *testing.T) {
	ctx := context.Background()

	existing := newTestSPC() // Spec and Status both nil

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

	syncer := &placementSyncer{
		serviceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{created}},
		// No cluster in cache => no PendingClusterServiceID => fresh selection.
		clusterLister: &corelistertesting.SliceClusterLister{},
		managementClusterLister: &fleetlistertesting.SliceManagementClusterLister{ManagementClusters: []*fleetapi.ManagementCluster{
			mcForStamp("1", true, true),
			mcForStamp("2", true, true),
		}},
		managementClusterSchedulingLister: &fleetlistertesting.SliceManagementClusterSchedulingLister{Schedulings: []*fleetapi.ManagementClusterScheduling{sched1, sched2}},
		cosmosClient:                      mockDB,
		fleetDBClient:                     fleetDB,
	}

	key := controllerutils.HCPClusterKey{SubscriptionID: testClusterSubscriptionID, ResourceGroupName: testClusterResourceGroup, HCPClusterName: testClusterName}
	require.NoError(t, syncer.SyncOnce(ctx, key))

	// Spec set to the emptier eligible MC (stamp "2") per spread selection.
	updated, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	require.NotNil(t, updated.Spec.ManagementClusterResourceID)
	assert.Equal(t, metadataapi.Must(fleetapi.ToManagementClusterResourceID("2")).String(), updated.Spec.ManagementClusterResourceID.String())
	require.NotNil(t, updated.Spec.ManagementClusterPlacementTime, "placement time must be recorded atomically with the placement intent")

	// Pending reservation recorded on the chosen MC's scheduling doc.
	scheduling, err := fleetDB.Stamps().ManagementClusters("2").Scheduling().Get(ctx, fleetapi.SchedulingResourceName)
	require.NoError(t, err)
	require.Len(t, scheduling.Status.PendingAssignedClusters, 1)
	assert.Equal(t, strings.ToLower(key.GetResourceID().String()), strings.ToLower(scheduling.Status.PendingAssignedClusters[0].String()))
}

func TestPlacementSyncer_SyncOnce_NoCapacityFails(t *testing.T) {
	ctx := context.Background()

	existing := newTestSPC()
	mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
	spcCRUD := mockDB.ServiceProviderClusters(testClusterSubscriptionID, testClusterResourceGroup, testClusterName)
	created, err := spcCRUD.Create(ctx, existing, nil)
	require.NoError(t, err)

	// Eligible MC but no scheduling doc in the cache => ineligible => no fit => error.
	syncer := &placementSyncer{
		serviceProviderClusterLister:      &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{created}},
		clusterLister:                     &corelistertesting.SliceClusterLister{},
		managementClusterLister:           &fleetlistertesting.SliceManagementClusterLister{ManagementClusters: []*fleetapi.ManagementCluster{mcForStamp("1", true, true)}},
		managementClusterSchedulingLister: &fleetlistertesting.SliceManagementClusterSchedulingLister{},
		cosmosClient:                      mockDB,
		fleetDBClient:                     fleetcosmosstoragetesting.NewMockFleetDBClient(),
	}

	key := controllerutils.HCPClusterKey{SubscriptionID: testClusterSubscriptionID, ResourceGroupName: testClusterResourceGroup, HCPClusterName: testClusterName}
	require.Error(t, syncer.SyncOnce(ctx, key))

	updated, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	assert.Nil(t, updated.Spec.ManagementClusterResourceID)
}

// TestPlacementSyncer_SyncOnce_PlacementSource is the tabular test for the three
// placement sources SyncOnce chooses between when Spec is unset:
//   - Status already set                     => backfill Spec from observed Status
//   - both nil + PendingClusterServiceID set => backfill Spec from Cluster Service
//     (rollout-race migration path; NOT a fresh capacity selection)
//   - both nil + no PendingClusterServiceID  => fresh capacity selection
//
// It also covers the defer case: a pending CS ID exists but Cluster Service has
// not reported a provision shard yet (Spec must stay nil, no error); and the
// Cluster Service error cases for a pending CS ID: a 404 (the pending ID never
// became a real cluster) falls through to a FRESH selection, while transient and
// other non-404 errors are returned so the workqueue retries.
func TestPlacementSyncer_SyncOnce_PlacementSource(t *testing.T) {
	pendingCSID := metadataapi.Must(metadataapi.NewInternalID(testClusterServiceIDStr))

	// freshMC is an eligible management cluster (distinct from the CS-mapped mc1)
	// used only by the fresh-selection case.
	const freshStamp = "fresh-mc"
	freshMCResourceID := metadataapi.Must(fleetapi.ToManagementClusterResourceID(freshStamp))

	tests := []struct {
		name               string
		spc                *coreapi.ServiceProviderCluster
		cluster            *coreapi.HCPOpenShiftCluster // nil => not present in cache
		managementClusters []*fleetapi.ManagementCluster
		schedulings        []*fleetapi.ManagementClusterScheduling
		csShard            *arohcpv1alpha1.ProvisionShard
		csError            error
		expectCSCall       bool
		expectedSpec       string // "" => nil
		expectError        bool
	}{
		{
			name: "status set - backfill from status (no CS call, no fresh select)",
			spc: newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
				spc.Status.ManagementClusterResourceID = testMgmtClusterResourceID()
			}),
			expectCSCall: false,
			expectedSpec: testMgmtClusterResourceID().String(),
		},
		{
			name: "both nil + pending CS ID - backfill from Cluster Service (not fresh select)",
			spc:  newTestSPC(),
			cluster: newTestHCPCluster(func(c *coreapi.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.ClusterServiceID = nil
				c.ServiceProviderProperties.PendingClusterServiceID = &pendingCSID
			}),
			managementClusters: []*fleetapi.ManagementCluster{newTestManagementCluster()},
			csShard: metadataapi.Must(arohcpv1alpha1.NewProvisionShard().
				HREF(testProvisionShardHREF(testProvisionShardIDStr)).
				Build()),
			expectCSCall: true,
			expectedSpec: testMgmtClusterResourceID().String(),
		},
		{
			name: "both nil + pending CS ID + shard not allocated - defer (no fresh select, no error)",
			spc:  newTestSPC(),
			cluster: newTestHCPCluster(func(c *coreapi.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.ClusterServiceID = nil
				c.ServiceProviderProperties.PendingClusterServiceID = &pendingCSID
			}),
			managementClusters: []*fleetapi.ManagementCluster{newTestManagementCluster()},
			csShard:            metadataapi.Must(arohcpv1alpha1.NewProvisionShard().Build()), // empty HREF
			expectCSCall:       true,
			expectedSpec:       "",
		},
		{
			name: "both nil + no pending CS ID - fresh selection",
			spc:  newTestSPC(),
			cluster: newTestHCPCluster(func(c *coreapi.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.ClusterServiceID = nil
			}),
			managementClusters: []*fleetapi.ManagementCluster{mcForStamp(freshStamp, true, true)},
			schedulings:        []*fleetapi.ManagementClusterScheduling{schedulingDoc(freshStamp, 6, 0, 0, 0)},
			expectCSCall:       false,
			expectedSpec:       freshMCResourceID.String(),
		},
		{
			// The pending CS ID is stale: an older backend recorded it then crashed
			// before creating the cluster in Cluster Service, so CS returns 404.
			// There is no committed placement to preserve, so SyncOnce must fall
			// through to a FRESH capacity-aware selection (picks the eligible MC)
			// rather than deferring forever.
			name: "both nil + pending CS ID + CS 404 - fresh selection (stale pending ID)",
			spc:  newTestSPC(),
			cluster: newTestHCPCluster(func(c *coreapi.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.ClusterServiceID = nil
				c.ServiceProviderProperties.PendingClusterServiceID = &pendingCSID
			}),
			managementClusters: []*fleetapi.ManagementCluster{mcForStamp(freshStamp, true, true)},
			schedulings:        []*fleetapi.ManagementClusterScheduling{schedulingDoc(freshStamp, 6, 0, 0, 0)},
			csError:            metadataapi.Must(ocmerrors.NewError().Status(http.StatusNotFound).Build()),
			expectCSCall:       true,
			expectedSpec:       freshMCResourceID.String(),
		},
		{
			// A transient (non-404) Cluster Service error must NOT fresh-select: it
			// is returned so the workqueue retries. An eligible MC is present to
			// prove the error short-circuits before fresh selection (no placement
			// is written).
			name: "both nil + pending CS ID + CS transient error - return error (retry, no placement)",
			spc:  newTestSPC(),
			cluster: newTestHCPCluster(func(c *coreapi.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.ClusterServiceID = nil
				c.ServiceProviderProperties.PendingClusterServiceID = &pendingCSID
			}),
			managementClusters: []*fleetapi.ManagementCluster{mcForStamp(freshStamp, true, true)},
			schedulings:        []*fleetapi.ManagementClusterScheduling{schedulingDoc(freshStamp, 6, 0, 0, 0)},
			csError:            fmt.Errorf("connection refused"),
			expectCSCall:       true,
			expectedSpec:       "",
			expectError:        true,
		},
		{
			// A non-404 Cluster Service HTTP error (e.g. 500) must also be returned
			// for retry: only a 404 means the cluster was never created and is safe
			// to fresh-select.
			name: "both nil + pending CS ID + CS non-404 error - return error (only 404 falls through)",
			spc:  newTestSPC(),
			cluster: newTestHCPCluster(func(c *coreapi.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.ClusterServiceID = nil
				c.ServiceProviderProperties.PendingClusterServiceID = &pendingCSID
			}),
			managementClusters: []*fleetapi.ManagementCluster{mcForStamp(freshStamp, true, true)},
			schedulings:        []*fleetapi.ManagementClusterScheduling{schedulingDoc(freshStamp, 6, 0, 0, 0)},
			csError:            metadataapi.Must(ocmerrors.NewError().Status(http.StatusInternalServerError).Build()),
			expectCSCall:       true,
			expectedSpec:       "",
			expectError:        true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
			spcCRUD := mockDB.ServiceProviderClusters(testClusterSubscriptionID, testClusterResourceGroup, testClusterName)
			created, err := spcCRUD.Create(ctx, tc.spc, nil)
			require.NoError(t, err)

			clusters := []*coreapi.HCPOpenShiftCluster{}
			if tc.cluster != nil {
				clusters = append(clusters, tc.cluster)
			}

			// The fleet DB backs the reservation write path (fresh selection only);
			// seed it with the same scheduling docs used for scoring.
			fleetDB := fleetcosmosstoragetesting.NewMockFleetDBClient()
			for _, s := range tc.schedulings {
				_, err := fleetDB.Stamps().ManagementClusters(s.PartitionKey).Scheduling().Create(ctx, s, nil)
				require.NoError(t, err)
			}

			mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
			if tc.expectCSCall {
				mockCSClient.EXPECT().
					GetClusterProvisionShard(gomock.Any(), pendingCSID).
					Return(tc.csShard, tc.csError)
			}

			syncer := &placementSyncer{
				serviceProviderClusterLister:      &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{created}},
				clusterLister:                     &corelistertesting.SliceClusterLister{Clusters: clusters},
				managementClusterLister:           &fleetlistertesting.SliceManagementClusterLister{ManagementClusters: tc.managementClusters},
				managementClusterSchedulingLister: &fleetlistertesting.SliceManagementClusterSchedulingLister{Schedulings: tc.schedulings},
				cosmosClient:                      mockDB,
				fleetDBClient:                     fleetDB,
				clusterServiceClient:              mockCSClient,
			}

			key := controllerutils.HCPClusterKey{SubscriptionID: testClusterSubscriptionID, ResourceGroupName: testClusterResourceGroup, HCPClusterName: testClusterName}
			err = syncer.SyncOnce(ctx, key)
			if tc.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			updated, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
			require.NoError(t, err)
			if tc.expectedSpec != "" {
				require.NotNil(t, updated.Spec.ManagementClusterResourceID)
				assert.Equal(t, tc.expectedSpec, updated.Spec.ManagementClusterResourceID.String())
				require.NotNil(t, updated.Spec.ManagementClusterPlacementTime, "placement time must be recorded when a placement is written")
			} else {
				assert.Nil(t, updated.Spec.ManagementClusterResourceID, "no placement should be written")
			}
		})
	}
}

// TestPlacementSyncer_setSpecPlacement_Timestamp covers the placement-time stamp:
// it is set on first placement and preserved (not overwritten) when a timestamp
// already exists, so it marks the moment of placement rather than the latest sync.
func TestPlacementSyncer_setSpecPlacement_Timestamp(t *testing.T) {
	ctx := context.Background()
	chosen := testMgmtClusterResourceID()
	key := controllerutils.HCPClusterKey{SubscriptionID: testClusterSubscriptionID, ResourceGroupName: testClusterResourceGroup, HCPClusterName: testClusterName}
	preExisting := metav1.NewTime(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))

	tests := []struct {
		name              string
		existingPlacement *metav1.Time
		// wantPreserved, when non-nil, is the timestamp the write must keep intact;
		// when nil the write must populate a fresh (non-nil) timestamp.
		wantPreserved *metav1.Time
	}{
		{name: "sets placement time on first placement", existingPlacement: nil, wantPreserved: nil},
		{name: "preserves an existing placement time across re-write", existingPlacement: &preExisting, wantPreserved: &preExisting},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			existing := newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
				spc.Spec.ManagementClusterPlacementTime = tc.existingPlacement
			})
			mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
			spcCRUD := mockDB.ServiceProviderClusters(testClusterSubscriptionID, testClusterResourceGroup, testClusterName)
			created, err := spcCRUD.Create(ctx, existing, nil)
			require.NoError(t, err)

			syncer := &placementSyncer{
				serviceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{created}},
				cosmosClient:                 mockDB,
			}
			require.NoError(t, syncer.setSpecPlacement(ctx, key, chosen))

			updated, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
			require.NoError(t, err)
			require.NotNil(t, updated.Spec.ManagementClusterResourceID)
			require.NotNil(t, updated.Spec.ManagementClusterPlacementTime, "placement time must be set")
			if tc.wantPreserved != nil {
				assert.Equal(t, tc.wantPreserved.Unix(), updated.Spec.ManagementClusterPlacementTime.Unix(), "existing placement time must be preserved")
			}
		})
	}
}
