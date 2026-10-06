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

package validation

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/lru"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/validationutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	testSubscriptionID = "00000000-0000-0000-0000-000000000000"
	testResourceGroup  = "test-rg"
	testClusterName    = "test-cluster"
	testNodePoolName   = "test-nodepool"
	testValidationName = "TestValidation"
)

var fixedNow = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

type fakeAfterEnqueuer struct {
	enqueuedKeys      []any
	enqueuedDurations []time.Duration
}

func (f *fakeAfterEnqueuer) EnqueueAfter(keyObj any, duration time.Duration) {
	f.enqueuedKeys = append(f.enqueuedKeys, keyObj)
	f.enqueuedDurations = append(f.enqueuedDurations, duration)
}

func newTestNodePoolKey() controllerutils.HCPNodePoolKey {
	return controllerutils.HCPNodePoolKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroup,
		HCPClusterName:    testClusterName,
		HCPNodePoolName:   testNodePoolName,
	}
}

func newTestCluster(t *testing.T) *coreapi.Cluster {
	t.Helper()
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroup +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName))
	return &coreapi.Cluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID:   resourceID,
				Name: testClusterName,
				Type: coreapi.ClusterResourceType.String(),
			},
			Location: "eastus",
		},
	}
}

func newTestNodePool(t *testing.T) *coreapi.NodePool {
	t.Helper()
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroup +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
			"/nodePools/" + testNodePoolName))
	return &coreapi.NodePool{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID:   resourceID,
				Name: testNodePoolName,
				Type: coreapi.NodePoolResourceType.String(),
			},
			Location: "eastus",
		},
	}
}

func newTestSubscription() *coreapi.Subscription {
	subResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID))
	return &coreapi.Subscription{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   subResourceID,
			PartitionKey: strings.ToLower(subResourceID.SubscriptionID),
		},
		State: coreapi.SubscriptionStateRegistered,
	}
}

func newTestSyncer(mockDB *corecosmosstoragetesting.MockResourcesDBClient, validation validationutils.NodePoolValidation, fakeClock *clocktesting.FakePassiveClock) (*nodePoolValidationSyncer, *fakeAfterEnqueuer) {
	retryCooldown := controllerutil.NewSettableCooldownChecker()
	retryCooldown.SetClock(fakeClock)
	enqueuer := &fakeAfterEnqueuer{}
	syncer := &nodePoolValidationSyncer{
		retryCooldownChecker:          retryCooldown,
		enqueueAfter:                  enqueuer,
		resourcesDBClient:             mockDB,
		serviceProviderNodePoolLister: &corelistertesting.DBServiceProviderNodePoolLister{ResourcesDBClient: mockDB},
		nodePoolLister:                &corelistertesting.DBNodePoolLister{ResourcesDBClient: mockDB},
		lastValidatedUserIntent:       lru.New(controllerutil.SettableCooldownCacheCapacity),
		validation:                    validation,
		consecutiveUnknownCounts:      lru.New(consecutiveUnknownCountsCacheCapacity),
	}
	return syncer, enqueuer
}

func TestNodePoolValidationSyncer_DeletionClearsUserIntent(t *testing.T) {
	for _, deletingResource := range []string{"cluster", "node pool"} {
		t.Run(deletingResource, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
			cluster := newTestCluster(t)
			nodePool := newTestNodePool(t)
			deletionTime := metav1.NewTime(fixedNow)
			if deletingResource == "cluster" {
				cluster.ServiceProviderProperties.DeletionTimestamp = &deletionTime
			} else {
				nodePool.ServiceProviderProperties.DeletionTimestamp = &deletionTime
			}
			_, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroup).Create(ctx, cluster, nil)
			require.NoError(t, err)
			_, err = mockDB.HCPClusters(testSubscriptionID, testResourceGroup).NodePools(testClusterName).Create(ctx, nodePool, nil)
			require.NoError(t, err)
			syncer, _ := newTestSyncer(mockDB, NewMockNodePoolValidation(testValidationName), clocktesting.NewFakePassiveClock(fixedNow))
			key := newTestNodePoolKey()
			syncer.lastValidatedUserIntent.Add(key, int64(1))
			require.NoError(t, syncer.SyncOnce(ctx, key))
			_, exists := syncer.lastValidatedUserIntent.Get(key)
			assert.False(t, exists, "deleting resources must not retain validated-generation history")
		})
	}
}

func TestNodePoolValidationSyncer_SyncOnce(t *testing.T) {

	defaultSetupDB := func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
		t.Helper()
		_, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroup).Create(ctx, newTestCluster(t), nil)
		require.NoError(t, err)
		nodePool := newTestNodePool(t)
		_, err = mockDB.HCPClusters(testSubscriptionID, testResourceGroup).NodePools(testClusterName).Create(ctx, nodePool, nil)
		require.NoError(t, err)
		_, err = mockDB.Subscriptions().Create(ctx, newTestSubscription(), nil)
		require.NoError(t, err)
		// Seed an empty ServiceProviderNodePool the way the production creator
		// controller would have populated it by the time the syncer runs.
		_, err = corecosmosstorage.GetOrCreateServiceProviderNodePool(ctx, mockDB, nodePool.ID)
		require.NoError(t, err)
	}

	testCases := []struct {
		name       string
		setupDB    func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient)
		validation validationutils.NodePoolValidation
		wantErr    bool
		// wantCondition, if non-nil, asserts that the stored validation condition's Status/Reason/Message
		// match (Type and LastTransitionTime are not compared).
		wantCondition *metav1.Condition
		// wantConditionAbsent asserts that no validation condition is stored at all.
		wantConditionAbsent bool
		wantEnqueue         bool
	}{
		{
			name: "cluster not found -- no-op",
			setupDB: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				_, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroup).NodePools(testClusterName).Create(ctx, newTestNodePool(t), nil)
				require.NoError(t, err)
				_, err = mockDB.Subscriptions().Create(ctx, newTestSubscription(), nil)
				require.NoError(t, err)
			},
			validation: NewMockNodePoolValidation(testValidationName),
		},
		{
			name: "node pool not found -- no-op",
			setupDB: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				_, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroup).Create(ctx, newTestCluster(t), nil)
				require.NoError(t, err)
				_, err = mockDB.Subscriptions().Create(ctx, newTestSubscription(), nil)
				require.NoError(t, err)
			},
			validation: NewMockNodePoolValidation(testValidationName),
		},
		{
			name:          "validation passes -- condition set to True, no requeue",
			setupDB:       defaultSetupDB,
			validation:    NewMockNodePoolValidation(testValidationName).WithPassed(),
			wantCondition: &metav1.Condition{Status: metav1.ConditionTrue, Reason: "AsExpected", Message: "As expected."},
			wantEnqueue:   false,
		},
		{
			name:    "validation fails -- condition set to False, requeue scheduled",
			setupDB: defaultSetupDB,
			validation: NewMockNodePoolValidation(testValidationName).WithFailed(
				"QuotaExceeded", "quota exceeded", "Quota exceeded for this subscription.",
			),
			wantCondition: &metav1.Condition{Status: metav1.ConditionFalse, Reason: "QuotaExceeded", Message: "Quota exceeded for this subscription."},
			wantEnqueue:   true,
		},
		{
			// Covers the "last step of SyncOnce" reporting-policy branch that turns an Unknown result into
			// an error return; see TestNodePoolValidationSyncer_SyncOnce's sibling case below for the
			// LogOnly branch of the same decision.
			name:    "validation unknown with ReportError -- condition set to Unknown, requeue scheduled, error returned",
			setupDB: defaultSetupDB,
			validation: NewMockNodePoolValidation(testValidationName).WithUnknownReportError(
				"InternalError", "failed to reach Azure", "Unable to verify.",
			),
			wantErr:       true,
			wantCondition: &metav1.Condition{Status: metav1.ConditionUnknown, Reason: "InternalError", Message: "Unable to verify."},
			wantEnqueue:   true,
		},
		{
			// Covers handleRequeue's nil guard: EarliestRetryAfter == nil must skip cooldown/requeue
			// without otherwise affecting the condition write.
			name:    "validation fails with nil EarliestRetryAfter -- condition still set, no cooldown or requeue scheduled",
			setupDB: defaultSetupDB,
			validation: NewMockNodePoolValidation(testValidationName).WithFailed(
				"QuotaExceeded", "quota exceeded", "Quota exceeded for this subscription.",
			).WithEarliestRetryAfter(nil),
			wantCondition: &metav1.Condition{Status: metav1.ConditionFalse, Reason: "QuotaExceeded", Message: "Quota exceeded for this subscription."},
			wantEnqueue:   false,
		},
		{
			name:    "validation unknown with LogOnly -- condition set to Unknown, requeue still scheduled, no error returned",
			setupDB: defaultSetupDB,
			validation: NewMockNodePoolValidation(testValidationName).WithUnknownLogOnly(
				"TransientIssue", "temporary network blip", "Temporarily unable to verify.",
			),
			wantCondition: &metav1.Condition{Status: metav1.ConditionUnknown, Reason: "TransientIssue", Message: "Temporarily unable to verify."},
			wantEnqueue:   true,
		},
		{
			name:    "validation skipped with no prior condition -- no condition persisted, no requeue",
			setupDB: defaultSetupDB,
			validation: NewMockNodePoolValidation(testValidationName).WithSkipped(
				"NotApplicable", "node pool does not need this check", "Not applicable.",
			),
			wantConditionAbsent: true,
			wantEnqueue:         false,
		},
		{
			name: "validation skipped with prior condition -- condition removed, no requeue",
			setupDB: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				defaultSetupDB(t, ctx, mockDB)
				spnpCRUD := mockDB.ServiceProviderNodePools(testSubscriptionID, testResourceGroup, testClusterName, testNodePoolName)
				spnp, err := spnpCRUD.Get(ctx, coreapi.ServiceProviderNodePoolResourceName)
				require.NoError(t, err)
				spnp.Status.Validations = []metav1.Condition{
					{
						Type:    testValidationName,
						Status:  metav1.ConditionFalse,
						Reason:  "PreviouslyFailed",
						Message: "previously failed",
					},
				}
				_, err = spnpCRUD.Replace(ctx, spnp, nil)
				require.NoError(t, err)
			},
			validation: NewMockNodePoolValidation(testValidationName).WithSkipped(
				"NotApplicable", "node pool does not need this check", "Not applicable.",
			),
			wantConditionAbsent: true,
			wantEnqueue:         false,
		},
		{
			name: "already-succeeded validation -- re-runs and overwrites with Failed",
			setupDB: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				defaultSetupDB(t, ctx, mockDB)
				spnpCRUD := mockDB.ServiceProviderNodePools(testSubscriptionID, testResourceGroup, testClusterName, testNodePoolName)
				spnp, err := spnpCRUD.Get(ctx, coreapi.ServiceProviderNodePoolResourceName)
				require.NoError(t, err)
				spnp.Status.Validations = []metav1.Condition{
					{
						Type:   testValidationName,
						Status: metav1.ConditionTrue,
						Reason: "AsExpected",
					},
				}
				_, err = spnpCRUD.Replace(ctx, spnp, nil)
				require.NoError(t, err)
			},
			validation: NewMockNodePoolValidation(testValidationName).WithFailed(
				"QuotaExceeded", "quota exceeded", "Quota exceeded for this subscription.",
			),
			wantCondition: &metav1.Condition{Status: metav1.ConditionFalse, Reason: "QuotaExceeded", Message: "Quota exceeded for this subscription."},
			wantEnqueue:   true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

			mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
			if tc.setupDB != nil {
				tc.setupDB(t, ctx, mockDB)
			}

			fakeClock := clocktesting.NewFakePassiveClock(fixedNow)
			syncer, enqueuer := newTestSyncer(mockDB, tc.validation, fakeClock)

			err := syncer.SyncOnce(ctx, newTestNodePoolKey())
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			if tc.wantCondition != nil || tc.wantConditionAbsent {
				generation, exists := syncer.lastValidatedUserIntent.Get(newTestNodePoolKey())
				require.True(t, exists, "every completed validation must record its generation")
				assert.Equal(t, int64(0), generation)
			}

			if tc.wantEnqueue {
				require.NotEmpty(t, enqueuer.enqueuedKeys, "expected a requeue to be scheduled")
			} else {
				require.Empty(t, enqueuer.enqueuedKeys, "expected no requeue to be scheduled")
			}

			if tc.wantConditionAbsent {
				spnp, spnpErr := mockDB.ServiceProviderNodePools(
					testSubscriptionID, testResourceGroup, testClusterName, testNodePoolName,
				).Get(ctx, coreapi.ServiceProviderNodePoolResourceName)
				require.NoError(t, spnpErr)

				cond := meta.FindStatusCondition(spnp.Status.Validations, testValidationName)
				assert.Nil(t, cond, "expected validation condition to be absent")
			}

			if tc.wantCondition != nil {
				spnp, spnpErr := mockDB.ServiceProviderNodePools(
					testSubscriptionID, testResourceGroup, testClusterName, testNodePoolName,
				).Get(ctx, coreapi.ServiceProviderNodePoolResourceName)
				require.NoError(t, spnpErr)

				cond := meta.FindStatusCondition(spnp.Status.Validations, testValidationName)
				require.NotNil(t, cond, "expected validation condition to be set")
				assert.Equal(t, tc.wantCondition.Status, cond.Status)
				assert.Equal(t, tc.wantCondition.Reason, cond.Reason)
				assert.Equal(t, tc.wantCondition.Message, cond.Message)
			}
		})
	}
}

// TestNodePoolValidationSyncer_ShouldWriteCondition unit-tests the suppression decision in isolation from
// Cosmos/DB plumbing, covering the boundary cases around maxConsecutiveUnknownsBeforeWrite.
func TestNodePoolValidationSyncer_ShouldWriteCondition(t *testing.T) {
	// shouldWriteCondition only checks previousCondition's nilness, not its Status, so the Status value
	// here is just fixture data.
	storedCondition := &metav1.Condition{Type: testValidationName, Status: metav1.ConditionUnknown}

	testCases := []struct {
		name                string
		previousCondition   *metav1.Condition
		consecutiveUnknowns int
		want                bool
	}{
		{
			name:                "no previously stored condition -- always write, even mid-streak",
			previousCondition:   nil,
			consecutiveUnknowns: 5,
			want:                true,
		},
		{
			name:                "previously stored condition, non-Unknown result (streak reset to 0) -- write",
			previousCondition:   storedCondition,
			consecutiveUnknowns: 0,
			want:                true,
		},
		{
			name:                "previously stored condition, first Unknown in streak -- suppress",
			previousCondition:   storedCondition,
			consecutiveUnknowns: 1,
			want:                false,
		},
		{
			name:                "previously stored condition, streak exactly at threshold -- suppress (boundary)",
			previousCondition:   storedCondition,
			consecutiveUnknowns: maxConsecutiveUnknownsBeforeWrite,
			want:                false,
		},
		{
			name:                "previously stored condition, streak one past threshold -- write (boundary)",
			previousCondition:   storedCondition,
			consecutiveUnknowns: maxConsecutiveUnknownsBeforeWrite + 1,
			want:                true,
		},
	}

	syncer := &nodePoolValidationSyncer{}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, syncer.shouldWriteCondition(tc.previousCondition, tc.consecutiveUnknowns))
		})
	}
}

// TestNodePoolValidationSyncer_TrackConsecutiveUnknowns unit-tests the per-key streak bookkeeping in
// isolation: incrementing across consecutive Unknown results, resetting on a non-Unknown result, and
// tracking each HCPNodePoolKey independently. Each test case is a sequence of steps run against a single
// fresh syncer, asserting the returned count after every step.
func TestNodePoolValidationSyncer_TrackConsecutiveUnknowns(t *testing.T) {
	keyA := newTestNodePoolKey()
	keyB := controllerutils.HCPNodePoolKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroup,
		HCPClusterName:    testClusterName,
		HCPNodePoolName:   "other-nodepool",
	}

	type step struct {
		key    controllerutils.HCPNodePoolKey
		status metav1.ConditionStatus
		want   int
	}

	testCases := []struct {
		name  string
		steps []step
	}{
		{
			name: "increments on consecutive Unknown results",
			steps: []step{
				{key: keyA, status: metav1.ConditionUnknown, want: 1},
				{key: keyA, status: metav1.ConditionUnknown, want: 2},
				{key: keyA, status: metav1.ConditionUnknown, want: 3},
			},
		},
		{
			name: "non-Unknown result resets the streak to 0",
			steps: []step{
				{key: keyA, status: metav1.ConditionUnknown, want: 1},
				{key: keyA, status: metav1.ConditionUnknown, want: 2},
				{key: keyA, status: metav1.ConditionFalse, want: 0},
			},
		},
		{
			name: "streak restarts at 1 after a reset, not continuing the pre-reset count",
			steps: []step{
				{key: keyA, status: metav1.ConditionUnknown, want: 1},
				{key: keyA, status: metav1.ConditionUnknown, want: 2},
				{key: keyA, status: metav1.ConditionTrue, want: 0},
				{key: keyA, status: metav1.ConditionUnknown, want: 1},
			},
		},
		{
			name: "non-Unknown result with no prior streak stays at 0",
			steps: []step{
				{key: keyA, status: metav1.ConditionFalse, want: 0},
			},
		},
		{
			name: "keys are tracked independently",
			steps: []step{
				{key: keyA, status: metav1.ConditionUnknown, want: 1},
				{key: keyA, status: metav1.ConditionUnknown, want: 2},
				{key: keyB, status: metav1.ConditionUnknown, want: 1},
				{key: keyA, status: metav1.ConditionUnknown, want: 3},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			syncer := &nodePoolValidationSyncer{consecutiveUnknownCounts: lru.New(consecutiveUnknownCountsCacheCapacity)}
			for i, s := range tc.steps {
				condition := metav1.Condition{Type: testValidationName, Status: s.status}
				got := syncer.trackConsecutiveUnknowns(s.key, condition)
				assert.Equalf(t, s.want, got, "step %d: key=%s, status=%s", i, s.key.HCPNodePoolName, s.status)
			}
		})
	}
}

// TestNodePoolValidationSyncer_ConsecutiveUnknownSuppression exercises the consecutive-Unknown suppression
// policy end-to-end across repeated SyncOnce calls: a previously stored Failed condition should survive the
// first maxConsecutiveUnknownsBeforeWrite consecutive Unknown results untouched (and skip the Cosmos write
// each time, per the equality.Semantic.DeepEqual guard), then get overwritten with Unknown once the streak
// persists past the threshold.
func TestNodePoolValidationSyncer_ConsecutiveUnknownSuppression(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

	mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
	_, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroup).Create(ctx, newTestCluster(t), nil)
	require.NoError(t, err)
	nodePool := newTestNodePool(t)
	_, err = mockDB.HCPClusters(testSubscriptionID, testResourceGroup).NodePools(testClusterName).Create(ctx, nodePool, nil)
	require.NoError(t, err)
	_, err = mockDB.Subscriptions().Create(ctx, newTestSubscription(), nil)
	require.NoError(t, err)
	_, err = corecosmosstorage.GetOrCreateServiceProviderNodePool(ctx, mockDB, nodePool.ID)
	require.NoError(t, err)

	spnpCRUD := mockDB.ServiceProviderNodePools(testSubscriptionID, testResourceGroup, testClusterName, testNodePoolName)
	spnp, err := spnpCRUD.Get(ctx, coreapi.ServiceProviderNodePoolResourceName)
	require.NoError(t, err)
	spnp.Status.Validations = []metav1.Condition{
		{
			Type:    testValidationName,
			Status:  metav1.ConditionFalse,
			Reason:  "PreviouslyFailed",
			Message: "previously failed",
		},
	}
	_, err = spnpCRUD.Replace(ctx, spnp, nil)
	require.NoError(t, err)

	validation := NewMockNodePoolValidation(testValidationName).WithUnknownLogOnly(
		"InternalError", "failed to reach Azure", "Unable to verify.",
	)

	fakeClock := clocktesting.NewFakePassiveClock(fixedNow)
	syncer, _ := newTestSyncer(mockDB, validation, fakeClock)

	for i := 1; i <= maxConsecutiveUnknownsBeforeWrite; i++ {
		// Advance the clock past the previous attempt's EarliestRetryAfter deadline so SyncOnce doesn't
		// short-circuit on the retryCooldownChecker suppression.
		fakeClock.SetTime(fakeClock.Now().Add(time.Hour))

		before, err := spnpCRUD.Get(ctx, coreapi.ServiceProviderNodePoolResourceName)
		require.NoError(t, err)

		require.NoError(t, syncer.SyncOnce(ctx, newTestNodePoolKey()))

		after, err := spnpCRUD.Get(ctx, coreapi.ServiceProviderNodePoolResourceName)
		require.NoError(t, err)

		cond := meta.FindStatusCondition(after.Status.Validations, testValidationName)
		require.NotNil(t, cond)
		assert.Equalf(t, metav1.ConditionFalse, cond.Status, "attempt %d: previous condition should be preserved", i)
		assert.Equalf(t, "PreviouslyFailed", cond.Reason, "attempt %d: previous condition should be preserved", i)
		assert.Equalf(t, before.CosmosETag, after.CosmosETag, "attempt %d: Cosmos write should have been skipped", i)
	}

	// The next attempt exceeds the threshold, so the Unknown condition finally overwrites the stored one.
	fakeClock.SetTime(fakeClock.Now().Add(time.Hour))

	before, err := spnpCRUD.Get(ctx, coreapi.ServiceProviderNodePoolResourceName)
	require.NoError(t, err)

	require.NoError(t, syncer.SyncOnce(ctx, newTestNodePoolKey()))

	after, err := spnpCRUD.Get(ctx, coreapi.ServiceProviderNodePoolResourceName)
	require.NoError(t, err)

	cond := meta.FindStatusCondition(after.Status.Validations, testValidationName)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionUnknown, cond.Status)
	assert.Equal(t, "InternalError", cond.Reason)
	assert.NotEqual(t, before.CosmosETag, after.CosmosETag, "expected a Cosmos write once the suppression threshold was exceeded")
}

type errorIndexer struct {
	cache.Indexer
}

func (indexer *errorIndexer) GetByKey(string) (interface{}, bool, error) {
	return nil, false, errors.New("informer cache unavailable")
}

func TestNodePoolValidationSyncer_DeleteRecreateDuringCooldown(t *testing.T) {
	for _, absent := range []bool{false, true} {
		name := "deleting"
		if absent {
			name = "absent"
		}
		t.Run(name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
			_, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroup).Create(ctx, newTestCluster(t), nil)
			require.NoError(t, err)
			resource := newTestNodePool(t)
			resource.ServiceProviderProperties.UserIntentGeneration = 5
			resourceClient := mockDB.HCPClusters(testSubscriptionID, testResourceGroup).NodePools(testClusterName)
			resource, err = resourceClient.Create(ctx, resource, nil)
			require.NoError(t, err)
			_, err = mockDB.Subscriptions().Create(ctx, newTestSubscription(), nil)
			require.NoError(t, err)
			_, err = corecosmosstorage.GetOrCreateServiceProviderNodePool(ctx, mockDB, resource.ID)
			require.NoError(t, err)
			indexer := cache.NewIndexer(func(obj interface{}) (string, error) {
				return strings.ToLower(obj.(*coreapi.NodePool).ResourceID.String()), nil
			}, cache.Indexers{})
			require.NoError(t, indexer.Add(resource))
			validation := NewMockNodePoolValidation(testValidationName).WithPassed()
			syncer, enqueuer := newTestSyncer(mockDB, validation, clocktesting.NewFakePassiveClock(fixedNow))
			syncer.nodePoolLister = corelisters.NewNodePoolLister(indexer)
			key := newTestNodePoolKey()
			require.NoError(t, syncer.SyncOnce(ctx, key))
			require.False(t, syncer.retryCooldownChecker.CanSync(ctx, key))
			syncer.consecutiveUnknownCounts.Add(key, 2)
			if absent {
				require.NoError(t, indexer.Delete(resource))
			} else {
				deleting := resource.DeepCopy()
				deletionTime := metav1.NewTime(fixedNow)
				deleting.ServiceProviderProperties.DeletionTimestamp = &deletionTime
				require.NoError(t, indexer.Update(deleting))
			}
			for range 2 {
				require.NoError(t, syncer.SyncOnce(ctx, key))
				_, exists := syncer.lastValidatedUserIntent.Get(key)
				require.False(t, exists)
				_, exists = syncer.consecutiveUnknownCounts.Get(key)
				require.False(t, exists)
				require.True(t, syncer.retryCooldownChecker.CanSync(ctx, key))
				require.Zero(t, syncer.retryCooldownChecker.TimeUntilReady(key))
			}
			require.Empty(t, enqueuer.enqueuedKeys)
			require.NoError(t, resourceClient.Delete(ctx, testNodePoolName))
			mockDB.DeleteDocument(metadataapi.Must(coreapi.ResourceIDToCosmosID(resource.ID)))
			recreated := newTestNodePool(t)
			recreated.ServiceProviderProperties.UserIntentGeneration = 1
			recreated, err = resourceClient.Create(ctx, recreated, nil)
			require.NoError(t, err)
			require.NoError(t, indexer.Update(recreated))
			require.NoError(t, syncer.SyncOnce(ctx, key))
			generation, exists := syncer.lastValidatedUserIntent.Get(key)
			require.True(t, exists)
			assert.Equal(t, int64(1), generation)
			assert.False(t, syncer.retryCooldownChecker.CanSync(ctx, key))
		})
	}
}

func TestNodePoolValidationSyncer_UserIntentCooldown(t *testing.T) {
	for _, testCase := range []struct {
		name              string
		initialGeneration int64
		generation        int64
		cachedGeneration  int64
		cacheError        bool
		forgetValidated   bool
		evictValidated    bool
		wantStatus        metav1.ConditionStatus
	}{
		{name: "internal churn", initialGeneration: 1, generation: 1, cachedGeneration: 1, wantStatus: metav1.ConditionTrue},
		{name: "stale cached generation", initialGeneration: 1, generation: 1, cachedGeneration: 0, wantStatus: metav1.ConditionTrue},
		{name: "new user intent", initialGeneration: 1, generation: 2, cachedGeneration: 2, wantStatus: metav1.ConditionFalse},
		{name: "legacy first user intent", generation: 1, cachedGeneration: 1, wantStatus: metav1.ConditionFalse},
		{name: "live generation newer than cache", generation: 2, cachedGeneration: 1, wantStatus: metav1.ConditionFalse},
		{name: "cache error", generation: 1, cacheError: true, wantStatus: metav1.ConditionTrue},
		{name: "missing history with active cooldown", initialGeneration: 1, generation: 1, cachedGeneration: 1, forgetValidated: true, wantStatus: metav1.ConditionFalse},
		{name: "missing history and cache error", generation: 1, cacheError: true, forgetValidated: true, wantStatus: metav1.ConditionTrue},
		{name: "evicted history with active cooldown", generation: 1, cachedGeneration: 1, evictValidated: true, wantStatus: metav1.ConditionFalse},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
			_, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroup).Create(ctx, newTestCluster(t), nil)
			require.NoError(t, err)
			nodePoolClient := mockDB.HCPClusters(testSubscriptionID, testResourceGroup).NodePools(testClusterName)
			nodePool := newTestNodePool(t)
			nodePool.ServiceProviderProperties.UserIntentGeneration = testCase.initialGeneration
			nodePool, err = nodePoolClient.Create(ctx, nodePool, nil)
			require.NoError(t, err)
			_, err = mockDB.Subscriptions().Create(ctx, newTestSubscription(), nil)
			require.NoError(t, err)
			_, err = corecosmosstorage.GetOrCreateServiceProviderNodePool(ctx, mockDB, nodePool.ID)
			require.NoError(t, err)

			indexer := cache.NewIndexer(func(obj interface{}) (string, error) {
				return strings.ToLower(obj.(*coreapi.NodePool).ResourceID.String()), nil
			}, cache.Indexers{})
			require.NoError(t, indexer.Add(nodePool))
			validation := NewMockNodePoolValidation(testValidationName).WithPassed()
			fakeClock := clocktesting.NewFakePassiveClock(fixedNow)
			syncer, enqueuer := newTestSyncer(mockDB, validation, fakeClock)
			syncer.nodePoolLister = corelisters.NewNodePoolLister(indexer)
			if testCase.evictValidated {
				syncer.lastValidatedUserIntent = lru.New(1)
			}
			key := newTestNodePoolKey()
			require.NoError(t, syncer.SyncOnce(ctx, key))
			require.False(t, syncer.retryCooldownChecker.CanSync(ctx, key))
			generation, exists := syncer.lastValidatedUserIntent.Get(key)
			require.True(t, exists)
			require.Equal(t, testCase.initialGeneration, generation)

			nodePool = nodePool.DeepCopy()
			nodePool.ServiceProviderProperties.UserIntentGeneration = testCase.generation
			nodePool.Properties.ProvisioningState = coreapi.ProvisioningStateSucceeded
			nodePool, err = nodePoolClient.Replace(ctx, nodePool, nil)
			require.NoError(t, err)
			if testCase.cacheError {
				syncer.nodePoolLister = corelisters.NewNodePoolLister(&errorIndexer{Indexer: indexer})
			} else {
				cachedNodePool := nodePool.DeepCopy()
				cachedNodePool.ServiceProviderProperties.UserIntentGeneration = testCase.cachedGeneration
				require.NoError(t, indexer.Update(cachedNodePool))
			}
			if testCase.forgetValidated {
				syncer.lastValidatedUserIntent.Remove(key)
			}
			if testCase.evictValidated {
				otherKey := key
				otherKey.HCPNodePoolName += "-other"
				syncer.lastValidatedUserIntent.Add(otherKey, int64(0))
				_, exists = syncer.lastValidatedUserIntent.Get(key)
				require.False(t, exists, "history must actually be evicted")
			}
			validation.WithFailed("UserIntentInvalid", "invalid intent", "invalid intent")
			require.NoError(t, syncer.SyncOnce(ctx, key))
			providerClient := mockDB.ServiceProviderNodePools(testSubscriptionID, testResourceGroup, testClusterName, testNodePoolName)
			providerNodePool, err := providerClient.Get(ctx, coreapi.ServiceProviderNodePoolResourceName)
			require.NoError(t, err)
			condition := meta.FindStatusCondition(providerNodePool.Status.Validations, testValidationName)
			require.NotNil(t, condition)
			assert.Equal(t, testCase.wantStatus, condition.Status)
			if testCase.wantStatus == metav1.ConditionFalse {
				generation, exists = syncer.lastValidatedUserIntent.Get(key)
				require.True(t, exists)
				assert.Equal(t, testCase.generation, generation)
			}

			validation.WithSkipped("ShouldNotRun", "should not run", "should not run")
			for range 2 {
				enqueuesBefore := len(enqueuer.enqueuedKeys)
				require.NoError(t, syncer.SyncOnce(ctx, key))
				assert.Len(t, enqueuer.enqueuedKeys, enqueuesBefore+1)
				providerNodePool, err = providerClient.Get(ctx, coreapi.ServiceProviderNodePoolResourceName)
				require.NoError(t, err)
				assert.Equal(t, condition, meta.FindStatusCondition(providerNodePool.Status.Validations, testValidationName))
			}
		})
	}
}

// TestNodePoolValidationSyncer_CooldownSuppression verifies that when the
// retryCooldownChecker's cooldown is active for a key, SyncOnce returns
// immediately without performing validation, and schedules a re-enqueue.
func TestNodePoolValidationSyncer_CooldownSuppression(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

	mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
	_, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroup).Create(ctx, newTestCluster(t), nil)
	require.NoError(t, err)
	nodePool := newTestNodePool(t)
	_, err = mockDB.HCPClusters(testSubscriptionID, testResourceGroup).NodePools(testClusterName).Create(ctx, nodePool, nil)
	require.NoError(t, err)
	_, err = mockDB.Subscriptions().Create(ctx, newTestSubscription(), nil)
	require.NoError(t, err)
	_, err = corecosmosstorage.GetOrCreateServiceProviderNodePool(ctx, mockDB, nodePool.ID)
	require.NoError(t, err)

	validation := NewMockNodePoolValidation(testValidationName).WithFailed(
		"ShouldNotRun", "should not run", "should not run",
	)

	fakeClock := clocktesting.NewFakePassiveClock(fixedNow)
	syncer, enqueuer := newTestSyncer(mockDB, validation, fakeClock)

	key := newTestNodePoolKey()
	syncer.lastValidatedUserIntent.Add(key, int64(0))
	syncer.retryCooldownChecker.SetCooldown(key, 60*time.Second)

	err = syncer.SyncOnce(ctx, key)
	require.NoError(t, err, "SyncOnce should return nil when cooldown is active")

	require.NotEmpty(t, enqueuer.enqueuedKeys, "should have re-enqueued after cooldown skip")
	assert.Greater(t, enqueuer.enqueuedDurations[0], time.Duration(0), "enqueue duration should be positive")
}
