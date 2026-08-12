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
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	testSubscriptionID = "00000000-0000-0000-0000-000000000000"
	testResourceGroup  = "test-rg"
	testClusterName    = "test-cluster"
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

func newTestClusterKey() controllerutils.HCPClusterKey {
	return controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroup,
		HCPClusterName:    testClusterName,
	}
}

func newTestCluster(t *testing.T) *coreapi.HCPOpenShiftCluster {
	t.Helper()
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroup +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName))
	return &coreapi.HCPOpenShiftCluster{
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

func newTestSubscription() *coreapi.Subscription {
	subResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID))
	return &coreapi.Subscription{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   subResourceID,
			PartitionKey: strings.ToLower(subResourceID.SubscriptionID),
		},
		ResourceID: subResourceID,
		State:      coreapi.SubscriptionStateRegistered,
	}
}

func newTestSyncer(mockDB *corecosmosstoragetesting.MockResourcesDBClient, validation validationutils.ClusterValidation, fakeClock *clocktesting.FakePassiveClock) (*clusterValidationSyncer, *fakeAfterEnqueuer) {
	retryCooldown := controllerutil.NewSettableCooldownChecker()
	retryCooldown.SetClock(fakeClock)
	enqueuer := &fakeAfterEnqueuer{}
	syncer := &clusterValidationSyncer{
		retryCooldownChecker:         retryCooldown,
		enqueueAfter:                 enqueuer,
		resourcesDBClient:            mockDB,
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockDB},
		validation:                   validation,
		consecutiveUnknownCounts:     lru.New(consecutiveUnknownCountsCacheCapacity),
	}
	return syncer, enqueuer
}

func TestClusterValidationSyncer_SyncOnce(t *testing.T) {

	defaultSetupDB := func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
		t.Helper()
		cluster := newTestCluster(t)
		_, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroup).Create(ctx, cluster, nil)
		require.NoError(t, err)
		_, err = mockDB.Subscriptions().Create(ctx, newTestSubscription(), nil)
		require.NoError(t, err)
		_, err = corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, mockDB, cluster.ID)
		require.NoError(t, err)
	}

	testCases := []struct {
		name       string
		setupDB    func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient)
		validation validationutils.ClusterValidation
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
				_, err := mockDB.Subscriptions().Create(ctx, newTestSubscription(), nil)
				require.NoError(t, err)
			},
			validation: NewMockClusterValidation(testValidationName),
		},
		{
			name: "service provider cluster not found -- no-op",
			setupDB: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				_, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroup).Create(ctx, newTestCluster(t), nil)
				require.NoError(t, err)
				_, err = mockDB.Subscriptions().Create(ctx, newTestSubscription(), nil)
				require.NoError(t, err)
			},
			validation: NewMockClusterValidation(testValidationName),
		},
		{
			name:          "validation passes -- condition set to True, no requeue",
			setupDB:       defaultSetupDB,
			validation:    NewMockClusterValidation(testValidationName).WithPassed(),
			wantCondition: &metav1.Condition{Status: metav1.ConditionTrue, Reason: "AsExpected", Message: "As expected."},
			wantEnqueue:   false,
		},
		{
			name:    "validation fails -- condition set to False, requeue scheduled",
			setupDB: defaultSetupDB,
			validation: NewMockClusterValidation(testValidationName).WithFailed(
				"QuotaExceeded", "quota exceeded", "Quota exceeded for this subscription.",
			),
			wantCondition: &metav1.Condition{Status: metav1.ConditionFalse, Reason: "QuotaExceeded", Message: "Quota exceeded for this subscription."},
			wantEnqueue:   true,
		},
		{
			// Covers the "last step of SyncOnce" reporting-policy branch that turns an Unknown result into
			// an error return; see TestClusterValidationSyncer_SyncOnce's sibling case below for the
			// LogOnly branch of the same decision.
			name:    "validation unknown with ReportError -- condition set to Unknown, requeue scheduled, error returned",
			setupDB: defaultSetupDB,
			validation: NewMockClusterValidation(testValidationName).WithUnknownReportError(
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
			validation: NewMockClusterValidation(testValidationName).WithFailed(
				"QuotaExceeded", "quota exceeded", "Quota exceeded for this subscription.",
			).WithEarliestRetryAfter(nil),
			wantCondition: &metav1.Condition{Status: metav1.ConditionFalse, Reason: "QuotaExceeded", Message: "Quota exceeded for this subscription."},
			wantEnqueue:   false,
		},
		{
			name:    "validation unknown with LogOnly -- condition set to Unknown, requeue still scheduled, no error returned",
			setupDB: defaultSetupDB,
			validation: NewMockClusterValidation(testValidationName).WithUnknownLogOnly(
				"TransientIssue", "temporary network blip", "Temporarily unable to verify.",
			),
			wantCondition: &metav1.Condition{Status: metav1.ConditionUnknown, Reason: "TransientIssue", Message: "Temporarily unable to verify."},
			wantEnqueue:   true,
		},
		{
			name:    "validation skipped with no prior condition -- no condition persisted, no requeue",
			setupDB: defaultSetupDB,
			validation: NewMockClusterValidation(testValidationName).WithSkipped(
				"NotApplicable", "cluster does not need this check", "Not applicable.",
			),
			wantConditionAbsent: true,
			wantEnqueue:         false,
		},
		{
			name: "validation skipped with prior condition -- condition removed, no requeue",
			setupDB: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				defaultSetupDB(t, ctx, mockDB)
				spcCRUD := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroup, testClusterName)
				spc, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				spc.Status.Validations = []metav1.Condition{
					{
						Type:    testValidationName,
						Status:  metav1.ConditionFalse,
						Reason:  "PreviouslyFailed",
						Message: "previously failed",
					},
				}
				_, err = spcCRUD.Replace(ctx, spc, nil)
				require.NoError(t, err)
			},
			validation: NewMockClusterValidation(testValidationName).WithSkipped(
				"NotApplicable", "cluster does not need this check", "Not applicable.",
			),
			wantConditionAbsent: true,
			wantEnqueue:         false,
		},
		{
			name: "already-succeeded validation -- skipped",
			setupDB: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				defaultSetupDB(t, ctx, mockDB)
				spcCRUD := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroup, testClusterName)
				spc, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				spc.Status.Validations = []metav1.Condition{
					{
						Type:   testValidationName,
						Status: metav1.ConditionTrue,
						Reason: "AsExpected",
					},
				}
				_, err = spcCRUD.Replace(ctx, spc, nil)
				require.NoError(t, err)
			},
			validation: NewMockClusterValidation(testValidationName).WithFailed(
				"ShouldNotBeCalled", "should not be called", "should not be called",
			),
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

			err := syncer.SyncOnce(ctx, newTestClusterKey())
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			if tc.wantEnqueue {
				require.NotEmpty(t, enqueuer.enqueuedKeys, "expected a requeue to be scheduled")
			} else {
				require.Empty(t, enqueuer.enqueuedKeys, "expected no requeue to be scheduled")
			}

			if tc.wantConditionAbsent {
				spc, spcErr := mockDB.ServiceProviderClusters(
					testSubscriptionID, testResourceGroup, testClusterName,
				).Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.NoError(t, spcErr)

				cond := meta.FindStatusCondition(spc.Status.Validations, testValidationName)
				assert.Nil(t, cond, "expected validation condition to be absent")
			}

			if tc.wantCondition != nil {
				spc, spcErr := mockDB.ServiceProviderClusters(
					testSubscriptionID, testResourceGroup, testClusterName,
				).Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.NoError(t, spcErr)

				cond := meta.FindStatusCondition(spc.Status.Validations, testValidationName)
				require.NotNil(t, cond, "expected validation condition to be set")
				assert.Equal(t, tc.wantCondition.Status, cond.Status)
				assert.Equal(t, tc.wantCondition.Reason, cond.Reason)
				assert.Equal(t, tc.wantCondition.Message, cond.Message)
			}
		})
	}
}

// TestClusterValidationSyncer_ShouldWriteCondition unit-tests the suppression decision in isolation from
// Cosmos/DB plumbing, covering the boundary cases around maxConsecutiveUnknownsBeforeWrite.
func TestClusterValidationSyncer_ShouldWriteCondition(t *testing.T) {
	syncer := &clusterValidationSyncer{}

	t.Run("no previously stored condition -- always write, even mid-streak", func(t *testing.T) {
		assert.True(t, syncer.shouldWriteCondition(nil, 5))
	})

	// shouldWriteCondition only checks previousCondition's nilness, not its Status. Vary Status here to
	// lock that contract: suppression depends solely on consecutiveUnknowns. A prior passed condition is
	// unreachable via SyncOnce (shouldProcess skips it), but the helper must still behave consistently.
	previousConditionFixtures := []struct {
		name      string
		condition *metav1.Condition
	}{
		{
			name:      "Unknown",
			condition: &metav1.Condition{Type: testValidationName, Status: metav1.ConditionUnknown},
		},
		{
			name:      "Failed",
			condition: &metav1.Condition{Type: testValidationName, Status: metav1.ConditionFalse, Reason: "PreviouslyFailed"},
		},
		{
			name:      "Passed",
			condition: &metav1.Condition{Type: testValidationName, Status: metav1.ConditionTrue, Reason: "AsExpected"},
		},
	}

	scenarios := []struct {
		name                string
		consecutiveUnknowns int
		want                bool
	}{
		{
			name:                "non-Unknown result (streak reset to 0) -- write",
			consecutiveUnknowns: 0,
			want:                true,
		},
		{
			name:                "first Unknown in streak -- suppress",
			consecutiveUnknowns: 1,
			want:                false,
		},
		{
			name:                "streak exactly at threshold -- suppress (boundary)",
			consecutiveUnknowns: maxConsecutiveUnknownsBeforeWrite,
			want:                false,
		},
		{
			name:                "streak one past threshold -- write (boundary)",
			consecutiveUnknowns: maxConsecutiveUnknownsBeforeWrite + 1,
			want:                true,
		},
	}

	for _, fixture := range previousConditionFixtures {
		for _, scenario := range scenarios {
			t.Run(fixture.name+" prior, "+scenario.name, func(t *testing.T) {
				assert.Equal(t, scenario.want, syncer.shouldWriteCondition(fixture.condition, scenario.consecutiveUnknowns))
			})
		}
	}
}

// TestClusterValidationSyncer_TrackConsecutiveUnknowns unit-tests the per-key streak bookkeeping in
// isolation: incrementing across consecutive Unknown results, resetting on a non-Unknown result, and
// tracking each HCPClusterKey independently. Each test case is a sequence of steps run against a single
// fresh syncer, asserting the returned count after every step.
func TestClusterValidationSyncer_TrackConsecutiveUnknowns(t *testing.T) {
	keyA := newTestClusterKey()
	keyB := controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroup,
		HCPClusterName:    "other-cluster",
	}

	type step struct {
		key    controllerutils.HCPClusterKey
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
			syncer := &clusterValidationSyncer{consecutiveUnknownCounts: lru.New(consecutiveUnknownCountsCacheCapacity)}
			for i, s := range tc.steps {
				condition := metav1.Condition{Type: testValidationName, Status: s.status}
				got := syncer.trackConsecutiveUnknowns(s.key, condition)
				assert.Equalf(t, s.want, got, "step %d: key=%s, status=%s", i, s.key.HCPClusterName, s.status)
			}
		})
	}
}

// TestClusterValidationSyncer_ConsecutiveUnknownSuppression exercises the consecutive-Unknown suppression
// policy end-to-end across repeated SyncOnce calls: a previously stored Failed condition should survive the
// first maxConsecutiveUnknownsBeforeWrite consecutive Unknown results untouched (and skip the Cosmos write
// each time, per the equality.Semantic.DeepEqual guard), then get overwritten with Unknown once the streak
// persists past the threshold.
func TestClusterValidationSyncer_ConsecutiveUnknownSuppression(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

	mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
	cluster := newTestCluster(t)
	_, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroup).Create(ctx, cluster, nil)
	require.NoError(t, err)
	_, err = mockDB.Subscriptions().Create(ctx, newTestSubscription(), nil)
	require.NoError(t, err)
	_, err = corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, mockDB, cluster.ID)
	require.NoError(t, err)

	spcCRUD := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroup, testClusterName)
	spc, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	spc.Status.Validations = []metav1.Condition{
		{
			Type:    testValidationName,
			Status:  metav1.ConditionFalse,
			Reason:  "PreviouslyFailed",
			Message: "previously failed",
		},
	}
	_, err = spcCRUD.Replace(ctx, spc, nil)
	require.NoError(t, err)

	validation := NewMockClusterValidation(testValidationName).WithUnknownLogOnly(
		"InternalError", "failed to reach Azure", "Unable to verify.",
	)

	fakeClock := clocktesting.NewFakePassiveClock(fixedNow)
	syncer, _ := newTestSyncer(mockDB, validation, fakeClock)

	for i := 1; i <= maxConsecutiveUnknownsBeforeWrite; i++ {
		fakeClock.SetTime(fakeClock.Now().Add(time.Hour))

		before, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
		require.NoError(t, err)

		require.NoError(t, syncer.SyncOnce(ctx, newTestClusterKey()))

		after, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
		require.NoError(t, err)

		cond := meta.FindStatusCondition(after.Status.Validations, testValidationName)
		require.NotNil(t, cond)
		assert.Equalf(t, metav1.ConditionFalse, cond.Status, "attempt %d: previous condition should be preserved", i)
		assert.Equalf(t, "PreviouslyFailed", cond.Reason, "attempt %d: previous condition should be preserved", i)
		assert.Equalf(t, before.CosmosETag, after.CosmosETag, "attempt %d: Cosmos write should have been skipped", i)
	}

	// The next attempt exceeds the threshold, so the Unknown condition finally overwrites the stored one.
	fakeClock.SetTime(fakeClock.Now().Add(time.Hour))

	before, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)

	require.NoError(t, syncer.SyncOnce(ctx, newTestClusterKey()))

	after, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)

	cond := meta.FindStatusCondition(after.Status.Validations, testValidationName)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionUnknown, cond.Status)
	assert.Equal(t, "InternalError", cond.Reason)
	assert.NotEqual(t, before.CosmosETag, after.CosmosETag, "expected a Cosmos write once the suppression threshold was exceeded")
}

// TestClusterValidationSyncer_CooldownSuppression verifies that when the
// retryCooldownChecker's cooldown is active for a key, SyncOnce returns
// immediately without performing validation, and schedules a re-enqueue.
func TestClusterValidationSyncer_CooldownSuppression(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

	mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
	cluster := newTestCluster(t)
	_, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroup).Create(ctx, cluster, nil)
	require.NoError(t, err)
	_, err = mockDB.Subscriptions().Create(ctx, newTestSubscription(), nil)
	require.NoError(t, err)
	_, err = corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, mockDB, cluster.ID)
	require.NoError(t, err)

	validation := NewMockClusterValidation(testValidationName).WithFailed(
		"ShouldNotRun", "should not run", "should not run",
	)

	fakeClock := clocktesting.NewFakePassiveClock(fixedNow)
	syncer, enqueuer := newTestSyncer(mockDB, validation, fakeClock)

	key := newTestClusterKey()
	syncer.retryCooldownChecker.SetCooldown(key, 60*time.Second)

	err = syncer.SyncOnce(ctx, key)
	require.NoError(t, err, "SyncOnce should return nil when cooldown is active")

	require.NotEmpty(t, enqueuer.enqueuedKeys, "should have re-enqueued after cooldown skip")
	assert.Greater(t, enqueuer.enqueuedDurations[0], time.Duration(0), "enqueue duration should be positive")
}

func TestShouldProcess(t *testing.T) {
	tests := []struct {
		name       string
		validation validationutils.ClusterValidation
		conditions []metav1.Condition
		want       bool
	}{
		{
			name:       "no condition exists",
			validation: NewMockClusterValidation("FakeValidation"),
			conditions: nil,
			want:       true,
		},
		{
			name:       "condition is False",
			validation: NewMockClusterValidation("FakeValidation"),
			conditions: []metav1.Condition{
				{Type: "FakeValidation", Status: metav1.ConditionFalse},
			},
			want: true,
		},
		{
			name:       "condition is True, non-keyed validation",
			validation: NewMockClusterValidation("FakeValidation"),
			conditions: []metav1.Condition{
				{Type: "FakeValidation", Status: metav1.ConditionTrue, Message: "Validation succeeded"},
			},
			want: false,
		},
		{
			name:       "condition is True, keyed validation, key matches",
			validation: newMockKeyedValidation("FakeValidation", "mi-resource-id-a"),
			conditions: []metav1.Condition{
				{Type: "FakeValidation", Status: metav1.ConditionTrue, Message: "mi-resource-id-a"},
			},
			want: false,
		},
		{
			name:       "condition is True, keyed validation, key changed",
			validation: newMockKeyedValidation("FakeValidation", "mi-resource-id-b"),
			conditions: []metav1.Condition{
				{Type: "FakeValidation", Status: metav1.ConditionTrue, Message: "mi-resource-id-a"},
			},
			want: true,
		},
		{
			name:       "condition is True, keyed validation, key cleared",
			validation: newMockKeyedValidation("FakeValidation", ""),
			conditions: []metav1.Condition{
				{Type: "FakeValidation", Status: metav1.ConditionTrue, Message: "mi-resource-id-a"},
			},
			want: true,
		},
		{
			name:       "condition is True, keyed validation, key set from empty",
			validation: newMockKeyedValidation("FakeValidation", "mi-resource-id-a"),
			conditions: []metav1.Condition{
				{Type: "FakeValidation", Status: metav1.ConditionTrue, Message: ""},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			syncer := &clusterValidationSyncer{
				validation: tt.validation,
			}
			spc := &coreapi.ServiceProviderCluster{
				Status: coreapi.ServiceProviderClusterStatus{
					Validations: tt.conditions,
				},
			}
			cluster := &coreapi.HCPOpenShiftCluster{}

			got := syncer.shouldProcess(spc, cluster)
			if got != tt.want {
				t.Errorf("shouldProcess() = %v, want %v", got, tt.want)
			}
		})
	}
}

// mockKeyedClusterValidation is a mock that implements both ClusterValidation
// and InputKeyedClusterValidation for testing shouldProcess with InputKey.
type mockKeyedClusterValidation struct {
	MockClusterValidation
	inputKey string
}

var _ validationutils.InputKeyedClusterValidation = (*mockKeyedClusterValidation)(nil)

func newMockKeyedValidation(name, key string) *mockKeyedClusterValidation {
	return &mockKeyedClusterValidation{
		MockClusterValidation: *NewMockClusterValidation(name),
		inputKey:              key,
	}
}

func (m *mockKeyedClusterValidation) InputKey(_ *coreapi.HCPOpenShiftCluster) string {
	return m.inputKey
}
