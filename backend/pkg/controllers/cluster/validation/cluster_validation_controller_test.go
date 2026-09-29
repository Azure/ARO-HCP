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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/testr"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/lru"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/validationutils"
	"github.com/Azure/ARO-HCP/backend/pkg/validationmetrics"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
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
			name: "already-succeeded validation -- re-runs and overwrites with Failed",
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
	// lock that contract: suppression depends solely on consecutiveUnknowns.
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

type timedValidation struct {
	*MockClusterValidation
	calls  int
	panics bool
}

func (v *timedValidation) Validate(ctx context.Context, _ *coreapi.Subscription, _ *coreapi.Cluster) validationutils.ValidationResult {
	v.calls++
	finish := validationmetrics.StartPhase(ctx, "check_access_call")
	time.Sleep(120 * time.Second)
	if v.panics {
		panic("validation panic")
	}
	finish(nil)
	return v.result
}

type persistenceDB struct {
	corecosmosstorage.ResourcesDBClient
	err    error
	panics bool
}

func (d *persistenceDB) ServiceProviderClusters(subscription, group, cluster string) cosmosstorageutils.ResourceCRUD[coreapi.ServiceProviderCluster, *coreapi.ServiceProviderCluster] {
	return &timedPersistence{ResourceCRUD: d.ResourcesDBClient.ServiceProviderClusters(subscription, group, cluster), err: d.err, panics: d.panics}
}

type timedPersistence struct {
	cosmosstorageutils.ResourceCRUD[coreapi.ServiceProviderCluster, *coreapi.ServiceProviderCluster]
	err    error
	panics bool
}

func (p *timedPersistence) Replace(ctx context.Context, obj *coreapi.ServiceProviderCluster, options *azcosmos.ItemOptions) (*coreapi.ServiceProviderCluster, error) {
	time.Sleep(7 * time.Second)
	if p.panics {
		panic("persistence panic")
	}
	if p.err != nil {
		return nil, p.err
	}
	return p.ResourceCRUD.Replace(ctx, obj, options)
}

func TestValidationTelemetry(t *testing.T) {
	controlPlaneName := (&validationutils.ControlPlaneIdentitiesPermissionsClusterValidation{}).Name()
	dataPlaneName := (&validationutils.DataPlaneIdentitiesPermissionsValidation{}).Name()
	require.Equal(t, "ClusterValidation"+controlPlaneName, validationmetrics.ControlPlaneController)
	require.Equal(t, "ClusterValidation"+dataPlaneName, validationmetrics.DataPlaneController)
	malformed := NewMockClusterValidation(controlPlaneName).WithPassed()
	malformed.result.Outcome.Passed = nil
	for _, tc := range []struct {
		name             string
		validation       *MockClusterValidation
		persistError     error
		prerequisite     bool
		readError        bool
		wantError        bool
		disposition      string
		persistence      string
		outcome          string
		validationPanic  bool
		persistencePanic bool
	}{
		{name: "passed and cooldown", validation: NewMockClusterValidation(controlPlaneName).WithPassed(), disposition: "completed", persistence: "success", outcome: "passed"},
		{name: "failed and cooldown", validation: NewMockClusterValidation(dataPlaneName).WithFailed("Denied", "denied", "Denied."), disposition: "completed", persistence: "success", outcome: "failed"},
		{name: "conflict before cooldown", validation: NewMockClusterValidation(controlPlaneName).WithPassed(), persistError: corecosmosstoragetesting.NewPreconditionFailedError(), disposition: "persist_conflict", persistence: "conflict", outcome: "passed"},
		{name: "persistence error", validation: NewMockClusterValidation(dataPlaneName).WithPassed(), persistError: errors.New("write failed"), disposition: "persist_error", persistence: "error", outcome: "passed", wantError: true},
		{name: "reported unknown", validation: NewMockClusterValidation(controlPlaneName).WithUnknownReportError("Unavailable", "unavailable", "Unavailable."), disposition: "reported_unknown", persistence: "success", outcome: "unknown", wantError: true},
		{name: "invalid result", validation: NewMockClusterValidation(dataPlaneName), disposition: "invalid_result", persistence: "not_attempted", outcome: "invalid_result", wantError: true},
		{name: "recognized outcome with malformed body", validation: malformed, disposition: "invalid_result", persistence: "not_attempted", outcome: "invalid_result", wantError: true},
		{name: "unchanged", validation: NewMockClusterValidation(controlPlaneName).WithSkipped("NotApplicable", "not applicable", "Not applicable."), disposition: "completed", persistence: "unchanged", outcome: "skipped"},
		{name: "prerequisite skip", validation: NewMockClusterValidation(controlPlaneName), prerequisite: true, disposition: "prerequisite_skip"},
		{name: "read error", validation: NewMockClusterValidation(dataPlaneName), readError: true, disposition: "read_error", wantError: true},
		{name: "other validator excluded", validation: NewMockClusterValidation(testValidationName).WithPassed()},
		{name: "other failed validator preserves only original log", validation: NewMockClusterValidation(testValidationName).WithFailed("Denied", "denied", "Denied.")},
		{name: "validation panic", validation: NewMockClusterValidation(controlPlaneName).WithPassed(), validationPanic: true, disposition: "panic", persistence: "not_attempted", outcome: "panic"},
		{name: "persistence panic", validation: NewMockClusterValidation(dataPlaneName).WithPassed(), persistencePanic: true, disposition: "panic", persistence: "error", outcome: "passed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				registry := prometheus.NewPedanticRegistry()
				var logs bytes.Buffer
				controller := "ClusterValidation" + tc.validation.Name()
				logger := newTestClusterKey().AddLoggerValues(logr.FromSlogHandler(slog.NewJSONHandler(&logs, nil))).WithValues(utils.LogValues{}.AddControllerName(controller)...)
				ctx := validationmetrics.WithMetrics(utils.ContextWithLogger(t.Context(), logger), validationmetrics.New(registry))
				baseline, err := registry.Gather()
				require.NoError(t, err)
				ctx = utils.ContextWithControllerName(ctx, "arbitrary-context-name-must-not-be-a-label")
				mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
				if !tc.prerequisite {
					cluster := newTestCluster(t)
					_, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroup).Create(ctx, cluster, nil)
					require.NoError(t, err)
					_, err = corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, mockDB, cluster.ID)
					require.NoError(t, err)
					if !tc.readError {
						_, err = mockDB.Subscriptions().Create(ctx, newTestSubscription(), nil)
						require.NoError(t, err)
					}
				}
				validation := &timedValidation{MockClusterValidation: tc.validation, panics: tc.validationPanic}
				syncer, enqueuer := newTestSyncer(mockDB, validation, clocktesting.NewFakePassiveClock(fixedNow))
				syncer.resourcesDBClient = &persistenceDB{ResourcesDBClient: mockDB, err: tc.persistError, panics: tc.persistencePanic}
				if tc.validationPanic || tc.persistencePanic {
					panicValue := "validation panic"
					if tc.persistencePanic {
						panicValue = "persistence panic"
					}
					require.PanicsWithValue(t, panicValue, func() { _ = syncer.SyncOnce(ctx, newTestClusterKey()) })
				} else {
					err = syncer.SyncOnce(ctx, newTestClusterKey())
				}
				if tc.wantError {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
				}
				wantCalls := 1
				if tc.prerequisite || tc.readError {
					wantCalls = 0
				}
				require.Equal(t, wantCalls, validation.calls)
				cooldown := tc.persistence == "success" || tc.persistence == "unchanged"
				if cooldown {
					require.NoError(t, syncer.SyncOnce(ctx, newTestClusterKey()))
					require.Equal(t, 1, validation.calls, "cooldown must not invoke Validate again")
				} else if tc.persistError != nil || tc.validationPanic || tc.persistencePanic {
					require.True(t, syncer.retryCooldownChecker.CanSync(ctx, newTestClusterKey()), "persistence failures must return before setting cooldown")
					require.Empty(t, enqueuer.enqueuedKeys)
				}

				completionCount := 0
				outcomeCount := 0
				decoder := json.NewDecoder(&logs)
				for decoder.More() {
					var record map[string]any
					require.NoError(t, decoder.Decode(&record))
					if record["msg"] != "Validation completed" {
						if record["msg"] == "Validation outcome" {
							outcomeCount++
							require.Equal(t, tc.validation.Name(), record["validation"])
							wantResult, err := json.Marshal(tc.validation.result)
							require.NoError(t, err)
							gotResult, err := json.Marshal(record["result"])
							require.NoError(t, err)
							require.JSONEq(t, string(wantResult), string(gotResult), "preserve the existing outcome details")
						}
						continue
					}
					completionCount++
					require.Equal(t, strings.ToLower(controller), record["controller_name"])
					require.Equal(t, testSubscriptionID, record["subscription_id"])
					require.NotEmpty(t, record["resource_id"])
					require.Equal(t, tc.validation.Name(), record["validation"])
					require.Equal(t, tc.outcome, record["outcome"])
					require.Equal(t, 120.0, record["duration_seconds"])
					require.Equal(t, tc.persistence, record["persistence_result"])
					durations := record["phase_durations_seconds"].(map[string]any)
					counts := record["phase_call_counts"].(map[string]any)
					require.Equal(t, 120.0, durations["check_access_call"])
					require.Equal(t, 1.0, counts["check_access_call"])
					if tc.persistence != "unchanged" && tc.persistence != "not_attempted" {
						require.Equal(t, 7.0, durations["persist_result"])
						require.Equal(t, 1.0, counts["persist_result"])
					}
				}
				wantOutcomeCount := 0
				if wantCalls > 0 && tc.validation.result.Validate() == nil && tc.validation.result.Outcome.Type != validationutils.OutcomeTypePassed {
					wantOutcomeCount = 1
				}
				require.Equal(t, wantOutcomeCount, outcomeCount)
				if tc.disposition == "" {
					wantCalls = 0 // Unscoped validators must produce no new telemetry.
				}
				require.Equal(t, wantCalls, completionCount)
				families, err := registry.Gather()
				require.NoError(t, err)
				if tc.disposition == "" {
					require.Equal(t, baseline, families, "other validators must not modify initialized telemetry")
					return
				}
				attempts := map[string]float64{}
				histogramCount := uint64(0)
				for _, family := range families {
					for _, metric := range family.Metric {
						labels := map[string]string{}
						for _, label := range metric.Label {
							labels[label.GetName()] = label.GetValue()
						}
						require.Contains(t, []string{validationmetrics.ControlPlaneController, validationmetrics.DataPlaneController}, labels["controller"])
						if labels["controller"] != controller {
							require.Zero(t, metric.GetHistogram().GetSampleCount())
							require.Zero(t, metric.GetCounter().GetValue())
							require.Zero(t, metric.GetGauge().GetValue())
							continue
						}
						switch family.GetName() {
						case "backend_validation_attempts_total":
							require.Len(t, labels, 2)
							if metric.Counter.GetValue() != 0 {
								attempts[labels["disposition"]] = metric.Counter.GetValue()
							}
						case "backend_validation_duration_seconds":
							histogramCount += metric.Histogram.GetSampleCount()
							if labels["outcome"] == tc.outcome {
								require.Equal(t, 120.0, metric.Histogram.GetSampleSum())
							} else {
								require.Zero(t, metric.Histogram.GetSampleCount())
								require.Zero(t, metric.Histogram.GetSampleSum())
							}
						case "backend_validation_phase_duration_seconds":
							if (tc.validationPanic && labels["phase"] == "check_access_call") || (tc.persistencePanic && labels["phase"] == "persist_result") {
								wantCount := uint64(0)
								if labels["result"] == "error" {
									wantCount = 1
								}
								require.Equal(t, wantCount, metric.Histogram.GetSampleCount(), "panicking phase must not report success")
							}
						case "backend_validation_phase_inflight":
							require.Zero(t, metric.Gauge.GetValue())
						}
					}
				}
				wantAttempts := map[string]float64{tc.disposition: 1}
				if cooldown {
					wantAttempts["cooldown"] = 1
				}
				require.Equal(t, wantAttempts, attempts)
				require.Equal(t, uint64(wantCalls), histogramCount)
			})
		})
	}
}
